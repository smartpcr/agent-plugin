package anthropic

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// The decoder streams of the tests below; seeds_test.go keeps its own copies (QG-6).
const (
	toolCallStream = "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"f\",\"input\":{}}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"a\\\":\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"1}\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	errorEventStream = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	usageStartStream = "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n"
	// unclosedToolStream finishes tool_use without stopping its block.
	unclosedToolStream = "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"f\",\"input\":{}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
)

type surface struct {
	msgs  []vocab.Message
	tools []vocab.ToolSchema
}

func (s surface) Messages() ([]vocab.Message, error) { return s.msgs, nil }
func (s surface) Tools() []vocab.ToolSchema          { return s.tools }

func msg(t *testing.T, id string, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	t.Helper()
	m, err := vocab.RestoreMessage(id, role, nil, blocks...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The rendering of a full conversation is pinned by a golden; a late system
// message is diagnosed; the encoding is deterministic.
func TestEncodeGolden(t *testing.T) {
	temp := 0.5
	s := surface{
		msgs: []vocab.Message{
			msg(t, "s", vocab.RoleSystem, vocab.TextBlock("Be brief.")),
			msg(t, "u", vocab.RoleUser, vocab.TextBlock("list <files> & go")),
			msg(t, "a", vocab.RoleAssistant, vocab.ReasoningBlock("think", "sig", false), vocab.ReasoningBlock("", "opaque", true),
				vocab.ToolUseBlock("c1", "ls", json.RawMessage(`{"dir":"."}`))),
			msg(t, "t", vocab.RoleTool, vocab.ToolResultBlock("c1", false, vocab.TextPart("a.go"), vocab.TextPart("b.go"))),
			msg(t, "d", vocab.RoleDeveloper, vocab.TextBlock("Late rule.")),
			msg(t, "u2", vocab.RoleUser, vocab.TextBlock("thanks")),
		},
		tools: []vocab.ToolSchema{{Name: "ls", Description: "list", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	cfg := vocab.CallConfig{Controls: vocab.Controls{MaxOutputTokens: 100, Temperature: &temp, Stop: []string{"END"},
		ToolChoice: vocab.ToolChoice{Mode: vocab.ToolChoiceRequired}}}
	a, err := Encode(s, "deepseek-chat", cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encode(s, "deepseek-chat", cfg)
	if string(a.Body) != string(b.Body) {
		t.Fatal("encoding is not deterministic")
	}
	if len(a.Diagnostics) != 1 || !strings.HasPrefix(a.Diagnostics[0], "llm-deepseek.4") {
		t.Fatalf("diagnostics = %v", a.Diagnostics)
	}
	golden.Assert(t, "encode/conversation", string(a.Body)+"\n")
}

func TestEncodeRefuses(t *testing.T) {
	img := surface{msgs: []vocab.Message{msg(t, "u", vocab.RoleUser, vocab.ImageBlock("image/png", "blob:1"))}}
	for name, tc := range map[string]struct {
		s    Surface
		cfg  vocab.CallConfig
		code vocab.Code
	}{
		"image":      {img, vocab.CallConfig{Controls: vocab.Controls{MaxOutputTokens: 1}}, vocab.CodeUnsupportedContent},
		"max tokens": {surface{}, vocab.CallConfig{}, vocab.CodeInvalidRequest},
		"effort":     {surface{}, vocab.CallConfig{Controls: vocab.Controls{MaxOutputTokens: 1, Effort: "max"}}, vocab.CodeUnsupportedReasoningEffort},
	} {
		_, err := Encode(tc.s, "m", tc.cfg)
		var f *vocab.Failure
		if !errors.As(err, &f) || f.Code != tc.code {
			t.Errorf("%s: err = %v, want %s", name, err, tc.code)
		}
	}
}

// A tool call assembles from its argument deltas and finishes tool_calls; a call
// whose arguments are not an object is malformed.
func TestDecodeToolCall(t *testing.T) {
	var ends []vocab.Block
	var fin vocab.Finish
	for c, err := range Decode(strings.NewReader(toolCallStream), nil) {
		if err != nil {
			t.Fatal(err)
		}
		switch c := c.(type) {
		case vocab.BlockEnd:
			ends = append(ends, c.Block)
		case vocab.Finish:
			fin = c
		}
	}
	if len(ends) != 1 || ends[0].ToolUse == nil || string(ends[0].ToolUse.Args) != `{"a":1}` || fin.Reason != vocab.FinishToolCalls {
		t.Fatalf("ends = %+v, finish = %+v", ends, fin)
	}
	bad := strings.Replace(toolCallStream, `1}`, `1]`, 1)
	var last error
	for _, err := range Decode(strings.NewReader(bad), nil) {
		last = err
	}
	var f *vocab.Failure
	if !errors.As(last, &f) || f.Code != vocab.CodeMalformedResponse {
		t.Fatalf("bad args: %v", last)
	}
}

func TestDecodeErrorEvent(t *testing.T) {
	for c, err := range Decode(strings.NewReader(errorEventStream), nil) {
		f, ok := c.(vocab.Finish)
		if err != nil || !ok || f.Failure == nil || f.Failure.Code != vocab.CodeServer {
			t.Fatalf("chunk %+v err %v, want an llm/server finish", c, err)
		}
	}
}

// Usage already reported survives every failed ending: a drop before
// message_stop, a malformed event and an in-stream error each yield the known
// counters, once, before the failure.
func TestDecodeKeepsUsageOnFailure(t *testing.T) {
	want := vocab.Usage{Input: 12, Output: 1, Total: 13}
	for name, stream := range map[string]string{
		"dropped":   usageStartStream,
		"malformed": usageStartStream + "data: not json\n\n",
		"error":     usageStartStream + errorEventStream,
	} {
		var usages []vocab.Usage
		failed := false
		for c, err := range Decode(strings.NewReader(stream), nil) {
			if failed {
				t.Fatalf("%s: a chunk after the failure", name)
			}
			if err != nil {
				failed = true
				continue
			}
			switch c := c.(type) {
			case vocab.UsageChunk:
				usages = append(usages, c.Usage)
			case vocab.Finish:
				failed = c.Failure != nil
			}
		}
		if !failed || len(usages) != 1 || usages[0] != want {
			t.Errorf("%s: failed=%v usages=%+v, want one %+v before the failure", name, failed, usages, want)
		}
	}
}

// message_stop while a block is open is malformed, and the unfinished tool call
// never ends as a block.
func TestDecodeRejectsOpenBlockAtStop(t *testing.T) {
	var last error
	for c, err := range Decode(strings.NewReader(unclosedToolStream), nil) {
		switch c.(type) {
		case vocab.BlockEnd, vocab.Finish:
			t.Fatalf("chunk %+v from an unfinished block", c)
		}
		last = err
	}
	var f *vocab.Failure
	if !errors.As(last, &f) || f.Code != vocab.CodeMalformedResponse {
		t.Fatalf("err = %v, want llm/malformed-response", last)
	}
}
