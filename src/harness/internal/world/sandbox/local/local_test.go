package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// reexecEnv marks the test binary re-executed as the stage or as the marker
// command the stage becomes.
const reexecEnv = "LOCALTEST_REEXEC"

const markerRole = "marker"

func TestMain(m *testing.M) {
	if os.Getenv(reexecEnv) == "1" {
		os.Exit(reexec(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func reexec(args []string) int {
	if code, ok := launch.ServeIfAsked(args, os.Stderr); ok {
		return code
	}
	if len(args) == 2 && args[0] == markerRole {
		if err := os.WriteFile(args[1], []byte("ran"), 0o644); err != nil {
			return 1
		}
		return 0
	}
	return 3
}

// fake is a backend whose probe reports a fixed result.
type fake struct {
	name   sandbox.Backend
	custom bool
	probe  local.Probe
	err    error
	calls  int
	canary []string
}

func (f *fake) Name() sandbox.Backend { return f.name }
func (f *fake) Custom() bool          { return f.custom }
func (f *fake) Probe(_ context.Context, canary string) (local.Probe, error) {
	f.calls++
	f.canary = append(f.canary, canary)
	return f.probe, f.err
}

// passing is a backend whose probe refused the canary and governed governed.
func passing(name sandbox.Backend, governed ...sandbox.Effect) *fake {
	return &fake{name: name, probe: local.Probe{Canary: launch.CanaryRefused, Governed: governed}}
}

func newService(t *testing.T, chain ...local.Backend) *local.Service {
	t.Helper()
	s, err := local.New(context.Background(), local.Config{}, chain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func issue(t *testing.T, s mint.Spec) sandbox.Policy {
	t.Helper()
	p, err := sandbox.Issue(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func command(t *testing.T) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe, markerRole, filepath.Join(t.TempDir(), "marker")}
}

// TestFourStepHostDecision: with fake backends, the host's decision on each ready
// follows architecture.md sec. 4.2: a confined plan whose canary was written or
// not tried is rejected (and the chain re-probed once), an unconfined plan
// reporting not-tried is accepted Unconfined, and an unconfined plan under
// require: full is refused sandbox/enforcement-insufficient (TC-04a).
//
//dsh:test DH-T-SANDBOX-3, DH-T-SANDBOX-7
func TestFourStepHostDecision(t *testing.T) {
	all := []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectNetwork, sandbox.EffectHarnessHome, sandbox.EffectDelegatedExecution}
	confined := sandboxtest.Policy(sandbox.WorkspaceWrite, t.TempDir())
	unconfined := sandboxtest.Policy(sandbox.DangerFullAccess)
	unconfinedFull := issue(t, mint.Spec{Mode: string(sandbox.DangerFullAccess), Require: string(sandbox.RequireFull)})
	for _, row := range []struct {
		name      string
		policy    sandbox.Policy
		ready     launch.Ready
		want      herr.Reason
		state     sandbox.State
		reprobing bool
	}{
		{"confined, canary written", confined, launch.Ready{Backend: sandbox.BackendBwrap, Governed: all, Canary: launch.CanaryWritten}, sandbox.CodeLaunchFailed, "", true},
		{"confined, canary not tried", confined, launch.Ready{Backend: sandbox.BackendBwrap, Governed: all, Canary: launch.CanaryNotTried}, sandbox.CodeLaunchFailed, "", true},
		{"unconfined, canary not tried", unconfined, launch.Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: launch.CanaryNotTried}, "", sandbox.StateUnconfined, false},
		{"unconfined under require full", unconfinedFull, launch.Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: launch.CanaryNotTried}, sandbox.CodeEnforcementInsufficient, "", false},
		{"confined, canary refused", confined, launch.Ready{Backend: sandbox.BackendBwrap, Governed: all, Canary: launch.CanaryRefused}, "", sandbox.StateFull, false},
		{"unconfined, canary refused", unconfined, launch.Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: launch.CanaryRefused}, sandbox.CodeLaunchFailed, "", false},
		{"confined, other backend", confined, launch.Ready{Backend: sandbox.BackendLandlock, Governed: all, Canary: launch.CanaryRefused}, sandbox.CodeLaunchFailed, "", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := newService(t, passing(sandbox.BackendBwrap, all...))
			before := s.Probes()
			_, c, err := local.Decide(context.Background(), s, row.policy, command(t), row.ready)
			if row.want != "" {
				if herr.Kind(err) != row.want || c.Valid() {
					t.Fatalf("Confirm = %+v, %v; want a rejection with %s", c, err, row.want)
				}
			} else if err != nil || c.State != row.state {
				t.Fatalf("Confirm = %+v, %v; want %s", c, err, row.state)
			}
			if row.state == sandbox.StateUnconfined && (c.Backend != sandbox.BackendNone || !slices.Equal(c.Missing, sandbox.Effects)) {
				t.Fatalf("unconfined Confinement = %+v, want backend none and every effect missing", c)
			}
			if got, want := s.Probes()-before, map[bool]int{true: 1, false: 0}[row.reprobing]; got != want {
				t.Fatalf("the decision re-probed %d times, want %d", got, want)
			}
		})
	}
}

// TestPrepareFixesThePlanKind: Prepare selects the first backend that passed its
// probe for a confined policy and backend none for danger-full-access; a confined
// policy with no passing backend is refused sandbox/unavailable (TC-04a).
//
//dsh:test DH-T-SANDBOX-7
func TestPrepareFixesThePlanKind(t *testing.T) {
	broken := &fake{name: sandbox.BackendBwrap, err: errors.New("user namespaces are restricted")}
	written := &fake{name: sandbox.BackendLandlock, probe: local.Probe{Canary: launch.CanaryWritten}}
	s := newService(t, broken, passing(sandbox.BackendLandlock, sandbox.EffectFSWrite))
	ready := launch.Ready{Backend: sandbox.BackendLandlock, Governed: []sandbox.Effect{sandbox.EffectFSWrite}, Canary: launch.CanaryRefused}
	plan, c, err := local.Decide(context.Background(), s, sandboxtest.Policy(sandbox.ReadOnly), command(t), ready)
	if err != nil || plan.Kind != launch.Confined || plan.Backend != sandbox.BackendLandlock || plan.Canary == "" {
		t.Fatalf("Prepare = %v, %v; want a confined landlock plan with a canary", plan, err)
	}
	if c.State != sandbox.StatePartial || c.Backend != sandbox.BackendLandlock {
		t.Fatalf("Confinement = %+v, want Partial under landlock", c)
	}
	if _, err := os.Stat(plan.Canary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the start's canary %s outlived Release: %v", plan.Canary, err)
	}
	st := s.Describe(context.Background())
	if len(st) != 2 || st[0].Available || st[0].Reason == "" || !st[1].Available || st[1].Reason != "" {
		t.Fatalf("Describe = %+v", st)
	}
	if len(broken.canary) != 1 || filepath.Dir(broken.canary[0]) == "" {
		t.Fatalf("the probe was given canaries %v", broken.canary)
	}

	none := newService(t, broken, written)
	for _, m := range []sandbox.Mode{sandbox.ReadOnly, sandbox.WorkspaceWrite} {
		roots := []string{}
		if m == sandbox.WorkspaceWrite {
			roots = append(roots, t.TempDir())
		}
		l, err := none.Prepare(context.Background(), sandboxtest.Policy(m, roots...), command(t))
		if herr.Kind(err) != sandbox.CodeUnavailable || l.Valid() {
			t.Fatalf("Prepare(%s) with no passing backend = %v, %v; want %s", m, l, err, sandbox.CodeUnavailable)
		}
	}
	plan, _, err = local.Decide(context.Background(), none, sandboxtest.Policy(sandbox.DangerFullAccess), command(t),
		launch.Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: launch.CanaryNotTried})
	if err != nil || plan.Kind != launch.Unconfined || plan.Backend != sandbox.BackendNone || plan.Canary != "" {
		t.Fatalf("Prepare(danger-full-access) = %v, %v; want an unconfined plan", plan, err)
	}
	if _, err := none.Prepare(context.Background(), sandbox.Policy{}, command(t)); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("Prepare(zero policy) = %v, want %s", err, sandbox.CodeUnavailable)
	}
	if _, err := none.Prepare(context.Background(), sandboxtest.Policy(sandbox.DangerFullAccess), []string{"sh"}); herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("Prepare(relative program) = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
}

// TestWeakerEvidenceWinsAtConfirm: a probe that showed every effect governed and
// a ready that leaves out the network give Partial, Missing [network]; under
// require: full the same start is refused (TC-04a, INV-3, INV-4).
//
//dsh:test DH-T-SANDBOX-3
func TestWeakerEvidenceWinsAtConfirm(t *testing.T) {
	all := []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectNetwork, sandbox.EffectHarnessHome, sandbox.EffectDelegatedExecution}
	s := newService(t, passing(sandbox.BackendBwrap, all...))
	scratch := t.TempDir()
	p := issue(t, mint.Spec{Mode: string(sandbox.WorkspaceWrite), Roots: []mint.Root{
		{Path: t.TempDir(), Source: string(sandbox.SourceWorkingDirectory)},
		{Path: scratch, Source: string(sandbox.SourceScratch)},
	}})
	ready := launch.Ready{Backend: sandbox.BackendBwrap, Governed: []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectHarnessHome, sandbox.EffectDelegatedExecution}, Canary: launch.CanaryRefused}
	_, c, err := local.Decide(context.Background(), s, p, command(t), ready)
	if err != nil || c.State != sandbox.StatePartial || !slices.Equal(c.Missing, []sandbox.Effect{sandbox.EffectNetwork}) || c.Scratch != scratch {
		t.Fatalf("Confirm = %+v, %v; want Partial missing [network] with scratch %s", c, err, scratch)
	}
	full := issue(t, mint.Spec{Mode: string(sandbox.ReadOnly), Require: string(sandbox.RequireFull)})
	if _, c, err := local.Decide(context.Background(), s, full, command(t), ready); herr.Kind(err) != sandbox.CodeEnforcementInsufficient || c.Valid() {
		t.Fatalf("require full on a partial start = %+v, %v; want %s", c, err, sandbox.CodeEnforcementInsufficient)
	}
	custom := &fake{name: sandbox.BackendBwrap, custom: true, probe: local.Probe{Canary: launch.CanaryRefused, Governed: all}}
	ready.Governed = all
	if _, c, err := local.Decide(context.Background(), newService(t, custom), full, command(t), ready); herr.Kind(err) != sandbox.CodeEnforcementInsufficient {
		t.Fatalf("require full under a custom runner = %+v, %v; want %s", c, err, sandbox.CodeEnforcementInsufficient)
	}
}

// TestConfigMayOnlyRemoveBackends: Backends reduces the chain in chain order and
// refuses a name the chain lacks; Reprobe after a repair changes the selection.
//
//dsh:test DH-T-SANDBOX-7
func TestConfigMayOnlyRemoveBackends(t *testing.T) {
	bwrap, landlock := passing(sandbox.BackendBwrap), passing(sandbox.BackendLandlock)
	s, err := local.New(context.Background(), local.Config{Backends: []sandbox.Backend{sandbox.BackendLandlock}}, []local.Backend{bwrap, landlock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if st := s.Describe(context.Background()); len(st) != 1 || st[0].Backend != sandbox.BackendLandlock || bwrap.calls != 0 {
		t.Fatalf("Describe = %+v, bwrap probed %d times", st, bwrap.calls)
	}
	if _, err := local.New(context.Background(), local.Config{Backends: []sandbox.Backend{sandbox.BackendBwrap}}, []local.Backend{landlock}); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("New with a backend the chain lacks = %v, want %s", err, sandbox.CodeUnavailable)
	}

	repaired := &fake{name: sandbox.BackendBwrap, err: errors.New("bwrap not found")}
	r := newService(t, repaired)
	if _, err := r.Prepare(context.Background(), sandboxtest.Policy(sandbox.ReadOnly), command(t)); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("Prepare before the repair = %v", err)
	}
	repaired.err, repaired.probe = nil, local.Probe{Canary: launch.CanaryRefused}
	if st := r.Reprobe(context.Background()); !st[0].Available {
		t.Fatalf("Reprobe after the repair = %+v", st)
	}
	if l, err := r.Prepare(context.Background(), sandboxtest.Policy(sandbox.ReadOnly), command(t)); err != nil || !l.Valid() {
		t.Fatalf("Prepare after the repair = %v, %v", l, err)
	}
}

// A canary below a write root proves nothing: the confined start is refused.
func TestCanaryBelowAWriteRootIsRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "canaries")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := local.New(context.Background(), local.Config{CanaryDir: dir}, []local.Backend{passing(sandbox.BackendBwrap)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(context.Background(), sandboxtest.Policy(sandbox.WorkspaceWrite, root), command(t)); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("Prepare = %v, want %s", err, sandbox.CodeUnavailable)
	}
}

// start runs argv through the one starter under l, the test binary re-executed.
func start(t *testing.T, l sandbox.Launch, argv []string) (*subprocess.Process, error) {
	t.Helper()
	env := subprocess.Env{reexecEnv + "=1"}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	p, err := subprocess.Start(ctx, subprocess.Spec{Argv: argv, Env: env, Launch: l})
	if p != nil {
		t.Cleanup(func() { _ = p.StopTree(context.Background(), 0) })
	}
	return p, err
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// TestWindowsRefusesConfinedStart: on the Windows lane the platform chain is
// empty, so Prepare refuses workspace-write with sandbox/unavailable and nothing
// starts; danger-full-access runs through the real stage, reported Unconfined
// (TC-06b row on Windows, M2-C-8).
//
//dsh:test DH-T-SANDBOX-7
func TestWindowsRefusesConfinedStart(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows lane's row: Linux has backends (DH-D-SANDBOX-4)")
	}
	if n := len(local.Platform()); n != 0 {
		t.Fatalf("the Windows chain has %d backends; M2 has none", n)
	}
	s := newService(t, local.Platform()...)
	argv := command(t)
	l, err := s.Prepare(context.Background(), sandboxtest.Policy(sandbox.WorkspaceWrite, t.TempDir()), argv)
	if herr.Kind(err) != sandbox.CodeUnavailable || l.Valid() {
		t.Fatalf("Prepare(workspace-write) = %v, %v; want %s and no plan", l, err, sandbox.CodeUnavailable)
	}
	if p, err := start(t, l, argv); p != nil || err == nil {
		t.Fatalf("Start without a plan = %v, %v; want a refusal", p, err)
	}
	if exists(t, argv[2]) {
		t.Fatal("a command ran although its confined policy was refused")
	}
	unconfinedRuns(t, s)
}

// TestUnconfinedPlanRunsThroughTheStage: Prepare's danger-full-access plan runs
// the real stage, the decision accepts its not-tried ready, and the command runs
// reported Unconfined (TC-04b).
//
//dsh:test DH-T-SANDBOX-7
func TestUnconfinedPlanRunsThroughTheStage(t *testing.T) {
	unconfinedRuns(t, newService(t))
}

func unconfinedRuns(t *testing.T, s *local.Service) {
	t.Helper()
	argv := command(t)
	l, err := s.Prepare(context.Background(), sandboxtest.Policy(sandbox.DangerFullAccess), argv)
	if err != nil {
		t.Fatal(err)
	}
	p, err := start(t, l, argv)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Wait(); err != nil || p.ExitCode() != 0 {
		t.Fatalf("Wait = %v, exit %d", err, p.ExitCode())
	}
	if !exists(t, argv[2]) {
		t.Fatal("the unconfined command did not run")
	}
	if c := p.Confinement(); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone {
		t.Fatalf("Confinement = %+v, want Unconfined, backend none", c)
	}
}

// TestFailedConfinementRunsNothing: a confined plan under a backend whose stage
// does not really confine (the fake bwrap: no wrapper) reports its canary
// written; the decision rejects it, the command never runs, the canary is
// removed and the chain is re-probed once (TC-04b, INV-14).
//
//dsh:test DH-T-SANDBOX-7
func TestFailedConfinementRunsNothing(t *testing.T) {
	s := newService(t, passing(sandbox.BackendBwrap, sandbox.Effects...))
	argv := command(t)
	l, err := s.Prepare(context.Background(), sandboxtest.Policy(sandbox.ReadOnly), argv)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Probes()
	p, err := start(t, l, argv)
	if p != nil || herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("Start = %v, %v; want no handle and %s", p, err, sandbox.CodeLaunchFailed)
	}
	if exists(t, argv[2]) {
		t.Fatal("the command ran although its confinement failed")
	}
	if n := s.Probes() - before; n != 1 {
		t.Fatalf("re-probed %d times after the failed confinement, want 1", n)
	}
}
