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
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	log.Info("lucien " + version + " starting")
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
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
	w.Start(ctx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
		w.Stop()
	}
	return nil
}
