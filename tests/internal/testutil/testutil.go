// Package testutil provides the system-test harness pieces: building real
// binaries (backend, stubs) and starting them on ephemeral ports with bounded,
// observable waits. Determinism rules (IMPLEMENTATION-FRAMEWORK.md 4): no
// sleeps, no fixed ports, waits are bounded polls that fail by name.
package testutil

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var buildCache sync.Map // package path -> binary path

var listenRE = regexp.MustCompile(`addr=127\.0\.0\.1:(\d+)`)

// BuildBinary compiles pkg into a shared temp dir and returns the binary
// path. Each package is built at most once per test-binary run.
func BuildBinary(t *testing.T, pkg string) string {
	t.Helper()
	if p, ok := buildCache.Load(pkg); ok {
		return p.(string)
	}
	dir, err := os.MkdirTemp("", "lucien-bin-*")
	if err != nil {
		t.Fatalf("build temp dir: %v", err)
	}
	bin := filepath.Join(dir, filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", pkg, err)
	}
	actual, _ := buildCache.LoadOrStore(pkg, bin)
	return actual.(string)
}

// Proc is a started child process whose startup line announced its port.
type Proc struct {
	Cmd  *exec.Cmd
	Port int

	mu     sync.Mutex
	output []byte
	exited atomic.Bool
	waitCh chan error
}

// Start launches bin with args and waits (bounded) for the startup line
// "addr=127.0.0.1:<port>" that server binaries print to stderr.
func Start(t *testing.T, bin string, args ...string) *Proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	// An os.Pipe (not StdoutPipe) so the monitor goroutine can Wait without
	// closing the reader out from under the drain.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		t.Fatalf("start %s: %v", bin, err)
	}
	pw.Close() // parent copy; child holds the write end

	p := &Proc{Cmd: cmd, waitCh: make(chan error, 1)}
	go func() {
		// Incremental read: a running server holds the write end open
		// forever, so ReadAll (EOF-only) would never surface the listen line.
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				p.mu.Lock()
				p.output = append(p.output, buf[:n]...)
				p.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		pr.Close()
	}()
	go func() {
		p.waitCh <- cmd.Wait()
		p.exited.Store(true)
	}()
	t.Cleanup(p.Stop)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		text := string(p.output)
		p.mu.Unlock()
		if m := listenRE.FindStringSubmatch(text); m != nil {
			if _, err := fmt.Sscanf(m[1], "%d", &p.Port); err != nil {
				t.Fatalf("bad port in listen line %q: %v", m[0], err)
			}
			return p
		}
		if p.exited.Load() {
			p.mu.Lock()
			out := string(p.output)
			p.mu.Unlock()
			t.Fatalf("%s exited before announcing a port:\n%s", filepath.Base(bin), out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not announce addr=127.0.0.1:<port> within 15s", filepath.Base(bin))
	return nil
}

// Stop terminates the process if still running. Safe to call twice.
func (p *Proc) Stop() {
	if p.exited.Swap(true) {
		return // already stopped or exited; the waitCh value is spent
	}
	if p.Cmd != nil && p.Cmd.Process != nil {
		_ = p.Cmd.Process.Kill()
	}
	select {
	case <-p.waitCh:
	case <-time.After(5 * time.Second):
	}
}

// Output returns everything the process has printed so far.
func (p *Proc) Output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.output)
}

// BaseURL is the http base address of the running process.
func (p *Proc) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.Port)
}

// WaitFor polls GET url until it answers 2xx, or fails the test by name.
func WaitFor(t *testing.T, url string, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s at %s", timeout, what, url)
}

// LinesContaining returns process output lines containing substr, for log
// assertions.
func (p *Proc) LinesContaining(substr string) []string {
	var out []string
	for _, l := range strings.Split(p.Output(), "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}
