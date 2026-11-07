package notestore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git is the production VCS: every content mutation is one commit in the
// notes repository (DESIGN.md 10). Claims and retry bookkeeping are never
// committed by the store; they ride along into the next content commit.
type Git struct {
	Dir string
}

// serviceIdent keeps commits deterministic and working on machines without
// a global git identity (like CI temp repos). DESIGN.md 12 has no config
// knob for this; the choice is recorded in the DoD report.
const (
	serviceName  = "Lucien"
	serviceEmail = "lucien@localhost"
)

// Init creates the repository (branch main) if notes_dir is not one yet;
// the backend owns its notes directory (flagged design gap in DoD report:
// DESIGN.md never says who runs git init).
func (g *Git) Init() error {
	if _, err := os.Stat(filepath.Join(g.Dir, ".git")); err == nil {
		return nil
	}
	return g.run("init", "-b", "main")
}

// CommitPaths stages exactly the given note-relative paths (deletions
// included, via add -A) and commits. When staging produced no change the
// commit is skipped: history is content history (DESIGN.md 10).
func (g *Git) CommitPaths(paths []string, message string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := g.run(append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return err
	}
	err := g.run("-c", "user.name="+serviceName, "-c", "user.email="+serviceEmail,
		"commit", "-m", message)
	if err != nil && strings.Contains(err.Error(), "nothing to commit") {
		return nil
	}
	return err
}

func (g *Git) run(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", g.Dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}
