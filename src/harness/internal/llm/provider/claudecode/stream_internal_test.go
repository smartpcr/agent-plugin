package claudecode

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

const (
	initOK      = `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.312","model":"m","apiKeySource":"none","tools":["mcp__dsh__probe"],"mcp_servers":[{"name":"dsh","status":"connected"}]}`
	msgStart    = `{"type":"stream_event","event":{"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":1}}}}`
	textStart   = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`
	textDelta   = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}}`
	stop0       = `{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`
	toolStart   = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"mcp__dsh__probe","input":{}}}}`
	toolArgs    = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}}`
	endTurn     = `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}}`
	endTool     = `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}}`
	endMax      = `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":2}}}`
	endRefusal  = `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"refusal"}}}`
	msgStop     = `{"type":"stream_event","event":{"type":"message_stop"}}`
	success     = `{"type":"result","subtype":"success","is_error":false,"usage":{"input_tokens":3,"output_tokens":2}}`
	maxTurns    = `{"type":"result","subtype":"error_max_turns","is_error":true,"permission_denials":[{"tool_use_id":"t1"}]}`
	maxTurnsBad = `{"type":"result","subtype":"error_max_turns","is_error":true,"permission_denials":[]}`
	denied      = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true}]}}`
)

var sep = string(filepath.Separator)

func lines(ls ...string) func() ([]byte, error) {
	i := 0
	return func() ([]byte, error) {
		if i >= len(ls) {
			return nil, io.EOF
		}
		i++
		return []byte(ls[i-1]), nil
	}
}

// play runs the lines through a mapper expecting tools, and returns the chunks
// it yielded and its finish.
func play(t *testing.T, next func() ([]byte, error)) ([]vocab.Chunk, vocab.Finish, *mapper) {
	t.Helper()
	m := newMapper(expect{sessionID: "s", version: "2.1.312", tools: true, keySources: []string{"none"}})
	var got []vocab.Chunk
	out, reading := m.stream(next, func(c vocab.Chunk) bool { got = append(got, c); return true })
	if !reading {
		t.Fatal("the consumer did not stop")
	}
	return got, m.decide(out, 0, false), m
}

func text(ls ...string) []string {
	return append([]string{initOK, msgStart, textStart, textDelta, stop0}, ls...)
}

func tool(ls ...string) []string {
	return append([]string{initOK, msgStart, toolStart, toolArgs, stop0, endTool, msgStop}, ls...)
}

// TestMapperFinishes: the rows of claude-code-cli.md sec. 5.2 that end an
// attempt normally, and every line that the route cannot place, as a finish.
//
//dsh:test DH-T-LLM-21, DH-T-CLAUDECODECLI-2
func TestMapperFinishes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lines  []string
		reason vocab.FinishReason
		code   vocab.Code
	}{
		{"text", text(endTurn, msgStop, success), vocab.FinishStop, ""},
		{"max tokens", text(endMax, msgStop, success), vocab.FinishMaxTokens, ""},
		{"max tokens by result", text(endTurn, msgStop, `{"type":"result","subtype":"success","stop_reason":"max_tokens"}`), vocab.FinishMaxTokens, ""},
		{"refusal", text(endRefusal, msgStop, success), vocab.FinishError, vocab.CodeContentRefused},
		{"output cap error", text(endMax, msgStop, `{"type":"assistant","message":{"content":[]},"error":"max_output_tokens"}`, `{"type":"result","subtype":"success","is_error":true}`), vocab.FinishMaxTokens, ""},
		{"output cap error, no message", []string{initOK, `{"type":"assistant","message":{"content":[]},"error":"max_output_tokens"}`, `{"type":"result","subtype":"success","is_error":true}`}, vocab.FinishError, vocab.CodeClientContract},
		{"no output", []string{initOK, success}, vocab.FinishStop, ""},
		{"tool call", tool(denied, maxTurns), vocab.FinishToolCalls, ""},
		{"denied by line", tool(`{"type":"system","subtype":"permission_denied","tool_use_id":"t1"}`, denied, `{"type":"result","subtype":"error_max_turns","is_error":true}`), vocab.FinishToolCalls, ""},
		{"turn limit, call not denied", tool(maxTurnsBad), vocab.FinishError, vocab.CodeClientContract},
		{"turn limit, no call", text(endTurn, msgStop, maxTurns), vocab.FinishError, vocab.CodeClientContract},
		{"success after a call", tool(success), vocab.FinishError, vocab.CodeClientContract},
		{"a tool ran", tool(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":false}]}}`, success), vocab.FinishError, vocab.CodeClientContract},
		{"denial not reported", tool(denied, `{"type":"result","subtype":"error_max_turns","is_error":true,"permission_denials":[{"tool_use_id":"t9"}]}`), vocab.FinishError, vocab.CodeClientContract},
		{"call not streamed", text(endTurn, msgStop, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t7","name":"mcp__dsh__probe"}]}}`, success), vocab.FinishError, vocab.CodeClientContract},
		{"undeclared tool streamed", []string{initOK, msgStart, strings.Replace(toolStart, "mcp__dsh__probe", "Bash", 1)}, vocab.FinishError, vocab.CodeClientContract},
		{"undeclared tool reported", text(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash"}]}}`), vocab.FinishError, vocab.CodeClientContract},
		{"result inside a message", text(success), vocab.FinishError, vocab.CodeClientContract},
		{"no result", text(endTurn, msgStop), vocab.FinishError, vocab.CodeStreamClosed},
		{"second request", text(endTurn, msgStop, msgStart, success), vocab.FinishError, vocab.CodeClientContract},
		{"second result", text(endTurn, msgStop, success, success), vocab.FinishError, vocab.CodeClientContract},
		{"unknown subtype", text(endTurn, msgStop, `{"type":"result","subtype":"error_new_kind","is_error":false}`), vocab.FinishError, vocab.CodeClientContract},
		{"not JSON", []string{initOK, "this is not json"}, vocab.FinishError, vocab.CodeClientContract},
		{"stream before init", []string{msgStart}, vocab.FinishError, vocab.CodeClientContract},
		{"event without type", []string{initOK, `{"type":"stream_event","event":{}}`}, vocab.FinishError, vocab.CodeClientContract},
		{"undecodable event", []string{initOK, msgStart, `{"type":"stream_event","event":{"type":"content_block_delta","index":4,"delta":{"type":"text_delta","text":"x"}}}`, success}, vocab.FinishError, vocab.CodeClientContract},
		{"in-stream error", []string{initOK, msgStart, `{"type":"stream_event","event":{"type":"error","error":{"type":"overloaded_error","message":"x"}}}`, success}, vocab.FinishError, vocab.CodeServer},
		{"bad result", text(endTurn, msgStop, `{"type":"result","subtype":5}`), vocab.FinishError, vocab.CodeClientContract},
		{"bad assistant", []string{initOK, `{"type":"assistant","message":5}`}, vocab.FinishError, vocab.CodeClientContract},
		{"bad user", []string{initOK, `{"type":"user","message":5}`}, vocab.FinishError, vocab.CodeClientContract},
		{"user echo", text(`{"type":"user","message":{"content":"hello"}}`, endTurn, msgStop, success), vocab.FinishStop, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, fin, _ := play(t, lines(tc.lines...))
			code := vocab.Code("")
			if fin.Failure != nil {
				code = fin.Failure.Code
			}
			if fin.Reason != tc.reason || code != tc.code {
				t.Fatalf("finish = %s %s (%v), want %s %s", fin.Reason, code, fin.Failure, tc.reason, tc.code)
			}
		})
	}
}

// TestMapperInit: the init line must name the harness's session and the probed
// version, offer only the declared tools with the listing server connected,
// and authenticate with the sign-in; a second init line is a breach.
//
//dsh:test DH-T-LLM-22, DH-T-CLAUDECODECLI-3
func TestMapperInit(t *testing.T) {
	for name, tc := range map[string]struct {
		line string
		code vocab.Code
	}{
		"key source":    {strings.Replace(initOK, `"none"`, `"ANTHROPIC_API_KEY"`, 1), vocab.CodeInvalidCredential},
		"no key source": {strings.Replace(initOK, `"apiKeySource":"none",`, "", 1), vocab.CodeInvalidCredential},
		"helper":        {strings.Replace(initOK, `"none"`, `"apiKeyHelper with spaces"`, 1), vocab.CodeInvalidCredential},
		"session":       {strings.Replace(initOK, `"session_id":"s"`, `"session_id":"other"`, 1), vocab.CodeClientContract},
		"version":       {strings.Replace(initOK, `"2.1.312"`, `"2.1.313"`, 1), vocab.CodeClientContract},
		"built-in tool": {strings.Replace(initOK, `["mcp__dsh__probe"]`, `["mcp__dsh__probe","Bash"]`, 1), vocab.CodeClientContract},
		"no server":     {strings.Replace(initOK, `"connected"`, `"failed"`, 1), vocab.CodeClientContract},
		"bad":           {`{"type":"system","subtype":"init","tools":"x"}`, vocab.CodeClientContract},
	} {
		t.Run(name, func(t *testing.T) {
			_, fin, _ := play(t, lines(tc.line, msgStart))
			if fin.Failure == nil || fin.Failure.Code != tc.code || fin.Failure.Class != vocab.Permanent {
				t.Fatalf("finish = %+v, want %s", fin, tc.code)
			}
			if strings.Contains(fin.Failure.Message, "spaces") {
				t.Fatalf("an unshaped key source reached the message: %q", fin.Failure.Message)
			}
		})
	}
	_, fin, _ := play(t, lines(initOK, initOK))
	if fin.Failure == nil || fin.Failure.Code != vocab.CodeClientContract {
		t.Fatalf("a second init = %+v", fin)
	}
	m := newMapper(expect{sessionID: "s", version: "2.1.312", keySources: []string{"none"}})
	if _, _, err := m.line([]byte(initOK)); !errors.Is(err, errFatal) || m.fatal.Code != vocab.CodeClientContract {
		t.Fatalf("tools offered to a request that declared none: %v %+v", err, m.fatal)
	}
	if _, _, err := m.line([]byte(success)); !errors.Is(err, errFatal) {
		t.Fatal("a mapper kept reading after a fatal line")
	}
}

// TestMapperChunks: tool names lose the listing server's prefix in deltas and
// blocks; usage and the finish are held for the result; counts are recorded;
// a consumer that stops reading stops the mapper; an over-long line and a read
// error end the attempt.
//
//dsh:test DH-T-LLM-21, DH-T-CLAUDECODECLI-2
func TestMapperChunks(t *testing.T) {
	got, fin, m := play(t, lines(append([]string{`{"type":"system","subtype":"api_retry"}`, `{"type":"system","subtype":"status"}`, `{"type":"unknown"}`, "   "}, tool(denied, maxTurns)...)...))
	if fin.Reason != vocab.FinishToolCalls || m.retries != 1 || len(m.denied) != 1 || m.model != "m" || m.lines != 12 {
		t.Fatalf("finish %+v retries %d denied %v model %q lines %d", fin, m.retries, m.denied, m.model, m.lines)
	}
	for _, c := range got {
		switch c := c.(type) {
		case vocab.ToolCallDelta:
			if c.Name != "" && c.Name != "probe" {
				t.Fatalf("delta name %q", c.Name)
			}
		case vocab.BlockEnd:
			if c.Block.ToolUse == nil || c.Block.ToolUse.Name != "probe" {
				t.Fatalf("block %+v", c.Block)
			}
		case vocab.UsageChunk, vocab.Finish:
			t.Fatalf("%T was yielded before the result", c)
		}
	}
	if u := m.usage(decoded{}); u != nil {
		t.Fatalf("usage without a result usage = %+v", u)
	}
	held := &vocab.Usage{Input: 1}
	if u := m.usage(decoded{usage: held}); u != held {
		t.Fatal("the message's usage was not the fallback")
	}

	m2 := newMapper(expect{sessionID: "s", version: "2.1.312", tools: true, keySources: []string{"none"}})
	n := 0
	if _, reading := m2.stream(lines(text(endTurn, msgStop, success)...), func(vocab.Chunk) bool { n++; return n < 2 }); reading {
		t.Fatal("a consumer that stopped was read past")
	}

	tooLong := func() func() ([]byte, error) {
		first := true
		return func() ([]byte, error) {
			if first {
				first = false
				return []byte(initOK), nil
			}
			return nil, clientroute.ErrLineTooLong
		}
	}
	if _, fin, _ := play(t, tooLong()); fin.Failure == nil || fin.Failure.Code != vocab.CodeClientContract {
		t.Fatalf("an over-long line = %+v", fin)
	}
	after := lines(text(endTurn, msgStop)...)
	afterLong := func() ([]byte, error) {
		if b, err := after(); err == nil {
			return b, nil
		}
		return nil, clientroute.ErrLineTooLong
	}
	if _, fin, _ := play(t, afterLong); fin.Failure == nil || fin.Failure.Code != vocab.CodeClientContract {
		t.Fatalf("an over-long line after the message = %+v", fin)
	}
	broken := lines(initOK)
	readErr := func() ([]byte, error) {
		if b, err := broken(); err == nil {
			return b, nil
		}
		return nil, errors.New("pipe broke")
	}
	if _, fin, _ := play(t, readErr); fin.Failure == nil || fin.Failure.Code != vocab.CodeStreamClosed {
		t.Fatalf("a read error before any result = %+v", fin)
	}
	m3 := newMapper(expect{sessionID: "s", version: "2.1.312", tools: true, keySources: []string{"none"}})
	m3.result = &resultLine{Subtype: "success"}
	if fin := m3.decide(decoded{err: errors.New("pipe broke")}, 0, false); fin.Failure == nil || fin.Failure.Code != vocab.CodeTransport {
		t.Fatalf("an unclassified read error = %+v", fin)
	}
	if fin := m3.decide(decoded{}, 1, false); fin.Failure == nil || fin.Failure.Code != vocab.CodeClientContract {
		t.Fatalf("a listing-server call = %+v", fin)
	}
	m4 := newMapper(expect{})
	if fin := m4.decide(decoded{}, 0, true); fin.Failure == nil || fin.Failure.Code != vocab.CodeClientUpgradeRequired {
		t.Fatalf("a refused argument = %+v", fin)
	}
	if clip(strings.Repeat("é", 100)) == strings.Repeat("é", 100) || !strings.HasSuffix(clip(strings.Repeat("x", 81)), "…") {
		t.Fatal("clip does not bound")
	}
}

// TestClassify: every row of the structured failure table of claude-code-cli.md
// sec. 5.4 gives its code, class and status.
//
//dsh:test DH-T-LLM-22, DH-T-CLAUDECODECLI-3
func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		status int
		code   vocab.Code
		class  vocab.Class
	}{
		{errAuthFailed, 0, vocab.CodeSignInRequired, vocab.Permanent},
		{errOrgNotAllowed, 0, vocab.CodeSignInRequired, vocab.Permanent},
		{errAuthFailed, 401, vocab.CodeAuth, vocab.Permanent},
		{errAuthFailed, 403, vocab.CodeAuth, vocab.Permanent},
		{errCloudCredentials, 0, vocab.CodeAuth, vocab.Permanent},
		{errRateLimit, 429, vocab.CodeRateLimit, vocab.Transient},
		{errOverloaded, 529, vocab.CodeServer, vocab.Transient},
		{errServer, 500, vocab.CodeServer, vocab.Transient},
		{errBilling, 0, vocab.CodeQuota, vocab.Permanent},
		{errAccountOnHold, 0, vocab.CodeAccountQuota, vocab.Permanent},
		{errInvalidRequest, 400, vocab.CodeInvalidRequest, vocab.Permanent},
		{errModelNotFound, 404, vocab.CodeUnknownModel, vocab.Permanent},
		{"unknown", 418, vocab.HTTPCode(418), vocab.Permanent},
		{"unknown", 502, vocab.HTTPCode(502), vocab.Transient},
		{"unknown", 0, vocab.CodeServer, vocab.Transient},
		{"", 0, vocab.CodeServer, vocab.Transient},
		{"", 99, vocab.CodeServer, vocab.Transient},
	} {
		fin := classify(tc.kind, tc.status)
		f := fin.Failure
		if fin.Reason != vocab.FinishError || f == nil || f.Code != tc.code || f.Class != tc.class || f.Validate() != nil {
			t.Errorf("classify(%q, %d) = %+v, want %s %s", tc.kind, tc.status, f, tc.code, tc.class)
			continue
		}
		if wantStatus := tc.status; wantStatus >= 100 && f.Status != wantStatus {
			t.Errorf("classify(%q, %d) status %d", tc.kind, tc.status, f.Status)
		}
	}
	if fin := classify(errMaxOutputTokens, 0); fin.Reason != vocab.FinishMaxTokens || fin.Failure != nil {
		t.Errorf("max_output_tokens = %+v", fin)
	}
}

// TestArgs: the argument list is claude-code-cli.md sec. 5.1 in a fixed order,
// the MCP configuration only with tools and the effort only when set; no
// argument holds a character a .cmd shim refuses.
//
//dsh:test DH-T-LLM-20, DH-T-CLAUDECODECLI-1
func TestArgs(t *testing.T) {
	d := clientroute.AttemptPlaceholder
	got := Args(ArgsSpec{Dir: d, Model: "sonnet", SessionID: "sid", Tools: true, Effort: "high"})
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--system-prompt-file", d + sep + "system.txt", "--tools", "", "--strict-mcp-config", "--mcp-config", d + sep + "mcp.json",
		"--permission-mode", "dontAsk", "--permission-prompts", "none", "--max-turns", "1", "--no-session-persistence",
		"--session-id", "sid", "--setting-sources", "local", "--model", "sonnet", "--effort", "high"}
	if !slices.Equal(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
	plain := Args(ArgsSpec{Dir: d, Model: "sonnet", SessionID: "sid"})
	if slices.Contains(plain, "--mcp-config") || slices.Contains(plain, "--effort") || len(plain) != len(want)-5 {
		t.Fatalf("args without tools or effort = %q", plain)
	}
	for _, a := range got {
		if strings.ContainsAny(a, "\"%!\r\n") {
			t.Fatalf("argument %q holds a character a .cmd shim refuses", a)
		}
	}
	spec := Env()
	if err := spec.Check(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if !slices.Contains(spec.Remove, n) {
			t.Errorf("%s is not removed by name", n)
		}
	}
	if !VersionPattern.MatchString("2.1.312 (Claude Code)") || VersionPattern.MatchString("2.1.312") || Floor.Less(Floor) || Checked.Less(Floor) {
		t.Fatal("the version facts disagree")
	}
}
