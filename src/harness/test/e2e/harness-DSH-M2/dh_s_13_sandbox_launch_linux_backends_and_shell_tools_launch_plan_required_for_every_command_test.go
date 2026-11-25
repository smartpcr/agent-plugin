//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/claudecode"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// lprRoleEnv selects what this e2e test binary plays when re-executed: the
// launch stage, the marker command, the recording fake spawner or the fake
// signed-in client. It has no DSH_ prefix because the subprocess environment
// builder strips DSH_* names.
const lprRoleEnv = "LAUNCHPLANE2E_ROLE"

// lprLogEnv names the file every re-executed fake appends one line to per start:
// the recording fake spawner's log.
const lprLogEnv = "LAUNCHPLANE2E_LOG"

const (
	lprRoleStage  = "stage"
	lprRoleMarker = "marker"
	lprRoleSpawn  = "spawn"
	lprRoleClient = "client"

	lprClientVersion = "2.1.312 (Claude Code)"
)

func init() {
	role := os.Getenv(lprRoleEnv)
	if role == "" {
		return
	}
	args := os.Args[1:]
	switch role {
	case lprRoleStage:
		if code, ok := launch.ServeIfAsked(args, os.Stderr); ok {
			os.Exit(code)
		}
		// Once the stage has become its command, the command is this binary
		// again with the marker's arguments.
		if len(args) == 2 && args[0] == lprRoleMarker {
			if err := os.WriteFile(args[1], []byte("ran"), 0o644); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
		os.Exit(3)
	case lprRoleSpawn, lprRoleClient:
		lprAppend(strings.Join(args, " "))
		if role == lprRoleClient && len(args) == 1 && args[0] == "--version" {
			fmt.Println(lprClientVersion)
		} else if role == lprRoleClient {
			fmt.Println(`{"type":"result"}`)
		}
		os.Exit(0)
	}
	os.Exit(3)
}

// lprAppend records one start in the spawn log.
func lprAppend(line string) {
	path := os.Getenv(lprLogEnv)
	if path == "" {
		os.Exit(4)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(4)
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// lprStarts are the lines of the spawn log; a missing log is zero starts.
func lprStarts(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

func lprEnv(role, log string) subprocess.Env {
	env := subprocess.Env{lprRoleEnv + "=" + role}
	if log != "" {
		env = append(env, lprLogEnv+"="+log)
	}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	return env
}

func lprTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

type lprWorld struct {
	dir string
	exe string

	// Spec without a plan.
	spec   subprocess.Spec
	log    string
	proc   *subprocess.Process
	err    error
	runErr error

	// Rejected confirm.
	marker   string
	released atomic.Int32
	confirms atomic.Int32
	saw      launch.Ready
	reject   herr.Reason

	// Client routes.
	module string
	goOut  string
	goErr  error
}

func (w *lprWorld) setup() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	w.exe = exe
	w.dir, err = os.MkdirTemp("", "lpr-e2e-")
	if err != nil {
		return err
	}
	w.log = filepath.Join(w.dir, "spawns.log")
	return nil
}

func (w *lprWorld) cleanup() {
	if w.proc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = w.proc.StopTree(ctx, 0)
		cancel()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

// --- Scenario: Spec without a plan refused (TC-04a) ---

func (w *lprWorld) givenSpecWithoutPlan() error {
	if err := w.setup(); err != nil {
		return err
	}
	w.spec = subprocess.Spec{Argv: []string{w.exe, "plan-required-probe"}, Env: lprEnv(lprRoleSpawn, w.log)}
	if w.spec.Launch.Valid() {
		return errors.New("the zero Launch reports itself valid")
	}
	return nil
}

func (w *lprWorld) whenStartCalled() error {
	ctx, cancel := lprTimeout()
	defer cancel()
	w.proc, w.err = subprocess.Start(ctx, w.spec)
	_, w.runErr = subprocess.Run(ctx, w.spec, subprocess.RunOptions{})
	return nil
}

func (w *lprWorld) thenHerrReason(reason string) error {
	if w.proc != nil {
		return errors.New("Start returned a handle for a Spec without a plan")
	}
	if w.err == nil {
		return errors.New("Start accepted a Spec without a plan")
	}
	if _, ok := herr.ReasonOf(w.err); !ok {
		return fmt.Errorf("Start's error %v is not an herr error", w.err)
	}
	if got := herr.Kind(w.err); got != herr.Reason(reason) || got != subprocess.ReasonNoLaunch {
		return fmt.Errorf("Start's error reason = %q, want %q", got, reason)
	}
	if got := herr.Kind(w.runErr); got != herr.Reason(reason) {
		return fmt.Errorf("Run's error = %v, want reason %q", w.runErr, reason)
	}
	return nil
}

func (w *lprWorld) thenZeroStarts() error {
	starts, err := lprStarts(w.log)
	if err != nil {
		return err
	}
	if len(starts) != 0 {
		return fmt.Errorf("the fake spawner recorded %d starts of a Spec without a plan: %q", len(starts), starts)
	}
	return nil
}

// The control: the very same Spec with a plan does reach the fake spawner, so
// zero starts above is the refusal and not a broken recorder.
func (w *lprWorld) thenWithPlanOneStart() error {
	ctx, cancel := lprTimeout()
	defer cancel()
	spec := w.spec
	spec.Launch = subprocesstest.Launch
	res, err := subprocess.Run(ctx, spec, subprocess.RunOptions{})
	if err != nil {
		return fmt.Errorf("Run with a plan: %w", err)
	}
	if !res.Started || res.ExitCode != 0 {
		return fmt.Errorf("Run with a plan = started %v, exit %d", res.Started, res.ExitCode)
	}
	if c := res.Confinement; c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone {
		return fmt.Errorf("a plan that starts the command itself reported %+v, want unconfined, backend none", c)
	}
	starts, err := lprStarts(w.log)
	if err != nil {
		return err
	}
	if len(starts) != 1 || starts[0] != "plan-required-probe" {
		return fmt.Errorf("the fake spawner recorded %q, want exactly one start", starts)
	}
	return nil
}

// --- Scenario: Rejected confirm leaves no command (TC-04b) ---

func (w *lprWorld) givenRejectingStage(reason string) error {
	if err := w.setup(); err != nil {
		return err
	}
	w.reject = herr.Reason(reason)
	w.marker = filepath.Join(w.dir, "marker")
	w.saw = launch.Ready{Backend: "unset"}
	return nil
}

func (w *lprWorld) whenStartRunsStaged() error {
	hooks := launch.Hooks{
		Confirm: func(_ context.Context, p launch.Plan, r launch.Ready) (sandbox.Confinement, error) {
			w.confirms.Add(1)
			w.saw = r
			if err := r.Agrees(p); err != nil {
				return sandbox.Confinement{}, fmt.Errorf("ready disagrees with its plan: %w", err)
			}
			return sandbox.Confinement{}, herr.New(w.reject, "the e2e host refuses the stage")
		},
		Release: func(context.Context) error { w.released.Add(1); return nil },
		Exe:     w.exe,
	}
	plan, err := launch.NewLaunch(sandboxtest.Policy(sandbox.DangerFullAccess),
		launch.Plan{Kind: launch.Unconfined, Backend: sandbox.BackendNone}, hooks)
	if err != nil {
		return fmt.Errorf("NewLaunch: %w", err)
	}
	if plan.Stage() == nil || !plan.Valid() {
		return fmt.Errorf("NewLaunch = %v, want a valid staged plan", plan)
	}
	ctx, cancel := lprTimeout()
	defer cancel()
	w.proc, w.err = subprocess.Start(ctx, subprocess.Spec{
		Argv:   []string{w.exe, lprRoleMarker, w.marker},
		Env:    lprEnv(lprRoleStage, ""),
		Launch: plan,
	})
	return nil
}

func (w *lprWorld) thenMarkerAbsent() error {
	// Start stopped the stage's tree before it returned; give a stray command a
	// moment anyway so a late marker would be seen.
	time.Sleep(300 * time.Millisecond)
	_, err := os.Stat(w.marker)
	if err == nil {
		return errors.New("the marker exists: the command ran although Confirm rejected the stage")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (w *lprWorld) thenRejectOutcome(reason string) error {
	if w.proc != nil {
		return errors.New("Start returned a handle although Confirm rejected the stage")
	}
	if w.err == nil {
		return errors.New("Start succeeded although Confirm rejected the stage")
	}
	if got := herr.Kind(w.err); got != herr.Reason(reason) {
		return fmt.Errorf("Start's error = %v (reason %q), want %q", w.err, got, reason)
	}
	return nil
}

func (w *lprWorld) thenConfirmSawReady(backend string) error {
	if n := w.confirms.Load(); n != 1 {
		return fmt.Errorf("Confirm ran %d times, want 1", n)
	}
	if w.saw.Backend != sandbox.Backend(backend) {
		return fmt.Errorf("the decision saw ready %+v, want the real stage's ready for backend %s", w.saw, backend)
	}
	return nil
}

func (w *lprWorld) thenReleasedOnce() error {
	if n := w.released.Load(); n != 1 {
		return fmt.Errorf("Release ran %d times, want 1", n)
	}
	return nil
}

// --- Scenario: Client routes still start (regression on M1) ---

// lprClientPackages are the fake-CLI test packages of the signed-in client
// routes, and the tests that pin the helper plan on their spawns.
var (
	lprClientPackages = []string{
		"./internal/llm/provider/claudecode/",
		"./internal/llm/provider/copilot/",
		"./internal/llm/provider/clientroute/",
	}
	lprHelperTests = []string{
		"TestClaudeCodeStartsUnderAHelperPlan",
		"TestHelperLaunch",
		"TestProbe",
		"TestRun",
	}
)

func lprModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			regexp.MustCompile(`(?m)^module\s+github\.com/smartpcr/agent-plugin/src/harness\s*$`).Match(b) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no src/harness go.mod above the working directory")
		}
		dir = parent
	}
}

func (w *lprWorld) givenClaudecodeTests() error {
	if err := w.setup(); err != nil {
		return err
	}
	root, err := lprModuleRoot()
	if err != nil {
		return err
	}
	w.module = root
	for _, pkg := range lprClientPackages {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pkg), "*_test.go"))
		if err != nil || len(matches) == 0 {
			return fmt.Errorf("package %s has no tests (%v)", pkg, err)
		}
	}
	return nil
}

func (w *lprWorld) whenTheyRun() error {
	gobin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("the go toolchain is required to run the client tests: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	args := append([]string{"test", "-count=1", "-v"}, lprClientPackages...)
	cmd := exec.CommandContext(ctx, gobin, args...)
	cmd.Dir = w.module
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	w.goOut, w.goErr = string(out), err
	return nil
}

func (w *lprWorld) thenTheyPass() error {
	tail := w.goOut
	if len(tail) > 6000 {
		tail = tail[len(tail)-6000:]
	}
	if w.goErr != nil {
		return fmt.Errorf("go test of the client routes failed: %v\n%s", w.goErr, tail)
	}
	if strings.Contains(w.goOut, "--- FAIL") {
		return fmt.Errorf("a client route test failed:\n%s", tail)
	}
	for _, name := range lprHelperTests {
		if !regexp.MustCompile(`(?m)^\s*--- PASS: ` + regexp.QuoteMeta(name) + ` `).MatchString(w.goOut) {
			return fmt.Errorf("%s did not run and pass:\n%s", name, tail)
		}
	}
	sc := bufio.NewScanner(strings.NewReader(w.goOut))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		for _, name := range lprHelperTests {
			if strings.HasPrefix(l, "--- SKIP: "+name+" ") {
				return fmt.Errorf("%s was skipped", name)
			}
		}
	}
	return nil
}

// thenSpawnsCarryHelperPlan drives the route's own launch path in process: the
// claude-code route refuses to exist without a resolver, the helper plan comes
// from ForHelper, and every start of the fake client (probe and run) is under
// that plan, recorded by the fake client itself; without the plan nothing starts.
func (w *lprWorld) thenSpawnsCarryHelperPlan() error {
	if _, err := claudecode.Route(claudecode.Config{}); err == nil {
		return errors.New("a claude-code route without a sandbox resolver was accepted")
	}
	kind := string(claudecode.RouteID)
	res := &sandboxtest.Resolver{UnconfinedKinds: []string{kind}}
	if _, err := claudecode.Route(claudecode.Config{Sandbox: res}); err != nil {
		return fmt.Errorf("a claude-code route with a resolver: %w", err)
	}
	ctx, cancel := lprTimeout()
	defer cancel()

	if _, err := clientroute.HelperLaunch(ctx, &sandboxtest.Resolver{}, claudecode.Client, kind); err == nil {
		return errors.New("a resolver that does not let the client run unconfined gave a plan")
	}
	plan, err := clientroute.HelperLaunch(ctx, res, claudecode.Client, kind)
	if err != nil {
		return fmt.Errorf("HelperLaunch: %w", err)
	}
	if !plan.Valid() || plan.IsTrusted() || plan.Stage() != nil || !strings.HasPrefix(plan.Reason(), "helper "+kind) {
		return fmt.Errorf("HelperLaunch = %v (trusted %v), want the explicit unconfined helper plan of %s", plan, plan.IsTrusted(), kind)
	}
	if plan.Policy().IsZero() || plan.Policy().Mode() != sandbox.DangerFullAccess {
		return fmt.Errorf("the helper plan's policy is %v, want the danger-full-access policy ForHelper issued", plan.Policy().Mode())
	}
	reqs := res.Helpers()
	if len(reqs) != 1 || reqs[0].Kind != kind || !reqs[0].Unconfined {
		return fmt.Errorf("helper requests = %+v; want one unconfined %s request", reqs, kind)
	}

	prog := subprocess.Program{Path: w.exe}
	env := lprEnv(lprRoleClient, w.log)
	pattern := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+) \(Claude Code\)$`)
	probe := clientroute.ProbeSpec{Client: claudecode.Client, Program: prog, Args: []string{"--version"}, Env: env,
		Pattern: pattern, Timeout: 30 * time.Second}
	if _, err := clientroute.Probe(ctx, probe); err == nil {
		return errors.New("a probe without a launch plan was accepted")
	}
	run := clientroute.RunSpec{Client: claudecode.Client, Program: prog, Args: []string{"-p"}, Dir: w.dir, Env: env}
	if r, err := clientroute.Start(ctx, run); err == nil {
		_ = r.Stop(0)
		return errors.New("a client run without a launch plan was started")
	}
	if starts, err := lprStarts(w.log); err != nil || len(starts) != 0 {
		return fmt.Errorf("the fake client recorded starts %q without a plan (%v)", starts, err)
	}

	probe.Launch = plan
	v, err := clientroute.Probe(ctx, probe)
	if err != nil || v.String() != "2.1.312" {
		return fmt.Errorf("probe under the helper plan = %v, %v", v, err)
	}
	run.Launch = plan
	r, err := clientroute.Start(ctx, run)
	if err != nil {
		return fmt.Errorf("run under the helper plan: %w", err)
	}
	if c := r.Confinement(); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone {
		_ = r.Stop(0)
		return fmt.Errorf("the client run reported %+v, want unconfined, backend none", c)
	}
	if code := r.Wait(30 * time.Second); code != 0 {
		_ = r.Stop(0)
		return fmt.Errorf("the fake client exited %d", code)
	}
	starts, err := lprStarts(w.log)
	if err != nil {
		return err
	}
	if len(starts) != 2 || starts[0] != "--version" || starts[1] != "-p" {
		return fmt.Errorf("the fake client recorded %q, want the probe and the run, each under the helper plan", starts)
	}
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_launch_plan_required_for_every_command(ctx *godog.ScenarioContext) {
	w := &lprWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = lprWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, err
	})

	ctx.Step(`^a subprocess Spec whose argv is the recording fake spawner and which has no Launch$`, w.givenSpecWithoutPlan)
	ctx.Step(`^Start is called$`, w.whenStartCalled)
	ctx.Step(`^it returns an herr error with reason "([^"]*)"$`, w.thenHerrReason)
	ctx.Step(`^the fake spawner records zero starts$`, w.thenZeroStarts)
	ctx.Step(`^the same Spec with a launch plan records exactly one start$`, w.thenWithPlanOneStart)

	ctx.Step(`^a real re-exec launch stage whose command writes a marker and a Confirm hook that rejects with "([^"]*)"$`, w.givenRejectingStage)
	ctx.Step(`^Start runs the staged plan$`, w.whenStartRunsStaged)
	ctx.Step(`^the marker is absent$`, w.thenMarkerAbsent)
	ctx.Step(`^the outcome is an herr error with reason "([^"]*)" and no handle$`, w.thenRejectOutcome)
	ctx.Step(`^the Confirm hook saw the stage's ready for backend "([^"]*)"$`, w.thenConfirmSawReady)
	ctx.Step(`^Release ran once$`, w.thenReleasedOnce)

	ctx.Step(`^the existing claudecode fake-CLI tests$`, w.givenClaudecodeTests)
	ctx.Step(`^they run after the change$`, w.whenTheyRun)
	ctx.Step(`^they pass$`, w.thenTheyPass)
	ctx.Step(`^each recorded spawn carries a helper plan$`, w.thenSpawnsCarryHelperPlan)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_launch_plan_required_for_every_command(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_launch_plan_required_for_every_command,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_launch_plan_required_for_every_command.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
