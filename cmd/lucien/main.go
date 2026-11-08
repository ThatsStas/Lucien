// Command lucien is the notes backend: a self-hosted REST service that
// stores notes as git-versioned JSON files and processes them through
// asynchronous pipelines. See docs/DESIGN.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lucien/internal/api"
	"lucien/internal/config"
	"lucien/internal/index"
	"lucien/internal/notestore"
	"lucien/internal/worker"
)

const version = "dev"

func main() {
	configPath := flag.String("config", "lucien.toml", "path to TOML config")
	debug := flag.Bool("debug", false, "verbose logs: every request, auth decision, whisper call and worker cycle")
	flag.Parse()

	log := NewLogger(*debug)
	if *debug {
		log.Info("debug logging enabled")
	}
	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// NewLogger builds the process logger. Default level shows what happened
// (INFO) and everything wrong (WARN/ERROR); --debug additionally shows the
// mechanics: per-request access lines, auth decisions, whisper calls and
// worker sleep/wake cycles.
func NewLogger(debug bool) *slog.Logger {
	opts := &slog.HandlerOptions{}
	if debug {
		opts.Level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// logResolvedConfig dumps the effective configuration at debug level so a
// "why is it doing THAT" report can be answered from the log alone. Client
// names are printed; token hashes never are (they are the secrets).
func logResolvedConfig(log *slog.Logger, cfg *config.Config) {
	names := make([]string, 0, len(cfg.Clients))
	for _, c := range cfg.Clients {
		names = append(names, c.Name)
	}
	log.Debug("resolved config",
		"bind", cfg.Bind, "port", cfg.Port,
		"notes_dir", cfg.NotesDir, "index_path", cfg.IndexPath,
		"whisper_url", cfg.WhisperURL, "whisper_language", cfg.WhisperLanguage,
		"clients", strings.Join(names, ","),
		"max_retries", cfg.MaxRetries,
		"backoff", fmt.Sprintf("%ds*%d^n capped %ds", cfg.BackoffBaseSeconds, cfg.BackoffFactor, cfg.BackoffCapSeconds),
		"worker_idle_poll_seconds", cfg.WorkerIdlePollSeconds,
		"trash_retention_days", cfg.TrashRetentionDays,
		"max_upload_mb", cfg.MaxUploadMB,
	)
}

func run(configPath string, log *slog.Logger) error {
	log.Info("lucien " + version + " starting")
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logResolvedConfig(log, cfg)
	idx, err := index.Open(cfg.IndexPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	store := notestore.New(cfg.NotesDir, &notestore.Git{Dir: cfg.NotesDir}, idx, log)
	if err := store.Init(); err != nil {
		return err
	}

	w := worker.New(store, idx, cfg, log)
	w.ResetRunning() // DESIGN.md 6 rule 3, before the loop and before serving

	srv := api.New(store, idx, cfg, log)
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port))
	if err != nil {
		return fmt.Errorf("listen %s:%d: %w", cfg.Bind, cfg.Port, err)
	}
	log.Debug("listening", "local_addr", ln.Addr().String())
	// The harness contract: announce the bound port on stderr (port 0 in
	// config means ephemeral). 0.0.0.0/:: bind every interface, so
	// 127.0.0.1 is an honest announcement.
	host := cfg.Bind
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	fmt.Fprintf(os.Stderr, "addr=%s:%d\n", host, ln.Addr().(*net.TCPAddr).Port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	w.Start(ctx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
		sig := "unknown"
		select {
		case s := <-sigCh:
			sig = s.String()
		default:
		}
		log.Info("shutdown signal received", "signal", sig)
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
		w.Stop()
		log.Debug("worker loop stopped")
	}
	return nil
}
