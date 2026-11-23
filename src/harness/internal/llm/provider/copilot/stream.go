package copilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// The JSONL line types the route places (github-copilot-cli.md sec. 5.2). The
// vendor does not document the schema: the names and fields below are those of
// the lib record, pinned by the fixtures DH-E-COPILOTCLI-1 (plan 16.5.9)
// records. A line of one of these types that lacks a field the route needs
// fails the attempt with llm/client-contract; a line of any other type is
// ignored.
const (
	lineMessageDelta   = "assistant.message_delta"
	lineReasoningDelta = "assistant.reasoning_delta"
	lineReasoning      = "assistant.reasoning"
	lineMessage        = "assistant.message"
	lineTurnStart      = "assistant.turn_start"
	lineTurnEnd        = "assistant.turn_end"
	lineIntent         = "assistant.intent"
	lineResult         = "result"
	// toolRan prefixes the lines of a tool run inside the client
	// (tool.execution_start): a contract breach.
	toolRan = "tool.execution"
)

// placed reports whether the route reads lines of type t.
func placed(t string) bool {
	switch t {
	case lineMessageDelta, lineReasoningDelta, lineReasoning, lineMessage, lineTurnStart, lineTurnEnd, lineIntent, lineResult, errorLine:
		return true
	}
	return strings.HasPrefix(t, toolRan)
}

// block is one content block being streamed.
type block struct {
	index int
	text  strings.Builder
}

// toolRequest is one entry of an assistant message's data.toolRequests.
type toolRequest struct {
	ToolCallID *string         `json:"toolCallId"`
	Name       *string         `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
}

// mapper turns the client's JSONL lines into chunks (github-copilot-cli.md
// sec. 5.2) and records what the finish depends on.
type mapper struct {
	tools map[string]bool // the declared harness tool names

	next      int               // the next block index
	text      map[string]*block // open text blocks, by message id
	reasoning map[string]*block // open reasoning blocks, by reasoning id
	ended     map[string]bool   // ended blocks, "m:"+message id or "r:"+reasoning id
	calls     map[string]bool   // tool call ids returned

	sawText   bool // an assistant message carried text
	assistant bool // an assistant line came before the current one
	turns     int  // assistant.turn_start lines
	retries   int  // turns after the first: client-internal requests

	errFinish  *vocab.Finish // the last error line's finish; nil when it could not be classified
	result     *int          // the result line's exitCode
	toolCalls  int           // > 0 once the route has stopped at a tool request
	readFailed bool          // the output could not be read
	fatal      *vocab.Failure
}

func newMapper(tools []string) *mapper {
	m := &mapper{tools: map[string]bool{}, text: map[string]*block{}, reasoning: map[string]*block{},
		ended: map[string]bool{}, calls: map[string]bool{}}
	for _, t := range tools {
		m.tools[t] = true
	}
	return m
}

func (m *mapper) fail(f *vocab.Failure) []vocab.Chunk {
	if m.fatal == nil {
		m.fatal = f
	}
	return nil
}

// stream reads lines until the output ends, the attempt cannot continue, or an
// assistant message requests a tool. At that message stop is called before its
// chunks are yielded, so the client cannot go on to run the call or send its
// next request, and no later line is read. It returns false when the consumer
// stopped reading.
func (m *mapper) stream(next func() ([]byte, error), stop func(), yield func(vocab.Chunk) bool) bool {
	for m.fatal == nil && m.toolCalls == 0 {
		line, err := next()
		if err != nil {
			if errors.Is(err, clientroute.ErrLineTooLong) {
				m.fail(breach("a line of the client's output is over the bound"))
			} else if !errors.Is(err, io.EOF) {
				m.readFailed = true
			}
			break
		}
		chunks := m.line(line)
		if m.fatal != nil {
			break
		}
		if m.toolCalls > 0 {
			stop()
		}
		for _, c := range chunks {
			if !yield(c) {
				return false
			}
		}
	}
	return true
}

// envelope is the common part of every line.
type envelope struct {
	Type *string         `json:"type"`
	Data json.RawMessage `json:"data"`
}

// data decodes a placed line's data object into v; a missing or non-object
// data is a breach.
func (m *mapper) data(t string, raw json.RawMessage, v any) bool {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) || json.Unmarshal(raw, v) != nil {
		m.fail(breach("a %s line carries no data object this route can read", t))
		return false
	}
	return true
}

// line places one line and returns its chunks.
func (m *mapper) line(b []byte) []vocab.Chunk {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var env envelope
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) || json.Unmarshal(b, &env) != nil {
		return m.fail(breach("a line of the client's output is not a JSON object"))
	}
	if env.Type == nil || *env.Type == "" {
		return m.fail(breach("a line of the client's output carries no type"))
	}
	t := *env.Type
	if !placed(t) {
		return nil // an unknown type, session.* included: no chunk
	}
	if m.result != nil {
		return m.fail(breach("the client printed a %s line after its result", safeType(t)))
	}
	defer func() {
		if strings.HasPrefix(t, "assistant.") {
			m.assistant = true
		}
	}()
	switch t {
	case lineTurnStart:
		m.turns++
		if m.turns > 1 {
			m.retries++
		}
	case lineTurnEnd, lineIntent:
	case lineMessageDelta:
		return m.delta(t, env.Data, "messageId", vocab.KindText)
	case lineReasoningDelta:
		return m.delta(t, env.Data, "reasoningId", vocab.KindReasoning)
	case lineReasoning:
		return m.reasoningEnd(env.Data)
	case lineMessage:
		return m.message(env.Data)
	case errorLine:
		var d struct {
			ErrorType *string `json:"errorType"`
		}
		if !m.data(t, env.Data, &d) {
			return nil
		}
		kind := ""
		if d.ErrorType != nil {
			kind = *d.ErrorType
		}
		m.errFinish = nil
		if fin, ok := classifyError(kind, m.assistant); ok {
			m.errFinish = &fin
		}
	case lineResult:
		var r struct {
			ExitCode *int `json:"exitCode"`
		}
		if json.Unmarshal(b, &r) != nil || r.ExitCode == nil {
			return m.fail(breach("the client's result line carries no exit code"))
		}
		m.result = r.ExitCode
	default: // tool.execution_*
		return m.fail(breach("a tool ran inside the client (a %s line)", safeType(t)))
	}
	return nil
}

// delta is a message or reasoning delta: BlockStart on a new id, then the
// delta.
func (m *mapper) delta(t string, raw json.RawMessage, idField string, kind vocab.BlockKind) []vocab.Chunk {
	var d map[string]json.RawMessage
	if !m.data(t, raw, &d) {
		return nil
	}
	id, ok := str(d[idField])
	if !ok || id == "" {
		return m.fail(breach("a %s line carries no %s", t, idField))
	}
	text, ok := str(d["deltaContent"])
	if !ok {
		return m.fail(breach("a %s line carries no deltaContent", t))
	}
	open, key := m.text, "m:"+id
	if kind == vocab.KindReasoning {
		open, key = m.reasoning, "r:"+id
	}
	if m.ended[key] {
		return m.fail(breach("a %s line continues a block that has ended", t))
	}
	var out []vocab.Chunk
	bl, ok := open[id]
	if !ok {
		bl = &block{index: m.next}
		m.next++
		open[id] = bl
		out = append(out, vocab.BlockStart{Index: bl.index, Kind: kind})
	}
	if text == "" {
		return out
	}
	bl.text.WriteString(text)
	if kind == vocab.KindReasoning {
		return append(out, vocab.ReasoningDelta{Index: bl.index, Text: text})
	}
	return append(out, vocab.TextDelta{Index: bl.index, Text: text})
}

// end closes the block of id in open (the only open one when id is empty), or
// streams content as a whole block when none is open. content, when not
// empty, is the block's final text.
func (m *mapper) end(open map[string]*block, prefix, id string, content *string, kind vocab.BlockKind, what string) []vocab.Chunk {
	if id == "" && len(open) == 1 {
		for k := range open {
			id = k
		}
	}
	if id != "" && m.ended[prefix+id] {
		return m.fail(breach("the client ended a %s that has already ended", what))
	}
	if id == "" && len(open) > 1 {
		return m.fail(breach("the client ended a %s without saying which", what))
	}
	whole := ""
	if content != nil {
		whole = *content
	}
	bl, ok := open[id]
	var out []vocab.Chunk
	if !ok {
		if whole == "" {
			return nil
		}
		bl = &block{index: m.next}
		m.next++
		bl.text.WriteString(whole)
		out = append(out, vocab.BlockStart{Index: bl.index, Kind: kind})
		if kind == vocab.KindReasoning {
			out = append(out, vocab.ReasoningDelta{Index: bl.index, Text: whole})
		} else {
			out = append(out, vocab.TextDelta{Index: bl.index, Text: whole})
		}
	}
	delete(open, id)
	if id != "" {
		m.ended[prefix+id] = true
	}
	text := bl.text.String()
	if whole != "" {
		text = whole
	}
	if kind == vocab.KindText && text != "" {
		m.sawText = true
	}
	if kind == vocab.KindReasoning {
		return append(out, vocab.BlockEnd{Index: bl.index, Block: vocab.ReasoningBlock(text, "", false)})
	}
	return append(out, vocab.BlockEnd{Index: bl.index, Block: vocab.TextBlock(text)})
}

// reasoningEnd is assistant.reasoning: the BlockEnd of the reasoning block.
// Inference: it names the block by data.reasoningId and may carry the whole
// text in data.content.
func (m *mapper) reasoningEnd(raw json.RawMessage) []vocab.Chunk {
	var d struct {
		ReasoningID *string `json:"reasoningId"`
		Content     *string `json:"content"`
	}
	if !m.data(lineReasoning, raw, &d) {
		return nil
	}
	id := ""
	if d.ReasoningID != nil {
		id = *d.ReasoningID
	}
	return m.end(m.reasoning, "r:", id, d.Content, vocab.KindReasoning, "reasoning block")
}

// message is assistant.message: the BlockEnd of its text, then each tool
// request as a whole tool-call block. A request marks the route stopped.
func (m *mapper) message(raw json.RawMessage) []vocab.Chunk {
	var d struct {
		MessageID    *string       `json:"messageId"`
		Content      *string       `json:"content"`
		ToolRequests []toolRequest `json:"toolRequests"`
	}
	if !m.data(lineMessage, raw, &d) {
		return nil
	}
	id := ""
	if d.MessageID != nil {
		id = *d.MessageID
	}
	out := m.end(m.text, "m:", id, d.Content, vocab.KindText, "message")
	if m.fatal != nil || len(d.ToolRequests) == 0 {
		return out
	}
	for _, r := range d.ToolRequests {
		if r.ToolCallID == nil || *r.ToolCallID == "" || r.Name == nil {
			return m.fail(breach("a tool request carries no toolCallId or name"))
		}
		callID := *r.ToolCallID
		if m.calls[callID] {
			return m.fail(breach("the client requested call %q twice", clip(callID)))
		}
		name, ok := m.harnessName(*r.Name)
		if !ok {
			return m.fail(breach("the model called a tool the harness did not declare (%q)", clip(*r.Name)))
		}
		args, ok := arguments(r.Arguments)
		if !ok {
			return m.fail(breach("the arguments of call %q are not a JSON object", clip(callID)))
		}
		m.calls[callID] = true
		i := m.next
		m.next++
		out = append(out,
			vocab.BlockStart{Index: i, Kind: vocab.KindToolUse},
			vocab.ToolCallDelta{Index: i, ID: callID, Name: name, ArgsDelta: string(args)},
			vocab.BlockEnd{Index: i, Block: vocab.ToolUseBlock(callID, name, args)})
	}
	m.toolCalls = len(d.ToolRequests)
	return out
}

// harnessName is the declared tool the client's name stands for: the client's
// MCP form (ToolPrefix + name) or the bare name.
func (m *mapper) harnessName(client string) (string, bool) {
	if name, ok := strings.CutPrefix(client, ToolPrefix); ok && m.tools[name] {
		return name, true
	}
	return client, m.tools[client]
}

// arguments are a tool request's arguments as a JSON object: an object, a
// string holding one, or nothing (an empty object).
func arguments(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return json.RawMessage(`{}`), true
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, false
		}
		raw = bytes.TrimSpace([]byte(s))
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}

// decide is the attempt's finish from everything read (github-copilot-cli.md
// sec. 5.2 and 5.4); exit is the client's exit status (-1 when unknown), calls
// the listing server's count of tools/call (-1 when its record cannot be read).
func (m *mapper) decide(exit, calls int) vocab.Finish {
	switch {
	case m.fatal != nil:
		return vocab.Finish{Reason: vocab.FinishError, Failure: m.fatal}
	case calls != 0:
		return vocab.Finish{Reason: vocab.FinishError, Failure: breach("the listing server was asked to run a tool, or its record cannot be read")}
	case m.toolCalls > 0:
		return vocab.Finish{Reason: vocab.FinishToolCalls}
	case m.readFailed:
		return failed(vocab.CodeTransport, "reading the client's output failed")
	case m.result == nil:
		if m.errFinish != nil {
			return *m.errFinish
		}
		if exit != 0 && exit != -1 {
			return exited(exit)
		}
		return vocab.Finish{Reason: vocab.FinishError, Failure: breach("the client's output ended without a result line")}
	case len(m.text) > 0 || len(m.reasoning) > 0:
		return vocab.Finish{Reason: vocab.FinishError, Failure: breach("the client's result arrived inside an unfinished message")}
	case *m.result != 0:
		if m.errFinish != nil {
			return *m.errFinish
		}
		return exited(*m.result)
	case !m.sawText && m.errFinish != nil:
		return *m.errFinish
	}
	// No text at all is a stop the runtime turns into llm/empty-response.
	return vocab.Finish{Reason: vocab.FinishStop}
}

// str is raw as a JSON string; ok is false for anything else, null included.
func str(raw json.RawMessage) (string, bool) {
	var s string
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// lineType is the shape of a type name shown in a failure.
var lineType = regexp.MustCompile(`^[a-z][a-z_.]{0,63}$`)

// safeType is t when it looks like a line type, so no client text reaches a
// failure (R9).
func safeType(t string) string {
	if lineType.MatchString(t) {
		return t
	}
	return "(unnamed)"
}

func clip(s string) string {
	const n = 80
	s = strings.ToValidUTF8(s, "?")
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "") + "…"
	}
	return s
}
