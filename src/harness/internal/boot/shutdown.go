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

// forcedStop bounds, in wall time, the forced stop of the registered process trees
// and the wait for the unload to finish once they are gone.
const forcedStop = 5 * time.Second

// shutdown closes Stopping (if no stop was asked yet), unloads the tree and waits at
// most budget on the clock. A clean unload returns status. When the bound passes, or
// an Interrupted stop arrives during the wait, the watchdog's stuck cleanups are
// printed, every registered process tree is stopped with StopTree, and the status
// is 124 (70 stays 70: the fatal report was already written).
func (s *State) shutdown(status int, budget time.Duration) int {
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
		s.stopProcesses()
		return status
	case <-s.Clock.After(budget):
		why = fmt.Sprintf("shutdown exceeded %s", budget)
	case <-s.App.Urgent():
		why = "shutdown interrupted"
	}
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
	s.stopProcesses()
	select {
	case <-done:
	case <-time.After(forcedStop):
	}
	if status == output.Fatal {
		return status
	}
	return output.Forced
}

// stopProcesses force-stops every process tree still registered.
func (s *State) stopProcesses() {
	reg := s.Host.Processes()
	if reg == nil || len(reg.Live()) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), forcedStop)
	defer cancel()
	if err := reg.StopAll(ctx, 0); err != nil {
		_, _ = fmt.Fprintf(s.Stderr, "dsh: shutdown: %v\n", err)
	}
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

// Stuck lists the entries not yet disposed, with the effects they held, as
// "<id> (<component>): <effect>, ...". With no entry among them it lists the other
// fibers not yet disposed.
func (t *tree) Stuck() []string {
	t.mu.Lock()
	known := make(map[*cordis.Fiber][]string, len(t.fibers))
	for f, labels := range t.fibers {
		known[f] = labels
	}
	t.mu.Unlock()
	var entries, others []string
	for f, labels := range known {
		if st := f.State(); st == cordis.Disposed || st == cordis.Pending {
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
