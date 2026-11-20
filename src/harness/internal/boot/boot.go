// Package boot runs one launch of a profile (DH-D-BOOT-5): the launch facts, the
// home check, the guards, the layers, the composition, the tree, the audit,
// readiness, the run and a bounded shutdown. Run is the only code that unloads the
// root tree; launch.App.Stop is the only way to end it.
package boot

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Error codes of the boot sequence.
const (
	// ReasonRequired: a required entry is absent, disabled or not active (status 78).
	ReasonRequired herr.Reason = "boot/required"
	// ReasonHost: the tree could not be prepared (status 70).
	ReasonHost herr.Reason = "boot/host-preparation"
	// ReasonPanic: the boot goroutine panicked (status 70).
	ReasonPanic herr.Reason = "boot/panic"
)

// Clock is the time boot bounds its waits with; the test kit's manual clock
// shortens a 20 s bound to no wall time.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options are the inputs of one launch.
type Options struct {
	// Request is what the command line resolved: arguments, home flag, profile.
	Request launch.Request
	// Profile is the definition of the profile launched.
	Profile profile.Definition
	// Stderr is the diagnostic stream; nil discards. Boot never writes standard
	// output, which may carry a protocol.
	Stderr io.Writer
	// Catalog resolves entry names; nil is the built-in catalogue.
	Catalog *catalog.Catalog
	// App is the stop control; nil makes a new one.
	App *launch.Control
	// Clock bounds start-up and shutdown; nil is wall time.
	Clock Clock
	// Steps replaces the real steps; nil runs them. A test passes fakes.
	Steps Steps
}

// Host is the live tree as shutdown sees it.
type Host interface {
	// Unload disposes the root, leaves first, and returns once every cleanup
	// returned.
	Unload()
	// Stuck lists the cleanups still running: the watchdog's view.
	Stuck() []string
	// Processes is the registry of the process trees the tree started.
	Processes() *subprocess.Registry
}

// State is what the steps build, step by step.
type State struct {
	Options Options
	App     *launch.Control
	Clock   Clock
	Stderr  io.Writer

	// Facts
	Data launch.Data
	Env  *launch.Snapshot
	// Home
	Home home.Layout
	// Guards
	Diag    *diag.Path
	Ring    *diag.Ring
	release []func()
	// Layers
	Layers []loader.Layer
	Issues []loader.Issue
	// Compose
	Composed []loader.EntryOptions
	Index    *loader.Index
	// Tree
	Host    Host
	Service *loader.Service
	// Settle
	ApplyErr error
	// Audit
	Audit Audit
}

// OnRelease registers a function Run calls when it returns, last registered first.
func (s *State) OnRelease(f func()) { s.release = append(s.release, f) }

// Steps are the steps of the boot sequence, in the order Run calls them. Each
// reads and fills the State.
type Steps interface {
	Facts(ctx context.Context, s *State) error
	Home(ctx context.Context, s *State) error
	Guards(ctx context.Context, s *State) error
	Layers(ctx context.Context, s *State) error
	Compose(ctx context.Context, s *State) error
	Tree(ctx context.Context, s *State) error
	Settle(ctx context.Context, s *State) error
	Audit(ctx context.Context, s *State) error
}

// Run runs one launch and returns its exit status: 0 or the status the first stop
// gave; a failure of the facts, home or layers is printed as one line with its own
// status (5 unknown profile, 78 a broken layer); 70 a fatal error with a report; 78
// a required entry that did not load, with a report; 124 the shutdown bound passed.
func Run(ctx context.Context, o Options) (status int) {
	s := &State{Options: o, App: o.App, Clock: o.Clock, Stderr: o.Stderr}
	if s.App == nil {
		s.App = launch.NewApp()
	}
	if s.Clock == nil {
		s.Clock = wallClock{}
	}
	if s.Stderr == nil {
		s.Stderr = io.Discard
	}
	steps := o.Steps
	if steps == nil {
		steps = Real{}
	}
	defer func() {
		for i := len(s.release) - 1; i >= 0; i-- {
			s.release[i]()
		}
	}()
	defer func() {
		if v := recover(); v != nil {
			err := herr.Newf(ReasonPanic, "panic: %v\n%s", v, debug.Stack())
			s.fatal("panic", err)
			status = s.shutdown(output.Fatal, s.limits().FatalReleaseTimeout)
		}
	}()

	// Steps 1 to 4: nothing is loaded yet; a failure is one line.
	for _, step := range []func(context.Context, *State) error{steps.Facts, steps.Home} {
		if err := step(ctx, s); err != nil {
			return s.fail(err)
		}
	}
	if err := steps.Guards(ctx, s); err != nil {
		return s.fail(err)
	}
	if s.Diag == nil {
		s.Diag = diag.New(diag.Config{Stderr: s.Stderr})
	}
	for _, step := range []func(context.Context, *State) error{steps.Layers, steps.Compose} {
		if err := step(ctx, s); err != nil {
			return s.fail(err)
		}
	}
	// Step 5: the tree.
	if err := steps.Tree(ctx, s); err != nil {
		s.fatal("boot", herr.Wrap(ReasonHost, err, "host preparation failed"))
		return s.shutdown(output.Fatal, s.limits().FatalReleaseTimeout)
	}
	// Steps 6 and 7: apply, settle and audit.
	if err := steps.Settle(ctx, s); err != nil {
		s.fatal("boot", herr.Wrap(ReasonHost, err, "host preparation failed"))
		return s.shutdown(output.Fatal, s.limits().FatalReleaseTimeout)
	}
	// A stop during start-up (a component's App.Stop, the fatal path's release, the
	// context's end) skips the audit and readiness.
	if s.stopRequested(ctx) {
		return s.shutdownStopped()
	}
	if err := steps.Audit(ctx, s); err != nil {
		if s.stopRequested(ctx) {
			return s.shutdownStopped()
		}
		// The tree is unloaded first, so the report is the last word.
		forced := s.shutdown(output.Status(err), s.limits().ShutdownTimeout)
		s.fatal("boot", err)
		return forced
	}
	// Step 8: ready, unless a stop came first; MarkReady refuses then.
	if s.stopRequested(ctx) || !s.App.MarkReady() {
		return s.shutdownStopped()
	}
	s.printAudit()
	// Step 9: run until the first stop.
	select {
	case <-s.App.Stopping():
	case <-ctx.Done():
		s.App.Stop(launch.Interrupted, output.Interrupted)
	}
	// Step 10: bounded shutdown.
	return s.shutdownStopped()
}

// stopRequested reports whether the app is stopping; the context's end is an
// Interrupted stop.
func (s *State) stopRequested(ctx context.Context) bool {
	if ctx.Err() != nil {
		s.App.Stop(launch.Interrupted, output.Interrupted)
	}
	select {
	case <-s.App.Stopping():
		return true
	default:
		return false
	}
}

// shutdownStopped runs the bounded shutdown with the first stop's status; a fatal
// stop has FatalReleaseTimeout, any other ShutdownTimeout.
func (s *State) shutdownStopped() int {
	reason, code, _ := s.App.Result()
	budget := s.limits().ShutdownTimeout
	if reason == launch.Fatal {
		budget = s.limits().FatalReleaseTimeout
	}
	return s.shutdown(code, budget)
}

// limits are the profile's limits, defaults filling what is unset.
func (s *State) limits() profile.Limits {
	l, d := s.Options.Profile.Limits, profile.DefaultLimits()
	if l.StartupTimeout <= 0 {
		l.StartupTimeout = d.StartupTimeout
	}
	if l.ShutdownTimeout <= 0 {
		l.ShutdownTimeout = d.ShutdownTimeout
	}
	if l.FatalReleaseTimeout <= 0 {
		l.FatalReleaseTimeout = d.FatalReleaseTimeout
	}
	if l.CleanupWarnAfter <= 0 {
		l.CleanupWarnAfter = d.CleanupWarnAfter
	}
	return l
}

// fail prints a failure of a step that loaded nothing, as the command line prints
// any failure, and returns its status.
func (s *State) fail(err error) int {
	name := s.Options.Profile.Name
	if name == "" {
		name = s.Options.Request.Profile
	}
	return output.New(io.Discard, s.Stderr, output.Text).Fail(name, err)
}

// fatal reports err once through the fatal path.
func (s *State) fatal(source string, err error) {
	if s.Diag == nil {
		s.Diag = diag.New(diag.Config{Stderr: s.Stderr})
	}
	s.Diag.Fatal(source, err)
}

// printAudit writes the boot audit: every outbound permit, and one warning for
// the entries that are not active.
func (s *State) printAudit() {
	_, _ = io.WriteString(s.Stderr, s.Audit.PermitLine()+"\n")
	if s.Ring != nil {
		_, _ = fmt.Fprintf(s.Ring, "%s\n", s.Audit.PermitLine())
	}
	if line := s.Audit.WarningLine(); line != "" {
		_, _ = io.WriteString(s.Stderr, line+"\n")
	}
}
