package launcher

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// ErrTimeout is Run's error for a program whose tree outlived its bound.
var ErrTimeout = errors.New("launcher: the program did not exit in time")

// Host is a program started through internal/world/subprocess in a process tree
// of its own, without a temp home or a client: scenario files run the real binary
// through it with the environment they choose. Its output is captured.
type Host struct {
	// Process is the started program and the tree it owns.
	Process *subprocess.Process
	// Stdout and Stderr are everything the program and its tree wrote.
	Stdout, Stderr testkit.Buffer
	// Started is when the program was started.
	Started time.Time
}

// Spawn starts p.Argv in p.Dir with p.Env added to the whole host environment
// (DSH_* names included; later entries win); p.RPC is unused. When the test ends,
// a tree still running is logged and force-stopped, and the stop and the wait for
// the program are bounded by 3*Grace; a tree that outlives them fails the test.
func Spawn(t testing.TB, p Program) *Host {
	t.Helper()
	if len(p.Argv) == 0 {
		t.Fatal("launcher: no program")
	}
	h := &Host{Started: time.Now()}
	h.Stdout.SetLimit(p.MaxOutput)
	h.Stderr.SetLimit(p.MaxOutput)
	proc, err := subprocess.Start(context.Background(), subprocess.Spec{
		Argv: p.Argv, Dir: p.Dir, Env: subprocess.Env(append(os.Environ(), p.Env...)),
		Stdout: &h.Stdout, Stderr: &h.Stderr, Grace: Grace, Launch: sandbox.Trusted("launcher: the program under test, run by a scenario"),
	})
	if err != nil {
		t.Fatalf("launcher: start %s: %v", p.Argv[0], err)
	}
	h.Process = proc
	register(t, proc)
	t.Cleanup(func() {
		select {
		case <-proc.Exited():
		default:
			t.Logf("launcher: stopping the surviving tree of %s; stderr:\n%s", p.Argv[0], h.Stderr.String())
		}
		if err := h.Stop(); err != nil {
			t.Errorf("launcher: the tree of %s outlives the test: %v", p.Argv[0], err)
		}
	})
	return h
}

// Run spawns p and waits up to within for the program to exit and its output to
// be drained. When within passes first -- the program hangs, or a descendant
// still holds its output -- the whole tree is force-stopped and Run returns the
// host with ErrTimeout (or the stop's error when the tree survives).
func Run(t testing.TB, within time.Duration, p Program) (*Host, error) {
	t.Helper()
	h := Spawn(t, p)
	select {
	case <-h.Process.Exited():
		return h, nil
	case <-time.After(within):
	}
	if err := h.Stop(); err != nil {
		return h, err
	}
	return h, ErrTimeout
}

// Await waits up to within for the program to exit and reports whether it did.
func (h *Host) Await(within time.Duration) bool {
	select {
	case <-h.Process.Exited():
		return true
	case <-time.After(within):
		return false
	}
}

// Stop force-stops the tree, then waits for the program and its output, all
// within 3*Grace. It is idempotent.
func (h *Host) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*Grace)
	defer cancel()
	if err := h.Process.StopTree(ctx, 0); err != nil {
		return err
	}
	select {
	case <-h.Process.Exited():
		return nil
	case <-ctx.Done():
		return errors.New("launcher: the program's output is still held open after its tree stopped")
	}
}
