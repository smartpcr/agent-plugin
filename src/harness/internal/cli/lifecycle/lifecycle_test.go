package lifecycle

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
)

// countingApp is launch.Control with every Stop call recorded.
type countingApp struct {
	*launch.Control
	mu    sync.Mutex
	calls []stopCall
}

type stopCall struct {
	reason launch.Reason
	status int
}

func newApp() *countingApp { return &countingApp{Control: launch.NewApp()} }

func (a *countingApp) Stop(r launch.Reason, status int) {
	a.mu.Lock()
	a.calls = append(a.calls, stopCall{r, status})
	a.mu.Unlock()
	a.Control.Stop(r, status)
}

func (a *countingApp) stops() []stopCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]stopCall(nil), a.calls...)
}

func urgent(a *countingApp) bool {
	select {
	case <-a.Urgent():
		return true
	default:
		return false
	}
}

// machine is one Watch run inside a bubble, fed through its channels.
type machine struct {
	app      *countingApp
	signals  chan os.Signal
	triggers chan Trigger
	notify   *testkit.Buffer
	mu       sync.Mutex
	state    State
	cancel   context.CancelFunc
	done     chan State
}

func start(t *testing.T, busy func(context.Context) []string) *machine {
	t.Helper()
	m := &machine{app: newApp(), signals: make(chan os.Signal), triggers: make(chan Trigger), notify: &testkit.Buffer{}, done: make(chan State, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	in := Inputs{Signals: m.signals, Triggers: m.triggers, Busy: busy, Notify: m.notify, Observe: func(s State) {
		m.mu.Lock()
		m.state = s
		m.mu.Unlock()
	}}
	go func() { m.done <- Watch(ctx, m.app, in) }()
	t.Cleanup(func() { cancel(); <-m.done })
	return m
}

func (m *machine) signal(sig os.Signal) { m.signals <- sig; clock.Settle() }
func (m *machine) fire(t Trigger)       { m.triggers <- t; clock.Settle() }

func (m *machine) current() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// TC-14b: Ctrl-C, SIGTERM, SIGHUP, the end of standard input, `dsh stop`, the
// end of a stdio carrier's input and a stalled client each reach Stop exactly
// once with their reason and status; a later request that is not a signal joins.
//
//dsh:test DH-T-CLI-8
func TestEveryTriggerReachesOneStop(t *testing.T) {
	for _, c := range []struct {
		name   string
		fire   func(m *machine)
		reason launch.Reason
		status int
	}{
		{"ctrl-c", func(m *machine) { m.signal(os.Interrupt) }, launch.Interrupted, output.Interrupted},
		{"sigterm", func(m *machine) { m.signal(syscall.SIGTERM) }, launch.Terminated, output.OK},
		{"sighup", func(m *machine) { m.signal(syscall.SIGHUP) }, launch.Terminated, output.OK},
		{"stdin end", func(m *machine) { m.fire(InputEnd) }, launch.InputClosed, output.OK},
		{"dsh stop", func(m *machine) { m.fire(Request) }, launch.Requested, output.OK},
		{"stdio end", func(m *machine) { m.fire(StdioEnd) }, launch.InputClosed, output.OK},
		{"stalled client", func(m *machine) { m.fire(Stalled) }, launch.ClientStalled, output.Stalled},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock.Run(t, func(t *testing.T) {
				m := start(t, nil)
				c.fire(m)
				m.fire(Request)
				m.fire(InputEnd)
				if got := m.app.stops(); len(got) != 1 || got[0] != (stopCall{c.reason, c.status}) {
					t.Fatalf("Stop calls %v, want one (%s, %d)", got, c.reason, c.status)
				}
				if s := m.current(); s != Stopping {
					t.Errorf("state %s, want stopping", s)
				}
				if urgent(m.app) {
					t.Error("a joining request forced the shutdown")
				}
				if r, status, _ := m.app.Result(); r != c.reason || status != c.status {
					t.Errorf("the launch stops with (%s, %d), want (%s, %d)", r, status, c.reason, c.status)
				}
			})
		})
	}
}

// TC-14b: a second Ctrl-C, or any signal, during the stop forces it: the
// launch's shutdown wait ends at once (boot exits 124), and the first stop's
// status stays recorded.
//
//dsh:test DH-T-CLI-8
func TestSecondSignalForces(t *testing.T) {
	for name, second := range map[string]os.Signal{"ctrl-c": os.Interrupt, "sigterm": syscall.SIGTERM} {
		t.Run(name, func(t *testing.T) {
			clock.Run(t, func(t *testing.T) {
				m := start(t, nil)
				m.signal(os.Interrupt)
				if urgent(m.app) {
					t.Fatal("the first Ctrl-C forced the shutdown")
				}
				m.signal(second)
				if s := <-m.done; s != Forced {
					t.Fatalf("Watch ended %s, want forced", s)
				}
				m.done <- Forced
				if !urgent(m.app) {
					t.Error("the second signal did not force the shutdown")
				}
				if got := m.app.stops(); len(got) != 2 || got[0] != (stopCall{launch.Interrupted, output.Interrupted}) {
					t.Errorf("Stop calls %v, want the interrupt and the force", got)
				}
				if r, status, _ := m.app.Result(); r != launch.Interrupted || status != output.Interrupted {
					t.Errorf("the launch stops with (%s, %d), want the first stop's", r, status)
				}
			})
		})
	}
}

// A stop another caller of the App asked (the stdio carrier, host.stop, the run
// itself) moves the machine to stopping without a second call; a Ctrl-C then
// forces.
//
//dsh:test DH-T-CLI-8
func TestStopByAnotherCallerThenCtrlCForces(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		m := start(t, nil)
		m.app.Control.Stop(launch.InputClosed, output.OK)
		clock.Settle()
		if s := m.current(); s != Stopping {
			t.Fatalf("state %s, want stopping", s)
		}
		m.signal(os.Interrupt)
		if !urgent(m.app) {
			t.Error("a Ctrl-C during the stop did not force it")
		}
		if r, status, _ := m.app.Result(); r != launch.InputClosed || status != output.OK {
			t.Errorf("the launch stops with (%s, %d), want the carrier's", r, status)
		}
	})
}

// A host with running work: the first Ctrl-C prints the work and stops nothing;
// past the window the machine runs again; a second press within the window
// stops with status 130.
//
//dsh:test DH-T-CLI-8
func TestCtrlCConfirmsRunningWork(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		asked := 0
		m := start(t, func(context.Context) []string { asked++; return []string{"agent ses_1: turn 3"} })
		m.signal(os.Interrupt)
		if s := m.current(); s != Confirming || len(m.app.stops()) != 0 {
			t.Fatalf("state %s, stops %v; want confirming and no stop", s, m.app.stops())
		}
		if out := m.notify.String(); !strings.Contains(out, "agent ses_1: turn 3") || !strings.Contains(out, "within 5s") {
			t.Errorf("the prompt does not name the work and the window:\n%s", out)
		}
		clock.Advance(ConfirmWindow)
		if s := m.current(); s != Running || len(m.app.stops()) != 0 {
			t.Fatalf("past the window: state %s, stops %v; want running and no stop", s, m.app.stops())
		}
		m.signal(os.Interrupt)
		clock.Advance(ConfirmWindow - time.Second)
		m.signal(os.Interrupt)
		if got := m.app.stops(); len(got) != 1 || got[0] != (stopCall{launch.Interrupted, output.Interrupted}) {
			t.Fatalf("Stop calls %v, want one interrupt", got)
		}
		if asked != 2 {
			t.Errorf("the host services were asked %d times, want once per first press", asked)
		}
		// A press during shutdown forces.
		m.signal(os.Interrupt)
		if !urgent(m.app) {
			t.Error("a press during the shutdown did not force it")
		}
	})
}

// A signal never enters the confirming state, also while confirming; with
// nothing running the first Ctrl-C stops; no answer within AskLimit counts as
// work running.
//
//dsh:test DH-T-CLI-8
func TestSignalNeverConfirms(t *testing.T) {
	t.Run("sigterm with work", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			m := start(t, func(context.Context) []string { t.Error("a signal asked the host services"); return nil })
			m.signal(syscall.SIGTERM)
			if got := m.app.stops(); len(got) != 1 || got[0] != (stopCall{launch.Terminated, output.OK}) {
				t.Fatalf("Stop calls %v, want one terminate", got)
			}
		})
	})
	t.Run("sigterm while confirming", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			m := start(t, func(context.Context) []string { return []string{"job"} })
			m.signal(os.Interrupt)
			m.signal(syscall.SIGTERM)
			if got := m.app.stops(); len(got) != 1 || got[0] != (stopCall{launch.Terminated, output.OK}) {
				t.Fatalf("Stop calls %v, want one terminate", got)
			}
		})
	})
	t.Run("nothing running", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			m := start(t, func(context.Context) []string { return nil })
			m.signal(os.Interrupt)
			if got := m.app.stops(); len(got) != 1 || got[0] != (stopCall{launch.Interrupted, output.Interrupted}) {
				t.Fatalf("Stop calls %v, want one interrupt", got)
			}
		})
	})
	t.Run("no answer", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			m := start(t, func(ctx context.Context) []string { <-ctx.Done(); return nil })
			m.signal(os.Interrupt)
			clock.Advance(AskLimit)
			if s := m.current(); s != Confirming || len(m.app.stops()) != 0 {
				t.Fatalf("state %s, stops %v; want confirming and no stop", s, m.app.stops())
			}
			if out := m.notify.String(); !strings.Contains(out, "did not answer") {
				t.Errorf("the prompt does not say the host services did not answer:\n%s", out)
			}
		})
	})
}

// A closed input is no request: closing the triggers or the signals, before or
// during the stop, calls no Stop and leaves Watch serving the other input, so a
// later Ctrl-C still stops or forces. (A closed input that stayed selected would
// keep Watch busy, and Settle would never return.)
//
//dsh:test DH-T-CLI-8
func TestClosedInputsStopNothing(t *testing.T) {
	for _, closed := range []string{"triggers", "signals"} {
		for _, phase := range []string{"running", "stopping"} {
			t.Run(closed+" while "+phase, func(t *testing.T) {
				clock.Run(t, func(t *testing.T) {
					m := start(t, nil)
					ctrlC := func() { m.signal(os.Interrupt) }
					if closed == "signals" {
						ctrlC = func() { m.fire(Interrupt) }
					}
					want := 0
					if phase == "stopping" {
						m.app.Stop(launch.Requested, output.OK)
						clock.Settle()
						want = 1
					}
					if closed == "triggers" {
						close(m.triggers)
					} else {
						close(m.signals)
					}
					clock.Settle()
					if got := m.app.stops(); len(got) != want {
						t.Fatalf("closing the %s made Stop calls %v", closed, got)
					}
					ctrlC()
					switch phase {
					case "running":
						if got := m.app.stops(); len(got) != 1 || got[0] != (stopCall{launch.Interrupted, output.Interrupted}) {
							t.Fatalf("Ctrl-C after the close: Stop calls %v, want one Interrupted", got)
						}
						if s := m.current(); s != Stopping {
							t.Errorf("state %s, want stopping", s)
						}
					case "stopping":
						if !urgent(m.app) || m.current() != Forced {
							t.Fatalf("Ctrl-C during the stop after the close did not force (state %s)", m.current())
						}
					}
				})
			})
		}
	}
}

// The watch outlives ctx: when ctx ends, run (boot.Run) stops the launch and
// shuts down, and a Ctrl-C during that shutdown still forces it instead of
// waiting, unread, for the whole bound.
//
//dsh:test DH-T-CLI-8
func TestProcessWatchesThroughContextShutdown(t *testing.T) {
	app := launch.NewApp()
	triggers := make(chan Trigger)
	ctx, cancel := context.WithCancel(context.Background())
	status := make(chan int, 1)
	go func() {
		status <- Process(ctx, app, Inputs{Triggers: triggers}, func(ctx context.Context) int {
			<-ctx.Done()
			app.Stop(launch.Interrupted, output.Interrupted)
			// The cleanup is blocked: only a forced stop ends it.
			select {
			case <-app.Urgent():
				return output.Forced
			case <-time.After(time.Minute):
				return output.OK
			}
		})
	}()
	cancel()
	<-app.Stopping()
	select {
	case triggers <- Interrupt:
	case <-time.After(10 * time.Second):
		t.Fatal("no watcher read the Ctrl-C after the context ended")
	}
	select {
	case got := <-status:
		if got != output.Forced {
			t.Fatalf("status %d, want %d (forced)", got, output.Forced)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Process did not return after the forcing Ctrl-C")
	}
}

// A Ctrl-C that select hands over before it observes another caller's stop
// still forces, on a host with Busy too: the machine reads the app's stopping
// state before it would ask. The first case drives exactly that interleaving;
// the second leaves the order to select, with the interrupt ready from the
// start.
//
//dsh:test DH-T-CLI-8
func TestCtrlCDuringAnotherCallersStopForcesWithBusy(t *testing.T) {
	busy := func(context.Context) []string { return []string{"agent a1"} }
	t.Run("interrupt first", func(t *testing.T) {
		app := newApp()
		app.Stop(launch.Requested, output.OK)
		w := &watcher{ctx: context.Background(), app: app, in: Inputs{Busy: busy, Notify: io.Discard}, answers: make(chan answer, 1)}
		w.on(Interrupt)
		if w.state != Forced || !urgent(app) {
			t.Fatalf("state %s, urgent %v: a Ctrl-C during the stop asked instead of forcing", w.state, urgent(app))
		}
		if r, status, _ := app.Result(); r != launch.Requested || status != output.OK {
			t.Errorf("the first stop's result became (%s, %d)", r, status)
		}
	})
	t.Run("ready interrupt", func(t *testing.T) {
		for i := range 50 {
			app := newApp()
			app.Stop(launch.Requested, output.OK)
			triggers := make(chan Trigger, 1)
			triggers <- Interrupt
			done := make(chan State, 1)
			go func() { done <- Watch(context.Background(), app, Inputs{Triggers: triggers, Busy: busy}) }()
			select {
			case s := <-done:
				if s != Forced || !urgent(app) {
					t.Fatalf("run %d: state %s, urgent %v", i, s, urgent(app))
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("run %d: the Ctrl-C during the stop never forced", i)
			}
		}
	})
}

// DH-T-CLI-9, over the source: lifecycle starts no exit timer, disposes no tree
// and never exits the process; the bound is boot's.
//
//dsh:test DH-T-CLI-9
func TestLifecycleOwnsNoTreeNoTimerNoExit(t *testing.T) {
	src, err := os.ReadFile("lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"os.Exit", "Dispose(", "Unload(", "time.AfterFunc", "time.NewTimer", "WithDeadline("} {
		if strings.Contains(string(src), banned) {
			t.Errorf("lifecycle.go uses %s", banned)
		}
	}
}
