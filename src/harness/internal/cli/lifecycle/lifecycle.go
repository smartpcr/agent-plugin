// Package lifecycle is the one stop path of a launch (A24, C51, DH-D-CLI-4,
// DH-ADR-CLI-5): Ctrl-C, SIGTERM and SIGHUP (on Windows a closed console, a
// logoff or a shutdown, which the Go runtime delivers as SIGTERM), the end of
// standard input, `dsh stop` and the end of a stdio carrier's input all end in
// one call of launch.App.Stop with the reason's status.
//
// Watch is a state machine (running, confirming, stopping, forced) whose only
// output is that call. A second Ctrl-C, or a second signal of any kind, while
// stopping calls Stop with launch.Interrupted again, which ends boot's shutdown
// wait at once (status 124). A request that is not a signal joins the first
// stop. A signal never enters the confirming state, and only a first Ctrl-C on
// a host with Busy work does: it prints the work and stops only if Ctrl-C is
// pressed again within ConfirmWindow.
//
// The bound and the forced exit are boot's (ShutdownTimeout): this package owns
// no tree and no exit timer and never exits the process.
package lifecycle

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
)

// Trigger is one stop request.
type Trigger int

// The triggers.
const (
	// Interrupt is Ctrl-C (SIGINT; CTRL_C_EVENT or CTRL_BREAK_EVENT on Windows).
	Interrupt Trigger = iota + 1
	// Terminate is SIGTERM or SIGHUP, or on Windows a closed console, a logoff
	// or a shutdown: a supervisor's ordinary stop, which never asks.
	Terminate
	// InputEnd is the end of standard input of a launch that stops on it.
	InputEnd
	// StdioEnd is the end of a stdio carrier's input.
	StdioEnd
	// Stalled is a stdio client that stopped reading.
	Stalled
	// Request is `dsh stop` or the UI's stop action, already confirmed by
	// whoever asked.
	Request
)

func (t Trigger) String() string {
	switch t {
	case Interrupt:
		return "interrupt"
	case Terminate:
		return "terminate"
	case InputEnd:
		return "input-end"
	case StdioEnd:
		return "stdio-end"
	case Stalled:
		return "stalled"
	case Request:
		return "request"
	}
	return fmt.Sprintf("trigger(%d)", int(t))
}

// Stop is the reason and the exit status a trigger asks the stop with.
func (t Trigger) Stop() (launch.Reason, int) {
	switch t {
	case Interrupt:
		return launch.Interrupted, output.Interrupted
	case Terminate:
		return launch.Terminated, output.OK
	case InputEnd, StdioEnd:
		return launch.InputClosed, output.OK
	case Stalled:
		return launch.ClientStalled, output.Stalled
	case Request:
		return launch.Requested, output.OK
	}
	return "", 0
}

// signal reports whether t comes from the operating system: one during a stop
// forces it.
func (t Trigger) signal() bool { return t == Interrupt || t == Terminate }

// valid reports whether t is one of the triggers; any other value stops nothing.
func (t Trigger) valid() bool { return t >= Interrupt && t <= Request }

// Of is the trigger of an operating-system signal, 0 for one that stops nothing.
func Of(sig os.Signal) Trigger {
	switch sig {
	case os.Interrupt:
		return Interrupt
	case syscall.SIGTERM, syscall.SIGHUP:
		return Terminate
	}
	return 0
}

// Signals are the operating-system signals Process watches.
var Signals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// State is where the stop path is.
type State int

// The states.
const (
	Running State = iota
	// Confirming: a first Ctrl-C found work running; a second one within the
	// window stops.
	Confirming
	// Stopping: Stop was called, by Watch or by another caller of the App.
	Stopping
	// Forced: a second signal arrived while stopping.
	Forced
)

func (s State) String() string {
	return [...]string{"running", "confirming", "stopping", "forced"}[s]
}

// ConfirmWindow is the default window for the second Ctrl-C (app-cli.md,
// lifecycle.Config).
const ConfirmWindow = 5 * time.Second

// AskLimit bounds the question to the host services of what is running; no
// answer by then counts as work running.
const AskLimit = 2 * time.Second

// Inputs are what Watch reacts to.
type Inputs struct {
	// Signals are operating-system signals, mapped by Of.
	Signals <-chan os.Signal
	// Triggers are the requests that are not signals, and signals a test sends.
	Triggers <-chan Trigger
	// Busy lists the work a stop would interrupt; nil (a one-shot or a stdio
	// launch) means a first Ctrl-C stops at once. It is called with a context
	// that ends after AskLimit.
	Busy func(ctx context.Context) []string
	// ConfirmWindow is the window for the second Ctrl-C; zero is ConfirmWindow.
	ConfirmWindow time.Duration
	// Notify receives the confirming prompt; nil discards.
	Notify io.Writer
	// Observe, when set, is called with each state Watch enters.
	Observe func(State)
}

// answer is the host services' answer for the press of one generation.
type answer struct {
	gen  int
	work []string
}

// watcher is one run of Watch.
type watcher struct {
	ctx     context.Context
	app     launch.App
	in      Inputs
	state   State
	gen     int
	answers chan answer
	window  <-chan time.Time
}

// Watch runs the stop state machine against app until ctx ends or the stop is
// forced, and returns the last state.
func Watch(ctx context.Context, app launch.App, in Inputs) State {
	if in.ConfirmWindow <= 0 {
		in.ConfirmWindow = ConfirmWindow
	}
	if in.Notify == nil {
		in.Notify = io.Discard
	}
	w := &watcher{ctx: ctx, app: app, in: in, answers: make(chan answer, 1)}
	stopping := app.Stopping()
	// A closed input is switched off: its end is no request.
	signals, triggers := in.Signals, in.Triggers
	for w.state != Forced {
		select {
		case <-ctx.Done():
			return w.state
		case <-stopping:
			stopping = nil
			if w.state < Stopping {
				w.window = nil
				w.enter(Stopping)
			}
		case sig, ok := <-signals:
			if !ok {
				signals = nil
			} else if t := Of(sig); t != 0 {
				w.on(t)
			}
		case t, ok := <-triggers:
			if !ok {
				triggers = nil
			} else if t.valid() {
				w.on(t)
			}
		case a := <-w.answers:
			w.answered(a)
		case <-w.window:
			w.window = nil
			fmt.Fprintln(w.in.Notify, "dsh: not stopping")
			w.enter(Running)
		}
	}
	return w.state
}

func (w *watcher) enter(s State) {
	w.state = s
	if w.in.Observe != nil {
		w.in.Observe(s)
	}
}

// on moves the machine for one trigger.
func (w *watcher) on(t Trigger) {
	switch w.state {
	case Running:
		if t == Interrupt && w.in.Busy != nil {
			w.confirm()
			return
		}
		w.stop(t)
	case Confirming:
		w.stop(t)
	case Stopping:
		if t.signal() {
			// launch.Control ends the shutdown wait on a later Interrupted stop.
			w.app.Stop(launch.Interrupted, output.Interrupted)
			w.enter(Forced)
		}
	}
}

// stop is the one call of Stop.
func (w *watcher) stop(t Trigger) {
	w.window = nil
	w.gen++
	reason, status := t.Stop()
	w.app.Stop(reason, status)
	w.enter(Stopping)
}

// confirm enters Confirming and asks the host services, within AskLimit and
// without blocking the machine, what a stop would interrupt.
func (w *watcher) confirm() {
	w.gen++
	gen, busy := w.gen, w.in.Busy
	w.enter(Confirming)
	go func() {
		ctx, cancel := context.WithTimeout(w.ctx, AskLimit)
		defer cancel()
		got := make(chan []string, 1)
		go func() { got <- busy(ctx) }()
		var work []string
		select {
		case work = <-got:
		case <-ctx.Done():
			work = []string{"work (the host services did not answer within " + AskLimit.String() + ")"}
		}
		select {
		case w.answers <- answer{gen, work}:
		case <-w.ctx.Done():
		}
	}()
}

// answered prints the running work and opens the window, or stops when nothing
// would be interrupted. An answer for an earlier press is dropped.
func (w *watcher) answered(a answer) {
	if a.gen != w.gen || w.state != Confirming {
		return
	}
	if len(a.work) == 0 {
		w.stop(Interrupt)
		return
	}
	fmt.Fprintf(w.in.Notify, "dsh: running: %s\ndsh: press Ctrl-C again within %s to stop\n", strings.Join(a.work, "; "), w.in.ConfirmWindow)
	w.window = time.After(w.in.ConfirmWindow)
}

// Process runs run with the process's stop signals watched against app, and
// returns run's status. run is boot.Run: it owns the tree and the bound. The
// watch lasts until run returns, not until ctx ends: run turns the end of ctx
// into a stop and shuts down, and a Ctrl-C during that shutdown still forces it.
func Process(ctx context.Context, app launch.App, in Inputs, run func(ctx context.Context) int) int {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, Signals...)
	defer signal.Stop(sigs)
	in.Signals = sigs
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(watchCtx, app, in)
	}()
	defer func() { cancel(); <-done }()
	return run(ctx)
}
