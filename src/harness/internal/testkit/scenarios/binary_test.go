//go:build scenario

package scenarios

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// dshBinary builds cmd/dsh from this checkout through the test kit's launcher
// (DH-D-UTIL-6), with tags, into a temporary directory removed at cleanup.
func dshBinary(t *testing.T, tags ...string) string {
	t.Helper()
	return launcher.Build(t, filepath.Join(repositoryRoot(t), "src", "harness"), "./cmd/dsh", tags...)
}

// dshRun is one finished run of the binary.
type dshRun struct {
	stdout, stderr string
	code           int
	took           time.Duration
}

// runDsh runs bin with args and env added to the parent environment in a process
// tree of its own (launcher.Run), waits up to within for it to exit, and returns
// what it wrote and its exit code. A run whose tree outlives within -- the binary
// hangs, or a descendant holds its output -- is stopped whole and fails the scenario.
func runDsh(t *testing.T, within time.Duration, env []string, bin string, args ...string) dshRun {
	t.Helper()
	h, err := launcher.Run(t, within, launcher.Program{Argv: append([]string{bin}, args...), Env: env})
	if err != nil {
		t.Fatalf("%s %v: %v within %s\nstdout:\n%s\nstderr:\n%s", bin, args, err, within, h.Stdout.String(), h.Stderr.String())
	}
	return dshRun{stdout: h.Stdout.String(), stderr: h.Stderr.String(), code: h.Process.ExitCode(), took: time.Since(h.Started)}
}

// dshHost is a running instance of the binary in a process tree of its own
// (launcher.Spawn); on Windows the tree's root is the console host that gives the
// binary a console terminate can close.
type dshHost struct {
	*launcher.Host
	pid func() int
}

// startDsh starts bin with args and env added to the parent environment, so that
// terminate can reach it. At cleanup a tree still running is stopped whole, with
// its output logged, within a bound.
func startDsh(t *testing.T, env []string, bin string, args ...string) *dshHost {
	t.Helper()
	argv, env, pid := hostCommand(t, env, bin, args...)
	h := launcher.Spawn(t, launcher.Program{Argv: argv, Env: env})
	return &dshHost{Host: h, pid: func() int { return pid(h) }}
}

// output is everything the host wrote so far, standard output first.
func (h *dshHost) output() string { return h.Stdout.String() + h.Stderr.String() }

// await waits up to within for the host to exit and reports whether it did.
func (h *dshHost) await(within time.Duration) bool { return h.Await(within) }

// code is the exit status of an exited host, -1 when it did not exit normally.
func (h *dshHost) code() int { return h.Process.ExitCode() }

// waitFor polls the host's output until match finds something, and returns it.
// It fails the scenario when the host exits first or within passes.
func (h *dshHost) waitFor(t *testing.T, within time.Duration, what string, match func(string) string) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if got := match(h.output()); got != "" {
			return got
		}
		select {
		case <-h.Process.Exited():
			if got := match(h.output()); got != "" {
				return got
			}
			t.Fatalf("the host ended with exit %d before %s:\n%s", h.code(), what, h.output())
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s:\n%s", what, within, h.output())
		}
	}
}
