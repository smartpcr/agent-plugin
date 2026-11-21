package anthropic

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

const (
	toolCallSeed = "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"f\",\"input\":{}}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"a\\\":\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"1}\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	errorEventSeed = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
)

var decodeSeeds = []string{
	"",
	"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi \\ud83d\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\\ude00\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	toolCallSeed,
	errorEventSeed,
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"opaque\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
	"data: {\"type\":\"content_block_delta\",\"index\":9,\"delta\":{\"type\":\"text_delta\",\"text\":1}}\n\n",
	"data: not json\n\n",
}

// FuzzSSE: decoding any event stream never panics, yields at most one Finish
// and nothing after it, every BlockEnd carries a valid block, and a failure is
// always one of the closed table.
func FuzzSSE(f *testing.F) {
	for _, s := range decodeSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, stream string) {
		finished := false
		for c, err := range Decode(strings.NewReader(stream), nil) {
			if finished {
				t.Fatal("a chunk after the Finish")
			}
			if err != nil {
				if fl, ok := err.(*vocab.Failure); !ok || fl.Validate() != nil {
					t.Fatalf("error %v is not a valid failure", err)
				}
				finished = true
				continue
			}
			switch c := c.(type) {
			case vocab.BlockEnd:
				if err := c.Block.Validate(); err != nil {
					t.Fatalf("invalid block: %v", err)
				}
			case vocab.Finish:
				if c.Failure != nil && c.Failure.Validate() != nil {
					t.Fatalf("invalid failure %+v", c.Failure)
				}
				finished = true
			}
		}
		if !finished {
			t.Fatal("the stream ended with neither a Finish nor an error")
		}
	})
}
