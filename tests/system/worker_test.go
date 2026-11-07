// Worker seam at the system level (IMPLEMENTATION-FRAMEWORK.md 3): the REAL
// backend binary with the REAL async loop, driven through the HTTP API
// against stub_whisper. These tests intentionally touch wall time — they
// prove the production wiring (wake, retry timer, boot recovery) — with
// bounded polls and fast configs, never sleeps.
package system

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lucien/tests/internal/testutil"
)

// startStub boots stub_whisper in a mode and returns its transcription
// endpoint URL (what whisper_url must point at, DESIGN 7.1).
func startStub(t *testing.T, mode string) *testutil.Proc {
	t.Helper()
	bin := testutil.BuildBinary(t, "lucien/tests/stubs/stub_whisper")
	return testutil.Start(t, bin, "--port", "0", "--mode", mode)
}

func setStubMode(t *testing.T, p *testutil.Proc, mode string) {
	t.Helper()
	resp, err := http.Post(p.BaseURL()+"/__mode", "application/json",
		strings.NewReader(`{"mode":"`+mode+`"}`))
	if err != nil {
		t.Fatalf("stub mode: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stub mode %s: status %d", mode, resp.StatusCode)
	}
}

func stubRequestCount(t *testing.T, p *testutil.Proc) int {
	t.Helper()
	resp, err := http.Get(p.BaseURL() + "/__requests")
	if err != nil {
		t.Fatalf("stub requests: %v", err)
	}
	defer resp.Body.Close()
	var reqs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&reqs); err != nil {
		t.Fatalf("stub requests decode: %v", err)
	}
	return len(reqs)
}

// pollNoteFile polls the note JSON on disk (truth source, not the API)
// until cond passes or the bounded deadline fails by name.
func pollNoteFile(t *testing.T, b *backend, id string, timeout time.Duration, what string, cond func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		m, err := os.ReadFile(filepath.Join(b.notesDir, id+".json"))
		if err == nil {
			var got map[string]any
			if json.Unmarshal(m, &got) == nil && cond(got) {
				return got
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s (last: %s)", timeout, what, m)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// eventually is the generic bounded poll for post-commit effects the
// worker performs after its step commit (buffer deletion + its commit):
// the transcript is durable before cleanup, so assertions right after the
// step must poll, never assume (DESIGN 4 crash-equivalence).
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func processingOf(m map[string]any) map[string]any {
	p, _ := m["processing"].(map[string]any)
	return p
}

func entriesOf(m map[string]any) []any {
	e, _ := m["entries"].([]any)
	return e
}

func createAudioNote(t *testing.T, b *backend, data []byte) string {
	t.Helper()
	code, body := b.createMultipart("clip.m4a", data, nil)
	if code != 201 {
		t.Fatalf("audio create: %d %s", code, body)
	}
	return decodeMap(t, body)["id"].(string)
}

// --- happy path: POST audio -> transcript entry, buffers gone, committed ---

func TestSystemAudioPipelineEndToEnd(t *testing.T) {
	stub := startStub(t, "ok")
	b := startBackendWhisper(t, stub.BaseURL()+"/v1/audio/transcriptions",
		"worker_idle_poll_seconds = 1")

	id := createAudioNote(t, b, fixture(t))

	got := pollNoteFile(t, b, id, 20*time.Second, "transcript entry from the async worker",
		func(m map[string]any) bool {
			p := processingOf(m)
			es := entriesOf(m)
			return len(es) == 1 && p["step"] == "tag" && p["status"] == "pending"
		})
	entry := entriesOf(got)[0].(map[string]any)
	if entry["kind"] != "transcript" || entry["text"] != "stub transcript" {
		t.Fatalf("transcript entry wrong: %v", entry)
	}
	// Audio buffers are deleted on success (DESIGN 7.1) — cleanup happens
	// after the step commit, so poll for the eventual end state (rm
	// precedes its commit; asserting at the step-commit instant would
	// race by design).
	eventually(t, 15*time.Second, "buffer deletion and its commit", func() bool {
		return !b.hasFile(t, id+".1.m4a") &&
			countCommits(t, gitLog(t, b.notesDir), "transcribe "+id) == 2
	})
	// git truth: add + step commit + buffer-deletion commit (DESIGN 10).
	log := gitLog(t, b.notesDir)
	if countCommits(t, log, "add note "+id) != 1 {
		t.Fatalf("missing add commit: %v", log)
	}
	// The stub saw exactly the buffer contract.
	resp, err := http.Get(stub.BaseURL() + "/__requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var reqs []map[string]any
	json.NewDecoder(resp.Body).Decode(&reqs)
	if len(reqs) != 1 || reqs[0]["filename"] != id+".1.m4a" {
		t.Fatalf("stub requests: %v", reqs)
	}
	fields := reqs[0]["fields"].(map[string]any)
	if _, ok := fields["language"]; ok {
		t.Fatalf(`language must be omitted for the "auto" default: %v`, fields)
	}
	if fields["response_format"] != "json" {
		t.Fatalf("multipart fields: %v", fields)
	}

	// API view agrees with disk truth.
	code, body := b.get("/v1/notes/" + id)
	if code != 200 {
		t.Fatalf("get: %d", code)
	}
	apiNote := decodeMap(t, body)
	if len(entriesOf(apiNote)) != 1 {
		t.Fatalf("API note not refreshed: %v", apiNote["entries"])
	}
}

// --- retryable failures: backoff then terminal failed at the cap ----------

func TestSystemTranscribeRetriesThenFails(t *testing.T) {
	stub := startStub(t, "error500")
	b := startBackendWhisper(t, stub.BaseURL()+"/v1/audio/transcriptions",
		"backoff_base_seconds = 1",
		"backoff_factor = 1",
		"backoff_cap_seconds = 1",
		"max_retries = 2",
		"worker_idle_poll_seconds = 1")

	id := createAudioNote(t, b, fixture(t))

	got := pollNoteFile(t, b, id, 30*time.Second, "note to reach failed after retries",
		func(m map[string]any) bool { return processingOf(m)["status"] == "failed" })
	p := processingOf(got)
	if p["step"] != "transcribe" {
		t.Fatalf("failed at wrong step: %v", p)
	}
	if p["next_retry_at"] != nil {
		t.Fatalf("failed must clear the retry timer: %v", p["next_retry_at"])
	}
	if le, _ := p["last_error"].(string); !strings.Contains(le, "500") {
		t.Fatalf("last_error: %v", p["last_error"])
	}
	// initial attempt + 2 retries = 3 whisper calls; never more.
	if n := stubRequestCount(t, stub); n != 3 {
		t.Fatalf("want exactly 3 attempts, stub saw %d", n)
	}
	// Failed notes keep their audio (retry is a later phase) and the
	// failed state never committed (only the add commit exists).
	if !b.hasFile(t, id+".1.m4a") {
		t.Fatal("failed note must keep its audio buffer")
	}
	if len(gitLog(t, b.notesDir)) != 1 {
		t.Fatalf("retry bookkeeping must not commit: %v", gitLog(t, b.notesDir))
	}
}

// --- recovery: endpoint heals, pending retry succeeds ----------------------

func TestSystemTranscribeRecoversAfterHeal(t *testing.T) {
	stub := startStub(t, "error500")
	b := startBackendWhisper(t, stub.BaseURL()+"/v1/audio/transcriptions",
		"backoff_base_seconds = 1",
		"backoff_factor = 2",
		"backoff_cap_seconds = 4",
		"max_retries = 10",
		"worker_idle_poll_seconds = 1")

	id := createAudioNote(t, b, fixture(t))

	// Let it fail at least once, then heal the endpoint.
	pollNoteFile(t, b, id, 20*time.Second, "first retryable failure",
		func(m map[string]any) bool {
			p := processingOf(m)
			n, _ := p["retry_count"].(float64)
			return n >= 1 && p["status"] == "pending"
		})
	setStubMode(t, stub, "ok")

	got := pollNoteFile(t, b, id, 30*time.Second, "recovered transcription",
		func(m map[string]any) bool {
			p := processingOf(m)
			return p["step"] == "tag" && p["status"] == "pending" && len(entriesOf(m)) == 1
		})
	if p := processingOf(got); p["retry_count"] != float64(0) || p["last_error"] != nil {
		t.Fatalf("bookkeeping not reset on recovery: %v", p)
	}
}

// --- boot recovery: kill the backend mid-transcription (card script) --------

func TestSystemBootRecoveryAfterCrashMidTranscription(t *testing.T) {
	// delay mode sleeps before answering: a wide, deterministic window in
	// which the note sits claimed/running while "whisper" works.
	bin := testutil.BuildBinary(t, "lucien/tests/stubs/stub_whisper")
	slowStub := testutil.Start(t, bin, "--port", "0", "--mode", "delay", "--delay", "8s")
	b := startBackendWhisper(t, slowStub.BaseURL()+"/v1/audio/transcriptions",
		"backoff_base_seconds = 1",
		"worker_idle_poll_seconds = 1")

	id := createAudioNote(t, b, fixture(t))

	// Wait until the worker has claimed the note (running) — proof the
	// kill lands mid-transcription, not before or after.
	pollNoteFile(t, b, id, 20*time.Second, "note claimed (running) mid-transcription",
		func(m map[string]any) bool {
			p := processingOf(m)
			return p["status"] == "running" && p["step"] == "transcribe"
		})

	// Crash: kill the backend while the transcription is in flight.
	b.proc.Stop()

	// Heal the endpoint, then boot a fresh backend from the same state.
	setStubMode(t, slowStub, "ok")
	b.restart()

	got := pollNoteFile(t, b, id, 30*time.Second, "boot recovery to re-arm and transcribe",
		func(m map[string]any) bool {
			p := processingOf(m)
			return p["step"] == "tag" && p["status"] == "pending" && len(entriesOf(m)) == 1
		})
	if p := processingOf(got); p["status"] != "pending" {
		t.Fatalf("unexpected state after recovery: %v", p)
	}
	eventually(t, 15*time.Second, "buffer cleanup after recovered transcription",
		func() bool { return !b.hasFile(t, id+".1.m4a") })
	if n := stubRequestCount(t, slowStub); n < 2 {
		t.Fatalf("expected the killed request plus the recovered retry, stub saw %d", n)
	}
}
