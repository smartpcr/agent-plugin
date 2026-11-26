//go:build scenario

package scenarios

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// need returns the run-time input name, which an operator of the DH-S-18 run sets
// to describe the real environment. A missing input fails the scenario: a scenario
// that cannot run never skips (DH-O-15).
func need(t *testing.T, name, what string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Fatalf("%s is not set: %s", name, what)
	}
	return v
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

// shell runs command line through the platform shell (sh -c, or cmd /C on
// Windows) and returns its combined output.
func shell(t *testing.T, line string) (string, error) {
	t.Helper()
	return shellContext(t.Context(), line)
}

// shellContext is shell bound to ctx instead of the test's context, for
// commands that run in a cleanup callback (T.Context is canceled by then).
func shellContext(ctx context.Context, line string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", line)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
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
	done chan struct{}
	out  string
	err  error
}

// startShell runs line through the platform shell without waiting for it. The
// command is bound to the test's context and waited for at cleanup.
func startShell(t *testing.T, line string) *running {
	t.Helper()
	r := &running{line: line, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.out, r.err = shell(t, line)
	}()
	t.Cleanup(func() { <-r.done })
	return r
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
