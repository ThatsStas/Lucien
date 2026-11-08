// Log-narrative system tests: the operator story the logs must tell.
// --debug surfaces mechanics (config dump, per-request access lines,
// whisper calls); failures are visible at default level without it.
package system

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lucien/tests/internal/testutil"
)

// startBackendLogging boots the real binary with the given extra CLI flags
// (-debug and friends). Same config contract as startBackend: ephemeral
// port, temp git notes dir, dead whisper endpoint.
func startBackendLogging(t *testing.T, flags ...string) *backend {
	t.Helper()
	dir := t.TempDir()
	notesDir := filepath.Join(dir, "notes")
	indexDir := filepath.Join(dir, "data")
	sum := sha256.Sum256([]byte(testToken))
	cfgPath := filepath.Join(dir, "lucien.toml")
	cfg := fmt.Sprintf(`bind = "127.0.0.1"
port = 0
notes_dir = %q
index_path = %q
whisper_url = "http://127.0.0.1:9/v1/audio/transcriptions"

[[clients]]
name = "phone"
token_sha256 = %q
`, notesDir, filepath.Join(indexDir, "index.sqlite"), hex.EncodeToString(sum[:]))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := testutil.BuildBinary(t, "lucien/cmd/lucien")
	args := append([]string{"-config", cfgPath}, flags...)
	proc := testutil.Start(t, bin, args...)
	b := &backend{t: t, proc: proc, url: proc.BaseURL(), notesDir: notesDir, dir: dir,
		cfgPath: cfgPath, bin: bin, token: testToken}
	testutil.WaitFor(t, b.url+"/v1/health", 10*time.Second, "backend health")
	return b
}

func TestDebugFlagSurfacesStartupMechanics(t *testing.T) {
	b := startBackendLogging(t, "-debug")

	if len(b.proc.LinesContaining("debug logging enabled")) == 0 {
		t.Errorf("--debug must confirm itself at INFO so operators know they are in verbose mode:\n%s", b.proc.Output())
	}
	dump := b.proc.LinesContaining("resolved config")
	if len(dump) != 1 {
		t.Fatalf("exactly one resolved-config line, got %d:\n%s", len(dump), b.proc.Output())
	}
	line := dump[0]
	for _, want := range []string{"127.0.0.1", "notes", "index.sqlite", "127.0.0.1:9", "phone"} {
		if !strings.Contains(line, want) {
			t.Errorf("config dump missing %q: %s", want, line)
		}
	}
	// The dump lists client NAMES but must never print token hashes.
	sum := sha256.Sum256([]byte(testToken))
	if strings.Contains(b.proc.Output(), hex.EncodeToString(sum[:])) {
		t.Error("config dump leaked a token hash to stderr")
	}
}

func TestDefaultLevelHidesDebugLines(t *testing.T) {
	b := startBackendLogging(t)

	if got := b.proc.LinesContaining("resolved config"); len(got) != 0 {
		t.Errorf("debug lines must stay hidden without --debug:\n%v", got)
	}
	if len(b.proc.LinesContaining("starting")) == 0 {
		t.Errorf("INFO startup line must exist without --debug:\n%s", b.proc.Output())
	}
}

// TestAuthRejectionVisibleWithoutDebug: the operator story that started
// this feature — a device getting 401s — must be explainable from the
// default log alone, including WHY the token bounced.
func TestAuthRejectionVisibleWithoutDebug(t *testing.T) {
	b := startBackendLogging(t)

	b.do("GET", "/v1/notes", nil, "", false) // no Authorization header
	b.do("GET", "/v1/notes", nil, "", true)  // correct token (no noise expected)

	warns := b.proc.LinesContaining("auth rejected")
	if len(warns) != 1 {
		t.Fatalf("exactly one auth rejected line, got %d:\n%s", len(warns), b.proc.Output())
	}
	for _, want := range []string{"level=WARN", "reason=missing_header", "method=GET", "path=/v1/notes"} {
		if !strings.Contains(warns[0], want) {
			t.Errorf("auth line missing %q: %s", want, warns[0])
		}
	}
	if strings.Contains(b.proc.Output(), testToken) {
		t.Error("auth log leaked the bearer token")
	}
}

// TestAccessLinesRequireDebug: with -debug every request gets its access
// line — public health polls included — matched per unique path (the
// harness's own WaitFor-health polls make global counts racy, so pin
// per-request instead).
func TestAccessLinesRequireDebug(t *testing.T) {
	b := startBackendLogging(t, "-debug")

	code, _ := b.do("GET", "/v1/notes/01TESTTESTTESTTESTTESTA1", nil, "", false) // 401
	if code != 401 {
		t.Fatalf("probe1: %d", code)
	}
	code, _ = b.do("GET", "/v1/notes/01TESTTESTTESTTESTTESTA2", nil, "", true) // 404 authed
	if code != 404 {
		t.Fatalf("probe2: %d", code)
	}
	code, _ = b.do("POST", "/v1/notes", strings.NewReader(`{"text":"access probe"}`), "application/json", true) // 201
	if code != 201 {
		t.Fatalf("probe3: %d", code)
	}

	joined := strings.Join(b.proc.LinesContaining("http request"), "\n")
	for _, want := range []string{
		"status=401", "status=404", "status=201",
		"path=/v1/notes/01TESTTESTTESTTESTTESTA1",
		"path=/v1/notes/01TESTTESTTESTTESTTESTA2",
		"method=POST", "dur_ms=", "remote=",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("access lines missing %q:\n%s", want, joined)
		}
	}
	// Public health was logged too (from the harness's readiness poll).
	if !strings.Contains(joined, "path=/v1/health") {
		t.Errorf("health requests must get access lines too:\n%s", joined)
	}
}
