//go:build e2e

// Stage 7.4 (DH-S-17): DH-E-LLM-1, LLM-3 and VIEW-1 on both client routes, and
// DH-E-TOOLS-1 and DH-E-LOADER-1 on claude-code (TC-20s).
//
// Each route is the real route package (claudecode, copilot) with its client
// program set to this test binary, re-executed as a stand-in client (s74StandIn).
// The stand-in probes, starts the route's listing MCP server from the
// configuration the route wrote, and replays the route's committed synthetic
// client lines. Sessions are kept in a scratch SQLite store. With the S17
// machine's DSH_SCENARIO_* inputs set, the real scenario tests are also run
// against the signed-in clients (s74Live).
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/claudecode"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/copilot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/titlemodel"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/title"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// The stand-in's settings, passed through the route's Config.Env (no DSH_ name,
// which the route removes).
const (
	s74RoleEnv     = "S74_ROLE"     // "claude" or "copilot"
	s74RecordEnv   = "S74_RECORD"   // where it counts probes and runs
	s74FixturesEnv = "S74_FIXTURES" // the lines of run n are fixture n (the last one repeats)
	s74ModeEnv     = "S74_MODE"     // "hang": stall after the lines until the tree is stopped
	s74ExitEnv     = "S74_EXIT"     // its exit status

	s74Wait       = 30 * time.Second
	s74LiveWithin = 30 * time.Minute
	s74Legs       = 8
)

var (
	s74OuterT *testing.T
	s74Bg     = context.Background()
	s74Routes = map[string]vocab.RouteID{"claude-code": claudecode.RouteID, "copilot-cli": copilot.RouteID}
	// s74LiveInputs are the inputs each real scenario test reads on the S17 machine.
	s74LiveInputs = map[string][]string{
		"TestTurnWithReasoningAndToolCall":          {"DSH_SCENARIO_LLM1_LAYER", "DSH_SCENARIO_LLM1_PROMPT", "DSH_SCENARIO_LLM1_PROMPT2", "DSH_SCENARIO_LLM1_USAGE"},
		"TestSignedOutIsAFact":                      {"DSH_SCENARIO_LLM3_LAYER", "DSH_SCENARIO_LLM3_PROMPT", "DSH_SCENARIO_LLM3_MAX_TOKENS"},
		"TestModelTitlesSession":                    {"DSH_SCENARIO_VIEW1_LAYER", "DSH_SCENARIO_VIEW1_BAD_LAYER", "DSH_SCENARIO_VIEW1_PROMPT"},
		"TestGuardedPipelineRefusesTheWrite":        {"DSH_SCENARIO_TOOLS1_LAYER", "DSH_SCENARIO_TOOLS1_PROMPT", "DSH_SCENARIO_TOOLS1_REPLAY"},
		"TestRunningHarnessFollowsProfilePatchEdit": {"DSH_SCENARIO_LOADER_PROFILE", "DSH_SCENARIO_LOADER_START", "DSH_SCENARIO_LOADER_INSPECT", "DSH_SCENARIO_LOADER_DISABLE", "DSH_SCENARIO_LOADER_STOP"},
	}
	s74LiveScenario = map[string]string{
		"TestTurnWithReasoningAndToolCall": "DH-E-LLM-1", "TestSignedOutIsAFact": "DH-E-LLM-3", "TestModelTitlesSession": "DH-E-VIEW-1",
		"TestGuardedPipelineRefusesTheWrite": "DH-E-TOOLS-1", "TestRunningHarnessFollowsProfilePatchEdit": "DH-E-LOADER-1",
	}
)

// init plays the stand-in client, or the listing server it starts (which
// inherits S74_ROLE), when this binary is re-executed by a route.
func init() {
	role := os.Getenv(s74RoleEnv)
	if role == "" {
		return
	}
	if code, ok := listing.ServeIfAsked(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(s74StandIn(role))
}

// s74Count appends one line to the record file name and returns its line count.
func s74Count(name string) int {
	path := filepath.Join(os.Getenv(s74RecordEnv), name)
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	_, _ = f.WriteString("x\n")
	_ = f.Close()
	raw, _ := os.ReadFile(path)
	return strings.Count(string(raw), "\n")
}

func s74StandIn(role string) int {
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		s74Count("probes")
		if role == "copilot" {
			fmt.Println("GitHub Copilot CLI 1.0.115-7.")
			fmt.Println("Run 'copilot update' to check for updates.")
		} else {
			fmt.Println("2.1.312 (Claude Code)")
		}
		return 0
	}
	n := s74Count("runs")
	_, _ = io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	flag := "--mcp-config"
	if role == "copilot" {
		flag = "--additional-mcp-config"
	}
	var srv *exec.Cmd
	var to io.WriteCloser
	var from *bufio.Reader
	tools, servers := []string{}, "[]"
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		if srv, to, from = s74Server(strings.TrimPrefix(args[i+1], "@")); srv != nil {
			tools, servers = s74List(to, from), `[{"name":"dsh","status":"connected"}]`
		}
	}
	sid := ""
	if i := slices.Index(args, "--session-id"); i >= 0 && i+1 < len(args) {
		sid = args[i+1]
	}
	fixtures := filepath.SplitList(os.Getenv(s74FixturesEnv))
	raw, _ := os.ReadFile(fixtures[min(n, len(fixtures))-1])
	toolsJSON, _ := json.Marshal(tools)
	cwdJSON, _ := json.Marshal(cwd)
	_, _ = os.Stdout.WriteString(strings.NewReplacer(`"{{tools}}"`, string(toolsJSON), `"{{mcp_servers}}"`, servers,
		`"{{cwd}}"`, string(cwdJSON), "{{session_id}}", sid, "{{version}}", "2.1.312").Replace(string(raw)))
	if os.Getenv(s74ModeEnv) == "hang" {
		if from != nil {
			_, _ = from.ReadBytes('\n')
		} else {
			time.Sleep(time.Hour)
		}
	}
	if srv != nil {
		_ = to.Close()
		_ = srv.Wait()
	}
	code, _ := strconv.Atoi(os.Getenv(s74ExitEnv))
	return code
}

// s74Server starts the "dsh" stdio server of the MCP configuration at path.
func s74Server(path string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type, Command string
			Args          []string
			Env           map[string]string
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return nil, nil, nil
	}
	s, ok := cfg.MCPServers["dsh"]
	if !ok || s.Type != "stdio" {
		return nil, nil, nil
	}
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if cmd.Start() != nil {
		return nil, nil, nil
	}
	return cmd, in, bufio.NewReader(out)
}

// s74List initializes the listing server and returns its tools as the client
// names them.
func s74List(to io.Writer, from *bufio.Reader) []string {
	call := func(id int, method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		_, _ = to.Write(append(b, '\n'))
		line, _ := from.ReadBytes('\n')
		var resp map[string]any
		_ = json.Unmarshal(line, &resp)
		return resp
	}
	call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "s74"}})
	_, _ = to.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	var names []string
	res, _ := call(2, "tools/list", map[string]any{})["result"].(map[string]any)
	ts, _ := res["tools"].([]any)
	for _, t := range ts {
		if m, ok := t.(map[string]any); ok {
			name, _ := m["name"].(string)
			names = append(names, claudecode.ToolPrefix+name)
		}
	}
	return names
}

// ---- Route rigs ----

// s74Rig is one route whose client is the stand-in, with its record directory.
type s74Rig struct {
	id    vocab.RouteID
	model string
	dir   string
	route llm.Route
}

// s74NewRig is route id playing fixtures (testdata names of the route package,
// or absolute paths) in run order, with the stand-in settings extra.
func s74NewRig(id vocab.RouteID, extra []string, fixtures ...string) (*s74Rig, error) {
	mod, err := s17rModuleDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	pkg, role, model := "claudecode", "claude", "sonnet"
	if id == copilot.RouteID {
		pkg, role, model = "copilot", "copilot", copilot.Models[0].Name
	}
	for i, f := range fixtures {
		if !filepath.IsAbs(f) {
			fixtures[i] = filepath.Join(mod, "internal", "llm", "provider", pkg, "testdata", f+".jsonl")
		}
	}
	r := &s74Rig{id: id, model: model, dir: s74OuterT.TempDir()}
	env := append([]string{s74RoleEnv + "=" + role, s74RecordEnv + "=" + r.dir,
		s74FixturesEnv + "=" + strings.Join(fixtures, string(os.PathListSeparator))}, extra...)
	res := &sandboxtest.Resolver{UnconfinedKinds: []string{string(claudecode.RouteID), string(copilot.RouteID)}}
	if id == copilot.RouteID {
		r.route, err = copilot.Route(copilot.Config{Program: exe, Env: env, TempDir: s74OuterT.TempDir(), Grace: 200 * time.Millisecond, Sandbox: res})
	} else {
		r.route, err = claudecode.Route(claudecode.Config{Program: exe, Env: env, TempDir: s74OuterT.TempDir(), Grace: 200 * time.Millisecond, Sandbox: res})
	}
	return r, err
}

// count is how many times the stand-in was probed ("probes") or run ("runs").
func (r *s74Rig) count(name string) int {
	raw, _ := os.ReadFile(filepath.Join(r.dir, name))
	return strings.Count(string(raw), "\n")
}

func s74Runtime(rigs ...*s74Rig) (*llm.Runtime, error) {
	rt := llm.New()
	for _, r := range rigs {
		if _, err := rt.Register(string(r.id), r.route); err != nil {
			return nil, err
		}
	}
	return rt, nil
}

// s74Root is a quiet cordis root disposed when the suite ends.
func s74Root() *cordis.Context {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s74OuterT.Cleanup(root.Dispose)
	return root
}

// s74Store is a fresh scratch SQLite store.
func s74Store() (*sqlite.Store, error) {
	st, err := sqlite.Open(s74Bg, filepath.Join(s74OuterT.TempDir(), "s74.db"))
	if err == nil {
		s74OuterT.Cleanup(func() { _ = st.Close(s74Bg) })
	}
	return st, err
}

// s74Session opens turn 1 on route with a system and a user message, declaring
// the probe tool when withTool is set.
func s74Session(route vocab.RouteID, withTool bool) (session.Handle, error) {
	st, err := s74Store()
	if err != nil {
		return nil, err
	}
	h, err := session.NewService(s74Root(), st, session.Config{}).Create(s74Bg, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return nil, err
	}
	s74OuterT.Cleanup(func() { _ = h.Close(s74Bg) })
	hdr := event.RequestHeaderPayload{Route: route}
	if withTool {
		hdr.Tools = []vocab.ToolSchema{{Name: "probe", Description: "Echo a text",
			Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}}
	}
	return h, s74Open(h, hdr, "Be brief.", "hello")
}

func s74Open(h session.Handle, hdr event.RequestHeaderPayload, system, user string) error {
	var drafts []event.Draft
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) { return event.RequestHeader.Draft(hdr) },
		func() (event.Draft, error) {
			m, err := vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock(system))
			if err != nil {
				return event.Draft{}, err
			}
			return event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: m})
		},
		func() (event.Draft, error) {
			m, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock(user))
			if err != nil {
				return event.Draft{}, err
			}
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})
		},
	} {
		d, err := mk()
		if err != nil {
			return err
		}
		drafts = append(drafts, d)
	}
	if _, err := h.AppendGroup(s74Bg, drafts); err != nil {
		return err
	}
	_, err := h.Flush(s74Bg)
	return err
}

// s74Sink appends each settlement to the session, as the driver does.
type s74Sink struct {
	h       session.Handle
	records []llm.Attempt
}

func (s *s74Sink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	d, err := a.Draft()
	if err != nil {
		return err
	}
	_, err = s.h.Append(s74Bg, d)
	return err
}

// s74Attempt streams one attempt of r over h; each chunk is passed to on first.
func s74Attempt(ctx context.Context, rt *llm.Runtime, r *s74Rig, h session.Handle, on func(vocab.Chunk)) ([]vocab.Chunk, *s74Sink, llm.Prepared, error) {
	p, err := rt.Prepare(ctx, r.id, r.model, vocab.Controls{})
	if err != nil {
		return nil, nil, nil, err
	}
	req, err := h.NextRequest()
	if err != nil {
		return nil, nil, nil, err
	}
	s := &s74Sink{h: h}
	var chunks []vocab.Chunk
	for c := range p.Stream(ctx, req, s) {
		if on != nil {
			on(c)
		}
		chunks = append(chunks, c)
	}
	if len(s.records) != 1 {
		return nil, nil, nil, fmt.Errorf("%d settlements, want exactly one", len(s.records))
	}
	return chunks, s, p, nil
}

func s74Finish(chunks []vocab.Chunk) vocab.Finish {
	if len(chunks) > 0 {
		if f, ok := chunks[len(chunks)-1].(vocab.Finish); ok {
			return f
		}
	}
	return vocab.Finish{}
}

// ---- Scenario 1: the two-route legs ----

type s74Result struct {
	Route, Leg, Err string
}

type s74State struct {
	results []s74Result

	// TOOLS-1
	ws     string
	before map[string][]byte
	events []event.Event

	// LOADER-1
	ldr      *loader.Service
	user     string
	turns    int
	mu       sync.Mutex
	log      []string
	reloaded chan struct{}
	rejected chan error
}

func (s *s74State) note(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, line)
}

func (s *s74State) givenRoutes(a, b string) error {
	for _, id := range []string{a, b} {
		if _, ok := s74Routes[id]; !ok {
			return fmt.Errorf("%s is not a client route", id)
		}
	}
	_, err := s74Store()
	return err
}

func (s *s74State) runLegs() error {
	for _, name := range []string{"claude-code", "copilot-cli"} {
		id := s74Routes[name]
		other := copilot.RouteID
		if id == copilot.RouteID {
			other = claudecode.RouteID
		}
		for _, leg := range []struct {
			name string
			run  func(vocab.RouteID, vocab.RouteID) error
		}{
			{"LLM-1 reasoning turn", s74Reasoning}, {"LLM-1 tool call", s74ToolCall}, {"LLM-1 cancel", s74Cancel},
			{"LLM-3 signed out", s74SignedOut}, {"LLM-3 output cap", s74OutputCap}, {"LLM-3 plain turn", s74Plain},
			{"VIEW-1 generated title", s74TitleGenerated}, {"VIEW-1 failing title route", s74TitleFails},
		} {
			r := s74Result{Route: name, Leg: leg.name}
			if err := leg.run(id, other); err != nil {
				r.Err = err.Error()
			}
			s74OuterT.Logf("s74 kept result: %s / %s: %s", r.Route, r.Leg, cmpOr(r.Err, "pass"))
			s.results = append(s.results, r)
		}
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// s74Reasoning: reasoning then text, with usage and the client invocation.
func s74Reasoning(id, _ vocab.RouteID) error {
	r, err := s74NewRig(id, nil, "reasoning")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r)
	if err != nil {
		return err
	}
	h, err := s74Session(id, false)
	if err != nil {
		return err
	}
	chunks, sink, _, err := s74Attempt(s74Bg, rt, r, h, nil)
	if err != nil {
		return err
	}
	var reasoned, said bool
	for _, c := range chunks {
		switch c.(type) {
		case vocab.ReasoningDelta:
			reasoned = true
		case vocab.TextDelta:
			said = true
		}
	}
	a := sink.records[0]
	if !reasoned || !said || !a.Succeeded() || a.Usage == nil || a.Usage.Output == 0 {
		return fmt.Errorf("reasoning %v, text %v, succeeded %v, usage %+v", reasoned, said, a.Succeeded(), a.Usage)
	}
	if a.Client == nil || a.Client.SessionID == "" || a.Client.Version == "" || a.Client.ArgsDigest == "" {
		return fmt.Errorf("the attempt recorded no client invocation: %+v", a.Client)
	}
	return nil
}

// s74ToolCall: the declared tool is called with valid JSON arguments.
func s74ToolCall(id, _ vocab.RouteID) error {
	r, err := s74NewRig(id, nil, "tool")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r)
	if err != nil {
		return err
	}
	h, err := s74Session(id, true)
	if err != nil {
		return err
	}
	chunks, _, _, err := s74Attempt(s74Bg, rt, r, h, nil)
	if err != nil {
		return err
	}
	var name, args string
	for _, c := range chunks {
		if d, ok := c.(vocab.ToolCallDelta); ok {
			name += d.Name
			args += d.ArgsDelta
		}
	}
	var got map[string]any
	if name != "probe" || json.Unmarshal([]byte(args), &got) != nil || got["text"] != "ping" {
		return fmt.Errorf("tool call %q with arguments %q, want probe {\"text\":\"ping\"}", name, args)
	}
	if f := s74Finish(chunks); f.Reason != vocab.FinishToolCalls {
		return fmt.Errorf("finish %+v, want tool_calls", f)
	}
	return nil
}

// s74Cancel: a stalled client is cancelled; the attempt settles aborted.
func s74Cancel(id, _ vocab.RouteID) error {
	r, err := s74NewRig(id, []string{s74ModeEnv + "=hang"}, "slow")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r)
	if err != nil {
		return err
	}
	h, err := s74Session(id, true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s74Bg, s74Wait)
	defer cancel()
	chunks, sink, _, err := s74Attempt(ctx, rt, r, h, func(c vocab.Chunk) {
		if _, ok := c.(vocab.TextDelta); ok {
			cancel()
		}
	})
	if err != nil {
		return err
	}
	f, a := s74Finish(chunks), sink.records[0]
	if f.Reason != vocab.FinishAborted || a.Finish.Failure == nil || a.Finish.Failure.Code != vocab.CodeAborted || a.Client == nil {
		return fmt.Errorf("finish %+v, settled %+v, invocation %+v; want aborted with llm/aborted", f, a.Finish, a.Client)
	}
	return nil
}

// s74SignedOut: one permanent llm/sign-in-required attempt, no retry and no
// fallback to the other registered route.
func s74SignedOut(id, other vocab.RouteID) error {
	fixture, extra := "signed-out", []string(nil)
	if id == copilot.RouteID {
		fixture, extra = "signed-in-line", []string{s74ExitEnv + "=1"}
	}
	r, err := s74NewRig(id, extra, fixture)
	if err != nil {
		return err
	}
	o, err := s74NewRig(other, nil, "text")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r, o)
	if err != nil {
		return err
	}
	h, err := s74Session(id, false)
	if err != nil {
		return err
	}
	_, sink, p, err := s74Attempt(s74Bg, rt, r, h, nil)
	if err != nil {
		return err
	}
	a := sink.records[0]
	d, err := a.Draft()
	if err != nil {
		return err
	}
	f := a.Finish.Failure
	if d.Type() != event.AssistantAttempt.Name() || f == nil || f.Code != vocab.CodeSignInRequired || f.Class != vocab.Permanent {
		return fmt.Errorf("settled %s %+v, want one assistant/attempt llm/sign-in-required permanent", d.Type(), f)
	}
	if _, stop := p.Retry().Next(f, 1, 0, 0.5); stop == "" {
		return fmt.Errorf("the route's policy would retry %s", f.Code)
	}
	if n := r.count("runs"); n != 1 {
		return fmt.Errorf("the signed-out client ran %d times, want once", n)
	}
	if n := o.count("runs") + o.count("probes"); n != 0 {
		return fmt.Errorf("the other route %s was used (%d) after the sign-in failure", other, n)
	}
	return nil
}

// s74OutputCap: an output cap is refused before the client is probed or run.
func s74OutputCap(id, _ vocab.RouteID) error {
	r, err := s74NewRig(id, nil, "text")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r)
	if err != nil {
		return err
	}
	_, err = rt.Prepare(s74Bg, id, r.model, vocab.Controls{MaxOutputTokens: 64})
	var f *vocab.Failure
	if !errors.As(err, &f) || f.Code != vocab.CodeUnsupportedOption {
		return fmt.Errorf("prepare with an output cap = %v, want llm/unsupported-option", err)
	}
	if n := r.count("probes") + r.count("runs"); n != 0 {
		return fmt.Errorf("the client was started %d times for a refused option", n)
	}
	return nil
}

// s74Plain: a plain text turn completes.
func s74Plain(id, _ vocab.RouteID) error {
	r, err := s74NewRig(id, nil, "text")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r)
	if err != nil {
		return err
	}
	h, err := s74Session(id, false)
	if err != nil {
		return err
	}
	chunks, sink, _, err := s74Attempt(s74Bg, rt, r, h, nil)
	if err != nil {
		return err
	}
	if !sink.records[0].Succeeded() || s74Finish(chunks).Reason != vocab.FinishStop {
		return fmt.Errorf("plain turn settled %+v", sink.records[0].Finish)
	}
	return nil
}

// s74Titled runs the first turn of a titled session on r and returns the
// title before and after the turn and the session's title events.
func s74Titled(r *s74Rig) (before, after title.Title, evs []event.Event, err error) {
	rt, err := s74Runtime(r)
	if err != nil {
		return
	}
	root := s74Root()
	reg := views.New()
	ts, err := title.New(reg, title.WithModel(titlemodel.New(rt)))
	if err != nil {
		return
	}
	if err = ts.Listen(root); err != nil {
		return
	}
	ended := make(chan struct{}, 4)
	if _, err = session.Committed.On(root, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Event.Type() == event.TurnEndType.Name() {
			ended <- struct{}{}
		}
		return nil
	}); err != nil {
		return
	}
	st, err := s74Store()
	if err != nil {
		return
	}
	h, err := session.NewService(root, st, session.Config{Folder: reg}).Create(s74Bg, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return
	}
	s74OuterT.Cleanup(func() { _ = h.Close(s74Bg) })
	if err = s74Open(h, event.RequestHeaderPayload{Route: r.id}, "Be brief.", "please fix the login bug in the auth form"); err != nil {
		return
	}
	if before, err = ts.Get(s74Bg, h.ID()); err != nil {
		return
	}
	var drafts []event.Draft
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.TurnEndType.Draft(event.TurnEnd{Turn: 1, Reason: event.Reason{Kind: event.ReasonCompleted}})
		},
	} {
		d, derr := mk()
		if derr != nil {
			return before, after, nil, derr
		}
		drafts = append(drafts, d)
	}
	if _, err = h.AppendGroup(s74Bg, drafts); err != nil {
		return
	}
	if _, err = h.Flush(s74Bg); err != nil {
		return
	}
	select {
	case <-ended:
	case <-time.After(s74Wait):
		return before, after, nil, errors.New("turn/end was never delivered")
	}
	ts.Wait()
	if after, err = ts.Get(s74Bg, h.ID()); err != nil {
		return
	}
	all, err := h.Read(s74Bg, 0, int(h.Len()))
	for _, ev := range all {
		if strings.HasPrefix(ev.Type(), "session/title") {
			evs = append(evs, ev)
		}
	}
	return before, after, evs, err
}

func s74Types(evs []event.Event) string {
	var types []string
	for _, ev := range evs {
		types = append(types, ev.Type())
	}
	return strings.Join(types, ",")
}

// s74TitleGenerated: a fallback title, then the one the route's model generated.
func s74TitleGenerated(id, _ vocab.RouteID) error {
	r, err := s74NewRig(id, nil, "text")
	if err != nil {
		return err
	}
	before, after, evs, err := s74Titled(r)
	if err != nil {
		return err
	}
	if before.Source != title.SourceFallback || before.Text == "" {
		return fmt.Errorf("title before the turn = %+v, want a fallback", before)
	}
	if got := s74Types(evs); got != "session/title-request,session/title-attempt,session/title" {
		detail := ""
		if len(evs) > 1 {
			if a, aerr := title.AttemptType.As(evs[1]); aerr == nil {
				detail = fmt.Sprintf(" (attempt %+v)", a)
			}
		}
		return fmt.Errorf("title events %s%s after %d client runs, want request, attempt, title", got, detail, r.count("runs"))
	}
	if after.Source != title.SourceGenerated || after.Text == "" || after.Text == before.Text {
		return fmt.Errorf("title after the turn = %+v, want a generated one", after)
	}
	a, err := title.AttemptType.As(evs[1])
	if err != nil {
		return err
	}
	if a.Outcome != title.OutcomeAccepted || a.RequestSeq != evs[0].Seq() || r.count("runs") != 1 {
		return fmt.Errorf("title attempt %+v after %d client runs, want accepted for the request from one run", a, r.count("runs"))
	}
	return nil
}

// s74TitleFails: a signed-out title route leaves the fallback.
func s74TitleFails(_, other vocab.RouteID) error {
	fixture, extra := "signed-out", []string(nil)
	if other == copilot.RouteID {
		fixture, extra = "signed-in-line", []string{s74ExitEnv + "=1"}
	}
	r, err := s74NewRig(other, extra, fixture)
	if err != nil {
		return err
	}
	before, after, evs, err := s74Titled(r)
	if err != nil {
		return err
	}
	if after.Text != before.Text || after.Source != title.SourceFallback || after.Pinned {
		return fmt.Errorf("title %+v after a failed title attempt, want the fallback %+v kept", after, before)
	}
	if got := s74Types(evs); got != "session/title-request,session/title-attempt" {
		return fmt.Errorf("title events %s, want one request and one attempt and no title", got)
	}
	a, err := title.AttemptType.As(evs[1])
	if err != nil {
		return err
	}
	if a.Outcome != title.OutcomeFailed || a.ErrorCode != string(vocab.CodeSignInRequired) {
		return fmt.Errorf("title attempt %+v after %d client runs, want failed with %s from the signed-out client", a, r.count("runs"), vocab.CodeSignInRequired)
	}
	return nil
}

func (s *s74State) thenRoutePassed(name string) error {
	var failed []string
	n := 0
	for _, r := range s.results {
		if r.Route == name {
			n++
			if r.Err != "" {
				failed = append(failed, r.Leg+": "+r.Err)
			}
		}
	}
	if n != s74Legs || len(failed) > 0 {
		return fmt.Errorf("%s ran %d of %d legs; failed:\n%s", name, n, s74Legs, strings.Join(failed, "\n"))
	}
	return nil
}

func (s *s74State) thenRouteKept(name string) error {
	n := 0
	for _, r := range s.results {
		if r.Route == name && r.Leg != "" {
			n++
		}
	}
	if n != s74Legs {
		return fmt.Errorf("%s kept %d of %d leg results", name, n, s74Legs)
	}
	return nil
}

// s74Live runs the real scenario tests where their S17 inputs are set: a
// claude-code subtest must pass, a copilot-cli subtest must have a pass or fail
// result (kept in the log), and a test without route legs must pass. With no
// input set the in-process legs above are the proof; a partial set fails.
func (s *s74State) thenLive(list string) error {
	tests := strings.Split(list, ",")
	var set, missing, scenarios []string
	for _, t := range tests {
		for _, name := range s74LiveInputs[t] {
			if strings.TrimSpace(os.Getenv(name)) == "" {
				missing = append(missing, name)
			} else {
				set = append(set, name)
			}
		}
		scenarios = append(scenarios, s74LiveScenario[t])
	}
	if len(set) == 0 {
		return nil
	}
	if len(missing) > 0 {
		return fmt.Errorf("the S17 inputs are partly set: %s unset while %s are set", strings.Join(missing, ", "), strings.Join(set, ", "))
	}
	mod, err := s17rModuleDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s74Bg, s74LiveWithin+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-tags", "scenario", "-count=1", "-json", "-timeout", s74LiveWithin.String(),
		"-run", "^("+strings.Join(tests, "|")+")$", "./internal/testkit/scenarios/")
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = mod
	cmd.Env = s17rEnv("DSH_REQUIRE_SCENARIOS=" + strings.Join(scenarios, ","))
	out, _ := cmd.Output()
	action := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		var ev struct{ Action, Test string }
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Test != "" && slices.Contains([]string{"pass", "fail", "skip"}, ev.Action) {
			action[ev.Test] = ev.Action
		}
	}
	var problems []string
	for _, t := range tests {
		claude, hasLegs := action[t+"/claude-code"]
		switch {
		case !hasLegs && action[t] != "pass":
			problems = append(problems, fmt.Sprintf("%s: %q, want pass", t, action[t]))
		case hasLegs && claude != "pass":
			problems = append(problems, fmt.Sprintf("%s/claude-code: %q, want pass", t, claude))
		case hasLegs && action[t+"/copilot-cli"] != "pass" && action[t+"/copilot-cli"] != "fail":
			problems = append(problems, fmt.Sprintf("%s/copilot-cli: %q, want a kept pass or fail", t, action[t+"/copilot-cli"]))
		}
		s74OuterT.Logf("s74 live kept result: %s claude-code=%s copilot-cli=%s", t, action[t+"/claude-code"], action[t+"/copilot-cli"])
	}
	if len(problems) > 0 {
		return fmt.Errorf("on the S17 machine:\n%s\n%s", strings.Join(problems, "\n"), out)
	}
	return nil
}

// ---- Scenario 2: DH-E-TOOLS-1 ----

type s74ReadIn struct {
	Path string `json:"path" dsh:"required"`
}

type s74WriteIn struct {
	Path string `json:"path" dsh:"required"`
	Text string `json:"text" dsh:"required"`
}

func (s *s74State) givenWorkspace(files string) error {
	s.ws, s.before = s74OuterT.TempDir(), map[string][]byte{}
	for _, f := range strings.Split(files, ",") {
		b := []byte("contents of " + f + "\n")
		if err := os.WriteFile(filepath.Join(s.ws, f), b, 0o600); err != nil {
			return err
		}
		s.before[f] = b
	}
	return nil
}

// s74ToolsFixture writes the client lines of one step calling read a, read b
// and write c, each denied inside the client as the route requires.
func s74ToolsFixture(a, b, c string) (string, error) {
	mod, err := s17rModuleDir()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(mod, "internal", "llm", "provider", "claudecode", "testdata", "parallel.jsonl"))
	if err != nil {
		return "", err
	}
	head := strings.SplitN(string(raw), "\n", 4)[:3]
	calls := []struct{ id, name, args string }{
		{"toolu_s74a", "scenario_read", `{"path":"` + a + `"}`},
		{"toolu_s74b", "scenario_read", `{"path":"` + b + `"}`},
		{"toolu_s74c", "scenario_write", `{"path":"` + c + `","text":"overwritten"}`},
	}
	stream := func(e map[string]any) map[string]any {
		return map[string]any{"type": "stream_event", "event": e, "session_id": "{{session_id}}", "parent_tool_use_id": nil}
	}
	var lines []any
	var denials []any
	for i, call := range calls {
		lines = append(lines,
			stream(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": call.id, "name": claudecode.ToolPrefix + call.name, "input": map[string]any{}}}),
			stream(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": call.args}}),
			stream(map[string]any{"type": "content_block_stop", "index": i}))
		denials = append(denials, map[string]any{"tool_name": claudecode.ToolPrefix + call.name, "tool_use_id": call.id, "tool_input": map[string]any{}})
	}
	lines = append(lines,
		stream(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 30}}),
		stream(map[string]any{"type": "message_stop"}))
	for _, call := range calls {
		lines = append(lines, map[string]any{"type": "user", "parent_tool_use_id": nil, "session_id": "{{session_id}}", "message": map[string]any{"role": "user",
			"content": []any{map[string]any{"type": "tool_result", "tool_use_id": call.id, "content": "Permission to use " + claudecode.ToolPrefix + call.name + " has been denied.", "is_error": true}}}})
	}
	lines = append(lines, map[string]any{"type": "result", "subtype": "error_max_turns", "is_error": true, "duration_ms": 1200, "duration_api_ms": 1100, "num_turns": 1,
		"session_id": "{{session_id}}", "total_cost_usd": 0.0012, "usage": map[string]any{"input_tokens": 12, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 4, "output_tokens": 30},
		"modelUsage": map[string]any{}, "permission_denials": denials, "uuid": "00000000-0000-4000-8000-000000000301", "stop_reason": "tool_use"})
	out := strings.Join(head, "\n") + "\n"
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			return "", err
		}
		out += string(j) + "\n"
	}
	path := filepath.Join(s74OuterT.TempDir(), "tools.jsonl")
	return path, os.WriteFile(path, []byte(out), 0o600)
}

// whenGuardedTurn runs one driver turn on claude-code: step 1 calls the two
// reads and the write, which the policy asks about and the answerer rejects;
// step 2 answers in text.
func (s *s74State) whenGuardedTurn(a, b, c string) error {
	fixture, err := s74ToolsFixture(a, b, c)
	if err != nil {
		return err
	}
	r, err := s74NewRig(claudecode.RouteID, nil, fixture, "text")
	if err != nil {
		return err
	}
	rt, err := s74Runtime(r)
	if err != nil {
		return err
	}
	root := s74Root()
	if err := s74Wait1(root.Use(scope.Component, cordis.None{})); err != nil {
		return err
	}
	if err := s74Wait1(root.Use(prompt.Component, prompt.Defaults())); err != nil {
		return err
	}
	var tab scope.Table
	var ps *prompt.Service
	if err := s74Wait1(root.Inject([]string{scope.TableKey.Name(), prompt.Key.Name()}, func(cx *cordis.Context) (err error) {
		if tab, err = scope.TableKey.Get(cx); err != nil {
			return err
		}
		ps, err = prompt.Key.Get(cx)
		return err
	})); err != nil {
		return err
	}
	const id, ch = "s-s74-tools", human.Channel("ui")
	ref := agent.Ref{Session: id, Incarnation: 1}
	reg := human.New()
	s74OuterT.Cleanup(reg.Close)
	answerer.Register(s74OuterT, root, reg, ch, answerer.Deny())
	if _, err := reg.Bind(root, ref, human.Binding{Channel: ch}); err != nil {
		return err
	}
	svc := core.NewWith(root, tab, core.Config{Approval: approval.New(approval.Config{Registry: reg})})
	if _, err := svc.Register(root, tools.Define(tools.Spec[s74ReadIn, string]{Name: "scenario_read", Description: "Reads a workspace file.",
		Run: func(_ context.Context, in s74ReadIn, _ tools.Run) (string, error) {
			b, err := os.ReadFile(filepath.Join(s.ws, filepath.Base(in.Path)))
			return string(b), err
		}})); err != nil {
		return err
	}
	if _, err := svc.Register(root, tools.Define(tools.Spec[s74WriteIn, string]{Name: "scenario_write", Description: "Writes a workspace file.",
		Run: func(_ context.Context, in s74WriteIn, _ tools.Run) (string, error) {
			return "written", os.WriteFile(filepath.Join(s.ws, filepath.Base(in.Path)), []byte(in.Text), 0o600)
		}})); err != nil {
		return err
	}
	if _, err := svc.Decide(root, "s74.ask-write", func(_ context.Context, c tools.Call) tools.Decision {
		if c.Name() == "scenario_write" {
			return tools.Ask("writes need a person", "")
		}
		return tools.Allow()
	}); err != nil {
		return err
	}
	st, err := s74Store()
	if err != nil {
		return err
	}
	sess, err := session.NewService(root, st, session.Config{}).Create(s74Bg, nil, session.CreateOptions{Header: session.Header{ID: id}})
	if err != nil {
		return err
	}
	s74OuterT.Cleanup(func() { _ = sess.Close(s74Bg) })
	_, seat, err := request.New(root, request.Options{Table: tab,
		Sessions: func(agent.Ref) (request.Log, error) { return sess, nil },
		Scopes:   func(agent.Ref) (scope.Key, error) { return scope.Key{}, nil }})
	if err != nil {
		return err
	}
	s74OuterT.Cleanup(seat.Dispose)
	inbox, err := agent.OpenInbox(s74Bg, sess)
	if err != nil {
		return err
	}
	mod, err := agent.OpenModel(s74Bg, sess, agent.ModelOptions{Initial: agent.ModelSelection{Route: claudecode.RouteID, Model: r.model}})
	if err != nil {
		return err
	}
	drv, err := driver.Start(driver.Options{Context: root, Agent: s74Agent{ref: ref}, Session: sess, Inbox: inbox, Model: mod, Models: rt, Tools: svc, Prompt: ps})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s74Bg, s74Wait)
	defer cancel()
	defer func() { _ = drv.Close(ctx) }()
	if _, err := inbox.Send(s74Bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "read a and b, then write c"}); err != nil {
		return err
	}
	if err := drv.WaitIdle(ctx); err != nil {
		return err
	}
	s.events, err = sess.Read(s74Bg, 0, 0)
	return err
}

func s74Wait1(f interface{ Wait() error }, err error) error {
	if err != nil {
		return err
	}
	return f.Wait()
}

type s74Agent struct {
	agent.Agent
	ref agent.Ref
}

func (a s74Agent) Ref() agent.Ref      { return a.ref }
func (a s74Agent) ScopeKey() scope.Key { return scope.Key{} }

func (s *s74State) thenCallsInOrder() error {
	var calls []string
	callAt, resultAt := map[string]int{}, map[string]int{}
	for i, ev := range s.events {
		switch ev.Type() {
		case event.ToolCall.Name():
			p, err := event.ToolCall.As(ev)
			if err != nil {
				return err
			}
			calls = append(calls, p.CallID+":"+p.Name)
			callAt[p.CallID] = i
		case event.ToolResult.Name():
			p, err := event.ToolResult.As(ev)
			if err != nil {
				return err
			}
			resultAt[p.CallID] = i
		}
	}
	want := []string{"toolu_s74a:scenario_read", "toolu_s74b:scenario_read", "toolu_s74c:scenario_write"}
	if !slices.Equal(calls, want) {
		return fmt.Errorf("tool calls %v, want %v", calls, want)
	}
	for id, at := range callAt {
		if r, ok := resultAt[id]; !ok || r < at {
			return fmt.Errorf("call %s at %d has its result at %d (present %v)", id, at, r, ok)
		}
	}
	return nil
}

// s74ResultText is the text of a tool result message: its text blocks and the
// text parts of its tool_result blocks.
func s74ResultText(m vocab.Message) string {
	var text string
	for _, b := range m.Blocks() {
		if b.Text != nil {
			text += b.Text.Text
		}
		if b.ToolResult != nil {
			for _, part := range b.ToolResult.Parts {
				if part.Text != nil {
					text += part.Text.Text
				}
			}
		}
	}
	return text
}

func (s *s74State) thenWriteRefused(code, file string) error {
	for _, ev := range s.events {
		if ev.Type() != event.ToolResult.Name() {
			continue
		}
		p, err := event.ToolResult.As(ev)
		if err != nil {
			return err
		}
		text := s74ResultText(p.Message)
		if p.CallID == "toolu_s74c" && (!p.IsError || !strings.Contains(text, code)) {
			return fmt.Errorf("the write's result is %q (error %v), want an %s code", text, p.IsError, code)
		}
		if p.CallID != "toolu_s74c" && p.IsError {
			return fmt.Errorf("read %s failed: %s", p.CallID, text)
		}
	}
	got, err := os.ReadFile(filepath.Join(s.ws, file))
	if err != nil || string(got) != string(s.before[file]) {
		return fmt.Errorf("%s = %q (%v), want it unchanged %q", file, got, err, s.before[file])
	}
	return nil
}

func (s *s74State) thenTurnCompleted() error {
	var reply string
	for _, ev := range s.events {
		switch ev.Type() {
		case event.AssistantMessage.Name():
			p, err := event.AssistantMessage.As(ev)
			if err != nil {
				return err
			}
			for _, b := range p.Message.Blocks() {
				if b.Text != nil {
					reply = b.Text.Text
				}
			}
		case event.TurnEndType.Name():
			p, err := event.TurnEndType.As(ev)
			if err != nil {
				return err
			}
			if p.Reason.Kind != event.ReasonCompleted || reply != "Hello there." {
				return fmt.Errorf("turn ended %q after reply %q, want completed after \"Hello there.\"", p.Reason.Kind, reply)
			}
			return nil
		}
	}
	return errors.New("no turn/end in the log")
}

// ---- Scenario 2: DH-E-LOADER-1 ----

type s74Probe struct {
	Level string `json:"level,omitempty"`
}

func (c s74Probe) Validate() error {
	switch c.Level {
	case "", "debug", "info", "warn":
		return nil
	}
	return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Path: "level", Message: fmt.Sprintf("level %q is not debug, info or warn", c.Level)}}}
}

func (s *s74State) givenWatch(level string) error {
	comp := cordis.Define("x.s74probe", func(_ *cordis.Context, c s74Probe) error {
		s.note("x.s74probe applied level=" + c.Level)
		return nil
	})
	cat, err := catalog.Build([]catalog.Item{{Name: "x.s74probe", Component: comp, Config: reflect.TypeFor[s74Probe]()}})
	if err != nil {
		return err
	}
	dir, def := temphome.Dir(s74OuterT), profile.Definition{Name: "s74"}
	temphome.Write(s74OuterT, profile.LayerPath(dir, def.Name), "- insert:\n    - id: probe\n      name: x.s74probe\n      config:\n        level: "+level+"\n")
	s.user = profile.UserLayerPath(dir)
	root := testkit.Tree(s74OuterT)
	if err := s74Wait1(root.Use(loader.Component, loader.Options{Resolver: cat})); err != nil {
		return err
	}
	if s.ldr, err = loader.Key.Get(root); err != nil {
		return err
	}
	cs, err := config.NewService(config.ServiceOptions{Context: root, Loader: s.ldr, Catalog: cat,
		Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
			return profile.Stack(def, profile.Sources{Home: dir, Read: profile.ReadFile(read)})
		}})
	if err != nil {
		return err
	}
	if err := cs.Current().Report.Err(); err != nil {
		return err
	}
	if err := s.ldr.ApplyLayers(cs.Current().Layers); err != nil {
		return err
	}
	s.reloaded, s.rejected = make(chan struct{}, 8), make(chan error, 8)
	cs.OnRejected(func(err error) { s.rejected <- err })
	if _, err := loader.Reloaded.Prepend(root, func(_ *cordis.Context, n loader.ReloadNotice) error {
		if n.Loader == s.ldr {
			s.reloaded <- struct{}{}
		}
		return nil
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s74Bg)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		cs.Watch(ctx, 20*time.Millisecond)
	}()
	s74OuterT.Cleanup(func() { cancel(); <-watched })
	return s.turnSees(level)
}

// turnSees runs the next turn, which reads the probe's effective config.
func (s *s74State) turnSees(level string) error {
	entry := s.ldr.Find("probe")
	if entry == nil {
		return errors.New("no probe entry")
	}
	cfg, ok := s.ldr.Fiber(entry).EffectiveConfig()
	if !ok {
		return errors.New("the probe holds no effective config")
	}
	s.turns++
	got := cfg.(s74Probe).Level
	s.note(fmt.Sprintf("turn %d level=%s", s.turns, got))
	if got != level {
		return fmt.Errorf("turn %d sees level %q, want %q", s.turns, got, level)
	}
	return nil
}

// whenSaved saves the user layer as an editor does: a temporary file renamed over it.
func (s *s74State) whenSaved(level string) error {
	tmp := s.user + ".s74~"
	if err := os.WriteFile(tmp, []byte("- id: probe\n  merge:\n    level: "+level+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.user); err != nil {
		return err
	}
	select {
	case <-s.reloaded:
		return nil
	case err := <-s.rejected:
		return fmt.Errorf("the edit to %s was rejected: %v", level, err)
	case <-time.After(s74Wait):
		return errors.New("the watch never applied the edit")
	}
}

func (s *s74State) thenNextTurn(level string) error {
	if err := s.turnSees(level); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	applied := slices.Index(s.log, "x.s74probe applied level="+level)
	turn := slices.Index(s.log, fmt.Sprintf("turn %d level=%s", s.turns, level))
	if applied < 0 || applied > turn || (turn > 0 && !strings.HasPrefix(s.log[turn-1], "x.s74probe applied")) {
		return fmt.Errorf("the log does not show the edit applied just before turn %d:\n%s", s.turns, strings.Join(s.log, "\n"))
	}
	s74OuterT.Logf("s74 loader log:\n%s", strings.Join(s.log, "\n"))
	return nil
}

func (s *s74State) whenSavedBad(level string) error {
	if err := os.WriteFile(s.user, []byte("- id: probe\n  merge:\n    level: "+level+"\n"), 0o600); err != nil {
		return err
	}
	select {
	case err := <-s.rejected:
		s.note("rejected: " + err.Error())
		return nil
	case <-s.reloaded:
		return fmt.Errorf("the invalid level %s was applied", level)
	case <-time.After(s74Wait):
		return errors.New("the watch never reported the invalid edit")
	}
}

func (s *s74State) thenRejected(level string) error {
	s.mu.Lock()
	last := s.log[len(s.log)-1]
	s.mu.Unlock()
	if !strings.Contains(last, s.user+":3:") || !strings.Contains(last, "probe.config.level") {
		return fmt.Errorf("the diagnostic is not positioned at %s:3 probe.config.level: %s", s.user, last)
	}
	return s.turnSees(level)
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_model_tools_titles_and_configuration_acceptance(ctx *godog.ScenarioContext) {
	s := &s74State{}
	ctx.Step(`^both client routes "([^"]*)" and "([^"]*)" with a scratch store$`, s.givenRoutes)
	ctx.Step(`^the LLM-1, LLM-3 and VIEW-1 legs run on each route$`, s.runLegs)
	ctx.Step(`^every "([^"]*)" leg passed$`, s.thenRoutePassed)
	ctx.Step(`^every "([^"]*)" leg has a kept result$`, s.thenRouteKept)
	ctx.Step(`^on the S17 machine the live "([^"]*)" legs agree$`, s.thenLive)
	ctx.Step(`^the claude-code route over a workspace holding "([^"]*)"$`, s.givenWorkspace)
	ctx.Step(`^the model reads "([^"]*)" and "([^"]*)" and asks to write "([^"]*)" through the guarded pipeline and the write is rejected$`, s.whenGuardedTurn)
	ctx.Step(`^each tool/call precedes its tool/result in the order the model sent them$`, s.thenCallsInOrder)
	ctx.Step(`^the write ended with an "([^"]*)" code and "([^"]*)" is unchanged$`, s.thenWriteRefused)
	ctx.Step(`^the turn ended completed with the stand-in's reply$`, s.thenTurnCompleted)
	ctx.Step(`^a running config watch over a scratch home whose probe level is "([^"]*)"$`, s.givenWatch)
	ctx.Step(`^the user layer is saved editor-style with level "([^"]*)"$`, s.whenSaved)
	ctx.Step(`^the next turn sees level "([^"]*)" and the log shows the edit applied before that turn$`, s.thenNextTurn)
	ctx.Step(`^the user layer is saved with an invalid level "([^"]*)"$`, s.whenSavedBad)
	ctx.Step(`^the edit is rejected with a diagnostic positioned in the user layer and the next turn still sees "([^"]*)"$`, s.thenRejected)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_model_tools_titles_and_configuration_acceptance(t *testing.T) {
	s74OuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_model_tools_titles_and_configuration_acceptance,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_model_tools_titles_and_configuration_acceptance.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the model, tools, titles and configuration e2e scenarios failed")
	}
}
