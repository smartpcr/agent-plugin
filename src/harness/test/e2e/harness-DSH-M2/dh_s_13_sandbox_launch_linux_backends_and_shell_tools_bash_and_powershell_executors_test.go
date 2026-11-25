//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
}

// bpeLane resolves a dialect's program by its preflight rules. On the
// dialect's own lane a missing program fails the row, never skips it; off that
// lane the row runs whenever the program is found.
func (w *bpeWorld) bpeLane(lane string, find func(shellexec.Host) (shellexec.Program, error)) error {
	w.host = shellexec.LocalHost(os.Environ())
	p, err := find(w.host)
	if err != nil {
		if runtime.GOOS == lane {
			return fmt.Errorf("the %s lane requires the shell, preflight did not find it: %w", lane, err)
		}
		return fmt.Errorf("%w: not the %s lane and the shell is not found here: %v", godog.ErrSkip, lane, err)
	}
	if !filepath.IsAbs(p.Path) {
		return fmt.Errorf("preflight resolved a relative program %q", p.Path)
	}
	w.program = p
	return nil
}

func (w *bpeWorld) theLinuxLaneWithBash() error {
	return w.bpeLane("linux", func(h shellexec.Host) (shellexec.Program, error) { return bash.Find("", h) })
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
	e, err := bash.New(bash.Config{Path: w.program.Path}, w.host, w.options())
	if err != nil {
		return fmt.Errorf("bash executor: %w", err)
	}
	if e.Dialect() != shellexec.Bash {
		return fmt.Errorf("executor dialect = %s, want bash", e.Dialect())
	}
	return w.run(e, command)
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
		return fmt.Errorf("confinement = %+v, want %s", w.outcome.Confinement, state)
	}
	return nil
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
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bash_and_powershell_executors.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the bash and PowerShell executor scenarios failed")
	}
}
