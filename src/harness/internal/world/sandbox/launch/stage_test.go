package launch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// reexecEnv marks the test binary re-executed as the stage or as the command the
// stage becomes; its first argument says which.
const reexecEnv = "LAUNCHTEST_REEXEC"

// markerRole is the command: it creates the file named by its argument, so the
// marker exists exactly when a command ran.
const markerRole = "marker"

// diesEnv makes the re-executed stage exit before it sends ready.
const diesEnv = "LAUNCHTEST_STAGE_DIES"

func TestMain(m *testing.M) {
	if os.Getenv(reexecEnv) == "1" {
		os.Exit(reexec(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func reexec(args []string) int {
	if len(args) > 0 && args[0] == launch.Mode && os.Getenv(diesEnv) == "1" {
		return 3
	}
	if code, ok := launch.ServeIfAsked(args, os.Stderr); ok {
		return code
	}
	if len(args) == 2 && args[0] == markerRole {
		if err := os.WriteFile(args[1], []byte("ran"), 0o644); err != nil {
			return 1
		}
		return 0
	}
	if len(args) == 2 && args[0] == channelRole {
		n, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return 1
		}
		f := os.NewFile(uintptr(n), "kept channel")
		if f == nil {
			return 1
		}
		if _, err := f.WriteString(channelText); err != nil {
			return 1
		}
		return 0
	}
	return 3
}

// channelRole is a command that writes channelText to the descriptor its
// argument names: a channel the caller passed in Spec.Extra.
const channelRole = "channel"

const channelText = "through the stage"

// lockedBuffer is a stage's standard error, written by the copier and read by the
// test after Wait.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// stage is one started stage and its host channel.
type stage struct {
	plan   launch.Plan
	ch     *launch.Channel
	p      *subprocess.Process
	marker string
	stderr *lockedBuffer
}

// startStage re-executes the test binary as `sandbox-launch` with plan, whose
// command defaults to the marker helper, and returns it once started. extraEnv
// reaches the stage and the command.
func startStage(t *testing.T, plan launch.Plan, extraEnv ...string) *stage {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	if plan.Argv == nil {
		plan.Argv = []string{exe, markerRole, marker}
	}
	ch, err := launch.NewChannel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ch.Close)
	plan = ch.Bind(plan)
	env := subprocess.Env{reexecEnv + "=1"}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	env = append(env, extraEnv...)
	stderr := &lockedBuffer{}
	p := subprocesstest.Start(t, subprocess.Spec{
		Argv:   append([]string{exe, launch.Mode}, plan.Args()...),
		Env:    env,
		Stderr: stderr,
		Launch: subprocesstest.Launch,
		Extra:  ch.Extra(),
	})
	ch.CloseChildEnds()
	return &stage{plan: plan, ch: ch, p: p, marker: marker, stderr: stderr}
}

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// exitCode waits for the stage and returns its exit code.
func (s *stage) exitCode(t *testing.T) int {
	t.Helper()
	_ = s.p.Wait()
	return s.p.ExitCode()
}

func (s *stage) markerExists(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(s.marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func unconfinedPlan() launch.Plan {
	return launch.Plan{Kind: launch.Unconfined, Backend: sandbox.BackendNone}
}

// TestNoCommandBeforeGo: the host reads ready and closes the pair without go; the
// stage exits non-zero and the command never ran (TC-04b, INV-14).
//
//dsh:test DH-T-SANDBOX-4
func TestNoCommandBeforeGo(t *testing.T) {
	s := startStage(t, unconfinedPlan())
	ready, err := s.ch.ReadReady(timeout(t))
	if err != nil {
		t.Fatalf("ReadReady: %v", err)
	}
	if s.markerExists(t) {
		t.Fatal("the command ran before go")
	}
	if ready.Backend != sandbox.BackendNone {
		t.Errorf("ready backend = %s, want none", ready.Backend)
	}
	s.ch.Reject()
	if code := s.exitCode(t); code == 0 {
		t.Errorf("the stage exited 0 after a rejection")
	} else if code != launch.ExitFailed {
		t.Errorf("the stage exited %d, want %d", code, launch.ExitFailed)
	}
	if s.markerExists(t) {
		t.Error("the command ran without go")
	}
	if got := s.stderr.String(); !strings.Contains(got, "no command ran") {
		t.Errorf("stage stderr = %q, want it to say no command ran", got)
	}
}

// TestUnconfinedPlanReachesGo: a danger-full-access plan under backend none
// reports canary not-tried, the host agrees and sends go, and the command runs
// (TC-04b).
//
//dsh:test DH-T-SANDBOX-4
func TestUnconfinedPlanReachesGo(t *testing.T) {
	ctx := timeout(t)
	s := startStage(t, unconfinedPlan())
	ready, err := s.ch.ReadReady(ctx)
	if err != nil {
		t.Fatalf("ReadReady: %v", err)
	}
	if ready.Backend != sandbox.BackendNone || ready.Canary != launch.CanaryNotTried || len(ready.Governed) != 0 {
		t.Errorf("ready = %+v, want backend none, nothing governed, canary not-tried", ready)
	}
	if err := ready.Agrees(s.plan); err != nil || s.plan.Kind != launch.Unconfined {
		t.Fatalf("an unconfined ready disagrees with its plan: %v", err)
	}
	if err := s.ch.Go(); err != nil {
		t.Fatalf("Go: %v", err)
	}
	if err := s.ch.AwaitExec(ctx); err != nil {
		t.Fatalf("AwaitExec: %v", err)
	}
	if code := s.exitCode(t); code != 0 {
		t.Errorf("the command exited %d, want 0; stderr %q", code, s.stderr.String())
	}
	if !s.markerExists(t) {
		t.Error("the command did not run after go")
	}
}

// TestHostRefusesAWrittenCanary: a confined plan whose stage could open the
// canary for writing (bubblewrap is not wrapping it here, so the honest result is
// written) fails the host's check; the host rejects and nothing runs (TC-04b).
//
//dsh:test DH-T-SANDBOX-4
func TestHostRefusesAWrittenCanary(t *testing.T) {
	canary := filepath.Join(t.TempDir(), "canary")
	if err := os.WriteFile(canary, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := startStage(t, launch.Plan{Kind: launch.Confined, Backend: sandbox.BackendBwrap, Canary: canary})
	ready, err := s.ch.ReadReady(timeout(t))
	if err != nil {
		t.Fatalf("ReadReady: %v", err)
	}
	if ready.Canary != launch.CanaryWritten {
		t.Fatalf("ready canary = %s, want written", ready.Canary)
	}
	if err := ready.Agrees(s.plan); !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Fatalf("Agrees = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	s.ch.Reject()
	if code := s.exitCode(t); code != launch.ExitFailed {
		t.Errorf("the stage exited %d, want %d", code, launch.ExitFailed)
	}
	if s.markerExists(t) {
		t.Error("the command ran under a failed confinement")
	}
}

// TestStageEndsBeforeReady: a stage that cannot confine itself (landlock is not
// in this build) ends before ready; the host reads end of file as
// sandbox/launch-failed and nothing runs.
//
//dsh:test DH-T-SANDBOX-4
func TestStageEndsBeforeReady(t *testing.T) {
	canary := filepath.Join(t.TempDir(), "canary")
	if err := os.WriteFile(canary, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := startStage(t, launch.Plan{Kind: launch.Confined, Backend: sandbox.BackendLandlock, Canary: canary})
	if r, err := s.ch.ReadReady(timeout(t)); !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Fatalf("ReadReady = %+v, %v; want %s", r, err, sandbox.CodeLaunchFailed)
	}
	if code := s.exitCode(t); code != launch.ExitFailed {
		t.Errorf("the stage exited %d, want %d", code, launch.ExitFailed)
	}
	if s.markerExists(t) {
		t.Error("the command ran without ready")
	}
}

// TestExecFailureReachesTheHost: after go, a command that cannot start is
// reported on the channel as sandbox/exec-failed, never mistaken for the
// command's own failure.
//
//dsh:test DH-T-SANDBOX-4
func TestExecFailureReachesTheHost(t *testing.T) {
	ctx := timeout(t)
	plan := unconfinedPlan()
	plan.Argv = []string{filepath.Join(t.TempDir(), "no-such-program")}
	s := startStage(t, plan)
	if _, err := s.ch.ReadReady(ctx); err != nil {
		t.Fatalf("ReadReady: %v", err)
	}
	if err := s.ch.Go(); err != nil {
		t.Fatalf("Go: %v", err)
	}
	if err := s.ch.AwaitExec(ctx); !herr.Has(err, sandbox.CodeExecFailed) {
		t.Fatalf("AwaitExec = %v, want %s", err, sandbox.CodeExecFailed)
	}
	if code := s.exitCode(t); code != launch.ExitNotFound {
		t.Errorf("the stage exited %d, want %d; %s", code, launch.ExitNotFound, s.stderr.String())
	}
}

// TestReadReadyHonoursTheLaunchLimit: a stage that never sends ready (here one
// the host never started past its pair) is sandbox/launch-failed once the
// launch time limit ends.
func TestReadReadyHonoursTheLaunchLimit(t *testing.T) {
	ch, err := launch.NewChannel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ch.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ch.ReadReady(ctx); !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Fatalf("ReadReady = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	ch.CloseChildEnds()
}
