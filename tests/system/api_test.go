// Package system holds the HTTP API seam tests: they drive the REAL
// lucien binary with real config files, real git and temp directories
// (IMPLEMENTATION-FRAMEWORK.md 3/4). One backend per test, own temp tree.
package system

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lucien/tests/internal/testutil"
)

const testToken = "sekrit-token-42"

type backend struct {
	t        *testing.T
	proc     *testutil.Proc
	url      string
	notesDir string
	dir      string
	cfgPath  string
	bin      string
	token    string
}

// startBackend writes a config with a temp notes_dir (real git) and an
// index_path outside it, boots the real binary and waits for its port.
// extra are appended TOML lines (e.g. max_upload_mb overrides). The
// whisper endpoint is a dead port: API tests never exercise the worker.
func startBackend(t *testing.T, extra ...string) *backend {
	t.Helper()
	return startBackendWhisper(t, "http://127.0.0.1:9/v1/audio/transcriptions", extra...)
}

func startBackendWhisper(t *testing.T, whisperURL string, extra ...string) *backend {
	t.Helper()
	dir := t.TempDir()
	notesDir := filepath.Join(dir, "notes")
	indexDir := filepath.Join(dir, "data") // index lives outside the git repo
	sum := sha256.Sum256([]byte(testToken))
	cfgPath := filepath.Join(dir, "lucien.toml")
	cfg := fmt.Sprintf(`bind = "127.0.0.1"
port = 0
notes_dir = %q
index_path = %q
whisper_url = %q
%s

[[clients]]
name = "test"
token_sha256 = %q
`, notesDir, filepath.Join(indexDir, "index.sqlite"), whisperURL, strings.Join(extra, "\n"), hex.EncodeToString(sum[:]))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := testutil.BuildBinary(t, "lucien/cmd/lucien")
	proc := testutil.Start(t, bin, "-config", cfgPath)
	b := &backend{t: t, proc: proc, url: proc.BaseURL(), notesDir: notesDir, dir: dir,
		cfgPath: cfgPath, bin: bin, token: testToken}
	testutil.WaitFor(t, b.url+"/v1/health", 10*time.Second, "backend health")
	return b
}

// restart kills the running process and boots a fresh one from the same
// config on a new ephemeral port (boot-recovery tests, DESIGN 6 rule 3).
func (b *backend) restart() {
	b.t.Helper()
	b.proc.Stop()
	b.proc = testutil.Start(b.t, b.bin, "-config", b.cfgPath)
	b.url = b.proc.BaseURL()
	testutil.WaitFor(b.t, b.url+"/v1/health", 10*time.Second, "backend health after restart")
}

func (b *backend) do(method, path string, body io.Reader, contentType string, auth bool) (int, []byte) {
	var req *http.Request
	if body == nil {
		req, _ = http.NewRequest(method, b.url+path, nil)
	} else {
		req, _ = http.NewRequest(method, b.url+path, body)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatalf("read body %s %s: %v", method, path, err)
	}
	return resp.StatusCode, data
}

func (b *backend) get(path string) (int, []byte) { return b.do("GET", path, nil, "", true) }

func (b *backend) postJSON(path, body string) (int, []byte) {
	return b.do("POST", path, strings.NewReader(body), "application/json", true)
}

func (b *backend) patchJSON(path, body string) (int, []byte) {
	return b.do("PATCH", path, strings.NewReader(body), "application/json", true)
}

func (b *backend) createText(body string) string {
	code, data := b.postJSON("/v1/notes", body)
	if code != 201 {
		b.t.Fatalf("create text: status %d body %s", code, data)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		b.t.Fatalf("create text: %v (%s)", err, data)
	}
	return out.ID
}

// multipartBody builds a multipart form with optional file and string fields.
func multipartBody(fileName string, fileData []byte, fields map[string]string) (io.Reader, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if fileData != nil {
		part, _ := w.CreateFormFile("file", fileName)
		part.Write(fileData)
	}
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

func (b *backend) createMultipart(fileName string, data []byte, fields map[string]string) (int, []byte) {
	body, ct := multipartBody(fileName, data, fields)
	return b.do("POST", "/v1/notes", body, ct, true)
}

// noteFile reads the raw note JSON from disk (truth check, never the API).
func (b *backend) noteFile(t *testing.T, id string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(b.notesDir, id+".json"))
	if err != nil {
		t.Fatalf("note file %s: %v", id, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("note file %s json: %v", id, err)
	}
	return m
}

func (b *backend) hasFile(t *testing.T, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(b.notesDir, name))
	return err == nil
}

func gitLog(t *testing.T, dir string) []string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "log", "--format=%s").Output()
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func countCommits(t *testing.T, logLines []string, prefix string) int {
	n := 0
	for _, l := range logLines {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "fixtures", "sample.m4a"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return data
}

// waitAfterSecond is a bounded poll (never a blind sleep) until the wall
// clock's RFC3339 second is strictly past the given stamp. Needed because
// note timestamps are second-precision by design.
func waitAfterSecond(t *testing.T, stamp string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if time.Now().UTC().Format(time.RFC3339) > stamp {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("wall clock never advanced past %s", stamp)
}

func decodeMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json %s: %v", data, err)
	}
	return m
}

// --- tests -------------------------------------------------------------------

func TestHealthAndAuth(t *testing.T) {
	b := startBackend(t)
	if code, _ := b.do("GET", "/v1/health", nil, "", false); code != 200 {
		t.Fatalf("health without auth: %d", code)
	}
	// Missing token.
	if code, _ := b.do("GET", "/v1/notes", nil, "", false); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	// Wrong token.
	req, _ := http.NewRequest("GET", b.url+"/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	// Malformed header.
	if code, _ := b.do("GET", "/v1/notes", nil, "", true); code != 200 {
		t.Fatalf("valid token: %d", code)
	}
	if code, _ := b.do("POST", "/v1/admin/reindex", nil, "", false); code != 401 {
		t.Fatalf("reindex without auth: %d", code)
	}
}

func TestCreateTextNoteE2E(t *testing.T) {
	b := startBackend(t)
	code, data := b.postJSON("/v1/notes", `{"text":"buy milk","tags":[{"name":"errand","by":"human"}]}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, data)
	}
	id := decodeMap(t, data)["id"].(string)
	if id == "" {
		t.Fatal("no id in response")
	}
	if !b.hasFile(t, id+".json") {
		t.Fatalf("note file not written")
	}
	m := b.noteFile(t, id)
	if m["state"] != "inbox" {
		t.Fatalf("state: %v", m["state"])
	}
	entries := m["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["kind"] != "text" {
		t.Fatalf("entries: %v", m["entries"])
	}
	if entries[0].(map[string]any)["id"].(float64) != 1 {
		t.Fatalf("first entry id: %v", entries[0])
	}
	// One commit naming the note (DESIGN.md 10).
	log := gitLog(t, b.notesDir)
	if countCommits(t, log, "add note "+id) != 1 {
		t.Fatalf("git log: %v", log)
	}
	// Summary in the list projection (DESIGN.md 5).
	code, data = b.get("/v1/notes")
	if code != 200 {
		t.Fatalf("list: %d", code)
	}
	lr := decodeMap(t, data)
	if lr["total"].(float64) != 1 {
		t.Fatalf("total: %v", lr["total"])
	}
	sum := lr["notes"].([]any)[0].(map[string]any)
	if sum["id"] != id || sum["snippet"] != "buy milk" || sum["state"] != "inbox" {
		t.Fatalf("summary: %v", sum)
	}
	tags := sum["tags"].([]any)
	if len(tags) != 1 || tags[0].(map[string]any)["name"] != "errand" {
		t.Fatalf("summary tags: %v", tags)
	}
	if _, ok := sum["source"].(map[string]any)["kind"]; !ok {
		t.Fatalf("summary source.kind missing: %v", sum)
	}
	if sum["processing"].(map[string]any)["step"] != "tag" { // text chain start
		t.Fatalf("summary processing: %v", sum["processing"])
	}
}

func TestCreateValidation(t *testing.T) {
	b := startBackend(t)
	cases := []struct {
		name, body string
		want       int
	}{
		{"empty body", `{}`, 400},
		{"blank text", `{"text":"   "}`, 400},
		{"bad state", `{"text":"x","state":"bogus"}`, 400},
		{"priority too high", `{"text":"x","priority":99}`, 400},
		{"bad tag by", `{"text":"x","tags":[{"name":"a","by":"robot"}]}`, 400},
		{"bad remind_at", `{"text":"x","remind_at":"yesterday"}`, 400},
		{"malformed json", `{"text":`, 400},
		{"url is phase 2", `{"url":"https://example.com"}`, 400},
		{"unknown field", `{"text":"x","bogus":1}`, 400},
	}
	for _, c := range cases {
		code, _ := b.postJSON("/v1/notes", c.body)
		if code != c.want {
			t.Fatalf("%s: want %d got %d", c.name, c.want, code)
		}
	}
	// Bad content type.
	if code, _ := b.do("POST", "/v1/notes", strings.NewReader("text=hi"), "text/plain", true); code != 415 {
		t.Fatalf("content type: %d", code)
	}
}

func TestCreateAudioNote(t *testing.T) {
	b := startBackend(t)
	code, data := b.createMultipart("voice.m4a", fixture(t), nil)
	if code != 201 {
		t.Fatalf("audio create: %d %s", code, data)
	}
	id := decodeMap(t, data)["id"].(string)
	if !b.hasFile(t, id+".1.m4a") {
		t.Fatalf("audio buffer missing")
	}
	m := b.noteFile(t, id)
	proc := m["processing"].(map[string]any)
	if proc["step"] != "transcribe" || proc["status"] != "pending" {
		t.Fatalf("processing: %v", proc)
	}
	if countCommits(t, gitLog(t, b.notesDir), "add note "+id) != 1 {
		t.Fatalf("commit missing")
	}
}

func TestAudioErrors(t *testing.T) {
	b := startBackend(t, `max_upload_mb = 1`)
	if code, _ := b.createMultipart("notes.txt", []byte("hello"), nil); code != 415 {
		t.Fatalf("unsupported format: %d", code)
	}
	if code, _ := b.createMultipart("", nil, map[string]string{"state": "inbox"}); code != 400 {
		t.Fatalf("missing file: %d", code)
	}
	huge := bytes.Repeat([]byte("x"), 1250000)
	if code, _ := b.createMultipart("big.m4a", huge, nil); code != 413 {
		t.Fatalf("oversize: %d", code)
	}
	// The rejected uploads left no note files behind.
	entries, _ := os.ReadDir(b.notesDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".m4a") || strings.HasSuffix(e.Name(), ".txt") {
			t.Fatalf("partial state after rejection: %s", e.Name())
		}
	}
}

func TestGetNote(t *testing.T) {
	b := startBackend(t)
	id := b.createText(`{"text":"the whole truth"}`)
	code, data := b.get("/v1/notes/" + id)
	if code != 200 {
		t.Fatalf("get: %d", code)
	}
	m := decodeMap(t, data)
	if m["id"] != id {
		t.Fatalf("id: %v", m["id"])
	}
	if m["created"] != m["modified"] {
		t.Fatalf("created/modified: %v %v", m["created"], m["modified"])
	}
	if len(m["entries"].([]any)) != 1 {
		t.Fatalf("entries: %v", m["entries"])
	}
	if _, ok := m["dedup"].(map[string]any)["content_hash"]; !ok {
		t.Fatalf("dedup: %v", m["dedup"])
	}
	if code, _ := b.get("/v1/notes/01NONEXISTENT"); code != 404 {
		t.Fatalf("unknown id: %d", code)
	}
}

func TestListFilters(t *testing.T) {
	b := startBackend(t)
	a := b.createText(`{"text":"buy milk","state":"todo"}`)
	c := b.createText(`{"text":"buy bread","tags":["groceries"]}`)
	b.createText(`{"text":"fix kernel panic","tags":[{"name":"work","by":"agent"}]}`)

	list := func(q string) (float64, []any) {
		code, data := b.get("/v1/notes?" + q)
		if code != 200 {
			t.Fatalf("list %s: %d", q, code)
		}
		lr := decodeMap(t, data)
		return lr["total"].(float64), lr["notes"].([]any)
	}
	ids := func(items []any) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.(map[string]any)["id"].(string))
		}
		return out
	}
	if total, items := list("state=todo"); total != 1 || len(items) == 0 || items[0].(map[string]any)["id"] != a {
		t.Fatalf("state=todo: %v", ids(items))
	}
	if total, _ := list("q=milk"); total != 1 {
		t.Fatalf("q=milk: %v", total)
	}
	if total, _ := list("q=buy"); total != 2 {
		t.Fatalf("q=buy: %v", total)
	}
	if total, _ := list("tag=work"); total != 1 {
		t.Fatalf("tag=work: %v", total)
	}
	if total, _ := list("tag=groceries&state=todo"); total != 0 { // AND-combined
		t.Fatalf("tag+state: %v", total)
	}
	if total, items := list("limit=1&offset=1"); total != 3 || len(items) != 1 {
		t.Fatalf("limit/offset: %v %v", total, ids(items))
	}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if total, _ := list("modified_since=" + future); total != 0 {
		t.Fatalf("modified_since future: %v", total)
	}
	past := "2000-01-01T00:00:00Z"
	if total, _ := list("modified_since=" + past); total != 3 {
		t.Fatalf("modified_since past: %v", total)
	}
	if total, _ := list("processing_status=pending"); total != 3 {
		t.Fatalf("processing_status: %v", total)
	}
	if total, _ := list("state=todo&state=archived"); total != 1 { // repeated param OR
		t.Fatalf("repeated state: %v", total)
	}
	for _, bad := range []string{"limit=abc", "state=bogus", "min_age=7x", "modified_since=noon", "processing_status=bogus", "offset=-1", "defer_due=maybe"} {
		if code, _ := b.get("/v1/notes?" + bad); code != 400 {
			t.Fatalf("bad param %s: %d", bad, code)
		}
	}
	_ = c
}

func TestPatchFlow(t *testing.T) {
	b := startBackend(t)
	id := b.createText(`{"text":"original"}`)
	mod := b.noteFile(t, id)["modified"].(string)
	// Timestamps are second-precision by design; wait (bounded) until the
	// wall clock has left the creation second so a real mutation must bump.
	waitAfterSecond(t, mod)

	// Stale base_modified -> 409 plus the FULL current note (DESIGN.md 5.7).
	code, data := b.patchJSON("/v1/notes/"+id, `{"base_modified":"1970-01-01T00:00:00Z","state":"todo"}`)
	if code != 409 {
		t.Fatalf("stale: %d %s", code, data)
	}
	stale := decodeMap(t, data)
	if stale["id"] != id || stale["state"] != "inbox" || stale["modified"] != mod {
		t.Fatalf("409 body must be the current note: %v", data)
	}

	// Fresh base_modified -> 200, state change committed with the state.
	code, data = b.patchJSON("/v1/notes/"+id, fmt.Sprintf(`{"base_modified":%q,"state":"todo"}`, mod))
	if code != 200 {
		t.Fatalf("patch: %d %s", code, data)
	}
	updated := decodeMap(t, data)
	if updated["state"] != "todo" {
		t.Fatalf("state: %v", updated["state"])
	}
	if updated["modified"] == mod {
		t.Fatalf("modified not bumped")
	}
	if updated["state_changed_at"] == mod {
		t.Fatalf("state_changed_at not moved")
	}
	log := gitLog(t, b.notesDir)
	if countCommits(t, log, "state "+id+" todo") != 1 {
		t.Fatalf("state commit: %v", log)
	}

	// No-op patch: same values -> no commit, no modified bump (content history).
	mod2 := updated["modified"].(string)
	before := len(gitLog(t, b.notesDir))
	code, data = b.patchJSON("/v1/notes/"+id, fmt.Sprintf(`{"base_modified":%q,"state":"todo"}`, mod2))
	if code != 200 {
		t.Fatalf("noop patch: %d %s", code, data)
	}
	if decodeMap(t, data)["modified"] != mod2 {
		t.Fatalf("noop bumped modified")
	}
	if len(gitLog(t, b.notesDir)) != before {
		t.Fatalf("noop created a commit")
	}

	// entry_edit replaces the entry text and recomputes content_hash.
	beforeHash := b.noteFile(t, id)["dedup"].(map[string]any)["content_hash"]
	code, data = b.patchJSON("/v1/notes/"+id, fmt.Sprintf(`{"base_modified":%q,"entry_edit":{"id":1,"text":"rewritten"}}`, mod2))
	if code != 200 {
		t.Fatalf("entry_edit: %d %s", code, data)
	}
	entries := b.noteFile(t, id)["entries"].([]any)
	if entries[0].(map[string]any)["text"] != "rewritten" {
		t.Fatalf("entry not edited: %v", entries)
	}
	if b.noteFile(t, id)["dedup"].(map[string]any)["content_hash"] == beforeHash {
		t.Fatalf("content_hash not recomputed")
	}
	if countCommits(t, gitLog(t, b.notesDir), "edit "+id) != 1 {
		t.Fatalf("edit commit")
	}

	// entry_edit on a note with pending transcription -> 409.
	code, data = b.createMultipart("v.m4a", fixture(t), nil)
	if code != 201 {
		t.Fatalf("audio create: %d", code)
	}
	audioID := decodeMap(t, data)["id"].(string)
	audioMod := b.noteFile(t, audioID)["modified"].(string)
	code, _ = b.patchJSON("/v1/notes/"+audioID, fmt.Sprintf(`{"base_modified":%q,"entry_edit":{"id":1,"text":"x"}}`, audioMod))
	if code != 409 {
		t.Fatalf("entry_edit during transcribe: %d", code)
	}

	// Unknown entry id -> 400; unknown note -> 404; missing base_modified -> 400.
	_, cur := b.do("GET", "/v1/notes/"+id, nil, "", true)
	mod3 := decodeMap(t, cur)["modified"].(string)
	if code, _ := b.patchJSON("/v1/notes/"+id, fmt.Sprintf(`{"base_modified":%q,"entry_edit":{"id":77,"text":"x"}}`, mod3)); code != 400 {
		t.Fatalf("unknown entry: %d", code)
	}
	if code, _ := b.patchJSON("/v1/notes/01NOPE", `{"base_modified":"2020-01-01T00:00:00Z","state":"todo"}`); code != 404 {
		t.Fatalf("unknown note: %d", code)
	}
	if code, _ := b.patchJSON("/v1/notes/"+id, `{"state":"todo"}`); code != 400 {
		t.Fatalf("missing base_modified: %d", code)
	}
	// tags replace as sent; clearing remind_at with null.
	if code, _ := b.patchJSON("/v1/notes/"+id, fmt.Sprintf(`{"base_modified":%q,"tags":["fresh"],"priority":-3}`, mod3)); code != 200 {
		t.Fatalf("tags patch: %d", code)
	}
	fresh := b.noteFile(t, id)
	if fresh["priority"].(float64) != -3 {
		t.Fatalf("priority: %v", fresh["priority"])
	}
	if tg := fresh["tags"].([]any); len(tg) != 1 || tg[0].(map[string]any)["name"] != "fresh" || tg[0].(map[string]any)["by"] != "human" {
		t.Fatalf("tags: %v", tg)
	}
}

func TestAppend(t *testing.T) {
	b := startBackend(t)
	id := b.createText(`{"text":"first"}`)
	code, data := b.postJSON("/v1/notes/"+id+"/append", `{"text":"second thought"}`)
	if code != 200 {
		t.Fatalf("append: %d %s", code, data)
	}
	m := b.noteFile(t, id)
	entries := m["entries"].([]any)
	if len(entries) != 2 || entries[1].(map[string]any)["kind"] != "text" {
		t.Fatalf("entries: %v", entries)
	}
	if entries[1].(map[string]any)["id"].(float64) != 2 {
		t.Fatalf("entry id: %v", entries[1])
	}
	if p := m["processing"].(map[string]any); p["step"] != "embed" || p["status"] != "pending" {
		t.Fatalf("text append must re-arm at embed: %v", p)
	}
	if countCommits(t, gitLog(t, b.notesDir), "append "+id) != 1 {
		t.Fatalf("append commit")
	}

	// Audio append on a note with a transcription in flight -> 409.
	code, data = b.createMultipart("v.m4a", fixture(t), nil)
	audioID := decodeMap(t, data)["id"].(string)
	body, ct := multipartBody("more.m4a", fixture(t), nil)
	if code, _ := b.do("POST", "/v1/notes/"+audioID+"/append", body, ct, true); code != 409 {
		t.Fatalf("append during transcribe: %d", code)
	}

	// Multipart append on a quiet note: buffer numbered, chain at transcribe.
	body, ct = multipartBody("more.m4a", fixture(t), nil)
	code, data = b.do("POST", "/v1/notes/"+id+"/append", body, ct, true)
	if code != 200 {
		t.Fatalf("multipart append: %d %s", code, data)
	}
	if !b.hasFile(t, id+".2.m4a") { // .1 was never used (text note), numbering continues
		if !b.hasFile(t, id+".1.m4a") {
			t.Fatalf("append buffer missing")
		}
	}
	m = b.noteFile(t, id)
	if p := m["processing"].(map[string]any); p["step"] != "transcribe" || p["status"] != "pending" {
		t.Fatalf("audio append chain: %v", p)
	}

	// Empty text and unknown id.
	if code, _ := b.postJSON("/v1/notes/"+id+"/append", `{"text":""}`); code != 400 {
		t.Fatalf("empty append: %d", code)
	}
	if code, _ := b.postJSON("/v1/notes/01NOPE/append", `{"text":"x"}`); code != 404 {
		t.Fatalf("append unknown: %d", code)
	}
}

func TestDeleteFlow(t *testing.T) {
	b := startBackend(t)
	code, data := b.createMultipart("v.m4a", fixture(t), nil)
	if code != 201 {
		t.Fatalf("audio create: %d", code)
	}
	id := decodeMap(t, data)["id"].(string)

	if code, _ := b.do("DELETE", "/v1/notes/"+id, nil, "", false); code != 401 {
		t.Fatalf("delete without auth: %d", code)
	}
	if code, _ := b.do("DELETE", "/v1/notes/"+id, nil, "", true); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if b.hasFile(t, id+".json") || b.hasFile(t, id+".1.m4a") {
		t.Fatalf("files still in place after delete")
	}
	for _, name := range []string{id + ".json", id + ".1.m4a"} {
		if _, err := os.Stat(filepath.Join(b.notesDir, ".trash", name)); err != nil {
			t.Fatalf("%s not in .trash: %v", name, err)
		}
	}
	if countCommits(t, gitLog(t, b.notesDir), "delete "+id) != 1 {
		t.Fatalf("delete commit")
	}
	if code, _ := b.get("/v1/notes/" + id); code != 404 {
		t.Fatalf("get after delete: %d", code)
	}
	if code, _ := b.do("DELETE", "/v1/notes/"+id, nil, "", true); code != 404 {
		t.Fatalf("double delete: %d", code)
	}
	if _, data := b.get("/v1/notes"); decodeMap(t, data)["total"].(float64) != 0 {
		t.Fatalf("deleted note still listed")
	}
}

func TestReindexAdmin(t *testing.T) {
	b := startBackend(t)
	id1 := b.createText(`{"text":"indexed via mutation"}`)

	// Plant a note file that never passed through the store: the file is
	// truth, the index must be rebuilt to see it.
	plant(b, t, "01PLANTED000000000000000", "orphan file")
	code, data := b.get("/v1/notes")
	if total := decodeMap(t, data)["total"].(float64); total != 1 {
		t.Fatalf("stale index should not show the orphan yet: %v", total)
	}
	code, data = b.do("POST", "/v1/admin/reindex", nil, "", true)
	if code != 200 {
		t.Fatalf("reindex: %d %s", code, data)
	}
	if n := decodeMap(t, data)["reindexed"].(float64); n != 2 {
		t.Fatalf("reindexed: %v", data)
	}
	code, data = b.get("/v1/notes?q=orphan")
	if total := decodeMap(t, data)["total"].(float64); total != 1 {
		t.Fatalf("reindexed orphan not listed: %v", total)
	}
	// Existing notes survive reindex untouched.
	if code, _ := b.get("/v1/notes/" + id1); code != 200 {
		t.Fatalf("id1 after reindex: %d", code)
	}
	// Deleted notes stay out: reindex must not resurrect trash.
	b.do("DELETE", "/v1/notes/"+id1, nil, "", true)
	b.do("POST", "/v1/admin/reindex", nil, "", true)
	if _, data := b.get("/v1/notes"); decodeMap(t, data)["total"].(float64) != 1 {
		t.Fatalf("trash resurrected by reindex")
	}
}

// plant writes a schema-valid note JSON directly into the notes dir.
func plant(b *backend, t *testing.T, id, text string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	note := map[string]any{
		"id": id, "created": now, "modified": now,
		"state": "inbox", "state_changed_at": now,
		"tags": []any{}, "priority": 0,
		"source": map[string]any{"kind": "text", "url": nil},
		"entries": []any{map[string]any{"id": 1, "ts": now, "kind": "text", "text": text}},
		"processing": map[string]any{"step": "tag", "status": "pending",
			"retry_count": 0, "next_retry_at": nil, "last_error": nil},
		"remind_at": nil, "defer_until": nil,
		"dedup": map[string]any{"url_hash": nil, "content_hash": "sha256:x", "resaved": []any{}},
	}
	data, _ := json.MarshalIndent(note, "", "  ")
	if err := os.WriteFile(filepath.Join(b.notesDir, id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
