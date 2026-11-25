package shellexec_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// childRole re-executes the test binary as the fake shell's program.
const childRole = "shellexec-child"

func TestMain(m *testing.M) {
	if code, ok := launch.ServeIfAsked(os.Args[1:], os.Stderr); ok {
		os.Exit(code)
	}
	if len(os.Args) == 3 && os.Args[1] == childRole {
		os.Exit(child(os.Args[2]))
	}
	os.Exit(m.Run())
}

// child is the fake shell: "exit3" prints hi and exits 3, "env" prints its
// environment, "hang" never ends by itself.
func child(command string) int {
	switch command {
	case "exit3":
		fmt.Println("hi")
		return 3
	case "env":
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
		return 0
	case "hang":
		<-time.After(time.Minute)
		return 0
	}
	return 2
}

// fakeShell runs the test binary as its program.
type fakeShell struct {
	path   string
	limits shellexec.Limits
	unset  []string
	argErr error
	// scratch, when set, receives the private directory Argv asks for.
	scratch func(dir string)
}

func (*fakeShell) Dialect() shellexec.Dialect { return shellexec.Bash }
func (s *fakeShell) Program() shellexec.Program {
	return shellexec.Program{Path: s.path, Edition: "fake", Source: shellexec.SourceConfig}
}
func (s *fakeShell) Limits() shellexec.Limits { return s.limits }
func (s *fakeShell) Argv(command string, scratch func() (string, error)) ([]string, error) {
	if s.argErr != nil {
		return nil, s.argErr
	}
	if s.scratch != nil {
		dir, err := scratch()
		if err != nil {
			return nil, err
		}
		s.scratch(dir)
	}
	return []string{s.path, childRole, command}, nil
}
func (*fakeShell) Defaults() []string { return []string{"FAKE_DEFAULT=1"} }
func (s *fakeShell) Unset() []string  { return s.unset }

// hostEnv passes the host environment and the entries, as the builder would
// without its scrub.
type hostEnv struct{ err error }

func (h hostEnv) Environ(_ context.Context, entries []string) (subprocess.Env, error) {
	if h.err != nil {
		return nil, h.err
	}
	env := subprocess.Env(os.Environ())
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	return append(env, entries...), nil
}

func newService(t *testing.T) *local.Service {
	t.Helper()
	s, err := local.New(context.Background(), local.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newExecutor(t *testing.T, sh *fakeShell, env shellexec.Environment) *shellexec.Executor {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sh.path = exe
	if sh.limits.Grace == 0 {
		sh.limits.Grace = 100 * time.Millisecond
	}
	if env == nil {
		env = hostEnv{}
	}
	e, err := shellexec.New(sh, shellexec.Options{Sandbox: newService(t), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func unconfined() sandbox.Policy { return sandboxtest.Policy(sandbox.DangerFullAccess) }

// start starts req and stops it when the test ends.
func start(t *testing.T, e *shellexec.Executor, req shellexec.Request) *shellexec.Execution {
	t.Helper()
	if req.Policy.IsZero() {
		req.Policy = unconfined()
	}
	x, err := e.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = x.Stop(ctx, nil)
	})
	return x
}

func wait(t *testing.T, x *shellexec.Execution) shellexec.Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, err := x.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return o
}

// TestResolveAppliesDefaultsAndCaps: the dialect, the default timeout, the cap,
// a clean directory and a trimmed description are filled in.
//
//dsh:test DH-T-EXEC-13
func TestResolveAppliesDefaultsAndCaps(t *testing.T) {
	e := newExecutor(t, &fakeShell{limits: shellexec.Limits{Timeout: time.Second, MaxTimeout: 5 * time.Second}}, nil)
	dir := t.TempDir()
	r, err := e.Resolve(shellexec.Request{Command: "x", Dir: dir + string(filepath.Separator) + ".", Description: "  say hi ", Policy: unconfined()})
	if err != nil {
		t.Fatal(err)
	}
	if r.Shell != shellexec.Bash || r.Timeout != time.Second || r.Dir != filepath.Clean(dir) || r.Description != "say hi" {
		t.Fatalf("Resolve = %+v", r)
	}
	r, err = e.Resolve(shellexec.Request{Command: "x", Timeout: time.Hour, Policy: unconfined()})
	if err != nil || r.Timeout != 5*time.Second {
		t.Fatalf("Resolve(1h) = %v, %v; want the 5s cap", r.Timeout, err)
	}
	if l := e.Limits(); l.TailBytes != shellexec.DefaultTailBytes || l.SpillBytes != shellexec.DefaultSpillBytes {
		t.Fatalf("Limits = %+v; want the default tail and spill", l)
	}
}

// TestResolveRefuses: a request that cannot run is refused before anything
// starts.
//
//dsh:test DH-T-EXEC-13
func TestResolveRefuses(t *testing.T) {
	e := newExecutor(t, &fakeShell{}, nil)
	for name, req := range map[string]shellexec.Request{
		"other dialect": {Shell: shellexec.Pwsh, Command: "x", Policy: unconfined()},
		"blank command": {Command: "  ", Policy: unconfined()},
		"nul":           {Command: "a\x00b", Policy: unconfined()},
		"relative dir":  {Command: "x", Dir: "rel", Policy: unconfined()},
		"negative":      {Command: "x", Timeout: -time.Second, Policy: unconfined()},
		"no policy":     {Command: "x"},
	} {
		if _, err := e.Resolve(req); herr.Kind(err) != shellexec.ReasonBadRequest {
			t.Errorf("%s: Resolve = %v, want %s", name, err, shellexec.ReasonBadRequest)
		}
		if x, err := e.Start(context.Background(), req); x != nil || herr.Kind(err) != shellexec.ReasonBadRequest {
			t.Errorf("%s: Start = %v, %v; want a refusal", name, x, err)
		}
	}
}

// TestNewRefusesBadOptions: an executor needs the sandbox seam, a builder and
// valid limits.
func TestNewRefusesBadOptions(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := newService(t)
	for name, c := range map[string]struct {
		sh shellexec.Shell
		o  shellexec.Options
	}{
		"no shell":     {nil, shellexec.Options{Sandbox: s, Env: hostEnv{}}},
		"no sandbox":   {&fakeShell{path: exe}, shellexec.Options{Env: hostEnv{}}},
		"no env":       {&fakeShell{path: exe}, shellexec.Options{Sandbox: s}},
		"relative":     {&fakeShell{path: "bash"}, shellexec.Options{Sandbox: s, Env: hostEnv{}}},
		"over the cap": {&fakeShell{path: exe, limits: shellexec.Limits{Timeout: time.Hour}}, shellexec.Options{Sandbox: s, Env: hostEnv{}}},
	} {
		if _, err := shellexec.New(c.sh, c.o); herr.Kind(err) != shellexec.ReasonBadConfig {
			t.Errorf("%s: New = %v, want %s", name, err, shellexec.ReasonBadConfig)
		}
	}
}

// TestStartRunsThroughThePlan: the command runs through the sandbox plan; the
// outcome has its exit code, its stdout tail and Unconfined, and a background
// reader of the same execution sees the same bytes.
//
//dsh:test DH-T-EXEC-13
func TestStartRunsThroughThePlan(t *testing.T) {
	e := newExecutor(t, &fakeShell{}, nil)
	x := start(t, e, shellexec.Request{Command: "exit3"})
	o := wait(t, x)
	if o.Kind != shellexec.Exited || o.ExitCode != 3 || o.Err != nil {
		t.Fatalf("Outcome = %+v, want exited 3", o)
	}
	if got := strings.TrimSpace(string(o.Stdout.Tail)); got != "hi" {
		t.Fatalf("stdout tail = %q, want hi", got)
	}
	if o.Confinement.State != sandbox.StateUnconfined {
		t.Fatalf("Confinement = %+v, want Unconfined", o.Confinement)
	}
	r := x.Reader(subprocess.StreamStdout)
	if r == nil {
		t.Fatal("no background reader")
	}
	if b, _ := r.Next(); strings.TrimSpace(string(b)) != "hi" {
		t.Fatalf("background read = %q, want hi", b)
	}
	if got, ok := x.Outcome(); !ok || got.ExitCode != 3 {
		t.Fatalf("Outcome() = %+v, %v", got, ok)
	}
}

// TestEnvironmentDefaultsAndUnset: the dialect's defaults reach the child and
// its unset names do not.
func TestEnvironmentDefaultsAndUnset(t *testing.T) {
	t.Setenv("SHELLEXEC_UNSET_ME", "1")
	e := newExecutor(t, &fakeShell{unset: []string{"SHELLEXEC_UNSET_ME"}}, nil)
	o := wait(t, start(t, e, shellexec.Request{Command: "env", Env: []string{"FROM_CALL=2"}}))
	lines := strings.Split(strings.ReplaceAll(string(o.Stdout.Tail), "\r\n", "\n"), "\n")
	for _, want := range []string{"FAKE_DEFAULT=1", "FROM_CALL=2"} {
		if !slices.Contains(lines, want) {
			t.Errorf("child env lacks %s", want)
		}
	}
	if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(strings.ToUpper(l), "SHELLEXEC_UNSET_ME=") }) {
		t.Error("an unset name reached the child")
	}
}

// TestTimeoutStopsTheCommand: a command that outlives its timeout is TimedOut.
//
//dsh:test DH-T-EXEC-13
func TestTimeoutStopsTheCommand(t *testing.T) {
	e := newExecutor(t, &fakeShell{}, nil)
	o := wait(t, start(t, e, shellexec.Request{Command: "hang", Timeout: 200 * time.Millisecond}))
	if o.Kind != shellexec.TimedOut || !errors.Is(o.Cause, shellexec.ErrTimedOut) {
		t.Fatalf("Outcome = %+v, want timed out", o)
	}
}

// TestForegroundWaitLeavesABackgroundRun: a Wait whose context ends first does
// not stop the command; Stop does, and the outcome is Cancelled.
//
//dsh:test DH-T-EXEC-13
func TestForegroundWaitLeavesABackgroundRun(t *testing.T) {
	e := newExecutor(t, &fakeShell{}, nil)
	x := start(t, e, shellexec.Request{Command: "hang"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := x.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait(ended) = %v, want context.Canceled", err)
	}
	if _, ok := x.Outcome(); ok {
		t.Fatal("ending a foreground wait ended the command")
	}
	if r := x.Reader(subprocess.StreamStderr); r == nil {
		t.Fatal("no background reader of the running command")
	}
	sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer scancel()
	o, err := x.Stop(sctx, nil)
	if err != nil || o.Kind != shellexec.Cancelled || !errors.Is(o.Cause, shellexec.ErrStopped) {
		t.Fatalf("Stop = %+v, %v; want cancelled by ErrStopped", o, err)
	}
}

// TestFailedOutcome: a command that cannot start is an execution already done
// with a Failed outcome: a confined policy the empty chain cannot honour, an
// environment that cannot be built, an argv that cannot be made.
//
//dsh:test DH-T-EXEC-13
func TestFailedOutcome(t *testing.T) {
	envErr := errors.New("no env")
	argErr := errors.New("no argv")
	for name, c := range map[string]struct {
		sh     *fakeShell
		env    shellexec.Environment
		policy sandbox.Policy
		want   func(error) bool
	}{
		"unavailable": {&fakeShell{}, nil, sandboxtest.Policy(sandbox.WorkspaceWrite, t.TempDir()), func(err error) bool { return herr.Kind(err) == sandbox.CodeUnavailable }},
		"env":         {&fakeShell{}, hostEnv{err: envErr}, unconfined(), func(err error) bool { return errors.Is(err, envErr) }},
		"argv":        {&fakeShell{argErr: argErr}, nil, unconfined(), func(err error) bool { return errors.Is(err, argErr) }},
	} {
		e := newExecutor(t, c.sh, c.env)
		x, err := e.Start(context.Background(), shellexec.Request{Command: "exit3", Policy: c.policy})
		if err != nil {
			t.Fatalf("%s: Start = %v; a start failure is an outcome", name, err)
		}
		o, ok := x.Outcome()
		if !ok || o.Kind != shellexec.Failed || o.ExitCode != -1 || !c.want(o.Err) {
			t.Fatalf("%s: Outcome = %+v, %v; want Failed", name, o, ok)
		}
		if x.Reader(subprocess.StreamStdout) != nil {
			t.Fatalf("%s: a failed execution has a reader", name)
		}
	}
}

// TestPrivateDirIsVisibleUnderThePolicy: under workspace-write the command's
// private directory (the PowerShell script file) lies below the policy's
// scratch root, else below its first write root, so a confiner that hides the
// host's temporary directory still shows it: in the bubblewrap argv for the
// same policy the last mount covering it is a bind of the host's tree, after
// the private /tmp. The policy is not widened.
//
//dsh:test DH-T-EXEC-13
func TestPrivateDirIsVisibleUnderThePolicy(t *testing.T) {
	ctx := context.Background()
	grant, scratch, root := t.TempDir(), t.TempDir(), t.TempDir()
	r := &sandboxtest.Resolver{Scratch: scratch}
	release, err := r.GrantRoot(ctx, "", grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	withScratch, err := r.Resolve(ctx, sandbox.Request{Mode: sandbox.WorkspaceWrite})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		policy sandbox.Policy
		parent string
	}{
		"scratch root":     {withScratch, scratch},
		"first write root": {sandboxtest.Policy(sandbox.WorkspaceWrite, root, grant), root},
	} {
		var dir string
		e := newExecutor(t, &fakeShell{scratch: func(d string) { dir = d }}, nil)
		x, err := e.Start(ctx, shellexec.Request{Command: "exit3", Policy: c.policy})
		if err != nil {
			t.Fatalf("%s: Start: %v", name, err)
		}
		t.Cleanup(func() {
			sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = x.Stop(sctx, nil)
		})
		if filepath.Dir(dir) != c.parent {
			t.Fatalf("%s: private dir = %q, want one below %q", name, dir, c.parent)
		}
		before := c.policy.WriteRoots()
		argv := bwrapArgv(t, c.policy)
		if !slices.Equal(c.policy.WriteRoots(), before) {
			t.Fatalf("%s: the policy was widened", name)
		}
		if op := coveringMount(argv, filepath.Join(dir, "dsh-shell.ps1")); op != "--bind" {
			t.Fatalf("%s: last mount over %q is %q, want --bind (argv %q)", name, dir, op, argv)
		}
	}
	if runtime.GOOS == "linux" && os.TempDir() == "/tmp" {
		argv := bwrapArgv(t, withScratch)
		if op := coveringMount(argv, "/tmp/dsh-shell-x/dsh-shell.ps1"); op != "--tmpfs" {
			t.Fatalf("host /tmp under bubblewrap: last mount %q, want --tmpfs", op)
		}
	}
}

func bwrapArgv(t *testing.T, p sandbox.Policy) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	argv, err := local.BwrapArgv(local.BwrapArgs{
		Bwrap:  filepath.Join(dir, "bwrap"),
		Host:   local.Host{Exe: exe},
		Canary: filepath.Join(dir, "canary"),
		Policy: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	return argv
}

// coveringMount is the last bubblewrap mount option whose destination covers
// path: the one that decides what the command sees there.
func coveringMount(argv []string, path string) string {
	under := func(dst string) bool {
		if dst == "/" {
			return strings.HasPrefix(path, "/")
		}
		return path == dst || strings.HasPrefix(path, dst+string(filepath.Separator))
	}
	last := ""
	for i := 1; i < len(argv) && argv[i] != "--"; i++ {
		switch argv[i] {
		case "--bind", "--ro-bind", "--ro-bind-try":
			if i+2 < len(argv) && under(argv[i+2]) {
				last = argv[i]
			}
			i += 2
		case "--tmpfs", "--dev", "--proc":
			if i+1 < len(argv) && under(argv[i+1]) {
				last = argv[i]
			}
			i++
		}
	}
	return last
}

// TestRegistry: one executor per dialect; a second is refused and the release
// frees the dialect.
//
//dsh:test DH-T-EXEC-14
func TestRegistry(t *testing.T) {
	r := shellexec.NewRegistry()
	e := newExecutor(t, &fakeShell{}, nil)
	release, err := r.Register(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(e); err == nil {
		t.Fatal("a second bash executor was registered")
	}
	if got, err := r.Get(shellexec.Bash); err != nil || got != e {
		t.Fatalf("Get(bash) = %v, %v", got, err)
	}
	if _, err := r.Get(shellexec.Pwsh); herr.Kind(err) != shellexec.ReasonNotFound {
		t.Fatalf("Get(pwsh) = %v, want %s", err, shellexec.ReasonNotFound)
	}
	if d := r.Dialects(); !slices.Equal(d, []shellexec.Dialect{shellexec.Bash}) {
		t.Fatalf("Dialects = %v", d)
	}
	release()
	if _, err := r.Get(shellexec.Bash); err == nil {
		t.Fatal("the released executor is still registered")
	}
}

// TestHostSearch: the PATH search adds .exe on Windows, skips relative entries
// and the skipped directories, and Configured refuses a relative path.
//
//dsh:test DH-T-EXEC-13
func TestHostSearch(t *testing.T) {
	files := map[string]bool{`C:\rel\bash.exe`: true, `C:\skip\bash.exe`: true, `C:\tools\bash.exe`: true}
	h := shellexec.Host{
		GOOS:   "windows",
		Env:    subprocess.Env{`Path=rel;C:\skip;C:\tools`},
		Exists: func(p string) bool { return files[p] },
	}
	skip := func(dir string) bool { return h.Clean(dir) == h.Clean(`C:\skip`) }
	if p, ok := h.Search("bash", skip); !ok || p != `C:\tools\bash.exe` {
		t.Fatalf("Search = %q, %v; want C:\\tools\\bash.exe", p, ok)
	}
	if _, err := h.Configured("bash.exe"); herr.Kind(err) != shellexec.ReasonBadConfig {
		t.Fatalf("Configured(relative) = %v, want %s", err, shellexec.ReasonBadConfig)
	}
	if _, err := h.Configured(`C:\none\bash.exe`); herr.Kind(err) != shellexec.ReasonNotFound {
		t.Fatalf("Configured(missing) = %v, want %s", err, shellexec.ReasonNotFound)
	}
}

func TestReasonsAreValid(t *testing.T) {
	for _, r := range shellexec.Reasons {
		if !r.Valid() {
			t.Errorf("reason %q is not valid", r)
		}
	}
}
