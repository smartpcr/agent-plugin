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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	shelltools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

// osstStageEnv marks this e2e binary re-executed by the sandbox seam as the
// launch stage of one of this suite's commands. The tool builds the request
// itself, so the marker reaches the stage through the parent environment,
// which the env builder passes on (it has no DSH_ prefix). It gates the stage
// so sibling stages' scripted stages in this package are not pre-empted.
const osstStageEnv = "OSSTE2E_STAGE"

func init() {
	if os.Getenv(osstStageEnv) != "1" {
		return
	}
	if code, ok := launch.ServeIfAsked(os.Args[1:], os.Stderr); ok {
		os.Exit(code)
	}
}

// osstDirs is every session's working directory.
type osstDirs struct{ root string }

func (d osstDirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d osstDirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d osstDirs) Path() (string, error) { return d.root, nil }
func (d osstDirs) Root() (string, error) { return d.root, nil }

// osstCountingSeam counts the plans asked of it and grants none: a command
// that reaches it is a command the tool tried to start.
type osstCountingSeam struct{ prepared atomic.Int32 }

func (*osstCountingSeam) Describe(context.Context) []sandbox.Status { return nil }
func (*osstCountingSeam) Reprobe(context.Context) []sandbox.Status  { return nil }
func (s *osstCountingSeam) Prepare(context.Context, sandbox.Policy, []string) (sandbox.Launch, error) {
	s.prepared.Add(1)
	return sandbox.Launch{}, herr.New(sandbox.CodeUnavailable, "the counting seam grants no plan")
}

// osstFakeShell is a bash dialect whose program is this binary; a validation
// failure must stop the call before it is ever started.
type osstFakeShell struct{ path string }

func (*osstFakeShell) Dialect() shellexec.Dialect { return shellexec.Bash }
func (s *osstFakeShell) Program() shellexec.Program {
	return shellexec.Program{Path: s.path, Edition: "fake", Source: shellexec.SourceConfig}
}
func (*osstFakeShell) Limits() shellexec.Limits { return shellexec.Limits{} }
func (s *osstFakeShell) Argv(command string, _ func() (string, error)) ([]string, error) {
	return []string{s.path, command}, nil
}
func (*osstFakeShell) Defaults() []string { return nil }
func (*osstFakeShell) Unset() []string    { return nil }

func osstEnv() shellexec.Environment {
	return &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}
}

// osstWorld is the state of one scenario.
type osstWorld struct {
	cleanups []func()

	shells *shellexec.Registry
	seam   *osstCountingSeam
	mode   sandbox.Mode
	svc    *local.Service

	args   map[string]any
	result htools.Result
	logged string

	// remote is a Linux lane reached through WSL; its run's logged result is
	// taken as this row's.
	remote bool
}

func (w *osstWorld) defer_(f func()) { w.cleanups = append(w.cleanups, f) }

func (w *osstWorld) cleanup() {
	for i := len(w.cleanups) - 1; i >= 0; i-- {
		w.cleanups[i]()
	}
}

func (w *osstWorld) tempDir(prefix string) (string, error) {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	w.defer_(func() { _ = os.RemoveAll(d) })
	return d, nil
}

// pipeline mounts the shell tools over w.shells in the real tool pipeline,
// runs one call in a fresh session step of a real SQLite log, and keeps its
// result and the raw tool/result event logged for it.
func (w *osstWorld) pipeline(name string) error {
	ctx := context.Background()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	w.defer_(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		return fmt.Errorf("session scope: %w", err)
	}
	var tab scope.Table
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return fmt.Errorf("scope table: %w", err)
	}
	svc := core.NewWith(root, tab, core.Config{Checkpoint: checkpoint.Default{}})
	hd, err := w.tempDir("osst-home-")
	if err != nil {
		return err
	}
	l := home.New(hd)
	if err := home.Ensure(l); err != nil {
		return err
	}
	st, err := sqlite.OpenHome(ctx, l)
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}
	w.defer_(func() { _ = st.Close(context.Background()) })
	wdir, err := w.tempDir("osst-wd-")
	if err != nil {
		return err
	}
	wd := osstDirs{wdir}
	deps := shelltools.Deps{Shells: w.shells, Resolver: &sandboxtest.Resolver{Default: w.mode, Workdir: wd}, Workdir: wd}
	if _, err := shelltools.Mount(root, svc, deps, shelltools.OneShotConfig{}); err != nil {
		return fmt.Errorf("mount the shell tools: %w", err)
	}

	raw, err := json.Marshal(w.args)
	if err != nil {
		return err
	}
	id := session.ID("s-osst-e2e")
	c := htools.CallRequest{ID: "c1", Name: name, Arguments: string(raw)}
	h, err := session.NewService(root, st, session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		return fmt.Errorf("create the session: %w", err)
	}
	w.defer_(func() { _ = h.Close(context.Background()) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return err
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	if err != nil {
		return err
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			return err
		}
		if _, err := h.Append(ctx, d); err != nil {
			return fmt.Errorf("append the step: %w", err)
		}
	}
	v, err := svc.Resolve(ctx, htools.Target{Agent: agent.Ref{Session: id, Incarnation: 1}})
	if err != nil {
		return fmt.Errorf("resolve the tool view: %w", err)
	}
	defer v.Release()
	bctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := svc.ExecuteBatch(bctx, htools.Batch{View: v, Log: h, Calls: []htools.CallRequest{c}})
	if err != nil {
		return fmt.Errorf("ExecuteBatch: %w", err)
	}
	if len(res.Results) != 1 {
		return fmt.Errorf("the batch returned %d results, want 1", len(res.Results))
	}
	w.result = res.Results[0]
	evs, err := h.Read(ctx, 0, 0)
	if err != nil {
		return fmt.Errorf("read the log: %w", err)
	}
	for _, ev := range evs {
		if ev.Type() == "tool/result" {
			w.logged = ev.Raw()
			return nil
		}
	}
	return errors.New("no tool/result event was logged for the call")
}

// --- Scenario: Unknown argument rejected ---

func (w *osstWorld) bashOverCountingSeam() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	w.seam = &osstCountingSeam{}
	e, err := shellexec.New(&osstFakeShell{path: exe}, shellexec.Options{Sandbox: w.seam, Env: osstEnv()})
	if err != nil {
		return fmt.Errorf("bash executor: %w", err)
	}
	w.shells = shellexec.NewRegistry()
	if _, err := w.shells.Register(e); err != nil {
		return err
	}
	// The widest mode: nothing but the argument check can stop the call.
	w.mode = sandbox.DangerFullAccess
	return nil
}

func (w *osstWorld) aToolCallWithArguments(list string) error {
	w.args = map[string]any{}
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		switch n {
		case "":
			return fmt.Errorf("empty argument name in %q", list)
		case "command":
			w.args[n] = "echo hi"
		default:
			w.args[n] = "x"
		}
	}
	if _, ok := w.args["command"]; !ok {
		return fmt.Errorf("%q has no command", list)
	}
	return nil
}

func (w *osstWorld) theToolValidatesInput() error {
	return w.pipeline(shelltools.BashName)
}

func (w *osstWorld) validationErrorNaming(arg string) error {
	if !w.result.IsError || w.result.Code != htools.CodeInvalidArguments {
		return fmt.Errorf("result = %+v, want an error with code %s", w.result, htools.CodeInvalidArguments)
	}
	if !strings.Contains(w.result.Text, arg) {
		return fmt.Errorf("the validation error does not name %q: %q", arg, w.result.Text)
	}
	return nil
}

func (w *osstWorld) loggedResultNames(arg string) error {
	if !strings.Contains(w.logged, arg) {
		return fmt.Errorf("the logged tool/result does not name %q: %s", arg, w.logged)
	}
	if strings.Contains(w.logged, `exit_code`) {
		return fmt.Errorf("a refused call has an exit code in the log: %s", w.logged)
	}
	return nil
}

func (w *osstWorld) nothingIsStarted() error {
	if w.seam == nil {
		return errors.New("no counting seam: the tool was not mounted over one")
	}
	if n := w.seam.prepared.Load(); n != 0 {
		return fmt.Errorf("the seam was asked for %d launch plans: a command was started", n)
	}
	return nil
}

// --- Scenario: Tool result carries confinement ---

// theLinuxLaneWithBash is the Linux lane: this host when it is Linux, else, on
// a Windows host, a WSL Linux distribution this suite is cross-compiled for
// and run in. A missing bash or an unreachable lane fails the row.
func (w *osstWorld) theLinuxLaneWithBash() error {
	switch runtime.GOOS {
	case "linux":
		h := shellexec.LocalHost(os.Environ())
		p, err := bash.Find("", h)
		if err != nil {
			return fmt.Errorf("the Linux lane requires bash, preflight did not find it: %w", err)
		}
		if !filepath.IsAbs(p.Path) {
			return fmt.Errorf("preflight resolved a relative bash %q", p.Path)
		}
		return nil
	case "windows":
		out, err := osstWSL(30*time.Second, "uname", "-s")
		if err != nil || strings.TrimSpace(out) != "Linux" {
			return fmt.Errorf("the Linux lane is unreachable: WSL has no running Linux distribution (uname -s = %q): %v", out, err)
		}
		if out, err := osstWSL(30*time.Second, "sh", "-c", "command -v bash"); err != nil || !strings.HasPrefix(strings.TrimSpace(out), "/") {
			return fmt.Errorf("the Linux lane has no bash on PATH (%q): %v", out, err)
		}
		w.remote = true
		return nil
	}
	return fmt.Errorf("this host is %s: the Linux lane is reachable only natively or through WSL on Windows", runtime.GOOS)
}

func (w *osstWorld) aDangerFullAccessLayer() error {
	w.mode = sandbox.DangerFullAccess
	if p := sandboxtest.Policy(w.mode); p.Mode() != sandbox.DangerFullAccess {
		return fmt.Errorf("the test layer resolved %s, want danger-full-access", p.Mode())
	}
	if w.remote {
		return nil
	}
	s, err := local.New(context.Background(), local.Config{}, nil)
	if err != nil {
		return fmt.Errorf("the sandbox seam: %w", err)
	}
	w.svc = s
	w.defer_(func() { _ = s.Close() })
	return nil
}

func (w *osstWorld) bashToolRuns(command string) error {
	if w.remote {
		return w.runInWSL(command)
	}
	if w.svc == nil {
		return errors.New("no sandbox seam: the danger-full-access layer was not given")
	}
	h := shellexec.LocalHost(os.Environ())
	e, err := bash.New(bash.Config{Timeout: 60 * time.Second}, h, shellexec.Options{Sandbox: w.svc, Env: osstEnv()})
	if err != nil {
		return fmt.Errorf("bash executor: %w", err)
	}
	w.shells = shellexec.NewRegistry()
	if _, err := w.shells.Register(e); err != nil {
		return err
	}
	old, had := os.LookupEnv(osstStageEnv)
	w.defer_(func() {
		if had {
			_ = os.Setenv(osstStageEnv, old)
		} else {
			_ = os.Unsetenv(osstStageEnv)
		}
	})
	if err := os.Setenv(osstStageEnv, "1"); err != nil {
		return err
	}
	w.args = map[string]any{"command": command, "description": "e2e probe"}
	if err := w.pipeline(shelltools.BashName); err != nil {
		return err
	}
	if w.result.IsError {
		return fmt.Errorf("the bash tool failed: %s (%s)", w.result.Text, w.result.Code)
	}
	return osstWriteReport(command, w.logged)
}

// loggedResult is the shell tool's structured result as the tool/result event
// logged it: the JSON object with exit_code and confinement found in the
// event's text.
func (w *osstWorld) loggedResult() (map[string]any, error) {
	if w.logged == "" {
		return nil, errors.New("no tool/result was logged")
	}
	var doc any
	if err := json.Unmarshal([]byte(w.logged), &doc); err != nil {
		return nil, fmt.Errorf("the logged tool/result is not JSON: %w", err)
	}
	var found map[string]any
	var walk func(any)
	walk = func(v any) {
		if found != nil {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["exit_code"]; ok {
				if _, ok := x["confinement"]; ok {
					found = x
					return
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		case string:
			var inner any
			if strings.HasPrefix(strings.TrimSpace(x), "{") && json.Unmarshal([]byte(x), &inner) == nil {
				walk(inner)
			}
		}
	}
	walk(doc)
	if found == nil {
		return nil, fmt.Errorf("the logged tool/result holds no structured shell result: %s", w.logged)
	}
	return found, nil
}

func (w *osstWorld) loggedExitCode(code int) error {
	r, err := w.loggedResult()
	if err != nil {
		return err
	}
	got, ok := r["exit_code"].(float64)
	if !ok || int(got) != code {
		return fmt.Errorf("logged exit_code = %v, want %d (%s)", r["exit_code"], code, w.logged)
	}
	if r["outcome"] != string(shellexec.Exited) {
		return fmt.Errorf("logged outcome = %v, want %s", r["outcome"], shellexec.Exited)
	}
	return nil
}

func (w *osstWorld) loggedConfinement(field, want string) error {
	r, err := w.loggedResult()
	if err != nil {
		return err
	}
	c, ok := r["confinement"].(map[string]any)
	if !ok {
		return fmt.Errorf("logged confinement = %v, want an object", r["confinement"])
	}
	expect := want
	if field == "state" {
		expect = string(map[string]sandbox.State{"Unconfined": sandbox.StateUnconfined}[want])
		if expect == "" {
			return fmt.Errorf("unknown confinement state %q in the feature", want)
		}
	}
	if c[field] != expect {
		return fmt.Errorf("logged confinement.%s = %v, want %q (%v)", field, c[field], expect, c)
	}
	if field == "state" && c["backend"] != string(sandbox.BackendNone) {
		return fmt.Errorf("logged confinement.backend = %v, want %s for an unconfined run", c["backend"], sandbox.BackendNone)
	}
	return nil
}

// The Linux lane through WSL: the parent cross-compiles this suite for linux,
// runs only its @lane-linux rows inside WSL with osstChildEnv set, and reads
// the logged tool/result the child records at osstReportEnv. The child asserts
// its own Then steps too, so a failure there fails the parent's When.
const (
	osstChildEnv  = "OSST_LANE_CHILD"
	osstReportEnv = "OSST_LANE_REPORT"
)

type osstReport struct {
	GOOS    string `json:"goos"`
	Command string `json:"command"`
	Logged  string `json:"logged"`
}

func osstWriteReport(command, logged string) error {
	path := os.Getenv(osstReportEnv)
	if path == "" {
		return nil
	}
	b, err := json.Marshal(osstReport{GOOS: runtime.GOOS, Command: command, Logged: logged})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func osstWSL(d time.Duration, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wsl.exe", append([]string{"-e"}, argv...)...).Output()
	return string(out), err
}

func (w *osstWorld) runInWSL(command string) error {
	pkg, err := os.Getwd()
	if err != nil {
		return err
	}
	dir, err := w.tempDir("osst-wsl-")
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "osst-e2e.linux.test")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "test", "-c", "-tags", "e2e", "-o", bin, ".")
	build.Dir = pkg
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("cross-compile the suite for the Linux lane: %v\n%s", err, out)
	}
	linuxBin, err := osstWSL(30*time.Second, "wslpath", "-a", bin)
	if err != nil {
		return fmt.Errorf("translate %s into the Linux lane: %w", bin, err)
	}
	report := filepath.Join(dir, "report.json")
	child := exec.CommandContext(ctx, "wsl.exe", "--cd", pkg, "-e", strings.TrimSpace(linuxBin),
		"-test.run", "^TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_one_shot_shell_tools$",
		"-test.count=1", "-test.v", "-test.timeout=5m")
	wslenv := osstChildEnv + "/u:" + osstReportEnv + "/up"
	if prev := os.Getenv("WSLENV"); prev != "" {
		wslenv = prev + ":" + wslenv
	}
	child.Env = append(os.Environ(), osstChildEnv+"=linux", osstReportEnv+"="+report, "WSLENV="+wslenv)
	out, err := child.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the suite failed on the Linux lane: %v\n%s", err, out)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		return fmt.Errorf("the Linux lane recorded no outcome: %v\n%s", err, out)
	}
	var r osstReport
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("the Linux lane's report: %w", err)
	}
	switch {
	case r.GOOS != "linux":
		return fmt.Errorf("the lane run reports GOOS %q, want linux", r.GOOS)
	case r.Command != command:
		return fmt.Errorf("the lane ran %q, want %q", r.Command, command)
	}
	fmt.Printf("    [Linux lane via WSL: GOOS=%s]\n", r.GOOS)
	w.logged = r.Logged
	return nil
}

// osstTags selects the rows: a WSL child runs only its lane's rows; every
// other run runs them all, the Linux row natively or through WSL.
func osstTags() (string, error) {
	switch l := os.Getenv(osstChildEnv); l {
	case "":
		return "", nil
	case "linux":
		return "@lane-linux", nil
	default:
		return "", fmt.Errorf("%s: unknown lane %q", osstChildEnv, l)
	}
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_one_shot_shell_tools(ctx *godog.ScenarioContext) {
	w := &osstWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = osstWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, nil
	})
	ctx.Step(`^the bash tool mounted in the tool pipeline over a seam that counts plans$`, w.bashOverCountingSeam)
	ctx.Step(`^a tool call with arguments "([^"]*)"$`, w.aToolCallWithArguments)
	ctx.Step(`^the tool validates input$`, w.theToolValidatesInput)
	ctx.Step(`^it returns a validation error naming "([^"]*)"$`, w.validationErrorNaming)
	ctx.Step(`^the logged result names "([^"]*)"$`, w.loggedResultNames)
	ctx.Step(`^nothing is started$`, w.nothingIsStarted)
	ctx.Step(`^the Linux lane with bash found by preflight$`, w.theLinuxLaneWithBash)
	ctx.Step(`^a danger-full-access layer$`, w.aDangerFullAccessLayer)
	ctx.Step(`^the bash tool runs "([^"]*)" through the tool pipeline$`, w.bashToolRuns)
	ctx.Step(`^the logged result contains exit_code (-?\d+)$`, w.loggedExitCode)
	ctx.Step(`^the logged result contains confinement\.(state|mode) "([^"]*)"$`, w.loggedConfinement)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_one_shot_shell_tools(t *testing.T) {
	tags, err := osstTags()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tags %q", tags)
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_one_shot_shell_tools,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_one_shot_shell_tools.feature"},
			TestingT: t,
			Strict:   true,
			Tags:     tags,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the one-shot shell tool scenarios failed")
	}
}
