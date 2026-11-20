package llm_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// intercepted is a runtime whose attempts pass StreamAround through root, with
// route r/a on fa and route r/b on fb.
func intercepted(t *testing.T, fa, fb *fake) (*llm.Runtime, *cordis.Context) {
	t.Helper()
	root := cordis.New()
	t.Cleanup(func() { root.Dispose() })
	rt := llm.New(llm.WithInterception(root))
	must(rt.Register("a", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: fa}))
	must(rt.Register("b", llm.Route{ID: "r/b", Models: []llm.Model{{Name: "m1"}}, Adapter: fb}))
	return rt, root
}

type listener = func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error)

func listen(t *testing.T, root *cordis.Context, l listener) {
	t.Helper()
	h := must(llm.StreamAround.On(root, l))
	t.Cleanup(func() { h.Dispose() })
}

func served(s script) iter.Seq2[vocab.Chunk, error] {
	return func(y func(vocab.Chunk, error) bool) { s(bg, y) }
}

// one runs one attempt on r/a and returns its chunks, finishes and the one record.
func one(t *testing.T, rt *llm.Runtime) ([]vocab.Chunk, []vocab.Finish, llm.Attempt) {
	t.Helper()
	var recs []llm.Attempt
	all, fins := drain(must(rt.Prepare(bg, "r/a", "", vocab.Controls{})).Stream(bg, must(newSession(t).NextRequest()),
		llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })))
	if len(recs) != 1 || len(fins) != 1 {
		t.Fatalf("%d records, %d finishes; want one of each", len(recs), len(fins))
	}
	return all, fins, recs[0]
}

// DH-D-LLM-1: a Serve decision substitutes the stream; the provider is never
// called, the record names who served it and settles as any attempt does.
func TestStreamAroundServesASubstitutedStream(t *testing.T) {
	fa, fb := &fake{}, &fake{}
	rt, root := intercepted(t, fa, fb)
	var seen llm.StreamRequest
	listen(t, root, func(_ *cordis.Context, r llm.StreamRequest, _ func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
		seen = r
		return llm.Serve{Stream: served(chunks(append(text("replayed"), vocab.Finish{Reason: vocab.FinishStop})...)), ServedBy: "replay"}, nil
	})
	all, fins, rec := one(t, rt)
	if fa.attempts.Load() != 0 || fb.attempts.Load() != 0 {
		t.Fatal("a served attempt reached a provider")
	}
	if !rec.Succeeded() || rec.ServedBy != "replay" || rec.Route != "r/a" || rec.RequestDigest == "" || fins[0].Reason != vocab.FinishStop {
		t.Fatalf("served record = %+v", rec)
	}
	if len(all) != 4 || rec.Message.Blocks()[0].Text.Text != "replayed" {
		t.Fatalf("served chunks = %+v", all)
	}
	if seen.Route != "r/a" || seen.Model != "m1" || seen.Request.Digest() != rec.RequestDigest {
		t.Fatalf("the listener saw %+v", seen)
	}
	d := must(rec.Draft())
	if d.Type() != "assistant/message" || !strings.Contains(d.Raw(), `"served_by":"replay"`) {
		t.Fatalf("settlement event %s %s does not name who served it", d.Type(), d.Raw())
	}
}

// A served stream is normalised exactly like a provider's: a recorded failure,
// a missing finish, a panic and an unknown reason each become the one Finish
// and the one record, logged as assistant/attempt naming who served it.
func TestStreamAroundServedStreamIsNormalised(t *testing.T) {
	cases := map[string]struct {
		s    script
		code vocab.Code
	}{
		"failure": {chunks(append(text("part"), vocab.Finish{Reason: vocab.FinishError, Failure: overloaded()})...), vocab.CodeServer},
		"error": {func(_ context.Context, y func(vocab.Chunk, error) bool) {
			y(nil, must(vocab.NewFailure(vocab.CodeRateLimit, "recorded")))
		}, vocab.CodeRateLimit},
		"no finish": {chunks(text("part")...), vocab.CodeStreamClosed},
		"panic":     {func(context.Context, func(vocab.Chunk, error) bool) { panic("bad replay") }, vocab.CodeMalformedResponse},
		"unknown":   {chunks(append(text("x"), vocab.Finish{Reason: "weird"})...), vocab.CodeMalformedResponse},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fa := &fake{}
			rt, root := intercepted(t, fa, &fake{})
			listen(t, root, func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
				return llm.Serve{Stream: served(c.s), ServedBy: "replay"}, nil
			})
			_, fins, rec := one(t, rt)
			if rec.Succeeded() || rec.Finish.Failure == nil || rec.Finish.Failure.Code != c.code || fins[0].Reason != vocab.FinishError {
				t.Fatalf("finish = %+v, want error %s", rec.Finish, c.code)
			}
			if d := must(rec.Draft()); d.Type() != "assistant/attempt" || !strings.Contains(d.Raw(), `"served_by":"replay"`) || rec.ServedBy != "replay" {
				t.Fatalf("settlement %s %s", d.Type(), d.Raw())
			}
			if fa.attempts.Load() != 0 {
				t.Fatal("the provider ran")
			}
		})
	}
}

// DH-T-LLM-4: StreamAround cannot change the route. A listener that hands next
// another route, model or request is refused; one that swallows the refusal and
// proceeds still runs on the prepared route; Proceed reaches the provider with
// no served-by.
func TestStreamAroundCannotReroute(t *testing.T) {
	ok := chunks(append(text("ok"), vocab.Finish{Reason: vocab.FinishStop})...)
	for _, change := range []func(*llm.StreamRequest){
		func(r *llm.StreamRequest) { r.Route = "r/b" },
		func(r *llm.StreamRequest) { r.Model = "other" },
		func(r *llm.StreamRequest) { r.Config.Controls.MaxOutputTokens = 7 },
		func(r *llm.StreamRequest) { r.Request = must(newSession(t).NextRequest()) },
	} {
		fa, fb := &fake{scripts: []script{ok}}, &fake{scripts: []script{ok}}
		rt, root := intercepted(t, fa, fb)
		var refused error
		swallow := false
		listen(t, root, func(_ *cordis.Context, r llm.StreamRequest, next func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
			change(&r)
			d, err := next(r)
			refused = err
			if swallow {
				return llm.Proceed{}, nil
			}
			return d, err
		})
		_, _, rec := one(t, rt)
		if refused == nil || rec.Finish.Failure == nil || rec.Finish.Failure.Code != vocab.CodeInvalidRequest || fa.attempts.Load()+fb.attempts.Load() != 0 {
			t.Fatalf("a changed request was not refused: %v, %+v", refused, rec.Finish)
		}
		swallow = true
		_, _, rec = one(t, rt)
		if !rec.Succeeded() || rec.Route != "r/a" || rec.ServedBy != "" || fa.attempts.Load() != 1 || fb.attempts.Load() != 0 {
			t.Fatalf("after a swallowed refusal: %+v; r/a %d, r/b %d attempts", rec, fa.attempts.Load(), fb.attempts.Load())
		}
	}
}

// A listener's error, panic, missing decision or an incomplete Serve settles the
// attempt as llm/invalid-request before any provider I/O.
func TestStreamAroundListenerFailures(t *testing.T) {
	for name, l := range map[string]listener{
		"error": func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
			return nil, errors.New("boom")
		},
		"panic": func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
			panic("boom")
		},
		"nil": func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
			return nil, nil
		},
		"no stream": func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
			return llm.Serve{ServedBy: "replay"}, nil
		},
		"no served-by": func(*cordis.Context, llm.StreamRequest, func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
			return llm.Serve{Stream: served(chunks(vocab.Finish{Reason: vocab.FinishStop}))}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			fa := &fake{}
			rt, root := intercepted(t, fa, &fake{})
			listen(t, root, l)
			_, fins, rec := one(t, rt)
			if rec.Finish.Failure == nil || rec.Finish.Failure.Code != vocab.CodeInvalidRequest || fins[0].Reason != vocab.FinishError || fa.attempts.Load() != 0 {
				t.Fatalf("finish = %+v, %d attempts", rec.Finish, fa.attempts.Load())
			}
		})
	}
}

// A served stream takes no route slot: with MaxConcurrent=1 held by a provider
// attempt, a served attempt still runs.
func TestStreamAroundServedTakesNoSlot(t *testing.T) {
	hold, started := make(chan struct{}), make(chan struct{})
	slow := func(ctx context.Context, y func(vocab.Chunk, error) bool) {
		close(started)
		<-hold
		for _, c := range append(text("slow"), vocab.Finish{Reason: vocab.FinishStop}) {
			y(c, nil)
		}
	}
	fa := &fake{scripts: []script{slow}}
	root := cordis.New()
	t.Cleanup(func() { root.Dispose() })
	rt := llm.New(llm.WithInterception(root))
	must(rt.Register("a", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: fa, MaxConcurrent: 1}))
	serve := false
	listen(t, root, func(_ *cordis.Context, r llm.StreamRequest, next func(llm.StreamRequest) (llm.StreamDecision, error)) (llm.StreamDecision, error) {
		if serve {
			return llm.Serve{Stream: served(chunks(append(text("r"), vocab.Finish{Reason: vocab.FinishStop})...)), ServedBy: "replay"}, nil
		}
		return next(r)
	})
	done := make(chan llm.Attempt, 1)
	go func() { _, _, rec := one(t, rt); done <- rec }()
	<-started
	serve = true
	if _, _, rec := one(t, rt); !rec.Succeeded() || rec.ServedBy != "replay" {
		t.Fatalf("served attempt while the slot was held: %+v", rec.Finish)
	}
	close(hold)
	if rec := <-done; !rec.Succeeded() || rec.ServedBy != "" {
		t.Fatalf("provider attempt: %+v", rec)
	}
}
