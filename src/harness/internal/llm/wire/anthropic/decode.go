package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// ErrorClassifier turns an error body (a non-2xx response, or the data of an
// in-stream error event, whose status is 0) into a failure; nil falls back to
// ErrorFailure.
type ErrorClassifier func(status int, h http.Header, body []byte) *vocab.Failure

// ErrorBody is the Messages error envelope.
type ErrorBody struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ParseError reads an error envelope; ok is false when body is not one.
func ParseError(body []byte) (ErrorBody, bool) {
	var e ErrorBody
	if json.Unmarshal(body, &e) != nil || e.Error.Type == "" {
		return ErrorBody{}, false
	}
	return e, true
}

// ErrorCodes maps the dialect's error types onto the closed failure table.
var ErrorCodes = map[string]vocab.Code{
	"authentication_error":  vocab.CodeAuth,
	"permission_error":      vocab.CodeAuth,
	"rate_limit_error":      vocab.CodeRateLimit,
	"overloaded_error":      vocab.CodeServer,
	"api_error":             vocab.CodeServer,
	"timeout_error":         vocab.CodeTimeout,
	"invalid_request_error": vocab.CodeInvalidRequest,
	"request_too_large":     vocab.CodeInvalidRequest,
	"not_found_error":       vocab.CodeInvalidRequest,
	"billing_error":         vocab.CodeQuota,
}

// ErrorFailure classifies by the dialect's error type and falls back to the
// status (transport.StatusFailure) for a type it does not know; an in-stream
// error with an unknown type is llm/server, the vendor failing mid-reply.
func ErrorFailure(status int, h http.Header, body []byte) *vocab.Failure {
	e, ok := ParseError(body)
	if ok {
		if code, known := ErrorCodes[e.Error.Type]; known {
			return transport.Fail(code, "%s: %s", e.Error.Type, clip(e.Error.Message))
		}
	}
	if status == 0 {
		f := transport.Fail(vocab.CodeServer, "the stream reported an error")
		if ok {
			f.Message = "the stream reported error type " + strconv.Quote(e.Error.Type)
		}
		return f
	}
	return transport.StatusFailure(status, h, body)
}

func clip(s string) string {
	const max = 512
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "") + "…"
	}
	return s
}

type usageJSON struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
}

type eventJSON struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage *usageJSON `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Data     string          `json:"data"`
		Input    json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta *struct {
		Type        string          `json:"type"`
		Text        json.RawMessage `json:"text"`
		Thinking    json.RawMessage `json:"thinking"`
		Signature   string          `json:"signature"`
		PartialJSON string          `json:"partial_json"`
		StopReason  string          `json:"stop_reason"`
	} `json:"delta"`
	Usage *usageJSON `json:"usage"`
}

// open is one content block between its start and stop.
type open struct {
	kind      vocab.BlockKind
	text      strings.Builder
	held      string // a trailing high-surrogate escape awaiting its pair
	signature string
	redacted  bool
	id, name  string
	args      strings.Builder
}

// Decode reads a Messages event stream and yields the attempt's chunks: one
// BlockStart, deltas and one BlockEnd (authoritative) per text, thinking and
// tool_use block, then at message_stop the usage and the Finish. Unknown events
// and block types are skipped. An error event ends the stream with its
// classified failure, as does a malformed event or a message_stop while a block
// is still open; a stream that ends before message_stop yields llm/stream-closed.
// A line or event over the event-stream parser's bound is llm/malformed-response;
// any other read error from body is yielded as is. Usage already reported is yielded
// before any of these endings, so a failed attempt keeps its known counters.
func Decode(body io.Reader, classify ErrorClassifier) iter.Seq2[vocab.Chunk, error] {
	if classify == nil {
		classify = ErrorFailure
	}
	return func(yield func(vocab.Chunk, error) bool) {
		blocks := map[int]*open{}
		var usage vocab.Usage
		haveUsage := false
		reason := ""
		addUsage := func(u *usageJSON) {
			if u == nil {
				return
			}
			haveUsage = true
			set := func(dst *int, v *int) {
				if v != nil && *v >= 0 {
					*dst = *v
				}
			}
			set(&usage.Input, u.InputTokens)
			set(&usage.Output, u.OutputTokens)
			set(&usage.CacheWrite, u.CacheCreationInputTokens)
			set(&usage.CacheRead, u.CacheReadInputTokens)
		}
		// emitUsage yields the counters reported so far, once; every ending
		// (finish, failure, read error) passes through it so a failed attempt
		// still carries the usage it was billed for.
		usageSent := false
		emitUsage := func() bool {
			if !haveUsage || usageSent {
				return true
			}
			usageSent = true
			u := usage
			u.Total = u.Input + u.CacheRead + u.CacheWrite + u.Output
			return yield(vocab.UsageChunk{Usage: u}, nil)
		}
		fault := func(err error) {
			if emitUsage() {
				yield(nil, err)
			}
		}
		malformed := func(format string, args ...any) {
			fault(transport.Fail(vocab.CodeMalformedResponse, format, args...))
		}
		for ev, err := range transport.Events(body) {
			if err != nil {
				if errors.Is(err, transport.ErrTooLarge) {
					malformed("anthropic: %v", err)
				} else {
					fault(err)
				}
				return
			}
			if ev.Type == "ping" {
				continue
			}
			if ev.Type == "error" {
				if emitUsage() {
					yield(vocab.Finish{Reason: vocab.FinishError, Failure: classify(0, nil, []byte(ev.Data))}, nil)
				}
				return
			}
			var e eventJSON
			if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
				malformed("anthropic: event %q is not JSON: %v", ev.Type, err)
				return
			}
			if e.Type == "" {
				e.Type = ev.Type
			}
			switch e.Type {
			case "message_start":
				if e.Message != nil {
					addUsage(e.Message.Usage)
				}
			case "content_block_start":
				if e.ContentBlock == nil || e.Index < 0 {
					malformed("anthropic: content_block_start without a block")
					return
				}
				if _, dup := blocks[e.Index]; dup {
					malformed("anthropic: block %d started twice", e.Index)
					return
				}
				cb := e.ContentBlock
				b := &open{}
				var first vocab.Chunk
				switch cb.Type {
				case "text":
					b.kind = vocab.KindText
					b.text.WriteString(cb.Text)
					if cb.Text != "" {
						first = vocab.TextDelta{Index: e.Index, Text: cb.Text}
					}
				case "thinking":
					b.kind = vocab.KindReasoning
					b.text.WriteString(cb.Thinking)
					if cb.Thinking != "" {
						first = vocab.ReasoningDelta{Index: e.Index, Text: cb.Thinking}
					}
				case "redacted_thinking":
					b.kind, b.redacted, b.signature = vocab.KindReasoning, true, cb.Data
				case "tool_use":
					b.kind, b.id, b.name = vocab.KindToolUse, cb.ID, cb.Name
					first = vocab.ToolCallDelta{Index: e.Index, ID: cb.ID, Name: cb.Name}
				default:
					blocks[e.Index] = &open{} // kind "": deltas and stop skipped
					continue
				}
				blocks[e.Index] = b
				if !yield(vocab.BlockStart{Index: e.Index, Kind: b.kind}, nil) {
					return
				}
				if first != nil && !yield(first, nil) {
					return
				}
			case "content_block_delta":
				b := blocks[e.Index]
				if b == nil || e.Delta == nil {
					malformed("anthropic: a delta for block %d, which is not open", e.Index)
					return
				}
				if b.kind == "" {
					continue
				}
				// A delta whose type does not match its block's kind is skipped,
				// like an unknown delta, so the streamed deltas always add up to
				// the BlockEnd.
				var c vocab.Chunk
				switch e.Delta.Type {
				case "text_delta":
					if b.kind != vocab.KindText {
						continue
					}
					s, ok := b.joinText(e.Delta.Text)
					if !ok {
						malformed("anthropic: text_delta for block %d is not a string", e.Index)
						return
					}
					if s != "" {
						c = vocab.TextDelta{Index: e.Index, Text: s}
					}
				case "thinking_delta":
					if b.kind != vocab.KindReasoning || b.redacted {
						continue
					}
					s, ok := b.joinText(e.Delta.Thinking)
					if !ok {
						malformed("anthropic: thinking_delta for block %d is not a string", e.Index)
						return
					}
					if s != "" {
						c = vocab.ReasoningDelta{Index: e.Index, Text: s}
					}
				case "signature_delta":
					if b.kind == vocab.KindReasoning {
						b.signature += e.Delta.Signature
					}
				case "input_json_delta":
					if b.kind == vocab.KindToolUse {
						b.args.WriteString(e.Delta.PartialJSON)
						c = vocab.ToolCallDelta{Index: e.Index, ArgsDelta: e.Delta.PartialJSON}
					}
				}
				if c != nil && !yield(c, nil) {
					return
				}
			case "content_block_stop":
				b := blocks[e.Index]
				if b == nil {
					malformed("anthropic: stop for block %d, which is not open", e.Index)
					return
				}
				delete(blocks, e.Index)
				if b.kind == "" {
					continue
				}
				blk, ok := b.block()
				if !ok {
					malformed("anthropic: tool call %q has arguments that are not a JSON object", b.id)
					return
				}
				if !yield(vocab.BlockEnd{Index: e.Index, Block: blk}, nil) {
					return
				}
			case "message_delta":
				if e.Delta != nil && e.Delta.StopReason != "" {
					reason = e.Delta.StopReason
				}
				addUsage(e.Usage)
			case "message_stop":
				if len(blocks) > 0 {
					malformed("anthropic: message_stop with block %d still open", lowest(blocks))
					return
				}
				if !emitUsage() {
					return
				}
				yield(finish(reason), nil)
				return
			}
		}
		fault(transport.Fail(vocab.CodeStreamClosed, "anthropic: the stream ended before message_stop"))
	}
}

func lowest(blocks map[int]*open) int {
	first := true
	n := 0
	for i := range blocks {
		if first || i < n {
			n, first = i, false
		}
	}
	return n
}

func finish(reason string) vocab.Finish {
	switch reason {
	case "end_turn", "stop_sequence", "pause_turn", "":
		return vocab.Finish{Reason: vocab.FinishStop}
	case "tool_use":
		return vocab.Finish{Reason: vocab.FinishToolCalls}
	case "max_tokens", "model_context_window_exceeded":
		return vocab.Finish{Reason: vocab.FinishMaxTokens}
	case "refusal":
		return vocab.Finish{Reason: vocab.FinishError, Failure: transport.Fail(vocab.CodeContentRefused, "the model refused")}
	}
	return vocab.Finish{Reason: vocab.FinishError,
		Failure: transport.Fail(vocab.CodeMalformedResponse, "anthropic: unknown stop_reason %q", clip(reason))}
}

// joinText decodes one raw JSON string delta. A trailing high-surrogate escape
// is held and joined to the next delta, so a pair split across two events
// decodes to its character instead of two U+FFFD.
func (b *open) joinText(raw json.RawMessage) (string, bool) {
	s := string(raw)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", len(raw) == 0
	}
	s = `"` + b.held + s[1:]
	b.held = ""
	if esc, ok := trailingHigh(s); ok {
		b.held = esc
		s = s[:len(s)-1-len(esc)] + `"`
	}
	var out string
	if json.Unmarshal([]byte(s), &out) != nil {
		return "", false
	}
	b.text.WriteString(out)
	return out, true
}

// trailingHigh reports the \uD800-\uDBFF escape that ends the JSON string
// literal s, when it is a real escape (an odd run of backslashes before 'u').
func trailingHigh(s string) (string, bool) {
	body := s[:len(s)-1]
	if len(body) < 7 {
		return "", false
	}
	esc := body[len(body)-6:]
	if esc[0] != '\\' || esc[1] != 'u' {
		return "", false
	}
	v, err := strconv.ParseUint(esc[2:], 16, 16)
	if err != nil || v < 0xD800 || v > 0xDBFF {
		return "", false
	}
	n := 0
	for i := len(body) - 6; i >= 1 && body[i] == '\\'; i-- {
		n++
	}
	return esc, n%2 == 1
}

func (b *open) block() (vocab.Block, bool) {
	switch b.kind {
	case vocab.KindText:
		return vocab.TextBlock(b.text.String() + b.flush()), true
	case vocab.KindReasoning:
		if b.redacted {
			if b.signature == "" {
				return vocab.ReasoningBlock("", "", false), true
			}
			return vocab.ReasoningBlock("", b.signature, true), true
		}
		return vocab.ReasoningBlock(b.text.String()+b.flush(), b.signature, false), true
	}
	args := strings.TrimSpace(b.args.String())
	if args == "" {
		args = "{}"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &obj) != nil || obj == nil || b.id == "" || b.name == "" {
		return vocab.Block{}, false
	}
	return vocab.ToolUseBlock(b.id, b.name, json.RawMessage(args)), true
}

func (b *open) flush() string {
	if b.held == "" {
		return ""
	}
	b.held = ""
	return "\uFFFD"
}
