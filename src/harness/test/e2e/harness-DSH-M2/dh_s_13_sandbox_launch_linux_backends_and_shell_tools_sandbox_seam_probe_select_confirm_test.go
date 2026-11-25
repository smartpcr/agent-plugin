//go:build e2e

package e2e

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// spcRoleEnv marks this e2e test binary re-executed as the scripted launch stage
// of this stage's scenarios. The scripted stage speaks the real channel: it parses
// the plan the host's Wrap gave it, sends the ready the scenario names, then
// waits for go or the host's rejection. No DSH_ prefix: the subprocess
// environment builder strips DSH_* names.
const spcRoleEnv = "SEAMCONFIRME2E_ROLE"

// The ready the scripted stage sends, and the file it appends one line to per
// event ("stage", then "go" or "rejected").
const (
	spcBackendEnv  = "SEAMCONFIRME2E_BACKEND"
	spcGovernedEnv = "SEAMCONFIRME2E_GOVERNED"
	spcCanaryEnv   = "SEAMCONFIRME2E_CANARY"
	spcLogEnv      = "SEAMCONFIRME2E_LOG"
)

const (
	spcRoleStage  = "stage"
	spcMarkerRole = "spc-marker"
	spcHelperKind = "seam-confirm-e2e"
)

func init() {
	if os.Getenv(spcRoleEnv) != spcRoleStage {
		return
	}
	os.Exit(spcStage(os.Args[1:]))
}

// spcStage is the scripted launch stage. After go it does what the command would
// (writes the marker the plan's argv names), then closes status, which the host
// reads as the command having started.
func spcStage(args []string) int {
	if len(args) == 0 || args[0] != launch.Mode {
		return 3
	}
	spcLog("stage")
	p, err := launch.Parse(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "spc stage:", err)
		return launch.ExitFailed
	}
	control := os.NewFile(p.Control, "control")
	status := os.NewFile(p.Status, "status")
	if control == nil || status == nil {
		return launch.ExitFailed
	}
	r := launch.Ready{
		Backend:  sandbox.Backend(os.Getenv(spcBackendEnv)),
		Governed: spcEffects(os.Getenv(spcGovernedEnv)),
		Canary:   launch.Canary(os.Getenv(spcCanaryEnv)),
	}
	b, err := launch.EncodeReady(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spc stage:", err)
		return launch.ExitFailed
	}
	if _, err := status.Write(b); err != nil {
		return launch.ExitFailed
	}
	var header [4]byte
	if _, err := io.ReadFull(control, header[:]); err != nil {
		spcLog("rejected")
		return launch.ExitFailed
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(control, body); err != nil {
		spcLog("rejected")
		return launch.ExitFailed
	}
	var f struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(body, &f); err != nil || f.Msg != "go" {
		spcLog("rejected")
		return launch.ExitFailed
	}
	spcLog("go")
	_ = control.Close()
	if len(p.Argv) != 3 || p.Argv[1] != spcMarkerRole {
		return launch.ExitCannotRun
	}
	if err := os.WriteFile(p.Argv[2], []byte("ran"), 0o644); err != nil {
		return launch.ExitCannotRun
	}
	_ = status.Close()
	return 0
}

func spcLog(line string) {
	path := os.Getenv(spcLogEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

func spcEffects(csv string) []sandbox.Effect {
	out := []sandbox.Effect{}
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, sandbox.Effect(s))
		}
	}
	return out
}

// spcBackend is a fake backend whose start-up probe reports a fixed result.
type spcBackend struct {
	name  sandbox.Backend
	probe local.Probe
}

func (b *spcBackend) Name() sandbox.Backend { return b.name }
func (b *spcBackend) Custom() bool          { return false }
func (b *spcBackend) Probe(context.Context, string) (local.Probe, error) {
	return b.probe, nil
}

type spcWorld struct {
	dir    string
	exe    string
	marker string
	log    string

	svc    *local.Service
	policy sandbox.Policy
	ready  launch.Ready

	// probeState and probeMode are the start-up probe claim, checked against
	// policy once the policy step has built it.
	probeState string
	probeMode  sandbox.Mode
	before int

	plan sandbox.Launch
	proc *subprocess.Process
	err  error
}

func (w *spcWorld) setup() error {
	if w.dir != "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	w.exe = exe
	if w.dir, err = os.MkdirTemp("", "spc-e2e-"); err != nil {
		return err
	}
	w.marker = filepath.Join(w.dir, "marker")
	w.log = filepath.Join(w.dir, "stages.log")
	return nil
}

func (w *spcWorld) cleanup() {
	if w.proc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = w.proc.StopTree(ctx, 0)
		cancel()
	}
	if w.svc != nil {
		_ = w.svc.Close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

func (w *spcWorld) argv() []string { return []string{w.exe, spcMarkerRole, w.marker} }

func (w *spcWorld) givenService(backend string) error {
	if err := w.setup(); err != nil {
		return err
	}
	fake := &spcBackend{name: sandbox.Backend(backend), probe: local.Probe{Canary: launch.CanaryRefused, Governed: slices.Clone(sandbox.Effects)}}
	svc, err := local.New(context.Background(), local.Config{}, []local.Backend{fake})
	if err != nil {
		return fmt.Errorf("local.New: %w", err)
	}
	w.svc = svc
	st := svc.Describe(context.Background())
	if len(st) != 1 || !st[0].Available || st[0].Backend != sandbox.Backend(backend) || !slices.Equal(st[0].Governed, sandbox.Effects) {
		return fmt.Errorf("Describe = %+v; want %s available, governing every effect", st, backend)
	}
	return nil
}

// givenWindowsLane uses the real platform chain, so it only runs on Windows:
// substituting an empty chain elsewhere would record a non-Windows pass as
// Windows-lane (@platform:windows) evidence.
func (w *spcWorld) givenWindowsLane() error {
	if runtime.GOOS != "windows" {
		return godog.ErrSkip
	}
	if err := w.setup(); err != nil {
		return err
	}
	chain := local.Platform()
	if len(chain) != 0 {
		return fmt.Errorf("the Windows chain has %d backends; M2 has none (M2-C-8)", len(chain))
	}
	svc, err := local.New(context.Background(), local.Config{}, chain)
	if err != nil {
		return fmt.Errorf("local.New: %w", err)
	}
	w.svc = svc
	if st := svc.Describe(context.Background()); len(st) != 0 {
		return fmt.Errorf("Describe = %+v; want no backend on the Windows lane", st)
	}
	return nil
}

func (w *spcWorld) givenPolicy(name string) error {
	if err := w.setup(); err != nil {
		return err
	}
	switch name {
	case "workspace-write":
		root := filepath.Join(w.dir, "workspace")
		if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		w.policy = sandboxtest.Policy(sandbox.WorkspaceWrite, root)
	case "danger-full-access":
		w.policy = sandboxtest.Policy(sandbox.DangerFullAccess)
	case "danger-full-access require full":
		res := &sandboxtest.Resolver{UnconfinedKinds: []string{spcHelperKind}}
		p, err := res.ForHelper(context.Background(), sandbox.HelperRequest{Kind: spcHelperKind, Unconfined: true, Require: sandbox.RequireFull})
		if err != nil {
			return fmt.Errorf("ForHelper: %w", err)
		}
		w.policy = p
	default:
		return fmt.Errorf("unknown policy %q", name)
	}
	if err := w.policy.Validate(); err != nil {
		return err
	}
	if name == "danger-full-access require full" && (w.policy.Mode() != sandbox.DangerFullAccess || w.policy.Require() != sandbox.RequireFull) {
		return fmt.Errorf("policy is %s require %s; want danger-full-access require full", w.policy.Mode(), w.policy.Require())
	}
	return w.checkProbeClaim()
}

// givenProbeFull records the scenario's claim that the start-up probe alone
// gives state for its mode-policy. The feature states it before the policy, so
// the claim is checked by checkProbeClaim once givenPolicy has built the very
// policy Prepare and Start receive.
func (w *spcWorld) givenProbeFull(state, mode string) error {
	if mode != string(sandbox.WorkspaceWrite) {
		return fmt.Errorf("unsupported mode %q", mode)
	}
	w.probeState, w.probeMode = state, sandbox.Mode(mode)
	return nil
}

// checkProbeClaim shows the start-up probe's evidence alone gives the claimed
// state for w.policy, so a Partial at Confirm comes from the weaker ready for
// the same policy, not from the probe.
func (w *spcWorld) checkProbeClaim() error {
	if w.probeState == "" {
		return nil
	}
	p := w.policy
	if p.Mode() != w.probeMode {
		return fmt.Errorf("the probe claim is for a %s policy; the scenario's policy is %s", w.probeMode, p.Mode())
	}
	if p.Network() || !slices.Contains(sandbox.Promised(p), sandbox.EffectNetwork) {
		return errors.New("the scenario's workspace-write policy does not deny and promise the network")
	}
	st := w.svc.Describe(context.Background())
	if len(st) != 1 || !st[0].Available {
		return fmt.Errorf("Describe = %+v; want the one probed backend available", st)
	}
	c := sandbox.Enforce(p, sandbox.Evidence{Backend: st[0].Backend, Probe: st[0].Governed, Start: st[0].Governed})
	if string(c.State) != w.probeState || len(c.Missing) != 0 {
		return fmt.Errorf("the probe's evidence gives %+v for the scenario's policy; want %s", c, w.probeState)
	}
	return nil
}

func (w *spcWorld) givenStage(backend, governed, canary string) error {
	w.ready = launch.Ready{Backend: sandbox.Backend(backend), Governed: spcEffects(governed), Canary: launch.Canary(canary)}
	if _, err := launch.EncodeReady(w.ready); err != nil {
		return fmt.Errorf("the scenario's ready is not a valid frame: %w", err)
	}
	return nil
}

func (w *spcWorld) env() subprocess.Env {
	governed := make([]string, len(w.ready.Governed))
	for i, e := range w.ready.Governed {
		governed[i] = string(e)
	}
	env := subprocess.Env{
		spcRoleEnv + "=" + spcRoleStage,
		spcBackendEnv + "=" + string(w.ready.Backend),
		spcGovernedEnv + "=" + strings.Join(governed, ","),
		spcCanaryEnv + "=" + string(w.ready.Canary),
		spcLogEnv + "=" + w.log,
	}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	return env
}

func (w *spcWorld) whenConfirm() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	l, err := w.svc.Prepare(ctx, w.policy, w.argv())
	if err != nil {
		return fmt.Errorf("Prepare: %w", err)
	}
	if !l.Valid() || l.Stage() == nil {
		return fmt.Errorf("Prepare = %v; want a valid staged plan", l)
	}
	w.plan = l
	w.before = w.svc.Probes()
	w.proc, w.err = subprocess.Start(ctx, subprocess.Spec{Argv: w.argv(), Env: w.env(), Launch: l})
	if w.proc != nil {
		if err := w.proc.Wait(); err != nil {
			return fmt.Errorf("Wait: %w", err)
		}
	}
	return nil
}

func (w *spcWorld) markerExists() (bool, error) {
	_, err := os.Stat(w.marker)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (w *spcWorld) stages() ([]string, error) {
	b, err := os.ReadFile(w.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(b)), nil
}

func (w *spcWorld) thenDecision(decision, result string) error {
	switch decision {
	case "reject":
		if w.proc != nil {
			return errors.New("Start returned a handle although the decision should reject the start")
		}
		if got := herr.Kind(w.err); got != herr.Reason(result) {
			return fmt.Errorf("Start's error = %v (reason %q), want %q", w.err, got, result)
		}
		// Start stopped the stage's tree before it returned; give a stray command a
		// moment anyway so a late marker would be seen.
		time.Sleep(200 * time.Millisecond)
		if ran, err := w.markerExists(); err != nil || ran {
			return fmt.Errorf("the command ran (%v) although the decision rejected the start", err)
		}
		// Start stops the rejected stage's tree, so the stage may not live to log
		// its rejection; what matters is that go never reached it.
		if s, err := w.stages(); err != nil || !slices.Contains(s, "stage") || slices.Contains(s, "go") {
			return fmt.Errorf("the stage recorded %q (%v); want a started stage that never received go", s, err)
		}
	case "accept":
		if w.err != nil || w.proc == nil {
			return fmt.Errorf("Start = %v, %v; want the command started", w.proc, w.err)
		}
		if code := w.proc.ExitCode(); code != 0 {
			return fmt.Errorf("the command exited %d", code)
		}
		if ran, err := w.markerExists(); err != nil || !ran {
			return fmt.Errorf("the accepted command did not run (%v)", err)
		}
		c := w.proc.Confinement()
		if string(c.State) != result || !c.Valid() {
			return fmt.Errorf("Confinement = %+v; want %s", c, result)
		}
		if c.State == sandbox.StateUnconfined && (c.Backend != sandbox.BackendNone || !slices.Equal(c.Missing, sandbox.Effects)) {
			return fmt.Errorf("unconfined Confinement = %+v; want backend none and every effect missing", c)
		}
		if c.State != sandbox.StateUnconfined && c.Backend != w.ready.Backend {
			return fmt.Errorf("Confinement backend = %s; want %s", c.Backend, w.ready.Backend)
		}
	default:
		return fmt.Errorf("unknown decision %q", decision)
	}
	return nil
}

func (w *spcWorld) thenMissing(csv string) error {
	c := w.proc.Confinement()
	if want := spcEffects(csv); !slices.Equal(c.Missing, want) {
		return fmt.Errorf("Missing = %v; want %v", c.Missing, want)
	}
	return nil
}

func (w *spcWorld) thenOneStage() error {
	s, err := w.stages()
	if err != nil {
		return err
	}
	n := 0
	for _, l := range s {
		if l == "stage" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("%d launch stages started (%q); want 1", n, s)
	}
	return nil
}

func (w *spcWorld) thenReprobed(want int) error {
	if got := w.svc.Probes() - w.before; got != want {
		return fmt.Errorf("the chain was re-probed %d times; want %d", got, want)
	}
	return nil
}

func (w *spcWorld) whenPrepare() error {
	w.plan, w.err = w.svc.Prepare(context.Background(), w.policy, w.argv())
	return nil
}

func (w *spcWorld) thenPrepareRefused(reason string) error {
	if got := herr.Kind(w.err); got != herr.Reason(reason) {
		return fmt.Errorf("Prepare's error = %v (reason %q); want %q", w.err, got, reason)
	}
	if w.plan.Valid() || w.plan.Stage() != nil {
		return fmt.Errorf("Prepare returned plan %v with its refusal", w.plan)
	}
	return nil
}

func (w *spcWorld) thenNoProcess() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w.ready = launch.Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: launch.CanaryNotTried}
	p, err := subprocess.Start(ctx, subprocess.Spec{Argv: w.argv(), Env: w.env(), Launch: w.plan})
	if p != nil {
		w.proc = p
		return errors.New("Start returned a handle for the refused plan")
	}
	if err == nil {
		return errors.New("Start accepted the refused plan")
	}
	time.Sleep(200 * time.Millisecond)
	if s, err := w.stages(); err != nil || len(s) != 0 {
		return fmt.Errorf("a launch stage started (%q, %v) although Prepare refused the policy", s, err)
	}
	if ran, err := w.markerExists(); err != nil || ran {
		return fmt.Errorf("the command ran (%v) although Prepare refused the policy", err)
	}
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_seam_probe_select_confirm(ctx *godog.ScenarioContext) {
	w := &spcWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = spcWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, err
	})

	ctx.Step(`^a local sandbox service whose fake "([^"]*)" backend passed its start-up probe governing every effect$`, w.givenService)
	ctx.Step(`^the Windows lane's local sandbox service$`, w.givenWindowsLane)
	ctx.Step(`^a "([^"]*)" policy$`, w.givenPolicy)
	ctx.Step(`^the start-up probe reports enforcement "([^"]*)" for a "([^"]*)" policy that denies the network$`, w.givenProbeFull)
	ctx.Step(`^a launch stage that reports backend "([^"]*)", governed "([^"]*)" and canary "([^"]*)"$`, w.givenStage)
	ctx.Step(`^Confirm evaluates the stage's ready$`, w.whenConfirm)
	ctx.Step(`^the host decision is "([^"]*)" with "([^"]*)"$`, w.thenDecision)
	ctx.Step(`^the confinement is missing exactly "([^"]*)"$`, w.thenMissing)
	ctx.Step(`^exactly one launch stage was started$`, w.thenOneStage)
	ctx.Step(`^the chain was re-probed (\d+) times$`, w.thenReprobed)
	ctx.Step(`^Prepare runs for the marker command$`, w.whenPrepare)
	ctx.Step(`^Prepare returns "([^"]*)" and no plan$`, w.thenPrepareRefused)
	ctx.Step(`^starting the command without a plan is refused and starts no process$`, w.thenNoProcess)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_seam_probe_select_confirm(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_seam_probe_select_confirm,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_seam_probe_select_confirm.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
