package boot

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/lifecycle"
)

// Process runs a launch of the running process: Run, with the process's stop
// signals watched by the one stop path, lifecycle.Watch (DH-D-CLI-4). A first
// Ctrl-C is an Interrupted stop (status 130); a first SIGTERM or SIGHUP (on
// Windows a closed console) is the supervisor's ordinary stop, a Terminated stop
// (status 0 inside the shutdown bound). A second signal of any kind, during
// shutdown, ends the shutdown wait at once (status 124). A host (web) launch on
// a terminal asks first: a first Ctrl-C with work running prints it and stops
// only on a second press within the window (DH-ADR-CLI-5).
func Process(ctx context.Context, o Options) int {
	if o.App == nil {
		o.App = launch.NewApp()
	}
	in := stopInputs(&o)
	return lifecycle.Process(ctx, o.App, in, func(ctx context.Context) int {
		return Run(ctx, o)
	})
}

// stopInputs are the lifecycle inputs of a launch of o. Only a host launch on a
// terminal has Busy, which asks the live tree; a one-shot, a stdio carrier or a
// launch without a terminal stops on its first Ctrl-C.
func stopInputs(o *Options) lifecycle.Inputs {
	in := lifecycle.Inputs{Notify: o.Stderr}
	if o.Terminal && o.Profile.Name == profile.Web {
		w := &liveWork{}
		o.onHost = w.set
		in.Busy = w.running
	}
	return in
}

// liveWork is the work summary of the live tree, as host.launch's host.status
// reports it: the process trees the tree started.
type liveWork struct {
	mu   sync.Mutex
	host Host
}

func (w *liveWork) set(h Host) {
	w.mu.Lock()
	w.host = h
	w.mu.Unlock()
}

// running lists what a stop would interrupt; none before the tree exists.
func (w *liveWork) running(context.Context) []string {
	w.mu.Lock()
	h := w.host
	w.mu.Unlock()
	if h == nil {
		return nil
	}
	reg := h.Processes()
	if reg == nil {
		return nil
	}
	switch n := len(reg.Live()); n {
	case 0:
		return nil
	case 1:
		return []string{"1 command"}
	default:
		return []string{fmt.Sprintf("%d commands", n)}
	}
}
