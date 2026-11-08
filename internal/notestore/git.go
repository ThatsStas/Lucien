package notestore

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Git is the production VCS: every content mutation is one commit in the
// notes repository (DESIGN.md 10). Claims and retry bookkeeping are never
// committed by the store; they ride along into the next content commit.
// Failures log ERROR with the git output — a repo that cannot commit is an
// operator event, a no-op commit is not (it stays at debug).
type Git struct {
	Dir string
	Log *slog.Logger // nil → slog.Default()
}

func (g *Git) logger() *slog.Logger {
	if g.Log == nil {
		return slog.Default()
	}
	return g.Log
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
// DESIGN.md never says who runs git init). Creation is INFO (a boot-time
// fact), failure ERROR: without a repo nothing the backend does is durable
// in history, so the operator must see it.
func (g *Git) Init() error {
	if _, err := os.Stat(filepath.Join(g.Dir, ".git")); err == nil {
		return nil
	}
	if err := g.run("init", "-b", "main"); err != nil {
		g.logger().Error("git init failed", "dir", g.Dir, "err", err)
		return err
	}
	g.logger().Info("notes repo initialized", "dir", g.Dir)
	return nil
}

// CommitPaths stages exactly the given note-relative paths (deletions
// included, via add -A) and commits. When staging produced no change the
// commit is skipped: history is content history (DESIGN.md 10).
func (g *Git) CommitPaths(paths []string, message string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := g.run(append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		g.logger().Error("git add failed", "dir", g.Dir, "paths", paths, "err", err)
		return err
	}
	err := g.run("-c", "user.name="+serviceName, "-c", "user.email="+serviceEmail,
		"commit", "-m", message)
	if err != nil && strings.Contains(err.Error(), "nothing to commit") {
		g.logger().Debug("git commit skipped", "message", message)
		return nil
	}
	if err != nil {
		g.logger().Error("git commit failed", "dir", g.Dir, "message", message, "err", err)
	}
	return err
}

func (g *Git) run(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", g.Dir}, args...)...)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	g.logger().Debug("git command", "args", strings.Join(args, " "),
		"dur_ms", time.Since(start).Milliseconds())
	return nil
}
