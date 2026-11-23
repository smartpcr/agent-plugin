//go:build manual

// The DH-S-9 manual check (gap-closure plan 16.5.6 and the "DH-S-9 manual-check
// runbook" of Stage 16.5; DH-E-CLAUDECODECLI-1, DH-E-COPILOTCLI-1). It streams
// one request through each signed-in client route, signed in and signed out,
// through the user's own installed and signed-in Claude Code and GitHub Copilot
// CLI. It is `manual`, never a gate criterion: the build tag keeps it out of
// every default and gate run, every case skips unless DSH_MANUAL_SIGNEDIN=1, and
// only the owner or operator runs it, on their own workstation, because the
// sign-in must be their own. No key is typed, read or set: the routes remove
// every credential variable from the client's environment.
//
// The operator's procedure is docs/stories/harness-DSH-M1/evidence/DH-S-9-manual-runbook.md:
//
//	$env:DSH_MANUAL_SIGNEDIN='1'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run TestManualSignedIn ./internal/llm/
//
// Each case prints one line, "DH-S-9 <client> <case>: ...", masked through
// secret.Store.Mask and the token-shape filter (signedin_mask_test.go), and
// copied into the runbook's table.

package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/claudecode"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/copilot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/retry"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

// The operator's switches. DSH_* names never reach a client (BuildEnv).
const (
	manualEnv = "DSH_MANUAL_SIGNEDIN" // "1" runs the cases
	// manualCommitEnv names the harness commit; unset, it is read from .git.
	manualCommitEnv = "DSH_MANUAL_COMMIT"
	// manualHomeOnlyEnv "1" points only the client's own configuration
	// variable at the empty home in the signed-out case, not HOME and
	// USERPROFILE too (for a client whose installed build lives under the home).
	manualHomeOnlyEnv = "DSH_MANUAL_HOME_ONLY"
	// manualAttempts bounds the cases' driver loop, as the driver's
	// MaxAttemptsPerStep does.
	manualAttempts = 3
)

// TestMain lets the test binary serve the listing-only MCP server the clients
// start from the attempt's MCP configuration, exactly as cmd/dsh does.
func TestMain(m *testing.M) {
	if code, ok := listing.ServeIfAsked(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// observed is what the tap saw of the client's raw lines: only the fields the
// runbook's open inferences need, never a line itself.
type observed struct {
	mu            sync.Mutex
	lines         int
	first, last   string   // the first and last line types
	keySource     string   // Claude: init apiKeySource ("absent" when missing)
	initTools     []string // Claude: init tools
	messageStarts int      // Claude: stream_event message_start lines (a second one is a second model request)
	numTurns      string   // Claude: result num_turns
}

func (o *observed) tap(claude bool) func([]byte) {
	return func(line []byte) {
		var l struct {
			Type      string          `json:"type"`
			Subtype   string          `json:"subtype"`
			KeySource *string         `json:"apiKeySource"`
			Tools     []string        `json:"tools"`
			Event     json.RawMessage `json:"event"`
			NumTurns  *int            `json:"num_turns"`
		}
		if len(bytes.TrimSpace(line)) == 0 {
			return
		}
		o.mu.Lock()
		defer o.mu.Unlock()
		o.lines++
		typ := "(not JSON)"
		if json.Unmarshal(line, &l) == nil {
			typ = l.Type
			if l.Subtype != "" {
				typ += "/" + l.Subtype
			}
		}
		if o.first == "" {
			o.first = typ
		}
		o.last = typ
		if !claude {
			return
		}
		switch {
		case l.Type == "system" && l.Subtype == "init":
			o.keySource = "absent"
			if l.KeySource != nil {
				o.keySource = *l.KeySource
			}
			o.initTools = l.Tools
		case l.Type == "stream_event":
			var e struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(l.Event, &e) == nil && e.Type == "message_start" {
				o.messageStarts++
			}
		case l.Type == "result" && l.NumTurns != nil:
			o.numTurns = fmt.Sprint(*l.NumTurns)
		}
	}
}

// extras are the per-client fields of the line.
func (o *observed) extras(claude bool) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if claude {
		tools := "[" + strings.Join(o.initTools, ",") + "]"
		return fmt.Sprintf("api-key-source=%s init-tools=%s message-starts=%d num-turns=%s lines=%d",
			orDash(o.keySource), tools, o.messageStarts, orDash(o.numTurns), o.lines)
	}
	return fmt.Sprintf("first-event=%s last-event=%s lines=%d", orDash(o.first), orDash(o.last), o.lines)
}

// manualClient is one signed-in client route under the check.
type manualClient struct {
	label    string // the line's <client>, as the DH-S-9 register names it
	name     string
	route    vocab.RouteID
	model    string // the default model; modelEnv overrides it
	modelEnv string
	// homeVar is the client's configuration home (the lib record's sec. 5.3
	// "kept" variable that says where the sign-in lives).
	homeVar string
	claude  bool
	build   func(model string, env []string, secrets *secret.Store, tap func([]byte)) (llm.Route, error)
}

var manualClients = []manualClient{
	{label: "claude", name: claudecode.Client, route: claudecode.RouteID, model: "sonnet", modelEnv: "DSH_MANUAL_CLAUDE_MODEL", homeVar: "CLAUDE_CONFIG_DIR", claude: true,
		build: func(model string, env []string, secrets *secret.Store, tap func([]byte)) (llm.Route, error) {
			return claudecode.Route(claudecode.Config{Models: []llm.Model{{Name: model}}, Env: env, Secrets: secrets, Tap: tap})
		}},
	{label: "copilot", name: copilot.Client, route: copilot.RouteID, model: copilot.Models[0].Name, modelEnv: "DSH_MANUAL_COPILOT_MODEL", homeVar: "COPILOT_HOME",
		build: func(model string, env []string, secrets *secret.Store, tap func([]byte)) (llm.Route, error) {
			return copilot.Route(copilot.Config{Models: []llm.Model{{Name: model}}, Env: env, Secrets: secrets, Tap: tap})
		}},
}

// manualTool is the one test tool the signed-in request declares, so the
// listing server, the denial and the stop after one turn are exercised too.
var manualTool = vocab.ToolSchema{Name: "probe", Description: "Echo a text back.",
	Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}

// TestManualSignedIn is the four DH-S-9 cases: each client, signed in and
// signed out. Each streams through Prepared.Stream with the retry component
// mounted on the failed-attempt point and a second registered route that must
// stay untouched, and prints one masked DH-S-9 line.
func TestManualSignedIn(t *testing.T) {
	for _, c := range manualClients {
		for _, signedIn := range []bool{true, false} {
			name := "signed-out"
			if signedIn {
				name = "signed-in"
			}
			t.Run(c.label+"/"+name, func(t *testing.T) {
				if os.Getenv(manualEnv) != "1" {
					t.Skipf("manual: set %s=1, with %s installed and signed in as yourself (DH-S-9 runbook)", manualEnv, c.name)
				}
				manualCase(t, c, name, signedIn)
			})
		}
	}
}

func manualCase(t *testing.T, c manualClient, name string, signedIn bool) {
	store := secret.NewStore(secret.EnvSource(os.LookupEnv))
	model := c.model
	if v := os.Getenv(c.modelEnv); v != "" {
		model = v
	}
	var env []string
	home := "own"
	if !signedIn {
		dir := t.TempDir() // empty: no sign-in, no settings
		vars := []string{c.homeVar}
		if os.Getenv(manualHomeOnlyEnv) != "1" {
			vars = append(vars, "HOME", "USERPROFILE")
		}
		for _, v := range vars {
			env = append(env, v+"="+dir)
		}
		home = "empty:" + strings.Join(vars, "+")
	}
	obs := &observed{}
	route, err := c.build(model, env, store, obs.tap(c.claude))
	if err != nil {
		t.Fatal(err)
	}
	root, h := manualSession(t, c.route, signedIn)
	rt := llm.New()
	must(rt.Register(c.name, route))
	other := &fake{scripts: []script{chunks(append(text("from elsewhere"), vocab.Finish{Reason: vocab.FinishStop})...)}}
	must(rt.Register("other", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: other, Retry: llm.DefaultRetry()}))
	comp := must(retry.Listen(root, retry.Config{Record: func(ctx context.Context, _ agent.AttemptFailure, d event.Draft) error {
		_, err := h.Append(ctx, d)
		return err
	}}))
	t.Cleanup(comp.Close)

	ctx, cancel := context.WithTimeout(bg, 5*time.Minute)
	defer cancel()
	sink := &recordingSink{h: h}
	prepared := "ok"
	started := time.Now()
	// The driver's part (M1-D-PIPE-2 A): stream, and on a failed attempt ask
	// the failed-attempt point; on Retry wait After and send the wait's outcome.
	for n := 1; n <= manualAttempts; n++ {
		p, err := rt.Prepare(ctx, c.route, model, vocab.Controls{})
		if err != nil {
			prepared = "failed:" + string(codeOf(err))
			break
		}
		before := len(sink.records)
		for range p.Stream(ctx, must(h.NextRequest()), sink) {
		}
		if len(sink.records) == before {
			t.Fatal("the attempt settled nothing")
		}
		a := sink.records[len(sink.records)-1]
		if a.Succeeded() {
			break
		}
		f := agent.AttemptFailure{Ctx: ctx, Agent: agent.Ref{Session: h.ID(), Incarnation: 1}, Turn: 1, Step: 1, Attempt: n,
			Failure: a.Finish.Failure, Policy: agent.RetryPolicy{MaxAttempts: manualAttempts, BaseDelay: time.Second, MaxDelay: 8 * time.Second},
			AttemptsLeft: manualAttempts - n, StepStarted: started, RoutePolicy: p.Retry()}
		d, err := agent.AttemptFailed.Around(root, f, func(agent.AttemptFailure) (agent.AttemptDecision, error) { return agent.GiveUp{}, nil })
		if err != nil {
			t.Fatalf("the failed-attempt point: %v", err)
		}
		r, ok := d.(agent.Retry)
		if !ok {
			break
		}
		timer := time.NewTimer(r.After)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
		agent.RetryWaited.Notify(root, scope.Key{}, agent.RetryWaitEnded{Agent: f.Agent, Turn: 1, Step: 1, Attempt: n, Cancelled: ctx.Err() != nil})
		if ctx.Err() != nil {
			break
		}
	}

	// The record.
	retries, abandoned := 0, "-"
	for _, ev := range must(h.Read(bg, 0, 0)) {
		switch ev.Type() {
		case retry.ScheduledType.Name():
			retries++
		case retry.AbandonedType.Name():
			if ab, err := retry.AbandonedType.As(ev); err == nil {
				abandoned = string(ab.Reason)
			}
		}
	}
	seq, typ, finish, code, class, usage := "-", "-", "-", "-", "-", "-"
	version, program, reported, sid, clientRetries, denials, calls := "-", "-", "-", "-", 0, 0, 0
	var last *llm.Attempt
	if n := len(sink.records); n > 0 {
		last = &sink.records[n-1]
		seq, typ, finish = fmt.Sprint(sink.seqs[n-1]), must(last.Draft()).Type(), string(last.Finish.Reason)
		if f := last.Finish.Failure; f != nil {
			code, class = string(f.Code), string(f.Class)
		}
		if last.Usage != nil {
			usage = fmt.Sprintf("in:%d,out:%d,cache-read:%d,cache-write:%d", last.Usage.Input, last.Usage.Output, last.Usage.CacheRead, last.Usage.CacheWrite)
		}
		if last.Message != nil {
			for _, b := range last.Message.Blocks() {
				if b.Kind == vocab.KindToolUse {
					calls++
				}
			}
		}
		if inv := last.Client; inv != nil {
			version, program, reported, sid = inv.Version, inv.Program, orDash(inv.Model), maskID(store, inv.SessionID)
			clientRetries, denials = inv.Retries, inv.Denials
		}
	}
	line := fmt.Sprintf("DH-S-9 %s %s: commit=%s host=%s client=%s version=%s program=%s route=%s model=%s reported-model=%s home=%s prepare=%s "+
		"settlements=%d seq=%s type=%s finish=%s code=%s class=%s llm-retry=%d retry-abandoned=%s other-route-prepares=%d other-route-attempts=%d "+
		"session=%s client-retries=%d denials=%d tool-calls=%d usage=%s %s",
		c.label, name, manualCommit(), manualHost(), c.name, version, program, c.route, model, reported, home, prepared,
		len(sink.records), seq, typ, finish, code, class, retries, abandoned, other.prepares.Load(), other.attempts.Load(),
		sid, clientRetries, denials, calls, usage, obs.extras(c.claude))
	fmt.Println(redact(store, line))

	// The pass conditions of the runbook.
	if other.prepares.Load() != 0 || other.attempts.Load() != 0 {
		t.Error("another route was prepared or attempted")
	}
	if last == nil {
		t.Fatalf("no attempt settled (prepare %s)", prepared)
	}
	if len(sink.records) != 1 || retries != 0 {
		t.Errorf("settlements %d, llm/retry %d; want exactly one settlement and no retry", len(sink.records), retries)
	}
	if signedIn {
		if typ != "assistant/message" {
			t.Errorf("signed in settled %s (%s), want assistant/message", typ, code)
		}
		return
	}
	if typ != "assistant/attempt" || class != string(vocab.Permanent) || abandoned != string(llm.AbandonNotRetryable) {
		t.Errorf("signed out settled %s %s %s (abandoned %s), want one Permanent assistant/attempt, abandoned not-retryable", typ, code, class, abandoned)
	}
	if code != string(vocab.CodeSignInRequired) {
		if c.claude {
			t.Errorf("signed out = %s, want llm/sign-in-required", code)
		} else {
			// github-copilot-cli.md sec. 5.4: until this run pins the signed-out
			// line, the Copilot route settles it from the exit status.
			t.Logf("open (DH-E-COPILOTCLI-1): signed out settled %s, not llm/sign-in-required; record it", code)
		}
	}
}

// manualSession is a session on route with a system and a user message; the
// signed-in request declares the test tool and asks for one call to it.
func manualSession(t *testing.T, route vocab.RouteID, withTool bool) (*cordis.Context, session.Handle) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	h := must(session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	hdr := event.RequestHeaderPayload{Route: route}
	ask := "Reply with the single word: pong"
	if withTool {
		hdr.Tools = []vocab.ToolSchema{manualTool}
		ask = `Call the probe tool once with {"text":"ping"}. If you cannot call it, reply with the single word: pong`
	}
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(hdr)),
		must(event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: must(vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock("Be brief.")))})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock(ask)))})),
	}))
	return root, h
}

func codeOf(err error) vocab.Code {
	var f *vocab.Failure
	if errors.As(err, &f) {
		return f.Code
	}
	return "unclassified"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// manualHost is the system the check ran on: windows, linux, or linux/wsl.
func manualHost() string {
	if runtime.GOOS == "linux" && os.Getenv("WSL_DISTRO_NAME") != "" {
		return "linux/wsl"
	}
	return runtime.GOOS
}

// manualCommit is DSH_MANUAL_COMMIT, else the checkout's HEAD read from .git.
func manualCommit() string {
	if c := os.Getenv(manualCommitEnv); c != "" {
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
			return headOf(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "unknown"
}

func headOf(gitDir string) string {
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "unknown"
	}
	head := strings.TrimSpace(string(raw))
	ref, ok := strings.CutPrefix(head, "ref: ")
	if !ok {
		return head
	}
	if b, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b))
	}
	// A packed ref, or a worktree's ref kept in the common directory.
	for _, dir := range []string{gitDir, filepath.Join(gitDir, "..", "..")} {
		packed, _ := os.ReadFile(filepath.Join(dir, "packed-refs"))
		for _, l := range strings.Split(string(packed), "\n") {
			if sha, name, ok := strings.Cut(strings.TrimSpace(l), " "); ok && name == ref {
				return sha
			}
		}
		if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return "unknown (" + ref + ")"
}
