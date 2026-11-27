//go:build scenario

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// need returns the run-time input name, which an operator of the DH-S-18 run sets
// to describe the real environment (scenario.Need). A missing input fails the
// scenario: a scenario that cannot run never skips (DH-O-15).
func need(t *testing.T, name, what string) string {
	t.Helper()
	return scenario.Need(t, name, what)
}

// needList is need for a list input: it splits the value on ";", drops empty items,
// and fails the scenario when no item is left (a value such as ";" would otherwise
// run the scenario against no target at all and pass).
func needList(t *testing.T, name, what string) []string {
	t.Helper()
	var out []string
	for _, item := range strings.Split(need(t, name, what), ";") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s lists no item: %s", name, what)
	}
	return out
}

// expand replaces each {key} of template with vars[key].
func expand(template string, vars map[string]string) string {
	for k, v := range vars {
		template = strings.ReplaceAll(template, "{"+k+"}", v)
	}
	return template
}

// cleanupShellWithin bounds a shell command run from a cleanup callback, where
// the test's context has already ended.
const cleanupShellWithin = time.Minute

// shell runs command line through the platform shell (sh -c, or cmd /C on
// Windows) and returns its output, standard output then standard error.
func shell(t *testing.T, line string) (string, error) {
	t.Helper()
	return spawnShell(t, line).wait()
}

// shellRun is a shell command line started by spawnShell.
type shellRun struct {
	line    string
	host    *launcher.Host
	ctx     context.Context
	cleanup bool
}

// spawnShell starts line through the platform shell in a process tree of its own
// (launcher.Spawn), registered under the test, so a scenario past its deadline
// stops it whole (launcher.StopTrees) and the test's end stops whatever is left.
// Started from a cleanup callback, its wait is bounded by cleanupShellWithin
// instead of the test's ended context.
func spawnShell(t *testing.T, line string) shellRun {
	t.Helper()
	return shellRun{
		line: line, ctx: t.Context(), cleanup: t.Context().Err() != nil,
		host: launcher.Spawn(t, launcher.Program{Argv: shellArgv(t, line)}),
	}
}

// wait waits for the command and its output. When the test's context ends first
// (or cleanupShellWithin passes, for a cleanup command), the whole tree, every
// descendant included, is stopped within launcher's bound and the cause returned.
func (s shellRun) wait() (string, error) {
	done, bound := s.ctx.Done(), (<-chan time.Time)(nil)
	if s.cleanup {
		done, bound = nil, time.After(cleanupShellWithin)
	}
	var err error
	select {
	case <-s.host.Process.Exited():
		// The exit code is the direct shell's alone: a shell that exited 0 while a
		// descendant held its output, until a stop ended that descendant, was
		// stopped all the same.
		if cause := s.host.Process.StopCause(); cause != nil {
			err = fmt.Errorf("%s: stopped: %w", s.line, cause)
		} else if code := s.host.Process.ExitCode(); code != 0 {
			err = fmt.Errorf("%s: exit %d", s.line, code)
		}
	case <-done:
		err = errors.Join(s.ctx.Err(), s.host.Stop())
	case <-bound:
		err = errors.Join(launcher.ErrTimeout, s.host.Stop())
	}
	return s.host.Stdout.String() + s.host.Stderr.String(), err
}

// mustShell is shell that fails the scenario on a non-zero exit.
func mustShell(t *testing.T, line string) string {
	t.Helper()
	out, err := shell(t, line)
	if err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
	return out
}

// running is a shell command line started by startShell.
type running struct {
	line string
	run  shellRun
	done chan struct{}
	out  string
	err  error
}

// startShell runs line through the platform shell without waiting for it. The
// command runs in a process tree of its own (spawnShell), is bound to the test's
// context and waited for at cleanup.
func startShell(t *testing.T, line string) *running {
	t.Helper()
	s := spawnShell(t, line)
	r := &running{line: line, run: s, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.out, r.err = s.wait()
	}()
	t.Cleanup(func() { <-r.done })
	return r
}

// startShellTree runs line through the platform shell in a process tree of its
// own (launcher.Spawn), for a long-running command such as a server: at cleanup
// the whole tree, every descendant included, is stopped within a bound, so a
// child that outlives the shell can neither keep listening nor hold the output.
func startShellTree(t *testing.T, line string) *launcher.Host {
	t.Helper()
	return launcher.Spawn(t, launcher.Program{Argv: shellArgv(t, line)})
}

// shellTree runs line through the platform shell in a process tree of its own
// (launcher.Run) and waits up to within for it and its output. A tree that
// outlives within -- a descendant still holds the output -- is stopped whole,
// within launcher's bound, and returned with the error; so is a non-zero exit.
// It needs no live test context, so a cleanup callback can use it.
func shellTree(t *testing.T, line string, within time.Duration) (*launcher.Host, error) {
	t.Helper()
	h, err := launcher.Run(t, within, launcher.Program{Argv: shellArgv(t, line)})
	if err == nil && h.Process.ExitCode() != 0 {
		err = fmt.Errorf("%s: exit %d", line, h.Process.ExitCode())
	}
	return h, err
}

// shellArgv is the platform shell's argv running line, the shell found on PATH.
func shellArgv(t *testing.T, line string) []string {
	t.Helper()
	argv := []string{"sh", "-c", line}
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/C", line}
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		t.Fatalf("no shell to run %s: %v", line, err)
	}
	argv[0] = path
	return argv
}

// exited reports whether the command has exited.
func (r *running) exited() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// wait waits for the command and fails the scenario on a non-zero exit.
func (r *running) wait(t *testing.T) {
	t.Helper()
	<-r.done
	if r.err != nil {
		t.Fatalf("%s: %v\n%s", r.line, r.err, r.out)
	}
}

// changedAt returns the moment this machine first sees path differ from before
// (size, modification time or identity), polled every millisecond while r runs, so
// a command that lingers after saving does not move the moment later. Once r has
// exited the poll goes on for grace (a remote change may take that long to be
// visible here); a command that fails, or changes nothing, fails the scenario. Stat
// does not open the file, so the poll cannot get in the way of an editor's save.
func changedAt(t *testing.T, path string, before os.FileInfo, r *running, grace time.Duration) time.Time {
	t.Helper()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	var exitedAt time.Time
	for {
		exited := r.exited()
		if info, err := os.Stat(path); err == nil &&
			(info.Size() != before.Size() || !info.ModTime().Equal(before.ModTime()) || !os.SameFile(info, before)) {
			return time.Now()
		}
		if exited {
			if r.err != nil {
				r.wait(t)
			}
			if exitedAt.IsZero() {
				exitedAt = time.Now()
			} else if time.Since(exitedAt) > grace {
				t.Fatalf("%s exited without changing %s", r.line, path)
			}
		}
		<-tick.C
	}
}

// arrivals records the moment each watch callback runs.
type arrivals struct {
	mu    sync.Mutex
	times []time.Time
}

func (a *arrivals) record() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.times = append(a.times, time.Now())
}

// since returns the arrivals at or after from.
func (a *arrivals) since(from time.Time) []time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []time.Time
	for _, at := range a.times {
		if !at.Before(from) {
			out = append(out, at)
		}
	}
	return out
}
