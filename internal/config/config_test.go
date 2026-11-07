package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// valid returns a complete, minimal, valid TOML config for tests to mutate.
func valid() string {
	return `
bind = "127.0.0.1"
port = 8188
notes_dir = "/tmp/notes"
index_path = "/tmp/notes-index.sqlite"
whisper_url = "http://127.0.0.1:9001/v1/audio/transcriptions"

[[clients]]
name = "phone"
token_sha256 = "` + strings.Repeat("a", 64) + `"

[[clients]]
name = "web"
token_sha256 = "` + strings.Repeat("b", 64) + `"
`
}

func load(t *testing.T, toml string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoad_ValidMinimalAppliesDefaults(t *testing.T) {
	cfg, err := load(t, valid())
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if cfg.Bind != "127.0.0.1" || cfg.Port != 8188 || cfg.NotesDir != "/tmp/notes" {
		t.Errorf("scalars: %+v", cfg)
	}
	if len(cfg.Clients) != 2 || cfg.Clients[0].Name != "phone" {
		t.Errorf("clients: %+v", cfg.Clients)
	}
	// Defaults from DESIGN.md 4 and 3.1.
	if cfg.BackoffBaseSeconds != 60 || cfg.BackoffFactor != 2 ||
		cfg.BackoffCapSeconds != 3600 || cfg.MaxRetries != 10 {
		t.Errorf("backoff defaults: %+v", cfg)
	}
	if cfg.TrashRetentionDays != 30 {
		t.Errorf("trash retention default = %d, want 30", cfg.TrashRetentionDays)
	}
	if cfg.MaxUploadMB != 50 {
		t.Errorf("max upload default = %d, want 50", cfg.MaxUploadMB)
	}
	if cfg.WorkerIdlePollSeconds != 30 {
		t.Errorf("worker idle poll default = %d, want 30", cfg.WorkerIdlePollSeconds)
	}
	if cfg.WhisperLanguage != "auto" {
		t.Errorf("whisper_language default = %q, want auto", cfg.WhisperLanguage)
	}
}

// The phase-1 card's first test: config validation rejects a missing
// required field.
func TestLoad_RejectsMissingRequiredField(t *testing.T) {
	for _, field := range []string{"bind", "notes_dir", "index_path", "whisper_url"} {
		t.Run(field, func(t *testing.T) {
			_, err := load(t, dropLine(valid(), field))
			if err == nil {
				t.Fatalf("config missing %s accepted, want rejection", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error %q does not name the missing field %q", err, field)
			}
		})
	}
}

func TestLoad_RejectsMissingClients(t *testing.T) {
	_, err := load(t, `
bind = "127.0.0.1"
port = 8188
notes_dir = "/tmp/notes"
index_path = "/tmp/idx.sqlite"
whisper_url = "http://127.0.0.1:9001/v1/audio/transcriptions"
`)
	if err == nil || !strings.Contains(err.Error(), "clients") {
		t.Fatalf("empty clients: got %v, want error naming clients", err)
	}
}

func TestLoad_RejectsMalformedClientToken(t *testing.T) {
	_, err := load(t, strings.Replace(valid(), strings.Repeat("a", 64), "deadbeef", 1))
	if err == nil || !strings.Contains(err.Error(), "phone") {
		t.Fatalf("bad token hash: got %v, want error naming client phone", err)
	}
}

func TestLoad_RejectsDuplicateClientNames(t *testing.T) {
	dup := valid() + `
[[clients]]
name = "phone"
token_sha256 = "` + strings.Repeat("c", 64) + `"
`
	if _, err := load(t, dup); err == nil {
		t.Fatal("duplicate client names accepted, want rejection")
	}
}

func TestLoad_RejectsBadWhisperURL(t *testing.T) {
	_, err := load(t, strings.Replace(valid(),
		`"http://127.0.0.1:9001/v1/audio/transcriptions"`, `"not a url"`, 1))
	if err == nil || !strings.Contains(err.Error(), "whisper_url") {
		t.Fatalf("bad whisper_url: got %v, want error naming whisper_url", err)
	}
}

func TestLoad_RejectsBadPort(t *testing.T) {
	_, err := load(t, strings.Replace(valid(), "port = 8188", "port = 70000", 1))
	if err == nil {
		t.Fatal("port 70000 accepted, want rejection")
	}
}

func TestLoad_AllowsPortZeroForTests(t *testing.T) {
	if _, err := load(t, strings.Replace(valid(), "port = 8188", "port = 0", 1)); err != nil {
		t.Fatalf("port 0 (ephemeral, needed by system tests) rejected: %v", err)
	}
}

func TestLoad_RejectsUnknownKey(t *testing.T) {
	_, err := load(t, valid()+"\nnotes_dirr = \"/typo\"\n")
	if err == nil || !strings.Contains(err.Error(), "notes_dirr") {
		t.Fatalf("unknown key accepted: %v, want error naming notes_dirr", err)
	}
}

func TestLoad_RejectsNonsenseBackoff(t *testing.T) {
	src := strings.Replace(valid(),
		`whisper_url = "http://127.0.0.1:9001/v1/audio/transcriptions"`,
		"whisper_url = \"http://127.0.0.1:9001/v1/audio/transcriptions\"\nmax_retries = -1", 1)
	_, err := load(t, src)
	if err == nil || !strings.Contains(err.Error(), "max_retries") {
		t.Fatalf("max_retries=-1 accepted: %v", err)
	}
}

// DESIGN.md 12: TLS fields exist from day one but are unused in v1.
func TestLoad_TLSFieldsPresentButUnused(t *testing.T) {
	src := strings.Replace(valid(),
		`whisper_url = "http://127.0.0.1:9001/v1/audio/transcriptions"`,
		`whisper_url = "http://127.0.0.1:9001/v1/audio/transcriptions"
tls_cert_file = "/etc/lucien/tls.pem"
tls_key_file = "/etc/lucien/tls.key"`, 1)
	cfg, err := load(t, src)
	if err != nil {
		t.Fatalf("TLS fields rejected although spec'd present: %v", err)
	}
	if cfg.TLSCertFile != "/etc/lucien/tls.pem" || cfg.TLSKeyFile != "/etc/lucien/tls.key" {
		t.Errorf("TLS fields not loaded: %+v", cfg)
	}
}

// The shipped example must stay valid against the loader (renamed or
// removed keys would otherwise silently drift).
func TestExampleConfigIsValid(t *testing.T) {
	data, err := os.ReadFile("../../config.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	src := bytes.ReplaceAll(data, []byte("REPLACE_ME_64_HEX"), []byte(strings.Repeat("d", 64)))
	cfg, err := Parse(src)
	if err != nil {
		t.Fatalf("config.example.toml rejected: %v", err)
	}
	if cfg.WhisperLanguage != "auto" || cfg.Port != 8188 {
		t.Errorf("unexpected example values: %+v", cfg)
	}
}

// dropLine removes the "key = ..." line for the given key.
func dropLine(toml, key string) string {
	var keep []string
	for _, line := range strings.Split(toml, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+" =") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}
