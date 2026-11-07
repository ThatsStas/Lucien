// Package config loads and validates the single typed configuration file
// (DESIGN.md 12). It is read once at startup and injected into constructors;
// nothing re-reads it at runtime.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Client is one bearer-token holder (phone, web, agent, ribbon).
type Client struct {
	Name        string `toml:"name"`
	TokenSHA256 string `toml:"token_sha256"`
}

// Config is the full typed configuration. Fields absent in TOML take the
// documented defaults; a zero value in the file means "unset" (so, e.g.,
// an explicit max_retries=0 is not expressible — 0 means the default 10).
type Config struct {
	// API binding. Port 0 means ephemeral (used by system tests).
	Bind string `toml:"bind"`
	Port int    `toml:"port"`

	// Storage. NotesDir is the git-versioned notes repository; IndexPath
	// is the derived SQLite cache, kept OUTSIDE NotesDir so it never
	// pollutes the notes git repo (DESIGN 3.1 shows it sibling to notes/
	// without naming a parent — config value settles it).
	NotesDir  string `toml:"notes_dir"`
	IndexPath string `toml:"index_path"`

	Clients []Client `toml:"clients"`

	// Transcription (DESIGN 7.1). Language "auto" sends language=auto;
	// "" omits the parameter entirely.
	WhisperURL      string `toml:"whisper_url"`
	WhisperLanguage string `toml:"whisper_language"`

	// Backoff per DESIGN 4: next_retry_at = now + min(base*factor^n, cap).
	BackoffBaseSeconds int `toml:"backoff_base_seconds"`
	BackoffFactor      int `toml:"backoff_factor"`
	BackoffCapSeconds  int `toml:"backoff_cap_seconds"`
	MaxRetries         int `toml:"max_retries"`

	TrashRetentionDays int `toml:"trash_retention_days"`
	MaxUploadMB        int `toml:"max_upload_mb"`

	// TLS is present from day one but unused in v1 (DESIGN 12): the VPN
	// provides transport security; enabling TLS later is a config change.
	TLSCertFile string `toml:"tls_cert_file"`
	TLSKeyFile  string `toml:"tls_key_file"`
}

// Load reads, decodes, defaults and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse validates in-memory TOML (the same contract Load applies to bytes
// read from disk).
func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("config: unknown key(s): %s", strings.Join(keys, ", "))
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.WhisperLanguage == "" {
		c.WhisperLanguage = "auto"
	}
	if c.BackoffBaseSeconds == 0 {
		c.BackoffBaseSeconds = 60
	}
	if c.BackoffFactor == 0 {
		c.BackoffFactor = 2
	}
	if c.BackoffCapSeconds == 0 {
		c.BackoffCapSeconds = 3600
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 10
	}
	if c.TrashRetentionDays == 0 {
		c.TrashRetentionDays = 30
	}
	if c.MaxUploadMB == 0 {
		c.MaxUploadMB = 50
	}
}

func (c *Config) validate() error {
	for _, req := range []struct{ name, value string }{
		{"bind", c.Bind},
		{"notes_dir", c.NotesDir},
		{"index_path", c.IndexPath},
		{"whisper_url", c.WhisperURL},
	} {
		if req.value == "" {
			return fmt.Errorf("config: %s is required", req.name)
		}
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("config: port %d out of range 0..65535", c.Port)
	}
	if len(c.Clients) == 0 {
		return fmt.Errorf("config: clients is required (at least one {name, token_sha256})")
	}
	seen := map[string]bool{}
	for _, cl := range c.Clients {
		if cl.Name == "" {
			return fmt.Errorf("config: client with empty name")
		}
		if seen[cl.Name] {
			return fmt.Errorf("config: duplicate client name %q", cl.Name)
		}
		seen[cl.Name] = true
		if !isHex64(cl.TokenSHA256) {
			return fmt.Errorf("config: client %q: token_sha256 must be 64 hex chars", cl.Name)
		}
	}
	u, err := url.Parse(c.WhisperURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: whisper_url %q is not an absolute http(s) URL", c.WhisperURL)
	}
	if c.BackoffBaseSeconds <= 0 {
		return fmt.Errorf("config: backoff_base_seconds must be > 0")
	}
	if c.BackoffFactor < 1 {
		return fmt.Errorf("config: backoff_factor must be >= 1")
	}
	if c.BackoffCapSeconds < c.BackoffBaseSeconds {
		return fmt.Errorf("config: backoff_cap_seconds must be >= backoff_base_seconds")
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("config: max_retries must be >= 0")
	}
	if c.TrashRetentionDays <= 0 {
		return fmt.Errorf("config: trash_retention_days must be > 0")
	}
	if c.MaxUploadMB <= 0 {
		return fmt.Errorf("config: max_upload_mb must be > 0")
	}
	return nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, ch := range s {
		hex := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
		if !hex {
			return false
		}
	}
	return true
}
