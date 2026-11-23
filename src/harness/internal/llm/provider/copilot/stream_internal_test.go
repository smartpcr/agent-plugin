package copilot

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

// Synthetic lines, pending the 16.5.9 recording (github-copilot-cli.md sec.
// 5.2 and 5.4).
const (
	turn       = `{"type":"assistant.turn_start","data":{"turnId":"0"}}`
	turnEnd    = `{"type":"assistant.turn_end","data":{}}`
	delta1     = `{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"hi"}}`
	message1   = `{"type":"assistant.message","data":{"messageId":"m1","content":"hi","toolRequests":[]}}`
	result0    = `{"type":"result","exitCode":0}`
	result1    = `{"type":"result","exitCode":1}`
	authError  = `{"type":"session.error","data":{"errorType":"authentication","message":"x"}}`
	rateError  = `{"type":"session.error","data":{"errorType":"rate_limit"}}`
	otherError = `{"type":"session.error","data":{"errorType":"something_else"}}`
)

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

func toolMsg(reqs string) string {
	return `{"type":"assistant.message","data":{"messageId":"m1","content":"","toolRequests":[` + reqs + `]}}`
}

// play runs the lines through a mapper declaring the probe tool and returns
// the chunks it yielded, its finish for exit and whether it stopped the tree.
func play(t *testing.T, exit int, ls ...string) ([]vocab.Chunk, vocab.Finish, *mapper, bool) {
	t.Helper()
	m := newMapper([]string{"probe"})
	var got []vocab.Chunk
	stopped := false
	if !m.stream(lines(ls...), func() { stopped = true }, func(c vocab.Chunk) bool { got = append(got, c); return true }) {
		t.Fatal("the consumer did not stop")
	}
	return got, m.decide(exit, 0), m, stopped
}

func code(f vocab.Finish) vocab.Code {
	if f.Failure == nil {
		return ""
	}
	return f.Failure.Code
}

// TestMapperChunks: deltas open a block per id and stream into it; the
// message or reasoning line ends it, with its content authoritative; content
// with no deltas is streamed as one block; a block named by no id is the one
// open block; unknown types, session.* and blank lines give no chunk; a second
// turn is a client-internal request; no text at all is a plain stop.
//
//dsh:test DH-T-LLM-25, DH-T-COPILOTCLI-2
func TestMapperChunks(t *testing.T) {
	chunks, fin, m, stopped := play(t, 0, "", `{"type":"session.start","data":{}}`, turn,
		`{"type":"assistant.reasoning_delta","data":{"reasoningId":"r1","deltaContent":"think"}}`,
		`{"type":"assistant.reasoning","data":{}}`,
		`{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":""}}`,
		`{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"draft"}}`,
		`{"type":"assistant.message","data":{"content":"final"}}`,
		`{"type":"assistant.reasoning","data":{"reasoningId":"r2","content":"whole"}}`,
		`{"type":"assistant.message","data":{"messageId":"m2","content":"second"}}`,
		`{"type":"assistant.message","data":{"messageId":"m3"}}`,
		`{"type":"assistant.intent","data":{"intent":"x"}}`, turn, `{"type":"new.kind"}`, turnEnd, result0,
		`{"type":"later.unknown"}`)
	if fin.Reason != vocab.FinishStop || stopped || m.retries != 1 {
		t.Fatalf("finish = %+v, stopped %v, retries %d", fin, stopped, m.retries)
	}
	var ends []string
	for _, c := range chunks {
		if e, ok := c.(vocab.BlockEnd); ok {
			switch e.Block.Kind {
			case vocab.KindText:
				ends = append(ends, "text:"+e.Block.Text.Text)
			case vocab.KindReasoning:
				ends = append(ends, "reasoning:"+e.Block.Reasoning.Text)
			}
		}
	}
	if want := []string{"reasoning:think", "text:final", "reasoning:whole", "text:second"}; !slices.Equal(ends, want) {
		t.Fatalf("block ends %v, want %v", ends, want)
	}
	if _, fin, _, _ := play(t, 0, turn, `{"type":"assistant.message","data":{"content":""}}`, result0); fin.Reason != vocab.FinishStop {
		t.Fatalf("an empty answer = %+v, want the stop the runtime makes empty", fin)
	}
}

// TestMapperToolRequests: a tool request stops the tree before its chunks are
// yielded and no later line is read; the client's MCP name and the bare name
// both map to the declared tool; arguments may be an object, a string holding
// one or absent; every other shape fails closed.
//
//dsh:test DH-T-LLM-25, DH-T-COPILOTCLI-2
func TestMapperToolRequests(t *testing.T) {
	m := newMapper([]string{"probe"})
	stopped := false
	var got []vocab.Chunk
	m.stream(lines(turn, toolMsg(`{"toolCallId":"c1","name":"dsh-probe","arguments":{"a":1}},{"toolCallId":"c2","name":"probe","arguments":"{\"b\":2}"},{"toolCallId":"c3","name":"probe"}`),
		`{"type":"tool.execution_start","data":{}}`), func() { stopped = true }, func(c vocab.Chunk) bool {
		if !stopped {
			t.Fatal("a chunk of the tool message was yielded before the tree was stopped")
		}
		got = append(got, c)
		return true
	})
	if fin := m.decide(-1, 0); fin.Reason != vocab.FinishToolCalls || m.fatal != nil {
		t.Fatalf("finish = %+v (%v)", fin, m.fatal)
	}
	var calls []string
	for _, c := range got {
		if e, ok := c.(vocab.BlockEnd); ok && e.Block.Kind == vocab.KindToolUse {
			calls = append(calls, e.Block.ToolUse.ID+"/"+e.Block.ToolUse.Name+"/"+string(e.Block.ToolUse.Args))
		}
	}
	if want := []string{`c1/probe/{"a":1}`, `c2/probe/{"b":2}`, `c3/probe/{}`}; !slices.Equal(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	for name, req := range map[string]string{
		"undeclared":    `{"toolCallId":"c1","name":"dsh-shell","arguments":{}}`,
		"no id":         `{"name":"dsh-probe","arguments":{}}`,
		"empty id":      `{"toolCallId":"","name":"dsh-probe"}`,
		"no name":       `{"toolCallId":"c1"}`,
		"array args":    `{"toolCallId":"c1","name":"dsh-probe","arguments":[1]}`,
		"string args":   `{"toolCallId":"c1","name":"dsh-probe","arguments":"[1]"}`,
		"bad string":    `{"toolCallId":"c1","name":"dsh-probe","arguments":"{"}`,
		"number args":   `{"toolCallId":"c1","name":"dsh-probe","arguments":7}`,
		"duplicate ids": `{"toolCallId":"c1","name":"dsh-probe"},{"toolCallId":"c1","name":"dsh-probe"}`,
	} {
		if _, fin, _, stopped := play(t, 0, turn, toolMsg(req), result0); code(fin) != vocab.CodeClientContract || stopped {
			t.Errorf("%s: finish = %+v, stopped %v", name, fin, stopped)
		}
	}
	nm := newMapper(nil)
	nm.line([]byte(toolMsg(`{"toolCallId":"c1","name":"dsh-probe"}`)))
	if nm.fatal == nil || nm.fatal.Code != vocab.CodeClientContract {
		t.Fatalf("a tool request with no tool declared = %v", nm.fatal)
	}
}

// TestMapperFailsClosed: every line the route cannot place fails the attempt
// with llm/client-contract: not a JSON object, no type, a placed type without
// its data or fields, a delta continuing an ended block, a block ended twice or
// without saying which, a tool that ran, a line after the result, a second
// result, a result without its exit code or inside an unfinished message, an
// over-long line; no result at all is a breach when the client exited 0 and the
// exit-status failure otherwise.
//
//dsh:test DH-T-LLM-26, DH-T-COPILOTCLI-3
func TestMapperFailsClosed(t *testing.T) {
	for name, ls := range map[string][]string{
		"not json":         {"hello"},
		"array":            {`[1]`},
		"null":             {`null`},
		"no type":          {`{"data":{}}`},
		"empty type":       {`{"type":""}`},
		"no data":          {`{"type":"assistant.message_delta"}`},
		"data not object":  {`{"type":"assistant.message","data":"hi"}`},
		"bad data field":   {`{"type":"assistant.message","data":{"content":7}}`},
		"no message id":    {`{"type":"assistant.message_delta","data":{"deltaContent":"x"}}`},
		"null delta":       {`{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":null}}`},
		"no reasoning id":  {`{"type":"assistant.reasoning_delta","data":{"deltaContent":"x"}}`},
		"bad reasoning":    {`{"type":"assistant.reasoning","data":[]}`},
		"bad error line":   {`{"type":"session.error","data":1}`},
		"delta after end":  {delta1, message1, delta1},
		"ended twice":      {delta1, message1, message1},
		"which message":    {delta1, `{"type":"assistant.message_delta","data":{"messageId":"m2","deltaContent":"x"}}`, `{"type":"assistant.message","data":{"content":"x"}}`},
		"tool ran":         {turn, `{"type":"tool.execution_start","data":{"toolName":"shell"}}`},
		"tool completed":   {`{"type":"tool.execution_complete","data":{}}`},
		"after result":     {delta1, message1, result0, delta1},
		"second result":    {result0, result0},
		"no exit code":     {`{"type":"result"}`},
		"unfinished":       {delta1, result0},
		"unfinished think": {`{"type":"assistant.reasoning_delta","data":{"reasoningId":"r1","deltaContent":"x"}}`, result0},
		"no result":        {delta1, message1},
	} {
		if _, fin, _, _ := play(t, 0, ls...); code(fin) != vocab.CodeClientContract || fin.Reason != vocab.FinishError {
			t.Errorf("%s: finish = %+v", name, fin)
		}
	}
	m := newMapper(nil)
	m.stream(func() ([]byte, error) { return nil, clientroute.ErrLineTooLong }, func() {}, func(vocab.Chunk) bool { return true })
	if fin := m.decide(0, 0); code(fin) != vocab.CodeClientContract || !strings.Contains(fin.Failure.Message, "over the bound") {
		t.Fatalf("a long line = %+v", fin)
	}
	if _, fin, _, _ := play(t, -1, delta1, message1); code(fin) != vocab.CodeClientContract || !strings.Contains(fin.Failure.Message, "without a result") {
		t.Fatalf("an unknown exit with no result = %+v", fin)
	}
	if _, fin, _, _ := play(t, 2); code(fin) != vocab.CodeClientContract || !strings.Contains(fin.Failure.Message, "status 2") {
		t.Fatalf("exit 2 with nothing = %+v", fin)
	}
	if safeType("tool.execution_start") != "tool.execution_start" || safeType("tool.execution gho_X") != "(unnamed)" {
		t.Fatal("safeType")
	}
	if c := clip(strings.Repeat("é", 100)); len(c) > 84 || !strings.HasSuffix(c, "…") {
		t.Fatalf("clip = %q", c)
	}
}

// TestMapperFailures: the structured error rows of github-copilot-cli.md sec.
// 5.4 (inference: session.error with data.errorType): authentication before any
// assistant line is llm/sign-in-required, after one llm/auth; a rate limit is
// Transient; an unclassified kind falls to the exit status; an error the
// client recovered from (an answer and exit 0) is no failure, one with no
// answer is; a failing read is llm/transport; a tools/call record is a breach,
// an unreadable record too.
//
//dsh:test DH-T-LLM-26, DH-T-COPILOTCLI-3
func TestMapperFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		ls   []string
		exit int
		code vocab.Code
	}{
		"signed out":          {[]string{authError, result1}, 1, vocab.CodeSignInRequired},
		"signed out, no rest": {[]string{authError}, 1, vocab.CodeSignInRequired},
		"rejected":            {[]string{turn, authError, result1}, 1, vocab.CodeAuth},
		"rate limit":          {[]string{turn, rateError, result1}, 1, vocab.CodeRateLimit},
		"unclassified":        {[]string{turn, otherError, result1}, 1, vocab.CodeClientContract},
		"no kind":             {[]string{`{"type":"session.error","data":{}}`, result1}, 1, vocab.CodeClientContract},
		"recovered":           {[]string{turn, rateError, turn, delta1, message1, result0}, 0, ""},
		"error, no answer":    {[]string{turn, rateError, result0}, 0, vocab.CodeRateLimit},
		"exit, no error":      {[]string{turn, delta1, message1, result1}, 1, vocab.CodeClientContract},
	} {
		_, fin, _, _ := play(t, tc.exit, tc.ls...)
		if code(fin) != tc.code {
			t.Errorf("%s: finish = %+v, want %q", name, fin, tc.code)
		}
	}
	if fin, _ := classifyError("rate_limit", false); fin.Failure.Class != vocab.Transient {
		t.Fatal("a rate limit is not Transient")
	}
	m := newMapper(nil)
	m.stream(func() ([]byte, error) { return nil, errors.New("closed") }, func() {}, func(vocab.Chunk) bool { return true })
	if fin := m.decide(0, 0); code(fin) != vocab.CodeTransport {
		t.Fatalf("a failing read = %+v", fin)
	}
	for _, calls := range []int{1, -1} {
		if fin := newMapper(nil).decide(0, calls); code(fin) != vocab.CodeClientContract {
			t.Fatalf("calls %d = %+v", calls, fin)
		}
	}
	stop := newMapper(nil)
	if stop.stream(lines(turn, delta1), func() {}, func(vocab.Chunk) bool { return false }) {
		t.Fatal("a consumer that stopped was read on")
	}
}

// TestArgs: the argument list is sec. 5.1's in a fixed order, the MCP
// configuration only with tools, an empty tool list without, the effort only
// when set; no argument holds a character a .cmd shim refuses. The environment
// rules hold the credential names, and the version facts agree.
//
//dsh:test DH-T-LLM-24, DH-T-COPILOTCLI-1
func TestArgs(t *testing.T) {
	d := clientroute.AttemptPlaceholder
	sep := string(filepath.Separator)
	got := Args(ArgsSpec{Dir: d, Model: "gpt-5", SessionID: "sid", Tools: []string{"probe", "other"}, Effort: "high"})
	want := []string{"--no-auto-update", "--output-format", "json", "--stream", "on",
		"--deny-tool", "shell", "--deny-tool", "write", "--deny-tool", "read", "--deny-tool", "url", "--deny-tool", "memory",
		"--disable-builtin-mcps", "--no-ask-user", "--no-custom-instructions", "--disallow-temp-dir",
		"--additional-mcp-config", "@" + d + sep + "mcp.json", "--deny-tool", "dsh",
		"--available-tools", "dsh-probe,dsh-other",
		"--sync-session=off", "--disable-client-telemetry", "--session-id", "sid", "--model", "gpt-5", "--reasoning-effort", "high"}
	if !slices.Equal(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
	plain := Args(ArgsSpec{Dir: d, Model: "gpt-5", SessionID: "sid"})
	if i := slices.Index(plain, "--available-tools"); slices.Contains(plain, "--additional-mcp-config") || slices.Contains(plain, "--reasoning-effort") || i < 0 || plain[i+1] != "" {
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
	for _, n := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_MODEL", "COPILOT_OFFLINE"} {
		if !slices.Contains(spec.Remove, n) {
			t.Errorf("%s is not removed by name", n)
		}
	}
	if !slices.Contains(spec.Set, "COPILOT_AUTO_UPDATE=false") || !slices.Contains(spec.Set, "COPILOT_TELEMETRY_DISABLED=true") {
		t.Errorf("settings = %v", spec.Set)
	}
	for _, ok := range []string{"GitHub Copilot CLI 1.0.115-7.", "GitHub Copilot CLI 1.0.115.", "GitHub Copilot CLI 1.2.3"} {
		if !VersionPattern.MatchString(ok) {
			t.Errorf("%q is not recognised", ok)
		}
	}
	for _, bad := range []string{"Run 'copilot update' to check for updates.", "1.0.115", "GitHub Copilot CLI 1.0", " GitHub Copilot CLI 1.0.115."} {
		if VersionPattern.MatchString(bad) {
			t.Errorf("%q is recognised", bad)
		}
	}
	if Floor.Less(Floor) || Checked.Less(Floor) || !slices.Equal(ProbeArgs, []string{"--version", "--no-auto-update"}) {
		t.Fatal("the version facts disagree")
	}
}
