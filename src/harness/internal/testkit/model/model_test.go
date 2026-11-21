package model_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// newSession is a session on a kit tree with an open step for route r/a.
func newSession(t *testing.T) session.Handle {
	t.Helper()
	ctx := context.Background()
	svc := session.NewService(testkit.Tree(t), memstore.New(), session.Config{})
	h := must(svc.Create(ctx, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(ctx) })
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))
	drafts := []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a"})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}
	must(h.AppendGroup(ctx, drafts))
	return h
}

// sink appends each record as its settlement event.
type sink struct{ h session.Handle }

func (s sink) Settle(a llm.Attempt) error {
	d, err := a.Draft()
	if err != nil {
		return err
	}
	_, err = s.h.Append(context.Background(), d)
	return err
}

// attempt makes one attempt on route r/a and returns the events it settled.
func attempt(t *testing.T, ctx context.Context, h session.Handle, tr *model.Transport) []event.Event {
	t.Helper()
	rt := llm.New()
	model.Register(t, rt, "r/a", tr)
	p := must(rt.Prepare(ctx, "r/a", "", vocab.Controls{}))
	start := h.Len()
	for range p.Stream(ctx, must(h.NextRequest()), sink{h}) {
	}
	return must(h.Read(context.Background(), start, 10))
}

// TC-06a: the fake model scripted with one text reply makes the real runtime
// settle one assistant/message carrying the scripted text, through the fake
// transport's SSE stream.
func TestFakeModelScriptsATurn(t *testing.T) {
	h := newSession(t)
	tr := model.NewTransport(t, model.Reply("scripted hello"))
	evs := attempt(t, context.Background(), h, tr)
	if len(evs) != 1 || evs[0].Type() != "assistant/message" {
		t.Fatalf("settled %d events, want one assistant/message: %+v", len(evs), evs)
	}
	var got struct {
		Message struct {
			Blocks []struct {
				Text struct{ Text string } `json:"text"`
			} `json:"blocks"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(evs[0].Raw()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Message.Blocks) != 1 || got.Message.Blocks[0].Text.Text != "scripted hello" {
		t.Fatalf("settled message %s, want the scripted text", evs[0].Raw())
	}
	reqs := tr.Requests()
	if len(reqs) != 1 || reqs[0].Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("transport saw %+v, want one streaming request", reqs)
	}
	var body model.Body
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil || body.Route != "r/a" || body.Model != model.ModelName || body.Digest == "" {
		t.Fatalf("request body %s (%v)", reqs[0].Body, err)
	}
}

// A refused status and an in-stream error each settle as a failed attempt with
// the mapped code; a hanging stream ends when the caller cancels.
func TestFakeModelScriptsFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		turn model.Turn
		code vocab.Code
	}{
		{"status", model.Status(503), vocab.CodeServer},
		{"in-stream", model.Turn{Events: []model.Event{model.Text("par"), model.Error(vocab.CodeRateLimit, "slow down")}}, vocab.CodeRateLimit},
		{"hang", model.Turn{Events: []model.Event{model.Text("par")}, Hang: true}, vocab.CodeAborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSession(t)
			ctx := context.Background()
			if tc.turn.Hang {
				c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				t.Cleanup(cancel)
				ctx = c
			}
			evs := attempt(t, ctx, h, model.NewTransport(t, tc.turn))
			if len(evs) != 1 || evs[0].Type() != "assistant/attempt" {
				t.Fatalf("settled %+v, want one assistant/attempt", evs)
			}
			var got struct {
				Failure *vocab.Failure `json:"failure"`
			}
			if err := json.Unmarshal([]byte(evs[0].Raw()), &got); err != nil {
				t.Fatal(err)
			}
			if got.Failure == nil || (got.Failure.Code != tc.code && !(tc.turn.Hang && got.Failure.Code == vocab.CodeTimeout)) {
				t.Fatalf("failure %+v, want %s: %s", got.Failure, tc.code, evs[0].Raw())
			}
		})
	}
}
