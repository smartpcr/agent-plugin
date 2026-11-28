//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_linux_confinement_and_sandbox_status.feature
// (DH-S-18 Stage 8.5, impl.md 18.4: DH-E-SANDBOX-1 and DH-E-NATIVESYSTEM rows,
// TC-21s1).
//
// Each scenario's machine runs a machine agent: this suite's own test binary,
// built for Linux and started on the machine with lcssAgentEnv naming the role.
// The agent reads the preflight facts, runs the dsh binary built from this
// checkout ("dsh sandbox status" on a scratch home) and then runs the
// scenario's commands through the real bash tool (tools/core ExecuteBatch,
// shellexec.tools, the bash executor) over the real Linux sandbox chain
// (local.Platform: bubblewrap, then Landlock) and the real policy resolver; the
// launch stage is the agent binary itself (launch.ServeIfAsked in init), the
// same stage code cmd/dsh serves. It prints one JSON report; the steps assert
// on it here.
//
// The machines (proof service:linux-sandbox-bwrap, service:linux-landlock-only,
// service:linux-unconfined): when DSH_SCENARIO_SANDBOX1_MACHINES names an SSH
// alias for the role, that machine (the provided instance) is used as it is.
// Otherwise the machine is ephemeral, on this host's Linux (native, or WSL from
// Windows): the Landlock-only machine is a PATH from which bwrap is left out,
// the unconfined one adds a seccomp filter failing the Landlock system calls
// with EOPNOTSUPP, as a kernel booted with Landlock disabled does. Either way
// the scenario's preflight must hold, or the scenario fails.

import (
	"bytes"
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
	"sync"
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
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	shelltools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

// The agent's inputs, set by the host on the machine.
const (
	lcssAgentEnv   = "LCSS_E2E_AGENT"   // the machine role: bwrap, landlock or unconfined
	lcssEmulateEnv = "LCSS_E2E_EMULATE" // "1": make this host's Linux the role's machine
	lcssSeccompEnv = "LCSS_E2E_SECCOMP" // "1": the Landlock filter is installed (set by the agent)
	lcssDshEnv     = "LCSS_E2E_DSH"     // the dsh binary on the machine
	lcssBaseEnv    = "LCSS_E2E_BASE"    // the directory the agent makes its scratch in
	lcssJobEnv     = "LCSS_E2E_JOB"     // confined or refuse
	lcssModeEnv    = "LCSS_E2E_MODE"    // the session's sandbox mode
	lcssRequireEnv = "LCSS_E2E_REQUIRE" // the policy's require: any or full
)

// lcssMachinesEnv lists provided machines as ';'-separated role=alias items
// (the input of the scenario package's DH-E-SANDBOX-1).
const lcssMachinesEnv = "DSH_SCENARIO_SANDBOX1_MACHINES"

// lcssReportMark prefixes the agent's one report line on its stdout.
const lcssReportMark = "LCSS-REPORT "

const (
	lcssAgentWithin = 4 * time.Minute
	lcssBuildWithin = 10 * time.Minute
)

// lcssServices maps the scenario's proof service to its machine role.
var lcssServices = map[string]string{
	"service:linux-sandbox-bwrap": "bwrap",
	"service:linux-landlock-only": "landlock",
	"service:linux-unconfined":    "unconfined",
}

func init() {
	// A launch stage: a probe or a command start of the agent's sandbox seam.
	// Probe stages keep no environment of the host's but the bus variables, so
	// this is not gated on one; every other stage of this package is gated by
	// its own variable in an earlier file and has already exited.
	if runtime.GOOS == "linux" {
		if code, ok := launch.ServeIfAsked(os.Args[1:], os.Stderr); ok {
			os.Exit(code)
		}
	}
	if role := os.Getenv(lcssAgentEnv); role != "" {
		os.Exit(lcssAgentMain(role))
	}
}

// --- the report ---

// lcssShell is the bash tool's structured result (shellexec/tools Result).
type lcssShell struct {
	Outcome  string `json:"outcome"`
	ExitCode int    `json:"exit_code"`
	Stdout   struct {
		Tail string `json:"tail"`
	} `json:"stdout"`
	Stderr struct {
		Tail string `json:"tail"`
	} `json:"stderr"`
	Confinement struct {
		Mode    string   `json:"mode"`
		State   string   `json:"state"`
		Backend string   `json:"backend"`
		Missing []string `json:"missing"`
		Exposed []string `json:"exposed"`
	} `json:"confinement"`
}

// record is the outcome as the evidence record copies it.
func (s lcssShell) record() string {
	return fmt.Sprintf("%s, %s, exit %d, missing [%s], exposed [%s]", s.Confinement.State, s.Confinement.Backend, s.ExitCode,
		strings.Join(s.Confinement.Missing, ","), strings.Join(s.Confinement.Exposed, ","))
}

// lcssCommand is one bash tool call and what came of it.
type lcssCommand struct {
	Name    string     `json:"name"`
	Command string     `json:"command"`
	IsError bool       `json:"isError"`
	Code    string     `json:"code,omitempty"`
	Text    string     `json:"text,omitempty"`
	Shell   *lcssShell `json:"shell,omitempty"`
}

// lcssReport is the agent's report.
type lcssReport struct {
	Role        string          `json:"role"`
	Kernel      string          `json:"kernel"`
	Emulated    bool            `json:"emulated"`
	Bwrap       string          `json:"bwrap,omitempty"` // bwrap on PATH, absolute; "" when none
	LandlockABI int             `json:"landlockABI"`
	ABIError    string          `json:"abiError,omitempty"`
	Status      string          `json:"status"`
	StatusError string          `json:"statusError,omitempty"`
	Commands    []lcssCommand   `json:"commands"`
	Exists      map[string]bool `json:"exists"`
	Error       string          `json:"error,omitempty"`
}

func (r *lcssReport) command(name string) (lcssCommand, error) {
	for _, c := range r.Commands {
		if c.Name == name {
			return c, nil
		}
	}
	return lcssCommand{}, fmt.Errorf("the machine ran no %q command: %+v", name, r.Commands)
}

// --- the agent, on the machine ---

func lcssAgentMain(role string) int {
	rep := &lcssReport{Role: role, Exists: map[string]bool{}}
	if err := lcssAgent(rep); err != nil {
		if errors.Is(err, errLcssReexec) {
			return 0
		}
		rep.Error = err.Error()
	}
	b, err := json.Marshal(rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println(lcssReportMark + string(b))
	return 0
}

var errLcssReexec = errors.New("re-executed")

func lcssAgent(rep *lcssReport) error {
	base := os.Getenv(lcssBaseEnv)
	if !filepath.IsAbs(base) {
		return fmt.Errorf("%s = %q is not an absolute directory", lcssBaseEnv, base)
	}
	run, err := os.MkdirTemp(base, "run-"+rep.Role+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(run)
	if os.Getenv(lcssEmulateEnv) == "1" {
		rep.Emulated = true
		if err := lcssEmulate(rep.Role, run); err != nil {
			return err
		}
	}
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		rep.Kernel = strings.TrimSpace(string(out))
	}
	if p, err := exec.LookPath("bwrap"); err == nil && filepath.IsAbs(p) {
		rep.Bwrap = p
	}
	if rep.LandlockABI, err = lcssLandlockABI(); err != nil {
		rep.ABIError = err.Error()
	}

	m := lcssMachine{run: run, ws: filepath.Join(run, "W"), extra: filepath.Join(run, "X"), outside: filepath.Join(run, "O"),
		beside: filepath.Join(run, "beside"), home: filepath.Join(run, "home")}
	for _, d := range []string{filepath.Join(m.ws, ".git"), m.extra, m.outside, m.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(m.beside, []byte("keep"), 0o644); err != nil {
		return err
	}

	st := exec.Command(os.Getenv(lcssDshEnv), "sandbox", "status")
	st.Env = append(os.Environ(), "DSH_HOME="+m.home)
	var stderr bytes.Buffer
	st.Stderr = &stderr
	out, err := st.Output()
	rep.Status = string(out)
	if err != nil {
		rep.StatusError = fmt.Sprintf("%v: %s", err, stderr.String())
	}

	job := lcssJobs[os.Getenv(lcssJobEnv)]
	if job == nil {
		return fmt.Errorf("%s = %q names no job", lcssJobEnv, os.Getenv(lcssJobEnv))
	}
	mode, err := sandbox.ParseMode(os.Getenv(lcssModeEnv))
	if err != nil {
		return err
	}
	cmds, files := job(m)
	if rep.Commands, err = m.runAll(mode, sandbox.Require(os.Getenv(lcssRequireEnv)), cmds); err != nil {
		return err
	}
	for name, p := range files {
		_, err := os.Lstat(p)
		rep.Exists[name] = err == nil
	}
	return nil
}

// lcssEmulate makes this Linux the role's machine: for landlock and unconfined a
// PATH without bwrap, for unconfined also the Landlock filter (the process is
// re-executed under it).
func lcssEmulate(role, run string) error {
	switch role {
	case "bwrap":
		return nil
	case "landlock", "unconfined":
	default:
		return fmt.Errorf("no emulation of the %q machine", role)
	}
	if role == "unconfined" && os.Getenv(lcssSeccompEnv) != "1" {
		if err := lcssDisableLandlock(append(os.Environ(), lcssSeccompEnv+"=1")); err != nil {
			return fmt.Errorf("disable Landlock for the unconfined machine: %w", err)
		}
		return errLcssReexec
	}
	bin := filepath.Join(run, "path")
	if err := os.Mkdir(bin, 0o755); err != nil {
		return err
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		ents, err := os.ReadDir(dir)
		if err != nil || !filepath.IsAbs(dir) {
			continue
		}
		for _, e := range ents {
			link := filepath.Join(bin, e.Name())
			if e.Name() == "bwrap" {
				continue
			}
			if _, err := os.Lstat(link); err == nil {
				continue
			}
			if err := os.Symlink(filepath.Join(dir, e.Name()), link); err != nil {
				return err
			}
		}
	}
	return os.Setenv("PATH", bin)
}

// lcssMachine is one agent run's scratch: W the workspace (a git root), X an
// extra write root, O a directory outside both, beside a file next to W, home
// the harness home.
type lcssMachine struct {
	run, ws, extra, outside, beside, home string
}

type lcssStep struct{ name, cmd string }

// lcssJobs are the scenarios' commands and the paths whose existence they
// report.
var lcssJobs = map[string]func(m lcssMachine) ([]lcssStep, map[string]string){
	"confined": func(m lcssMachine) ([]lcssStep, map[string]string) {
		q := func(p string) string { return "'" + p + "'" }
		return []lcssStep{
				{"inside", "printf ok > " + q(m.ws+"/ok") + " && printf ok > " + q(m.extra+"/ok")},
				{"outside", "printf x > " + q(m.outside+"/direct")},
				{"remove-beside", "rm -f " + q(m.beside)},
				{"outside-child", "sh -c \"printf x > " + q(m.outside+"/child") + "\""},
				{"own-exit", "echo 'bwrap: fatal' >&2; echo 'dsh sandbox-launch: failed' >&2; exit 3"},
			}, map[string]string{
				"W/ok": m.ws + "/ok", "X/ok": m.extra + "/ok",
				"O/direct": m.outside + "/direct", "O/child": m.outside + "/child", "beside": m.beside,
			}
	},
	"refuse": func(m lcssMachine) ([]lcssStep, map[string]string) {
		return []lcssStep{{"marker", "touch '" + m.ws + "/M'"}}, map[string]string{"W/M": m.ws + "/M"}
	},
}

// lcssDirs is every session's working directory: the workspace.
type lcssDirs struct{ root string }

func (d lcssDirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d lcssDirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d lcssDirs) Path() (string, error) { return d.root, nil }
func (d lcssDirs) Root() (string, error) { return d.root, nil }

// runAll runs each step as one bash tool call through the real tool pipeline,
// in a session of a real SQLite log, under the real policy resolver and the
// real Linux sandbox chain.
func (m lcssMachine) runAll(mode sandbox.Mode, require sandbox.Require, steps []lcssStep) ([]lcssCommand, error) {
	ctx := context.Background()
	var cleanups []func()
	defer func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	cleanups = append(cleanups, root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		return nil, fmt.Errorf("session scope: %w", err)
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
		return nil, fmt.Errorf("scope table: %w", err)
	}
	svc := core.NewWith(root, tab, core.Config{Checkpoint: checkpoint.Default{}})

	l := home.New(m.home)
	if err := home.Ensure(l); err != nil {
		return nil, err
	}
	st, err := sqlite.OpenHome(ctx, l)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	cleanups = append(cleanups, func() { _ = st.Close(context.Background()) })

	seam, err := local.New(ctx, local.Config{Home: m.home}, local.Platform())
	if err != nil {
		return nil, fmt.Errorf("the sandbox seam: %w", err)
	}
	cleanups = append(cleanups, func() { _ = seam.Close() })
	wd := lcssDirs{m.ws}
	res, err := policy.New(policy.Config{DefaultMode: mode, ExtraWriteRoots: []string{m.extra}, Network: policy.NetworkDenied, Require: require},
		policy.Deps{Owner: root, Workdir: wd})
	if err != nil {
		return nil, fmt.Errorf("the policy resolver: %w", err)
	}
	e, err := bash.New(bash.Config{Timeout: 60 * time.Second}, shellexec.LocalHost(os.Environ()),
		shellexec.Options{Sandbox: seam, Env: &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}})
	if err != nil {
		return nil, fmt.Errorf("bash executor: %w", err)
	}
	shells := shellexec.NewRegistry()
	if _, err := shells.Register(e); err != nil {
		return nil, err
	}
	if _, err := shelltools.Mount(root, svc, shelltools.Deps{Shells: shells, Resolver: res, Workdir: wd}, shelltools.OneShotConfig{}); err != nil {
		return nil, fmt.Errorf("mount the shell tools: %w", err)
	}

	sessions := session.NewService(root, st, session.Config{})
	var out []lcssCommand
	for i, s := range steps {
		c, err := lcssCall(ctx, svc, sessions, session.ID(fmt.Sprintf("s-lcss-%d", i)), s)
		if err != nil {
			return out, fmt.Errorf("%s: %w", s.name, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// lcssCall runs one bash call in a fresh session step of the store.
func lcssCall(ctx context.Context, svc htools.Service, sessions session.Service, id session.ID, s lcssStep) (lcssCommand, error) {
	raw, err := json.Marshal(map[string]any{"command": s.cmd, "description": "e2e " + s.name})
	if err != nil {
		return lcssCommand{}, err
	}
	call := htools.CallRequest{ID: "c1", Name: shelltools.BashName, Arguments: string(raw)}
	h, err := sessions.Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		return lcssCommand{}, fmt.Errorf("create the session: %w", err)
	}
	defer h.Close(context.Background())
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return lcssCommand{}, err
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock(call.ID, call.Name, json.RawMessage(call.Arguments)))
	if err != nil {
		return lcssCommand{}, err
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
			return lcssCommand{}, err
		}
		if _, err := h.Append(ctx, d); err != nil {
			return lcssCommand{}, fmt.Errorf("append the step: %w", err)
		}
	}
	v, err := svc.Resolve(ctx, htools.Target{Agent: agent.Ref{Session: id, Incarnation: 1}})
	if err != nil {
		return lcssCommand{}, fmt.Errorf("resolve the tool view: %w", err)
	}
	defer v.Release()
	bctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := svc.ExecuteBatch(bctx, htools.Batch{View: v, Log: h, Calls: []htools.CallRequest{call}})
	if err != nil {
		return lcssCommand{}, fmt.Errorf("ExecuteBatch: %w", err)
	}
	if len(res.Results) != 1 {
		return lcssCommand{}, fmt.Errorf("the batch returned %d results, want 1", len(res.Results))
	}
	r := res.Results[0]
	c := lcssCommand{Name: s.name, Command: s.cmd, IsError: r.IsError, Code: string(r.Code), Text: r.Text}
	var sh lcssShell
	if json.Unmarshal([]byte(r.Text), &sh) == nil && sh.Outcome != "" {
		c.Shell = &sh
	}
	return c, nil
}

// --- the machines, from the host ---

// lcssBuilt are the agent and dsh binaries for one Linux architecture.
type lcssBuilt struct{ agent, dsh string }

var (
	lcssOuterT   *testing.T
	lcssBuildMu  sync.Mutex
	lcssBuilds   = map[string]lcssBuilt{}
	lcssBuildDir string
)

// lcssBuild cross-compiles (or, on Linux of the same architecture, builds)
// this suite and cmd/dsh for linux/arch, once per architecture.
func lcssBuild(arch string) (lcssBuilt, error) {
	lcssBuildMu.Lock()
	defer lcssBuildMu.Unlock()
	if b, ok := lcssBuilds[arch]; ok {
		return b, nil
	}
	if lcssBuildDir == "" {
		d, err := os.MkdirTemp("", "dsh-e2e-lcss-build-")
		if err != nil {
			return lcssBuilt{}, err
		}
		lcssOuterT.Cleanup(func() { _ = os.RemoveAll(d) })
		lcssBuildDir = d
	}
	pkg, err := os.Getwd()
	if err != nil {
		return lcssBuilt{}, err
	}
	mod, err := lcssModuleDir(pkg)
	if err != nil {
		return lcssBuilt{}, err
	}
	b := lcssBuilt{agent: filepath.Join(lcssBuildDir, "lcss-agent-"+arch), dsh: filepath.Join(lcssBuildDir, "dsh-"+arch)}
	ctx, cancel := context.WithTimeout(context.Background(), lcssBuildWithin)
	defer cancel()
	env := append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	for _, c := range []struct {
		dir  string
		args []string
	}{
		{pkg, []string{"test", "-c", "-tags", "e2e", "-o", b.agent, "."}},
		{mod, []string{"build", "-o", b.dsh, "./cmd/dsh"}},
	} {
		cmd := exec.CommandContext(ctx, "go", c.args...)
		cmd.Dir, cmd.Env = c.dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return lcssBuilt{}, fmt.Errorf("go %s (linux/%s): %v\n%s", strings.Join(c.args, " "), arch, err, out)
		}
	}
	lcssBuilds[arch] = b
	return b, nil
}

// lcssModuleDir is the harness module: the nearest directory above dir with
// cmd/dsh.
func lcssModuleDir(dir string) (string, error) {
	for d := dir; ; {
		if fi, err := os.Stat(filepath.Join(d, "cmd", "dsh")); err == nil && fi.IsDir() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no cmd/dsh above %s", dir)
		}
		d = parent
	}
}

// lcssTarget is a Linux machine the agent runs on.
type lcssTarget interface {
	name() string
	emulated() bool
	// shell runs argv on the machine and returns its stdout.
	shell(ctx context.Context, stdin []byte, argv ...string) (string, error)
	// put copies the local file src to dst on the machine, executable.
	put(ctx context.Context, src, dst string) error
}

// lcssLocal is this host, a Linux one.
type lcssLocal struct{}

func (lcssLocal) name() string   { return "this Linux host" }
func (lcssLocal) emulated() bool { return true }
func (lcssLocal) shell(ctx context.Context, stdin []byte, argv ...string) (string, error) {
	return lcssRun(exec.CommandContext(ctx, argv[0], argv[1:]...), stdin)
}
func (lcssLocal) put(_ context.Context, src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

// lcssWSL is the WSL Linux of a Windows host.
type lcssWSL struct{}

func (lcssWSL) name() string   { return "WSL on this Windows host" }
func (lcssWSL) emulated() bool { return true }
func (lcssWSL) shell(ctx context.Context, stdin []byte, argv ...string) (string, error) {
	return lcssRun(exec.CommandContext(ctx, "wsl.exe", append([]string{"-e"}, argv...)...), stdin)
}
func (w lcssWSL) put(ctx context.Context, src, dst string) error {
	p, err := w.shell(ctx, nil, "wslpath", "-a", src)
	if err != nil {
		return err
	}
	_, err = w.shell(ctx, nil, "install", "-m", "0755", strings.TrimSpace(p), dst)
	return err
}

// lcssSSH is a provided machine, reached by its SSH alias.
type lcssSSH struct{ alias string }

func (s lcssSSH) name() string   { return "provided machine " + s.alias }
func (s lcssSSH) emulated() bool { return false }
func (s lcssSSH) shell(ctx context.Context, stdin []byte, argv ...string) (string, error) {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return lcssRun(exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", s.alias, strings.Join(quoted, " ")), stdin)
}
func (s lcssSSH) put(ctx context.Context, src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	_, err = s.shell(ctx, b, "sh", "-c", "cat > \"$0\" && chmod 0755 \"$0\"", dst)
	return err
}

func lcssRun(cmd *exec.Cmd, stdin []byte) (string, error) {
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(cmd.Args, " "), err, out, stderr.String())
	}
	return string(out), nil
}

// lcssProvided is the SSH alias lcssMachinesEnv gives role, or "".
func lcssProvided(role string) (string, error) {
	v := strings.TrimSpace(os.Getenv(lcssMachinesEnv))
	if v == "" {
		return "", nil
	}
	for _, item := range strings.Split(v, ";") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		r, alias, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(alias) == "" {
			return "", fmt.Errorf("%s item %q is not role=alias", lcssMachinesEnv, item)
		}
		if strings.TrimSpace(r) == role {
			return strings.TrimSpace(alias), nil
		}
	}
	return "", nil
}

// lcssTargetFor is the role's machine: the provided one, else this host's Linux.
func lcssTargetFor(role string) (lcssTarget, error) {
	alias, err := lcssProvided(role)
	switch {
	case err != nil:
		return nil, err
	case alias != "":
		return lcssSSH{alias}, nil
	}
	switch runtime.GOOS {
	case "linux":
		return lcssLocal{}, nil
	case "windows":
		out, err := lcssWSL{}.shell(context.Background(), nil, "uname", "-s")
		if err != nil || strings.TrimSpace(out) != "Linux" {
			return nil, fmt.Errorf("no %s machine: %s is unset for it and WSL has no running Linux distribution (uname -s = %q): %v",
				role, lcssMachinesEnv, strings.TrimSpace(out), err)
		}
		return lcssWSL{}, nil
	}
	return nil, fmt.Errorf("no %s machine: %s is unset for it and this %s host has no Linux to stand one up on", role, lcssMachinesEnv, runtime.GOOS)
}

// lcssGoArch maps "uname -m" to GOARCH.
func lcssGoArch(m string) (string, error) {
	switch strings.TrimSpace(m) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	}
	return "", fmt.Errorf("machine type %q has no release build", m)
}

// lcssRunAgent copies the binaries to a scratch directory on t and runs the
// agent there as role with job; the directory is removed afterwards.
func lcssRunAgent(t lcssTarget, role, job string, mode sandbox.Mode, require sandbox.Require) (*lcssReport, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lcssBuildWithin+lcssAgentWithin)
	defer cancel()
	m, err := t.shell(ctx, nil, "uname", "-m")
	if err != nil {
		return nil, "", err
	}
	arch, err := lcssGoArch(m)
	if err != nil {
		return nil, "", err
	}
	b, err := lcssBuild(arch)
	if err != nil {
		return nil, "", err
	}
	// Under $HOME, not /tmp: bubblewrap mounts a fresh /tmp, which would hide a
	// directory outside the write roots instead of denying writes to it.
	dir, err := t.shell(ctx, nil, "sh", "-c", `mktemp -d "${HOME:-/tmp}/dsh-e2e-lcss.XXXXXX"`)
	if err != nil {
		return nil, "", err
	}
	dir = strings.TrimSpace(dir)
	if !strings.HasPrefix(dir, "/") {
		return nil, "", fmt.Errorf("mktemp gave %q", dir)
	}
	defer func() { _, _ = t.shell(context.Background(), nil, "rm", "-rf", dir) }()
	agentBin, dshBin := dir+"/lcss-agent", dir+"/dsh"
	if err := t.put(ctx, b.agent, agentBin); err != nil {
		return nil, "", err
	}
	if err := t.put(ctx, b.dsh, dshBin); err != nil {
		return nil, "", err
	}
	argv := []string{"env", lcssAgentEnv + "=" + role, lcssDshEnv + "=" + dshBin, lcssBaseEnv + "=" + dir,
		lcssJobEnv + "=" + job, lcssModeEnv + "=" + string(mode), lcssRequireEnv + "=" + string(require)}
	if t.emulated() {
		argv = append(argv, lcssEmulateEnv+"=1")
	}
	actx, acancel := context.WithTimeout(ctx, lcssAgentWithin)
	defer acancel()
	out, err := t.shell(actx, nil, append(argv, agentBin)...)
	if err != nil {
		return nil, out, fmt.Errorf("the agent on %s: %w", t.name(), err)
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), lcssReportMark); ok {
			var r lcssReport
			if err := json.Unmarshal([]byte(rest), &r); err != nil {
				return nil, out, fmt.Errorf("the agent's report: %w", err)
			}
			if r.Error != "" {
				return &r, out, fmt.Errorf("the agent on %s failed: %s", t.name(), r.Error)
			}
			return &r, out, nil
		}
	}
	return nil, out, fmt.Errorf("the agent on %s printed no report:\n%s", t.name(), out)
}

// --- the status oracle ---

// lcssVerdict is the head of the "dsh sandbox status" text report: the state,
// the backend and the missing and exposed effects ("none" when empty). The
// backend lines after it name every backend of the chain, available or not, so
// only this head says which one the machine has.
type lcssVerdict struct{ State, Backend, Missing, Exposed string }

func lcssReadVerdict(st string) lcssVerdict {
	var v lcssVerdict
	for _, line := range strings.Split(st, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "state":
			v.State = val
		case "backend":
			v.Backend = val
		case "missing":
			v.Missing = val
		case "exposed":
			v.Exposed = val
		case "backends":
			return v
		}
	}
	return v
}

func (v lcssVerdict) String() string {
	return fmt.Sprintf("%s %s, missing [%s], exposed [%s]", v.State, v.Backend, v.Missing, v.Exposed)
}

// --- the scenario ---

type lcssWorld struct {
	service, role string
	target        lcssTarget
	report        *lcssReport
	out           string
	mode          sandbox.Mode
}

var lcssStateOf = map[string]string{"bwrap Full": "bwrap", "landlock": "landlock", "Unconfined": "unconfined"}

func (w *lcssWorld) givenMachine(service, preflight string) error {
	role, ok := lcssServices[service]
	if !ok {
		return fmt.Errorf("unknown machine service %q", service)
	}
	if lcssStateOf[preflight] != role {
		return fmt.Errorf("preflight %q is not the %s machine's", preflight, service)
	}
	t, err := lcssTargetFor(role)
	if err != nil {
		return err
	}
	w.service, w.role, w.target = service, role, t
	return nil
}

func (w *lcssWorld) run(job, mode, require string) error {
	m, err := sandbox.ParseMode(mode)
	if err != nil {
		return err
	}
	w.mode = m
	r, out, err := lcssRunAgent(w.target, w.role, job, m, sandbox.Require(require))
	w.report, w.out = r, out
	if err != nil {
		return err
	}
	fmt.Printf("    [%s (%s), kernel %s, bwrap %q, Landlock ABI %d]\n", w.service, w.target.name(), r.Kernel, r.Bwrap, r.LandlockABI)
	return w.preflight()
}

// preflight checks the machine is the role's (variants.md): bwrap on an
// absolute path; no bwrap and Landlock ABI >= 5; neither.
func (w *lcssWorld) preflight() error {
	r := w.report
	if r.ABIError != "" {
		return fmt.Errorf("preflight: %s", r.ABIError)
	}
	switch w.role {
	case "bwrap":
		if r.Bwrap == "" {
			return fmt.Errorf("preflight: the %s machine (%s) has no bwrap on an absolute path", w.service, w.target.name())
		}
	case "landlock":
		if r.Bwrap != "" || r.LandlockABI < 5 {
			return fmt.Errorf("preflight: the %s machine (%s) has bwrap %q and Landlock ABI %d; want no bwrap and ABI >= 5",
				w.service, w.target.name(), r.Bwrap, r.LandlockABI)
		}
	case "unconfined":
		if r.Bwrap != "" || r.LandlockABI != 0 {
			return fmt.Errorf("preflight: the %s machine (%s) has bwrap %q and Landlock ABI %d; want neither",
				w.service, w.target.name(), r.Bwrap, r.LandlockABI)
		}
	}
	return nil
}

func (w *lcssWorld) whenRowsRun(mode, require string) error {
	return w.run("confined", mode, require)
}

func (w *lcssWorld) whenMarker(mode, require string) error {
	return w.run("refuse", mode, require)
}

func (w *lcssWorld) verdict() (lcssVerdict, error) {
	if w.report == nil {
		return lcssVerdict{}, errors.New("no machine ran")
	}
	if w.report.StatusError != "" {
		return lcssVerdict{}, fmt.Errorf("dsh sandbox status failed on %s: %s\n%s", w.target.name(), w.report.StatusError, w.report.Status)
	}
	v := lcssReadVerdict(w.report.Status)
	fmt.Printf("    [%s: enforcement %s]\n", w.role, v)
	return v, nil
}

func (w *lcssWorld) thenStatusFull(state, backend string) error {
	v, err := w.verdict()
	if err != nil {
		return err
	}
	if v.State != state || v.Backend != backend || v.Missing != "none" {
		return fmt.Errorf("status reports %s; want %s %s missing none:\n%s", v, state, backend, w.report.Status)
	}
	return nil
}

func (w *lcssWorld) thenStatusPartial(state, backend string) error {
	v, err := w.verdict()
	if err != nil {
		return err
	}
	if v.State != state || v.Backend != backend {
		return fmt.Errorf("status reports %s; want %s under %s:\n%s", v, state, backend, w.report.Status)
	}
	if v.Missing == "" || v.Missing == "none" {
		return fmt.Errorf("status reports %s: %s without naming a missing effect", v, state)
	}
	for _, e := range strings.Split(v.Missing, ",") {
		if !slices.Contains(sandbox.Effects, sandbox.Effect(strings.TrimSpace(e))) {
			return fmt.Errorf("status names %q missing, which is not an effect of %v", e, sandbox.Effects)
		}
	}
	return nil
}

// thenStatusUnconfined is the unconfined oracle: the verdict is Unconfined and
// every backend of the Linux chain has a line saying unavailable.
func (w *lcssWorld) thenStatusUnconfined() error {
	v, err := w.verdict()
	if err != nil {
		return err
	}
	if v.State != "Unconfined" {
		return fmt.Errorf("status reports %s; want Unconfined:\n%s", v, w.report.Status)
	}
	for _, b := range []string{"bwrap", "landlock"} {
		found := false
		for _, line := range strings.Split(strings.ToLower(w.report.Status), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), b+":") && strings.Contains(line, "unavailable") {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("status has no %s line saying unavailable:\n%s", b, w.report.Status)
		}
	}
	return nil
}

func (w *lcssWorld) shellOf(name string) (lcssShell, error) {
	c, err := w.report.command(name)
	if err != nil {
		return lcssShell{}, err
	}
	if c.IsError || c.Shell == nil || c.Shell.Outcome != string(shellexec.Exited) {
		return lcssShell{}, fmt.Errorf("%q did not run to an exit: error %v (%s) %s", c.Command, c.IsError, c.Code, c.Text)
	}
	return *c.Shell, nil
}

func (w *lcssWorld) thenEveryResult(backend, state string) error {
	for _, c := range w.report.Commands {
		s, err := w.shellOf(c.Name)
		if err != nil {
			return err
		}
		if s.Confinement.Backend != backend || s.Confinement.State != state || s.Confinement.Mode != string(w.mode) {
			return fmt.Errorf("%q reported %s (mode %s); want backend %s in state %s, mode %s", c.Command, s.record(), s.Confinement.Mode, backend, state, w.mode)
		}
		if state == string(sandbox.StateFull) && len(s.Confinement.Missing) != 0 {
			return fmt.Errorf("%q reported full but missing %v", c.Command, s.Confinement.Missing)
		}
	}
	return nil
}

func (w *lcssWorld) thenEveryResultNaming(backend, state string) error {
	if err := w.thenEveryResult(backend, state); err != nil {
		return err
	}
	for _, c := range w.report.Commands {
		if len(c.Shell.Confinement.Missing) == 0 {
			return fmt.Errorf("%q reported %s without naming a missing effect", c.Command, c.Shell.record())
		}
	}
	return nil
}

func (w *lcssWorld) thenInsideSucceed() error {
	s, err := w.shellOf("inside")
	if err != nil {
		return err
	}
	if s.ExitCode != 0 {
		return fmt.Errorf("the writes inside W and X exited %d: %q", s.ExitCode, s.Stderr.Tail)
	}
	for _, p := range []string{"W/ok", "X/ok"} {
		if !w.report.Exists[p] {
			return fmt.Errorf("the write to %s inside the write roots did not land", p)
		}
	}
	return nil
}

func (w *lcssWorld) thenOutsideDenied() error {
	for _, name := range []string{"outside", "remove-beside", "outside-child"} {
		s, err := w.shellOf(name)
		if err != nil {
			return err
		}
		if s.ExitCode == 0 {
			return fmt.Errorf("%q succeeded outside the write roots (%s)", name, s.record())
		}
		if name == "outside" {
			fmt.Printf("    [%s: denied write %s; stderr %q]\n", w.role, s.record(), strings.TrimSpace(s.Stderr.Tail))
		}
	}
	for _, p := range []string{"O/direct", "O/child"} {
		if w.report.Exists[p] {
			return fmt.Errorf("a confined command made %s outside the write roots", p)
		}
	}
	if !w.report.Exists["beside"] {
		return errors.New("a confined command removed the file beside the workspace")
	}
	return nil
}

func (w *lcssWorld) thenOwnExit() error {
	s, err := w.shellOf("own-exit")
	if err != nil {
		return err
	}
	if s.ExitCode != 3 {
		return fmt.Errorf("the command exited 3 but the caller got %d", s.ExitCode)
	}
	if !strings.Contains(s.Stderr.Tail, "bwrap: fatal") || !strings.Contains(s.Stderr.Tail, "dsh sandbox-launch: failed") {
		return fmt.Errorf("the command's own stderr did not reach the caller unchanged: %q", s.Stderr.Tail)
	}
	return nil
}

func (w *lcssWorld) thenRefused(a, b string) error {
	c, err := w.report.command("marker")
	if err != nil {
		return err
	}
	if c.Shell != nil && c.Shell.Outcome == string(shellexec.Exited) {
		return fmt.Errorf("%q ran (exit %d, %s); want its start refused", c.Command, c.Shell.ExitCode, c.Shell.record())
	}
	if !c.IsError || !(strings.Contains(c.Code+" "+c.Text, a) || strings.Contains(c.Code+" "+c.Text, b)) {
		return fmt.Errorf("%q answered error=%v code %q: %s; want a refusal naming %s or %s", c.Command, c.IsError, c.Code, c.Text, a, b)
	}
	fmt.Printf("    [%s: refused %s: %s]\n", w.role, c.Code, c.Text)
	return nil
}

func (w *lcssWorld) thenNoMarker() error {
	if _, ok := w.report.Exists["W/M"]; !ok {
		return errors.New("the machine did not look for the marker")
	}
	if w.report.Exists["W/M"] {
		return errors.New("the refused command made the marker")
	}
	return nil
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_linux_confinement_and_sandbox_status(ctx *godog.ScenarioContext) {
	w := &lcssWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = lcssWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if err != nil && w.out != "" {
			fmt.Printf("    [machine agent output]\n%s\n", w.out)
		}
		return c, nil
	})
	ctx.Step(`^the "([^"]*)" machine whose preflight reads "([^"]*)"$`, w.givenMachine)
	ctx.Step(`^the SANDBOX-1 and NATIVESYSTEM rows run on it in a "([^"]*)" session with require "([^"]*)"$`, w.whenRowsRun)
	ctx.Step(`^a command creating a marker is started on it in a "([^"]*)" session with require "([^"]*)"$`, w.whenMarker)
	ctx.Step(`^"dsh sandbox status" reports "([^"]*)" under backend "([^"]*)" with no missing effect$`, w.thenStatusFull)
	ctx.Step(`^"dsh sandbox status" reports "([^"]*)" under backend "([^"]*)" naming the missing effects$`, w.thenStatusPartial)
	ctx.Step(`^"dsh sandbox status" reports "Unconfined" with every backend of the chain unavailable$`, w.thenStatusUnconfined)
	ctx.Step(`^every command's result reports backend "([^"]*)" in state "([^"]*)"$`, w.thenEveryResult)
	ctx.Step(`^every command's result reports backend "([^"]*)" in state "([^"]*)" naming the missing effects$`, w.thenEveryResultNaming)
	ctx.Step(`^the writes inside the write roots succeed$`, w.thenInsideSucceed)
	ctx.Step(`^the writes outside the write roots are denied and leave nothing behind$`, w.thenOutsideDenied)
	ctx.Step(`^the command's own exit code and stderr reach the caller$`, w.thenOwnExit)
	ctx.Step(`^the start is refused with "([^"]*)" or "([^"]*)"$`, w.thenRefused)
	ctx.Step(`^no command ran: the marker does not exist$`, w.thenNoMarker)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_linux_confinement_and_sandbox_status(t *testing.T) {
	lcssOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_linux_confinement_and_sandbox_status,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_linux_confinement_and_sandbox_status.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the Linux confinement and sandbox status scenarios failed")
	}
}
