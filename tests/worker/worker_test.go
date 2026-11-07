// Package worker_test drives the chain runner against planted notes and the
// whisper adapter against stub_whisper (IMPLEMENTATION-FRAMEWORK.md 5: the
// worker seam). Determinism: fixed clock, Pass() called explicitly, stub on
// an ephemeral port, per-test temp dirs. Wall-clock is touched only by the
// two event-loop tests, and only through bounded polls.
package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"lucien/internal/config"
	"lucien/internal/index"
	"lucien/internal/notestore"
	"lucien/internal/worker"
	"lucien/tests/internal/testutil"
)

type env struct {
	store *notestore.Store
	idx   *index.Index
	vcs   *notestore.NullVCS
	w     *worker.Worker
	now   *time.Time
	stub  *testutil.Proc
	cfg   *config.Config
}

func newEnv(t *testing.T, mode string) *env {
	t.Helper()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := base
	dir := t.TempDir()
	x, err := index.Open(dir + "-index/index.sqlite")
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { x.Close() })
	vcs := &notestore.NullVCS{}
	s := notestore.New(dir, vcs, x, nil)
	s.SetClock(func() time.Time { return now })
	if err := s.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}

	bin := testutil.BuildBinary(t, "lucien/tests/stubs/stub_whisper")
	stub := testutil.Start(t, bin, "--port", "0", "--mode", mode)

	cfg := &config.Config{
		Bind:                  "127.0.0.1",
		NotesDir:              dir,
		IndexPath:             dir + "-index/index.sqlite",
		WhisperURL:            stub.BaseURL() + "/v1/audio/transcriptions",
		WhisperLanguage:       "auto",
		BackoffBaseSeconds:    10,
		BackoffFactor:         4,
		BackoffCapSeconds:     100,
		MaxRetries:            10,
		WorkerIdlePollSeconds: 1,
	}
	w := worker.New(s, x, cfg, nil)
	return &env{store: s, idx: x, vcs: vcs, w: w, now: &now, stub: stub, cfg: cfg}
}

func (e *env) advance(d time.Duration) { *e.now = e.now.Add(d) }

func (e *env) createAudio(t *testing.T, text string) *notestore.Note {
	t.Helper()
	n, err := e.store.Create(notestore.CreateRequest{
		SourceKind: "audio",
		Audio:      strings.NewReader("AUDIO:" + text),
		AudioExt:   "m4a",
	})
	if err != nil {
		t.Fatalf("create audio: %v", err)
	}
	return n
}

// stubRequests returns everything stub_whisper has recorded.
func stubRequests(t *testing.T, p *testutil.Proc) []map[string]any {
	t.Helper()
	resp, err := http.Get(p.BaseURL() + "/__requests")
	if err != nil {
		t.Fatalf("stub requests: %v", err)
	}
	defer resp.Body.Close()
	var reqs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&reqs); err != nil {
		t.Fatalf("decode stub requests: %v", err)
	}
	return reqs
}

func setStubMode(t *testing.T, p *testutil.Proc, mode string) {
	t.Helper()
	resp, err := http.Post(p.BaseURL()+"/__mode", "application/json",
		strings.NewReader(`{"mode":"`+mode+`"}`))
	if err != nil {
		t.Fatalf("set stub mode: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("set stub mode %s: status %d", mode, resp.StatusCode)
	}
}

func poll(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// --- happy path ---------------------------------------------------------------

func TestTranscribeHappyPath(t *testing.T) {
	e := newEnv(t, "ok")
	n := e.createAudio(t, "clip")
	ctx := context.Background()

	if ran := e.w.Pass(ctx); ran != 1 {
		t.Fatalf("pass ran %d, want 1", ran)
	}

	got, err := e.store.Read(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Processing.Step != notestore.StepTag || got.Processing.Status != notestore.StatusPending {
		t.Fatalf("chain did not advance: %+v", got.Processing)
	}
	if got.Processing.RetryCount != 0 || got.Processing.LastError != nil || got.Processing.NextRetryAt != nil {
		t.Fatalf("retry bookkeeping not reset: %+v", got.Processing)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries: %+v", got.Entries)
	}
	if got.Entries[0].Kind != "transcript" || got.Entries[0].Text != "stub transcript" {
		t.Fatalf("entry: %+v", got.Entries[0])
	}
	if !got.Entries[0].TS.Equal(got.Modified) {
		t.Fatalf("entry ts %v not stamped at completion %v", got.Entries[0].TS, got.Modified)
	}
	if bufs, _ := e.store.AudioBuffers(n.ID); len(bufs) != 0 {
		t.Fatalf("audio buffers not deleted on success: %v", bufs)
	}

	// Commits: "add note", the step commit, and the buffer-deletion commit,
	// all with DESIGN.md 10 messages.
	var msgs []string
	for _, c := range e.vcs.Commits {
		msgs = append(msgs, c.Message)
	}
	want := []string{"add note " + n.ID, "transcribe " + n.ID, "transcribe " + n.ID}
	if strings.Join(msgs, "|") != strings.Join(want, "|") {
		t.Fatalf("commits: %v", msgs)
	}
	if last := e.vcs.Commits[len(e.vcs.Commits)-1]; len(last.Paths) != 1 || last.Paths[0] != n.ID+".1.m4a" {
		t.Fatalf("deletion commit paths: %v", last.Paths)
	}

	// The whisper request carried the multipart contract (DESIGN 7.1).
	reqs := stubRequests(t, e.stub)
	if len(reqs) != 1 {
		t.Fatalf("stub saw %d requests", len(reqs))
	}
	if reqs[0]["filename"] != n.ID+".1.m4a" {
		t.Fatalf("filename = %v", reqs[0]["filename"])
	}
	fields, _ := reqs[0]["fields"].(map[string]any)
	if fields["language"] != "auto" || fields["response_format"] != "json" {
		t.Fatalf("multipart fields = %v", fields)
	}
}

func TestTranscribeEmptyLanguageOmitsField(t *testing.T) {
	e := newEnv(t, "ok")
	e.cfg.WhisperLanguage = ""
	e.w = worker.New(e.store, e.idx, e.cfg, nil)
	e.createAudio(t, "clip")
	if ran := e.w.Pass(context.Background()); ran != 1 {
		t.Fatalf("pass ran %d", ran)
	}
	reqs := stubRequests(t, e.stub)
	fields, _ := reqs[0]["fields"].(map[string]any)
	if _, ok := fields["language"]; ok {
		t.Fatalf("language field must be omitted when unset: %v", fields)
	}
	if fields["response_format"] != "json" {
		t.Fatalf("response_format must always be sent: %v", fields)
	}
}

func TestTranscribeMultipleBuffersInOrder(t *testing.T) {
	e := newEnv(t, "ok")
	n := e.createAudio(t, "first")
	// Realistic multi-buffer state: permanent failure keeps the first
	// buffer, a later append adds the second, re-arming transcribe.
	if _, err := e.store.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.FailStep(n.ID, notestore.StepTranscribe, 1, nil, "forced"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.AppendAudio(n.ID, strings.NewReader("AUDIO:second"), "m4a"); err != nil {
		t.Fatal(err)
	}

	if ran := e.w.Pass(context.Background()); ran != 1 {
		t.Fatalf("pass ran %d", ran)
	}
	got, _ := e.store.Read(n.ID)
	if len(got.Entries) != 2 || got.Entries[0].ID == got.Entries[1].ID {
		t.Fatalf("entries: %+v", got.Entries)
	}
	if got.Entries[0].ID != 1 || got.Entries[1].ID != 2 {
		t.Fatalf("entry ids not sequential: %+v", got.Entries)
	}
	reqs := stubRequests(t, e.stub)
	if len(reqs) != 2 {
		t.Fatalf("want 2 whisper calls, got %d", len(reqs))
	}
	if reqs[0]["filename"] != n.ID+".1.m4a" || reqs[1]["filename"] != n.ID+".2.m4a" {
		t.Fatalf("buffers must transcribe in number order: %v %v",
			reqs[0]["filename"], reqs[1]["filename"])
	}
	if bufs, _ := e.store.AudioBuffers(n.ID); len(bufs) != 0 {
		t.Fatalf("buffers not deleted: %v", bufs)
	}
}

// --- failure classification and backoff (DESIGN 4, 7.1) ----------------------

func TestRetryableFailureBackoffThenSuccess(t *testing.T) {
	e := newEnv(t, "error500")
	n := e.createAudio(t, "clip")
	ctx := context.Background()

	e.w.Pass(ctx)
	got, _ := e.store.Read(n.ID)
	if got.Processing.Status != notestore.StatusPending || got.Processing.RetryCount != 1 {
		t.Fatalf("after 1st failure: %+v", got.Processing)
	}
	if got.Processing.NextRetryAt == nil || !got.Processing.NextRetryAt.Equal(e.now.Add(10*time.Second)) {
		t.Fatalf("backoff 1: want now+10s, got %v", got.Processing.NextRetryAt)
	}
	if got.Processing.LastError == nil || !strings.Contains(*got.Processing.LastError, "500") {
		t.Fatalf("last_error: %v", got.Processing.LastError)
	}
	if len(e.vcs.Commits) != 1 {
		t.Fatalf("retry bookkeeping must not commit: %+v", e.vcs.Commits)
	}

	// Not due yet: a pass must not call whisper again.
	e.w.Pass(ctx)
	if reqs := stubRequests(t, e.stub); len(reqs) != 1 {
		t.Fatalf("premature retry: %d requests", len(reqs))
	}

	// Geometric growth (base 10, factor 4, cap 100): 10s, 40s, then the cap.
	for _, want := range []struct {
		delay time.Duration
		at    time.Duration // advance beyond previous
	}{{40 * time.Second, 30 * time.Second}, {100 * time.Second, 70 * time.Second}} {
		e.advance(want.at)
		e.w.Pass(ctx)
		got, _ = e.store.Read(n.ID)
		wantNext := e.now.Add(want.delay)
		if got.Processing.NextRetryAt == nil || !got.Processing.NextRetryAt.Equal(wantNext) {
			t.Fatalf("backoff: want %v, got %v", wantNext, got.Processing.NextRetryAt)
		}
	}
	if got.Processing.RetryCount != 3 {
		t.Fatalf("retry_count = %d, want 3", got.Processing.RetryCount)
	}

	// Recovery: once the endpoint heals, the note transcribes cleanly.
	setStubMode(t, e.stub, "ok")
	e.advance(100 * time.Second)
	e.w.Pass(ctx)
	got, _ = e.store.Read(n.ID)
	if got.Processing.Step != notestore.StepTag || got.Processing.Status != notestore.StatusPending {
		t.Fatalf("no recovery after heal: %+v", got.Processing)
	}
	if len(got.Entries) != 1 || got.Entries[0].Kind != "transcript" {
		t.Fatalf("transcript missing after recovery: %+v", got.Entries)
	}
	if got.Processing.RetryCount != 0 || got.Processing.LastError != nil {
		t.Fatalf("bookkeeping not reset on success: %+v", got.Processing)
	}
}

func TestPermanentFailureStopsChain(t *testing.T) {
	e := newEnv(t, "error422")
	n := e.createAudio(t, "clip")
	ctx := context.Background()

	e.w.Pass(ctx)
	got, _ := e.store.Read(n.ID)
	if got.Processing.Status != notestore.StatusFailed {
		t.Fatalf("4xx must fail permanently, got %+v", got.Processing)
	}
	if got.Processing.NextRetryAt != nil {
		t.Fatalf("permanent failure must not schedule a retry: %v", got.Processing.NextRetryAt)
	}
	if got.Processing.LastError == nil || !strings.Contains(*got.Processing.LastError, "422") {
		t.Fatalf("last_error: %v", got.Processing.LastError)
	}

	// A failed note is never rediscovered: the stub stays quiet.
	e.advance(time.Hour)
	e.w.Pass(ctx)
	if reqs := stubRequests(t, e.stub); len(reqs) != 1 {
		t.Fatalf("failed note rediscovered: %d requests", len(reqs))
	}
}

func TestConnectionErrorIsRetryable(t *testing.T) {
	e := newEnv(t, "refuse")
	n := e.createAudio(t, "clip")
	e.w.Pass(context.Background())
	got, _ := e.store.Read(n.ID)
	if got.Processing.Status != notestore.StatusPending || got.Processing.RetryCount != 1 {
		t.Fatalf("connection error must be retryable, got %+v", got.Processing)
	}
	if got.Processing.NextRetryAt == nil {
		t.Fatal("connection error must schedule a retry")
	}
}

func TestRetryCapThenFailed(t *testing.T) {
	e := newEnv(t, "error500")
	e.cfg.MaxRetries = 2
	e.w = worker.New(e.store, e.idx, e.cfg, nil)
	n := e.createAudio(t, "clip")
	ctx := context.Background()

	statuses := []string{}
	for i := 0; i < 4; i++ {
		e.w.Pass(ctx)
		got, _ := e.store.Read(n.ID)
		statuses = append(statuses, fmt.Sprintf("%s/%d", got.Processing.Status, got.Processing.RetryCount))
		e.advance(time.Duration(300*(i+1)) * time.Second) // always past any backoff (cap 100s)
	}
	// attempt 1..2 retry, attempt 3 exceeds the cap of 2 retries -> failed.
	want := []string{"pending/1", "pending/2", "failed/3", "failed/3"}
	if strings.Join(statuses, "|") != strings.Join(want, "|") {
		t.Fatalf("retry progression: got %v want %v", statuses, want)
	}
	if reqs := stubRequests(t, e.stub); len(reqs) != 3 {
		t.Fatalf("want 3 attempts total, got %d", len(reqs))
	}
}

// --- no-pipeline steps and boot recovery (DESIGN 6 rule 3) --------------------

func TestNoPipelineStepSkippedWithoutHotLoop(t *testing.T) {
	e := newEnv(t, "ok")
	// A text note lands at tag/pending; phase 1 registers no tag pipeline.
	n, err := e.store.Create(notestore.CreateRequest{SourceKind: "text", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	before := *n

	done := make(chan int, 1)
	go func() { done <- e.w.Pass(context.Background()) }()
	var ran int
	select {
	case ran = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Pass did not return: hot loop on unregistered pipeline?")
	}
	if ran != 0 {
		t.Fatalf("unregistered step must not count as work, ran %d", ran)
	}
	got, _ := e.store.Read(n.ID)
	if got.Processing != before.Processing || !got.Modified.Equal(before.Modified) {
		t.Fatalf("skip must not mutate the note: %+v", got.Processing)
	}
	// Still discoverable (not consumed), still zero whisper calls, zero commits.
	if ids, _ := e.idx.DiscoverDue(*e.now, 32); len(ids) != 1 || ids[0] != n.ID {
		t.Fatalf("skipped note must remain discoverable: %v", ids)
	}
	if len(e.vcs.Commits) != 1 {
		t.Fatalf("skip must not commit: %+v", e.vcs.Commits)
	}
	if reqs := stubRequests(t, e.stub); len(reqs) != 0 {
		t.Fatalf("skip must not call whisper: %d", len(reqs))
	}
}

func TestRegisterExtensibilitySeam(t *testing.T) {
	e := newEnv(t, "ok")
	// DESIGN 4 extensibility: a new pipeline is one registration, no core changes.
	e.w.Register(notestore.StepTag, func(ctx context.Context, n *notestore.Note) error {
		_, err := e.store.CompleteStep(n.ID, notestore.StepTag, func(n *notestore.Note) {
			n.Tags = append(n.Tags, notestore.Tag{Name: "auto", By: "agent"})
		}, "tag "+n.ID)
		return err
	})
	n, err := e.store.Create(notestore.CreateRequest{SourceKind: "text", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if ran := e.w.Pass(context.Background()); ran != 1 {
		t.Fatalf("registered step must run, ran %d", ran)
	}
	got, _ := e.store.Read(n.ID)
	if got.Processing.Step != notestore.StepEmbed || len(got.Tags) != 1 || got.Tags[0].Name != "auto" {
		t.Fatalf("registered step did not apply: %+v %+v", got.Processing, got.Tags)
	}
}

func TestBootResetRunningAndBufferSweep(t *testing.T) {
	e := newEnv(t, "ok")

	// Note A: claimed by a previous process; boot must re-arm it.
	a := e.createAudio(t, "a")
	if _, err := e.store.Claim(a.ID); err != nil {
		t.Fatal(err)
	}
	// Note B: crash between the step commit and buffer cleanup leaves
	// buffers behind on a note that already moved past transcribe.
	b := e.createAudio(t, "b")
	if _, err := e.store.Claim(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CompleteStep(b.ID, notestore.StepTranscribe, func(n *notestore.Note) {
		n.Entries = append(n.Entries, notestore.Entry{ID: 1, Kind: "transcript", Text: "done before crash"})
	}, "transcribe "+b.ID); err != nil {
		t.Fatal(err)
	}

	reset := e.w.ResetRunning()
	if reset != 1 {
		t.Fatalf("reset %d notes, want 1", reset)
	}
	gotA, _ := e.store.Read(a.ID)
	if gotA.Processing.Status != notestore.StatusPending || gotA.Processing.Step != notestore.StepTranscribe {
		t.Fatalf("running note not re-armed: %+v", gotA.Processing)
	}
	if bufs, _ := e.store.AudioBuffers(b.ID); len(bufs) != 0 {
		t.Fatalf("stray buffers not swept: %v", bufs)
	}
	last := e.vcs.Commits[len(e.vcs.Commits)-1]
	if last.Message != "transcribe "+b.ID || len(last.Paths) != 1 || last.Paths[0] != b.ID+".1.m4a" {
		t.Fatalf("sweep must commit the deletion: %+v", last)
	}

	// After boot, A transcribes normally; B (at tag, no pipeline) stays put.
	e.w.Pass(context.Background())
	if got, _ := e.store.Read(a.ID); len(got.Entries) != 1 || got.Entries[0].Kind != "transcript" {
		t.Fatalf("re-armed note did not transcribe: %+v", got.Entries)
	}
	if got, _ := e.store.Read(b.ID); got.Processing.Step != notestore.StepTag || len(got.Entries) != 1 {
		t.Fatalf("swept note reprocessed: %+v", got.Processing)
	}
}

// --- event loop (DESIGN 6 rule 2; wall clock, bounded polls) -------------------

func TestStartWakeProcessesNewNote(t *testing.T) {
	e := newEnv(t, "ok")
	e.store.SetClock(time.Now) // the loop path uses wall time like production
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.w.Start(ctx)
	defer e.w.Stop()

	n, err := e.store.Create(notestore.CreateRequest{
		SourceKind: "audio", Audio: strings.NewReader("AUDIO"), AudioExt: "m4a"})
	if err != nil {
		t.Fatal(err)
	}
	poll(t, 15*time.Second, "worker to transcribe the woken note", func() bool {
		got, err := e.store.Read(n.ID)
		return err == nil && len(got.Entries) == 1 && got.Entries[0].Kind == "transcript"
	})
}

func TestLostSignalDegradesToIdlePoll(t *testing.T) {
	e := newEnv(t, "ok")
	e.store.SetClock(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.w.Start(ctx)
	defer e.w.Stop()
	e.w.Register(notestore.StepTag, func(ctx context.Context, n *notestore.Note) error {
		_, err := e.store.CompleteStep(n.ID, notestore.StepTag, func(*notestore.Note) {}, "tag "+n.ID)
		return err
	})

	// Plant a note behind the worker's back: file + index row, no store
	// mutation, so no wake signal is ever delivered (DESIGN 6 rule 2's
	// "a lost signal degrades to a delayed retry, never a stuck note").
	now := time.Now().UTC().Truncate(time.Second)
	n := &notestore.Note{
		ID: ulid.Make().String(), Created: now, Modified: now,
		State: "inbox", StateChangedAt: now,
		Tags: []notestore.Tag{}, Source: notestore.Source{Kind: "text"},
		Processing: notestore.Processing{Step: notestore.StepTag, Status: notestore.StatusPending},
		Entries:    []notestore.Entry{{ID: 1, TS: now, Kind: "text", Text: "planted"}},
	}
	n.Dedup = notestore.Dedup{Resaved: []time.Time{}, ContentHash: notestore.ContentHash(n.Entries)}
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/%s.json", e.cfg.NotesDir, n.ID)
	tmp := path + ".tmp-write"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	if err := e.idx.Upsert(n); err != nil {
		t.Fatal(err)
	}

	poll(t, 15*time.Second, "idle poll to discover the un-signalled note", func() bool {
		got, err := e.store.Read(n.ID)
		return err == nil && got.Processing.Step == notestore.StepEmbed
	})
}
