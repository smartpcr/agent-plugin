package anthropic

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// sse renders each JSON event as one `data:` event of a stream.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

// decodeAll collects every chunk of a stream and the error that ended it, if any.
func decodeAll(t *testing.T, stream string, classify ErrorClassifier) ([]vocab.Chunk, error) {
	t.Helper()
	var chunks []vocab.Chunk
	for c, err := range Decode(strings.NewReader(stream), classify) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}

// ends returns the blocks of the BlockEnd chunks, in order.
func ends(chunks []vocab.Chunk) []vocab.Block {
	var out []vocab.Block
	for _, c := range chunks {
		if e, ok := c.(vocab.BlockEnd); ok {
			out = append(out, e.Block)
		}
	}
	return out
}

func lastFinish(t *testing.T, chunks []vocab.Chunk) vocab.Finish {
	t.Helper()
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	f, ok := chunks[len(chunks)-1].(vocab.Finish)
	if !ok {
		t.Fatalf("last chunk %+v is not a Finish", chunks[len(chunks)-1])
	}
	return f
}

// ErrorFailure maps a known error type to its code at any status, an unknown
// in-stream type to llm/server naming the type, an unreadable in-stream error to
// llm/server, and an unknown or unreadable HTTP error body to the status table.
func TestErrorFailureClassifies(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		code    vocab.Code
		message string
	}{
		"known type in stream": {0, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
			vocab.CodeRateLimit, "rate_limit_error: slow down"},
		"known type over HTTP": {400, `{"type":"error","error":{"type":"billing_error","message":"pay"}}`,
			vocab.CodeQuota, "billing_error: pay"},
		"unknown type in stream": {0, `{"type":"error","error":{"type":"strange_error","message":"?"}}`,
			vocab.CodeServer, `the stream reported error type "strange_error"`},
		"no envelope in stream": {0, `not json`, vocab.CodeServer, "the stream reported an error"},
		"envelope without type": {0, `{"type":"error","error":{"message":"x"}}`, vocab.CodeServer, "the stream reported an error"},
		"unknown type over HTTP": {503, `{"type":"error","error":{"type":"strange_error","message":"?"}}`,
			vocab.CodeServer, "status 503 Service Unavailable"},
		"no envelope over HTTP": {401, `<html>`, vocab.CodeAuth, "status 401 Unauthorized"},
	} {
		f := ErrorFailure(tc.status, http.Header{}, []byte(tc.body))
		if f == nil || f.Code != tc.code || f.Message != tc.message {
			t.Errorf("%s: got %+v, want %s %q", name, f, tc.code, tc.message)
		}
	}
	if _, ok := ParseError([]byte(`{"type":"error"}`)); ok {
		t.Error("ParseError accepted an envelope without an error type")
	}
}

// A vendor message is clipped to 512 bytes of valid UTF-8 plus an ellipsis, and
// invalid bytes become U+FFFD, so a hostile error body cannot flood or corrupt
// the failure record.
func TestErrorFailureClipsTheVendorMessage(t *testing.T) {
	long := strings.Repeat("é", 400) // 800 bytes, cut at a rune boundary
	f := ErrorFailure(0, nil, []byte(`{"error":{"type":"api_error","message":"`+long+`"}}`))
	msg := strings.TrimPrefix(f.Message, "api_error: ")
	if !strings.HasSuffix(msg, "…") || len(msg) > 512+len("…") || !utf8.ValidString(msg) {
		t.Fatalf("clipped message is %d bytes, valid %v: %q...", len(msg), utf8.ValidString(msg), msg[:16])
	}
	odd := strings.Repeat("a", 511) + "é" // the cut at 512 splits the last rune
	msg = strings.TrimPrefix(ErrorFailure(0, nil, []byte(`{"error":{"type":"api_error","message":"`+odd+`"}}`)).Message, "api_error: ")
	if msg != strings.Repeat("a", 511)+"…" {
		t.Fatalf("a split rune was kept: %q", msg[505:])
	}
	short := clip("bad \xff byte")
	if short != "bad � byte" {
		t.Fatalf("clip of invalid UTF-8 = %q", short)
	}
}

// Every stop_reason maps onto the closed finish set; refusal is
// llm/content-refused, and an unknown reason is malformed, named in the message.
func TestFinishReasons(t *testing.T) {
	for reason, want := range map[string]vocab.FinishReason{
		"end_turn": vocab.FinishStop, "stop_sequence": vocab.FinishStop, "pause_turn": vocab.FinishStop, "": vocab.FinishStop,
		"tool_use": vocab.FinishToolCalls, "max_tokens": vocab.FinishMaxTokens, "model_context_window_exceeded": vocab.FinishMaxTokens,
	} {
		if f := finish(reason); f.Reason != want || f.Failure != nil {
			t.Errorf("finish(%q) = %+v, want %s without a failure", reason, f, want)
		}
	}
	if f := finish("refusal"); f.Reason != vocab.FinishError || f.Failure == nil || f.Failure.Code != vocab.CodeContentRefused {
		t.Errorf("refusal: %+v", f)
	}
	f := finish("galaxy_brain")
	if f.Reason != vocab.FinishError || f.Failure == nil || f.Failure.Code != vocab.CodeMalformedResponse ||
		!strings.Contains(f.Failure.Message, `"galaxy_brain"`) {
		t.Errorf("unknown reason: %+v", f)
	}
}

// A full reply with text, thinking, redacted thinking and an unknown block: the
// streamed deltas add up to each BlockEnd; pings, unknown blocks and deltas of the
// wrong kind are skipped; usage merges message_start and message_delta; the stop
// reason decides the Finish.
func TestDecodeTextReasoningAndSkippedEvents(t *testing.T) {
	stream := "event: ping\ndata: {}\n\n" + sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":4,"output_tokens":-1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Hel"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"not text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"hm"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"m"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"not thinking"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"s1"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"s2"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"leak"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"content_block_start","index":4,"content_block":{"type":"server_tool_use"}}`,
		`{"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":4}`,
		`{"type":"content_block_start","index":5,"content_block":{"type":"tool_use","id":"c9","name":"noargs"}}`,
		`{"type":"content_block_delta","index":5,"delta":{"type":"text_delta","text":"ignored"}}`,
		`{"type":"content_block_stop","index":5}`,
		`{"type":"some_future_event"}`,
		`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":7,"cache_creation_input_tokens":2}}`,
		`{"type":"message_stop"}`,
	)
	chunks, err := decodeAll(t, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ends(chunks)
	want := []vocab.Block{
		vocab.TextBlock("Hello"),
		vocab.ReasoningBlock("hmm", "s1s2", false),
		vocab.ReasoningBlock("", "opaque", true),
		vocab.ReasoningBlock("", "", false),
		vocab.ToolUseBlock("c9", "noargs", []byte("{}")),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("block %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	var text, thinking string
	for _, c := range chunks {
		switch c := c.(type) {
		case vocab.TextDelta:
			text += c.Text
		case vocab.ReasoningDelta:
			thinking += c.Text
		case vocab.BlockStart:
			if c.Index == 4 {
				t.Error("an unknown block type was started")
			}
		}
	}
	if text != "Hello" || thinking != "hmm" {
		t.Errorf("deltas add up to text %q, thinking %q", text, thinking)
	}
	usage, ok := chunks[len(chunks)-2].(vocab.UsageChunk)
	if wantU := (vocab.Usage{Input: 10, CacheRead: 4, CacheWrite: 2, Output: 7, Total: 23}); !ok || usage.Usage != wantU {
		t.Errorf("usage = %+v, want %+v before the finish", chunks[len(chunks)-2], wantU)
	}
	if f := lastFinish(t, chunks); f.Reason != vocab.FinishMaxTokens {
		t.Errorf("finish = %+v", f)
	}
}

// A surrogate pair split across two deltas decodes to its character; an escaped
// backslash before "u" is no escape; a high surrogate still held at the block's
// end becomes one U+FFFD.
func TestDecodeJoinsSplitSurrogates(t *testing.T) {
	stream := sse(
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a\ud83d"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\ude00b"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\\\\ud83d"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"x\ud83d"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_stop"}`,
	)
	chunks, err := decodeAll(t, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ends(chunks)
	if len(got) != 2 || got[0].Text.Text != `a😀b\\ud83d` || got[1].Reasoning.Text != "x�" {
		t.Fatalf("blocks = %+v %+v", got[0].Text, got[1].Reasoning)
	}
	if f := lastFinish(t, chunks); f.Reason != vocab.FinishStop {
		t.Errorf("finish = %+v", f)
	}
}

// Each malformed event ends the stream with one llm/malformed-response naming the
// fault, and nothing follows it.
func TestDecodeMalformedEvents(t *testing.T) {
	start := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	thinking := `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`
	for name, tc := range map[string]struct {
		stream, want string
	}{
		"start without a block":     {sse(`{"type":"content_block_start","index":0}`), "without a block"},
		"start at a negative index": {sse(`{"type":"content_block_start","index":-1,"content_block":{"type":"text"}}`), "without a block"},
		"block started twice":       {sse(start, start), "started twice"},
		"delta for no block":        {sse(`{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"x"}}`), "block 3, which is not open"},
		"delta without a delta":     {sse(start, `{"type":"content_block_delta","index":0}`), "which is not open"},
		"text delta not a string":   {sse(start, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":5}}`), "text_delta for block 0 is not a string"},
		"text delta bad escape":     {sse(start, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\ud83d\ud83d\u12"}}`), "is not JSON"},
		"thinking not a string":     {sse(thinking, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":[]}}`), "thinking_delta for block 0 is not a string"},
		"stop for no block":         {sse(`{"type":"content_block_stop","index":2}`), "stop for block 2"},
		"tool call without an id":   {sse(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"f"}}`, `{"type":"content_block_stop","index":0}`), "not a JSON object"},
		"tool call with null args": {sse(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c","name":"f"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"null"}}`, `{"type":"content_block_stop","index":0}`), "not a JSON object"},
		"stop with the lowest open block named": {sse(`{"type":"content_block_start","index":5,"content_block":{"type":"text"}}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"text"}}`, `{"type":"message_stop"}`), "block 2 still open"},
	} {
		chunks, err := decodeAll(t, tc.stream, nil)
		var f *vocab.Failure
		if !errors.As(err, &f) || f.Code != vocab.CodeMalformedResponse || !strings.Contains(f.Message, tc.want) {
			t.Errorf("%s: err = %v after %d chunks, want llm/malformed-response containing %q", name, err, len(chunks), tc.want)
		}
		for _, c := range chunks {
			if _, ok := c.(vocab.Finish); ok {
				t.Errorf("%s: a Finish before the failure", name)
			}
		}
	}
}

// A stream that ends without message_stop is llm/stream-closed; an in-stream error
// goes to the caller's classifier, with status 0 and the event's data.
func TestDecodeEndingsAndClassifier(t *testing.T) {
	_, err := decodeAll(t, sse(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`), nil)
	var f *vocab.Failure
	if !errors.As(err, &f) || f.Code != vocab.CodeStreamClosed {
		t.Fatalf("truncated stream: err = %v, want llm/stream-closed", err)
	}
	var gotStatus = -1
	var gotBody string
	mine := vocab.Failure{Code: vocab.CodeQuota}
	chunks, err := decodeAll(t, errorEventStream, func(status int, _ http.Header, body []byte) *vocab.Failure {
		gotStatus, gotBody = status, string(body)
		return &mine
	})
	if err != nil || len(chunks) != 1 || lastFinish(t, chunks).Failure != &mine {
		t.Fatalf("classifier result not used: %+v, %v", chunks, err)
	}
	if gotStatus != 0 || !strings.Contains(gotBody, "overloaded_error") {
		t.Fatalf("classifier got status %d, body %q", gotStatus, gotBody)
	}
}

// A consumer may stop at any chunk: Decode then yields nothing more (the range
// loop would panic otherwise), whichever chunk the consumer stopped at.
func TestDecodeStopsWhenTheConsumerStops(t *testing.T) {
	stream := sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"a"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"b"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c","name":"f"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_stop"}`,
	)
	all, err := decodeAll(t, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= len(all); n++ {
		seen := 0
		for range Decode(strings.NewReader(stream), nil) {
			if seen++; seen == n {
				break
			}
		}
		if seen != n {
			t.Fatalf("stopping after %d chunks saw %d", n, seen)
		}
	}
	// A consumer that stops at the usage of a failed attempt sees no failure after it.
	for _, s := range []string{usageStartStream + errorEventStream, usageStartStream} {
		for c := range Decode(strings.NewReader(s), nil) {
			if _, ok := c.(vocab.UsageChunk); !ok {
				t.Fatalf("first chunk %+v, want the usage", c)
			}
			break
		}
	}
}
