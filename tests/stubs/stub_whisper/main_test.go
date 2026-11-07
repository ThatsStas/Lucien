package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lucien/tests/internal/testutil"
)

// postAudio posts a multipart transcription request like the whisper adapter
// will, and returns status + decoded body text.
func postAudio(t *testing.T, url string, fields map[string]string) (int, map[string]string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "sample.m4a")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("fake-audio-bytes"))
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()
	resp, err := http.Post(url, mw.FormDataContentType(), &body)
	if err != nil {
		return -1, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]string{}
	json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func requests(t *testing.T, url string) []Request {
	t.Helper()
	resp, err := http.Get(url + "/__requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var reqs []Request
	if err := json.NewDecoder(resp.Body).Decode(&reqs); err != nil {
		t.Fatalf("decode /__requests: %v", err)
	}
	return reqs
}

func newServer(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func TestStubWhisper_OKRecordsRequest(t *testing.T) {
	s := &Server{Mode: "ok", Transcript: "hello world"}
	ts := newServer(t, s)

	code, body := postAudio(t, ts.URL+"/v1/audio/transcriptions", map[string]string{"language": "en"})
	if code != 200 || body["text"] != "hello world" {
		t.Fatalf("ok mode: got %d %v, want 200 {text:hello world}", code, body)
	}

	reqs := requests(t, ts.URL)
	if len(reqs) != 1 {
		t.Fatalf("want 1 recorded request, got %d", len(reqs))
	}
	got := reqs[0]
	if got.Filename != "sample.m4a" || got.Size != len("fake-audio-bytes") {
		t.Errorf("recorded filename/size: %q/%d", got.Filename, got.Size)
	}
	// sha256 of the exact bytes posted, computed independently.
	sum := sha256.Sum256([]byte("fake-audio-bytes"))
	const want = "cd6b559f0d67ac8ed683ecd12b508031a69b4d823a166ae73a6e2d38d0820282"
	if got.SHA256 != hex.EncodeToString(sum[:]) || got.SHA256 != want {
		t.Errorf("recorded sha256 = %s, want %s", got.SHA256, want)
	}
	if got.Fields["language"] != "en" {
		t.Errorf("recorded fields = %v, want language=en", got.Fields)
	}
}

func TestStubWhisper_FailureModes(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"error500", "500"},
		{"error422", "422"},
	} {
		s := &Server{Mode: tc.mode}
		ts := newServer(t, s)
		code, _ := postAudio(t, ts.URL+"/v1/audio/transcriptions", nil)
		if want := mustAtoi(tc.want); code != want {
			t.Errorf("mode %s: got %d, want %d", tc.mode, code, want)
		}
		// Failed calls must still be recorded for assertions.
		if n := len(requests(t, ts.URL)); n != 1 {
			t.Errorf("mode %s: want 1 recorded request, got %d", tc.mode, n)
		}
	}
}

func TestStubWhisper_RefuseClosesConnection(t *testing.T) {
	s := &Server{Mode: "refuse"}
	ts := newServer(t, s)
	_, body := postAudio(t, ts.URL+"/v1/audio/transcriptions", nil)
	// postAudio returns -1 status on transport error (no response at all).
	if body == nil {
		return // connection killed before a response: the emulation works
	}
	t.Fatalf("refuse mode: expected no response, got body %v", body)
}

func TestStubWhisper_MissingFileIs422(t *testing.T) {
	s := &Server{Mode: "ok"}
	ts := newServer(t, s)
	resp, err := http.Post(ts.URL+"/v1/audio/transcriptions", "multipart/form-data; boundary=x", strings.NewReader("--x--"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("missing file: got %d, want 422", resp.StatusCode)
	}
}

func TestStubWhisper_DelayModeHonored(t *testing.T) {
	s := &Server{Mode: "delay", Transcript: "slow", Delay: 80 * time.Millisecond}
	ts := newServer(t, s)
	start := time.Now()
	code, body := postAudio(t, ts.URL+"/v1/audio/transcriptions", nil)
	elapsed := time.Since(start)
	if code != 200 || body["text"] != "slow" {
		t.Fatalf("delay mode: got %d %v", code, body)
	}
	if elapsed < 60*time.Millisecond {
		t.Fatalf("delay mode returned in %v, want >= ~80ms", elapsed)
	}
}

func TestStubWhisper_Health(t *testing.T) {
	s := &Server{Mode: "ok"}
	ts := newServer(t, s)
	resp, err := http.Get(ts.URL + "/v1/audio/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health: got %d, want 200", resp.StatusCode)
	}
}

func TestStubWhisper_RecordFileAndRealBinary(t *testing.T) {
	bin := testutil.BuildBinary(t, "lucien/tests/stubs/stub_whisper")
	dir := t.TempDir()
	recFile := filepath.Join(dir, "requests.ndjson")
	proc := testutil.Start(t, bin, "--port", "0", "--mode", "ok",
		"--transcript", "from binary", "--record", recFile)

	code, body := postAudio(t, proc.BaseURL()+"/v1/audio/transcriptions", nil)
	if code != 200 || body["text"] != "from binary" {
		t.Fatalf("binary ok mode: got %d %v", code, body)
	}
	proc.Stop()

	data, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatalf("record file: %v", err)
	}
	var rec Request
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("record line %q: %v", data, err)
	}
	if rec.Filename != "sample.m4a" {
		t.Errorf("record file entry: %+v", rec)
	}
}

func mustAtoi(s string) int {
	var n int
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
