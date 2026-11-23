//go:build manual

// The M1 manual check (impl.md DH-S-11, `manual`; tech-spec EV-6;
// gap-closure plan Stage 17.4): flow F2's composition of the real components --
// session, inbox, model selection, the guarded tool pipeline with one approval,
// the driver -- on a real model instead of the scripted one. Under OWNER-2
// (decisions.md models-signed-in-claude-copilot) that model is reached only
// through the user's own installed and signed-in Claude Code or GitHub Copilot
// CLI, which the signed-in client routes of plan Stage 16.5 start as a child
// process (M1-D-PIPE-3 A). No key is typed, read or set: the routes remove every
// credential variable from the client's environment, so the client's own
// sign-in is what is used. Run one sends one prompt with one test tool and
// records the bracketed log and every request rebuilt from it, at the
// client-invocation boundary where SP-1 holds on these routes (M1-D-PIPE-4 A,
// M1-D-CLOSE-3 A); run two cancels a second turn mid-stream and records the
// cause on its turn/end. It is not a scenario and not a gate criterion: the
// build tag keeps it out of every default and gate run.
//
// DSH_M1_ROUTE selects the route: "claude" (claude-code) or "copilot"
// (copilot-cli). Claude must pass; Copilot is run and recorded either way
// (M1-D-LOOP-6 A). The operator's procedure is
// docs/stories/harness-DSH-M1/evidence/DH-S-11-manual-runbook.md:
//
//	$env:DSH_M1_ROUTE='claude'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
//
// Both tests skip, sending nothing, when DSH_M1_ROUTE is unset or when the
// selected client is not installed at or above its version floor: the skip
// check locates the program on the child's PATH and runs its version probe,
// never a prompt. A client that is installed but signed out is not skipped: its
// run fails with llm/sign-in-required, which is the record.
//
// Every line printed passes m1Redact (m1_redact_test.go; plan 17.4.2), which
// masks the signed-in account name and email, credential headers, token shapes
// and token file paths, and fails the test, withholding the line, if a pattern
// survives.

package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/claudecode"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/copilot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/redact"
)

// The operator's switches. No DSH_* name reaches a client (subprocess.BuildEnv).
const (
	m1RouteEnv = "DSH_M1_ROUTE" // "claude" or "copilot"
	// m1ModelEnv overrides the route's default model.
	m1ModelEnv = "DSH_M1_MODEL"
	// m1CommitEnv names the harness commit; unset, it is read from .git.
	m1CommitEnv = "DSH_M1_COMMIT"
	// m1AccountEnv is the signed-in account names and emails to mask, comma
	// separated (the client's own account name, which the harness never reads).
	m1AccountEnv = "DSH_M1_ACCOUNT"
)

const (
	m1Prompt = "Call the probe tool exactly once with the text \"ping\", then reply with what it returned."
	m1Long   = "Count slowly from 1 to 200, one number per line, with a short sentence about each number."
)

// TestMain lets the test binary serve the listing-only MCP server the clients
// start from the attempt's MCP configuration, exactly as cmd/dsh does.
func TestMain(m *testing.M) {
	if code, ok := listing.ServeIfAsked(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// m1Client is one signed-in client route the check can run on, named by its
// DSH_M1_ROUTE value; everything route-specific is here.
type m1Client struct {
	key     string
	id      vocab.RouteID
	name    string
	program string
	model   string
	probe   []string
	pattern *regexp.Regexp
	floor   clientroute.Version
	env     func() clientroute.EnvSpec
	// systemInPrompt is the route's rendering: Copilot cannot replace its own
	// system prompt, so the harness's leads standard input.
	systemInPrompt bool
	route          func(model string) (llm.Route, error)
	// args is the argument list the route starts for one attempt, rebuilt from
	// the logged client session id and the rendered tool names.
	args func(model, sessionID string, tools []string) []string
}

var m1Clients = []m1Client{
	{
		key: "claude", id: claudecode.RouteID, name: claudecode.Client, program: claudecode.Program, model: "sonnet",
		probe: []string{"--version"}, pattern: claudecode.VersionPattern, floor: claudecode.Floor, env: claudecode.Env,
		route: func(model string) (llm.Route, error) {
			return claudecode.Route(claudecode.Config{Models: []llm.Model{{Name: model}}})
		},
		args: func(model, sid string, tools []string) []string {
			return claudecode.Args(claudecode.ArgsSpec{Dir: clientroute.AttemptPlaceholder, Model: model, SessionID: sid, Tools: len(tools) > 0})
		},
	},
	{
		key: "copilot", id: copilot.RouteID, name: copilot.Client, program: copilot.Program, model: copilot.Models[0].Name,
		probe: copilot.ProbeArgs, pattern: copilot.VersionPattern, floor: copilot.Floor, env: copilot.Env, systemInPrompt: true,
		route: func(model string) (llm.Route, error) {
			return copilot.Route(copilot.Config{Models: []llm.Model{{Name: model}}})
		},
		args: func(model, sid string, tools []string) []string {
			return copilot.Args(copilot.ArgsSpec{Dir: clientroute.AttemptPlaceholder, Model: model, SessionID: sid, Tools: tools})
		},
	},
}

// m1Run is the selected route, its model, the client found and the filter
// every printed line passes.
type m1Run struct {
	c       m1Client
	model   string
	program string
	version clientroute.Version
	filter  *redact.Filter
}

// logf prints one record through m1Redact (plan 17.4.2).
func (m *m1Run) logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Log(m1Redact(t, m.filter, fmt.Sprintf(format, args...)))
}

// m1Select is the route DSH_M1_ROUTE names, with its client found. It skips,
// sending nothing, when the variable is unset or the client is missing or below
// its floor: it locates the program on the child's PATH and runs the route's
// version probe, never a prompt.
func m1Select(t *testing.T) *m1Run {
	t.Helper()
	f := m1Filter(m1Identity()...)
	key := strings.TrimSpace(os.Getenv(m1RouteEnv))
	if key == "" {
		t.Skipf("manual: set %s=claude or %s=copilot, with that client installed and signed in as yourself (DH-S-11 runbook)", m1RouteEnv, m1RouteEnv)
	}
	var c *m1Client
	for i := range m1Clients {
		if m1Clients[i].key == key {
			c = &m1Clients[i]
		}
	}
	if c == nil {
		t.Fatalf("%s=%q: want claude or copilot", m1RouteEnv, key)
	}
	model := c.model
	if v := strings.TrimSpace(os.Getenv(m1ModelEnv)); v != "" {
		model = v
	}
	env, err := c.env().Build(os.Environ())
	if err != nil {
		t.Fatalf("the %s environment: %v", c.name, err)
	}
	prog, err := clientroute.Locate(c.name, "", c.program, env)
	if err != nil {
		t.Skip(m1Redact(t, f, fmt.Sprintf("manual: no %s client found on the PATH (%v); install it and sign in (DH-S-11 runbook)", c.name, err)))
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	v, err := clientroute.Probe(ctx, clientroute.ProbeSpec{
		Client: c.name, Program: prog, Args: c.probe, Env: env, Pattern: c.pattern, Floor: c.floor,
	})
	if err != nil {
		t.Skip(m1Redact(t, f, fmt.Sprintf("manual: the %s client cannot be used (%v); install %s or later (DH-S-11 runbook)", c.name, err, c.floor)))
	}
	return &m1Run{c: *c, model: model, program: filepath.Base(prog.Path), version: v, filter: f}
}

// m1Identity are the identity values m1Redact masks besides the patterns: the
// operator's account names (DSH_M1_ACCOUNT) and this machine's user name, which
// a home path would carry.
func m1Identity() []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(m1AccountEnv), ",") {
		out = append(out, strings.TrimSpace(v))
	}
	for _, name := range []string{"USERNAME", "USER", "LOGNAME"} {
		out = append(out, os.Getenv(name))
	}
	return out
}

// header prints the run's identity: the commit, the route and client, the
// model, the Go version and the operating system.
func (m *m1Run) header(t *testing.T, run string) {
	t.Helper()
	m.logf(t, "M1 header: run=%s commit=%s route=%s client=%s version=%s program=%s model=%s go=%s os=%s/%s host=%s",
		run, m1Commit(), m.c.id, m.c.name, m.version, m.program, m.model, runtime.Version(), runtime.GOOS, runtime.GOARCH, m1Host())
}

// start composes flow F2 on st with the selected route registered and
// selected; the one call site of f2Start in this file.
func (m *m1Run) start(t *testing.T, st store.Store, h f2Hooks) *f2Rig {
	t.Helper()
	return f2Start(t, st, h, func(rt *llm.Runtime) agent.ModelSelection {
		must(rt.Register(m.c.name, must(m.c.route(m.model))))
		return agent.ModelSelection{Route: m.c.id, Model: m.model}
	})
}

func m1Wait(t *testing.T, r *f2Rig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 3*time.Minute)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
}

func m1TurnEnds(t *testing.T, evs []event.Event) []event.TurnEnd {
	t.Helper()
	var out []event.TurnEnd
	for _, e := range evs {
		if e.Type() == "turn/end" {
			var te event.TurnEnd
			must(0, json.Unmarshal([]byte(e.Raw()), &te))
			out = append(out, te)
		}
	}
	return out
}

func m1Decode[T any](t *testing.T, e event.Event) T {
	t.Helper()
	return must(f2Decode[T](e))
}

// m1Settled is one settlement's request cut and its client invocation.
type m1Settled struct {
	seq    event.Seq
	asOf   *event.Seq
	digest string
	client *event.ClientInvocation
}

// m1Settlements are the settlements of evs in log order.
func m1Settlements(t *testing.T, evs []event.Event) []m1Settled {
	t.Helper()
	var out []m1Settled
	for _, e := range evs {
		switch e.Type() {
		case "assistant/message":
			p := m1Decode[event.AssistantMessagePayload](t, e)
			out = append(out, m1Settled{seq: e.Seq(), asOf: p.RequestAsOf, digest: p.RequestDigest, client: p.Client})
		case "assistant/attempt":
			p := m1Decode[event.AssistantAttemptPayload](t, e)
			out = append(out, m1Settled{seq: e.Seq(), asOf: p.RequestAsOf, digest: p.RequestDigest, client: p.Client})
		}
	}
	return out
}

// m1Invocation is the settled client invocation of s rebuilt from the log: the
// argument list the route starts for the request rebuilt at s's requestAsOf
// (with the logged client session id), its digest, and the digest of that
// request rendered as standard input. It reports whether both digests equal
// the logged ones.
func (m *m1Run) invocation(t *testing.T, b surface.Built, s m1Settled) (args, stdin string, equal bool) {
	t.Helper()
	if s.client == nil {
		return "-", "-", false
	}
	rr, err := clientroute.Render(b, clientroute.RenderOptions{SystemInPrompt: m.c.systemInPrompt})
	if err != nil {
		t.Errorf("rendering the rebuilt request: %v", err)
		return "-", "-", false
	}
	args = clientroute.ArgsDigest(m.c.args(m.model, s.client.SessionID, rr.ToolNames))
	stdin = clientroute.Digest(rr.Prompt)
	return args, stdin, args == s.client.ArgsDigest && stdin == s.client.StdinDigest
}

// Run one: one prompt, one tool call with one approval, the bracketed log and
// every sent request rebuilt from the stored log at its requestAsOf, compared
// with what was sent and with the client invocation it settled.
func TestManualM1OneTurn(t *testing.T) {
	m := m1Select(t)
	m.header(t, "one-turn")
	st := memstore.New()
	r := m.start(t, st, f2Hooks{wallClock: true})
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: m1Prompt}))
	m1Wait(t, r)

	evs := must(r.sess.Read(bg, 0, 0))
	m.logf(t, "M1 bracketed log (%d events, ids and digests numbered):\n%s", len(evs), f2Serialize(evs))
	if b := r.sess.Open(); b.IsOpen() {
		t.Fatalf("brackets still open: %+v", b)
	}
	if asked := r.ans.Asked(); len(asked) != 1 {
		t.Errorf("the answerer was asked %d times, want 1 (did the model call the tool?)", len(asked))
	}
	rd := must(st.OpenRead(bg, f2Session))
	defer func() { _ = rd.Close(bg) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	sent, settled := r.models.requests(), m1Settlements(t, evs)
	if len(settled) != len(sent) {
		t.Errorf("%d requests sent, %d settlements: each attempt settles once", len(sent), len(settled))
	}
	for i, req := range sent {
		b := must(surface.Rebuild(bg, event.Default(), read, f2Session, req.AsOf()))
		msgs := must(json.Marshal(must(b.Messages())))
		var s m1Settled
		if i < len(settled) {
			s = settled[i]
		}
		args, stdin, invEqual := m.invocation(t, b, s)
		loggedArgs, loggedStdin, session := "-", "-", "-"
		if s.client != nil {
			loggedArgs, loggedStdin, session = s.client.ArgsDigest, s.client.StdinDigest, m1Short(s.client.SessionID)
		}
		m.logf(t, "M1 request %d: asOf=%d sent=%s rebuilt=%s equal=%t settled-seq=%d client=%s args-logged=%s args-rebuilt=%s stdin-logged=%s stdin-rebuilt=%s invocation-equal=%t session=%s messages=%s",
			i+1, req.AsOf(), req.Digest(), b.Digest(), b.Digest() == req.Digest(), s.seq, m.c.name,
			loggedArgs, args, loggedStdin, stdin, invEqual, session, msgs)
		if b.Digest() != req.Digest() {
			t.Errorf("request %d: rebuilt digest %s, sent %s", i+1, b.Digest(), req.Digest())
		}
		switch {
		case s.client == nil:
			t.Errorf("request %d: its settlement records no client invocation", i+1)
		case s.client.Client != m.c.name || s.asOf == nil || *s.asOf != req.AsOf() || s.digest != req.Digest():
			t.Errorf("request %d: settlement %d is of client %s at requestAsOf %v digest %s, want %s at %d digest %s",
				i+1, s.seq, s.client.Client, s.asOf, s.digest, m.c.name, req.AsOf(), req.Digest())
		case !invEqual:
			t.Errorf("request %d: the invocation rebuilt from the log differs from the logged one", i+1)
		}
	}
	for _, te := range m1TurnEnds(t, evs) {
		m.logf(t, "M1 turn/end: turn=%d reason=%+v", te.Turn, te.Reason)
	}
	// M1 is evidence only for a turn that ran to its end; the oracle is the one
	// TestM1CompletionOracleOnTheF2Turn checks keylessly.
	reply, err := f2Completed(evs, f2Answer)
	if err != nil {
		t.Fatal(m1Redact(t, m.filter, fmt.Sprintf("M1 turn not completed successfully: %v", err)))
	}
	m.logf(t, "M1 final reply: %q", reply)
}

// Run two: a turn whose reply streams long is cancelled by the user once answer
// text is streaming; the turn ends aborted{user}, the step's settlement records
// the cancel and its client invocation, and the brackets close.
func TestManualM1CancelMidStream(t *testing.T) {
	m := m1Select(t)
	m.header(t, "cancel")
	var once sync.Once
	first := make(chan struct{})
	// Only a nonempty answer delta proves the reply is streaming: a BlockStart,
	// a usage chunk or an error Finish must not trigger the cancel.
	hooks := f2Hooks{wallClock: true, noApproval: true, chunk: func(_ int, c vocab.Chunk) {
		if d, ok := c.(vocab.TextDelta); ok && d.Text != "" {
			once.Do(func() { close(first) })
		}
	}}
	r := m.start(t, memstore.New(), hooks)
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: m1Long}))
	bound := time.NewTimer(2 * time.Minute)
	defer bound.Stop()
	select {
	case <-first:
	case <-bound.C:
		t.Fatal("no answer text streamed within 2m")
	}
	if err := r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	m1Wait(t, r)

	evs := must(r.sess.Read(bg, 0, 0))
	m.logf(t, "M1 cancelled log (%d events):\n%s", len(evs), f2Serialize(evs))
	if b := r.sess.Open(); b.IsOpen() {
		t.Fatalf("brackets still open after the cancel: %+v", b)
	}
	ends := m1TurnEnds(t, evs)
	if len(ends) != 1 {
		t.Fatalf("%d turn/end events, want 1", len(ends))
	}
	m.logf(t, "M1 cancel: turn/end reason=%+v", ends[0].Reason)
	if ends[0].Reason.Kind != event.ReasonAborted || ends[0].Reason.Cause != string(agent.CauseUser) {
		t.Fatalf("turn/end reason %+v, want aborted{cause: user}", ends[0].Reason)
	}
	steps := f2OfType(evs, "step/end")
	if len(steps) != 1 || m1Decode[event.StepEnd](t, steps[0]).Cause != string(agent.CauseUser) {
		t.Fatalf("step/end %v, want one step cancelled by the user", len(steps))
	}
	// The cancelled step settles either as an interrupted assistant/message
	// keeping the streamed prefix with no tool call, or as an assistant/attempt
	// whose failure is cancelled -- never as a provider failure. Either records
	// the client invocation that was stopped.
	settled := append(f2OfType(evs, "assistant/message"), f2OfType(evs, "assistant/attempt")...)
	if len(settled) != 1 {
		t.Fatalf("%d settlements, want 1", len(settled))
	}
	var client *event.ClientInvocation
	switch s := settled[0]; s.Type() {
	case "assistant/message":
		p := m1Decode[event.AssistantMessagePayload](t, s)
		if f2Text(p.Message) == "" || f2HasToolUse(p.Message) {
			t.Fatal("the interrupted message holds no streamed prefix, or a tool call")
		}
		client = p.Client
		m.logf(t, "M1 cancel: interrupted prefix kept (%d bytes)", len(f2Text(p.Message)))
	default:
		a := m1Decode[event.AssistantAttemptPayload](t, s)
		if a.Failure == nil || a.Failure.Class != vocab.Cancelled {
			t.Fatalf("the attempt's failure is not of class %q", vocab.Cancelled)
		}
		client = a.Client
		m.logf(t, "M1 cancel: attempt settled cancelled (%s)", a.Failure.Code)
	}
	if client == nil || client.Client != m.c.name {
		t.Fatalf("the cancelled settlement records client invocation %+v, want one of %s", client, m.c.name)
	}
	m.logf(t, "M1 cancel: client=%s version=%s session=%s", client.Client, client.Version, m1Short(client.SessionID))
}

// m1Short keeps the first eight characters of a client session id, so the
// operator can find the client's own record of it.
func m1Short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

// m1Host is the system the check ran on: windows, linux, or linux/wsl.
func m1Host() string {
	if runtime.GOOS == "linux" && os.Getenv("WSL_DISTRO_NAME") != "" {
		return "linux/wsl"
	}
	return runtime.GOOS
}

// m1Commit is DSH_M1_COMMIT, else the checkout's HEAD read from .git (a
// worktree's or a packed ref included), else "unknown".
func m1Commit() string {
	if c := strings.TrimSpace(os.Getenv(m1CommitEnv)); c != "" {
		return c
	}
	dir, err := os.Getwd()
	for err == nil {
		gitDir := filepath.Join(dir, ".git")
		if fi, serr := os.Stat(gitDir); serr == nil {
			if !fi.IsDir() { // a worktree: "gitdir: <path>"
				raw, _ := os.ReadFile(gitDir)
				gitDir = strings.TrimSpace(strings.TrimPrefix(string(raw), "gitdir:"))
			}
			return m1Head(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "unknown"
}

func m1Head(gitDir string) string {
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "unknown"
	}
	head := strings.TrimSpace(string(raw))
	ref, ok := strings.CutPrefix(head, "ref: ")
	if !ok {
		return head
	}
	for _, dir := range []string{gitDir, filepath.Join(gitDir, "..", "..")} {
		if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(b))
		}
		packed, _ := os.ReadFile(filepath.Join(dir, "packed-refs"))
		for _, l := range strings.Split(string(packed), "\n") {
			if sha, name, ok := strings.Cut(strings.TrimSpace(l), " "); ok && name == ref {
				return sha
			}
		}
	}
	return "unknown (" + ref + ")"
}
