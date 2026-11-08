// Package index is the derived SQLite query state (DESIGN.md 9). It is a
// cache, not truth: everything here rebuilds from the note files via
// Reindex. The Note Store calls Upsert/Remove transactionally with every
// file write; the API reads lists from here; the worker discovers due
// notes here. Phase 1 uses the filter columns only — the FTS table is
// populated but `q` runs a plain LIKE, upgraded to MATCH in phase 2.
package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"lucien/internal/notestore"
)

const timeFmt = time.RFC3339

// Index implements notestore.Index plus the read side (Query for the API,
// DiscoverDue for the worker).
type Index struct {
	db   *sql.DB
	path string
}

// Open creates (if needed) and opens the index database at path, applying
// the schema from DESIGN.md 9. The parent directory is created.
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("index dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("index open: %w", err)
	}
	// One connection serializes writers for free; the index is derived so
	// NORMAL synchronous is enough (a torn file heals via reindex).
	db.SetMaxOpenConns(1)
	for _, p := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=5000`,
	} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("index pragma: %w", err)
		}
	}
	x := &Index{db: db, path: path}
	if err := x.schema(); err != nil {
		db.Close()
		return nil, err
	}
	return x, nil
}

func (x *Index) Close() error { return x.db.Close() }

func (x *Index) schema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS notes(
			id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			created TEXT NOT NULL,
			modified TEXT NOT NULL,
			priority INTEGER NOT NULL,
			source_kind TEXT NOT NULL,
			url_hash TEXT,
			content_hash TEXT,
			processing_step TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			next_retry_at TEXT,
			remind_at TEXT,
			defer_until TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_status_retry ON notes(processing_status, next_retry_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_state ON notes(state)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_modified ON notes(modified)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_remind ON notes(remind_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_defer ON notes(defer_until)`,
		`CREATE TABLE IF NOT EXISTS tags(note_id TEXT NOT NULL, tag TEXT NOT NULL, tag_by TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_tags_note ON tags(note_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tags_tag ON tags(tag)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(note_id UNINDEXED, entry_id UNINDEXED, text)`,
		`CREATE TABLE IF NOT EXISTS embeddings(
			note_id TEXT PRIMARY KEY,
			vector BLOB,
			model TEXT,
			updated_at TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := x.db.Exec(s); err != nil {
			return fmt.Errorf("index schema: %w", err)
		}
	}
	return nil
}

// --- write side (implements notestore.Index) --------------------------------

// Upsert replaces the index rows for a note: the filter row, its tags and
// its entry texts. Idempotent, so re-running a mutation is harmless.
func (x *Index) Upsert(n *notestore.Note) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertIn(tx, n); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertIn(tx *sql.Tx, n *notestore.Note) error {
	if _, err := tx.Exec(`DELETE FROM tags WHERE note_id=?`, n.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM entries_fts WHERE note_id=?`, n.ID); err != nil {
		return err
	}
	var urlHash, contentHash any
	if n.Dedup.URLHash != nil && *n.Dedup.URLHash != "" {
		urlHash = *n.Dedup.URLHash
	}
	if n.Dedup.ContentHash != "" {
		contentHash = n.Dedup.ContentHash
	}
	_, err := tx.Exec(`INSERT OR REPLACE INTO notes(
		id,state,created,modified,priority,source_kind,url_hash,content_hash,
		processing_step,processing_status,next_retry_at,remind_at,defer_until)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.State, fmtTime(n.Created), fmtTime(n.Modified), n.Priority,
		n.Source.Kind, urlHash, contentHash,
		n.Processing.Step, n.Processing.Status,
		fmtTimePtr(n.Processing.NextRetryAt), fmtTimePtr(n.RemindAt), fmtTimePtr(n.DeferUntil))
	if err != nil {
		return err
	}
	for _, t := range n.Tags {
		if _, err := tx.Exec(`INSERT INTO tags(note_id,tag,tag_by) VALUES(?,?,?)`, n.ID, t.Name, t.By); err != nil {
			return err
		}
	}
	for _, e := range n.Entries {
		if _, err := tx.Exec(`INSERT INTO entries_fts(note_id,entry_id,text) VALUES(?,?,?)`,
			n.ID, fmt.Sprint(e.ID), e.Text); err != nil {
			return err
		}
	}
	return nil
}

// Remove drops every index row for a note. Unknown ids are a no-op.
func (x *Index) Remove(id string) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := removeIn(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func removeIn(tx *sql.Tx, id string) error {
	for _, q := range []string{
		`DELETE FROM notes WHERE id=?`,
		`DELETE FROM tags WHERE note_id=?`,
		`DELETE FROM entries_fts WHERE note_id=?`,
		`DELETE FROM embeddings WHERE note_id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// Reindex drops and rebuilds all tables from the notes read off disk
// (DESIGN.md 9: "reindex drops and rebuilds"). Returns the row count.
func (x *Index) Reindex(notes []*notestore.Note) (int, error) {
	tx, err := x.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM notes`, `DELETE FROM tags`,
		`DELETE FROM entries_fts`, `DELETE FROM embeddings`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return 0, err
		}
	}
	for _, n := range notes {
		if err := upsertIn(tx, n); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(notes), nil
}

// --- read side ----------------------------------------------------------------

// Summary is the list projection of DESIGN.md 5: id, state, tags, priority,
// created, modified, source.kind, processing step/status, first-entry
// snippet truncated to 200 characters.
type Summary struct {
	ID       string           `json:"id"`
	State    string           `json:"state"`
	Tags     []notestore.Tag  `json:"tags"`
	Priority int              `json:"priority"`
	Created  time.Time        `json:"created"`
	Modified time.Time        `json:"modified"`
	Source   notestore.Source `json:"source"`
	Proc     procBrief        `json:"processing"`
	Snippet  string           `json:"snippet"`
}

type procBrief struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// ListResult is the 200 body of GET /v1/notes (DESIGN.md 5.4).
type ListResult struct {
	Notes []Summary `json:"notes"`
	Total int       `json:"total"`
}

// Filters carries every query parameter of DESIGN.md 5.4, all optional and
// AND-combined. Zero values mean "no filter" except Now, which the caller
// must pass (the store's canonical clock) for age and due comparisons.
type Filters struct {
	Now              time.Time
	States           []string // OR-combined within the param
	Tags             []string // AND-combined
	Q                string   // plain LIKE over entry text (phase 1)
	MinAge           time.Duration
	MaxAge           time.Duration
	ModifiedSince    *time.Time
	RemindDue        bool
	DeferDue         bool
	ProcessingStatus string
	Limit            int // default 100, max 1000
	Offset           int
}

const (
	defaultLimit = 100
	maxLimit     = 1000
	snippetRunes = 200
)

// Query runs a filtered list and returns the summary projection plus the
// total match count (before limit/offset).
func (x *Index) Query(f Filters) (*ListResult, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	var conds []string
	var args []any
	if len(f.States) > 0 {
		ph := strings.Repeat("?,", len(f.States))
		conds = append(conds, "state IN ("+strings.TrimSuffix(ph, ",")+")")
		for _, s := range f.States {
			args = append(args, s)
		}
	}
	for _, t := range f.Tags {
		conds = append(conds, `id IN (SELECT note_id FROM tags WHERE tag=?)`)
		args = append(args, t)
	}
	if f.Q != "" {
		conds = append(conds, `id IN (SELECT DISTINCT note_id FROM entries_fts WHERE text LIKE ? ESCAPE '\')`)
		args = append(args, "%"+escapeLike(f.Q)+"%")
	}
	if f.MinAge > 0 {
		conds = append(conds, `created <= ?`)
		args = append(args, fmtTime(f.Now.Add(-f.MinAge)))
	}
	if f.MaxAge > 0 {
		conds = append(conds, `created >= ?`)
		args = append(args, fmtTime(f.Now.Add(-f.MaxAge)))
	}
	if f.ModifiedSince != nil {
		conds = append(conds, `modified >= ?`) // inclusive sync-cursor bound
		args = append(args, fmtTime(*f.ModifiedSince))
	}
	if f.RemindDue {
		conds = append(conds, `remind_at IS NOT NULL AND remind_at <= ? AND state NOT IN ('done','archived')`)
		args = append(args, fmtTime(f.Now))
	}
	if f.DeferDue {
		conds = append(conds, `defer_until IS NOT NULL AND defer_until <= ?`)
		args = append(args, fmtTime(f.Now))
	}
	if f.ProcessingStatus != "" {
		conds = append(conds, `processing_status = ?`)
		args = append(args, f.ProcessingStatus)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := x.db.QueryRow(`SELECT count(*) FROM notes`+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	rows, err := x.db.Query(`SELECT id,state,priority,created,modified,source_kind,processing_step,processing_status
		FROM notes`+where+` ORDER BY modified DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, err
	}
	// Materialize the page first: the pool is a single connection (writers
	// serialize for free), so per-row tag/snippet queries must not run
	// while the page cursor is open.
	type rawRow struct {
		id, state, created, modified, kind, step, status string
		priority                                         int
	}
	var raw []rawRow
	for rows.Next() {
		var r rawRow
		if err := rows.Scan(&r.id, &r.state, &r.priority, &r.created, &r.modified, &r.kind, &r.step, &r.status); err != nil {
			rows.Close()
			return nil, err
		}
		raw = append(raw, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	out := make([]Summary, 0, len(raw))
	for _, r := range raw {
		s := Summary{
			ID:       r.id,
			State:    r.state,
			Priority: r.priority,
			Source:   notestore.Source{Kind: r.kind},
			Proc:     procBrief{Step: r.step, Status: r.status},
		}
		if s.Created, err = time.Parse(timeFmt, r.created); err != nil {
			return nil, fmt.Errorf("index corrupt created %q: %w", r.created, err)
		}
		if s.Modified, err = time.Parse(timeFmt, r.modified); err != nil {
			return nil, fmt.Errorf("index corrupt modified %q: %w", r.modified, err)
		}
		if s.Tags, err = x.tagsOf(s.ID); err != nil {
			return nil, err
		}
		if s.Snippet, err = x.snippetOf(s.ID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return &ListResult{Notes: out, Total: total}, nil
}

func (x *Index) tagsOf(id string) ([]notestore.Tag, error) {
	// The tags table carries tag_by beyond the DESIGN.md 9 shape because the
	// summary projection (DESIGN 5) needs the full tag object while list
	// reads must come from the index (5.4). Superset column, flagged in the
	// DoD report; fully rebuildable by reindex.
	rows, err := x.db.Query(`SELECT tag, tag_by FROM tags WHERE note_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []notestore.Tag{}
	for rows.Next() {
		var t, b string
		if err := rows.Scan(&t, &b); err != nil {
			return nil, err
		}
		tags = append(tags, notestore.Tag{Name: t, By: b})
	}
	return tags, rows.Err()
}

func (x *Index) snippetOf(id string) (string, error) {
	var text string
	err := x.db.QueryRow(
		`SELECT text FROM entries_fts WHERE note_id=? ORDER BY CAST(entry_id AS INTEGER) LIMIT 1`, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	r := []rune(text)
	if len(r) > snippetRunes {
		return string(r[:snippetRunes]), nil
	}
	return text, nil
}

// DiscoverDue is the worker's discovery query, verbatim from DESIGN.md 6:
// pending notes whose retry time has arrived, oldest first.
func (x *Index) DiscoverDue(now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := x.db.Query(`SELECT id FROM notes
		WHERE processing_status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= ?)
		  AND processing_step != 'none'
		ORDER BY created LIMIT ?`, fmtTime(now.UTC()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EarliestRetry returns the soonest future next_retry_at among pending,
// discoverable notes (next wake-up deadline for the worker's sleep,
// DESIGN.md 6 rule 2), or nil when no pending note carries a timer.
func (x *Index) EarliestRetry(now time.Time) (*time.Time, error) {
	var raw string
	err := x.db.QueryRow(`SELECT next_retry_at FROM notes
		WHERE processing_status = 'pending' AND next_retry_at IS NOT NULL
		  AND next_retry_at > ? AND processing_step != 'none'
		ORDER BY next_retry_at LIMIT 1`, fmtTime(now.UTC())).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(timeFmt, raw)
	if err != nil {
		return nil, fmt.Errorf("index: bad next_retry_at %q: %w", raw, err)
	}
	u := t.UTC()
	return &u, nil
}

// --- helpers -----------------------------------------------------------------

func fmtTime(t time.Time) string { return t.UTC().Format(timeFmt) }

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

// escapeLike makes LIKE metacharacters literal in a user-supplied q.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// TagCount is one distinct tag and how many notes carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// Tags lists distinct tags with per-note counts, count desc then tag asc
// for a stable order. Derived from the index only; deleted notes are
// already removed from the tags table by Remove.
func (x *Index) Tags() ([]TagCount, error) {
	rows, err := x.db.Query(
		`SELECT tag, COUNT(DISTINCT note_id) FROM tags
		 GROUP BY tag ORDER BY COUNT(DISTINCT note_id) DESC, tag ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{} // never nil: the API serializes [] not null
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}
