package notestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// testStore builds a Store on a temp dir with a fixed clock, null git and a
// recording null index. Determinism per IMPLEMENTATION-FRAMEWORK.md 4: every
// timestamp comes from the injected clock, never from wall time.
func testStore(t *testing.T) (*Store, *NullVCS, *NullIndex, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	vcs := &NullVCS{}
	idx := &NullIndex{}
	s := New(dir, vcs, idx, nil)
	s.SetClock(func() time.Time { return now })
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s, vcs, idx, &now
}

func advance(p *time.Time, d time.Duration) { *p = p.Add(d) }

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustCreateText(t *testing.T, s *Store, text string) *Note {
	t.Helper()
	n, err := s.Create(CreateRequest{SourceKind: "text", Text: text})
	if err != nil {
		t.Fatalf("create text: %v", err)
	}
	return n
}

func rawFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func TestCreateTextNote_WritesSchemaFileAndCommits(t *testing.T) {
	s, vcs, idx, _ := testStore(t)
	n := mustCreateText(t, s, "hello world")

	// ULID: 26-char Crockford base32, parseable, backend-generated.
	if _, err := ulid.Parse(n.ID); err != nil {
		t.Fatalf("id %q is not a valid ULID: %v", n.ID, err)
	}

	m := rawFile(t, filepath.Join(s.dir, n.ID+".json"))
	for _, key := range []string{
		"id", "created", "modified", "state", "state_changed_at", "tags",
		"priority", "source", "entries", "processing", "remind_at",
		"defer_until", "dedup",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("schema: key %q missing from note file", key)
		}
	}
	// Nullables serialize as JSON null (DESIGN 3.2 shows them explicit).
	for _, key := range []string{"remind_at", "defer_until"} {
		if m[key] != nil {
			t.Errorf("%s = %v, want null", key, m[key])
		}
	}

	if n.Created.Equal(n.Modified) == false {
		t.Error("fresh note: created should equal modified")
	}
	if n.State != "inbox" {
		t.Errorf("state = %q, want inbox", n.State)
	}
	if n.Source.Kind != "text" {
		t.Errorf("source.kind = %q", n.Source.Kind)
	}
	if len(n.Entries) != 1 || n.Entries[0].Kind != "text" || n.Entries[0].ID != 1 ||
		n.Entries[0].Text != "hello world" {
		t.Errorf("entries = %+v", n.Entries)
	}
	// Chain per DESIGN 4: text starts at tag, pending.
	if n.Processing.Step != "tag" || n.Processing.Status != "pending" ||
		n.Processing.RetryCount != 0 || n.Processing.NextRetryAt != nil {
		t.Errorf("processing = %+v, want tag/pending/0/nil", n.Processing)
	}
	// content_hash = sha256 of entry texts joined by \n, "sha256:" prefix.
	if want := sha("hello world"); n.Dedup.ContentHash != want {
		t.Errorf("content_hash = %s, want %s", n.Dedup.ContentHash, want)
	}
	if n.Dedup.Resaved == nil {
		t.Error("dedup.resaved must serialize as [] not null")
	}

	// Timestamps are ISO 8601 UTC second precision (no fractional digits).
	data, _ := os.ReadFile(filepath.Join(s.dir, n.ID+".json"))
	if !strings.Contains(string(data), `"created": "2026-01-01T12:00:00Z"`) {
		t.Errorf("created timestamp not ISO-8601-second in:\n%s", data)
	}

	if len(vcs.Commits) != 1 || vcs.Commits[0].Message != "add note "+n.ID {
		t.Errorf("commits = %+v, want one 'add note %s'", vcs.Commits, n.ID)
	}
	if len(idx.Upserts) != 1 || idx.Upserts[len(idx.Upserts)-1].ID != n.ID {
		t.Errorf("index upserts = %+v", idx.Upserts)
	}
}

func TestCreateAudioNote_WritesBufferAndChainsAtTranscribe(t *testing.T) {
	s, vcs, _, _ := testStore(t)
	n, err := s.Create(CreateRequest{
		SourceKind: "audio", Audio: strings.NewReader("AUDIO-BYTES"), AudioExt: "m4a",
	})
	if err != nil {
		t.Fatalf("create audio: %v", err)
	}
	if n.Processing.Step != "transcribe" || n.Processing.Status != "pending" {
		t.Errorf("processing = %+v, want transcribe/pending", n.Processing)
	}
	audio, err := os.ReadFile(filepath.Join(s.dir, n.ID+".1.m4a"))
	if err != nil || string(audio) != "AUDIO-BYTES" {
		t.Fatalf("audio buffer %q err %v", audio, err)
	}
	if len(n.Entries) != 0 {
		t.Errorf("audio note before transcription should have no entries: %+v", n.Entries)
	}
	// Empty body hashes deterministically (empty string).
	if want := sha(""); n.Dedup.ContentHash != want {
		t.Errorf("content_hash = %s, want %s", n.Dedup.ContentHash, want)
	}
	// Both files land in the one commit (DESIGN 5.1 step 3).
	if len(vcs.Commits) != 1 || len(vcs.Commits[0].Paths) != 2 {
		t.Errorf("commits = %+v, want one commit with 2 paths", vcs.Commits)
	}
}

func TestCreateRejectsBadInput_LeavesNoPartialState(t *testing.T) {
	s, vcs, _, _ := testStore(t)

	if _, err := s.Create(CreateRequest{SourceKind: "text", Text: "  "}); err == nil {
		t.Error("empty text accepted")
	}
	if _, err := s.Create(CreateRequest{SourceKind: "notebook", Text: "x"}); err == nil {
		t.Error("unknown source kind accepted")
	}
	if _, err := s.Create(CreateRequest{SourceKind: "text", Text: "x", State: "someday-ish"}); err == nil {
		t.Error("invalid state accepted")
	}
	if _, err := s.Create(CreateRequest{SourceKind: "text", Text: "x", Priority: 42}); err == nil {
		t.Error("priority out of -20..19 accepted")
	}
	if _, err := s.Create(CreateRequest{SourceKind: "text", Text: "x",
		Tags: []Tag{{Name: "a", By: "robot"}}}); err == nil {
		t.Error("tag by=robot accepted")
	}
	if _, err := s.Create(CreateRequest{SourceKind: "audio", Audio: strings.NewReader("x"),
		AudioExt: "exe"}); !errors.Is(err, ErrUnsupportedAudio) {
		t.Errorf("bad audio ext: got %v, want ErrUnsupportedAudio", err)
	}
	if _, err := s.Create(CreateRequest{SourceKind: "audio"}); err == nil {
		t.Error("audio note without reader accepted")
	}

	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") || strings.Contains(e.Name(), ".m4a") ||
			strings.Contains(e.Name(), ".tmp") {
			t.Errorf("rejected create left residue: %s", e.Name())
		}
	}
	if len(vcs.Commits) != 0 {
		t.Errorf("rejected creates committed: %+v", vcs.Commits)
	}
}

func TestRead_RoundTripAndNotFound(t *testing.T) {
	s, _, _, _ := testStore(t)
	n := mustCreateText(t, s, "round trip")

	got, err := s.Read(n.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != n.ID || got.Entries[0].Text != "round trip" ||
		got.Dedup.ContentHash != n.Dedup.ContentHash {
		t.Errorf("round trip lost data: %+v", got)
	}

	if _, err := s.Read("01JZK4QW8V0F3M2N1P8R7T6X5Y"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}

	// A file whose id does not match its name is corrupt, not silently wrong.
	os.WriteFile(filepath.Join(s.dir, "01JZK4QW8V0F3M2N1P8R7T6X5Y.json"),
		[]byte(`{"id":"01JZK4QW8V0F3M2N1P8R7T6X5Z"}`), 0o600)
	if _, err := s.Read("01JZK4QW8V0F3M2N1P8R7T6X5Y"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("id-mismatched file: got %v, want corruption error", err)
	}
}

func TestAppendText_AddsEntryAndReEntersChainAtEmbed(t *testing.T) {
	s, vcs, idx, now := testStore(t)
	n := mustCreateText(t, s, "first")
	advance(now, time.Minute)

	got, err := s.AppendText(n.ID, "second")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %+v", got.Entries)
	}
	e := got.Entries[1]
	if e.ID != 2 || e.Kind != "text" || e.Text != "second" {
		t.Errorf("appended entry = %+v, want id 2 kind text", e)
	}
	// DESIGN 5.3: text append re-enters at embed only (no re-tagging).
	if got.Processing.Step != "embed" || got.Processing.Status != "pending" ||
		got.Processing.RetryCount != 0 {
		t.Errorf("processing = %+v, want embed/pending re-arm", got.Processing)
	}
	if !got.Modified.Equal(*now) || !got.Created.Equal(now.Add(-time.Minute)) {
		t.Errorf("created/modified = %v/%v", got.Created, got.Modified)
	}
	if want := sha("first\nsecond"); got.Dedup.ContentHash != want {
		t.Errorf("content_hash = %s, want %s", got.Dedup.ContentHash, want)
	}
	last := vcs.Commits[len(vcs.Commits)-1]
	if last.Message != "append "+n.ID {
		t.Errorf("commit = %q, want append %s", last.Message, n.ID)
	}
	if len(idx.Upserts) != 2 {
		t.Errorf("index upserts = %d, want 2", len(idx.Upserts))
	}
	if _, err := s.AppendText("01JZK4QW8V0F3M2N1P8R7T6X5Y", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("append to unknown: got %v, want ErrNotFound", err)
	}
}

func TestAppendAudio_NumberedBufferAndReEnterTranscribe(t *testing.T) {
	s, vcs, _, _ := testStore(t)
	n, err := s.Create(CreateRequest{SourceKind: "audio",
		Audio: strings.NewReader("A1"), AudioExt: "m4a"})
	if err != nil {
		t.Fatal(err)
	}
	// Force chain out of transcribe to prove append re-arms it.
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteStep(n.ID, "transcribe", func(n *Note) {
		n.Entries = append(n.Entries, Entry{ID: 1, Kind: "transcript", Text: "one"})
	}, "transcribe "+n.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.AppendAudio(n.ID, strings.NewReader("A2"), "m4a")
	if err != nil {
		t.Fatalf("append audio: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, n.ID+".2.m4a")); err != nil {
		t.Fatalf("second buffer missing: %v", err)
	}
	if got.Processing.Step != "transcribe" || got.Processing.Status != "pending" ||
		got.Processing.RetryCount != 0 || got.Processing.NextRetryAt != nil {
		t.Errorf("processing = %+v, want transcribe/pending re-armed", got.Processing)
	}
	if last := vcs.Commits[len(vcs.Commits)-1]; last.Message != "append "+n.ID {
		t.Errorf("commit = %q, want append %s", last.Message, n.ID)
	}
	// Unsupported ext leaves no third buffer.
	if _, err := s.AppendAudio(n.ID, strings.NewReader("x"), "avi"); !errors.Is(err, ErrUnsupportedAudio) {
		t.Errorf("got %v, want ErrUnsupportedAudio", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, n.ID+".3.m4a")); err == nil {
		t.Error("rejected append left buffer .3")
	}
}

func TestEdit_StaleBaseModifiedReturnsCurrentNote(t *testing.T) {
	s, _, _, now := testStore(t)
	n := mustCreateText(t, s, "body")
	advance(now, time.Minute)
	if _, err := s.Edit(n.ID, Patch{Priority: intPtr(5)}, n.Modified); err != nil {
		t.Fatalf("edit: %v", err)
	}
	advance(now, time.Minute)

	// Stale base_modified (client still holds the pre-edit value): 409 shape
	// per DESIGN 5.7 — sentinel plus the FULL current note for the client.
	stale, err := s.Edit(n.ID, Patch{Priority: intPtr(7)}, n.Modified)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("stale edit: got %v, want ErrStale", err)
	}
	if stale == nil || stale.Priority != 5 {
		t.Fatalf("stale edit must return current note, got %+v", stale)
	}
}

func TestEdit_AppliesFieldsAndCommits(t *testing.T) {
	s, vcs, _, now := testStore(t)
	n := mustCreateText(t, s, "body one")
	if _, err := s.AppendText(n.ID, "shared"); err != nil {
		t.Fatalf("append: %v", err)
	}
	cur := mustRead(t, s, n.ID)
	advance(now, time.Minute)

	newState := "done"
	got, err := s.Edit(n.ID, Patch{State: &newState}, cur.Modified)
	if err != nil {
		t.Fatalf("state edit: %v", err)
	}
	if got.State != "done" {
		t.Errorf("state = %q", got.State)
	}
	if !got.StateChangedAt.Equal(*now) {
		t.Errorf("state_changed_at not bumped on state change: %v", got.StateChangedAt)
	}
	if last := vcs.Commits[len(vcs.Commits)-1]; last.Message != "state "+n.ID+" done" {
		t.Errorf("commit = %q, want 'state %s done'", last.Message, n.ID)
	}

	// Non-state edit commits "edit <id>"; tags replace wholesale.
	advance(now, time.Minute)
	got, err = s.Edit(n.ID, Patch{
		Tags:        &[]Tag{{Name: "idea", By: "human"}},
		SetRemindAt: true,
		RemindAt:    now,
	}, got.Modified)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0].Name != "idea" || got.Tags[0].By != "human" {
		t.Errorf("tags = %+v", got.Tags)
	}
	if got.RemindAt == nil || !got.RemindAt.Equal(*now) {
		t.Errorf("remind_at = %v", got.RemindAt)
	}
	if last := vcs.Commits[len(vcs.Commits)-1]; last.Message != "edit "+n.ID {
		t.Errorf("commit = %q, want edit %s", last.Message, n.ID)
	}

	// entry_edit replaces one entry by id, keeps the others, rehashes.
	advance(now, time.Minute)
	got, err = s.Edit(n.ID, Patch{EntryEdit: &EntryEdit{ID: 1, Text: "edited"}}, got.Modified)
	if err != nil {
		t.Fatalf("entry_edit: %v", err)
	}
	if got.Entries[0].Text != "edited" {
		t.Errorf("entry not replaced: %+v", got.Entries)
	}
	if want := sha("edited\nshared"); got.Dedup.ContentHash != want {
		t.Errorf("content_hash = %s, want %s", got.Dedup.ContentHash, want)
	}
	// Unknown entry id: not-found style error, note untouched.
	before, _ := s.Read(n.ID)
	if _, err := s.Edit(n.ID, Patch{EntryEdit: &EntryEdit{ID: 99, Text: "x"}}, got.Modified); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("unknown entry id: got %v, want ErrEntryNotFound", err)
	}
	after, _ := s.Read(n.ID)
	if !after.Modified.Equal(before.Modified) || after.Dedup.ContentHash != before.Dedup.ContentHash {
		t.Error("failed entry_edit mutated the note")
	}
}

func TestEdit_EntryEditBlockedWhileTranscriptionPending(t *testing.T) {
	s, _, _, _ := testStore(t)
	n, err := s.Create(CreateRequest{SourceKind: "audio", Audio: strings.NewReader("A"), AudioExt: "m4a"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed one entry so entry_edit has a target.
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteStep(n.ID, "transcribe", func(n *Note) {
		n.Entries = append(n.Entries, Entry{ID: 1, Kind: "transcript", Text: "t"})
	}, "transcribe "+n.ID); err != nil {
		t.Fatal(err)
	}
	// Re-arm transcribe (as an audio append would) and block the edit.
	if _, err := s.AppendAudio(n.ID, strings.NewReader("B"), "m4a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Edit(n.ID, Patch{EntryEdit: &EntryEdit{ID: 1, Text: "nope"}}, n.Modified); !errors.Is(err, ErrPendingTranscription) {
		t.Fatalf("entry_edit during pending transcription: got %v, want ErrPendingTranscription", err)
	}
	// Non-content edits stay allowed while transcription is pending.
	if _, err := s.Edit(n.ID, Patch{Priority: intPtr(3)}, n.Modified); err != nil {
		t.Errorf("priority edit during pending transcription rejected: %v", err)
	}
}

func TestEdit_Validation(t *testing.T) {
	s, _, _, _ := testStore(t)
	n := mustCreateText(t, s, "x")
	bad := "later"
	if _, err := s.Edit(n.ID, Patch{State: &bad}, n.Modified); err == nil {
		t.Error("invalid state accepted")
	}
	if _, err := s.Edit(n.ID, Patch{Priority: intPtr(-21)}, n.Modified); err == nil {
		t.Error("priority -21 accepted")
	}
	if _, err := s.Edit(n.ID, Patch{Tags: &[]Tag{{Name: "t", By: "bot"}}}, n.Modified); err == nil {
		t.Error("tag by=bot accepted")
	}
	if _, err := s.Edit(n.ID, Patch{}, n.Modified); err != nil {
		t.Errorf("empty patch should be an accepted no-op, got %v", err)
	}
}

func TestDelete_MovesToTrashWithAudio(t *testing.T) {
	s, vcs, idx, _ := testStore(t)
	n, err := s.Create(CreateRequest{SourceKind: "audio", Audio: strings.NewReader("A"), AudioExt: "m4a"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Read(n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: got %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, n.ID+".json")); !os.IsNotExist(err) {
		t.Errorf("json still in notes dir")
	}
	if _, err := os.Stat(filepath.Join(s.dir, n.ID+".1.m4a")); !os.IsNotExist(err) {
		t.Errorf("audio still in notes dir")
	}
	for _, name := range []string{n.ID + ".json", n.ID + ".1.m4a"} {
		if _, err := os.Stat(filepath.Join(s.dir, ".trash", name)); err != nil {
			t.Errorf("%s not moved to .trash: %v", name, err)
		}
	}
	if last := vcs.Commits[len(vcs.Commits)-1]; last.Message != "delete "+n.ID {
		t.Errorf("commit = %q, want delete %s", last.Message, n.ID)
	}
	// The delete commit must stage BOTH sides of the move so git history
	// retains the content (DESIGN 5.10).
	staged := map[string]bool{}
	for _, p := range vcs.Commits[len(vcs.Commits)-1].Paths {
		staged[p] = true
	}
	for _, p := range []string{n.ID + ".json", ".trash/" + n.ID + ".json",
		n.ID + ".1.m4a", ".trash/" + n.ID + ".1.m4a"} {
		if !staged[p] {
			t.Errorf("delete commit missing path %q (staged %v)", p, staged)
		}
	}
	if len(idx.Removed) != 1 || idx.Removed[0] != n.ID {
		t.Errorf("index removals = %v", idx.Removed)
	}
	if err := s.Delete(n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: got %v, want ErrNotFound", err)
	}
}

func TestClaim_NoCommit(t *testing.T) {
	s, vcs, idx, _ := testStore(t)
	n := mustCreateText(t, s, "x")
	claimed, err := s.Claim(n.ID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Processing.Status != "running" {
		t.Errorf("after claim status = %q", claimed.Processing.Status)
	}
	if len(vcs.Commits) != 1 {
		t.Errorf("claim must not commit (DESIGN 10): %+v", vcs.Commits)
	}
	if len(idx.Upserts) != 2 {
		t.Errorf("claim should update index for worker discovery: %d upserts", len(idx.Upserts))
	}
	// Double claim is refused — claims are one-at-a-time per note.
	if _, err := s.Claim(n.ID); err == nil {
		t.Error("double claim accepted")
	}
}

func TestCompleteStep_AdvancesChainAndCommits(t *testing.T) {
	s, vcs, _, now := testStore(t)
	n, err := s.Create(CreateRequest{SourceKind: "audio", Audio: strings.NewReader("A"), AudioExt: "m4a"})
	if err != nil {
		t.Fatal(err)
	}
	advance(now, time.Minute)
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	out, err := s.CompleteStep(n.ID, "transcribe", func(n *Note) {
		n.Entries = append(n.Entries, Entry{ID: 1, Kind: "transcript", Text: "the text"})
	}, "transcribe "+n.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	// DESIGN 4: audio chain transcribe -> tag -> embed -> none.
	if out.Processing.Step != "tag" || out.Processing.Status != "pending" ||
		out.Processing.RetryCount != 0 || out.Processing.NextRetryAt != nil {
		t.Errorf("after transcribe: %+v, want tag/pending", out.Processing)
	}
	if !out.Modified.Equal(*now) || out.Created.Equal(out.Modified) {
		t.Errorf("modified = %v, want now", out.Modified)
	}
	if want := sha("the text"); out.Dedup.ContentHash != want {
		t.Errorf("content_hash = %s", out.Dedup.ContentHash)
	}
	if last := vcs.Commits[len(vcs.Commits)-1]; last.Message != "transcribe "+n.ID {
		t.Errorf("commit = %q", last.Message)
	}

	// Walk the rest of the chain to the rest state step: none.
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	out, err = s.CompleteStep(n.ID, "tag", func(*Note) {}, "tag "+n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Processing.Step != "embed" {
		t.Errorf("after tag: %+v", out.Processing)
	}
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	out, err = s.CompleteStep(n.ID, "embed", func(*Note) {}, "embed "+n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Processing.Step != "none" || out.Processing.Status != "pending" {
		t.Errorf("terminal rest state = %+v, want none/pending", out.Processing)
	}

	// Completing a step that is not the claimed one is refused (idempotency
	// guard: a crashed step replay must not double-advance).
	if _, err := s.CompleteStep(n.ID, "embed", func(*Note) {}, "x"); err == nil {
		t.Error("mismatched complete accepted")
	}
}

func TestFailStep_BookkeepingNoCommit(t *testing.T) {
	s, vcs, _, now := testStore(t)
	n := mustCreateText(t, s, "x")
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	advance(now, time.Minute)
	next := now.Add(time.Minute)
	out, err := s.FailStep(n.ID, "tag", 2, &next, "endpoint down")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if out.Processing.Status != "pending" || out.Processing.RetryCount != 2 ||
		out.Processing.NextRetryAt == nil || !out.Processing.NextRetryAt.Equal(next) ||
		out.Processing.LastError == nil || *out.Processing.LastError != "endpoint down" {
		t.Errorf("retry bookkeeping = %+v", out.Processing)
	}
	if len(vcs.Commits) != 1 {
		t.Errorf("retry bookkeeping must not commit (DESIGN 10): %+v", vcs.Commits)
	}

	// No next_retry_at means the cap was reached: permanent failure.
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	out, err = s.FailStep(n.ID, "tag", 10, nil, "endpoint down")
	if err != nil {
		t.Fatalf("fail terminal: %v", err)
	}
	if out.Processing.Status != "failed" {
		t.Errorf("status = %q, want failed", out.Processing.Status)
	}
}

func TestNoTempResidueAfterMutations(t *testing.T) {
	s, _, _, _ := testStore(t)
	n := mustCreateText(t, s, "a")
	s.AppendText(n.ID, "b")
	s.Edit(n.ID, Patch{Priority: intPtr(1)}, n.Modified)
	s.Delete(n.ID)
	m, _ := filepath.Glob(filepath.Join(s.dir, "*.tmp"))
	m2, _ := filepath.Glob(filepath.Join(s.dir, ".tmp*"))
	if len(m)+len(m2) != 0 {
		t.Errorf("atomic write residue: %v %v", m, m2)
	}
}

func TestChainTable_MatchesDesign(t *testing.T) {
	for kind, want := range map[string]string{
		"audio": "transcribe", "link": "extract", "youtube": "extract",
		"text": "tag", "ribbon": "tag",
	} {
		if got := FirstStep(kind); got != want {
			t.Errorf("FirstStep(%s) = %s, want %s", kind, got, want)
		}
	}
	// Full walks.
	if got := nextStep("audio", "transcribe"); got != "tag" {
		t.Errorf("audio transcribe -> %s", got)
	}
	if got := nextStep("text", "embed"); got != "none" {
		t.Errorf("text embed -> %s", got)
	}
	if got := nextStep("text", "extract"); got != "none" {
		t.Errorf("step not in chain should rest: got %s", got)
	}
}

func intPtr(i int) *int { return &i }
