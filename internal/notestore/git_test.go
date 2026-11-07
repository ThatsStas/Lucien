package notestore

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// gitStore builds a Store backed by a REAL git repository in a temp dir
// (IMPLEMENTATION-FRAMEWORK.md: VCS seam tested against real git, one
// fresh repo per test).
func gitStore(t *testing.T) (*Store, string, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	s := New(dir, &Git{Dir: dir}, &NullIndex{}, nil)
	s.SetClock(func() time.Time { return now })
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s, dir, &now
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func subjects(t *testing.T, dir string) []string {
	t.Helper()
	out := gitOut(t, dir, "log", "--format=%s")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n") // newest first
}

func mustRead(t *testing.T, s *Store, id string) *Note {
	t.Helper()
	n, err := s.Read(id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return n
}

func TestGit_InitCreatesRepoOnMain_AndReinitKeepsHistory(t *testing.T) {
	s, dir, _ := gitStore(t)
	if branch := gitOut(t, dir, "symbolic-ref", "--short", "HEAD"); branch != "main" {
		t.Errorf("default branch = %q, want main", branch)
	}
	n := mustCreateText(t, s, "kept")
	// Second Init (backend restart) must not re-init or wipe history.
	s2 := New(dir, &Git{Dir: dir}, &NullIndex{}, nil)
	if err := s2.Init(); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if got := subjects(t, dir); len(got) != 1 || got[0] != "add note "+n.ID {
		t.Errorf("history after re-init = %v", got)
	}
}

func TestGit_CommitPerMutation_AndLogGrep(t *testing.T) {
	s, dir, now := gitStore(t)
	n1 := mustCreateText(t, s, "first note")
	advance(now, time.Minute)
	n2 := mustCreateText(t, s, "second note")

	want := []string{"add note " + n2.ID, "add note " + n1.ID}
	if got := subjects(t, dir); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("subjects = %v, want %v", got, want)
	}

	// DESIGN 10: git log --grep <id> reconstructs one note's life.
	s.AppendText(n1.ID, "more")
	cur := mustRead(t, s, n1.ID)
	advance(now, time.Minute)
	newState := "todo"
	if _, err := s.Edit(n1.ID, Patch{State: &newState}, cur.Modified); err != nil {
		t.Fatalf("state edit: %v", err)
	}
	if err := s.Delete(n2.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got := strings.Split(gitOut(t, dir, "log", "--format=%s", "--grep", n1.ID), "\n")
	wantN1 := []string{"state " + n1.ID + " todo", "append " + n1.ID, "add note " + n1.ID}
	if strings.Join(got, "|") != strings.Join(wantN1, "|") {
		t.Errorf("log --grep %s = %v, want %v", n1.ID, got, wantN1)
	}
}

func TestGit_ClaimAndFailureProduceNoCommits_ContentCommitsDo(t *testing.T) {
	s, dir, now := gitStore(t)
	n, err := s.Create(CreateRequest{SourceKind: "audio",
		Audio: strings.NewReader("A"), AudioExt: "m4a"})
	if err != nil {
		t.Fatal(err)
	}
	before := subjects(t, dir)

	// Claim: file changed on disk, but NOT committed (DESIGN 10: claims
	// ride along into the next content commit).
	if _, err := s.Claim(n.ID); err != nil {
		t.Fatal(err)
	}
	if after := subjects(t, dir); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Errorf("claim created commits: %v", after)
	}

	// Retry bookkeeping likewise stays out of history (DESIGN 10).
	advance(now, time.Minute)
	next := now.Add(time.Minute)
	if _, err := s.FailStep(n.ID, "transcribe", 1, &next, "endpoint down"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if after := subjects(t, dir); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Errorf("fail bookkeeping created commits: %v", after)
	}

	// The content mutation commits, and the claim plus bookkeeping ride
	// along into it: working tree ends clean.
	if _, err := s.Claim(n.ID); err != nil { // pending -> running again
		t.Fatal(err)
	}
	if _, err := s.CompleteStep(n.ID, "transcribe", func(n *Note) {
		n.Entries = append(n.Entries, Entry{ID: 1, Kind: "transcript", Text: "hello"})
	}, "transcribe "+n.ID); err != nil {
		t.Fatal(err)
	}
	if subs := subjects(t, dir); subs[0] != "transcribe "+n.ID {
		t.Errorf("newest commit = %q, want transcribe %s", subs[0], n.ID)
	}
	if st := gitOut(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("working tree not clean after content commit: %q", st)
	}
}

func TestGit_DeleteStagesBothSidesOfMove(t *testing.T) {
	s, dir, _ := gitStore(t)
	n := mustCreateText(t, s, "doomed")
	if err := s.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	files := gitOut(t, dir, "ls-files")
	if !strings.Contains(files, ".trash/"+n.ID+".json") {
		t.Errorf("trash copy not tracked:\n%s", files)
	}
	for _, line := range strings.Split(files, "\n") {
		if line == n.ID+".json" {
			t.Errorf("original still tracked:\n%s", files)
		}
	}
	if st := gitOut(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("working tree not clean after delete commit: %q", st)
	}
}

func TestGit_NoOpEditCreatesNoCommit(t *testing.T) {
	s, dir, _ := gitStore(t)
	n := mustCreateText(t, s, "x")
	before := subjects(t, dir)
	// Same value back: accepted, but history stays content history (10).
	if _, err := s.Edit(n.ID, Patch{Priority: intPtr(0)}, n.Modified); err != nil {
		t.Fatalf("no-op edit: %v", err)
	}
	if after := subjects(t, dir); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Errorf("no-op edit created commits: %v", after)
	}
	// And the file on disk kept its modified stamp (no silent bump).
	after := mustRead(t, s, n.ID)
	if !after.Modified.Equal(n.Modified) {
		t.Errorf("no-op edit bumped modified: %v vs %v", after.Modified, n.Modified)
	}
}
