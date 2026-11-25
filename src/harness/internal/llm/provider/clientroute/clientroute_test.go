package clientroute_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// roleEnv selects a stand-in role of the re-executed test binary; lineEnv,
// exitEnv and errEnv are its line, exit status and standard error.
const (
	roleEnv = "CLIENTROUTETEST_ROLE"
	lineEnv = "CLIENTROUTETEST_LINES"
	exitEnv = "CLIENTROUTETEST_EXIT"
	errEnv  = "CLIENTROUTETEST_STDERR"
)

func TestMain(m *testing.M) {
	if role := os.Getenv(roleEnv); role != "" {
		os.Exit(standIn(role))
	}
	subprocesstest.Main(m)
}

// standIn plays a client: "print" writes the lines of lineEnv (a JSON array)
// and errEnv on standard error, then a line with the digest of its standard
// input and its arguments, and exits with exitEnv.
func standIn(role string) int {
	var lines []string
	_ = json.Unmarshal([]byte(os.Getenv(lineEnv)), &lines)
	if role != "print" {
		return 3
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Fprint(os.Stderr, os.Getenv(errEnv))
	in, _ := io.ReadAll(os.Stdin)
	sum := sha256.Sum256(in)
	args, _ := json.Marshal(os.Args[1:])
	fmt.Printf("stdin sha256:%s args %s\n", hex.EncodeToString(sum[:]), args)
	code, _ := strconv.Atoi(os.Getenv(exitEnv))
	return code
}

func exe(t *testing.T) subprocess.Program {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return subprocess.Program{Path: p}
}

// env is the stand-in's whole environment: its role, its script and the
// coverage directory.
func env(role string, lines []string, extra ...string) subprocess.Env {
	b, _ := json.Marshal(lines)
	e := subprocess.Env{roleEnv + "=" + role, lineEnv + "=" + string(b)}
	if d := os.Getenv(subprocesstest.CoverDirEnv); d != "" {
		e = append(e, subprocesstest.CoverDirEnv+"="+d)
	}
	return append(e, extra...)
}

func code(err error) vocab.Code {
	var f *vocab.Failure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

var versionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+) \(Claude Code\)$`)

// TestProbe: the version line is read in the given environment, through a
// bounded reader, and refused before any request when the program is missing,
// the line is not recognised, the version is below the floor, the probe exits
// non-zero or does not finish; a later line is ignored; a cancelled caller is
// llm/aborted.
//
//dsh:test DH-T-LLM-18, DH-T-CLAUDECODECLI-1
func TestProbe(t *testing.T) {
	ctx := context.Background()
	floor := clientroute.Version{Major: 2, Minor: 1, Patch: 259}
	spec := func(lines []string, extra ...string) clientroute.ProbeSpec {
		return clientroute.ProbeSpec{Client: "claude-code", Program: exe(t), Args: []string{"--version"},
			Env: env("print", lines, extra...), Pattern: versionRE, Floor: floor, Timeout: 20 * time.Second, Launch: helperPlan(t)}
	}
	v, err := clientroute.Probe(ctx, spec([]string{"2.1.312 (Claude Code)", "Run update to check"}))
	if err != nil || v != (clientroute.Version{Major: 2, Minor: 1, Patch: 312}) || v.String() != "2.1.312" {
		t.Fatalf("probe = %v, %v", v, err)
	}
	if v, err := clientroute.Probe(ctx, spec([]string{"2.1.259 (Claude Code)"})); err != nil || v != floor {
		t.Fatalf("the floor itself = %v, %v", v, err)
	}
	for name, s := range map[string]clientroute.ProbeSpec{
		"too old":     spec([]string{"2.1.100 (Claude Code)"}),
		"major old":   spec([]string{"1.9.999 (Claude Code)"}),
		"unparsable":  spec([]string{"claude version two"}),
		"padded":      spec([]string{" 2.1.312 (Claude Code)"}),
		"empty":       spec(nil),
		"overflowing": spec([]string{"99999999999999999999.1.1 (Claude Code)"}),
		"exit 1":      spec([]string{"2.1.312 (Claude Code)"}, exitEnv+"=1"),
		"over bound":  func() clientroute.ProbeSpec { s := spec([]string{strings.Repeat("9", 64)}); s.MaxBytes = 16; return s }(),
		"no start": func() clientroute.ProbeSpec {
			s := spec(nil)
			s.Program.Path = filepath.Join(t.TempDir(), "none")
			return s
		}(),
	} {
		v, err := clientroute.Probe(ctx, s)
		if code(err) != vocab.CodeClientUpgradeRequired {
			t.Errorf("%s: probe = %v, %v; want llm/client-upgrade-required", name, v, err)
		}
		if err != nil && strings.Contains(err.Error(), "version two") {
			t.Errorf("%s: the client's text reached the failure: %v", name, err)
		}
	}
	hung := clientroute.ProbeSpec{Client: "claude-code", Program: exe(t), Env: subprocesstest.Spec(t, subprocesstest.RoleStubborn).Env,
		Pattern: versionRE, Floor: floor, Timeout: 300 * time.Millisecond, Launch: helperPlan(t)}
	if _, err := clientroute.Probe(ctx, hung); code(err) != vocab.CodeClientUpgradeRequired || !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("a hung probe = %v; want client-upgrade-required naming the deadline", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := clientroute.Probe(cctx, spec([]string{"2.1.312 (Claude Code)"})); code(err) != vocab.CodeAborted {
		t.Errorf("a cancelled probe = %v; want llm/aborted", err)
	}
	if _, err := clientroute.Probe(ctx, clientroute.ProbeSpec{Pattern: regexp.MustCompile(`x`), Launch: helperPlan(t)}); err == nil {
		t.Error("a pattern without three groups was accepted")
	}
	if !(clientroute.Version{Major: 1, Minor: 9}).Less(floor) || floor.Less(floor) || !(clientroute.Version{Major: 2, Minor: 1, Patch: 258}).Less(floor) {
		t.Error("Version.Less disagrees with the ordering")
	}
}

// TestProbeBatchShim: on Windows a client installed as a .cmd shim is found by
// bare name, reported as a batch file, and probed through the checked batch
// run (plan 16.5.1); elsewhere there is no batch file to find.
//
//dsh:test DH-T-LLM-18
func TestProbeBatchShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch shims exist on Windows only")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.cmd"), []byte("@echo 2.1.312 (Claude Code)\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := subprocess.Env{"PATH=" + dir, "PATHEXT=.COM;.EXE;.BAT;.CMD"}
	p, err := clientroute.Locate("claude-code", "", "fakeclaude", env)
	if err != nil || !p.Batch {
		t.Fatalf("locate = %+v, %v; want the shim, reported as a batch file", p, err)
	}
	v, err := clientroute.Probe(context.Background(), clientroute.ProbeSpec{Client: "claude-code", Program: p, Args: []string{"--version"},
		Env: env, Pattern: versionRE, Floor: clientroute.Version{Major: 2, Minor: 1, Patch: 259}, Timeout: 20 * time.Second, Launch: helperPlan(t)})
	if err != nil || v.String() != "2.1.312" {
		t.Fatalf("probe through the shim = %v, %v", v, err)
	}
}

// TestLocate: a configured absolute path is used as given; a relative one is
// refused; a bare name is found on the child's PATH only, never the host's,
// and a missing program asks for the client to be installed.
//
//dsh:test DH-T-LLM-18
func TestLocate(t *testing.T) {
	self := exe(t).Path
	if p, err := clientroute.Locate("claude-code", self, "claude", nil); err != nil || p.Path != self || p.Batch {
		t.Fatalf("configured = %+v, %v", p, err)
	}
	if _, err := clientroute.Locate("claude-code", "bin/claude", "claude", nil); code(err) != vocab.CodeInvalidRequest {
		t.Errorf("relative configured path = %v", err)
	}
	if _, err := clientroute.Locate("claude-code", filepath.Join(t.TempDir(), "claude"), "claude", nil); code(err) != vocab.CodeClientUpgradeRequired {
		t.Errorf("configured missing path = %v", err)
	}
	if _, err := clientroute.Locate("claude-code", t.TempDir(), "claude", nil); code(err) != vocab.CodeClientUpgradeRequired {
		t.Errorf("configured directory = %v", err)
	}
	dir := t.TempDir()
	name := "fakeclient"
	file := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(file, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir) // the host's PATH must not be what is searched
	if _, err := clientroute.Locate("claude-code", "", name, subprocess.Env{"PATH=" + t.TempDir()}); code(err) != vocab.CodeClientUpgradeRequired {
		t.Errorf("a program only on the host's PATH was found: %v", err)
	}
	p, err := clientroute.Locate("claude-code", "", name, subprocess.Env{"PATH=" + dir})
	if err != nil || !strings.EqualFold(p.Path, file) {
		t.Errorf("on the child's PATH = %+v, %v", p, err)
	}
}

type secrets []string

func (s secrets) IsSecretName(n string) bool { return slices.Contains(s, n) }

// TestEnv: the removal lists, the patterns, the default deny rules, DSH_*
// names and the secret store's names are all absent from the child's
// environment; the route's settings and the checked extras are present; an
// extra that names a removed variable or overrides a setting is refused.
//
//dsh:test DH-T-LLM-18, DH-T-CLAUDECODECLI-1
func TestEnv(t *testing.T) {
	spec := clientroute.EnvSpec{
		Remove:   []string{"ANTHROPIC_BASE_URL", "GH_TOKEN"},
		Patterns: []string{"ANTHROPIC_*_BASE_URL"},
		Set:      []string{"CLAUDE_CODE_MAX_RETRIES=0"},
		Extra:    []string{"CLAUDE_CONFIG_DIR=/tmp/empty"},
		Secrets:  secrets{"MY_PAT"},
	}
	host := []string{"PATH=/bin", "ANTHROPIC_BASE_URL=http://x", "ANTHROPIC_VERTEX_BASE_URL=http://y", "GH_TOKEN=gho_x",
		"ANTHROPIC_API_KEY=sk-ant-x", "DSH_HOME=/h", "MY_PAT=p", "HTTPS_PROXY=http://proxy", "CLAUDE_CODE_MAX_RETRIES=9", "=C:=C:\\x", "BROKEN"}
	got, err := spec.Build(host)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "GH_TOKEN", "ANTHROPIC_API_KEY", "DSH_HOME", "MY_PAT", "BROKEN"} {
		if got.Has(n) {
			t.Errorf("%s reached the child", n)
		}
	}
	if got.Get("PATH") != "/bin" || got.Get("HTTPS_PROXY") != "http://proxy" || got.Get("CLAUDE_CODE_MAX_RETRIES") != "0" || got.Get("CLAUDE_CONFIG_DIR") != "/tmp/empty" {
		t.Errorf("child environment = %v", got)
	}
	names := clientroute.Names(got)
	if !slices.IsSorted(names) || slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "=") || n == "" }) {
		t.Errorf("names = %v", names)
	}
	for _, extra := range []string{"GH_TOKEN=x", "anthropic_base_url=x", "ANTHROPIC_FOO_BASE_URL=x", "SOME_API_KEY=x", "DSH_X=1", "MY_PAT=x", "CLAUDE_CODE_MAX_RETRIES=5", "NOVALUE", "=x"} {
		s := spec
		s.Extra = []string{extra}
		if err := s.Check(); err == nil {
			t.Errorf("extra %q was accepted", extra)
		}
		if _, err := s.Build(host); err == nil {
			t.Errorf("Build accepted extra %q", extra)
		}
	}
}

type surfaceOf struct {
	msgs  []vocab.Message
	tools []vocab.ToolSchema
	err   error
}

func (s surfaceOf) Messages() ([]vocab.Message, error) { return s.msgs, s.err }
func (s surfaceOf) Tools() []vocab.ToolSchema          { return s.tools }

func msg(t *testing.T, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	t.Helper()
	m, err := vocab.RestoreMessage("m", role, nil, blocks...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestRender: the system and developer text is the system file; one user
// message is its text; a longer conversation is a transcript with tool calls
// and results and no reasoning; images become the placeholder; the tool file
// holds the canonical schemas as MCP entries; the same request renders to the
// same bytes; a system image, a message error and Copilot's leading system
// section behave as stated.
//
//dsh:test DH-T-LLM-18
func TestRender(t *testing.T) {
	sys := msg(t, vocab.RoleSystem, vocab.TextBlock("Be brief."))
	dev := msg(t, vocab.RoleDeveloper, vocab.TextBlock("Use tools."))
	user := msg(t, vocab.RoleUser, vocab.TextBlock("hello"), vocab.ImageBlock("image/png", "att:1"))
	one, err := clientroute.Render(surfaceOf{msgs: []vocab.Message{sys, dev, user}}, clientroute.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(one.System) != "Be brief.\n\nUse tools." || string(one.Prompt) != "hello\n\n"+clientroute.ImagePlaceholder || one.Tools != nil || one.ToolNames != nil {
		t.Fatalf("one message = system %q prompt %q tools %q", one.System, one.Prompt, one.Tools)
	}
	tool := vocab.ToolSchema{Name: "probe", Description: "Echo <text>", Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)}
	bare := vocab.ToolSchema{Name: "bare"}
	conv := surfaceOf{msgs: []vocab.Message{sys, user,
		msg(t, vocab.RoleAssistant, vocab.ReasoningBlock("secret thoughts", "sig", false), vocab.TextBlock("calling"), vocab.ToolUseBlock("c1", "probe", json.RawMessage(`{"text":"ping"}`)), vocab.ToolUseBlock("c2", "bare", json.RawMessage(` {} `))),
		msg(t, vocab.RoleTool, vocab.ToolResultBlock("c1", false, vocab.TextPart("ping"), vocab.ImagePart("image/png", "att:2")), vocab.ToolResultBlock("c2", true, vocab.TextPart("failed"))),
		msg(t, vocab.RoleUser, vocab.TextBlock("and now?")),
	}, tools: []vocab.ToolSchema{tool, bare}}
	r, err := clientroute.Render(conv, clientroute.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := "The conversation so far is below, oldest first. Continue it: reply to its last turn as the assistant.\n\n" +
		"<conversation>\n<user>\nhello\n" + clientroute.ImagePlaceholder + "\n</user>\n" +
		"<assistant>\ncalling\n<tool_call id=\"c1\" name=\"probe\">{\"text\":\"ping\"}</tool_call>\n<tool_call id=\"c2\" name=\"bare\">{}</tool_call>\n</assistant>\n" +
		"<tool_result id=\"c1\">\nping\n" + clientroute.ImagePlaceholder + "\n</tool_result>\n<tool_result id=\"c2\" error=\"true\">\nfailed\n</tool_result>\n" +
		"<user>\nand now?\n</user>\n</conversation>\n"
	if string(r.Prompt) != want {
		t.Fatalf("transcript =\n%s\nwant\n%s", r.Prompt, want)
	}
	if bytes.Contains(r.Prompt, []byte("secret thoughts")) {
		t.Fatal("reasoning was rendered")
	}
	wantTools := `[{"name":"probe","description":"Echo <text>","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}},{"name":"bare","description":"","inputSchema":{"type":"object"}}]` + "\n"
	if string(r.Tools) != wantTools || !slices.Equal(r.ToolNames, []string{"probe", "bare"}) {
		t.Fatalf("tools = %s %v", r.Tools, r.ToolNames)
	}
	again, _ := clientroute.Render(conv, clientroute.RenderOptions{})
	if !bytes.Equal(again.Prompt, r.Prompt) || !bytes.Equal(again.Tools, r.Tools) || !bytes.Equal(again.System, r.System) {
		t.Fatal("the same request rendered to different bytes")
	}
	cp, _ := clientroute.Render(surfaceOf{msgs: []vocab.Message{sys, user}}, clientroute.RenderOptions{SystemInPrompt: true})
	if len(cp.System) != 0 || !strings.HasPrefix(string(cp.Prompt), "<system>\nBe brief.\n</system>\n\nhello") {
		t.Fatalf("system in prompt = %q / %q", cp.System, cp.Prompt)
	}
	if nosys, _ := clientroute.Render(surfaceOf{msgs: []vocab.Message{user}}, clientroute.RenderOptions{SystemInPrompt: true}); strings.Contains(string(nosys.Prompt), "<system>") {
		t.Fatalf("an empty system section was rendered: %q", nosys.Prompt)
	}
	if _, err := clientroute.Render(surfaceOf{msgs: []vocab.Message{msg(t, vocab.RoleSystem, vocab.ImageBlock("image/png", "a"))}}, clientroute.RenderOptions{}); code(err) != vocab.CodeUnsupportedContent {
		t.Fatalf("a system image = %v", err)
	}
	if _, err := clientroute.Render(surfaceOf{err: errors.New("boom")}, clientroute.RenderOptions{}); err == nil {
		t.Fatal("a message error was swallowed")
	}
}

// TestInvocation: the record writes the attempt directory as {attempt}, keeps
// the program's base name and the environment's names only, and its digests
// equal those recomputed from the logged list and the input; the recorder
// copies in and out; attempt files are private, plain-named and removed.
//
//dsh:test DH-T-LLM-18, DH-T-CLAUDECODECLI-1
func TestInvocation(t *testing.T) {
	d, err := clientroute.NewAttemptDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(d.Path()); len(entries) != 0 {
		t.Fatalf("a new attempt directory holds %v", entries)
	}
	p, err := d.Write("system.txt", []byte("Be brief."))
	if err != nil || p != d.Join("system.txt") {
		t.Fatal(p, err)
	}
	for _, bad := range []string{"", ".", "..", "a/b", filepath.Join("x", "y")} {
		if _, err := d.Write(bad, nil); err == nil {
			t.Errorf("Write(%q) was accepted", bad)
		}
	}
	if _, err := d.Write("system.txt", nil); err == nil {
		t.Error("a file was written twice")
	}
	args := []string{"-p", "--system-prompt-file", d.Join("system.txt"), "--tools", "", "<&>"}
	inv := clientroute.NewInvocation(clientroute.Invocation{Client: "claude-code", Version: clientroute.Version{Major: 2, Minor: 1, Patch: 312},
		Program: exe(t).Path, Args: args, Dir: d.Path(), Stdin: []byte("hello"), Files: d.Files(),
		Env: subprocess.Env{"PATH=/bin", "HOME=/h"}, SessionID: "s"})
	wantArgs := []string{"-p", "--system-prompt-file", filepath.Join(clientroute.AttemptPlaceholder, "system.txt"), "--tools", "", "<&>"}
	if !slices.Equal(inv.Args, wantArgs) || inv.Program != filepath.Base(exe(t).Path) || inv.Version != "2.1.312" || !slices.Equal(inv.Env, []string{"HOME", "PATH"}) {
		t.Fatalf("record = %+v", inv)
	}
	enc, _ := json.Marshal(wantArgs)
	enc = bytes.ReplaceAll(bytes.ReplaceAll(bytes.ReplaceAll(enc, []byte(`\u003c`), []byte("<")), []byte(`\u0026`), []byte("&")), []byte(`\u003e`), []byte(">"))
	sum := sha256.Sum256(enc)
	if inv.ArgsDigest != "sha256:"+hex.EncodeToString(sum[:]) || inv.ArgsDigest != clientroute.ArgsDigest(clientroute.Placeholder(args, d.Path())) {
		t.Fatalf("args digest = %s", inv.ArgsDigest)
	}
	if inv.StdinDigest != clientroute.Digest([]byte("hello")) || len(inv.Files) != 1 || inv.Files[0] != (event.FileDigest{Name: "system.txt", Digest: clientroute.Digest([]byte("Be brief."))}) {
		t.Fatalf("digests = %+v", inv)
	}
	if !slices.Equal(clientroute.ExpandPlaceholder(inv.Args, d.Path()), args) || clientroute.ArgsDigest(nil) != clientroute.Digest([]byte("[]")) {
		t.Fatal("the placeholder does not round-trip")
	}
	if !slices.Equal(clientroute.Placeholder(args, ""), args) {
		t.Fatal("an empty directory changed the list")
	}
	var r clientroute.Recorder
	if r.Invocation() != nil {
		t.Fatal("a recorder reported before Set")
	}
	r.Update(func(*event.ClientInvocation) { t.Fatal("Update ran before Set") })
	r.Set(inv)
	inv.Args[0] = "changed"
	r.Update(func(i *event.ClientInvocation) { i.Model, i.Denials = "claude-sonnet", 1 })
	got := r.Invocation()
	got.Env[0] = "changed"
	if again := r.Invocation(); again.Args[0] != "-p" || again.Env[0] != "HOME" || again.Model != "claude-sonnet" || again.Denials != 1 {
		t.Fatalf("the recorder shares its record: %+v", again)
	}
	id := clientroute.NewSessionID()
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) || id == clientroute.NewSessionID() {
		t.Fatalf("session id %q is not a fresh v4 UUID", id)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(d.Path()); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("attempt directory mode = %v, %v", fi.Mode(), err)
		}
	}
	if err := d.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the attempt directory is left: %v", err)
	}
	if _, err := clientroute.NewAttemptDir(filepath.Join(d.Path(), "gone")); err == nil {
		t.Fatal("an attempt directory under a missing parent was made")
	}
}

// TestRun: one start writes the input and closes it, reads standard output as
// bounded lines, keeps a bounded window on standard error for a marker, and
// reports the exit; a line over the bound is ErrLineTooLong; a refused start
// is llm/transport.
//
//dsh:test DH-T-LLM-18
func TestRun(t *testing.T) {
	ctx := context.Background()
	spec := clientroute.RunSpec{Client: "claude-code", Program: exe(t), Args: []string{"-p", "x y"}, Dir: t.TempDir(),
		Env:   env("print", []string{`{"type":"a"}`, "", `{"type":"b"}`}, errEnv+"=error: unknown option '--max-turns'", exitEnv+"=3"),
		Stdin: []byte("the request"), Launch: helperPlan(t)}
	r, err := clientroute.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop(0) })
	var lines []string
	for {
		l, err := r.ReadLine()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(l))
	}
	wantLast := "stdin " + clientroute.Digest([]byte("the request")) + ` args ["-p","x y"]`
	if len(lines) != 4 || lines[0] != `{"type":"a"}` || lines[1] != "" || lines[3] != wantLast {
		t.Fatalf("lines = %q", lines)
	}
	if got := r.Wait(20 * time.Second); got != 3 {
		t.Fatalf("exit = %d", got)
	}
	if !r.StderrHas("unknown option") || r.StderrHas("nothing like this") || r.Pid() <= 0 {
		t.Fatal("standard error window")
	}
	if err := r.Stop(0); err != nil || r.Stop(0) != nil {
		t.Fatalf("stop = %v", err)
	}

	long := spec
	long.Env = env("print", []string{strings.Repeat("x", 200)})
	long.MaxLine, long.StderrBytes = 64, 8
	r2, err := clientroute.Start(ctx, long)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r2.Stop(0) })
	if _, err := r2.ReadLine(); !errors.Is(err, clientroute.ErrLineTooLong) {
		t.Fatalf("a long line = %v", err)
	}
	if err := r2.Stop(time.Second); err != nil {
		t.Fatal(err)
	}

	bad := spec
	bad.Program = subprocess.Program{Path: filepath.Join(t.TempDir(), "missing")}
	if _, err := clientroute.Start(ctx, bad); code(err) != vocab.CodeTransport {
		t.Fatalf("a missing program = %v", err)
	}
	rel := spec
	rel.Program = subprocess.Program{Path: "relative"}
	if _, err := clientroute.Start(ctx, rel); code(err) != vocab.CodeTransport {
		t.Fatalf("a relative program = %v", err)
	}
	nul := spec
	nul.Args = []string{"a\x00b"}
	if _, err := clientroute.Start(ctx, nul); code(err) != vocab.CodeInvalidRequest {
		t.Fatalf("an argument holding NUL = %v", err)
	}
}

// TestRunCancelStopsTheTree: when the attempt's context ends, the client and
// the grandchild it started (the listing server's place) are both gone, and
// the output ends.
//
//dsh:test DH-T-LLM-18, DH-T-CLAUDECODECLI-4
func TestRunCancelStopsTheTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := clientroute.Start(ctx, clientroute.RunSpec{Client: "claude-code", Program: exe(t), Dir: t.TempDir(),
		Env: subprocesstest.Spec(t, subprocesstest.RoleTree).Env, Grace: 100 * time.Millisecond, Launch: helperPlan(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop(0) })
	line, err := r.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	var child, grandchild int
	if _, err := fmt.Sscanf(string(line), "tree %d %d", &child, &grandchild); err != nil {
		t.Fatalf("tree line %q: %v", line, err)
	}
	cancel()
	if _, err := r.ReadLine(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the cancel = %v, want the output's end", err)
	}
	wctx, wcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer wcancel()
	procwait.Until(wctx, t, func() bool { return subprocesstest.Exited(child) && subprocesstest.Exited(grandchild) })
}

// TestTapped: the tap sees every line the reader returns, in order, and not
// an error; a nil tap leaves the reader as it is.
//
//dsh:test DH-T-LLM-18
func TestTapped(t *testing.T) {
	src := []string{"a", "b"}
	next := func() ([]byte, error) {
		if len(src) == 0 {
			return nil, io.EOF
		}
		l := src[0]
		src = src[1:]
		return []byte(l), nil
	}
	var seen []string
	r := clientroute.Tapped(next, func(l []byte) { seen = append(seen, string(l)) })
	for {
		if _, err := r(); err != nil {
			break
		}
	}
	if !slices.Equal(seen, []string{"a", "b"}) {
		t.Fatalf("tap saw %q", seen)
	}
	if clientroute.Tapped(nil, nil) != nil {
		t.Fatal("a nil tap wrapped the reader")
	}
}

// helperPlan is the explicit unconfined helper plan a signed-in client starts
// under.
func helperPlan(t testing.TB) sandbox.Launch {
	t.Helper()
	l, err := sandbox.Helper(sandboxtest.Policy(sandbox.DangerFullAccess), "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestHelperLaunch: the client's plan is the unconfined helper plan of the
// policy ForHelper issues for its kind; no resolver, or one that does not let
// the kind run unconfined, is llm/invalid-request; a probe or a run without a
// plan starts nothing, and a started run reports it ran unconfined (DH-D-EXEC-4).
//
//dsh:test DH-T-EXEC-8
func TestHelperLaunch(t *testing.T) {
	ctx := context.Background()
	if _, err := clientroute.HelperLaunch(ctx, nil, "claude-code", "claude-code"); code(err) != vocab.CodeInvalidRequest {
		t.Fatalf("no resolver = %v; want llm/invalid-request", err)
	}
	if _, err := clientroute.HelperLaunch(ctx, &sandboxtest.Resolver{}, "claude-code", "claude-code"); code(err) != vocab.CodeInvalidRequest {
		t.Fatalf("a kind not allowed unconfined = %v; want llm/invalid-request", err)
	}
	plan, err := clientroute.HelperLaunch(ctx, &sandboxtest.Resolver{UnconfinedKinds: []string{"claude-code"}}, "claude-code", "claude-code")
	if err != nil || !plan.Valid() || plan.IsTrusted() || plan.Reason() != "helper claude-code, unconfined by its policy" {
		t.Fatalf("HelperLaunch = %v, %v; want the claude-code helper plan", plan, err)
	}

	probe := clientroute.ProbeSpec{Client: "claude-code", Program: exe(t), Args: []string{"--version"},
		Env: env("print", []string{"2.1.312 (Claude Code)"}), Pattern: versionRE, Timeout: 20 * time.Second}
	if _, err := clientroute.Probe(ctx, probe); err == nil || !strings.Contains(err.Error(), "no launch plan") {
		t.Fatalf("a probe without a plan = %v", err)
	}
	run := clientroute.RunSpec{Client: "claude-code", Program: exe(t), Dir: t.TempDir(), Env: env("print", []string{"x"})}
	if _, err := clientroute.Start(ctx, run); code(err) != vocab.CodeTransport || !strings.Contains(err.Error(), string(subprocess.ReasonNoLaunch)) {
		t.Fatalf("a run without a plan = %v; want llm/transport naming %s", err, subprocess.ReasonNoLaunch)
	}
	run.Launch = plan
	r, err := clientroute.Start(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop(0) })
	if c := r.Confinement(); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone {
		t.Fatalf("Confinement = %+v; want unconfined, backend none", c)
	}
}
