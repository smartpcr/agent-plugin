package testkit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/conform"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// request is a request for route r/a on a fresh session of a kit tree.
func request(t *testing.T) session.Request {
	t.Helper()
	ctx := context.Background()
	svc := session.NewService(testkit.Tree(t), memstore.New(), session.Config{})
	h := must(svc.Create(ctx, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(ctx) })
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))
	must(h.AppendGroup(ctx, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a"})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}))
	return must(h.NextRequest())
}

// settled is one attempt's record and when, in the bubble's time, it settled.
type settled struct {
	rec llm.Attempt
	at  time.Time
}

// attempt starts one attempt on route r/a under ctx and delivers its record.
func attempt(ctx context.Context, rt *llm.Runtime, req session.Request) <-chan settled {
	out := make(chan settled, 1)
	go func() {
		p, err := rt.Prepare(ctx, "r/a", "", vocab.Controls{})
		if err != nil {
			out <- settled{rec: llm.Attempt{Finish: vocab.Finish{Reason: vocab.FinishError}}}
			return
		}
		var rec llm.Attempt
		for range p.Stream(ctx, req, llm.AttemptSinkFunc(func(a llm.Attempt) error { rec = a; return nil })) {
		}
		out <- settled{rec: rec, at: time.Now()}
	}()
	return out
}

func pending(ch <-chan settled) bool {
	select {
	case <-ch:
		return false
	default:
		return true
	}
}

// DH-T-UTIL-12, both halves.
//
// The queue: an llm route's attempt slots are a bounded queue whose declaration
// (llm.Route.MaxConcurrent; llm/stream.go) says that a full route makes the
// next attempt wait without reaching its adapter, that the wait ends aborted
// when the attempt's context does, and that a slot is released when the
// attempt holding it exits. In a synctest bubble each "still waiting" is shown
// by the bubble settling, with no settle time, and the 10 s deadline passes in
// no wall time.
//
// The conform row: conform.ToolCancels is met by tools/conform.Cancellation
// (DH-T-TOOLS-9): a body that ignores its context fails it, one that stops
// passes.
//
//dsh:test DH-T-UTIL-12
func TestAFullBoundedQueueAndAnIgnoringToolBody(t *testing.T) {
	t.Run("a full route queue", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			req := request(t)
			s := model.NewScripted(t, model.Blocks(vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: "first"}),
				model.Answer("third"))
			rt := llm.New()
			model.RegisterRoute(t, rt, llm.Route{ID: "r/a", MaxConcurrent: 1, Adapter: s})

			holdCtx, release := context.WithCancel(t.Context())
			t.Cleanup(release)
			hold := attempt(holdCtx, rt, req)
			if err := s.WaitAttempts(t.Context(), 1); err != nil {
				t.Fatal(err)
			}

			waitCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			t.Cleanup(cancel)
			began := time.Now()
			waiter := attempt(waitCtx, rt, req)
			clock.Settle()
			if !pending(waiter) || len(s.Attempts()) != 1 {
				t.Fatalf("an attempt on a full route did not wait: %d adapter attempts", len(s.Attempts()))
			}
			clock.Advance(10 * time.Second)
			var w settled
			select {
			case w = <-waiter:
			default:
				t.Fatal("the waiting attempt outlived its context")
			}
			if w.rec.Finish.Reason != vocab.FinishAborted || w.rec.Finish.Failure == nil || w.rec.Finish.Failure.Code != vocab.CodeAborted {
				t.Fatalf("the waiting attempt settled %+v, want aborted", w.rec.Finish)
			}
			if waited := w.at.Sub(began); waited != 10*time.Second {
				t.Errorf("the wait ended after %v, want its 10s deadline", waited)
			}
			if n := len(s.Attempts()); n != 1 {
				t.Fatalf("an attempt that never had a slot reached the adapter: %d attempts", n)
			}

			next := attempt(t.Context(), rt, req)
			clock.Settle()
			if !pending(next) {
				t.Fatal("an attempt ran while the slot was still held")
			}
			release()
			if h := <-hold; h.rec.Finish.Reason != vocab.FinishAborted {
				t.Fatalf("the held attempt settled %+v", h.rec.Finish)
			}
			n := <-next
			if !n.rec.Succeeded() || n.rec.Message.Blocks()[0].Text.Text != "third" {
				t.Fatalf("the attempt after the release settled %+v", n.rec.Finish)
			}
			if got := len(s.Attempts()); got != 2 {
				t.Fatalf("%d adapter attempts, want 2", got)
			}
		})
	})

	t.Run("conform.ToolCancels", func(t *testing.T) {
		type noIn struct{}
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		stuck := tools.Define(tools.Spec[noIn, string]{Name: "stuck", Description: "Ignores cancellation.",
			Run: func(context.Context, noIn, tools.Run) (string, error) { <-release; return "late", nil }})
		if err := conform.Cancellation(stuck, nil, 50*time.Millisecond); err == nil || !strings.Contains(err.Error(), "did not return") {
			t.Fatalf("a body that ignores its context passed: %v", err)
		}
		polite := tools.Define(tools.Spec[noIn, string]{Name: "polite", Description: "Stops when cancelled.",
			Run: func(ctx context.Context, _ noIn, _ tools.Run) (string, error) {
				<-ctx.Done()
				return "", context.Cause(ctx)
			}})
		if err := conform.Cancellation(polite, nil, time.Second); err != nil {
			t.Fatalf("a body that stops failed: %v", err)
		}
	})
}
