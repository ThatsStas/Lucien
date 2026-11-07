package notestore

import (
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Sentinel errors the API layer maps to HTTP codes (DESIGN.md 5).
var (
	ErrNotFound             = errors.New("note not found")
	ErrCorrupt              = errors.New("corrupt note file")
	ErrStale                = errors.New("base_modified mismatch")
	ErrPendingTranscription = errors.New("entry_edit blocked while transcription pending")
	ErrEntryNotFound        = errors.New("entry id not found")
	ErrStepMismatch         = errors.New("processing step/status mismatch")
	ErrUnsupportedAudio     = errors.New("unsupported audio format")
)

var audioExts = set("m4a", "mp3", "wav", "ogg", "flac")

// VCS abstracts versioning of the notes directory. NullVCS records calls;
// Git is the production implementation (DESIGN.md 10).
type VCS interface {
	Init() error
	CommitPaths(paths []string, message string) error
}

// Index receives every mutation so query state stays current with the
// files (DESIGN.md 2: "index notification"). The index is derived state:
// a failed update heals via POST /v1/admin/reindex.
type Index interface {
	Upsert(n *Note) error
	Remove(id string) error
}

// Store owns the notes directory. It is not safe for concurrent use by
// multiple processes; within the backend one mutex-serialized instance
// serves API and worker.
type Store struct {
	dir   string
	vcs   VCS
	idx   Index
	log   *slog.Logger
	clock func() time.Time

	entropy    *ulid.MonotonicEntropy
	onMutation func() // worker wake hook (DESIGN.md 6 rule 2)
}

func New(dir string, vcs VCS, idx Index, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		dir:     dir,
		vcs:     vcs,
		idx:     idx,
		log:     logger,
		clock:   time.Now,
		entropy: ulid.Monotonic(crand.Reader, 0),
	}
}

// SetClock overrides the time source (deterministic tests).
func (s *Store) SetClock(f func() time.Time) { s.clock = f }

// SetWorkerWake registers the callback invoked after every mutation that
// creates or re-arms a pending note (DESIGN.md 6 rule 2).
func (s *Store) SetWorkerWake(f func()) { s.onMutation = f }

// Now is the store's canonical timestamp: UTC, second precision, so files
// serialize as ISO 8601 without fractional digits.
func (s *Store) Now() time.Time { return s.clock().UTC().Truncate(time.Second) }

// Init prepares the notes directory (and .trash) and the VCS. Safe to call
// on an existing tree: the backend runs it at every boot, git init included.
func (s *Store) Init() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("notes dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, ".trash"), 0o700); err != nil {
		return fmt.Errorf("trash dir: %w", err)
	}
	return s.vcs.Init()
}

func (s *Store) notePath(id string) string { return filepath.Join(s.dir, id+".json") }

// --- Create ----------------------------------------------------------------

type CreateRequest struct {
	SourceKind string // phase 1 API uses "text" and "audio"
	Text       string // body for text notes
	Audio      io.Reader
	AudioExt   string // "m4a" (leading dot optional); validated
	Tags       []Tag
	State      string // empty means inbox
	Priority   int
	RemindAt   *time.Time
	DeferUntil *time.Time
}

// Create writes a new note per DESIGN.md 5.1: assign id, write audio
// buffer if any, write the JSON, update index, one commit "add note <id>".
func (s *Store) Create(req CreateRequest) (*Note, error) {
	if !sourceKinds[req.SourceKind] {
		return nil, fmt.Errorf("invalid source kind %q", req.SourceKind)
	}
	ext := ""
	switch req.SourceKind {
	case "text":
		if strings.TrimSpace(req.Text) == "" {
			return nil, fmt.Errorf("text note requires non-empty text")
		}
	case "audio":
		if req.Audio == nil {
			return nil, fmt.Errorf("audio note requires audio data")
		}
		var err error
		ext, err = normExt(req.AudioExt)
		if err != nil {
			return nil, err
		}
	}

	state := req.State
	if state == "" {
		state = "inbox"
	}
	now := s.Now()
	n := &Note{
		ID:             s.newID(),
		Created:        now,
		Modified:       now,
		State:          state,
		StateChangedAt: now,
		Tags:           req.Tags,
		Priority:       req.Priority,
		Source:         Source{Kind: req.SourceKind},
		Entries:        []Entry{},
		Processing:     Processing{Step: FirstStep(req.SourceKind), Status: StatusPending},
		RemindAt:       truncTime(req.RemindAt),
		DeferUntil:     truncTime(req.DeferUntil),
		Dedup:          Dedup{Resaved: []time.Time{}},
	}
	if n.Tags == nil {
		n.Tags = []Tag{}
	}
	if req.SourceKind == "text" {
		n.Entries = []Entry{{ID: 1, TS: now, Kind: "text", Text: req.Text}}
	}
	n.Dedup.ContentHash = ContentHash(n.Entries)
	if err := validate(n); err != nil {
		return nil, err
	}

	paths := []string{n.ID + ".json"}
	if req.SourceKind == "audio" {
		bufName := n.ID + ".1." + ext
		if err := s.writeAtomic(filepath.Join(s.dir, bufName), req.Audio); err != nil {
			return nil, err
		}
		paths = append(paths, bufName)
	}
	if err := s.persist(n, paths, "add note "+n.ID, true, true); err != nil {
		if req.SourceKind == "audio" { // no partial state on failure
			os.Remove(filepath.Join(s.dir, n.ID+".1."+ext))
			os.Remove(s.notePath(n.ID))
		}
		return nil, err
	}
	return n, nil
}

// --- Read -------------------------------------------------------------------

func (s *Store) Read(id string) (*Note, error) {
	data, err := os.ReadFile(s.notePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, err
	}
	var n Note
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, id, err)
	}
	if n.ID != id {
		return nil, fmt.Errorf("%w: %s: id field is %q", ErrCorrupt, id, n.ID)
	}
	if err := validate(&n); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, id, err)
	}
	return &n, nil
}

// --- Append -----------------------------------------------------------------

// AppendText adds a text entry and re-enters the chain at embed only
// (no re-tagging), per DESIGN.md 5.3.
func (s *Store) AppendText(id, text string) (*Note, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("append requires non-empty text")
	}
	n, err := s.Read(id)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	n.Entries = append(n.Entries, Entry{ID: nextEntryID(n.Entries), TS: now, Kind: "text", Text: text})
	n.Processing = Processing{Step: StepEmbed, Status: StatusPending}
	n.Dedup.ContentHash = ContentHash(n.Entries)
	n.Modified = now
	if err := validate(n); err != nil {
		return nil, err
	}
	if err := s.persist(n, []string{n.ID + ".json"}, "append "+n.ID, true, true); err != nil {
		return nil, err
	}
	return n, nil
}

// AppendAudio stores an extra buffer <id>.<n>.<ext> and re-enters the
// chain at transcribe (DESIGN.md 5.3).
func (s *Store) AppendAudio(id string, r io.Reader, ext string) (*Note, error) {
	ext, err := normExt(ext)
	if err != nil {
		return nil, err
	}
	n, err := s.Read(id)
	if err != nil {
		return nil, err
	}
	bufName := fmt.Sprintf("%s.%d.%s", n.ID, s.nextBuffer(n.ID, ext), ext)
	if err := s.writeAtomic(filepath.Join(s.dir, bufName), r); err != nil {
		return nil, err
	}
	n.Processing = Processing{Step: StepTranscribe, Status: StatusPending}
	n.Modified = s.Now()
	if err := validate(n); err != nil {
		os.Remove(filepath.Join(s.dir, bufName))
		return nil, err
	}
	if err := s.persist(n, []string{n.ID + ".json", bufName}, "append "+n.ID, true, true); err != nil {
		os.Remove(filepath.Join(s.dir, bufName))
		return nil, err
	}
	return n, nil
}

// --- Edit (PATCH) -------------------------------------------------------------

type EntryEdit struct {
	ID   int
	Text string
}

// Patch carries the mutable fields of PATCH /v1/notes/:id (DESIGN.md 5.7).
// A nil pointer means "leave alone"; the Set* flags distinguish "clear to
// null" from "absent" for the nullable timestamps.
type Patch struct {
	State         *string
	Tags          *[]Tag
	Priority      *int
	SetRemindAt   bool
	RemindAt      *time.Time
	SetDeferUntil bool
	DeferUntil    *time.Time
	EntryEdit     *EntryEdit
}

// Edit applies a patch after base_modified concurrency check (DESIGN.md
// 5.7). On mismatch it returns the FULL current note plus ErrStale so the
// API can answer 409 with it. Commit is "state <id> <new>" when state
// changed, otherwise "edit <id>" (DESIGN.md 10).
func (s *Store) Edit(id string, p Patch, baseModified time.Time) (*Note, error) {
	n, err := s.Read(id)
	if err != nil {
		return nil, err
	}
	if !n.Modified.Equal(baseModified.UTC().Truncate(time.Second)) {
		return n, ErrStale
	}
	if p.EntryEdit != nil && n.Processing.Step == StepTranscribe &&
		(n.Processing.Status == StatusPending || n.Processing.Status == StatusRunning) {
		return nil, ErrPendingTranscription
	}

	orig, _ := json.Marshal(n)
	stateChanged := false
	if p.State != nil {
		if !states[*p.State] {
			return nil, fmt.Errorf("invalid state %q", *p.State)
		}
		if *p.State != n.State {
			n.State = *p.State
			n.StateChangedAt = s.Now()
			stateChanged = true
		}
	}
	if p.Tags != nil {
		for _, t := range *p.Tags {
			if t.Name == "" || !tagBy[t.By] {
				return nil, fmt.Errorf("invalid tag %+v", t)
			}
		}
		n.Tags = *p.Tags // tag edits replace the full list as-sent (5.7)
		if n.Tags == nil {
			n.Tags = []Tag{}
		}
	}
	if p.Priority != nil {
		if *p.Priority < -20 || *p.Priority > 19 {
			return nil, fmt.Errorf("priority %d outside -20..19", *p.Priority)
		}
		n.Priority = *p.Priority
	}
	if p.SetRemindAt {
		n.RemindAt = truncTime(p.RemindAt)
	}
	if p.SetDeferUntil {
		n.DeferUntil = truncTime(p.DeferUntil)
	}
	if p.EntryEdit != nil {
		found := false
		for i := range n.Entries {
			if n.Entries[i].ID == p.EntryEdit.ID {
				n.Entries[i].Text = p.EntryEdit.Text
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: %d", ErrEntryNotFound, p.EntryEdit.ID)
		}
		n.Dedup.ContentHash = ContentHash(n.Entries)
	}

	if equal, err := contentEqual(n, orig); err != nil {
		return nil, err
	} else if equal {
		return n, nil // no-op: history is content history (DESIGN.md 10)
	}
	n.Modified = s.Now()
	if err := validate(n); err != nil {
		return nil, err
	}
	msg := "edit " + n.ID
	if stateChanged {
		msg = "state " + n.ID + " " + n.State
	}
	if err := s.persist(n, []string{n.ID + ".json"}, msg, true, false); err != nil {
		return nil, err
	}
	return n, nil
}

// --- Delete -------------------------------------------------------------------

// Delete moves the note JSON and every audio buffer to .trash/ and commits
// both sides of the move so git history retains the content (DESIGN.md
// 5.10, 10). Phase 4 adds the trash GC.
func (s *Store) Delete(id string) error {
	n, err := s.Read(id)
	if err != nil {
		return err
	}
	trash := filepath.Join(s.dir, ".trash")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return err
	}
	names := []string{n.ID + ".json"}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), n.ID+".") && e.Name() != n.ID+".json" {
			names = append(names, e.Name())
		}
	}
	var staged []string
	for _, name := range names {
		if err := os.Rename(filepath.Join(s.dir, name), filepath.Join(trash, name)); err != nil {
			return fmt.Errorf("trash %s: %w", name, err)
		}
		staged = append(staged, name, ".trash/"+name)
	}
	if err := s.idx.Remove(n.ID); err != nil {
		return fmt.Errorf("index remove: %w", err)
	}
	if err := s.vcs.CommitPaths(staged, "delete "+n.ID); err != nil {
		return err
	}
	return nil
}

// --- Worker-facing transitions --------------------------------------------------

// Claim flips processing to running and writes the file WITHOUT committing
// (DESIGN.md 10: claims ride along into the next content commit). Only a
// pending note can be claimed, which serializes claims per note.
func (s *Store) Claim(id string) (*Note, error) {
	n, err := s.Read(id)
	if err != nil {
		return nil, err
	}
	if n.Processing.Status != StatusPending {
		return nil, fmt.Errorf("%w: claim needs pending, status is %q", ErrStepMismatch, n.Processing.Status)
	}
	n.Processing.Status = StatusRunning
	if err := s.persist(n, nil, "", false, false); err != nil {
		return nil, err
	}
	return n, nil
}

// CompleteStep applies the step's content changes, advances the chain to
// the next step (pending, retry bookkeeping reset) and commits with the
// caller's DESIGN.md 10 message ("transcribe <id>" etc). The running
// claim rides along into this commit.
func (s *Store) CompleteStep(id, step string, apply func(*Note), commitMsg string) (*Note, error) {
	n, err := s.Read(id)
	if err != nil {
		return nil, err
	}
	if n.Processing.Step != step || n.Processing.Status != StatusRunning {
		return nil, fmt.Errorf("%w: want %s/running, have %s/%s", ErrStepMismatch,
			step, n.Processing.Step, n.Processing.Status)
	}
	apply(n)
	n.Processing = Processing{Step: nextStep(n.Source.Kind, step), Status: StatusPending}
	n.Modified = s.Now()
	for i := range n.Entries { // fill in any missing entry stamps
		if n.Entries[i].TS.IsZero() {
			n.Entries[i].TS = n.Modified
		}
	}
	if n.Entries == nil {
		n.Entries = []Entry{}
	}
	n.Dedup.ContentHash = ContentHash(n.Entries)
	if err := validate(n); err != nil {
		return nil, err
	}
	if err := s.persist(n, []string{n.ID + ".json"}, commitMsg, true, true); err != nil {
		return nil, err
	}
	return n, nil
}

// FailStep records retry bookkeeping WITHOUT a commit (DESIGN.md 10:
// "Not committed: retry bookkeeping"). nextRetry != nil keeps the note
// pending for retry; nil marks it permanently failed. The worker owns the
// backoff computation; the store just persists what it is told.
func (s *Store) FailStep(id, step string, retryCount int, nextRetry *time.Time, lastErr string) (*Note, error) {
	n, err := s.Read(id)
	if err != nil {
		return nil, err
	}
	if n.Processing.Step != step || n.Processing.Status != StatusRunning {
		return nil, fmt.Errorf("%w: want %s/running, have %s/%s", ErrStepMismatch,
			step, n.Processing.Step, n.Processing.Status)
	}
	n.Processing.RetryCount = retryCount
	msg := lastErr
	n.Processing.LastError = &msg
	if t := truncTime(nextRetry); t != nil {
		n.Processing.NextRetryAt = t
		n.Processing.Status = StatusPending
	} else {
		n.Processing.NextRetryAt = nil
		n.Processing.Status = StatusFailed
	}
	if err := s.persist(n, nil, "", false, true); err != nil {
		return nil, err
	}
	return n, nil
}

// --- Bulk read (reindex support) ----------------------------------------------

// All reads every top-level note file, oldest first (ULID names sort by
// creation). Corrupt or unreadable files are skipped and reported as a
// joined error so an admin reindex still rebuilds the healthy notes.
// .trash/ and .tmp-* are not notes and are never listed.
func (s *Store) All() ([]*Note, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var notes []*Note
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		n, err := s.Read(id)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		notes = append(notes, n)
	}
	return notes, errors.Join(errs...)
}

// --- internals --------------------------------------------------------------

func (s *Store) newID() string {
	// Monotonic entropy guarantees strictly increasing ids even when the
	// clock is frozen (tests). crypto/rand never fails; on a hypothetical
	// error ulid.Make is the infallible fallback.
	id, err := ulid.New(ulid.Timestamp(s.clock()), s.entropy)
	if err != nil {
		return ulid.Make().String()
	}
	return id.String()
}

// persist serializes the note and applies the mutation side effects in the
// DESIGN.md 2 order: atomic file write, index, commit, worker wake.
func (s *Store) persist(n *Note, paths []string, msg string, commit, wake bool) error {
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := s.writeAtomic(s.notePath(n.ID), strings.NewReader(string(data))); err != nil {
		return err
	}
	if err := s.idx.Upsert(n); err != nil {
		return fmt.Errorf("index upsert: %w", err)
	}
	if commit {
		if err := s.vcs.CommitPaths(paths, msg); err != nil {
			return err
		}
	}
	if wake && s.onMutation != nil {
		s.onMutation()
	}
	return nil
}

// writeAtomic writes through a temp file in the destination directory and
// renames, so readers never see a half-written note (DESIGN.md 2).
func (s *Store) writeAtomic(path string, r io.Reader) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if _, err := os.Stat(tmpName); err == nil {
			os.Remove(tmpName)
		}
	}()
	data, err := io.ReadAll(r)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil { // notes are private
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // best-effort durability
		d.Sync()
		d.Close()
	}
	return nil
}

// nextBuffer returns the next audio buffer number for a note by scanning
// existing <id>.<n>.<ext> files (DESIGN.md 3.1 numbering).
func (s *Store) nextBuffer(id, ext string) int {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 1
	}
	prefix := id + "."
	max := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, "."+ext) {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(name, prefix), "."+ext)
		if t, err := strconv.Atoi(mid); err == nil && t > max {
			max = t
		}
	}
	return max + 1
}

func nextEntryID(entries []Entry) int {
	max := 0
	for _, e := range entries {
		if e.ID > max {
			max = e.ID
		}
	}
	return max + 1
}

func normExt(ext string) (string, error) {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if !audioExts[ext] {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAudio, ext)
	}
	return ext, nil
}

func truncTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC().Truncate(time.Second)
	return &v
}

// contentEqual compares a note against its pre-edit JSON, ignoring the
// modified stamp which changes with every accepted edit.
func contentEqual(n *Note, orig []byte) (bool, error) {
	c := *n
	c.Modified = time.Time{}
	cur, err := json.Marshal(&c)
	if err != nil {
		return false, err
	}
	var om, cm map[string]any
	if err := json.Unmarshal(orig, &om); err != nil {
		return false, err
	}
	delete(om, "modified")
	if err := json.Unmarshal(cur, &cm); err != nil {
		return false, err
	}
	delete(cm, "modified")
	a, _ := json.Marshal(om)
	b, _ := json.Marshal(cm)
	return string(a) == string(b), nil
}
