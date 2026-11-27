//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// The helpers of the scenarios that drive the built dsh over its SDK protocol
// (DH-S-9's DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1). They speak to the product only
// through the client-facing types of src/sdk/protocol and src/sdk/jsonrpc and start
// it only through the test kit's launcher: no scenario here re-implements product
// behaviour from internal packages (the gate's scenario-index check holds them to that).

// sdkCall bounds one request to the host.
const sdkCall = 2 * time.Minute

// sdkHost is one running `dsh ... sdk` and the client connected to it.
type sdkHost struct {
	*launcher.Session

	mu      sync.Mutex
	runs    []protocol.RunCompletedNotification
	streams []protocol.StreamNotification
	changed chan struct{}
}

// sdkTool is a client tool the scenario declares and answers.
type sdkTool struct {
	Decl   protocol.ToolDeclaration
	Answer func(protocol.ToolCallParams) protocol.ToolCallResult
}

// sdkOptions are what a scenario adds to the connection startSDK makes.
type sdkOptions struct {
	// Approve, when set, answers the host's approval/request and declares the
	// approvals capability; unset, the connection declares none.
	Approve func(protocol.ApprovalRequest) protocol.ApprovalResult
}

// startSDK launches bin with args followed by "sdk" against a fresh temp home,
// with env added to the scrubbed host environment, and initializes the connection
// with the stream capability and tools declared. Requests the host sends for tools
// are answered by their Answer.
func startSDK(t *testing.T, bin string, env, args []string, tools ...sdkTool) *sdkHost {
	t.Helper()
	return startSDKWith(t, bin, env, args, sdkOptions{}, tools...)
}

// startSDKWith is startSDK with opts.
func startSDKWith(t *testing.T, bin string, env, args []string, opts sdkOptions, tools ...sdkTool) *sdkHost {
	t.Helper()
	h := &sdkHost{changed: make(chan struct{}, 1)}
	byName := map[string]sdkTool{}
	var decls []protocol.ToolDeclaration
	for _, tool := range tools {
		byName[tool.Decl.Name] = tool
		decls = append(decls, tool.Decl)
	}
	methods := map[string]jsonrpc.Handler{
		"tool/call": func(_ context.Context, raw json.RawMessage) (any, error) {
			var p protocol.ToolCallParams
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			tool, ok := byName[p.Name]
			if !ok {
				return protocol.ToolCallResult{IsError: true, Content: []protocol.ContentBlock{{Type: "text", Text: "no such tool: " + p.Name}}}, nil
			}
			return tool.Answer(p), nil
		},
	}
	if opts.Approve != nil {
		methods["approval/request"] = func(_ context.Context, raw json.RawMessage) (any, error) {
			var p protocol.ApprovalRequest
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			return opts.Approve(p), nil
		}
	}
	h.Session = scenario.Start(t, launcher.Program{
		Argv: append(append([]string{bin}, args...), "sdk"),
		Env:  env,
		RPC:  jsonrpc.Options{Methods: methods, OnNotify: h.notify},
	})
	var init protocol.InitializeResult
	h.call(t, "initialize", protocol.InitializeParams{
		ProtocolVersions: []int{protocol.Version},
		ClientInfo:       protocol.Implementation{Name: "dsh-scenario", Version: "1"},
		Capabilities:     protocol.ClientCapabilities{Stream: true, Approvals: opts.Approve != nil, Tools: decls},
	}, &init)
	if init.ProtocolVersion != protocol.Version {
		t.Fatalf("initialize answered protocol %d, want %d", init.ProtocolVersion, protocol.Version)
	}
	return h
}

func (h *sdkHost) notify(method string, raw json.RawMessage) {
	h.mu.Lock()
	switch method {
	case "session.run.completed":
		var n protocol.RunCompletedNotification
		if json.Unmarshal(raw, &n) == nil {
			h.runs = append(h.runs, n)
		}
	case "session.stream":
		var n protocol.StreamNotification
		if json.Unmarshal(raw, &n) == nil {
			h.streams = append(h.streams, n)
		}
	}
	h.mu.Unlock()
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

// call sends one request and decodes its result into result; an error answer fails
// the scenario with the host's standard error.
func (h *sdkHost) call(t *testing.T, method string, params, result any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), sdkCall)
	defer cancel()
	if err := h.Client.Call(ctx, method, params, result); err != nil {
		t.Fatalf("%s: %v\nstderr:\n%s", method, err, h.Stderr.String())
	}
}

// create opens a session on model and returns its id.
func (h *sdkHost) create(t *testing.T, model *protocol.ModelRoute) string {
	t.Helper()
	var r protocol.SessionCreateResult
	h.call(t, "session/create", protocol.SessionCreateParams{Model: model}, &r)
	if r.SessionID == "" {
		t.Fatal("session/create named no session")
	}
	return r.SessionID
}

// prompt queues text as a user message and returns its receipt.
func (h *sdkHost) prompt(t *testing.T, session, text string) protocol.SessionPromptResult {
	t.Helper()
	var r protocol.SessionPromptResult
	h.call(t, "session/prompt", protocol.SessionPromptParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: text}}}, &r)
	if r.MessageID == "" {
		t.Fatal("session/prompt returned no message id")
	}
	return r
}

// until waits up to within for match to hold over what the host has notified.
func (h *sdkHost) until(t *testing.T, within time.Duration, what string, match func() bool) {
	t.Helper()
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		ok := match()
		h.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-h.changed:
		case <-h.Process.Exited():
			t.Fatalf("dsh exited (code %d) before %s\nstderr:\n%s", h.Process.ExitCode(), what, h.Stderr.String())
		case <-deadline.C:
			t.Fatalf("no %s within %s\nstderr:\n%s", what, within, h.Stderr.String())
		}
	}
}

// awaitRun waits for the run result of the prompt receipt names.
func (h *sdkHost) awaitRun(t *testing.T, session string, receipt protocol.SessionPromptResult, within time.Duration) protocol.RunCompletedNotification {
	t.Helper()
	var got protocol.RunCompletedNotification
	h.until(t, within, "run result of "+receipt.MessageID, func() bool {
		for _, r := range h.runs {
			if r.SessionID == session && r.Run.MessageID == receipt.MessageID {
				got = r
				return true
			}
		}
		return false
	})
	return got
}

// history reads every stored event of session, following the pages
// (historyPages), and records a sign-in failure among them (noteSignIn).
func (h *sdkHost) history(t *testing.T, session string) []protocol.Event {
	t.Helper()
	evs, err := historyPages(t.Context(), func(ctx context.Context, method string, params, result any) error {
		ctx, cancel := context.WithTimeout(ctx, sdkCall)
		defer cancel()
		return h.Client.Call(ctx, method, params, result)
	}, session)
	if err != nil {
		t.Fatalf("session/history: %v\nstderr:\n%s", err, h.Stderr.String())
	}
	noteSignIn(t, evs)
	return evs
}

// inRun keeps the events whose seq lies in [first, last].
func inRun(evs []protocol.Event, first, last int64) []protocol.Event {
	var out []protocol.Event
	for _, e := range evs {
		if e.Seq >= first && e.Seq <= last {
			out = append(out, e)
		}
	}
	return out
}

// ofType keeps the events of the given types, in log order.
func ofType(evs []protocol.Event, types ...string) []protocol.Event {
	var out []protocol.Event
	for _, e := range evs {
		for _, typ := range types {
			if e.Type == typ {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// decode unmarshals an event's committed payload.
func decode(t *testing.T, e protocol.Event, v any) {
	t.Helper()
	if err := json.Unmarshal(e.Data, v); err != nil {
		t.Fatalf("event %d %s: %v\n%s", e.Seq, e.Type, err, e.Data)
	}
}

// keepExport writes the session's log to a file kept past the run when the
// scenario fails, and logs its path (the scenarios' "keep the exported log").
func (h *sdkHost) keepExport(t *testing.T, session string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), sdkCall)
		defer cancel()
		var page protocol.SessionHistoryResult
		if err := h.Client.Call(ctx, "session/history", protocol.SessionHistoryParams{SessionID: session, Limit: 5000}, &page); err != nil {
			t.Logf("export of %s: %v", session, err)
			return
		}
		f, err := os.CreateTemp("", "dsh-scenario-"+session+"-*.json")
		if err != nil {
			t.Logf("export of %s: %v", session, err)
			return
		}
		defer f.Close()
		_ = json.NewEncoder(f).Encode(page.Events)
		t.Logf("kept the exported log of %s at %s", session, f.Name())
	})
}

// writeLayer writes a configuration layer to a temp file and returns its path.
func writeLayer(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layer.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The wire shapes the scenarios read from committed payloads.
type (
	wireFailure struct {
		Code      string `json:"code"`
		Class     string `json:"class"`
		Status    int    `json:"status"`
		RequestID string `json:"request_id"`
	}
	wireUsage struct {
		Input      int `json:"input"`
		CacheRead  int `json:"cache_read"`
		CacheWrite int `json:"cache_write"`
		Output     int `json:"output"`
	}
	wireBlock struct {
		Kind string `json:"kind"`
		Text *struct {
			Text string `json:"text"`
		} `json:"text"`
		Reasoning *struct {
			Text      string `json:"text"`
			Signature string `json:"signature"`
		} `json:"reasoning"`
		ToolUse *struct {
			ID   string          `json:"id"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"tool_use"`
	}
	wireSettlement struct {
		Message *struct {
			Blocks []wireBlock `json:"blocks"`
		} `json:"message"`
		Usage   *wireUsage   `json:"usage"`
		Failure *wireFailure `json:"failure"`
	}
	wireTurnEnd struct {
		Turn   int `json:"turn"`
		Reason struct {
			Kind    string       `json:"kind"`
			Cause   string       `json:"cause"`
			Failure *wireFailure `json:"failure"`
		} `json:"reason"`
	}
)

func (u *wireUsage) add(v wireUsage) {
	u.Input += v.Input
	u.CacheRead += v.CacheRead
	u.CacheWrite += v.CacheWrite
	u.Output += v.Output
}

// turnEnd decodes the one turn/end among evs.
func turnEnd(t *testing.T, evs []protocol.Event) wireTurnEnd {
	t.Helper()
	ends := ofType(evs, "turn/end")
	if len(ends) != 1 {
		t.Fatalf("%d turn/end events in the run, want 1", len(ends))
	}
	var end wireTurnEnd
	decode(t, ends[0], &end)
	return end
}
