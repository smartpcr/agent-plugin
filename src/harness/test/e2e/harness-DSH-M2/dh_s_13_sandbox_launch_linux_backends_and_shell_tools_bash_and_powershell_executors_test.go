//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/pwsh"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// bpeSecretValue is the fake secret value; it must never appear in the child.
const bpeSecretValue = "tok-bpe-e2e-FAKE-never-leaks"

// bpeStageEnv marks this e2e test binary re-executed by the sandbox seam as
// the launch stage of a bash or pwsh command. It has no DSH_ prefix because the
// environment builder strips DSH_* names, and it gates the stage so sibling
// stages' own scripted stages in this package are not pre-empted.
const bpeStageEnv = "BASHPWSHE2E_STAGE"

// init serves the sandbox-launch stage before the test framework starts when
// this binary is re-executed as the stage of one of this suite's commands.
func init() {
	if os.Getenv(bpeStageEnv) != "1" {
		return
	}
	if code, ok := launch.ServeIfAsked(os.Args[1:], os.Stderr); ok {
		os.Exit(code)
	}
}

// bpeWorld is the state of one scenario.
type bpeWorld struct {
	host    shellexec.Host
	program shellexec.Program
	policy  sandbox.Policy
	svc     *local.Service

	outcome shellexec.Outcome
	exec    *shellexec.Execution

	// remote is a Linux lane reached through WSL; report is what the suite,
	// run there, recorded of its own run.
	remote    bool
	remoteDir string
	report    *bpeReport

	tmp      string
	store    *secret.Store
	restores []func()
	env      subprocess.Env
}

func (w *bpeWorld) cleanup() {
	if w.exec != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = w.exec.Stop(ctx, nil)
		cancel()
	}
	if w.svc != nil {
		_ = w.svc.Close()
	}
	for i := len(w.restores) - 1; i >= 0; i-- {
		w.restores[i]()
	}
	if w.tmp != "" {
		_ = os.RemoveAll(w.tmp)
	}
	if w.remoteDir != "" {
		_ = os.RemoveAll(w.remoteDir)
	}
}

// bpeLane resolves a dialect's program by its preflight rules on this host,
// which must be the lane's system. A wrong system or a missing program fails
// the row; it never skips.
func (w *bpeWorld) bpeLane(lane string, find func(shellexec.Host) (shellexec.Program, error)) error {
	if runtime.GOOS != lane {
		return fmt.Errorf("this host is %s, not the %s lane", runtime.GOOS, lane)
	}
	w.host = shellexec.LocalHost(os.Environ())
	p, err := find(w.host)
	if err != nil {
		return fmt.Errorf("the %s lane requires the shell, preflight did not find it: %w", lane, err)
	}
	if !filepath.IsAbs(p.Path) {
		return fmt.Errorf("preflight resolved a relative program %q", p.Path)
	}
	w.program = p
	return nil
}

// theLinuxLaneWithBash is the Linux lane: this host when it is Linux, else, on
// a Windows host, a WSL Linux distribution that this suite is cross-compiled
// for and run in. Git for Windows' bash is never taken for the Linux lane.
func (w *bpeWorld) theLinuxLaneWithBash() error {
	if runtime.GOOS == "linux" {
		return w.bpeLane("linux", func(h shellexec.Host) (shellexec.Program, error) { return bash.Find("", h) })
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("this host is %s: the Linux lane is reachable only natively or through WSL on Windows", runtime.GOOS)
	}
	out, err := bpeWSL(30*time.Second, "uname", "-s")
	if err != nil || strings.TrimSpace(out) != "Linux" {
		return fmt.Errorf("the Linux lane is unreachable: WSL has no running Linux distribution (uname -s = %q): %v", out, err)
	}
	if out, err := bpeWSL(30*time.Second, "sh", "-c", "command -v bash"); err != nil || !strings.HasPrefix(strings.TrimSpace(out), "/") {
		return fmt.Errorf("the Linux lane has no bash on PATH (%q): %v", out, err)
	}
	w.remote = true
	return nil
}

func (w *bpeWorld) theWindowsLaneWithPwsh() error {
	return w.bpeLane("windows", func(h shellexec.Host) (shellexec.Program, error) {
		p, err := pwsh.Find("", false, h)
		if err == nil && p.Edition != pwsh.Core {
			return p, fmt.Errorf("preflight found %s PowerShell, want Core (PowerShell 7)", p.Edition)
		}
		return p, err
	})
}

func (w *bpeWorld) aDangerFullAccessLayer() error {
	w.policy = sandboxtest.Policy(sandbox.DangerFullAccess)
	if w.policy.Mode() != sandbox.DangerFullAccess {
		return fmt.Errorf("the test layer resolved %s, want danger-full-access", w.policy.Mode())
	}
	s, err := local.New(context.Background(), local.Config{}, nil)
	if err != nil {
		return fmt.Errorf("the sandbox seam: %w", err)
	}
	w.svc = s
	return nil
}

func (w *bpeWorld) options() shellexec.Options {
	return shellexec.Options{Sandbox: w.svc, Env: &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}}
}

func (w *bpeWorld) run(e *shellexec.Executor, command string) error {
	if w.svc == nil {
		return errors.New("no sandbox seam: the test layer was not given")
	}
	x, err := e.Start(context.Background(), shellexec.Request{Command: command, Policy: w.policy, Timeout: 60 * time.Second, Env: []string{bpeStageEnv + "=1"}})
	if err != nil {
		return fmt.Errorf("Start refused the request: %w", err)
	}
	w.exec = x
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	o, err := x.Wait(ctx)
	if err != nil {
		return fmt.Errorf("Wait: %w", err)
	}
	w.outcome = o
	return nil
}

func (w *bpeWorld) startRunsOnBash(command string) error {
	if w.remote {
		return w.runInWSL(command)
	}
	e, err := bash.New(bash.Config{Path: w.program.Path}, w.host, w.options())
	if err != nil {
		return fmt.Errorf("bash executor: %w", err)
	}
	if e.Dialect() != shellexec.Bash {
		return fmt.Errorf("executor dialect = %s, want bash", e.Dialect())
	}
	if err := w.run(e, command); err != nil {
		return err
	}
	return w.writeReport(command)
}

func (w *bpeWorld) startRunsOnPwsh(command string) error {
	e, err := pwsh.New(pwsh.Config{Path: w.program.Path}, w.host, w.options())
	if err != nil {
		return fmt.Errorf("pwsh executor: %w", err)
	}
	if e.Dialect() != shellexec.Pwsh {
		return fmt.Errorf("executor dialect = %s, want pwsh", e.Dialect())
	}
	return w.run(e, command)
}

func (w *bpeWorld) outcomeKindIs(kind string) error {
	if string(w.outcome.Kind) != kind {
		return fmt.Errorf("outcome kind = %q (err %v, cause %v), want %q", w.outcome.Kind, w.outcome.Err, w.outcome.Cause, kind)
	}
	return nil
}

func (w *bpeWorld) exitCodeIs(code int) error {
	if w.outcome.ExitCode != code {
		return fmt.Errorf("exit code = %d, want %d (stderr %q)", w.outcome.ExitCode, code, w.outcome.Stderr.Tail)
	}
	return nil
}

func (w *bpeWorld) stdoutTailIs(want string) error {
	if got := strings.TrimSpace(string(w.outcome.Stdout.Tail)); got != want {
		return fmt.Errorf("stdout tail = %q, want %q", got, want)
	}
	return nil
}

func (w *bpeWorld) confinementIs(state string) error {
	want := map[string]sandbox.State{"Unconfined": sandbox.StateUnconfined}[state]
	if want == "" {
		return fmt.Errorf("unknown confinement %q in the feature", state)
	}
	if w.outcome.Confinement.State != want || !w.outcome.Confinement.Valid() {
		if w.report != nil && w.report.Confinement == string(want) && w.report.ConfinementValid {
			return nil
		}
		return fmt.Errorf("confinement = %+v, want %s", w.outcome.Confinement, state)
	}
	return nil
}

// The Linux lane through WSL: the parent cross-compiles this suite for linux,
// runs only its @lane-linux rows inside WSL with bpeChildEnv set, and reads the
// outcome the child records at bpeReportEnv. The child asserts its own Then
// steps too, so a failure there fails the parent's When.
const (
	bpeChildEnv  = "BPE_LANE_CHILD"
	bpeReportEnv = "BPE_LANE_REPORT"
	bpeLanesEnv  = "BPE_LANES"
)

// bpeReport is one lane run's outcome as the child saw it.
type bpeReport struct {
	GOOS             string `json:"goos"`
	Command          string `json:"command"`
	Program          string `json:"program"`
	Kind             string `json:"kind"`
	ExitCode         int    `json:"exitCode"`
	StdoutTail       string `json:"stdoutTail"`
	Confinement      string `json:"confinement"`
	ConfinementValid bool   `json:"confinementValid"`
}

// writeReport records the run for a parent on another lane; it does nothing
// when no parent asked.
func (w *bpeWorld) writeReport(command string) error {
	path := os.Getenv(bpeReportEnv)
	if path == "" {
		return nil
	}
	b, err := json.Marshal(bpeReport{
		GOOS: runtime.GOOS, Command: command, Program: w.program.Path,
		Kind: string(w.outcome.Kind), ExitCode: w.outcome.ExitCode, StdoutTail: string(w.outcome.Stdout.Tail),
		Confinement: string(w.outcome.Confinement.State), ConfinementValid: w.outcome.Confinement.Valid(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// bpeWSL runs argv in the default WSL distribution and returns its stdout.
func bpeWSL(d time.Duration, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wsl.exe", append([]string{"-e"}, argv...)...).Output()
	return string(out), err
}

// runInWSL runs this suite's @lane-linux rows on the Linux lane and takes the
// child's recorded outcome as this row's.
func (w *bpeWorld) runInWSL(command string) error {
	pkg, err := os.Getwd()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "bpe-wsl-")
	if err != nil {
		return err
	}
	w.remoteDir = dir
	bin := filepath.Join(dir, "bpe-e2e.linux.test")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "test", "-c", "-tags", "e2e", "-o", bin, ".")
	build.Dir = pkg
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("cross-compile the suite for the Linux lane: %v\n%s", err, out)
	}
	linuxBin, err := bpeWSL(30*time.Second, "wslpath", "-a", bin)
	if err != nil {
		return fmt.Errorf("translate %s into the Linux lane: %w", bin, err)
	}
	report := filepath.Join(dir, "report.json")
	child := exec.CommandContext(ctx, "wsl.exe", "--cd", pkg, "-e", strings.TrimSpace(linuxBin),
		"-test.run", "^TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors$",
		"-test.count=1", "-test.v", "-test.timeout=5m")
	wslenv := bpeChildEnv + "/u:" + bpeReportEnv + "/up"
	if prev := os.Getenv("WSLENV"); prev != "" {
		wslenv = prev + ":" + wslenv
	}
	child.Env = append(os.Environ(), bpeChildEnv+"=linux", bpeReportEnv+"="+report, "WSLENV="+wslenv)
	out, err := child.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the suite failed on the Linux lane: %v\n%s", err, out)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		return fmt.Errorf("the Linux lane recorded no outcome: %v\n%s", err, out)
	}
	var r bpeReport
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("the Linux lane's report: %w", err)
	}
	switch {
	case r.GOOS != "linux":
		return fmt.Errorf("the lane run reports GOOS %q, want linux", r.GOOS)
	case r.Command != command:
		return fmt.Errorf("the lane ran %q, want %q", r.Command, command)
	case !strings.HasPrefix(r.Program, "/"):
		return fmt.Errorf("the lane's bash %q is not a Linux path", r.Program)
	}
	w.report = &r
	fmt.Printf("    [Linux lane via WSL: GOOS=%s bash=%s kind=%s exit=%d]\n", r.GOOS, r.Program, r.Kind, r.ExitCode)
	w.outcome = shellexec.Outcome{Kind: shellexec.Kind(r.Kind), ExitCode: r.ExitCode,
		Stdout: subprocess.Collected{Tail: []byte(r.StdoutTail)}}
	return nil
}

// bpeLanes are the lanes this run's shell rows cover: the child's own lane;
// else BPE_LANES ("linux,windows"); else every lane this host reaches —
// Windows reaches both (Linux through WSL), Linux only its own, where the
// Windows row runs in the CI matrix's Windows job.
func bpeLanes() ([]string, error) {
	if l := os.Getenv(bpeChildEnv); l != "" {
		return []string{l}, nil
	}
	if v := os.Getenv(bpeLanesEnv); v != "" {
		var out []string
		for _, l := range strings.Split(v, ",") {
			l = strings.TrimSpace(l)
			if l != "linux" && l != "windows" {
				return nil, fmt.Errorf("%s: unknown lane %q", bpeLanesEnv, l)
			}
			out = append(out, l)
		}
		return out, nil
	}
	if runtime.GOOS == "linux" {
		return []string{"linux"}, nil
	}
	return []string{"linux", "windows"}, nil
}

// bpeTags selects the rows of lanes; a child runs only its lane's rows.
func bpeTags(lanes []string) string {
	if os.Getenv(bpeChildEnv) != "" {
		return "@lane-" + lanes[0]
	}
	var not []string
	for _, l := range []string{"linux", "windows"} {
		if !slices.Contains(lanes, l) {
			not = append(not, "~@lane-"+l)
		}
	}
	return strings.Join(not, " && ")
}

// bpeRef is the secret reference whose environment name is name:
// DSH_TEST_TOKEN is secret://dsh/test-token.
func bpeRef(name string) (secret.Ref, error) {
	ns, rest, ok := strings.Cut(strings.ToLower(name), "_")
	if !ok {
		return secret.Ref{}, fmt.Errorf("%q has no namespace", name)
	}
	r, err := secret.Parse("secret://" + ns + "/" + strings.ReplaceAll(rest, "_", "-"))
	if err != nil {
		return secret.Ref{}, err
	}
	if r.EnvName() != name {
		return secret.Ref{}, fmt.Errorf("the reference %v names %s, want %s", r, r.EnvName(), name)
	}
	return r, nil
}

func (w *bpeWorld) aSecretStored(name string) error {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "bpe-e2e-")
	if err != nil {
		return err
	}
	w.tmp = tmp
	l := home.New(filepath.Join(tmp, "home"))
	if err := home.Ensure(l); err != nil {
		return err
	}
	ref, err := bpeRef(name)
	if err != nil {
		return err
	}
	if _, err := secret.NewStore(file.New(l.Secrets(), file.Options{})).Set(ctx, ref, secret.NewValue(bpeSecretValue), secret.RevisionAbsent); err != nil {
		return fmt.Errorf("store the secret: %w", err)
	}
	// A fresh store learns what is stored from its managed store, as a new
	// process would.
	w.store = secret.NewStore(secret.EnvSource(func(string) (string, bool) { return "", false }), file.New(l.Secrets(), file.Options{}))
	return nil
}

func (w *bpeWorld) presentInParent(name string) error {
	old, had := os.LookupEnv(name)
	w.restores = append(w.restores, func() {
		if had {
			_ = os.Setenv(name, old)
		} else {
			_ = os.Unsetenv(name)
		}
	})
	value := bpeSecretValue
	if !strings.HasPrefix(name, "DSH_") {
		value = "kept"
	}
	if err := os.Setenv(name, value); err != nil {
		return err
	}
	if got := os.Getenv(name); got != value {
		return fmt.Errorf("the parent environment holds %s=%q", name, got)
	}
	return nil
}

func (w *bpeWorld) envBuilderRuns(entry string) error {
	if w.store == nil {
		return errors.New("no secret store: the secret was not given")
	}
	b := &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{}), Secrets: w.store}
	env, err := b.Environ(context.Background(), []string{entry + "=" + bpeSecretValue})
	if err != nil {
		return fmt.Errorf("the env builder: %w", err)
	}
	w.env = env
	return nil
}

func (w *bpeWorld) bpeHas(name string) bool {
	for _, kv := range w.env {
		n, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func (w *bpeWorld) childLacks(name string) error {
	if w.bpeHas(name) {
		return fmt.Errorf("the child environment holds %s", name)
	}
	return nil
}

func (w *bpeWorld) noSecretValue() error {
	if strings.Contains(strings.Join(w.env, "\n"), bpeSecretValue) {
		return errors.New("the secret value reached the child environment")
	}
	return nil
}

func (w *bpeWorld) childKeeps(name string) error {
	if !w.bpeHas(name) {
		return fmt.Errorf("the child environment lacks %s: the builder dropped a name it should pass", name)
	}
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors(ctx *godog.ScenarioContext) {
	w := &bpeWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = bpeWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, nil
	})
	ctx.Step(`^the Linux lane with bash found by preflight$`, w.theLinuxLaneWithBash)
	ctx.Step(`^the Windows lane with pwsh found by preflight$`, w.theWindowsLaneWithPwsh)
	ctx.Step(`^a danger-full-access test layer$`, w.aDangerFullAccessLayer)
	ctx.Step(`^Start runs "([^"]*)" on the bash executor$`, w.startRunsOnBash)
	ctx.Step(`^Start runs "([^"]*)" on the pwsh executor$`, w.startRunsOnPwsh)
	ctx.Step(`^the outcome kind is "([^"]*)"$`, w.outcomeKindIs)
	ctx.Step(`^the outcome has exit code (-?\d+)$`, w.exitCodeIs)
	ctx.Step(`^the stdout tail is "([^"]*)"$`, w.stdoutTailIs)
	ctx.Step(`^the confinement is "([^"]*)"$`, w.confinementIs)
	ctx.Step(`^a secret named "([^"]*)" stored in human/secret$`, w.aSecretStored)
	ctx.Step(`^"([^"]*)" is present in the parent environment$`, w.presentInParent)
	ctx.Step(`^the env builder runs with the call entry "([^"]*)"$`, w.envBuilderRuns)
	ctx.Step(`^the child environment lacks "([^"]*)"$`, w.childLacks)
	ctx.Step(`^no secret value reaches the child environment$`, w.noSecretValue)
	ctx.Step(`^the child environment keeps "([^"]*)"$`, w.childKeeps)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors(t *testing.T) {
	lanes, err := bpeLanes()
	if err != nil {
		t.Fatal(err)
	}
	tags := bpeTags(lanes)
	t.Logf("shell lanes %v (tags %q)", lanes, tags)
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors.feature"},
			TestingT: t,
			Strict:   true,
			Tags:     tags,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the bash and PowerShell executor scenarios failed")
	}
}
