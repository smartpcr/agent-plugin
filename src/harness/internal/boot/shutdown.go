package boot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// forceShare caps the part of the shutdown bound kept for the forced stop: past
// budget minus the reserve the process trees are killed, and the reserve bounds
// the kill and the end of the unload, so the whole shutdown stays within budget.
const forceShare = 5 * time.Second

// forceReserve is the end part of budget kept for the forced stop: a quarter of
// it, at most forceShare.
func forceReserve(budget time.Duration) time.Duration {
	return min(budget/4, forceShare)
}

// shutdown closes Stopping (if no stop was asked yet), unloads the tree and
// returns within budget on the clock: one deadline for the whole operation. A
// clean unload returns status. When the cleanups still run at budget minus the
// forced-stop reserve, or an Interrupted stop arrives during the wait, the
// watchdog's stuck cleanups are printed, every registered process tree is killed
// (even one a cleanup is stopping gracefully), and the status is 124 whatever the
// stop asked for: a fatal report written before stays written.
func (s *State) shutdown(status int, budget time.Duration) int {
	// one deadline for the whole operation, the reload's drain included
	deadline := s.Clock.Now().Add(budget)
	reserve := forceReserve(budget)
	if s.stopReload != nil {
		s.stopReload(budget)
	}
	s.App.Stop(launch.Requested, status)
	if s.Host == nil {
		return status
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Host.Unload()
	}()
	var why string
	select {
	case <-done:
		ctx, cancel := s.until(deadline)
		defer cancel()
		if s.stopProcesses(ctx) {
			return output.Forced
		}
		return status
	case <-s.Clock.After(max(deadline.Add(-reserve).Sub(s.Clock.Now()), 0)):
		why = fmt.Sprintf("shutdown not done %s into its %s bound", budget-reserve, budget)
	case <-s.App.Urgent():
		why = "shutdown interrupted"
	}
	end := s.Clock.Now().Add(reserve)
	if end.After(deadline) {
		end = deadline
	}
	ctx, cancel := s.until(end)
	defer cancel()
	var b strings.Builder
	fmt.Fprintf(&b, "dsh: %s; stopping process trees", why)
	if stuck := s.Host.Stuck(); len(stuck) > 0 {
		b.WriteString("; stuck cleanups:")
		for _, line := range stuck {
			b.WriteString("\n  " + line)
		}
	}
	b.WriteString("\n")
	_, _ = fmt.Fprint(s.Stderr, b.String())
	if s.Ring != nil {
		_, _ = fmt.Fprint(s.Ring, b.String())
	}
	s.stopProcesses(ctx)
	select {
	case <-done:
	case <-ctx.Done():
		_, _ = fmt.Fprintf(s.Stderr, "dsh: shutdown bound %s passed; exiting with cleanups still running\n", budget)
	}
	return output.Forced
}

// until returns a context that ends when the clock reaches t.
func (s *State) until(t time.Time) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	after := s.Clock.After(t.Sub(s.Clock.Now()))
	go func() {
		select {
		case <-after:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// stopProcesses kills every process tree still registered, within ctx, and
// reports whether ctx ended first with trees still stopping.
func (s *State) stopProcesses(ctx context.Context) (overran bool) {
	reg := s.Host.Processes()
	if reg == nil || len(reg.Live()) == 0 {
		return false
	}
	if err := reg.StopAll(ctx, 0); err != nil {
		_, _ = fmt.Fprintf(s.Stderr, "dsh: shutdown: %v\n", err)
	}
	return ctx.Err() != nil
}

// tree is the live cordis tree: the root, the loader, the process registry and the
// watchdog. Status notifications are delivered only once a disposal returns, so the
// watchdog records every fiber and the labels of its effects as it becomes active,
// and Stuck reads the live state of each.
type tree struct {
	root   *cordis.Context
	svc    *loader.Service
	procs  *subprocess.Registry
	remove func()

	mu     sync.Mutex
	fibers map[*cordis.Fiber][]string // effect labels when last active
}

func newTree(root *cordis.Context, procs *subprocess.Registry) *tree {
	t := &tree{root: root, procs: procs, fibers: map[*cordis.Fiber][]string{}}
	t.remove = root.Internal().OnStatus(func(f *cordis.Fiber, _, next cordis.FiberState) {
		var labels []string
		if next == cordis.Active {
			for _, h := range f.Effects() {
				labels = append(labels, h.Label())
			}
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		switch next {
		case cordis.Disposed:
			delete(t.fibers, f)
		case cordis.Active:
			t.fibers[f] = labels
		default:
			if _, ok := t.fibers[f]; !ok {
				t.fibers[f] = nil
			}
		}
	})
	return t
}

func (t *tree) Unload() {
	t.root.Dispose()
	if t.remove != nil {
		t.remove()
	}
}

func (t *tree) Processes() *subprocess.Registry { return t.procs }

// Stuck lists the entries unloading (any not yet disposed before the unload reached
// one), with the effects they held, as
// "<id> (<component>): <effect>, ...". With no entry among them it lists the other
// fibers not yet disposed.
func (t *tree) Stuck() []string {
	t.mu.Lock()
	known := make(map[*cordis.Fiber][]string, len(t.fibers))
	for f, labels := range t.fibers {
		known[f] = labels
	}
	t.mu.Unlock()
	// Fibers whose cleanup runs; before the unload reached any, every live one.
	unloading := false
	for f := range known {
		if f.State() == cordis.Unloading {
			unloading = true
			break
		}
	}
	var entries, others []string
	for f, labels := range known {
		st := f.State()
		if st == cordis.Disposed || st == cordis.Pending || (unloading && st != cordis.Unloading) {
			continue
		}
		name := f.Name()
		var entry *loader.Entry
		if t.svc != nil {
			entry = t.svc.EntryOf(f)
		}
		if entry != nil {
			name = entry.ID() + " (" + name + ")"
		}
		if len(labels) > 0 {
			name += ": " + strings.Join(labels, ", ")
		}
		if entry != nil {
			entries = append(entries, name)
		} else {
			others = append(others, name)
		}
	}
	if len(entries) == 0 {
		entries = others
	}
	slices.Sort(entries)
	return entries
}
