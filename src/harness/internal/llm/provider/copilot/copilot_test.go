package copilot_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/copilot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// The fixtures under testdata are SYNTHETIC, pending the 16.5.9 recording
// (DH-E-COPILOTCLI-1): written from docs/harness/libs/github-copilot-cli.md
// sec. 5.2 and 5.4, not recorded from a real client.

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// planted is the token shape the fixtures plant in the client's text and the
// stand-in's standard error; it must never reach a failure.
const planted = "PLANTED"

// model is the route's default model.
const model = "claude-sonnet-4.5"

// version is the probe line of the version the route was read against.
const version = "GitHub Copilot CLI 1.0.115-7."

var probeTool = vocab.ToolSchema{Name: "probe", Description: "Echo a text",
	Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}

// rig is one stand-in client: its record directory and the route config that
// starts it.
type rig struct {
	dir string
	cfg copilot.Config
}

// newRig plays fixture (a testdata name) with the stand-in settings extra.
func newRig(t *testing.T, fixture string, extra ...string) *rig {
	t.Helper()
	dir := t.TempDir()
	path := must(filepath.Abs(filepath.Join("testdata", fixture+".jsonl")))
	env := append([]string{roleEnv + "=copilot", recordEnv + "=" + dir, fixtureEnv + "=" + path, versionEnv + "=" + version}, extra...)
	return &rig{dir: dir, cfg: copilot.Config{Program: must(os.Executable()), Env: env, TempDir: t.TempDir(), Grace: 200 * time.Millisecond}}
}

func (r *rig) runtime(t *testing.T) *llm.Runtime {
	t.Helper()
	rt := llm.New()
	must(rt.Register("copilot", must(copilot.Route(r.cfg))))
	return rt
}

// seen is what the stand-in recorded; ok is false when it never ran past the
// probe.
func (r *rig) seen(t *testing.T) (seen, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, "seen.json"))
	if errors.Is(err, os.ErrNotExist) {
		return seen{}, false
	}
	var s seen
	if err != nil || json.Unmarshal(raw, &s) != nil {
		t.Fatalf("the stand-in's record: %v", err)
	}
	return s, true
}

// probes is how many times the stand-in was asked its version.
func (r *rig) probes() int {
	raw, _ := os.ReadFile(filepath.Join(r.dir, "probes"))
	return strings.Count(string(raw), "\n")
}

// newSession is a session on copilot-cli with a system and a user message,
// declaring the probe tool when tools is set, and its store.
func newSession(t *testing.T, tools bool) (session.Handle, store.Store) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	st := memstore.New()
	h := must(session.NewService(root, st, session.Config{}).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	hdr := event.RequestHeaderPayload{Route: copilot.RouteID}
	if tools {
		hdr.Tools = []vocab.ToolSchema{probeTool}
	}
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(hdr)),
		must(event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: must(vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock("Be brief.")))})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))})),
	}))
	return h, st
}

// sink appends each settlement to the session, as the driver does.
type sink struct {
	h       session.Handle
	records []llm.Attempt
	seqs    []event.Seq
}

func (s *sink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	r, err := s.h.Append(bg, must(a.Draft()))
	if err != nil {
		return err
	}
	s.seqs = append(s.seqs, r.Seq)
	return nil
}

// attempt prepares and streams one attempt on the session's next request.
func attempt(t *testing.T, rt *llm.Runtime, h session.Handle) ([]vocab.Chunk, *sink) {
	t.Helper()
	p := must(rt.Prepare(bg, copilot.RouteID, model, vocab.Controls{}))
	s := &sink{h: h}
	var chunks []vocab.Chunk
	for c := range p.Stream(bg, must(h.NextRequest()), s) {
		chunks = append(chunks, c)
	}
	if len(s.records) != 1 {
		t.Fatalf("settlements = %d, want exactly one", len(s.records))
	}
	return chunks, s
}

func finishOf(t *testing.T, chunks []vocab.Chunk) vocab.Finish {
	t.Helper()
	f, ok := chunks[len(chunks)-1].(vocab.Finish)
	if !ok {
		t.Fatalf("the stream ended with %T, not a Finish", chunks[len(chunks)-1])
	}
	return f
}

// logged reads the settlement at seq back from the store and returns its
// client invocation, as a reader of the log sees it.
func logged(t *testing.T, st store.Store, h session.Handle, seq event.Seq) *event.ClientInvocation {
	t.Helper()
	r := must(st.OpenRead(bg, string(h.ID())))
	defer func() { _ = r.Close(bg) }()
	recs := must(r.Read(bg, 0, 1000, store.ReadOptions{WithAux: true}))
	for _, rec := range recs {
		if rec.Seq != seq {
			continue
		}
		switch rec.Type {
		case event.AssistantMessage.Name():
			return must(event.AssistantMessage.Decode(rec)).Client
		case event.AssistantAttempt.Name():
			return must(event.AssistantAttempt.Decode(rec)).Client
		}
	}
	t.Fatalf("no settlement at seq %d", seq)
	return nil
}

func rebuilt(t *testing.T, st store.Store, h session.Handle, asOf event.Seq) surface.Built {
	t.Helper()
	r := must(st.OpenRead(bg, string(h.ID())))
	t.Cleanup(func() { _ = r.Close(bg) })
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	return must(surface.Rebuild(bg, event.Default(), read, string(h.ID()), asOf))
}

// TestCopilotSettlesOnce is Scenario "A client route settles once" on the
// Copilot route: a stand-in replaying a synthetic success settles one
// assistant/message, and the logged invocation is the one started. The
// argument list rebuilt from the log (Args with {attempt}) equals the logged
// list and its digest; the request rebuilt from the log at requestAsOf renders,
// with the system text as the leading <system> section, to the standard input
// and attempt files whose digests were logged; both equal what the stand-in
// received. The client session id chosen by the harness is logged and passed
// as --session-id (M1-D-PIPE-7 A). The client ran in an empty private
// directory holding only the attempt files, gone afterwards, after one probe in
// the same environment.
//
//dsh:test DH-T-LLM-24, DH-T-COPILOTCLI-1
func TestCopilotSettlesOnce(t *testing.T) {
	r := newRig(t, "text")
	h, st := newSession(t, true)
	chunks, s := attempt(t, r.runtime(t), h)
	a := s.records[0]
	if !a.Succeeded() || must(a.Draft()).Type() != "assistant/message" || finishOf(t, chunks).Reason != vocab.FinishStop {
		t.Fatalf("attempt = %+v", a.Finish)
	}
	if got := a.Message.Blocks(); len(got) != 1 || got[0].Text.Text != "Hello there." {
		t.Fatalf("message = %+v", got)
	}
	inv := logged(t, st, h, s.seqs[0])
	if inv == nil {
		t.Fatal("the settlement logged no client invocation")
	}
	if inv.Client != copilot.Client || inv.Version != "1.0.115" || inv.Program != filepath.Base(r.cfg.Program) || inv.SessionID == "" || inv.Retries != 0 {
		t.Fatalf("invocation = %+v", inv)
	}
	want := copilot.Args(copilot.ArgsSpec{Dir: clientroute.AttemptPlaceholder, Model: model, SessionID: inv.SessionID, Tools: []string{"probe"}})
	if !slices.Equal(inv.Args, want) || inv.ArgsDigest != clientroute.ArgsDigest(want) {
		t.Fatalf("logged args %q (%s), rebuilt %q", inv.Args, inv.ArgsDigest, want)
	}
	re := rebuilt(t, st, h, a.RequestAsOf)
	if re.Digest() != a.RequestDigest {
		t.Fatalf("rebuilt digest %s, settled %s", re.Digest(), a.RequestDigest)
	}
	rr := must(clientroute.Render(re, clientroute.RenderOptions{SystemInPrompt: true}))
	if inv.StdinDigest != clientroute.Digest(rr.Prompt) {
		t.Fatalf("stdin digest %s, rebuilt %s", inv.StdinDigest, clientroute.Digest(rr.Prompt))
	}
	files := map[string]string{}
	for _, f := range inv.Files {
		files[f.Name] = f.Digest
	}
	if len(files) != 2 || files[copilot.ToolsFile] != clientroute.Digest(rr.Tools) || files[copilot.MCPFile] == "" {
		t.Fatalf("file digests %v", files)
	}
	got, ok := r.seen(t)
	if !ok {
		t.Fatal("the stand-in did not run")
	}
	if !slices.Equal(clientroute.Placeholder(got.Args, got.Cwd), inv.Args) {
		t.Fatalf("received args %q, logged %q", got.Args, inv.Args)
	}
	if i := slices.Index(got.Args, "--session-id"); i < 0 || got.Args[i+1] != inv.SessionID {
		t.Fatalf("the logged session id %s was not the one passed: %q", inv.SessionID, got.Args)
	}
	if got.Stdin != "<system>\nBe brief.\n</system>\n\nhello" || clientroute.Digest([]byte(got.Stdin)) != inv.StdinDigest {
		t.Fatalf("received stdin %q", got.Stdin)
	}
	if !slices.Equal(got.Files, []string{copilot.MCPFile, copilot.ToolsFile}) || !slices.Equal(got.Tools, []string{"probe"}) {
		t.Fatalf("the working directory held %v; the listing server listed %v", got.Files, got.Tools)
	}
	if _, err := os.Stat(got.Cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the attempt directory is left behind: %v", err)
	}
	if n := r.probes(); n != 1 {
		t.Fatalf("probes = %d, want 1", n)
	}
	if !slices.Contains(inv.Env, "COPILOT_AUTO_UPDATE") || slices.ContainsFunc(inv.Env, func(n string) bool { return strings.Contains(n, "=") }) {
		t.Fatalf("logged env names %v", inv.Env)
	}
}

// TestCopilotNoAmbientCredential is Scenario "No ambient credential reaches the
// client" on the Copilot route: with GH_TOKEN, GITHUB_TOKEN,
// COPILOT_GITHUB_TOKEN and the provider, model, approval and build overrides
// in the host's environment, the stand-in reports every one absent; the route's
// settings are present, and the client home and host are passed on unread. A
// request without tools passes no MCP configuration and an empty tool list.
//
//dsh:test DH-T-LLM-24, DH-T-COPILOTCLI-1
func TestCopilotNoAmbientCredential(t *testing.T) {
	home := t.TempDir() // a stand-in Copilot home, never the user's
	for _, kv := range [][2]string{
		{"GH_TOKEN", "gho_" + planted}, {"GITHUB_TOKEN", "ghu_" + planted}, {"COPILOT_GITHUB_TOKEN", "github_pat_" + planted},
		{"ANTHROPIC_API_KEY", "sk-ant-" + planted}, {"ANTHROPIC_AUTH_TOKEN", "x"},
		{"COPILOT_PROVIDER_BASE_URL", "http://127.0.0.1:1"}, {"COPILOT_PROVIDER_TYPE", "openai"}, {"COPILOT_PROVIDER_EXTRA", "x"},
		{"COPILOT_PROVIDERS_CONFIG", "x"}, {"COPILOT_OFFLINE", "1"}, {"COPILOT_MODEL", "other"}, {"COPILOT_ALLOW_ALL", "1"},
		{"COPILOT_ASSISTED_APPROVAL", "1"}, {"COPILOT_CLI_VERSION", "0.0.1"}, {"COPILOT_CLI_DIST_DIR", "/x"}, {"DSH_HOME", "/h"},
		{"COPILOT_AUTO_UPDATE", "true"}, {"COPILOT_TELEMETRY_DISABLED", "false"}, {"COPILOT_HOME", home}, {"GH_HOST", "github.com"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	r := newRig(t, "text")
	h, _ := newSession(t, false)
	_, s := attempt(t, r.runtime(t), h)
	if !s.records[0].Succeeded() {
		t.Fatalf("attempt = %+v", s.records[0].Finish)
	}
	got, _ := r.seen(t)
	for _, n := range []string{"GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_EXTRA", "COPILOT_PROVIDERS_CONFIG", "COPILOT_OFFLINE",
		"COPILOT_MODEL", "COPILOT_ALLOW_ALL", "COPILOT_ASSISTED_APPROVAL", "COPILOT_CLI_VERSION", "COPILOT_CLI_DIST_DIR", "DSH_HOME"} {
		if !slices.Contains(got.Absent, n) {
			t.Errorf("%s reached the client", n)
		}
	}
	for n, v := range map[string]string{"COPILOT_AUTO_UPDATE": "false", "COPILOT_TELEMETRY_DISABLED": "true", "COPILOT_HOME": home, "GH_HOST": "github.com"} {
		if got.Env[n] != v {
			t.Errorf("%s = %q, want %q", n, got.Env[n], v)
		}
	}
	if i := slices.Index(got.Args, "--available-tools"); slices.Contains(got.Args, "--additional-mcp-config") || len(got.Files) != 0 || i < 0 || got.Args[i+1] != "" {
		t.Errorf("a request without tools passed tools: %q, files %v", got.Args, got.Files)
	}
}

// TestCopilotStreamMapping: synthetic text (with unknown types), reasoning and
// text, one tool request and two parallel ones give the tabled chunks and
// finish. A tool call is returned unexecuted (M1-D-PIPE-6 A): the listing
// server listed it, the call comes back as ToolCallDelta under the harness's
// name and Finish{ToolCalls}, and the tree is stopped at it: the stand-in
// stalls after the message, so the client and the listing server are gone only
// because the route stopped them, and the lines after the message (a
// tool.execution_start and a second turn) are never read.
//
//dsh:test DH-T-LLM-25, DH-T-COPILOTCLI-2
func TestCopilotStreamMapping(t *testing.T) {
	t.Run("reasoning", func(t *testing.T) {
		r := newRig(t, "reasoning")
		h, _ := newSession(t, false)
		chunks, s := attempt(t, r.runtime(t), h)
		var kinds []string
		for _, c := range chunks {
			switch c := c.(type) {
			case vocab.BlockStart:
				kinds = append(kinds, "start:"+string(c.Kind))
			case vocab.ReasoningDelta:
				kinds = append(kinds, "reasoning:"+c.Text)
			case vocab.TextDelta:
				kinds = append(kinds, "text:"+c.Text)
			case vocab.BlockEnd:
				kinds = append(kinds, "end")
			case vocab.UsageChunk:
				kinds = append(kinds, "usage")
			case vocab.Finish:
				kinds = append(kinds, "finish:"+string(c.Reason))
			}
		}
		want := []string{"start:reasoning", "reasoning:The user ", "reasoning:greets.", "end", "start:text", "text:Hi.", "end", "finish:stop"}
		if !slices.Equal(kinds, want) {
			t.Fatalf("chunks %v, want %v", kinds, want)
		}
		b := s.records[0].Message.Blocks()
		if len(b) != 2 || b[0].Reasoning.Text != "The user greets." || b[1].Text.Text != "Hi." {
			t.Fatalf("message = %+v", b)
		}
	})
	for _, tc := range []struct {
		fixture string
		calls   []string // the returned calls' ids
		args    []string
		text    string
	}{
		{"tool", []string{"call_SYNTH1"}, []string{`{"text":"ping"}`}, "Probing."},
		{"parallel", []string{"call_A", "call_B"}, []string{`{"text":"a"}`, `{"text":"b"}`}, ""},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			r := newRig(t, tc.fixture, modeEnv+"=hang")
			h, st := newSession(t, true)
			chunks, s := attempt(t, r.runtime(t), h)
			a := s.records[0]
			if finishOf(t, chunks).Reason != vocab.FinishToolCalls || !a.Succeeded() || must(a.Draft()).Type() != "assistant/message" {
				t.Fatalf("finish = %+v", a.Finish)
			}
			var named []string
			for _, c := range chunks {
				if d, ok := c.(vocab.ToolCallDelta); ok && d.Name != "" {
					named = append(named, d.Name)
				}
			}
			var ids, args []string
			text := ""
			for _, b := range a.Message.Blocks() {
				switch b.Kind {
				case vocab.KindToolUse:
					if b.ToolUse.Name != "probe" {
						t.Fatalf("tool call name %q, want the harness's name", b.ToolUse.Name)
					}
					ids, args = append(ids, b.ToolUse.ID), append(args, string(b.ToolUse.Args))
				case vocab.KindText:
					text += b.Text.Text
				}
			}
			if !slices.Equal(ids, tc.calls) || !slices.Equal(args, tc.args) || len(named) != len(tc.calls) || named[0] != "probe" || text != tc.text {
				t.Fatalf("calls %v %v (deltas %v), text %q; want %v %v %q", ids, args, named, text, tc.calls, tc.args, tc.text)
			}
			if inv := logged(t, st, h, s.seqs[0]); inv == nil || inv.Retries != 0 {
				t.Fatalf("a line after the stop was read: %+v", inv)
			}
			got, _ := r.seen(t)
			if got.Server == 0 || !slices.Equal(got.Tools, []string{"probe"}) {
				t.Fatalf("the listing server did not list the tool: %+v", got)
			}
			ctx, cancel := context.WithTimeout(bg, 30*time.Second)
			defer cancel()
			procwait.Until(ctx, t, func() bool { return subprocesstest.Exited(got.Server) && subprocesstest.Exited(got.Client) })
		})
	}
}

// countingAdapter is a second route that must never be prepared.
type countingAdapter struct{ prepares atomic.Int32 }

func (c *countingAdapter) Prepare(context.Context, vocab.RouteID, string, vocab.CallConfig) (llm.Call, error) {
	c.prepares.Add(1)
	return nil, errors.New("not used")
}

// TestCopilotFailureMapping: each row of github-copilot-cli.md sec. 5.4 gives
// its code and class as exactly one assistant/attempt with its invocation. A
// signed-out client (a non-zero exit with nothing the route can place) is
// Permanent, so the route's policy never retries it and a second registered
// route is never prepared; until DH-E-COPILOTCLI-1 pins the signed-out line its
// code is llm/client-contract, and the check that it is llm/sign-in-required
// stays open. A structured error line (inference: session.error) naming
// authentication is llm/sign-in-required before any assistant line and llm/auth
// after one; a rate limit is Transient. A tool.execution_start line, a
// tools/call reaching the listing server, unparseable output, a placed line
// missing its fields and output with no result are llm/client-contract. A token
// planted in the client's text and standard error never reaches a failure.
//
//dsh:test DH-T-LLM-26, DH-T-COPILOTCLI-3
func TestCopilotFailureMapping(t *testing.T) {
	for _, tc := range []struct {
		name, fixture string
		tools         bool
		extra         []string
		code          vocab.Code
		class         vocab.Class
		says          string
	}{
		{"signed out", "signed-out", false, []string{exitEnv + "=1"}, vocab.CodeClientContract, vocab.Permanent, "status 1"},
		{"sign-in line", "signed-in-line", false, []string{exitEnv + "=1"}, vocab.CodeSignInRequired, vocab.Permanent, "copilot login"},
		{"rejected sign-in", "rejected", false, []string{exitEnv + "=1"}, vocab.CodeAuth, vocab.Permanent, "copilot login"},
		{"rate limit", "rate-limit", false, []string{exitEnv + "=1"}, vocab.CodeRateLimit, vocab.Transient, "rate limited"},
		{"tool ran", "tool-ran", true, nil, vocab.CodeClientContract, vocab.Permanent, "tool.execution_start"},
		{"listing asked to run", "tool", true, []string{modeEnv + "=call"}, vocab.CodeClientContract, vocab.Permanent, "listing server"},
		{"unparseable", "unparseable", false, nil, vocab.CodeClientContract, vocab.Permanent, "not a JSON object"},
		{"unknown shape", "unknown-shape", false, nil, vocab.CodeClientContract, vocab.Permanent, "messageId"},
		{"no result", "no-result", false, nil, vocab.CodeClientContract, vocab.Permanent, "without a result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.fixture, append([]string{stderrEnv + "=Error: not signed in gho_" + planted}, tc.extra...)...)
			rt := r.runtime(t)
			other := &countingAdapter{}
			must(rt.Register("other", llm.Route{ID: "other/route", Models: []llm.Model{{Name: "m"}}, Adapter: other}))
			h, st := newSession(t, tc.tools)
			p := must(rt.Prepare(bg, copilot.RouteID, model, vocab.Controls{}))
			s := &sink{h: h}
			for range p.Stream(bg, must(h.NextRequest()), s) {
			}
			if len(s.records) != 1 {
				t.Fatalf("settlements = %d", len(s.records))
			}
			a := s.records[0]
			f := a.Finish.Failure
			if must(a.Draft()).Type() != "assistant/attempt" || f == nil || f.Code != tc.code || f.Class != tc.class || !strings.Contains(f.Message, tc.says) {
				t.Fatalf("finish = %+v (%+v), want %s %s saying %q", a.Finish, f, tc.code, tc.class, tc.says)
			}
			if strings.Contains(f.Message, planted) || strings.Contains(f.Message, "not signed in gho") || strings.Contains(f.Message, "Welcome") {
				t.Fatalf("the client's text reached the failure: %q", f.Message)
			}
			if _, stop := p.Retry().Next(f, 1, 0, 0.5); tc.class == vocab.Permanent && stop == "" {
				t.Fatalf("the route's policy would retry %s", f.Code)
			}
			if other.prepares.Load() != 0 {
				t.Fatal("another route was prepared")
			}
			if inv := logged(t, st, h, s.seqs[0]); inv == nil || inv.SessionID == "" {
				t.Fatalf("a failed attempt logged no invocation: %+v", inv)
			}
		})
	}
}

// TestCopilotRefusedBeforeInput: a client below the floor, one whose version
// line is not recognised and a missing one fail Prepare with
// llm/client-upgrade-required, and the client is never started with the
// request, so no standard input is written; a control the client cannot take
// is refused before the probe.
//
//dsh:test DH-T-LLM-24, DH-T-COPILOTCLI-1
func TestCopilotRefusedBeforeInput(t *testing.T) {
	for name, v := range map[string]string{"too old": "GitHub Copilot CLI 1.0.114-3.", "major old": "GitHub Copilot CLI 0.9.999.", "unparsable": "copilot 1.0.115"} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "text", versionEnv+"="+v)
			_, err := r.runtime(t).Prepare(bg, copilot.RouteID, model, vocab.Controls{})
			var f *vocab.Failure
			if !errors.As(err, &f) || f.Code != vocab.CodeClientUpgradeRequired || f.Class != vocab.Permanent {
				t.Fatalf("prepare = %v", err)
			}
			if _, ran := r.seen(t); ran || r.probes() != 1 {
				t.Fatalf("the client ran (%v) or was probed %d times, want only one probe", ran, r.probes())
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		r := newRig(t, "text")
		r.cfg.Program = filepath.Join(t.TempDir(), "copilot")
		_, err := r.runtime(t).Prepare(bg, copilot.RouteID, model, vocab.Controls{})
		var f *vocab.Failure
		if !errors.As(err, &f) || f.Code != vocab.CodeClientUpgradeRequired {
			t.Fatalf("prepare = %v", err)
		}
	})
	t.Run("controls", func(t *testing.T) {
		r := newRig(t, "text")
		temp := 0.5
		for _, c := range []vocab.Controls{{Temperature: &temp}, {TopP: &temp}, {Stop: []string{"x"}}, {MaxOutputTokens: 10},
			{ToolChoice: vocab.ToolChoice{Mode: vocab.ToolChoiceRequired}}} {
			_, err := r.runtime(t).Prepare(bg, copilot.RouteID, model, c)
			var f *vocab.Failure
			if !errors.As(err, &f) || f.Code != vocab.CodeUnsupportedOption {
				t.Errorf("controls %+v: prepare = %v", c, err)
			}
		}
		if r.probes() != 0 {
			t.Fatalf("an unsupported control was probed for (%d)", r.probes())
		}
	})
}

// TestCopilotCancelStopsTheTree: a stand-in that has started the listing
// server and stalls mid-message is cancelled: the stream ends with
// Finish{Aborted}, one assistant/attempt with llm/aborted and the logged
// invocation settles, the client and the listing server are both gone, and the
// attempt directory is removed.
//
//dsh:test DH-T-LLM-27, DH-T-COPILOTCLI-4
func TestCopilotCancelStopsTheTree(t *testing.T) {
	r := newRig(t, "slow", modeEnv+"=hang")
	h, _ := newSession(t, true)
	p := must(r.runtime(t).Prepare(bg, copilot.RouteID, model, vocab.Controls{}))
	ctx, cancel := context.WithCancel(bg)
	t.Cleanup(cancel)
	s := &sink{h: h}
	var fin vocab.Finish
	var got seen
	for c := range p.Stream(ctx, must(h.NextRequest()), s) {
		switch c := c.(type) {
		case vocab.TextDelta:
			// The stand-in records before it streams: its tree is known now.
			got, _ = r.seen(t)
			cancel()
		case vocab.Finish:
			fin = c
		}
	}
	if got.Client == 0 || got.Server == 0 {
		t.Fatalf("the stand-in did not start its tree: %+v", got)
	}
	if fin.Reason != vocab.FinishAborted || len(s.records) != 1 || s.records[0].Finish.Failure.Code != vocab.CodeAborted {
		t.Fatalf("finish = %+v, settlements %d", fin, len(s.records))
	}
	if inv := s.records[0].Client; inv == nil || inv.SessionID == "" {
		t.Fatalf("the cancelled attempt settled no invocation: %+v", inv)
	}
	wctx, wcancel := context.WithTimeout(bg, 30*time.Second)
	defer wcancel()
	procwait.Until(wctx, t, func() bool { return subprocesstest.Exited(got.Client) && subprocesstest.Exited(got.Server) })
	procwait.Until(wctx, t, func() bool { _, err := os.Stat(got.Cwd); return errors.Is(err, os.ErrNotExist) })
}

// TestCopilotBatchShim is Scenario ".cmd client started by bare name" on the
// adapter: on Windows a copilot.cmd shim shaped like an npm install's, on the
// child's PATH, is found by bare name, probed and run through the checked batch
// opt-in (plan 16.5.1), and the stand-in behind it receives the logged argument
// list unchanged, with and without tools (an empty --available-tools value
// included). Elsewhere there is no batch file to start.
//
//dsh:test DH-T-LLM-24, DH-T-COPILOTCLI-1
func TestCopilotBatchShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch shims exist on Windows only")
	}
	bin := t.TempDir()
	shim := "@echo off\r\n\"" + must(os.Executable()) + "\" %*\r\n"
	if err := os.WriteFile(filepath.Join(bin, "copilot.cmd"), []byte(shim), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tools := range []bool{true, false} {
		r := newRig(t, "text", "PATH="+bin)
		r.cfg.Program = ""
		h, st := newSession(t, tools)
		_, s := attempt(t, r.runtime(t), h)
		if !s.records[0].Succeeded() {
			t.Fatalf("tools %v: attempt = %+v", tools, s.records[0].Finish)
		}
		inv := logged(t, st, h, s.seqs[0])
		got, ok := r.seen(t)
		if !ok || !strings.EqualFold(inv.Program, "copilot.cmd") || r.probes() != 1 {
			t.Fatalf("tools %v: the shim did not start the stand-in (ran %v, program %q, probes %d)", tools, ok, inv.Program, r.probes())
		}
		if !slices.Equal(clientroute.Placeholder(got.Args, got.Cwd), inv.Args) {
			t.Fatalf("tools %v: received args %q, logged %q", tools, got.Args, inv.Args)
		}
		if tools && !slices.Equal(got.Tools, []string{"probe"}) {
			t.Fatalf("the listing server behind the shim listed %v", got.Tools)
		}
	}
}

// TestCopilotRoute: the route is copilot-cli with its catalogue and the
// default retry policy; an extra variable that the route removes or that
// overrides its settings is refused; a call for another route is llm/no-route.
//
//dsh:test DH-T-LLM-24
func TestCopilotRoute(t *testing.T) {
	route := must(copilot.Route(copilot.Config{}))
	if route.ID != copilot.RouteID || len(route.Models) != len(copilot.Models) || route.Models[0].Name != model || route.Retry != llm.DefaultRetry() {
		t.Fatalf("route = %+v", route)
	}
	for _, kv := range []string{"GH_TOKEN=x", "COPILOT_GITHUB_TOKEN=x", "COPILOT_PROVIDER_ANY=x", "COPILOT_AUTO_UPDATE=true", "COPILOT_MODEL=x", "DSH_X=1", "nope"} {
		if _, err := copilot.Route(copilot.Config{Env: []string{kv}}); err == nil {
			t.Errorf("extra %q was accepted", kv)
		}
	}
	custom := must(copilot.Route(copilot.Config{Models: []llm.Model{{Name: "gpt-5.1"}}, MaxConcurrent: 2, Retry: llm.PersistentRetry()}))
	if custom.Models[0].Name != "gpt-5.1" || custom.MaxConcurrent != 2 || custom.Retry != llm.PersistentRetry() {
		t.Fatalf("custom route = %+v", custom)
	}
	if _, err := route.Adapter.Prepare(bg, "other", model, vocab.CallConfig{}); err == nil {
		t.Fatal("a call for another route was prepared")
	}
	r := newRig(t, "text")
	if _, err := r.runtime(t).Prepare(bg, copilot.RouteID, "gpt-5", vocab.Controls{Effort: "max"}); err == nil {
		t.Fatal("an effort the client does not take was accepted")
	}
	if _, err := r.runtime(t).Prepare(bg, copilot.RouteID, "gpt-5", vocab.Controls{Effort: "high"}); err != nil {
		t.Fatalf("an effort the client takes was refused: %v", err)
	}
}
