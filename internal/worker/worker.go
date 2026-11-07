// Package worker is the async pipeline runner (DESIGN.md 6): discovery,
// claims, wake-ups and adapter execution. Skeleton until the worker slice.
package worker

import (
	"context"
	"log/slog"

	"lucien/internal/config"
	"lucien/internal/index"
	"lucien/internal/notestore"
)

type Worker struct {
	store *notestore.Store
	idx   *index.Index
	cfg   *config.Config
	log   *slog.Logger
}

func New(store *notestore.Store, idx *index.Index, cfg *config.Config, log *slog.Logger) *Worker {
	return &Worker{store: store, idx: idx, cfg: cfg, log: log}
}

// ResetRunning is the boot reset of claimed notes (DESIGN.md 6 rule 3).
func (w *Worker) ResetRunning() {}

// Start launches the loop; Stop ends it.
func (w *Worker) Start(ctx context.Context) {}
func (w *Worker) Stop()                     {}
