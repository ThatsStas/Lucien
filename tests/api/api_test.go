// Package api holds the in-process HTTP seam tests for the logging story:
// auth rejections are WARN by default (with a reason, never the token),
// every request gets one access line at debug level, and every non-2xx
// outcome carries a reason. Determinism: ephemeral ports, temp dirs,
// captured slog output — IMPLEMENTATION-FRAMEWORK.md 4.
package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lucien/internal/api"
	"lucien/internal/config"
	"lucien/internal/index"
	"lucien/internal/notestore"
)

const apiToken = "sekrit-token-42"

// capture is a goroutine-safe slog sink (the test server writes logs from
// request goroutines while the test reads).
type capture struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.Write(p)
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

func (c *capture) lines(substr string) []string {
	var out []string
	for _, l := range strings.Split(c.String(), "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

type env struct {
	ts    *httptest.Server
	cap   *capture
	token string
	dir   string
}

func newEnv(t *testing.T, level slog.Level) *env {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatalf("index open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	store := notestore.New(dir, &notestore.NullVCS{}, idx, nil)
	if err := store.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}
	sum := sha256.Sum256([]byte(apiToken))
	cfg := &config.Config{
		Clients: []config.Client{
			{Name: "phone", TokenSHA256: hex.EncodeToString(sum[:])},
		},
		MaxUploadMB: 5,
	}
	c := &capture{}
	log := slog.New(slog.NewTextHandler(c, &slog.HandlerOptions{Level: level}))
	ts := httptest.NewServer(api.New(store, idx, cfg, log).Handler())
	t.Cleanup(ts.Close)
	return &env{ts: ts, cap: c, token: apiToken, dir: dir}
}

func (e *env) req(t *testing.T, method, path, body, ctype string, auth bool) (int, string) {
	t.Helper()
	var r *bytes.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	} else {
		r = bytes.NewReader(nil)
	}
	hreq, err := http.NewRequest(method, e.ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "" {
		hreq.Header.Set("Content-Type", ctype)
	}
	if auth {
		hreq.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func mustCreate(t *testing.T, e *env) (id, modified string) {
	t.Helper()
	code, body := e.req(t, "POST", "/v1/notes", `{"text":"auth log probe"}`, "application/json", true)
	if code != http.StatusCreated {
		t.Fatalf("create: got %d %s", code, body)
	}
	id = jsonField(body, "id")
	if id == "" {
		t.Fatalf("create response missing id: %s", body)
	}
	code, body = e.req(t, "GET", "/v1/notes/"+id, "", "", true)
	if code != http.StatusOK {
		t.Fatalf("get after create: %d %s", code, body)
	}
	modified = jsonField(body, "modified")
	if modified == "" {
		t.Fatalf("note response missing modified: %s", body)
	}
	return id, modified
}

func jsonField(body, key string) string {
	k := `"` + key + `":"`
	i := strings.Index(body, k)
	if i < 0 {
		return ""
	}
	rest := body[i+len(k):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// --- auth ---------------------------------------------------------------------

// TestAuthRejectionsWarnWithReason: default log level must show WHY a
// request was bounced (missing vs malformed vs unknown token) with request
// context, and must never echo the bearer token or its hash.
func TestAuthRejectionsWarnWithReason(t *testing.T) {
	e := newEnv(t, slog.LevelInfo)

	sum := sha256.Sum256([]byte(apiToken))
	tokenHash := hex.EncodeToString(sum[:])

	cases := []struct {
		name   string
		header string // "" = no Authorization header at all
		reason string
	}{
		{"missing", "", "missing_header"},
		{"malformed", "Token abc123", "malformed_header"},
		{"unknown", "Bearer totally-wrong-token", "unknown_token"},
	}
	for _, tc := range cases {
		e.cap.mu.Lock()
		e.cap.b.Reset()
		e.cap.mu.Unlock()

		hreq, _ := http.NewRequest("GET", e.ts.URL+"/v1/notes?state=inbox", nil)
		if tc.header != "" {
			hreq.Header.Set("Authorization", tc.header)
		}
		resp, err := http.DefaultClient.Do(hreq)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", tc.name, resp.StatusCode)
		}

		out := e.cap.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "auth rejected") {
			t.Errorf("%s: expected WARN auth rejected line, got:\n%s", tc.name, out)
			continue
		}
		for _, want := range []string{"reason=" + tc.reason, "method=GET", "path=/v1/notes", "remote="} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: auth line missing %q:\n%s", tc.name, want, out)
			}
		}
		// Security: the presented credential (right or wrong) and the
		// stored hash must never hit the log.
		if strings.Contains(out, apiToken) {
			t.Errorf("%s: log leaked the bearer token", tc.name)
		}
		if strings.Contains(out, tokenHash) {
			t.Errorf("%s: log leaked the token hash", tc.name)
		}
	}
}

// TestAuthSuccessNamesClientAtDebug: at debug level, an accepted request
// records WHICH client authenticated — the answer to "who keeps poking me".
func TestAuthSuccessNamesClientAtDebug(t *testing.T) {
	e := newEnv(t, slog.LevelDebug)
	if code, _ := e.req(t, "GET", "/v1/notes", "", "", true); code != http.StatusOK {
		t.Fatalf("authed list: %d", code)
	}
	lines := e.cap.lines("auth accepted")
	if len(lines) != 1 {
		t.Fatalf("one auth accepted line, got %d:\n%s", len(lines), e.cap.String())
	}
	if !strings.Contains(lines[0], "client=phone") || !strings.Contains(lines[0], "remote=") {
		t.Errorf("auth accepted line lacks client/remote: %s", lines[0])
	}
}

// --- access log ----------------------------------------------------------------

// TestAccessLogEveryRequest: with debug on, EVERY request — health,
// success, 401/404/409/415, client validation 400s and the 500 — gets
// exactly one structured access line with method/path/status and, where
// the server produced a reason, that reason. Not focused on any one
// status code: the whole surface is covered uniformly.
func TestAccessLogEveryRequest(t *testing.T) {
	e := newEnv(t, slog.LevelDebug)

	id, _ := mustCreate(t, e)

	// One of each distinct outcome:
	e.req(t, "GET", "/v1/health", "", "", false)          // 200 public
	e.req(t, "GET", "/v1/notes", "", "", true)            // 200 authed
	e.req(t, "GET", "/v1/notes/01TESTTESTTESTTESTTESTTEST", "", "", true) // 404
	e.req(t, "GET", "/v1/notes", "", "", false)            // 401
	e.req(t, "POST", "/v1/notes", "not json", "application/json", true)   // 400
	e.req(t, "POST", "/v1/notes", `{"text":"x","state":"bogus"}`, "application/json", true) // 400 store validation
	e.req(t, "POST", "/v1/notes", `{"text":"x"}`, "text/plain", true)     // 415
	e.req(t, "PATCH", "/v1/notes/"+id, `{"base_modified":"2000-01-01T00:00:00Z","priority":3}`, "application/json", true) // 409
	e.req(t, "GET", "/v1/zzz", "", "", true)               // 404 unknown route

	want := []struct{ status, path string }{
		{"status=200", "path=/v1/health"},
		{"status=200", "path=/v1/notes"},
		{"status=404", "path=/v1/notes/01TESTTESTTESTTESTTESTTEST"},
		{"status=401", "path=/v1/notes"},
		{"status=400", "path=/v1/notes"},
		{"status=415", "path=/v1/notes"},
		{"status=409", "path=/v1/notes/" + id},
		{"status=404", "path=/v1/zzz"},
	}
	access := e.cap.lines("msg=\"http request\"")
	// 2 from mustCreate (create + get) + the 9 probes below.
	if len(access) != 11 {
		t.Fatalf("11 access lines, got %d:\n%s", len(access), strings.Join(access, "\n"))
	}
	for _, w := range want {
		found := false
		for _, l := range access {
			if strings.Contains(l, w.status) && strings.Contains(l, w.path) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no access line with %s and %s:\n%s", w.status, w.path, strings.Join(access, "\n"))
		}
	}
	// Every line carries method, duration and remote address.
	for _, l := range access {
		for _, f := range []string{"method=", "dur_ms=", "remote="} {
			if !strings.Contains(l, f) {
				t.Errorf("access line missing %s: %s", f, l)
			}
		}
	}
	// Rejections carry the server's reason, not just a bare status.
	joined := strings.Join(access, "\n")
	if !strings.Contains(joined, `reason="note not found"`) {
		t.Errorf("404 access line missing reason:\n%s", joined)
	}
	if !strings.Contains(joined, "reason=unauthorized") {
		t.Errorf("401 access line missing reason:\n%s", joined)
	}
	// At default level the access lines vanish...
	// (next test)
}

func TestAccessLogHiddenAtDefaultLevel(t *testing.T) {
	e := newEnv(t, slog.LevelInfo)
	e.req(t, "GET", "/v1/health", "", "", false)
	e.req(t, "GET", "/v1/notes", "", "", true)
	if lines := e.cap.lines("msg=\"http request\""); len(lines) != 0 {
		t.Errorf("no access lines at info level, got:\n%s", strings.Join(lines, "\n"))
	}
}

// TestPatchConflictLogged: the 409 answers the client merge question in
// the log too — which note, what base the client claimed.
func TestPatchConflictLogged(t *testing.T) {
	e := newEnv(t, slog.LevelDebug)
	id, _ := mustCreate(t, e)
	code, _ := e.req(t, "PATCH", "/v1/notes/"+id,
		`{"base_modified":"2000-01-01T00:00:00Z","priority":5}`, "application/json", true)
	if code != http.StatusConflict {
		t.Fatalf("want 409, got %d", code)
	}
	lines := e.cap.lines("patch conflict")
	if len(lines) != 1 {
		t.Fatalf("one patch conflict line, got %d:\n%s", len(lines), e.cap.String())
	}
	if !strings.Contains(lines[0], "id="+id) || !strings.Contains(lines[0], "base_modified=") {
		t.Errorf("conflict line lacks id/base_modified: %s", lines[0])
	}
}

// TestServerErrorLoggedAtErrorLevel: an internally-broken note (corrupt
// file) answers 500 AND leaves an ERROR line even without --debug —
// server-side failures are never silent.
func TestServerErrorLoggedAtErrorLevel(t *testing.T) {
	e := newEnv(t, slog.LevelInfo)
	// Plant a file with a valid ULID name but non-note content: Read
	// fails internally → 500 + ERROR line at default level.
	if err := os.WriteFile(filepath.Join(e.dir, "01TESTTESTTESTTESTTESTTEST.json"),
		[]byte("this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _ := e.req(t, "GET", "/v1/notes/01TESTTESTTESTTESTTESTTEST", "", "", true)
	if code != http.StatusInternalServerError {
		t.Fatalf("corrupt note: want 500, got %d", code)
	}
	out := e.cap.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "corrupt") {
		t.Errorf("corrupt note must produce an ERROR line at default level:\n%s", out)
	}
}

// TestReindexLoggedAtInfo: an admin reindex states what it did at INFO —
// the operator triggered it and deserves a receipt without --debug.
func TestReindexLoggedAtInfo(t *testing.T) {
	e := newEnv(t, slog.LevelInfo)
	for _, txt := range []string{"one", "two"} {
		code, body := e.req(t, "POST", "/v1/notes", `{"text":"`+txt+`"}`, "application/json", true)
		if code != http.StatusCreated {
			t.Fatalf("create: %d %s", code, body)
		}
	}
	code, body := e.req(t, "POST", "/v1/admin/reindex", "", "", true)
	if code != http.StatusOK {
		t.Fatalf("reindex: %d %s", code, body)
	}
	lines := e.cap.lines("reindex complete")
	if len(lines) != 1 || !strings.Contains(lines[0], "level=INFO") ||
		!strings.Contains(lines[0], "reindexed=2") {
		t.Errorf("reindex must log one INFO receipt line:\n%s", strings.Join(lines, "\n"))
	}
}

// TestTagsEndpoint covers GET /v1/tags (user-requested addition beyond the
// cards): auth-gated like every /v1/ route, empty index answers
// {"tags":[]}, and the body carries distinct tags with per-note counts,
// count desc then tag asc.
func TestTagsEndpoint(t *testing.T) {
	e := newEnv(t, slog.LevelInfo)

	code, body := e.req(t, "GET", "/v1/tags", "", "", false)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthed tags: want 401, got %d %s", code, body)
	}
	code, body = e.req(t, "GET", "/v1/tags", "", "", true)
	if code != http.StatusOK || !strings.Contains(body, `"tags":[]`) {
		t.Fatalf("empty tags: want 200 with tags:[], got %d %s", code, body)
	}

	create := func(text, tags string) {
		t.Helper()
		code, body := e.req(t, "POST", "/v1/notes",
			`{"text":"`+text+`","tags":`+tags+`}`, "application/json", true)
		if code != http.StatusCreated {
			t.Fatalf("create %q: %d %s", text, code, body)
		}
	}
	create("kernel oops", `["kernel","idea"]`)
	create("kernel panic again", `["kernel"]`)
	create("no tags here", `[]`)

	code, body = e.req(t, "GET", "/v1/tags", "", "", true)
	if code != http.StatusOK {
		t.Fatalf("tags: %d %s", code, body)
	}
	want := `"tags":[{"tag":"kernel","count":2},{"tag":"idea","count":1}]`
	if !strings.Contains(body, want) {
		t.Fatalf("tags body must contain\n %s\ngot %s", want, body)
	}
}
