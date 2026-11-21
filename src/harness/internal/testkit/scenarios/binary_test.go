//go:build scenario

package scenarios

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
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

// runDsh runs bin with args and env added to the parent environment, waits up to
// within for it to exit, and returns what it wrote and its exit code. A run that
// outlives within is killed and fails the scenario.
func runDsh(t *testing.T, within time.Duration, env []string, bin string, args ...string) dshRun {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	var err error
	exited := make(chan struct{})
	go func() { err = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		select {
		case <-exited:
		default:
			_ = cmd.Process.Kill()
			<-exited
		}
	})
	select {
	case <-exited:
	case <-time.After(within):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatalf("%s %v did not exit within %s\nstdout:\n%s\nstderr:\n%s", bin, args, within, stdout.String(), stderr.String())
	}
	r := dshRun{stdout: stdout.String(), stderr: stderr.String(), code: 0, took: time.Since(start)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	return r
}

// dshHost is a running instance of the binary. exited is closed once, when the
// process has been waited for, so any number of receivers observe the exit;
// err is valid after that.
type dshHost struct {
	cmd            *exec.Cmd
	stdout, stderr testkit.Buffer
	exited         chan struct{}
	err            error
	start          time.Time
}

// startDsh starts bin with args and env added to the parent environment, in a
// console of its own so terminate can reach it. At cleanup a host still running
// is killed, with its output logged, and awaited.
func startDsh(t *testing.T, env []string, bin string, args ...string) *dshHost {
	t.Helper()
	h := &dshHost{exited: make(chan struct{})}
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = &h.stdout, &h.stderr
	ownConsole(cmd)
	h.cmd, h.start = cmd, time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	go func() { h.err = cmd.Wait(); close(h.exited) }()
	t.Cleanup(func() {
		select {
		case <-h.exited:
		default:
			t.Logf("killing the surviving host %s; stderr:\n%s", bin, h.stderr.String())
			_ = cmd.Process.Kill()
			<-h.exited
		}
	})
	return h
}

// output is everything the host wrote so far, standard output first.
func (h *dshHost) output() string { return h.stdout.String() + h.stderr.String() }

// await waits up to within for the host to exit and reports whether it did.
func (h *dshHost) await(within time.Duration) bool {
	select {
	case <-h.exited:
		return true
	case <-time.After(within):
		return false
	}
}

// code is the exit status of an exited host, -1 when it did not exit normally.
func (h *dshHost) code() int {
	var exit *exec.ExitError
	switch {
	case h.err == nil:
		return 0
	case errors.As(h.err, &exit):
		return exit.ExitCode()
	default:
		return -1
	}
}

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
		case <-h.exited:
			if got := match(h.output()); got != "" {
				return got
			}
			t.Fatalf("the host ended with %v before %s:\n%s", h.err, what, h.output())
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s:\n%s", what, within, h.output())
		}
	}
}
