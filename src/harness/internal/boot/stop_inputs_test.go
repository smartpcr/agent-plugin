package boot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/lifecycle"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// workSteps fake every step; the tree's registry holds procs.
type workSteps struct {
	fakeSteps
	procs *subprocess.Registry
}

func (w *workSteps) Tree(_ context.Context, s *State) error {
	w.record(s, "tree")
	s.Host = &workHost{procs: w.procs}
	return nil
}

type workHost struct{ procs *subprocess.Registry }

func (h *workHost) Unload()                         {}
func (h *workHost) Stuck() []string                 { return nil }
func (h *workHost) Processes() *subprocess.Registry { return h.procs }

// DH-ADR-CLI-5 clause 3 through Process's own inputs: a web launch on a
// terminal asks the live tree what is running; a first Ctrl-C with a command
// running prints it and stops nothing, and a second press stops (status 130).
//
//dsh:test DH-T-CLI-8
func TestHostOnATerminalConfirmsTheFirstCtrlC(t *testing.T) {
	procs := subprocess.NewRegistry()
	procs.Add(subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn)))
	app := launch.NewApp()
	var stderr testkit.Buffer
	o := Options{Profile: profile.Definition{Name: profile.Web}, Terminal: true, Stderr: &stderr, App: app,
		Steps: &workSteps{procs: procs}}
	in := stopInputs(&o)
	if in.Busy == nil {
		t.Fatal("a web launch on a terminal has no work summary")
	}
	if work := in.Busy(context.Background()); len(work) != 0 {
		t.Fatalf("work %q before the tree exists", work)
	}
	triggers := make(chan lifecycle.Trigger)
	in.Triggers = triggers
	status := make(chan int, 1)
	go func() {
		status <- lifecycle.Process(context.Background(), app, in, func(ctx context.Context) int { return Run(ctx, o) })
	}()
	select {
	case <-app.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("never ready")
	}
	triggers <- lifecycle.Interrupt
	waitFor(t, "the confirming prompt", &stderr, func() bool { return strings.Contains(stderr.String(), "press Ctrl-C again") })
	if out := stderr.String(); !strings.Contains(out, "dsh: running: 1 command") {
		t.Errorf("the prompt does not list the running command:\n%s", out)
	}
	select {
	case <-app.Stopping():
		t.Fatal("the first Ctrl-C stopped running work")
	default:
	}
	triggers <- lifecycle.Interrupt
	select {
	case got := <-status:
		if got != output.Interrupted {
			t.Fatalf("status %d, want %d", got, output.Interrupted)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the second Ctrl-C did not end the launch")
	}
}

// A one-shot, a stdio carrier and a launch without a terminal never ask: their
// first Ctrl-C stops (DH-ADR-CLI-5 clauses 2 and 5).
//
//dsh:test DH-T-CLI-8
func TestOnlyAHostOnATerminalAsks(t *testing.T) {
	for _, c := range []struct {
		profile  string
		terminal bool
	}{{profile.Web, false}, {profile.Headless, true}, {profile.SDK, true}} {
		o := Options{Profile: profile.Definition{Name: c.profile}, Terminal: c.terminal}
		if in := stopInputs(&o); in.Busy != nil || o.onHost != nil {
			t.Errorf("%s (terminal %v) asks before stopping", c.profile, c.terminal)
		}
	}
}
