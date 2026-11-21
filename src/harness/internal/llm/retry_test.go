package llm_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/deepseek"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
)

// seqRT serves recorded fixtures in order, one per request; past the last it
// fails the test.
type seqRT struct {
	t  *testing.T
	fs []fixture
	mu sync.Mutex
	n  int
}

func (rt *seqRT) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	if rt.n >= len(rt.fs) {
		rt.t.Errorf("request %d: no fixture left", rt.n+1)
		return nil, errors.New("no fixture left")
	}
	f := rt.fs[rt.n]
	rt.n++
	return &http.Response{StatusCode: f.status, Status: http.StatusText(f.status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: f.header.Clone(), Body: io.NopCloser(bytes.NewReader(f.body)), Request: req}, nil
}

func (rt *seqRT) requests() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.n
}

// virtual is a runtime option set on a virtual clock that notes every wait.
type virtual struct {
	clk   *clock.Manual
	mu    sync.Mutex
	waits []time.Duration
}

func newVirtual() *virtual { return &virtual{clk: clock.NewManual(time.Unix(1_700_000_000, 0))} }

func (v *virtual) options() []llm.Option {
	return []llm.Option{
		llm.WithClock(func() int64 { return v.clk.Now().UnixMilli() }),
		llm.WithTimer(func(d time.Duration) <-chan time.Time {
			v.mu.Lock()
			v.waits = append(v.waits, d)
			v.mu.Unlock()
			return v.clk.After(d)
		}),
		llm.WithJitter(func() float64 { return 0.5 }),
	}
}

func (v *virtual) noted() []time.Duration {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]time.Duration(nil), v.waits...)
}

// complete runs Complete in the background and, while it waits, advances the
// virtual clock by each of advance in turn.
func complete(t *testing.T, v *virtual, rt *llm.Runtime, ctx context.Context, req session.Request, sink llm.AttemptSink, advance ...time.Duration) (vocab.Message, error) {
	t.Helper()
	type result struct {
		m   vocab.Message
		err error
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	done := make(chan result, 1)
	go func() {
		m, err := rt.Complete(ctx, req, sink)
		done <- result{m, err}
	}()
	for _, d := range advance {
		if !v.clk.BlockUntil(1, 5*time.Second) {
			t.Fatal("Complete never waited")
		}
		v.clk.Advance(d)
	}
	select {
	case r := <-done:
		return r.m, r.err
	case <-time.After(10 * time.Second):
		t.Fatal("Complete never returned")
	}
	return vocab.Message{}, nil
}

// auxRequest is an auxiliary request on a deepseek-official session.
func auxRequest(t *testing.T) (session.Request, *recordingSink) {
	t.Helper()
	h, _ := dsSession(t)
	ask := must(h.Append(bg, must(summaryAsk.Draft(askPayload{Hint: "x"}))))
	return must(h.RequestFrom(ask.Seq)), &recordingSink{h: h}
}

func settledTypes(t *testing.T, s *recordingSink) []string {
	t.Helper()
	var out []string
	for _, a := range s.records {
		out = append(out, must(a.Draft()).Type())
	}
	return out
}

// DH-T-LLM-7 (TC-09a): the overloaded-with-retry-after fixture twice, then
// success, on the virtual clock: three settlements are logged, the waits equal
// the fixture's Retry-After, and no fourth attempt is made. A policy of two
// retries over the overloaded fixture alone stops after its third attempt.
func TestCompleteRetriesOverloadedWithinBound(t *testing.T) {
	over, ok := loadFixture(t, "overloaded-retry-after"), loadFixture(t, "success")
	retryAfter := 2 * time.Second // the fixture's Retry-After: 2

	t.Run("then-success", func(t *testing.T) {
		v := newVirtual()
		tr := &seqRT{t: t, fs: []fixture{over, over, ok}}
		rt := llm.New(v.options()...)
		must(rt.Register("deepseek", must(deepseek.Route(deepseek.Config{Secrets: keyStore(), Transport: transport.Over(tr, transport.Options{})}))))
		req, sink := auxRequest(t)
		start := sink.h.Len()
		m, err := complete(t, v, rt, bg, req, sink, retryAfter, retryAfter)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Blocks()) == 0 {
			t.Fatal("no message")
		}
		equal := func(got []string, want ...string) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("settlements %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("settlements %v, want %v", got, want)
				}
			}
		}
		equal(settledTypes(t, sink), "assistant/attempt", "assistant/attempt", "assistant/message")
		if sink.h.Len() != start+3 {
			t.Fatalf("log grew by %d, want three settlements", sink.h.Len()-start)
		}
		evs := must(sink.h.Read(bg, start, 10))
		equal([]string{evs[0].Type(), evs[1].Type(), evs[2].Type()}, "assistant/attempt", "assistant/attempt", "assistant/message")
		if w := v.noted(); len(w) != 2 || w[0] != retryAfter || w[1] != retryAfter {
			t.Fatalf("waits %v, want two of the %v Retry-After", w, retryAfter)
		}
		if got := tr.requests(); got != 3 {
			t.Fatalf("requests %d, want 3: no fourth attempt", got)
		}
		for _, a := range sink.records[:2] {
			if f := a.Finish.Failure; f == nil || f.Class != vocab.Transient || f.RetryAfter != retryAfter {
				t.Fatalf("failed attempt %+v", a.Finish)
			}
		}
	})

	t.Run("bound", func(t *testing.T) {
		v := newVirtual()
		tr := &seqRT{t: t, fs: []fixture{over, over, over}}
		rt := llm.New(v.options()...)
		must(rt.Register("deepseek", must(deepseek.Route(deepseek.Config{Secrets: keyStore(), Transport: transport.Over(tr, transport.Options{}),
			Retry: llm.RetryPolicy{MaxRetries: 2}}))))
		req, sink := auxRequest(t)
		_, err := complete(t, v, rt, bg, req, sink, retryAfter, retryAfter)
		var fl *vocab.Failure
		if !errors.As(err, &fl) || fl.Code != vocab.CodeServer {
			t.Fatalf("Complete = %v, want the last overloaded failure", err)
		}
		if len(sink.records) != 3 || tr.requests() != 3 || len(v.noted()) != 2 {
			t.Fatalf("settlements %d, requests %d, waits %v: want 3, 3 and 2", len(sink.records), tr.requests(), v.noted())
		}
	})
}

// SP-2 (TC-09a): the bad-key fixture is settled exactly once with an auth
// failure; it is never retried and no other route is tried, though one is
// registered and would succeed.
func TestCompleteNeverRetriesABadKey(t *testing.T) {
	v := newVirtual()
	tr := &seqRT{t: t, fs: []fixture{loadFixture(t, "bad-key")}}
	rt := llm.New(v.options()...)
	must(rt.Register("deepseek", must(deepseek.Route(deepseek.Config{Secrets: keyStore(), Transport: transport.Over(tr, transport.Options{}),
		Retry: llm.PersistentRetry()}))))
	other := &fake{scripts: []script{chunks(append(text("from elsewhere"), vocab.Finish{Reason: vocab.FinishStop})...)}}
	must(rt.Register("other", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: other, Retry: llm.DefaultRetry()}))
	req, sink := auxRequest(t)
	_, err := complete(t, v, rt, bg, req, sink)
	var fl *vocab.Failure
	if !errors.As(err, &fl) || fl.Code != vocab.CodeAuth || fl.Class != vocab.Permanent {
		t.Fatalf("Complete = %v, want llm/auth", err)
	}
	if len(sink.records) != 1 || settledTypes(t, sink)[0] != "assistant/attempt" || sink.records[0].Route != deepseek.RouteID {
		t.Fatalf("settlements %v, want one assistant/attempt on %s", settledTypes(t, sink), deepseek.RouteID)
	}
	if c := sink.records[0].Finish.Failure.Class; c != vocab.Permanent {
		t.Fatalf("settled failure class %s", c)
	}
	if tr.requests() != 1 || len(v.noted()) != 0 {
		t.Fatalf("requests %d, waits %v: want one request and no wait", tr.requests(), v.noted())
	}
	if other.prepares.Load() != 0 || other.attempts.Load() != 0 {
		t.Fatal("another route was tried")
	}
}

// DH-T-LLM-8: Complete calls its sink once per attempt, each with that
// attempt's record, and exposes no chunk of a failed attempt: the message is
// the successful attempt's alone. A cancel during the wait makes no attempt.
func TestCompleteSinkOncePerAttempt(t *testing.T) {
	h := newSession(t)
	f := &fake{scripts: []script{
		chunks(vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: "partial"},
			vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()}),
		chunks(append(text("whole"), vocab.Finish{Reason: vocab.FinishStop})...),
	}}
	v := newVirtual()
	rt := llm.New(v.options()...)
	must(rt.Register("fake", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: f, Retry: llm.DefaultRetry()}))
	ask := must(h.Append(bg, must(summaryAsk.Draft(askPayload{Hint: "x"}))))
	req := must(h.RequestFrom(ask.Seq))
	sink := &recordingSink{h: h}
	m, err := complete(t, v, rt, bg, req, sink, llm.DefaultBaseDelay)
	if err != nil {
		t.Fatal(err)
	}
	if bs := m.Blocks(); len(bs) != 1 || bs[0].Text == nil || bs[0].Text.Text != "whole" {
		t.Fatalf("message %+v carries more than the successful attempt", bs)
	}
	if len(sink.records) != 2 || sink.records[0].Succeeded() || !sink.records[1].Succeeded() || f.prepares.Load() != 2 {
		t.Fatalf("records %d, prepares %d: want one per attempt", len(sink.records), f.prepares.Load())
	}
	if w := v.noted(); len(w) != 1 || w[0] != llm.DefaultBaseDelay {
		t.Fatalf("waits %v, want one base delay", w)
	}

	f.scripts = append(f.scripts, chunks(vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()}))
	ctx, cancel := context.WithCancel(bg)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := rt.Complete(ctx, req, sink); done <- err }()
	if !v.clk.BlockUntil(1, 5*time.Second) {
		t.Fatal("no wait")
	}
	cancel()
	var fl *vocab.Failure
	if err := <-done; !errors.As(err, &fl) || fl.Code != vocab.CodeAborted {
		t.Fatalf("cancelled Complete = %v, want llm/aborted", err)
	}
	if len(sink.records) != 3 || f.attempts.Load() != 3 {
		t.Fatalf("an attempt ran after the cancel: records %d, attempts %d", len(sink.records), f.attempts.Load())
	}
}

// DH-D-LLM-3: an auxiliary call keeps the route, model and policy its first
// attempt captured. A Replace during the wait (another adapter, another default
// model and a longer retry bound) reaches no later attempt of the call, and the
// originally exhausted bound ends it.
func TestCompleteKeepsTheCaptureAcrossAReplace(t *testing.T) {
	h := newSession(t)
	orig := &fake{scripts: []script{
		chunks(vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()}),
		chunks(vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()}),
	}}
	repl := &fake{scripts: []script{chunks(append(text("replaced"), vocab.Finish{Reason: vocab.FinishStop})...)}}
	v := newVirtual()
	rt := llm.New(v.options()...)
	g := must(rt.Register("fake", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: orig,
		Retry: llm.RetryPolicy{Mode: llm.RetryBounded, MaxRetries: 1}}))
	ask := must(h.Append(bg, must(summaryAsk.Draft(askPayload{Hint: "x"}))))
	req := must(h.RequestFrom(ask.Seq))
	sink := &recordingSink{h: h}

	ctx, cancel := context.WithCancel(bg)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := rt.Complete(ctx, req, sink); done <- err }()
	if !v.clk.BlockUntil(1, 5*time.Second) {
		t.Fatal("Complete never waited")
	}
	if err := g.Replace(llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m2"}}, Adapter: repl,
		Retry: llm.RetryPolicy{Mode: llm.RetryBounded, MaxRetries: 5}}); err != nil {
		t.Fatal(err)
	}
	v.clk.Advance(time.Minute)
	var fl *vocab.Failure
	select {
	case err := <-done:
		if !errors.As(err, &fl) || fl.Code != vocab.CodeServer {
			t.Fatalf("Complete = %v, want the captured bound exhausted with the last overloaded failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Complete never returned: the replaced, longer bound was used")
	}
	if orig.prepares.Load() != 2 || orig.attempts.Load() != 2 || repl.prepares.Load() != 0 {
		t.Fatalf("prepares orig %d, attempts orig %d, prepares replacement %d: want 2, 2, 0",
			orig.prepares.Load(), orig.attempts.Load(), repl.prepares.Load())
	}
	for i, a := range sink.records {
		if a.Model != "m1" || a.Route != "r/a" {
			t.Fatalf("attempt %d on %s/%s, want r/a/m1", i+1, a.Route, a.Model)
		}
	}
	if len(sink.records) != 2 || len(v.noted()) != 1 {
		t.Fatalf("records %d, waits %v: want 2 and one wait", len(sink.records), v.noted())
	}
}

// DH-T-LLM-7: the policy alone, pure: a bounded count, a persistent elapsed
// bound, a Permanent or Repairable failure never retried, a Retry-After
// honoured up to its cap, and the jitter inside its fraction.
func TestRetryPolicyNext(t *testing.T) {
	fail := func(code vocab.Code, after time.Duration) *vocab.Failure {
		f := must(vocab.NewFailure(code, "x"))
		f.RetryAfter = after
		return f
	}
	bounded := llm.DefaultRetry()
	for n := 1; n <= llm.DefaultMaxRetries; n++ {
		if _, why := bounded.Next(fail(vocab.CodeServer, 0), n, 0, 0.5); why != "" {
			t.Fatalf("attempt %d: %s", n, why)
		}
	}
	if _, why := bounded.Next(fail(vocab.CodeServer, 0), llm.DefaultMaxRetries+1, 0, 0.5); why != llm.AbandonExhausted {
		t.Fatalf("past the count: %q", why)
	}
	if _, why := (llm.RetryPolicy{}).Next(fail(vocab.CodeServer, 0), 1, 0, 0.5); why != llm.AbandonExhausted {
		t.Fatalf("the zero policy retried: %q", why)
	}
	for _, code := range []vocab.Code{vocab.CodeAuth, vocab.CodeContextWindowExceeded} {
		for _, p := range []llm.RetryPolicy{bounded, llm.PersistentRetry()} {
			if _, why := p.Next(fail(code, time.Second), 1, 0, 0.5); why != llm.AbandonNotRetryable {
				t.Fatalf("%s in %s mode: %q", code, p.Mode, why)
			}
		}
	}
	if _, why := bounded.Next(fail(vocab.CodeAborted, 0), 1, 0, 0.5); why != llm.AbandonCancelled {
		t.Fatalf("cancelled: %q", why)
	}
	if w, _ := bounded.Next(fail(vocab.CodeRateLimit, 7*time.Second), 1, 0, 0.9); w != 7*time.Second {
		t.Fatalf("Retry-After wait %v, want exactly 7s", w)
	}
	if w, _ := bounded.Next(fail(vocab.CodeRateLimit, 24*time.Hour), 1, 0, 0.5); w != llm.DefaultRetryAfterCap {
		t.Fatalf("an over-long Retry-After waited %v, want the cap", w)
	}
	if w, _ := llm.PersistentRetry().Next(fail(vocab.CodeServer, 0), 10, 0, 0.5); w != llm.DefaultMaxDelay {
		t.Fatalf("backoff %v, want capped at %v", w, llm.DefaultMaxDelay)
	}
	lo, _ := bounded.Next(fail(vocab.CodeServer, 0), 1, 0, 0)
	hi, _ := bounded.Next(fail(vocab.CodeServer, 0), 1, 0, 0.999)
	if lo != 450*time.Millisecond || hi < 549*time.Millisecond || hi > 550*time.Millisecond {
		t.Fatalf("jitter spread %v..%v, want 450ms..550ms", lo, hi)
	}
	persistent := llm.RetryPolicy{Mode: llm.RetryPersistent, MaxElapsed: time.Minute}
	if _, why := persistent.Next(fail(vocab.CodeServer, 0), 100, 30*time.Second, 0.5); why != "" {
		t.Fatalf("persistent inside its bound: %q", why)
	}
	if _, why := persistent.Next(fail(vocab.CodeServer, 10*time.Second), 2, 55*time.Second, 0.5); why != llm.AbandonElapsed {
		t.Fatalf("persistent past its bound: %q", why)
	}
	if err := (llm.RetryPolicy{Mode: "always"}).Validate(); err == nil {
		t.Fatal("an unknown mode validated")
	}
	if _, err := llm.New().Register("x", llm.Route{ID: "r/x", Adapter: &fake{}, Retry: llm.RetryPolicy{MaxRetries: -1}}); err == nil {
		t.Fatal("a negative retry bound registered")
	}
	p := must(func() (llm.Prepared, error) {
		rt := llm.New()
		must(rt.Register("x", llm.Route{ID: "r/x", Models: []llm.Model{{Name: "m"}}, Adapter: &fake{}, Retry: llm.RetryPolicy{MaxRetries: 3}}))
		return rt.Prepare(bg, "r/x", "", vocab.Controls{})
	}())
	if r := p.Retry(); r.MaxRetries != 3 || r.Mode != llm.RetryBounded || r.BaseDelay != llm.DefaultBaseDelay {
		t.Fatalf("captured policy %+v", r)
	}
}
