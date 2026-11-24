package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// streamOnce makes one attempt on route r/a of rt and returns the record the
// runtime settled and every chunk it yielded.
func streamOnce(t *testing.T, ctx context.Context, rt *llm.Runtime, req session.Request) (llm.Attempt, []vocab.Chunk) {
	t.Helper()
	var rec llm.Attempt
	var got []vocab.Chunk
	p := must(rt.Prepare(ctx, "r/a", "", vocab.Controls{}))
	for c := range p.Stream(ctx, req, llm.AttemptSinkFunc(func(a llm.Attempt) error { rec = a; return nil })) {
		got = append(got, c)
	}
	return rec, got
}

func failureOf(t *testing.T, code vocab.Code) *vocab.Failure {
	t.Helper()
	f, err := vocab.NewFailure(code, string(code))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func toolCall(index int, id, name, args string) []vocab.Chunk {
	return []vocab.Chunk{
		vocab.BlockStart{Index: index, Kind: vocab.KindToolUse},
		vocab.ToolCallDelta{Index: index, ID: id, Name: name},
		vocab.ToolCallDelta{Index: index, ArgsDelta: args},
		vocab.BlockEnd{Index: index, Block: vocab.ToolUseBlock(id, name, json.RawMessage(args))},
	}
}

func joined(parts ...[]vocab.Chunk) []vocab.Chunk { return slices.Concat(parts...) }

var usage = vocab.UsageChunk{Usage: vocab.Usage{Input: 3, Output: 2}}

// The adapter conformance table (DH-T-UTIL-14). A04 has no exported adapter
// table yet (DH-T-LLM-6 runs DH-T-PIAI-8's over the wire drivers, P1), so the
// rows are its logical cases that a chunk-level adapter can express -- text,
// reasoning, parallel tool calls, the output cap, each way an attempt ends --
// each driven through the real llm runtime and checked on the record it settles.
//
//dsh:test DH-T-UTIL-14
func TestScriptedPassesTheAdapterConformanceTable(t *testing.T) {
	text := []vocab.Chunk{
		vocab.BlockStart{Index: 0, Kind: vocab.KindText},
		vocab.TextDelta{Index: 0, Text: "hello "},
		vocab.TextDelta{Index: 0, Text: "world"},
		vocab.BlockEnd{Index: 0, Block: vocab.TextBlock("hello world")},
	}
	reasoning := []vocab.Chunk{
		vocab.BlockStart{Index: 0, Kind: vocab.KindReasoning},
		vocab.ReasoningDelta{Index: 0, Text: "think"},
		vocab.BlockEnd{Index: 0, Block: vocab.ReasoningBlock("think", "sig", false)},
		vocab.BlockStart{Index: 1, Kind: vocab.KindText},
		vocab.TextDelta{Index: 1, Text: "done"},
		vocab.BlockEnd{Index: 1, Block: vocab.TextBlock("done")},
	}
	cases := []struct {
		name   string
		script model.Script
		reason vocab.FinishReason
		code   vocab.Code        // "" when the attempt succeeds
		kinds  []vocab.BlockKind // the kept message's blocks, nil for none
		check  func(t *testing.T, rec llm.Attempt)
	}{
		{name: "text", script: model.Chunks(joined(text, []vocab.Chunk{usage, vocab.Finish{Reason: vocab.FinishStop}})...),
			reason: vocab.FinishStop, kinds: []vocab.BlockKind{vocab.KindText},
			check: func(t *testing.T, rec llm.Attempt) {
				if got := rec.Message.Blocks()[0].Text.Text; got != "hello world" {
					t.Errorf("text %q", got)
				}
				if rec.Usage == nil || rec.Usage.Input != 3 || rec.Usage.Output != 2 {
					t.Errorf("usage %+v", rec.Usage)
				}
			}},
		{name: "answer", script: model.Answer("hi"), reason: vocab.FinishStop, kinds: []vocab.BlockKind{vocab.KindText}},
		{name: "reasoning", script: model.Chunks(joined(reasoning, []vocab.Chunk{vocab.Finish{Reason: vocab.FinishStop}})...),
			reason: vocab.FinishStop, kinds: []vocab.BlockKind{vocab.KindReasoning, vocab.KindText},
			check: func(t *testing.T, rec llm.Attempt) {
				if r := rec.Message.Blocks()[0].Reasoning; r.Text != "think" || r.Signature != "sig" {
					t.Errorf("reasoning %+v", r)
				}
			}},
		{name: "parallel tool calls",
			script: model.Chunks(joined(toolCall(0, "c1", "read", `{"p":"a"}`), toolCall(1, "c2", "read", `{"p":"b"}`),
				[]vocab.Chunk{usage, vocab.Finish{Reason: vocab.FinishToolCalls}})...),
			reason: vocab.FinishToolCalls, kinds: []vocab.BlockKind{vocab.KindToolUse, vocab.KindToolUse},
			check: func(t *testing.T, rec llm.Attempt) {
				bs := rec.Message.Blocks()
				if bs[0].ToolUse.ID != "c1" || bs[1].ToolUse.ID != "c2" || string(bs[1].ToolUse.Args) != `{"p":"b"}` {
					t.Errorf("calls %+v %+v", bs[0].ToolUse, bs[1].ToolUse)
				}
			}},
		{name: "output cap drops the call",
			script: model.Chunks(joined(text, toolCall(1, "c1", "read", `{}`), []vocab.Chunk{vocab.Finish{Reason: vocab.FinishMaxTokens}})...),
			reason: vocab.FinishMaxTokens, kinds: []vocab.BlockKind{vocab.KindText}},
		{name: "failure before the first chunk", script: model.Fails(failureOf(t, vocab.CodeServer)),
			reason: vocab.FinishError, code: vocab.CodeServer},
		{name: "failure after text", script: model.Fails(failureOf(t, vocab.CodeRateLimit), text...),
			reason: vocab.FinishError, code: vocab.CodeRateLimit, kinds: []vocab.BlockKind{vocab.KindText}},
		{name: "in-stream error finish",
			script: model.Chunks(joined(text, []vocab.Chunk{vocab.Finish{Reason: vocab.FinishError, Failure: failureOf(t, vocab.CodeServer)}})...),
			reason: vocab.FinishError, code: vocab.CodeServer, kinds: []vocab.BlockKind{vocab.KindText}},
		{name: "transport error", script: model.Script{Chunks: text, Err: errors.New("connection reset")},
			reason: vocab.FinishError, code: vocab.CodeTransport, kinds: []vocab.BlockKind{vocab.KindText}},
		{name: "closed without a finish", script: model.Chunks(text...),
			reason: vocab.FinishError, code: vocab.CodeStreamClosed, kinds: []vocab.BlockKind{vocab.KindText}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSession(t)
			s := model.NewScripted(t, tc.script)
			rt := llm.New()
			model.RegisterScripted(t, rt, "r/a", s)
			rec, chunks := streamOnce(t, context.Background(), rt, must(h.NextRequest()))
			if last, ok := chunks[len(chunks)-1].(vocab.Finish); !ok || last.Reason != tc.reason {
				t.Fatalf("the stream ended with %#v, want a %s finish", chunks[len(chunks)-1], tc.reason)
			}
			if rec.Finish.Reason != tc.reason {
				t.Fatalf("settled %s, want %s (%v)", rec.Finish.Reason, tc.reason, rec.Finish.Failure)
			}
			if tc.code == "" && (rec.Finish.Failure != nil || !rec.Succeeded()) ||
				tc.code != "" && (rec.Finish.Failure == nil || rec.Finish.Failure.Code != tc.code) {
				t.Fatalf("failure %v, want %q", rec.Finish.Failure, tc.code)
			}
			var kinds []vocab.BlockKind
			if rec.Message != nil {
				for _, b := range rec.Message.Blocks() {
					kinds = append(kinds, b.Kind)
				}
			}
			if !slices.Equal(kinds, tc.kinds) {
				t.Fatalf("kept blocks %v, want %v", kinds, tc.kinds)
			}
			if tc.check != nil {
				tc.check(t, rec)
			}
			at := s.Attempts()
			if len(at) != 1 || at[0].Route != "r/a" || at[0].Model != model.ModelName || at[0].Request.Digest() == "" {
				t.Fatalf("recorded attempts %+v", at)
			}
		})
	}
}

// DH-T-UTIL-14: an attempt with no script left fails the test and ends as a
// transport failure; a script never played fails the test when it ends.
//
//dsh:test DH-T-UTIL-14
func TestScriptedFailsOnUnscriptedAndLeftoverScript(t *testing.T) {
	h := newSession(t)
	rt := llm.New()
	rec := &recorder{TB: t}
	s := model.NewScripted(rec)
	model.RegisterScripted(t, rt, "r/a", s)
	att, _ := streamOnce(t, context.Background(), rt, must(h.NextRequest()))
	if att.Finish.Failure == nil || att.Finish.Failure.Code != vocab.CodeTransport {
		t.Fatalf("an unscripted attempt settled %+v", att.Finish)
	}
	if f := rec.failed(); len(f) != 1 || !strings.Contains(f[0], "unscripted attempt on route \"r/a\"") {
		t.Fatalf("an unscripted attempt reported %q", f)
	}

	left := &recorder{TB: t}
	model.NewScripted(left, model.Answer("never asked"), model.Answer("nor this"))
	left.cleanup()
	if f := left.failed(); len(f) != 1 || !strings.Contains(f[0], "2 scripted attempt(s) never made") {
		t.Fatalf("leftover script reported %q", f)
	}
	used := &recorder{TB: t}
	model.NewScripted(used).Script() // nothing scripted, nothing left
	used.cleanup()
	if f := used.failed(); len(f) != 0 {
		t.Fatalf("an empty script reported %q", f)
	}
}

// DH-T-UTIL-14: a blocked script ends when its context is cancelled. In a
// synctest bubble the 30 s deadline passes in no wall time; the attempt settles
// aborted (the runtime's reading of a deadline) and keeps the text so far.
//
//dsh:test DH-T-UTIL-14
func TestScriptedBlockEndsWhenTheContextIsCancelled(t *testing.T) {
	start := time.Now()
	clock.Run(t, func(t *testing.T) {
		h := newSession(t)
		s := model.NewScripted(t, model.Blocks(vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: "so far"}))
		rt := llm.New()
		model.RegisterScripted(t, rt, "r/a", s)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)
		began := time.Now()
		rec, _ := streamOnce(t, ctx, rt, must(h.NextRequest()))
		if waited := time.Since(began); waited != 30*time.Second {
			t.Errorf("the attempt ended after %v of virtual time, want 30s", waited)
		}
		if rec.Finish.Reason != vocab.FinishAborted || rec.Finish.Failure == nil || rec.Finish.Failure.Code != vocab.CodeAborted {
			t.Fatalf("a cancelled blocked script settled %+v", rec.Finish)
		}
		if rec.Message == nil || rec.Message.Blocks()[0].Text.Text != "so far" {
			t.Fatalf("kept %+v, want the text so far", rec.Message)
		}
	})
	if wall := time.Since(start); wall > 10*time.Second {
		t.Errorf("a 30 s virtual wait took %v of wall time", wall)
	}
}
