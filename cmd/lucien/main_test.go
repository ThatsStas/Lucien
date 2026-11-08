package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"lucien/internal/config"
)

// The --debug flag is the switch between "what happened" (INFO/WARN/ERROR)
// and "why it happened" (DEBUG): NewLogger must gate the debug level, and
// the startup config dump must surface every resolved knob without ever
// printing a token hash.

func TestNewLoggerDebugGating(t *testing.T) {
	ctx := context.Background()
	plain := NewLogger(false)
	if plain.Enabled(ctx, slog.LevelDebug) {
		t.Error("default logger must not enable debug")
	}
	for _, lvl := range []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		if !plain.Enabled(ctx, lvl) {
			t.Errorf("default logger must enable %s", lvl)
		}
	}
	dbg := NewLogger(true)
	for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		if !dbg.Enabled(ctx, lvl) {
			t.Errorf("--debug logger must enable %s", lvl)
		}
	}
}

func TestLogResolvedConfigShowsKnobsHidesSecrets(t *testing.T) {
	const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &config.Config{
		Bind: "0.0.0.0", Port: 8188,
		NotesDir:  "/home/u/notes",
		IndexPath: "/home/u/.lucien/index.sqlite",
		Clients: []config.Client{
			{Name: "phone", TokenSHA256: hashA},
			{Name: "agent", TokenSHA256: hashB},
		},
		WhisperURL:            "http://10.0.0.93:9001/v1/audio/transcriptions",
		WhisperLanguage:       "auto",
		BackoffBaseSeconds:    60,
		BackoffFactor:         2,
		BackoffCapSeconds:     3600,
		MaxRetries:            10,
		WorkerIdlePollSeconds: 30,
		TrashRetentionDays:    30,
		MaxUploadMB:           25,
	}

	logResolvedConfig(log, cfg)

	out := buf.String()
	for _, want := range []string{
		"0.0.0.0", "8188", "/home/u/notes", "index.sqlite",
		"10.0.0.93:9001", "auto", "phone", "agent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config dump missing %q in:\n%s", want, out)
		}
	}
	for _, leak := range []string{hashA, hashB, "aaaaaaaa", "bbbbbbbb", "token_sha256"} {
		if strings.Contains(out, leak) {
			t.Errorf("config dump leaked secret material %q", leak)
		}
	}
	if !strings.Contains(out, "level=DEBUG") {
		t.Errorf("config dump belongs at debug level, got:\n%s", out)
	}
}
