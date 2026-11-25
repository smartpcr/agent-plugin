package launch_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// stagedStart is a marker command started by subprocess.Start through a
// NewLaunch plan whose decision is confirm; it returns the outcome, the marker's
// path, the number of times Release ran and the start's error.
func stagedStart(t *testing.T, confirm launch.Confirm) (p *subprocess.Process, marker string, released *atomic.Int32, err error) {
	t.Helper()
	marker = filepath.Join(t.TempDir(), "marker")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, released, err = stagedStartWith(t, launch.Hooks{Confirm: confirm}, subprocess.Spec{Argv: []string{exe, markerRole, marker}})
	return p, marker, released, err
}

// stagedStartWith starts spec (the test binary re-executed, its Launch filled in)
// through a NewLaunch plan with hooks h, counting Release.
func stagedStartWith(t *testing.T, h launch.Hooks, spec subprocess.Spec) (p *subprocess.Process, released *atomic.Int32, err error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	released = new(atomic.Int32)
	h.Release = func(context.Context) error { released.Add(1); return nil }
	h.Exe = exe
	plan, err := launch.NewLaunch(sandboxtest.Policy(sandbox.DangerFullAccess), unconfinedPlan(), h)
	if err != nil {
		t.Fatal(err)
	}
	env := subprocess.Env{reexecEnv + "=1"}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	spec.Env = append(env, spec.Env...)
	spec.Launch = plan
	p, err = subprocess.Start(timeout(t), spec)
	if p != nil {
		t.Cleanup(func() { _ = p.StopTree(context.Background(), 0) })
	}
	return p, released, err
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// TestRejectedConfirmLeavesNoCommand: the one starter runs a real stage, the
// Confirm hook rejects its ready; the marker is absent, the outcome is the
// rejection and Release ran once (TC-04b, DH-D-EXEC-4).
//
//dsh:test DH-T-EXEC-8
func TestRejectedConfirmLeavesNoCommand(t *testing.T) {
	saw := launch.Ready{Backend: "unset"}
	p, marker, released, err := stagedStart(t, func(_ context.Context, _ launch.Plan, r launch.Ready) (sandbox.Confinement, error) {
		saw = r
		return sandbox.Confinement{}, herr.New(sandbox.CodeEnforcementInsufficient, "the test refuses")
	})
	if p != nil || herr.Kind(err) != sandbox.CodeEnforcementInsufficient {
		t.Fatalf("Start = %v, %v; want no handle and %s", p, err, sandbox.CodeEnforcementInsufficient)
	}
	if saw.Backend != sandbox.BackendNone {
		t.Fatalf("the decision saw ready %+v, want the unconfined stage's", saw)
	}
	if exists(t, marker) {
		t.Fatal("the command ran although Confirm rejected the stage")
	}
	if n := released.Load(); n != 1 {
		t.Fatalf("Release ran %d times, want 1", n)
	}
}

// TestConfirmedPlanRunsTheCommand: a Confirm that accepts sends go; the stage
// becomes the command, the marker exists and the handle and Run's Result carry
// the confirmed Confinement.
//
//dsh:test DH-T-EXEC-8
func TestConfirmedPlanRunsTheCommand(t *testing.T) {
	p, marker, released, err := stagedStart(t, func(context.Context, launch.Plan, launch.Ready) (sandbox.Confinement, error) {
		return sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendNone}, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Wait(); err != nil || p.ExitCode() != 0 {
		t.Fatalf("Wait = %v, exit %d", err, p.ExitCode())
	}
	if !exists(t, marker) {
		t.Fatal("the confirmed command did not run")
	}
	if c := p.Confinement(); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone {
		t.Fatalf("Confinement = %+v, want unconfined, backend none", c)
	}
	if n := released.Load(); n != 1 {
		t.Fatalf("Release ran %d times, want 1", n)
	}
}

// NewLaunch refuses a plan with no decision or with the parts Wrap fills in.
func TestNewLaunchRefusesAnIncompletePlan(t *testing.T) {
	policy := sandboxtest.Policy(sandbox.DangerFullAccess)
	accept := func(context.Context, launch.Plan, launch.Ready) (sandbox.Confinement, error) {
		return sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendNone}, nil
	}
	if _, err := launch.NewLaunch(policy, unconfinedPlan(), launch.Hooks{}); herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("NewLaunch without Confirm = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	p := unconfinedPlan()
	p.Argv = []string{"x"}
	if _, err := launch.NewLaunch(policy, p, launch.Hooks{Confirm: accept}); herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("NewLaunch with an argv = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	l, err := launch.NewLaunch(policy, unconfinedPlan(), launch.Hooks{Confirm: accept})
	if err != nil || !l.Valid() || l.Stage() == nil {
		t.Fatalf("NewLaunch = %v, %v; want a valid staged plan", l, err)
	}
}

// TestLateConfirmLeavesNoCommand: a decision that returns a valid Confinement
// only after the launch limit has passed authorizes nothing: the stage is
// rejected, the marker is absent and Release ran once (DH-D-EXEC-4).
//
//dsh:test DH-T-EXEC-8
func TestLateConfirmLeavesNoCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	late := func(ctx context.Context, _ launch.Plan, _ launch.Ready) (sandbox.Confinement, error) {
		<-ctx.Done()
		return sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendNone}, nil
	}
	p, released, err := stagedStartWith(t, launch.Hooks{Confirm: late, Limit: 200 * time.Millisecond},
		subprocess.Spec{Argv: []string{exe, markerRole, marker}})
	if p != nil || herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("Start = %v, %v; want no handle and %s", p, err, sandbox.CodeLaunchFailed)
	}
	// The stage was rejected; give a wrongly released command time to show.
	time.Sleep(300 * time.Millisecond)
	if exists(t, marker) {
		t.Fatal("the command ran on a decision made after the launch limit")
	}
	if n := released.Load(); n != 1 {
		t.Fatalf("Release ran %d times, want 1", n)
	}
}

// TestStagedCommandKeepsTheCallersChannels: a channel of the caller's
// Spec.Extra passes through the real stage to the command it becomes, under the
// number ChildFD gave, on both lanes (POSIX exec, the Windows child of Become).
//
//dsh:test DH-T-EXEC-8
func TestStagedCommandKeepsTheCallersChannels(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	extra := []subprocess.Channel{{File: w}}
	fd := strconv.FormatUint(uint64(subprocess.ChildFD(extra, 0)), 10)
	accept := func(context.Context, launch.Plan, launch.Ready) (sandbox.Confinement, error) {
		return sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendNone}, nil
	}
	p, released, err := stagedStartWith(t, launch.Hooks{Confirm: accept},
		subprocess.Spec{Argv: []string{exe, channelRole, fd}, Extra: extra})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = w.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil || p.ExitCode() != 0 {
		t.Fatalf("Wait = %v, exit %d", err, p.ExitCode())
	}
	if string(got) != channelText {
		t.Fatalf("the caller's channel carried %q, want %q", got, channelText)
	}
	if n := released.Load(); n != 1 {
		t.Fatalf("Release ran %d times, want 1", n)
	}
}
