package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"lucien/internal/notestore"
)

func ts(sec int) *time.Time {
	v := base.Add(time.Duration(sec) * time.Second)
	return &v
}

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// mkNote builds a valid note with the given entries (text of one entry each).
func mkNote(id string, createdOffset int, entries ...string) *notestore.Note {
	n := &notestore.Note{
		ID:             id,
		Created:        base.Add(time.Duration(createdOffset) * time.Second),
		Modified:       base.Add(time.Duration(createdOffset) * time.Second),
		State:          "inbox",
		StateChangedAt: base,
		Tags:           []notestore.Tag{},
		Source:         notestore.Source{Kind: "text"},
		Processing:     notestore.Processing{Step: notestore.StepTag, Status: notestore.StatusPending},
		Dedup:          notestore.Dedup{Resaved: []time.Time{}},
	}
	for i, e := range entries {
		n.Entries = append(n.Entries, notestore.Entry{ID: i + 1, TS: n.Created, Kind: "text", Text: e})
	}
	n.Dedup.ContentHash = notestore.ContentHash(n.Entries)
	return n
}

func openIndex(t *testing.T) *Index {
	t.Helper()
	x, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func TestOpenCreatesSchema(t *testing.T) {
	x := openIndex(t)
	db, err := sql.Open("sqlite", x.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	// All four DESIGN.md 9 tables must exist, FTS5 included.
	for _, want := range []string{"notes", "tags", "entries_fts", "embeddings"} {
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, want).Scan(&name)
		if err != nil || name != want {
			t.Fatalf("table %s missing: %v", want, err)
		}
	}
	// The worker discovery index must exist (DESIGN.md 6 rule 1).
	var cnt int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='notes'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt < 5 {
		t.Fatalf("expected >=5 indexes on notes, got %d", cnt)
	}
}

func TestUpsertQuerySummary(t *testing.T) {
	x := openIndex(t)
	long := strings.Repeat("ä", 250) // runes, to pin 200-char truncation
	n := mkNote("01A", 0, long, "second entry")
	n.State = "todo"
	n.Priority = 3
	n.Tags = []notestore.Tag{{Name: "kernel", By: "agent"}, {Name: "idea", By: "human"}}
	if err := x.Upsert(n); err != nil {
		t.Fatal(err)
	}
	res, err := x.Query(Filters{Now: base})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || len(res.Notes) != 1 {
		t.Fatalf("want 1/1, got %d/%d", res.Total, len(res.Notes))
	}
	s := res.Notes[0]
	if s.ID != "01A" || s.State != "todo" || s.Priority != 3 {
		t.Fatalf("summary mismatch: %+v", s)
	}
	if s.Source.Kind != "text" || s.Proc.Step != "tag" || s.Proc.Status != "pending" {
		t.Fatalf("summary projection mismatch: %+v", s)
	}
	if len(s.Tags) != 2 || s.Tags[0].Name != "kernel" || s.Tags[1].By != "human" {
		t.Fatalf("tags mismatch: %+v", s.Tags)
	}
	if len([]rune(s.Snippet)) != 200 {
		t.Fatalf("snippet not 200 runes: %d", len([]rune(s.Snippet)))
	}
	if !strings.HasPrefix(s.Snippet, "ä") {
		t.Fatalf("snippet not from first entry: %.20q", s.Snippet)
	}
	if !s.Created.Equal(n.Created) || !s.Modified.Equal(n.Modified) {
		t.Fatalf("timestamps mismatch: %+v", s)
	}
}

func TestUpsertReplacesPriorRows(t *testing.T) {
	x := openIndex(t)
	n := mkNote("01A", 0, "alpha", "beta")
	n.Tags = []notestore.Tag{{Name: "old", By: "human"}}
	if err := x.Upsert(n); err != nil {
		t.Fatal(err)
	}
	// Mutate as a real content change would.
	n.State = "done"
	n.Tags = []notestore.Tag{{Name: "new", By: "agent"}}
	n.Entries = []notestore.Entry{{ID: 1, TS: n.Created, Kind: "text", Text: "gamma only"}}
	if err := x.Upsert(n); err != nil {
		t.Fatal(err)
	}
	res, err := x.Query(Filters{Now: base})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Fatalf("upsert duplicated: total %d", res.Total)
	}
	if res.Notes[0].State != "done" || res.Notes[0].Snippet != "gamma only" {
		t.Fatalf("stale row: %+v", res.Notes[0])
	}
	if len(res.Notes[0].Tags) != 1 || res.Notes[0].Tags[0].Name != "new" {
		t.Fatalf("stale tags: %+v", res.Notes[0].Tags)
	}
	// Old entry text must be gone from search.
	res, err = x.Query(Filters{Q: "alpha", Now: base})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Fatalf("stale entry text still searchable")
	}
}

func TestRemove(t *testing.T) {
	x := openIndex(t)
	if err := x.Upsert(mkNote("01A", 0, "one")); err != nil {
		t.Fatal(err)
	}
	if err := x.Upsert(mkNote("01B", 0, "two")); err != nil {
		t.Fatal(err)
	}
	if err := x.Remove("01A"); err != nil {
		t.Fatal(err)
	}
	res, _ := x.Query(Filters{Now: base})
	if res.Total != 1 || res.Notes[0].ID != "01B" {
		t.Fatalf("remove failed: %+v", res)
	}
	if err := x.Remove("01A"); err != nil { // idempotent
		t.Fatalf("double remove: %v", err)
	}
}

func TestFilters(t *testing.T) {
	x := openIndex(t)
	// A: inbox, tag work, old, pending
	a := mkNote("A", -48*3600, "buy milk", "more milk")
	a.Tags = []notestore.Tag{{Name: "work", By: "human"}, {Name: "urgent", By: "agent"}}
	// B: todo, tags work+home, mid, running
	b := mkNote("B", -10*3600, "fix kernel panic")
	b.State = "todo"
	b.Tags = []notestore.Tag{{Name: "work", By: "human"}, {Name: "home", By: "human"}}
	b.Processing = notestore.Processing{Step: notestore.StepTranscribe, Status: notestore.StatusRunning}
	// C: inbox, fresh, failed, reminder in the past (not done/archived, so
	// remind_due surfaces it)
	c := mkNote("C", -3600, "annual report final")
	c.State = "inbox"
	c.Processing = notestore.Processing{Step: notestore.StepEmbed, Status: notestore.StatusFailed}
	c.RemindAt = ts(-1)
	// D: archived with a past reminder and past defer
	d := mkNote("D", -60, "deferred thing 100% sure")
	d.State = "archived"
	d.RemindAt = ts(-1)
	d.DeferUntil = ts(-1)
	// E: inbox with future reminder/defer
	e := mkNote("E", 0, "later")
	e.RemindAt = ts(3600)
	e.DeferUntil = ts(3600)
	for _, n := range []*notestore.Note{a, b, c, d, e} {
		if err := x.Upsert(n); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(f Filters) string {
		t.Helper()
		f.Now = base
		res, err := x.Query(f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(res.Notes))
		for i, s := range res.Notes {
			out[i] = s.ID
		}
		return strings.Join(out, ",")
	}
	count := func(f Filters) int {
		t.Helper()
		f.Now = base
		res, err := x.Query(f)
		if err != nil {
			t.Fatal(err)
		}
		return res.Total
	}

	if got := ids(Filters{States: []string{"inbox", "todo"}}); got != "E,C,B,A" { // modified DESC
		t.Fatalf("states: %s", got)
	}
	if got := ids(Filters{Tags: []string{"work"}}); got != "B,A" {
		t.Fatalf("tag work: %s", got)
	}
	if got := ids(Filters{Tags: []string{"work", "home"}}); got != "B" { // AND-combined
		t.Fatalf("tags AND: %s", got)
	}
	if got := ids(Filters{Q: "milk"}); got != "A" {
		t.Fatalf("q: %s", got)
	}
	if got := ids(Filters{Q: "100% sure"}); got != "D" { // LIKE metacharacters literal
		t.Fatalf("q literal: %s", got)
	}
	if got := ids(Filters{Q: "a_b"}); got != "" { // '_' is not a wildcard in q
		t.Fatalf("q underscore: %q", got)
	}
	if got := ids(Filters{MinAge: 12 * time.Hour}); got != "A" { // only A is >12h old
		t.Fatalf("min_age: %s", got)
	}
	if got := ids(Filters{MaxAge: 2 * time.Hour}); got != "E,D,C" {
		t.Fatalf("max_age: %s", got)
	}
	if got := ids(Filters{MinAge: 8 * time.Hour, MaxAge: 40 * time.Hour}); got != "B" { // B is 10h old
		t.Fatalf("age window: %s", got)
	}
	if got := ids(Filters{ModifiedSince: &[]time.Time{base.Add(-2 * time.Hour)}[0]}); got != "E,D,C" {
		t.Fatalf("modified_since: %s", got)
	}
	if got := ids(Filters{RemindDue: true}); got != "C" { // D excluded: archived
		t.Fatalf("remind_due: %s", got)
	}
	if got := ids(Filters{DeferDue: true}); got != "D" {
		t.Fatalf("defer_due: %s", got)
	}
	if got := ids(Filters{ProcessingStatus: "failed"}); got != "C" {
		t.Fatalf("processing_status: %s", got)
	}
	if got := ids(Filters{ProcessingStatus: "running"}); got != "B" {
		t.Fatalf("processing_status running: %s", got)
	}
	if got := count(Filters{States: []string{"inbox", "todo", "done", "archived"}}); got != 5 {
		t.Fatalf("total: %d", got)
	}
}

func TestLimitOffsetTotal(t *testing.T) {
	x := openIndex(t)
	for i := 0; i < 5; i++ {
		if err := x.Upsert(mkNote(fmt.Sprintf("N%d", i), i)); err != nil {
			t.Fatal(err)
		}
	}
	f := Filters{Now: base, Limit: 2}
	res, err := x.Query(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 5 || len(res.Notes) != 2 {
		t.Fatalf("limit: %d/%d", res.Total, len(res.Notes))
	}
	if res.Notes[0].ID != "N4" { // modified DESC default
		t.Fatalf("default order: %s", res.Notes[0].ID)
	}
	f.Offset = 4
	res, _ = x.Query(f)
	if len(res.Notes) != 1 || res.Notes[0].ID != "N0" {
		t.Fatalf("offset: %+v", res.Notes)
	}
	f.Offset = 9
	res, _ = x.Query(f)
	if len(res.Notes) != 0 || res.Total != 5 {
		t.Fatalf("offset past end: %+v", res)
	}
	f.Offset = 0
	f.Limit = 100000 // clamped to 1000
	if res, err = x.Query(f); err != nil || res.Total != 5 {
		t.Fatalf("clamp: %v %d", err, res.Total)
	}
}

func TestDiscoverDue(t *testing.T) {
	x := openIndex(t)
	// Due now: pending with null next_retry_at, oldest first.
	old := mkNote("OLD", -100)
	mid := mkNote("MID", -50)
	// Not due: future next_retry_at.
	later := mkNote("LATER", 0)
	later.Processing.NextRetryAt = ts(3600)
	// Not due: running and failed.
	run := mkNote("RUN", 0)
	run.Processing.Status = notestore.StatusRunning
	fail := mkNote("FAIL", 0)
	fail.Processing.Status = notestore.StatusFailed
	// Rest state: step none, status pending — must NOT be discovered.
	rest := mkNote("REST", 0)
	rest.Processing = notestore.Processing{Step: notestore.StepNone, Status: notestore.StatusPending}
	for _, n := range []*notestore.Note{old, mid, later, run, fail, rest} {
		if err := x.Upsert(n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := x.DiscoverDue(base, 32)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "OLD,MID" {
		t.Fatalf("discovery: %v", got)
	}
	// When its retry time arrives, LATER becomes due.
	if _, err := x.DiscoverDue(base, 32); err != nil {
		t.Fatal(err)
	}
	later.Processing.NextRetryAt = ts(-1)
	if err := x.Upsert(later); err != nil {
		t.Fatal(err)
	}
	got, _ = x.DiscoverDue(base, 32)
	if strings.Join(got, ",") != "OLD,MID,LATER" {
		t.Fatalf("discovery after due: %v", got)
	}
	got, _ = x.DiscoverDue(base, 1)
	if len(got) != 1 || got[0] != "OLD" {
		t.Fatalf("limit: %v", got)
	}
}

func TestReindexRebuilds(t *testing.T) {
	x := openIndex(t)
	for i := 0; i < 3; i++ {
		if err := x.Upsert(mkNote(fmt.Sprintf("N%d", i), i, fmt.Sprintf("text %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate index drift: a row that exists in no file.
	if err := x.Upsert(mkNote("GHOST", 0, "phantom")); err != nil {
		t.Fatal(err)
	}
	notes := []*notestore.Note{mkNote("N0", 0, "text 0"), mkNote("N1", 1, "text 1"), mkNote("N2", 2, "text 2")}
	// N2 changed on disk since it was last upserted.
	notes[2].State = "done"
	n, err := x.Reindex(notes)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("reindexed count: %d", n)
	}
	res, _ := x.Query(Filters{Now: base})
	if res.Total != 3 {
		t.Fatalf("ghost survived reindex: %+v", res)
	}
	for _, s := range res.Notes {
		if s.ID == "N2" && s.State != "done" {
			t.Fatalf("reindex kept stale row: %+v", s)
		}
	}
	if res, _ = x.Query(Filters{Q: "phantom", Now: base}); res.Total != 0 {
		t.Fatalf("ghost entry text survived")
	}
}

// TestStoreMutationPath proves DESIGN.md 9: the Note Store applies every
// mutation to the index transactionally with the file write.
func TestStoreMutationPath(t *testing.T) {
	dir := t.TempDir()
	x := openIndexAt(t, filepath.Join(dir, "index.sqlite"))
	vcs := &notestore.NullVCS{}
	s := notestore.New(dir, vcs, x, nil)
	clock := base
	s.SetClock(func() time.Time { return clock })
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}

	n, err := s.Create(notestore.CreateRequest{SourceKind: "text", Text: "hello world", Tags: []notestore.Tag{{Name: "greet", By: "human"}}})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := x.Query(Filters{Now: base})
	if res.Total != 1 || res.Notes[0].ID != n.ID || res.Notes[0].Snippet != "hello world" {
		t.Fatalf("create not indexed: %+v", res)
	}

	clock = clock.Add(time.Minute)
	if _, err := s.AppendText(n.ID, "more text"); err != nil {
		t.Fatal(err)
	}
	res, _ = x.Query(Filters{Q: "more", Now: base})
	if res.Total != 1 {
		t.Fatalf("append not indexed: %+v", res)
	}

	clock = clock.Add(time.Minute)
	state := "todo"
	if _, err := s.Edit(n.ID, notestore.Patch{State: &state}, clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	res, _ = x.Query(Filters{States: []string{"todo"}, Now: base})
	if res.Total != 1 || res.Notes[0].Modified.Equal(base) {
		t.Fatalf("edit not indexed: %+v", res)
	}

	if err := s.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	res, _ = x.Query(Filters{Now: base})
	if res.Total != 0 {
		t.Fatalf("delete not removed from index: %+v", res)
	}
}

func openIndexAt(t *testing.T, path string) *Index {
	t.Helper()
	x, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

// TestStoreAll feeds Reindex from files: notes planted through the store are
// listed by All in created order and survive a full index wipe.
func TestStoreAll(t *testing.T) {
	dir := t.TempDir()
	x := openIndexAt(t, filepath.Join(dir, "index.sqlite"))
	s := notestore.New(dir, &notestore.NullVCS{}, x, nil)
	clock := base
	s.SetClock(func() time.Time { return clock })
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	n1, _ := s.Create(notestore.CreateRequest{SourceKind: "text", Text: "one"})
	clock = clock.Add(time.Minute)
	n2, _ := s.Create(notestore.CreateRequest{SourceKind: "text", Text: "two"})
	// A trashed note must not come back through All.
	if err := s.Delete(n2.ID); err != nil {
		t.Fatal(err)
	}
	notes, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].ID != n1.ID {
		t.Fatalf("All: %+v", notes)
	}
	if _, err := x.Reindex(notes); err != nil {
		t.Fatal(err)
	}
	res, _ := x.Query(Filters{Now: base})
	if res.Total != 1 || res.Notes[0].ID != n1.ID {
		t.Fatalf("reindex from files: %+v", res)
	}

	// A corrupt file must not block reindex: All reports it, healthy notes
	// still rebuild.
	bad := filepath.Join(dir, "01BAD.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	notes, err = s.All()
	if err == nil || !strings.Contains(err.Error(), "01BAD.json") {
		t.Fatalf("corrupt file not reported: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != n1.ID {
		t.Fatalf("healthy notes lost: %+v", notes)
	}
}

func TestEarliestRetry(t *testing.T) {
	x := openIndex(t)
	upsert := func(id, step, status string, retryOff int) {
		n := mkNote(id, 0)
		n.Processing = notestore.Processing{Step: step, Status: status}
		if retryOff != 0 {
			n.Processing.NextRetryAt = ts(retryOff)
		}
		if err := x.Upsert(n); err != nil {
			t.Fatal(err)
		}
	}
	upsert("SOON", notestore.StepTranscribe, notestore.StatusPending, 5)
	upsert("LATE", notestore.StepTranscribe, notestore.StatusPending, 60)
	upsert("PASTDUE", notestore.StepTranscribe, notestore.StatusPending, -5) // due: next pass handles it
	upsert("NULLR", notestore.StepTranscribe, notestore.StatusPending, 0)    // no timer to schedule
	upsert("RUNT", notestore.StepTranscribe, notestore.StatusRunning, 10)    // not pending: no timer
	upsert("FAILT", notestore.StepTranscribe, notestore.StatusFailed, 10)
	upsert("RESTT", notestore.StepNone, notestore.StatusPending, 10)         // rest: never discovered

	got, err := x.EarliestRetry(base)
	if err != nil {
		t.Fatalf("earliest: %v", err)
	}
	if got == nil || !got.Equal(base.Add(5*time.Second)) {
		t.Fatalf("want base+5s, got %v", got)
	}

	// With the pending timer notes gone no wakeup remains: NULLR has no
	// timer, RUNT/FAILT are not pending, RESTT is the rest state.
	x.Remove("SOON")
	x.Remove("LATE")
	x.Remove("PASTDUE")
	got, err = x.EarliestRetry(base)
	if err != nil || got != nil {
		t.Fatalf("want nil, got %v (%v)", got, err)
	}
}
