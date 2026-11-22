//go:build manual

// The M1 manual check (impl.md DH-S-11, `manual`; tech-spec EV-6): flow F2's
// composition of the real components -- session, inbox, model selection, the
// guarded tool pipeline with one approval, the driver -- on the real DeepSeek
// route instead of the scripted model. Run one sends one prompt with one test
// tool and records the bracketed log and the request rebuilt from it; run two
// cancels a second turn mid-stream and records the cause on its turn/end. It is
// not a scenario and not a gate criterion: the build tag keeps it out of every
// default and gate run, and the key is read at run time only. Run with
//
//	$env:DEEPSEEK_API_KEY = '<key>'   # never committed, never logged
//	go -C src/harness test -tags manual -count=1 -v -run TestManualM1 ./internal/agent/
//
// and copy the printed lines into docs/stories/harness-DSH/evidence/DH-S-11.md.

package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/deepseek"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

const (
	m1Endpoint = "https://api.deepseek.com"
	m1Model    = "deepseek-chat"
	m1Prompt   = "Call the probe tool exactly once with the text \"ping\", then reply with what it returned."
	m1Long     = "Count slowly from 1 to 200, one number per line, with a short sentence about each number."
)

// m1Route registers the real DeepSeek route under a model-request permit for
// the endpoint, with the default bounded retry, and selects it.
func m1Route(t *testing.T, key string) func(*llm.Runtime) agent.ModelSelection {
	return func(rt *llm.Runtime) agent.ModelSelection {
		svc := egress.New(egress.Config{})
		t.Cleanup(svc.CloseIdleConnections)
		tr := must(transport.New(svc, permit.New(t, transport.Kind, m1Endpoint), transport.Options{Label: "deepseek", HeaderWait: 30 * time.Second}))
		t.Cleanup(tr.Close)
		keys := secret.NewStore(secret.EnvSource(func(name string) (string, bool) {
			if name == deepseek.KeyRef.EnvName() {
				return key, true
			}
			return "", false
		}))
		must(rt.Register("deepseek", must(deepseek.Route(deepseek.Config{Secrets: keys, Transport: tr, Retry: llm.DefaultRetry()}))))
		return agent.ModelSelection{Route: deepseek.RouteID, Model: m1Model}
	}
}

func m1Key(t *testing.T) string {
	t.Helper()
	key := os.Getenv(deepseek.KeyRef.EnvName())
	if key == "" {
		t.Skipf("manual: set %s at run time", deepseek.KeyRef.EnvName())
	}
	return key
}

func m1Wait(t *testing.T, r *f2Rig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 3*time.Minute)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
}

func m1TurnEnds(t *testing.T, evs []event.Event) []event.TurnEnd {
	t.Helper()
	var out []event.TurnEnd
	for _, e := range evs {
		if e.Type() == "turn/end" {
			var te event.TurnEnd
			must(0, json.Unmarshal([]byte(e.Raw()), &te))
			out = append(out, te)
		}
	}
	return out
}

// Run one: one prompt, one tool call with one approval, the bracketed log and
// every sent request rebuilt from the stored log at its requestAsOf.
func TestManualM1OneTurn(t *testing.T) {
	key := m1Key(t)
	st := memstore.New()
	r := f2Start(t, st, f2Hooks{wallClock: true}, m1Route(t, key))
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: m1Prompt}))
	m1Wait(t, r)

	evs := must(r.sess.Read(bg, 0, 0))
	t.Logf("M1 bracketed log (%d events, ids and digests numbered):\n%s", len(evs), f2Serialize(evs))
	if b := r.sess.Open(); b.IsOpen() {
		t.Fatalf("brackets still open: %+v", b)
	}
	if asked := r.ans.Asked(); len(asked) != 1 {
		t.Errorf("the answerer was asked %d times, want 1 (did the model call the tool?)", len(asked))
	}
	rd := must(st.OpenRead(bg, f2Session))
	defer func() { _ = rd.Close(bg) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	for i, req := range r.models.requests() {
		b := must(surface.Rebuild(bg, event.Default(), read, f2Session, req.AsOf()))
		msgs := must(json.Marshal(must(b.Messages())))
		t.Logf("M1 request %d: asOf=%d sent=%s rebuilt=%s equal=%t messages=%s",
			i+1, req.AsOf(), req.Digest(), b.Digest(), b.Digest() == req.Digest(), msgs)
		if b.Digest() != req.Digest() {
			t.Errorf("request %d: rebuilt digest %s, sent %s", i+1, b.Digest(), req.Digest())
		}
	}
	for _, te := range m1TurnEnds(t, evs) {
		t.Logf("M1 turn/end: turn=%d reason=%+v", te.Turn, te.Reason)
	}
}

// Run two: a turn whose reply streams long is cancelled after its first chunk;
// the cause is on its turn/end and the brackets close.
func TestManualM1CancelMidStream(t *testing.T) {
	key := m1Key(t)
	var once sync.Once
	first := make(chan struct{})
	hooks := f2Hooks{wallClock: true, chunk: func(int, vocab.Chunk) { once.Do(func() { close(first) }) }}
	r := f2Start(t, memstore.New(), hooks, m1Route(t, key))
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: m1Long}))
	select {
	case <-first:
	case <-time.After(2 * time.Minute):
		t.Fatal("no chunk streamed within 2m")
	}
	if err := r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	m1Wait(t, r)

	evs := must(r.sess.Read(bg, 0, 0))
	t.Logf("M1 cancelled log (%d events):\n%s", len(evs), f2Serialize(evs))
	if b := r.sess.Open(); b.IsOpen() {
		t.Fatalf("brackets still open after the cancel: %+v", b)
	}
	ends := m1TurnEnds(t, evs)
	if len(ends) != 1 {
		t.Fatalf("%d turn/end events, want 1", len(ends))
	}
	t.Logf("M1 cancel: turn/end reason=%+v", ends[0].Reason)
	if ends[0].Reason.Kind == event.ReasonCompleted {
		t.Errorf("the cancelled turn ended completed: %+v", ends[0].Reason)
	}
}
