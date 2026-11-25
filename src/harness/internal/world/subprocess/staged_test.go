package subprocess_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// fakeStage is a sandbox.Stage over a RoleChannel child: Wrap hands the child one
// pipe end through pass and makes it write "ready" there; Confirm reads that line
// and answers with confinement or reject. Each call is counted.
type fakeStage struct {
	wrapErr     error
	reject      error
	confinement sandbox.Confinement

	r, w                      *os.File
	wraps, confirms, releases atomic.Int32
	order                     []string
}

func (s *fakeStage) Wrap(argv []string, pass sandbox.Pass) (sandbox.Wrapped, error) {
	s.wraps.Add(1)
	s.order = append(s.order, "wrap")
	if s.wrapErr != nil {
		return sandbox.Wrapped{}, s.wrapErr
	}
	r, w, err := os.Pipe()
	if err != nil {
		return sandbox.Wrapped{}, err
	}
	s.r, s.w = r, w
	fd := pass(w)
	return sandbox.Wrapped{Argv: append(argv, strconv.FormatUint(uint64(fd), 10), "ready"), Env: []string{"FAKE_STAGE=1"}}, nil
}

func (s *fakeStage) Confirm(context.Context) (sandbox.Confinement, error) {
	s.confirms.Add(1)
	s.order = append(s.order, "confirm")
	_ = s.w.Close()
	line, err := bufio.NewReader(s.r).ReadString('\n')
	if err != nil && line == "" {
		return sandbox.Confinement{}, err
	}
	if line != "ready" && line != "ready\n" {
		return sandbox.Confinement{}, errors.New("the stage sent " + strconv.Quote(line))
	}
	if s.reject != nil {
		return sandbox.Confinement{}, s.reject
	}
	return s.confinement, nil
}

func (s *fakeStage) Release(context.Context) error {
	s.releases.Add(1)
	s.order = append(s.order, "release")
	if s.r != nil {
		_ = s.r.Close()
	}
	return nil
}

func stagedLaunch(s *fakeStage) sandbox.Launch {
	return sandbox.Staged(sandboxtest.Policy(sandbox.DangerFullAccess), s, "fake stage")
}

// TestSpecWithoutAPlanIsRefused: a Spec with no Launch is an herr error and the
// fake spawner records zero starts (TC-04a); so is a staged plan whose Wrap
// fails, and its Release still runs once.
//
//dsh:test DH-T-EXEC-8
func TestSpecWithoutAPlanIsRefused(t *testing.T) {
	const token = "plan-required-probe"
	spawns := subprocess.CountSpawns(t, token)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := subprocess.Spec{Argv: []string{exe, token}}
	if _, err := subprocess.Start(context.Background(), spec); herr.Kind(err) != subprocess.ReasonNoLaunch {
		t.Fatalf("Start without a plan = %v, want %s", err, subprocess.ReasonNoLaunch)
	}
	if _, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{}); herr.Kind(err) != subprocess.ReasonNoLaunch {
		t.Fatalf("Run without a plan = %v, want %s", err, subprocess.ReasonNoLaunch)
	}
	if n := spawns.Load(); n != 0 {
		t.Fatalf("the spawner recorded %d starts of a Spec without a plan, want 0", n)
	}

	stage := &fakeStage{wrapErr: herr.New(sandbox.CodeLaunchFailed, "no pair")}
	spec.Launch = stagedLaunch(stage)
	if _, err := subprocess.Start(context.Background(), spec); herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("Start with a failing Wrap = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	if n := spawns.Load(); n != 0 {
		t.Fatalf("the spawner recorded %d starts after Wrap failed, want 0", n)
	}
	if w, c, r := stage.wraps.Load(), stage.confirms.Load(), stage.releases.Load(); w != 1 || c != 0 || r != 1 {
		t.Fatalf("Wrap, Confirm, Release ran %d, %d, %d times; want 1, 0, 1", w, c, r)
	}

	spec.Launch = subprocesstest.Launch
	spec.Env = subprocess.Env{subprocesstest.RoleEnv + "=" + subprocesstest.RoleExit}
	if _, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{}); err != nil {
		t.Fatalf("Run with a plan: %v", err)
	}
	if n := spawns.Load(); n != 1 {
		t.Fatalf("the spawner recorded %d starts of a Spec with a plan, want 1", n)
	}
}

// TestStagedPlanSequence: Start runs Wrap, the start, Confirm and Release in that
// order, once each; the stage's channel is numbered after the caller's own Extra;
// the confirmed Confinement is on the handle and on Run's Result.
//
//dsh:test DH-T-EXEC-8
func TestStagedPlanSequence(t *testing.T) {
	own, ownW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = own.Close(); _ = ownW.Close() })
	want := sandbox.Confinement{State: sandbox.StatePartial, Backend: sandbox.BackendBwrap, Missing: []sandbox.Effect{"network"}}
	stage := &fakeStage{confinement: want}
	spec := subprocesstest.Spec(t, subprocesstest.RoleChannel)
	spec.Extra = []subprocess.Channel{{File: ownW}}
	spec.Launch = stagedLaunch(stage)
	res, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stage.order; len(got) != 3 || got[0] != "wrap" || got[1] != "confirm" || got[2] != "release" {
		t.Fatalf("the plan's steps ran as %v, want [wrap confirm release]", got)
	}
	if res.Confinement.State != want.State || res.Confinement.Backend != want.Backend || len(res.Confinement.Missing) != 1 {
		t.Fatalf("Result.Confinement = %+v, want %+v", res.Confinement, want)
	}
	if res.ExitCode != 0 {
		t.Fatalf("the staged child exited %d", res.ExitCode)
	}
}

// TestRejectedConfirmStopsTheStage: a Confirm that rejects is Start's error, the
// stage's tree is stopped and Release ran once.
//
//dsh:test DH-T-EXEC-8
func TestRejectedConfirmStopsTheStage(t *testing.T) {
	stage := &fakeStage{reject: herr.New(sandbox.CodeEnforcementInsufficient, "require full")}
	spec := subprocesstest.Spec(t, subprocesstest.RoleChannel)
	spec.Launch = stagedLaunch(stage)
	p, err := subprocess.Start(context.Background(), spec)
	if p != nil || herr.Kind(err) != sandbox.CodeEnforcementInsufficient {
		t.Fatalf("Start = %v, %v; want no handle and %s", p, err, sandbox.CodeEnforcementInsufficient)
	}
	if c, r := stage.confirms.Load(), stage.releases.Load(); c != 1 || r != 1 {
		t.Fatalf("Confirm and Release ran %d and %d times, want 1 and 1", c, r)
	}
}

// TestStagedPlanNeedsAValidConfinement: a Confirm that returns no valid
// Confinement does not let the command run.
func TestStagedPlanNeedsAValidConfinement(t *testing.T) {
	stage := &fakeStage{confinement: sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendBwrap}}
	spec := subprocesstest.Spec(t, subprocesstest.RoleChannel)
	spec.Launch = stagedLaunch(stage)
	if _, err := subprocess.Start(context.Background(), spec); herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("Start = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	if r := stage.releases.Load(); r != 1 {
		t.Fatalf("Release ran %d times, want 1", r)
	}
}

// A plan that starts the command itself reports unconfined, backend none.
func TestUnstagedPlanReportsUnconfined(t *testing.T) {
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleExit))
	_ = p.Wait()
	if c := p.Confinement(); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone {
		t.Fatalf("Confinement = %+v, want unconfined, backend none", c)
	}
}
