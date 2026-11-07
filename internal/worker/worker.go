// Package worker is the async pipeline runner (DESIGN.md 6): one loop that
// discovers due notes via indexed SQL, claims them through the Note Store,
// executes registered step pipelines, and sleeps event-driven between
// wake-ups. Adapters (whisper today) live beside the runner and classify
// their own failures; the runner owns claims, the chain table, backoff and
// retry bookkeeping (DESIGN.md 4).
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"lucien/internal/config"
	"lucien/internal/index"
	"lucien/internal/notestore"
)

// ErrPermanent marks a failure retries cannot fix (DESIGN.md 7.1: 4xx,
// unreadable audio). Anything else a step returns is retryable.
var ErrPermanent = errors.New("permanent failure")

// StepFunc executes one chain step end to end and applies its result as a
// single atomic Note Store mutation at the end (DESIGN.md 4 idempotency).
// It receives the freshly claimed note.
type StepFunc func(ctx context.Context, n *notestore.Note) error

// discoverBatch is the per-pass discovery limit (DESIGN.md 6 rule 1: LIMIT 32).
const discoverBatch = 32

// whisperTimeout bounds one transcription request. Large-v3 on a phone
// recording is seconds; minutes mean the endpoint is stuck.
const whisperTimeout = 10 * time.Minute

type Worker struct {
	store   *notestore.Store
	idx     *index.Index
	cfg     *config.Config
	log     *slog.Logger
	whisper *whisperClient

	mu       sync.RWMutex
	steps    map[string]StepFunc
	cancel   context.CancelFunc
	loopDone *sync.WaitGroup

	wake chan struct{} // buffered (1): store mutation signals (DESIGN 6 rule 2)
}

func New(store *notestore.Store, idx *index.Index, cfg *config.Config, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	w := &Worker{
		store: store,
		idx:   idx,
		cfg:   cfg,
		log:   log,
		steps: map[string]StepFunc{},
		wake:  make(chan struct{}, 1),
	}
	w.whisper = &whisperClient{
		url:      cfg.WhisperURL,
		language: cfg.WhisperLanguage,
		client:   &http.Client{Timeout: whisperTimeout},
	}
	// Phase 1 registers the transcribe pipeline only. tag/embed stay
	// pending and are skipped (design gap, DoD report).
	w.steps[notestore.StepTranscribe] = w.stepTranscribe
	// One wake channel per store; the store calls this after every
	// mutation that creates or re-arms a pending note.
	store.SetWorkerWake(func() {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	})
	return w
}

// Register adds or replaces a step pipeline (DESIGN.md 4 extensibility:
// a new pipeline is one registration, no core changes).
func (w *Worker) Register(step string, fn StepFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.steps[step] = fn
}

func (w *Worker) pipeline(step string) (StepFunc, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	fn, ok := w.steps[step]
	return fn, ok
}

// ResetRunning is boot recovery (DESIGN.md 6 rule 3): every running note
// was claimed by a dead process and goes back to pending, and audio
// buffers orphaned by a crash between a step's commit and its cleanup
// are swept away (with their deletion committed). It runs before the
// loop and before serving, and returns the number of re-armed notes.
func (w *Worker) ResetRunning() int {
	notes, err := w.store.All()
	if err != nil {
		w.log.Warn("worker: boot scan skipped unreadable notes", "err", err)
	}
	reset := 0
	for _, n := range notes {
		if n.Processing.Status == notestore.StatusRunning {
			if _, err := w.store.ResetToPending(n.ID); err != nil {
				w.log.Error("worker: boot reset failed", "id", n.ID, "err", err)
				continue
			}
			reset++
		}
		// Stray buffers: a note past transcribe still holding numbered
		// audio means the previous process died between the step commit
		// and cleanup. Notes at transcribe keep their buffers (pending
		// work); failed notes keep them too (retry is a later phase).
		if n.Processing.Step != notestore.StepTranscribe {
			bufs, err := w.store.AudioBuffers(n.ID)
			if err != nil {
				w.log.Warn("worker: buffer sweep scan failed", "id", n.ID, "err", err)
				continue
			}
			if len(bufs) > 0 {
				if err := w.store.DeleteBuffers(n.ID, bufs, "transcribe "+n.ID); err != nil {
					w.log.Warn("worker: buffer sweep failed", "id", n.ID, "err", err)
				}
			}
		}
	}
	return reset
}

// Start launches the event loop; Stop ends it.
func (w *Worker) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	w.mu.Lock()
	w.cancel, w.loopDone = cancel, wg
	w.mu.Unlock()
	go func() {
		defer wg.Done()
		w.loop(ctx)
	}()
}

func (w *Worker) Stop() {
	w.mu.Lock()
	cancel, wg := w.cancel, w.loopDone
	w.cancel, w.loopDone = nil, nil
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	wg.Wait()
}

// loop implements DESIGN.md 6 rule 2: work until a pass finds nothing,
// then sleep until the earliest retry deadline, a store wake-up, or the
// idle-poll ceiling (the lost-signal safety net: a missed signal degrades
// to a delayed retry, never a stuck note).
func (w *Worker) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if ran := w.Pass(ctx); ran > 0 {
			continue // something moved: there may be more, check again now
		}
		delay := time.Duration(w.cfg.WorkerIdlePollSeconds) * time.Second
		if next, err := w.idx.EarliestRetry(w.store.Now()); err != nil {
			w.log.Error("worker: retry deadline query failed", "err", err)
		} else if next != nil {
			if d := time.Until(*next); d < delay {
				delay = d
			}
		}
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

// Pass runs one discovery-and-execute round and returns the number of
// notes worked on. Exported for deterministic tests; the loop is the
// production caller.
func (w *Worker) Pass(ctx context.Context) int {
	ids, err := w.idx.DiscoverDue(w.store.Now(), discoverBatch)
	if err != nil {
		w.log.Error("worker: discovery failed", "err", err)
		return 0
	}
	ran := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		n, err := w.store.Read(id)
		if err != nil {
			w.log.Warn("worker: skipping unreadable note", "id", id, "err", err)
			continue
		}
		step, ok := w.pipeline(n.Processing.Step)
		if !ok || n.Processing.Status != notestore.StatusPending {
			continue // no pipeline here (phase-1 gap) or not claimable
		}
		claimed, err := w.store.Claim(id)
		if err != nil {
			if !errors.Is(err, notestore.ErrNotFound) && !errors.Is(err, notestore.ErrStepMismatch) {
				w.log.Error("worker: claim failed", "id", id, "err", err)
			}
			continue
		}
		w.runStep(ctx, claimed, step)
		ran++
	}
	return ran
}

// runStep executes one claimed note's step and persists the outcome:
// success is the step's own commit; failure becomes retry bookkeeping —
// a scheduled retry (retryable, within cap) or a terminal failed state
// (permanent, or the cap is exceeded). Bookkeeping never commits
// (DESIGN.md 10).
func (w *Worker) runStep(ctx context.Context, n *notestore.Note, step StepFunc) {
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("pipeline panic: %v", r)
				w.log.Error("worker: pipeline panicked", "id", n.ID, "step", n.Processing.Step, "panic", r)
			}
		}()
		return step(ctx, n)
	}()
	if err == nil {
		return
	}
	nextCount := n.Processing.RetryCount + 1
	var nextRetry *time.Time
	if !errors.Is(err, ErrPermanent) && nextCount <= w.cfg.MaxRetries {
		t := w.store.Now().Add(w.backoff(n.Processing.RetryCount))
		nextRetry = &t
	}
	if _, ferr := w.store.FailStep(n.ID, n.Processing.Step, nextCount, nextRetry, err.Error()); ferr != nil {
		if !errors.Is(ferr, notestore.ErrNotFound) && !errors.Is(ferr, notestore.ErrStepMismatch) {
			w.log.Error("worker: failure bookkeeping lost", "id", n.ID, "err", ferr)
		}
	}
}

// backoff is DESIGN.md 4: min(base * factor^retry_count, cap) from the
// note's last failure count, floored at one second so a misconfigured
// zero base can never hot-loop the runner.
func (w *Worker) backoff(retryCount int) time.Duration {
	base := float64(w.cfg.BackoffBaseSeconds)
	factor := float64(w.cfg.BackoffFactor)
	capS := float64(w.cfg.BackoffCapSeconds)
	d := base * math.Pow(factor, float64(retryCount))
	if d > capS {
		d = capS
	}
	if d < 1 {
		d = 1
	}
	return time.Duration(d) * time.Second
}

// --- transcribe pipeline (DESIGN.md 7.1) ------------------------------------

// stepTranscribe posts every numbered audio buffer to whisper, appends one
// transcript entry per buffer as a single atomic CompleteStep, then deletes
// the buffers with their own commit. Ordering is deliberate: the transcript
// is durable before the buffers disappear, so a crash between the two
// re-runs the step cleanly (DESIGN.md 4) and never loses audio.
func (w *Worker) stepTranscribe(ctx context.Context, n *notestore.Note) error {
	buffers, err := w.store.AudioBuffers(n.ID)
	if err != nil {
		return fmt.Errorf("list buffers: %w", err)
	}
	texts := make([]string, 0, len(buffers))
	for _, name := range buffers {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := w.store.ReadBuffer(n.ID, name)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPermanent, err) // unreadable audio (DESIGN 7.1)
		}
		text, err := w.whisper.Transcribe(ctx, name, data)
		if err != nil {
			return err
		}
		texts = append(texts, text)
	}
	if _, err := w.store.CompleteStep(n.ID, notestore.StepTranscribe, func(note *notestore.Note) {
		id := 0
		for _, e := range note.Entries {
			if e.ID > id {
				id = e.ID
			}
		}
		for _, text := range texts {
			if strings.TrimSpace(text) == "" {
				continue // silence: no transcript entry to pollute the note
			}
			id++
			note.Entries = append(note.Entries, notestore.Entry{ID: id, Kind: "transcript", Text: text})
		}
	}, "transcribe "+n.ID); err != nil {
		return err
	}
	if err := w.store.DeleteBuffers(n.ID, buffers, "transcribe "+n.ID); err != nil {
		// The transcript is durable; ResetRunning sweeps the rest later.
		w.log.Warn("worker: buffer cleanup deferred", "id", n.ID, "err", err)
	}
	return nil
}

// --- whisper adapter ----------------------------------------------------------

// whisperClient speaks the OpenAI-compatible transcription contract.
// Failure classification per DESIGN.md 7.1: transport errors, timeouts and
// 5xx are retryable plain errors; 4xx wraps ErrPermanent.
type whisperClient struct {
	url      string
	language string // "auto" per DESIGN default; "" omits the field
	client   *http.Client
}

func (c *whisperClient) Transcribe(ctx context.Context, filename string, data []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if c.language != "" {
		if err := mw.WriteField("language", c.language); err != nil {
			return "", err
		}
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.client.Do(req) // connection errors and timeouts land here: retryable
	if err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("whisper: reading response: %w", err)
	}
	switch {
	case resp.StatusCode >= 500:
		return "", fmt.Errorf("whisper: status %d: %s", resp.StatusCode, snippetOf(payload))
	case resp.StatusCode >= 400:
		return "", fmt.Errorf("%w: whisper: status %d: %s", ErrPermanent, resp.StatusCode, snippetOf(payload))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		// A 2xx that is not {"text": ...} is a contract break, but it is
		// more likely a transient gateway glitch than a permanent note
		// defect, so it retries until the cap (DoD-flagged).
		return "", fmt.Errorf("whisper: malformed response (%d bytes): %w", len(payload), err)
	}
	return out.Text, nil
}

func snippetOf(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
