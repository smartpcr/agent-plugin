package claudecode

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/wire/anthropic"
)

// ToolPrefix is how the client names a tool of the listing server "dsh".
const ToolPrefix = "mcp__dsh__"

// expect is what the stream must agree with: the values the adapter chose or
// probed for the attempt.
type expect struct {
	sessionID  string
	version    string
	tools      bool
	keySources []string
}

// usageJSON is the Messages usage the result line carries.
type usageJSON struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u usageJSON) usage() vocab.Usage {
	v := vocab.Usage{Input: max(u.InputTokens, 0), Output: max(u.OutputTokens, 0),
		CacheWrite: max(u.CacheCreationInputTokens, 0), CacheRead: max(u.CacheReadInputTokens, 0)}
	v.Total = v.Input + v.CacheRead + v.CacheWrite + v.Output
	return v
}

// resultLine is the fields of the result line the route reads (SDKResultMessage).
type resultLine struct {
	Subtype           string     `json:"subtype"`
	IsError           bool       `json:"is_error"`
	NumTurns          int        `json:"num_turns"`
	StopReason        string     `json:"stop_reason"`
	APIErrorStatus    *int       `json:"api_error_status"`
	Usage             *usageJSON `json:"usage"`
	PermissionDenials []struct {
		ToolUseID string `json:"tool_use_id"`
	} `json:"permission_denials"`
}

// errFatal ends the stream: the mapper holds the failure.
var errFatal = errors.New("claudecode: the attempt cannot continue")

// mapper reads the client's lines (claude-code-cli.md sec. 5.2): it checks and
// records every line that is not a stream_event, and hands each stream_event's
// raw Messages event to the decoder. Unknown line types and fields are ignored.
type mapper struct {
	exp   expect
	lines int

	inited   bool
	model    string
	retries  int
	denied   map[string]bool // tool_use ids the client denied
	results  map[string]bool // tool_use ids of the denial results the client fed back
	uses     map[string]bool // tool_use ids of the assistant lines
	errKind  string          // the first assistant error kind
	result   *resultLine
	fatal    *vocab.Failure
	started  bool // a message_start was seen
	finished bool // its message_stop was seen
}

func newMapper(exp expect) *mapper {
	return &mapper{exp: exp, denied: map[string]bool{}, results: map[string]bool{}, uses: map[string]bool{}}
}

func (m *mapper) fail(f *vocab.Failure) error {
	if m.fatal == nil {
		m.fatal = f
	}
	return errFatal
}

// envelope is the common part of every line.
type envelope struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Event   json.RawMessage `json:"event"`
}

// line takes one line; for a stream_event it returns the raw event and its
// type for the decoder. A fatal finding returns errFatal.
func (m *mapper) line(b []byte) (json.RawMessage, string, error) {
	if m.fatal != nil {
		return nil, "", errFatal
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, "", nil
	}
	m.lines++
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, "", m.fail(breach("a line of the client's output is not a JSON object"))
	}
	switch env.Type {
	case "system":
		switch env.Subtype {
		case "init":
			return nil, "", m.init(b)
		case "api_retry":
			m.retries++
		case "permission_denied":
			var d struct {
				ToolUseID string `json:"tool_use_id"`
			}
			_ = json.Unmarshal(b, &d)
			if d.ToolUseID != "" {
				m.denied[d.ToolUseID] = true
			}
		}
	case "stream_event":
		return m.streamEvent(env.Event)
	case "assistant":
		return nil, "", m.assistant(b)
	case "user":
		return nil, "", m.user(b)
	case "result":
		if m.result != nil {
			return nil, "", m.fail(breach("the client printed a second result"))
		}
		var r resultLine
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, "", m.fail(breach("the client's result line does not decode"))
		}
		m.result = &r
		for _, d := range r.PermissionDenials {
			if d.ToolUseID != "" {
				m.denied[d.ToolUseID] = true
			}
		}
	}
	return nil, "", nil
}

// sourceName is a key source shown in a failure: a variable or helper name the
// client reported, kept only when it looks like one.
var sourceName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

func (m *mapper) init(b []byte) error {
	var in struct {
		SessionID  string   `json:"session_id"`
		Version    string   `json:"claude_code_version"`
		Model      string   `json:"model"`
		KeySource  *string  `json:"apiKeySource"`
		Tools      []string `json:"tools"`
		MCPServers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"mcp_servers"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return m.fail(breach("the client's init line does not decode"))
	}
	if m.inited {
		return m.fail(breach("the client printed a second init line"))
	}
	m.inited = true
	if in.KeySource == nil || !slices.Contains(m.exp.keySources, *in.KeySource) {
		shown := "absent"
		if in.KeySource != nil && sourceName.MatchString(*in.KeySource) {
			shown = *in.KeySource
		}
		return m.fail(clientroute.Fail(vocab.CodeInvalidCredential,
			"claude-code: the client authenticates with a source other than its sign-in (apiKeySource %s); remove that key or helper", shown))
	}
	if in.SessionID != m.exp.sessionID {
		return m.fail(breach("the client runs session %q, not the session the harness chose", clip(in.SessionID)))
	}
	if in.Version != m.exp.version {
		return m.fail(breach("the client reports version %q, not the probed %s", clip(in.Version), m.exp.version))
	}
	m.model = clip(in.Model)
	for _, t := range in.Tools {
		if !m.exp.tools || !strings.HasPrefix(t, ToolPrefix) {
			return m.fail(breach("the client offers the model a tool the harness did not declare (%q)", clip(t)))
		}
	}
	if m.exp.tools && !slices.ContainsFunc(in.MCPServers, func(s struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}) bool {
		return s.Name == "dsh" && s.Status == "connected"
	}) {
		return m.fail(breach("the listing server is not connected, so the model cannot see the harness tools"))
	}
	return nil
}

func (m *mapper) streamEvent(ev json.RawMessage) (json.RawMessage, string, error) {
	if !m.inited {
		return nil, "", m.fail(breach("the client streamed before its init line"))
	}
	var e struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(ev, &e); err != nil || e.Type == "" {
		return nil, "", m.fail(breach("a stream_event line carries no Messages event"))
	}
	switch e.Type {
	case "message_start":
		if m.started {
			return nil, "", m.fail(breach("the client made a second model request inside one attempt"))
		}
		m.started = true
	case "message_stop":
		m.finished = true
	}
	return ev, e.Type, nil
}

// content is the part of a message the route reads.
type content struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
}

func (m *mapper) assistant(b []byte) error {
	var a struct {
		Message struct {
			Content []content `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return m.fail(breach("an assistant line does not decode"))
	}
	if a.Error != "" && m.errKind == "" {
		m.errKind = a.Error
	}
	for _, c := range a.Message.Content {
		if c.Type != "tool_use" {
			continue
		}
		if !strings.HasPrefix(c.Name, ToolPrefix) {
			return m.fail(breach("the model called a tool the harness did not declare (%q)", clip(c.Name)))
		}
		m.uses[c.ID] = true
	}
	return nil
}

func (m *mapper) user(b []byte) error {
	var u struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return m.fail(breach("a user line does not decode"))
	}
	var parts []content
	if json.Unmarshal(u.Message.Content, &parts) != nil {
		return nil // a text echo, not a tool result
	}
	for _, c := range parts {
		if c.Type != "tool_result" {
			continue
		}
		if !c.IsError {
			return m.fail(breach("a tool ran inside the client (call %q has a result that is not a denial)", clip(c.ToolUseID)))
		}
		m.results[c.ToolUseID] = true
	}
	return nil
}

func clip(s string) string {
	const n = 80
	s = strings.ToValidUTF8(s, "?")
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "") + "…"
	}
	return s
}

// feed is the decoder's input: one event-stream frame per stream_event line.
// Every other line is handled by the mapper as it is read, in stream order.
type feed struct {
	next func() ([]byte, error)
	m    *mapper
	buf  []byte
	err  error
}

func (f *feed) Read(p []byte) (int, error) {
	for len(f.buf) == 0 {
		if f.err != nil {
			return 0, f.err
		}
		line, err := f.next()
		if err != nil {
			if errors.Is(err, clientroute.ErrLineTooLong) {
				err = f.m.fail(breach("a line of the client's output is over the bound"))
			}
			f.err = err
			continue
		}
		ev, typ, err := f.m.line(line)
		if err != nil {
			f.err = err
			continue
		}
		if ev != nil {
			f.buf = append(append(append(append([]byte("event: "), typ...), "\ndata: "...), ev...), "\n\n"...)
		}
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

// decoded is what the decoder made of the stream.
type decoded struct {
	finish   *vocab.Finish // the message's own finish, at its message_stop
	usage    *vocab.Usage  // the message's usage
	err      error         // a decoder failure, nil at message_stop or a clean end
	toolUses []string      // the tool calls of the message, harness names
}

// stream drives the lines through the mapper and the Messages decoder, yields
// every chunk but usage and the finish (the route settles those from the result
// line), and reads the rest of the output once the message has ended. It
// returns false when the consumer stopped reading.
func (m *mapper) stream(next func() ([]byte, error), yield func(vocab.Chunk) bool) (decoded, bool) {
	var out decoded
	f := &feed{next: next, m: m}
	for ch, err := range anthropic.Decode(f, nil) {
		if err != nil {
			out.err = err
			break
		}
		switch c := ch.(type) {
		case vocab.UsageChunk:
			u := c.Usage
			out.usage = &u
			continue
		case vocab.Finish:
			fin := c
			out.finish = &fin
			continue
		case vocab.ToolCallDelta:
			if c.Name != "" {
				name, ok := strings.CutPrefix(c.Name, ToolPrefix)
				if !ok || name == "" {
					m.fail(breach("the model called a tool the harness did not declare (%q)", clip(c.Name)))
					return out, true
				}
				c.Name = name
			}
			ch = c
		case vocab.BlockEnd:
			if c.Block.Kind == vocab.KindToolUse {
				tu := c.Block.ToolUse
				name, ok := strings.CutPrefix(tu.Name, ToolPrefix)
				if !ok || name == "" {
					m.fail(breach("the model called a tool the harness did not declare (%q)", clip(tu.Name)))
					return out, true
				}
				c.Block = vocab.ToolUseBlock(tu.ID, name, tu.Args)
				out.toolUses = append(out.toolUses, tu.ID)
			}
			ch = c
		}
		if !yield(ch) {
			return out, false
		}
	}
	// After the message: the remaining lines (denials, the result) are read
	// and checked; a further stream_event is a second model request.
	for m.fatal == nil && f.err == nil {
		line, err := next()
		if err != nil {
			if errors.Is(err, clientroute.ErrLineTooLong) {
				m.fail(breach("a line of the client's output is over the bound"))
			} else if !errors.Is(err, io.EOF) {
				f.err = err
			}
			break
		}
		if _, _, err := m.line(line); err != nil {
			break
		}
	}
	return out, true
}

// decide is the attempt's finish from everything read (claude-code-cli.md
// sec. 5.2 and 5.4); calls is the listing server's count of tools/call (-1 when
// its record cannot be read), unknownOption whether standard error held the
// client's "unknown option" marker.
func (m *mapper) decide(out decoded, calls int, unknownOption bool) vocab.Finish {
	errorFinish := func(f *vocab.Failure) vocab.Finish { return vocab.Finish{Reason: vocab.FinishError, Failure: f} }
	switch {
	case m.fatal != nil:
		return errorFinish(m.fatal)
	case calls != 0:
		return errorFinish(breach("the listing server was asked to run a tool, or its record cannot be read"))
	case m.result == nil && m.lines == 0 && unknownOption:
		return errorFinish(clientroute.Fail(vocab.CodeClientUpgradeRequired,
			"claude-code: the client refused an argument of this route; upgrade the client (at least %s)", Floor))
	case m.result == nil:
		return errorFinish(clientroute.Fail(vocab.CodeStreamClosed, "claude-code: the client's output ended without a result"))
	}
	if out.err != nil {
		var f *vocab.Failure
		switch {
		case errors.Is(out.err, errFatal):
			return errorFinish(m.fatal)
		case !errors.As(out.err, &f):
			return errorFinish(clientroute.Fail(vocab.CodeTransport, "claude-code: reading the client's output failed"))
		case f.Code == vocab.CodeStreamClosed && !m.started:
			// No model output at all: the result line says why.
		case f.Code == vocab.CodeStreamClosed:
			return errorFinish(breach("the client's result arrived inside an unfinished message"))
		case f.Code == vocab.CodeMalformedResponse:
			return errorFinish(breach("a stream_event the Messages decoder cannot place"))
		default:
			return errorFinish(f)
		}
	}
	for id := range m.results {
		if !m.denied[id] {
			return errorFinish(breach("the client fed back a denial of call %q it did not report denied", clip(id)))
		}
	}
	for id := range m.uses {
		if !slices.Contains(out.toolUses, id) {
			return errorFinish(breach("the client reported call %q that was not streamed", clip(id)))
		}
	}
	r := m.result
	status := 0
	if r.APIErrorStatus != nil {
		status = *r.APIErrorStatus
	}
	switch {
	case r.Subtype == "error_max_turns" && m.errKind == "":
		if out.finish == nil || out.finish.Reason != vocab.FinishToolCalls || len(out.toolUses) == 0 {
			return errorFinish(breach("the client stopped at its turn limit without a tool call to return"))
		}
		for _, id := range out.toolUses {
			if !m.denied[id] {
				return errorFinish(breach("tool call %q reached the turn limit without being denied", clip(id)))
			}
		}
		return vocab.Finish{Reason: vocab.FinishToolCalls}
	case m.errKind != "" || r.IsError:
		fin := classify(m.errKind, status)
		if fin.Reason == vocab.FinishMaxTokens && !m.finished {
			return errorFinish(breach("the client reported the output cap without a finished message"))
		}
		return fin
	case r.Subtype != "success":
		return errorFinish(breach("the client's result has subtype %q, which this route does not know", clip(r.Subtype)))
	case !m.started:
		return vocab.Finish{Reason: vocab.FinishStop} // the runtime makes this llm/empty-response
	case out.finish == nil:
		return errorFinish(breach("the client reported success inside an unfinished message"))
	case out.finish.Reason == vocab.FinishToolCalls || len(out.toolUses) > 0:
		return errorFinish(breach("the client reported success after a tool call it should have stopped at"))
	case out.finish.Reason == vocab.FinishError:
		return *out.finish
	case out.finish.Reason == vocab.FinishMaxTokens || r.StopReason == "max_tokens":
		return vocab.Finish{Reason: vocab.FinishMaxTokens}
	}
	return vocab.Finish{Reason: vocab.FinishStop}
}

// usage is the attempt's usage: the result line's, else the message's.
func (m *mapper) usage(out decoded) *vocab.Usage {
	if m.result != nil && m.result.Usage != nil {
		u := m.result.Usage.usage()
		return &u
	}
	return out.usage
}
