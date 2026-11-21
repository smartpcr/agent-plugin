package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type askPayload struct {
	Hint string `json:"hint"`
}

// summaryAsk is a test plugin type that describes an auxiliary request.
var summaryAsk = func() *event.Type[askPayload] {
	typ := event.MustDefine(event.Default(), event.Spec[askPayload]{Name: "ext/llm-test-summary", Version: 1})
	if err := surface.DeclareBuilder(event.Default(), typ.Name(), func(ev event.Event, at surface.View, b *surface.AuxBuilder) error {
		b.Purpose("summary")
		b.History(at.History())
		m, err := vocab.RestoreMessage("summary-ask", vocab.RoleUser, nil, vocab.TextBlock("summarise"))
		if err != nil {
			return err
		}
		b.Message(m)
		return nil
	}); err != nil {
		panic(err)
	}
	return typ
}()

// newSession is a memstore-backed session with an open turn and step, a header
// for route r/a and one user message: NextRequest builds a request on it.
func newSession(t *testing.T) session.Handle {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	svc := session.NewService(root, memstore.New(), session.Config{})
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a"})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}))
	return h
}

// script is one scripted provider attempt.
type script func(ctx context.Context, yield func(vocab.Chunk, error) bool)

// fake is an in-memory route adapter: each Attempt runs the next script.
type fake struct {
	mu       sync.Mutex
	scripts  []script
	prepares atomic.Int32
	attempts atomic.Int32
	reqs     []string // the request digest each attempt saw
}

func (f *fake) Prepare(context.Context, vocab.RouteID, string, vocab.CallConfig) (llm.Call, error) {
	f.prepares.Add(1)
	return f, nil
}

func (f *fake) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	f.attempts.Add(1)
	f.mu.Lock()
	s := f.scripts[0]
	f.scripts = f.scripts[1:]
	f.reqs = append(f.reqs, req.Digest())
	f.mu.Unlock()
	return func(yield func(vocab.Chunk, error) bool) { s(ctx, yield) }
}

func chunks(cs ...vocab.Chunk) script {
	return func(_ context.Context, yield func(vocab.Chunk, error) bool) {
		for _, c := range cs {
			if !yield(c, nil) {
				return
			}
		}
	}
}

func text(s string) []vocab.Chunk {
	return []vocab.Chunk{vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: s},
		vocab.BlockEnd{Index: 0, Block: vocab.TextBlock(s)}}
}

func overloaded() *vocab.Failure { return must(vocab.NewFailure(vocab.CodeServer, "overloaded")) }

func newRuntime(t *testing.T, f *fake) *llm.Runtime {
	t.Helper()
	var now atomic.Int64
	rt := llm.New(llm.WithClock(func() int64 { return now.Add(1) }))
	must(rt.Register("fake", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1", Efforts: []string{"low", "high"}}}, Adapter: f}))
	return rt
}

// recordingSink is the caller's side: it logs each record as one settlement
// event and notes the log length the runtime left it.
type recordingSink struct {
	h       session.Handle
	records []llm.Attempt
	before  []event.Offset // the log length when the record arrived
	seqs    []event.Seq
}

func (s *recordingSink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	s.before = append(s.before, s.h.Len())
	d, err := a.Draft()
	if err != nil {
		return err
	}
	r, err := s.h.Append(bg, d)
	if err != nil {
		return err
	}
	s.seqs = append(s.seqs, r.Seq)
	return nil
}

func drain(seq iter.Seq[vocab.Chunk]) (all []vocab.Chunk, finishes []vocab.Finish) {
	for c := range seq {
		all = append(all, c)
		if f, ok := c.(vocab.Finish); ok {
			finishes = append(finishes, f)
		}
	}
	return all, finishes
}

type settlement struct {
	RequestAsOf   *event.Seq     `json:"request_as_of"`
	RequestDigest string         `json:"request_digest"`
	Failure       *vocab.Failure `json:"failure"`
	Estimate      int            `json:"estimate"`
}

// DH-T-LLM-2 (TC-05): one attempt, one caller-logged settlement. A route that
// fails once then succeeds: each Stream ends with exactly one Finish, hands one
// record carrying requestAsOf and requestDigest to the caller's sink, and the
// store holds exactly one settlement per attempt, every one appended by the
// caller; the runtime appends nothing.
func TestStreamOneAttemptOneSettlement(t *testing.T) {
	h := newSession(t)
	f := &fake{scripts: []script{
		chunks(vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: "par"},
			vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()}),
		chunks(append(text("hi there"), vocab.UsageChunk{Usage: vocab.Usage{Input: 3, Output: 2}}, vocab.Finish{Reason: vocab.FinishStop})...),
	}}
	rt := newRuntime(t, f)
	sink := &recordingSink{h: h}
	start := h.Len()
	for attempt := 1; attempt <= 2; attempt++ {
		p := must(rt.Prepare(bg, "r/a", "", vocab.Controls{})) // one prepared call per attempt
		if f.attempts.Load() != int32(attempt-1) {
			t.Fatal("Prepare did I/O")
		}
		req := must(h.NextRequest())
		_, fins := drain(p.Stream(bg, req, sink))
		if len(fins) != 1 {
			t.Fatalf("attempt %d: %d finishes", attempt, len(fins))
		}
		if len(sink.records) != attempt {
			t.Fatalf("attempt %d: %d records", attempt, len(sink.records))
		}
		rec := sink.records[attempt-1]
		if rec.RequestAsOf != req.AsOf() || rec.RequestDigest != req.Digest() || rec.RequestDigest == "" {
			t.Fatalf("attempt %d: record asOf %d digest %q, request %d %q", attempt, rec.RequestAsOf, rec.RequestDigest, req.AsOf(), req.Digest())
		}
		if f.reqs[attempt-1] != req.Digest() {
			t.Fatal("the adapter saw another request")
		}
		if rec.Estimate == 0 || rec.Estimate != meter.EstimateRequest(req) {
			t.Fatalf("attempt %d: estimate %d, the request prices at %d", attempt, rec.Estimate, meter.EstimateRequest(req))
		}
		if got := sink.before[attempt-1]; got != start+event.Offset(attempt-1) {
			t.Fatalf("attempt %d: the runtime appended: log %d at settle, want %d", attempt, got, start+event.Offset(attempt-1))
		}
	}
	if h.Len() != start+2 {
		t.Fatalf("log grew by %d, want 2 settlements", h.Len()-start)
	}
	evs := must(h.Read(bg, start, 10))
	wantTypes := []string{"assistant/attempt", "assistant/message"}
	for i, ev := range evs {
		if ev.Type() != wantTypes[i] || ev.Seq() != sink.seqs[i] {
			t.Fatalf("settlement %d: %s at %d", i, ev.Type(), ev.Seq())
		}
		var s settlement
		if err := json.Unmarshal([]byte(ev.Raw()), &s); err != nil {
			t.Fatal(err)
		}
		if s.RequestAsOf == nil || *s.RequestAsOf != sink.records[i].RequestAsOf || s.RequestDigest != sink.records[i].RequestDigest || s.Estimate != sink.records[i].Estimate {
			t.Fatalf("settlement %d does not attribute its request: %s", i, ev.Raw())
		}
	}
	first, second := sink.records[0], sink.records[1]
	if first.Succeeded() || first.Finish.Failure.Code != vocab.CodeServer || first.Message == nil || first.Message.Blocks()[0].Text.Text != "par" {
		t.Fatalf("failed attempt record %+v", first)
	}
	if !second.Succeeded() || second.Usage == nil || second.Usage.Output != 2 || second.Timing.Start == 0 || second.Timing.End < second.Timing.FirstToken {
		t.Fatalf("success record %+v", second)
	}
}

// DH-T-LLM-2: Complete makes one attempt and hands its one record to the
// caller's sink; the record names the auxiliary purpose and request.
func TestCompleteHandsOneRecordToTheCaller(t *testing.T) {
	h := newSession(t)
	f := &fake{scripts: []script{chunks(append(text("a summary"), vocab.Finish{Reason: vocab.FinishStop})...)}}
	rt := newRuntime(t, f)
	ask := must(h.Append(bg, must(summaryAsk.Draft(askPayload{Hint: "x"}))))
	req := must(h.RequestFrom(ask.Seq))
	sink := &recordingSink{h: h}
	start := h.Len()
	m, err := rt.Complete(bg, req, sink)
	if err != nil {
		t.Fatal(err)
	}
	if m.Blocks()[0].Text.Text != "a summary" || len(sink.records) != 1 || h.Len() != start+1 || sink.before[0] != start {
		t.Fatalf("Complete: records %d log +%d", len(sink.records), h.Len()-start)
	}
	if rec := sink.records[0]; rec.Purpose != "summary" || rec.RequestAsOf != ask.Seq || rec.RequestDigest != req.Digest() || rec.Model != "m1" {
		t.Fatalf("auxiliary record %+v", rec)
	}

	// A failed attempt returns its failure and still settles once; a sink error
	// is the sink's, not an llm failure.
	f.scripts = append(f.scripts, chunks(vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()}))
	var fl *vocab.Failure
	if _, err := rt.Complete(bg, req, sink); !errors.As(err, &fl) || fl.Code != vocab.CodeServer || len(sink.records) != 2 {
		t.Fatalf("failed Complete: %v, %d records", err, len(sink.records))
	}
	f.scripts = append(f.scripts, chunks(append(text("ok"), vocab.Finish{Reason: vocab.FinishStop})...))
	boom := errors.New("sink down")
	if _, err := rt.Complete(bg, req, llm.AttemptSinkFunc(func(llm.Attempt) error { return boom })); !errors.Is(err, boom) || errors.As(err, &fl) {
		t.Fatalf("sink error: %v", err)
	}
	loop := must(newSession(t).NextRequest())
	if _, err := rt.Complete(bg, loop, sink); !errors.As(err, &fl) || fl.Code != vocab.CodeInvalidRequest {
		t.Fatalf("Complete on a loop request: %v", err)
	}
}

// DH-T-LLM-1: an adapter error, a panic, a missing finish, a cancellation and a
// consumer that stops reading each end in exactly one Finish and one record;
// nothing after a Finish is read.
func TestStreamNormalisesEveryEnding(t *testing.T) {
	block := func(ctx context.Context, yield func(vocab.Chunk, error) bool) {
		yield(vocab.TextDelta{Index: 0, Text: "so far"}, nil)
		<-ctx.Done()
	}
	cases := []struct {
		name   string
		s      script
		ctx    func() (context.Context, context.CancelFunc)
		stop   bool // the consumer stops after the first chunk
		reason vocab.FinishReason
		code   vocab.Code
		kept   string
	}{
		{name: "error", s: func(_ context.Context, y func(vocab.Chunk, error) bool) { y(nil, errors.New("reset")) },
			reason: vocab.FinishError, code: vocab.CodeTransport},
		{name: "failure", s: func(_ context.Context, y func(vocab.Chunk, error) bool) { y(nil, overloaded()) },
			reason: vocab.FinishError, code: vocab.CodeServer},
		{name: "panic", s: func(context.Context, func(vocab.Chunk, error) bool) { panic("adapter bug") },
			reason: vocab.FinishError, code: vocab.CodeMalformedResponse},
		{name: "no finish", s: chunks(vocab.TextDelta{Index: 0, Text: "half"}),
			reason: vocab.FinishError, code: vocab.CodeStreamClosed, kept: "half"},
		{name: "after finish", s: chunks(append(text("done"), vocab.Finish{Reason: vocab.FinishStop}, vocab.TextDelta{Index: 1, Text: "late"}, vocab.Finish{Reason: vocab.FinishError})...),
			reason: vocab.FinishStop, kept: "done"},
		{name: "error without failure", s: chunks(vocab.Finish{Reason: vocab.FinishError}),
			reason: vocab.FinishError, code: vocab.CodeMalformedResponse},
		{name: "unknown reason", s: chunks(append(text("x"), vocab.Finish{Reason: "weird"})...),
			reason: vocab.FinishError, code: vocab.CodeMalformedResponse, kept: "x"},
		{name: "empty", s: chunks(vocab.Finish{Reason: vocab.FinishStop}),
			reason: vocab.FinishError, code: vocab.CodeEmptyResponse},
		{name: "cancelled", s: block, reason: vocab.FinishAborted, code: vocab.CodeAborted, kept: "so far",
			ctx: func() (context.Context, context.CancelFunc) { return context.WithTimeout(bg, 20*time.Millisecond) }},
		{name: "cancelled before", s: chunks(vocab.Finish{Reason: vocab.FinishStop}), reason: vocab.FinishAborted, code: vocab.CodeAborted,
			ctx: func() (context.Context, context.CancelFunc) {
				c, cancel := context.WithCancel(bg)
				cancel()
				return c, cancel
			}},
		{name: "consumer stops", s: block, stop: true, reason: vocab.FinishAborted, code: vocab.CodeAborted, kept: "so far"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSession(t)
			f := &fake{scripts: []script{tc.s}}
			rt := newRuntime(t, f)
			ctx, cancel := bg, context.CancelFunc(func() {})
			if tc.ctx != nil {
				ctx, cancel = tc.ctx()
			}
			defer cancel()
			req := must(h.NextRequest())
			var recs []llm.Attempt
			sink := llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })
			var fins []vocab.Finish
			for c := range must(rt.Prepare(bg, "r/a", "m1", vocab.Controls{})).Stream(ctx, req, sink) {
				if fin, ok := c.(vocab.Finish); ok {
					fins = append(fins, fin)
				}
				if tc.stop {
					break
				}
			}
			if want := 1; tc.stop {
				want = 0
				if len(fins) != want {
					t.Fatalf("%d finishes after the consumer stopped", len(fins))
				}
			} else if len(fins) != want {
				t.Fatalf("%d finishes", len(fins))
			}
			if len(recs) != 1 {
				t.Fatalf("%d records", len(recs))
			}
			rec := recs[0]
			if !tc.stop && rec.Finish.Reason != fins[0].Reason {
				t.Fatal("the record's finish is not the one yielded")
			}
			if rec.Finish.Reason != tc.reason {
				t.Fatalf("reason %q, want %q", rec.Finish.Reason, tc.reason)
			}
			if tc.code == "" && rec.Finish.Failure != nil || tc.code != "" && (rec.Finish.Failure == nil || rec.Finish.Failure.Code != tc.code) {
				t.Fatalf("failure %v, want %q", rec.Finish.Failure, tc.code)
			}
			switch {
			case tc.kept == "" && rec.Message != nil:
				t.Fatalf("a message was kept: %+v", rec.Message.Blocks())
			case tc.kept != "" && (rec.Message == nil || rec.Message.Blocks()[0].Text.Text != tc.kept):
				t.Fatalf("kept %+v, want %q", rec.Message, tc.kept)
			}
			if d, err := rec.Draft(); err != nil || (rec.Succeeded() != (d.Type() == "assistant/message")) {
				t.Fatalf("draft %v %v", d.Type(), err)
			}
		})
	}
}

// DH-T-LLM-3: a tool call is assembled from its deltas; max_tokens drops calls;
// an interrupted attempt keeps its text but not an unfinished call.
func TestStreamAssemblesBlocks(t *testing.T) {
	call := []vocab.Chunk{vocab.BlockStart{Index: 1, Kind: vocab.KindToolUse},
		vocab.ToolCallDelta{Index: 1, ID: "c1", Name: "search"}, vocab.ToolCallDelta{Index: 1, ArgsDelta: `{"q":`},
		vocab.ToolCallDelta{Index: 1, ArgsDelta: `1}`}}
	run := func(cs ...vocab.Chunk) llm.Attempt {
		h := newSession(t)
		rt := newRuntime(t, &fake{scripts: []script{chunks(cs...)}})
		var rec llm.Attempt
		drain(must(rt.Prepare(bg, "r/a", "", vocab.Controls{})).Stream(bg, must(h.NextRequest()), llm.AttemptSinkFunc(func(a llm.Attempt) error { rec = a; return nil })))
		return rec
	}
	withText := append(text("let me look"), call...)
	rec := run(append(withText, vocab.Finish{Reason: vocab.FinishToolCalls})...)
	bs := rec.Message.Blocks()
	if !rec.Succeeded() || len(bs) != 2 || bs[1].ToolUse == nil || bs[1].ToolUse.ID != "c1" || string(bs[1].ToolUse.Args) != `{"q":1}` {
		t.Fatalf("tool call: %+v", rec)
	}
	if rec := run(append(withText, vocab.Finish{Reason: vocab.FinishMaxTokens})...); !rec.Succeeded() || len(rec.Message.Blocks()) != 1 {
		t.Fatalf("max_tokens kept a call: %+v", rec.Message.Blocks())
	}
	if rec := run(append(withText, vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()})...); rec.Succeeded() || len(rec.Message.Blocks()) != 1 {
		t.Fatalf("an interrupted attempt kept a call: %+v", rec.Message.Blocks())
	}
	endedCall := append(withText, vocab.BlockEnd{Index: 1, Block: vocab.ToolUseBlock("c1", "search", json.RawMessage(`{"q":1}`))})
	for _, fin := range []vocab.Finish{{Reason: vocab.FinishError, Failure: overloaded()}, {Reason: vocab.FinishAborted}} {
		rec := run(append(endedCall, fin)...)
		if bs := rec.Message.Blocks(); rec.Succeeded() || len(bs) != 1 || bs[0].Text == nil || bs[0].Text.Text != "let me look" {
			t.Fatalf("%s: an interrupted attempt kept an ended call: %+v", fin.Reason, bs)
		}
	}
	bad := []vocab.Chunk{vocab.ToolCallDelta{Index: 0, ID: "c1", Name: "search", ArgsDelta: `[1]`}, vocab.Finish{Reason: vocab.FinishToolCalls}}
	if rec := run(bad...); rec.Succeeded() || rec.Finish.Failure.Code != vocab.CodeMalformedResponse {
		t.Fatalf("a malformed call: %+v", rec.Finish)
	}
	ended := []vocab.Chunk{vocab.TextDelta{Index: 0, Text: "draft"}, vocab.BlockEnd{Index: 0, Block: vocab.TextBlock("final")},
		vocab.TextDelta{Index: 0, Text: " ignored"}, vocab.Finish{Reason: vocab.FinishStop}}
	if rec := run(ended...); rec.Message.Blocks()[0].Text.Text != "final" {
		t.Fatalf("BlockEnd is not authoritative: %+v", rec.Message.Blocks())
	}
}

// DH-T-LLM-4: routing and validation fail before any I/O with closed codes; a
// route id is served once; Replace swaps a set atomically; a request for
// another route is refused with one record.
func TestPrepareRoutesAndValidates(t *testing.T) {
	f := &fake{}
	rt := newRuntime(t, f)
	code := func(err error) vocab.Code {
		var fl *vocab.Failure
		if !errors.As(err, &fl) {
			t.Fatalf("not a failure: %v", err)
		}
		return fl.Code
	}
	temp := 3.0
	for _, tc := range []struct {
		route vocab.RouteID
		model string
		c     vocab.Controls
		want  vocab.Code
	}{
		{"r/none", "", vocab.Controls{}, vocab.CodeNoRoute},
		{"r/a", "m9", vocab.Controls{}, vocab.CodeUnknownModel},
		{"r/a", "m1", vocab.Controls{Effort: "max"}, vocab.CodeUnsupportedReasoningEffort},
		{"r/a", "m1", vocab.Controls{Temperature: &temp}, vocab.CodeUnsupportedOption},
	} {
		if _, err := rt.Prepare(bg, tc.route, tc.model, tc.c); code(err) != tc.want {
			t.Fatalf("%s/%s: %v, want %s", tc.route, tc.model, err, tc.want)
		}
	}
	if f.prepares.Load() != 0 || f.attempts.Load() != 0 {
		t.Fatal("a refused Prepare reached the adapter")
	}

	if _, err := rt.Register("other", llm.Route{ID: "r/a", Adapter: f}); !errors.Is(err, llm.ErrDuplicateRoute) {
		t.Fatalf("duplicate route: %v", err)
	}
	half := 0.5
	reg := must(rt.Register("b", llm.Route{ID: "r/b", Adapter: f, Defaults: vocab.Controls{Temperature: &half, MaxOutputTokens: 64}}))
	p := must(rt.Prepare(bg, "r/b", "any", vocab.Controls{MaxOutputTokens: 10}))
	if cfg := p.Config(); *cfg.Controls.Temperature != 0.5 || cfg.Controls.MaxOutputTokens != 10 || strings.Join(cfg.Defaulted, ",") != "temperature" {
		t.Fatalf("effective config %+v", cfg)
	}
	if err := reg.Replace(llm.Route{ID: "r/c", Adapter: f}, llm.Route{ID: "r/a", Adapter: f}); !errors.Is(err, llm.ErrDuplicateRoute) {
		t.Fatalf("Replace onto a served id: %v", err)
	}
	if got := rt.Routes(); strings.Join([]string{string(got[0]), string(got[1])}, ",") != "r/a,r/b" || len(got) != 2 {
		t.Fatalf("a failed Replace changed the set: %v", got)
	}
	must(0, reg.Replace(llm.Route{ID: "r/c", Adapter: f}))
	if _, err := rt.Prepare(bg, "r/b", "any", vocab.Controls{}); code(err) != vocab.CodeNoRoute {
		t.Fatal("a replaced route still serves")
	}
	reg.Close()
	if len(rt.Routes()) != 1 {
		t.Fatalf("Close left %v", rt.Routes())
	}

	// p captured r/b before the Replace; a request built for r/a is refused.
	var recs []llm.Attempt
	_, fins := drain(p.Stream(bg, must(newSession(t).NextRequest()), llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })))
	if len(fins) != 1 || len(recs) != 1 || recs[0].Finish.Failure.Code != vocab.CodeInvalidRequest || f.attempts.Load() != 0 {
		t.Fatalf("a request for another route: %+v", fins)
	}
}

// MaxConcurrent caps a route's attempts in flight; a waiter that is cancelled
// settles aborted.
func TestRouteConcurrencyCap(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	hold := func(ctx context.Context, y func(vocab.Chunk, error) bool) {
		started <- struct{}{}
		<-release
		for _, c := range append(text("ok"), vocab.Finish{Reason: vocab.FinishStop}) {
			y(c, nil)
		}
	}
	f := &fake{scripts: []script{hold, hold}}
	rt := llm.New()
	must(rt.Register("cap", llm.Route{ID: "r/a", Adapter: f, MaxConcurrent: 1}))
	h := newSession(t)
	req := must(h.NextRequest())
	prep := func() llm.Prepared { return must(rt.Prepare(bg, "r/a", "m", vocab.Controls{})) }
	var wg sync.WaitGroup
	recs := make(chan llm.Attempt, 3)
	sink := llm.AttemptSinkFunc(func(a llm.Attempt) error { recs <- a; return nil })
	wg.Add(1)
	go func() { defer wg.Done(); drain(prep().Stream(bg, req, sink)) }()
	<-started
	ctx, cancel := context.WithTimeout(bg, 20*time.Millisecond)
	defer cancel()
	drain(prep().Stream(ctx, req, sink))
	if a := <-recs; a.Finish.Failure == nil || a.Finish.Failure.Code != vocab.CodeAborted {
		t.Fatalf("a waiter past the cap: %+v", a.Finish)
	}
	if f.attempts.Load() != 1 {
		t.Fatal("the cap admitted a second attempt")
	}
	close(release)
	wg.Wait()
	if a := <-recs; !a.Succeeded() {
		t.Fatalf("the admitted attempt: %+v", a.Finish)
	}
}

// DH-T-LLM-3: a prepared call runs once. A second Stream, sequential or
// concurrent, is refused with llm/invalid-request and one record, before any
// provider I/O.
func TestPreparedCallIsSingleUse(t *testing.T) {
	ok := chunks(append(text("ok"), vocab.Finish{Reason: vocab.FinishStop})...)
	f := &fake{scripts: []script{ok}}
	rt := newRuntime(t, f)
	h := newSession(t)
	p := must(rt.Prepare(bg, "r/a", "", vocab.Controls{}))
	var recs []llm.Attempt
	sink := llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })
	drain(p.Stream(bg, must(h.NextRequest()), sink))
	_, fins := drain(p.Stream(bg, must(h.NextRequest()), sink))
	if len(fins) != 1 || len(recs) != 2 || !recs[0].Succeeded() || recs[1].Finish.Failure == nil ||
		recs[1].Finish.Failure.Code != vocab.CodeInvalidRequest || f.attempts.Load() != 1 {
		t.Fatalf("sequential reuse: %d finishes, records %+v, %d attempts", len(fins), recs, f.attempts.Load())
	}

	const n = 8
	f2 := &fake{scripts: []script{ok}}
	rt2 := newRuntime(t, f2)
	p2 := must(rt2.Prepare(bg, "r/a", "", vocab.Controls{}))
	req := must(h.NextRequest())
	out := make(chan llm.Attempt, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			drain(p2.Stream(bg, req, llm.AttemptSinkFunc(func(a llm.Attempt) error { out <- a; return nil })))
		}()
	}
	wg.Wait()
	close(out)
	var won, refused int
	for a := range out {
		switch {
		case a.Succeeded():
			won++
		case a.Finish.Failure != nil && a.Finish.Failure.Code == vocab.CodeInvalidRequest:
			refused++
		}
	}
	if won != 1 || refused != n-1 || f2.attempts.Load() != 1 {
		t.Fatalf("concurrent reuse: %d ran, %d refused, %d provider attempts", won, refused, f2.attempts.Load())
	}
}

// DH-T-LLM-3: a request whose controls differ from the prepared configuration
// is refused before the adapter runs; one with exactly the prepared controls,
// defaults materialised either way, runs.
func TestStreamRefusesAConfigMismatch(t *testing.T) {
	half := 0.5
	ok := chunks(append(text("ok"), vocab.Finish{Reason: vocab.FinishStop})...)
	f := &fake{scripts: []script{ok}}
	rt := llm.New()
	must(rt.Register("cfg", llm.Route{ID: "r/a", Adapter: f, Defaults: vocab.Controls{Temperature: &half}}))
	h := newSession(t) // its request header sets no controls
	var recs []llm.Attempt
	sink := llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })

	drain(must(rt.Prepare(bg, "r/a", "m", vocab.Controls{MaxOutputTokens: 32})).Stream(bg, must(h.NextRequest()), sink))
	if len(recs) != 1 || recs[0].Finish.Failure == nil || recs[0].Finish.Failure.Code != vocab.CodeInvalidRequest || f.attempts.Load() != 0 {
		t.Fatalf("a mismatched request ran: %+v, %d attempts", recs, f.attempts.Load())
	}
	drain(must(rt.Prepare(bg, "r/a", "m", vocab.Controls{Temperature: &half})).Stream(bg, must(h.NextRequest()), sink))
	if len(recs) != 2 || !recs[1].Succeeded() || f.attempts.Load() != 1 {
		t.Fatalf("a request matching the materialised config was refused: %+v", recs[1].Finish)
	}
}

// A route's slot is held until the provider attempt exits: a cancelled
// attempt settles at once, but while its iterator is still unwinding no
// second attempt is admitted under MaxConcurrent=1.
func TestSlotHeldUntilTheProviderExits(t *testing.T) {
	unwind := make(chan struct{})
	started := make(chan struct{}, 3)
	slow := func(ctx context.Context, y func(vocab.Chunk, error) bool) {
		started <- struct{}{}
		<-ctx.Done()
		<-unwind // the provider ignores cancellation for a while
	}
	ok := func(_ context.Context, y func(vocab.Chunk, error) bool) {
		started <- struct{}{}
		for _, c := range append(text("ok"), vocab.Finish{Reason: vocab.FinishStop}) {
			y(c, nil)
		}
	}
	f := &fake{scripts: []script{slow, ok}}
	rt := llm.New()
	must(rt.Register("cap", llm.Route{ID: "r/a", Adapter: f, MaxConcurrent: 1}))
	req := must(newSession(t).NextRequest())
	prep := func() llm.Prepared { return must(rt.Prepare(bg, "r/a", "m", vocab.Controls{})) }
	var recs []llm.Attempt
	sink := llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })

	ctx, cancel := context.WithCancel(bg)
	go func() { <-started; cancel() }()
	drain(prep().Stream(ctx, req, sink)) // returns while the provider is still unwinding
	if len(recs) != 1 || recs[0].Finish.Reason != vocab.FinishAborted {
		t.Fatalf("the cancelled attempt did not settle promptly: %+v", recs)
	}

	wctx, wcancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer wcancel()
	drain(prep().Stream(wctx, req, sink))
	if len(recs) != 2 || recs[1].Finish.Reason != vocab.FinishAborted || f.attempts.Load() != 1 {
		t.Fatalf("a second attempt overlapped the unwinding one: %+v, %d attempts", recs[1].Finish, f.attempts.Load())
	}

	close(unwind)
	drain(prep().Stream(bg, req, sink)) // blocks until the slot frees, then runs
	if len(recs) != 3 || !recs[2].Succeeded() || f.attempts.Load() != 2 {
		t.Fatalf("after the provider exited: %+v", recs[2].Finish)
	}
}
