package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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
	spilllocal "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
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
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/pwsh"
	shelltools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

var bg = context.Background()

func TestMain(m *testing.M) {
	if code, ok := launch.ServeIfAsked(os.Args[1:], os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// dirs is every session's working directory.
type dirs struct{ root string }

func (d dirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d dirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d dirs) Path() (string, error) { return d.root, nil }
func (d dirs) Root() (string, error) { return d.root, nil }

// countingSeam is a sandbox seam that counts the plans asked of it and grants
// none: a command that reaches it is a command the tool tried to start.
type countingSeam struct{ prepared atomic.Int32 }

func (*countingSeam) Describe(context.Context) []sandbox.Status { return nil }
func (*countingSeam) Reprobe(context.Context) []sandbox.Status  { return nil }
func (s *countingSeam) Prepare(context.Context, sandbox.Policy, []string) (sandbox.Launch, error) {
	s.prepared.Add(1)
	return sandbox.Launch{}, herr.New(sandbox.CodeUnavailable, "the counting seam grants no plan")
}

// fakeShell is a bash dialect whose program is the test binary; it is never
// started by the tests that use it.
type fakeShell struct{ path string }

func (*fakeShell) Dialect() shellexec.Dialect { return shellexec.Bash }
func (s *fakeShell) Program() shellexec.Program {
	return shellexec.Program{Path: s.path, Edition: "fake", Source: shellexec.SourceConfig}
}
func (*fakeShell) Limits() shellexec.Limits { return shellexec.Limits{} }
func (s *fakeShell) Argv(command string, _ func() (string, error)) ([]string, error) {
	return []string{s.path, command}, nil
}
func (*fakeShell) Defaults() []string { return nil }
func (*fakeShell) Unset() []string    { return nil }

func builder() shellexec.Environment {
	return &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}
}

// rig is the real tool pipeline with the shell tools mounted, and a SQLite
// session log in a temp home.
type rig struct {
	t    *testing.T
	root *cordis.Context
	svc  htools.Service
	st   *sqlite.Store
	n    int
}

func newRig(t *testing.T, shells *shellexec.Registry, mode sandbox.Mode) *rig {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	svc := core.NewWith(root, tab, core.Config{Checkpoint: checkpoint.Default{}})
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	wd := dirs{t.TempDir()}
	deps := shelltools.Deps{Shells: shells, Resolver: &sandboxtest.Resolver{Default: mode, Workdir: wd}, Workdir: wd}
	if _, err := shelltools.Mount(root, svc, deps, shelltools.OneShotConfig{}); err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, root: root, svc: svc, st: st}
}

// run executes one call in a new session step and returns its result and the
// raw tool/result event the log holds for it.
func (r *rig) run(name string, args map[string]any) (htools.Result, string) {
	r.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	r.n++
	id := session.ID(fmt.Sprintf("s-shell-tools-%d", r.n))
	c := htools.CallRequest{ID: "c1", Name: name, Arguments: string(raw)}
	h := r.openStep(id, c)
	v, err := r.svc.Resolve(bg, htools.Target{Agent: agent.Ref{Session: id, Incarnation: 1}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer v.Release()
	res, err := r.svc.ExecuteBatch(bg, htools.Batch{View: v, Log: h, Calls: []htools.CallRequest{c}})
	if err != nil {
		r.t.Fatal(err)
	}
	if len(res.Results) != 1 {
		r.t.Fatalf("%d results", len(res.Results))
	}
	evs, err := h.Read(bg, 0, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type() == "tool/result" {
			return res.Results[0], ev.Raw()
		}
	}
	r.t.Fatal("no tool/result was logged")
	return htools.Result{}, ""
}

func (r *rig) openStep(id session.ID, c htools.CallRequest) session.Handle {
	r.t.Helper()
	h, err := session.NewService(r.root, r.st, session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = h.Close(context.Background()) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		r.t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	if err != nil {
		r.t.Fatal(err)
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
			r.t.Fatal(err)
		}
		if _, err := h.Append(bg, d); err != nil {
			r.t.Fatal(err)
		}
	}
	return h
}

// fakeRegistry mounts a bash executor over the counting seam.
func fakeRegistry(t *testing.T) (*shellexec.Registry, *countingSeam) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	seam := &countingSeam{}
	e, err := shellexec.New(&fakeShell{path: exe}, shellexec.Options{Sandbox: seam, Env: builder()})
	if err != nil {
		t.Fatal(err)
	}
	reg := shellexec.NewRegistry()
	if _, err := reg.Register(e); err != nil {
		t.Fatal(err)
	}
	return reg, seam
}

// Scenario: Unknown argument rejected (TC-06b logic). Given a tool call with
// arguments {command, stdin}, When the tool validates input, Then it returns a
// validation error naming stdin and nothing is started. The environment is
// refused the same way: the model names neither.
//
//dsh:test DH-T-EXEC-15
func TestUnknownArgumentRejected(t *testing.T) {
	reg, seam := fakeRegistry(t)
	r := newRig(t, reg, sandbox.DangerFullAccess)
	for _, extra := range []string{"stdin", "env"} {
		res, logged := r.run(shelltools.BashName, map[string]any{"command": "echo hi", extra: "x"})
		if !res.IsError || res.Code != htools.CodeInvalidArguments {
			t.Fatalf("%s: result = %+v, want %s", extra, res, htools.CodeInvalidArguments)
		}
		if !strings.Contains(res.Text, extra) || !strings.Contains(logged, extra) {
			t.Fatalf("%s: the error does not name the argument: %q", extra, res.Text)
		}
	}
	if n := seam.prepared.Load(); n != 0 {
		t.Fatalf("the seam was asked for %d plans: a command was started", n)
	}
}

// TestArgumentsAreNamedOnly: the model-facing schema of both tools has exactly
// command, timeout, description and the two escalation arguments
// sandbox_permissions and justification (execution.md, the shell tools row),
// never stdin or env, and command is required.
//
//dsh:test DH-T-EXEC-15
func TestArgumentsAreNamedOnly(t *testing.T) {
	reg, _ := fakeRegistry(t)
	ds, err := shelltools.Declarations(shelltools.Deps{Shells: reg, Resolver: &sandboxtest.Resolver{}}, shelltools.OneShotConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range ds {
		names = append(names, d.Name())
		var doc struct {
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(d.Schema().Parameters, &doc); err != nil {
			t.Fatal(err)
		}
		want := []string{"command", "timeout", "description", "sandbox_permissions", "justification"}
		ok := len(doc.Properties) == len(want)
		for _, p := range want {
			ok = ok && doc.Properties[p] != nil
		}
		if !ok {
			t.Fatalf("%s: properties = %v, want %v", d.Name(), doc.Properties, want)
		}
		if len(doc.Required) != 1 || doc.Required[0] != "command" {
			t.Fatalf("%s: required = %v, want [command]", d.Name(), doc.Required)
		}
	}
	if strings.Join(names, " ") != "bash pwsh" {
		t.Fatalf("tools = %v, want bash pwsh", names)
	}
}

// TestNoExecutorIsNotFound: pwsh with no pwsh executor mounted fails with
// shellexec/not-found and nothing is started.
func TestNoExecutorIsNotFound(t *testing.T) {
	reg, seam := fakeRegistry(t)
	r := newRig(t, reg, sandbox.DangerFullAccess)
	res, _ := r.run(shelltools.PwshName, map[string]any{"command": "Write-Output hi"})
	if !res.IsError || !strings.Contains(res.Text, string(shellexec.ReasonNotFound)) {
		t.Fatalf("result = %+v, want %s", res, shellexec.ReasonNotFound)
	}
	if n := seam.prepared.Load(); n != 0 {
		t.Fatalf("the seam was asked for %d plans", n)
	}
}

// TestRefusedPlanIsAnError: a command whose plan is refused never ran; the
// call fails with the seam's reason, not a result with an exit code.
func TestRefusedPlanIsAnError(t *testing.T) {
	reg, seam := fakeRegistry(t)
	r := newRig(t, reg, sandbox.WorkspaceWrite)
	res, logged := r.run(shelltools.BashName, map[string]any{"command": "echo hi"})
	if !res.IsError || !strings.Contains(res.Text, string(sandbox.CodeUnavailable)) {
		t.Fatalf("result = %+v, want %s", res, sandbox.CodeUnavailable)
	}
	if strings.Contains(logged, `"exit_code"`) {
		t.Fatalf("a command that never ran has an exit code: %s", logged)
	}
	if n := seam.prepared.Load(); n != 1 {
		t.Fatalf("the seam was asked for %d plans, want 1", n)
	}
}

// Scenario: Tool result carries confinement (TC-06b). Given the Linux lane and
// a danger-full-access layer, When the bash tool runs `true` through the tool
// pipeline, Then the logged result contains exit_code 0 and confinement.state
// Unconfined. The row fails, never skips, on Linux when bash is missing;
// elsewhere it runs when a bash is found.
//
//dsh:test DH-T-EXEC-15
func TestToolResultCarriesConfinement(t *testing.T) {
	h := shellexec.LocalHost(os.Environ())
	if _, err := bash.Find("", h); err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("bash is required on the Linux lane: %v", err)
		}
		t.Skipf("no bash on this lane: %v", err)
	}
	s := localSandbox(t)
	e, err := bash.New(bash.Config{Timeout: 60 * time.Second}, h, shellexec.Options{Sandbox: s, Env: builder()})
	if err != nil {
		t.Fatal(err)
	}
	runReal(t, e, shelltools.BashName, []realCase{{"true", 0, ""}, {"echo hi; exit 3", 3, "hi"}})
}

// The pwsh tool on the Windows lane (TC-06b): Write-Output hi; exit 3 through
// the pipeline gives exit 3 and tail hi. The row fails, never skips, on
// Windows when PowerShell 7 is missing; elsewhere it runs when one is found.
//
//dsh:test DH-T-EXEC-15
func TestPwshToolThroughThePipeline(t *testing.T) {
	h := shellexec.LocalHost(os.Environ())
	p, err := pwsh.Find("", false, h)
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("pwsh is required on the Windows lane: %v", err)
		}
		t.Skipf("no pwsh on this lane: %v", err)
	}
	s := localSandbox(t)
	e, err := pwsh.New(pwsh.Config{Path: p.Path}, h, shellexec.Options{Sandbox: s, Env: builder()})
	if err != nil {
		t.Fatal(err)
	}
	runReal(t, e, shelltools.PwshName, []realCase{{"Write-Output hi; exit 3", 3, "hi"}})
}

func localSandbox(t *testing.T) sandbox.Service {
	t.Helper()
	s, err := local.New(bg, local.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type realCase struct {
	command string
	code    int
	tail    string
}

// runReal runs each case through the pipeline with e mounted under a
// danger-full-access policy and checks the structured and the logged result.
func runReal(t *testing.T, e *shellexec.Executor, name string, cases []realCase) {
	t.Helper()
	reg := shellexec.NewRegistry()
	if _, err := reg.Register(e); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, reg, sandbox.DangerFullAccess)
	for _, c := range cases {
		res, logged := r.run(name, map[string]any{"command": c.command, "description": "probe"})
		if res.IsError {
			t.Fatalf("%q: %s", c.command, res.Text)
		}
		out, ok := res.Value.(shelltools.Result)
		if !ok {
			t.Fatalf("%q: value = %T", c.command, res.Value)
		}
		if out.Outcome != string(shellexec.Exited) || out.ExitCode != c.code || strings.TrimSpace(out.Stdout.Tail) != c.tail {
			t.Fatalf("%q: result = %+v, want exited %d with tail %q", c.command, out, c.code, c.tail)
		}
		if out.Confinement.State != string(sandbox.StateUnconfined) || out.Confinement.Backend != string(sandbox.BackendNone) ||
			out.Confinement.Mode != string(sandbox.DangerFullAccess) {
			t.Fatalf("%q: confinement = %+v, want unconfined under danger-full-access", c.command, out.Confinement)
		}
		for _, want := range []string{
			fmt.Sprintf(`\"exit_code\":%d`, c.code),
			`\"confinement\":{\"mode\":\"danger-full-access\",\"state\":\"unconfined\",\"backend\":\"none\"`,
		} {
			if !strings.Contains(logged, want) {
				t.Fatalf("%q: the logged result lacks %s: %s", c.command, want, logged)
			}
		}
	}
}

// TestTailIsBoundedWithSpill: a stream longer than the bound shows its last
// bytes, marked truncated, with its size; the whole stream's spill reference
// is carried when the executor saved one; and invalid bytes are repaired
// without lengthening the tail past the bound or cutting it.
//
//dsh:test DH-T-EXEC-15
func TestTailIsBoundedWithSpill(t *testing.T) {
	got := shelltools.StreamOf(subprocess.Collected{Tail: []byte("aaaa\u00e9bbb"), Written: 1 << 20}, 4)
	if got.Tail != "bbb" || !got.Truncated || got.Bytes != 1<<20 {
		t.Fatalf("stream = %+v, want tail bbb (cut on a whole character), truncated, 1 MiB", got)
	}
	got = shelltools.StreamOf(subprocess.Collected{Tail: []byte("ok"), Written: 9, Truncated: true, Spill: &event.Spill{Path: "s/x.txt", Bytes: 9}}, 10)
	if got.Tail != "ok" || !got.Truncated || got.Spill == nil || got.Spill.ID != "s/x.txt" || got.Spill.Bytes != 9 {
		t.Fatalf("stream = %+v, want the spill reference carried", got)
	}
	bad := []byte{0xff, 'a', 0xff}
	for _, c := range []struct {
		limit int
		tail  string
	}{
		{3, "?a?"},
		{6, "?a?"},
		{7, "\uFFFDa\uFFFD"},
	} {
		got = shelltools.StreamOf(subprocess.Collected{Tail: bad, Written: 3}, c.limit)
		if len(got.Tail) > c.limit || !utf8.ValidString(got.Tail) || got.Tail != c.tail || got.Truncated || !got.Replaced {
			t.Fatalf("limit %d: stream = %+v (%d bytes), want %q, whole, replaced", c.limit, got, len(got.Tail), c.tail)
		}
	}
	if got = shelltools.StreamOf(subprocess.Collected{Tail: []byte("ok"), Written: 2}, 2); got.Replaced {
		t.Fatalf("valid text marked replaced: %+v", got)
	}
}

// TestTimeoutIsCutBeforeConversion: a timeout in milliseconds too large for a
// time.Duration (one that would overflow negative, one that would wrap to a
// few microseconds, the largest int64) resolves to the executor's cap, not to
// a refusal or an instant timeout.
//
//dsh:test DH-T-EXEC-15
func TestTimeoutIsCutBeforeConversion(t *testing.T) {
	reg, _ := fakeRegistry(t)
	e, err := reg.Get(shellexec.Bash)
	if err != nil {
		t.Fatal(err)
	}
	lim := e.Limits()
	for _, ms := range []int64{lim.MaxTimeout.Milliseconds() + 1, 9223372036855, 18446744073710, math.MaxInt64} {
		d := shelltools.Budget(ms, lim)
		req, err := e.Resolve(shellexec.Request{Shell: shellexec.Bash, Command: "true", Timeout: d, Policy: sandboxtest.Policy(sandbox.DangerFullAccess)})
		if err != nil || req.Timeout != lim.MaxTimeout {
			t.Fatalf("timeout %d ms: budget %v resolved to %v, %v; want the cap %v", ms, d, req.Timeout, err, lim.MaxTimeout)
		}
	}
	if d := shelltools.Budget(1500, lim); d != 1500*time.Millisecond {
		t.Fatalf("budget(1500) = %v", d)
	}
	if d := shelltools.Budget(0, lim); d != 0 {
		t.Fatalf("budget(0) = %v, want 0 (the shell's default)", d)
	}
	if d := shelltools.Budget(-1, lim); d >= 0 {
		t.Fatalf("budget(-1) = %v, want negative for Resolve to refuse", d)
	}
}

// TestOutputPastTheRingIsSpilled: the tool shows what the executor's ring
// holds and cuts nothing more, from collector to result with a spill store
// mounted. Output that fits the ring is shown whole: 40 000 bytes under the
// default 64 000-byte ring (a regression for a tool bound of its own below
// the ring, which cut output nothing saved), and three non-UTF-8 bytes under a
// 3-byte ring, whose repair would lengthen them. Output past the ring is cut
// only where the collector spilled the whole stream.
//
//dsh:test DH-T-EXEC-15
func TestOutputPastTheRingIsSpilled(t *testing.T) {
	h := shellexec.LocalHost(os.Environ())
	if _, err := bash.Find("", h); err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("bash is required on the Linux lane: %v", err)
		}
		t.Skipf("no bash on this lane: %v", err)
	}
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	sp, err := spilllocal.New(l, spilllocal.DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	as := func(n int) string { return fmt.Sprintf("head -c %d /dev/zero | tr '\\0' a", n) }
	for _, c := range []struct {
		ring     int // 0 is the default
		command  string
		bytes    int
		tail     string
		replaced bool
	}{
		{0, as(40_000), 40_000, strings.Repeat("a", 40_000), false},
		{0, as(70_000), 70_000, strings.Repeat("a", shellexec.DefaultTailBytes), false},
		{1000, as(900), 900, strings.Repeat("a", 900), false},
		{1000, as(1500), 1500, strings.Repeat("a", 1000), false},
		{3, `printf '\377a\377'`, 3, "?a?", true},
	} {
		e, err := bash.New(bash.Config{Timeout: 60 * time.Second, TailBytes: c.ring}, h,
			shellexec.Options{Sandbox: localSandbox(t), Env: builder(), Spill: sp})
		if err != nil {
			t.Fatal(err)
		}
		reg := shellexec.NewRegistry()
		if _, err := reg.Register(e); err != nil {
			t.Fatal(err)
		}
		ring := e.Limits().TailBytes
		res, _ := newRig(t, reg, sandbox.DangerFullAccess).run(shelltools.BashName, map[string]any{"command": c.command})
		if res.IsError {
			t.Fatalf("%q: %s", c.command, res.Text)
		}
		out := res.Value.(shelltools.Result).Stdout
		past := c.bytes > ring
		if out.Bytes != int64(c.bytes) || out.Tail != c.tail || out.Replaced != c.replaced || out.Truncated != past {
			t.Fatalf("%q under a %d-byte ring: bytes=%d tail %d bytes %q... truncated=%v replaced=%v; want %d bytes, the tail whole unless past the ring",
				c.command, ring, out.Bytes, len(out.Tail), out.Tail[:min(len(out.Tail), 8)], out.Truncated, out.Replaced, c.bytes)
		}
		if past != (out.Spill != nil) || past && out.Spill.Bytes != int64(c.bytes) {
			t.Fatalf("%q under a %d-byte ring: spill = %+v; want the whole stream spilled exactly when cut", c.command, ring, out.Spill)
		}
	}
}
