package agent_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

type preStepFn = func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error)

func userMsg(id, s string) vocab.Message {
	return must(vocab.RestoreMessage(id, vocab.RoleUser, nil, vocab.TextBlock(s)))
}

// openTurn appends turn/start for turn n to the stub agent's session.
func openTurn(t *testing.T, a agent.Agent, n int) session.Handle {
	t.Helper()
	h := a.Session().(session.Handle)
	must(h.Append(bg, must(event.TurnStartType.Draft(event.TurnStart{Turn: n}))))
	return h
}

func types(t *testing.T, h session.Handle, from event.Offset) []string {
	t.Helper()
	var out []string
	for _, ev := range must(h.Read(bg, from, 100)) {
		out = append(out, ev.Type())
	}
	return out
}

func lastTurnEnd(t *testing.T, h session.Handle) event.TurnEnd {
	t.Helper()
	evs := must(h.Read(bg, h.Len()-1, 1))
	if evs[0].Type() != event.TurnEndType.Name() {
		t.Fatalf("last event %s", evs[0].Type())
	}
	var te event.TurnEnd
	must(0, json.Unmarshal([]byte(evs[0].Raw()), &te))
	return te
}

// DH-T-AGENT-10/-11/-12 (scenario): a PreStep listener returning Reject ends the
// turn blocked with who and why and appends no step/start, and a panicking
// observer is logged without changing that; a PreStep listener that panics
// ends the turn error with extension/failed, and the next turn still runs.
func TestPreStepRejectAndPanic(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	var observed []agent.StepNotice
	must(agent.StepDecided.On(e.root, func(_ *cordis.Context, n agent.StepNotice) error {
		observed = append(observed, n)
		panic("observer bug")
	}))
	reject := must(agent.PreStep.On(a.Scope(), preStepFn(func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		return agent.Reject{By: "quota-guard", Reason: "quota"}, nil
	})))

	h := openTurn(t, a, 1)
	start := h.Len()
	out := must(agent.ProposeStep(e.root, a, h, agent.PreStepRequest{Turn: 1, Step: 1, Messages: []vocab.Message{userMsg("u1", "hi")}}))
	if got := strings.Join(types(t, h, start), ","); got != "turn/end" {
		t.Fatalf("a rejected step appended %s", got)
	}
	te := lastTurnEnd(t, h)
	if out.Entered || te.Reason.Kind != event.ReasonBlocked || te.Reason.By != "quota-guard" || te.Reason.Detail != "quota" || out.Seq != h.Len()-1 {
		t.Fatalf("turn/end %+v, outcome %+v", te, out)
	}
	if len(observed) != 1 || observed[0].Entered || observed[0].Reason.Kind != event.ReasonBlocked {
		t.Fatalf("observed %+v", observed)
	}
	if !logged(e, "observer bug") {
		t.Fatal("the observer's panic was not recorded")
	}

	// A PreStep listener that panics ends the turn error, extension/failed.
	reject.Dispose()
	must(agent.PreStep.On(a.Scope(), preStepFn(func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		panic("listener bug")
	})))
	openTurn(t, a, 2)
	start = h.Len()
	out = must(agent.ProposeStep(e.root, a, h, agent.PreStepRequest{Turn: 2, Step: 1}))
	te = lastTurnEnd(t, h)
	if got := strings.Join(types(t, h, start), ","); got != "turn/end" || te.Reason.Kind != event.ReasonError ||
		te.Reason.By != agent.PreStep.Name() || !strings.Contains(te.Reason.Detail, "extension/failed") || !errors.Is(out.Err, agent.ErrExtensionFailed) {
		t.Fatalf("a panicking PreStep: %s %+v %v", got, te, out.Err)
	}
	if len(observed) != 2 {
		t.Fatal("the error outcome was not observed")
	}
	// A listener error is the same as a panic.
	e2 := newEnv(t)
	e2.seat(t)
	b := e2.create(t, "b").Agent()
	must(agent.PreStep.On(b.Scope(), preStepFn(func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		return nil, errors.New("listener failed")
	})))
	hb := openTurn(t, b, 1)
	if out := must(agent.ProposeStep(e2.root, b, hb, agent.PreStepRequest{Turn: 1, Step: 1})); lastTurnEnd(t, hb).Reason.Kind != event.ReasonError || !errors.Is(out.Err, agent.ErrExtensionFailed) {
		t.Fatalf("a failing PreStep: %+v", out)
	}
}

func logged(e *env, s string) bool {
	for _, m := range e.logs.Messages() {
		if strings.Contains(fmt.Sprint(m), s) {
			return true
		}
	}
	return false
}

// DH-T-AGENT-12: Enter replaces the batch and StartsSeries reaches the caller;
// step/start is appended; listeners see copies.
func TestPreStepEnterReplacesTheBatch(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	must(agent.PreStep.On(a.Scope(), preStepFn(func(_ *cordis.Context, req agent.PreStepRequest, next func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		req.Messages[0] = userMsg("x", "tampered")
		return agent.Enter{Messages: []vocab.Message{userMsg("u2", "replaced")}, StartsSeries: true}, nil
	})))
	h := openTurn(t, a, 1)
	batch := []vocab.Message{userMsg("u1", "hi")}
	start := h.Len()
	out := must(agent.ProposeStep(e.root, a, h, agent.PreStepRequest{Turn: 1, Step: 1, Messages: batch}))
	if !out.Entered || !out.Enter.StartsSeries || out.Enter.Messages[0].ID() != "u2" || batch[0].ID() != "u1" {
		t.Fatalf("Enter: %+v; caller batch %s", out, batch[0].ID())
	}
	if got := strings.Join(types(t, h, start), ","); got != "step/start" {
		t.Fatalf("an entered step appended %s", got)
	}
}

// DH-T-AGENT-13: KeepOpen from any one listener keeps the turn open in every
// registration order, messages joined in registration order; it is not asked
// after abort, error or budget; a listener error is extension/failed.
func TestTurnStoppingKeepOpenWinsInEveryOrder(t *testing.T) {
	allow := func(*cordis.Context, agent.StopRequest) (agent.StopDecision, error) { return agent.AllowStop{}, nil }
	keep := func(id string) agent.TurnStoppingListener {
		return func(*cordis.Context, agent.StopRequest) (agent.StopDecision, error) {
			return agent.KeepOpen{Messages: []vocab.Message{userMsg(id, id)}, Reason: id}, nil
		}
	}
	for pos := range 3 {
		for _, two := range []bool{false, true} {
			e := newEnv(t)
			e.seat(t)
			a := e.create(t, "a").Agent()
			var want []string
			for i := range 3 {
				l := agent.TurnStoppingListener(allow)
				if i == pos || (two && i == (pos+1)%3) {
					id := fmt.Sprintf("k%d", i)
					l, want = keep(id), append(want, id)
				}
				must(agent.OnTurnStopping(a.Scope(), l))
			}
			d := must(agent.AskTurnStopping(e.root, a.ScopeKey(), agent.StopRequest{Turn: 1, Reason: event.ReasonCompleted}))
			k, ok := d.(agent.KeepOpen)
			if !ok {
				t.Fatalf("pos %d two %v: %T", pos, two, d)
			}
			var got []string
			for _, m := range k.Messages {
				got = append(got, m.ID())
			}
			if strings.Join(got, ",") != strings.Join(sortedIDs(want), ",") {
				t.Fatalf("pos %d two %v: messages %v, want %v", pos, two, got, want)
			}
		}
	}

	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	asked := 0
	must(agent.OnTurnStopping(a.Scope(), func(*cordis.Context, agent.StopRequest) (agent.StopDecision, error) {
		asked++
		return agent.KeepOpen{}, nil
	}))
	for _, r := range []string{event.ReasonAborted, event.ReasonError, event.ReasonBudget} {
		if d := must(agent.AskTurnStopping(e.root, a.ScopeKey(), agent.StopRequest{Reason: r})); d != (agent.AllowStop{}) {
			t.Fatalf("%s: %T", r, d)
		}
	}
	if asked != 0 {
		t.Fatal("TurnStopping was asked after an abort, error or budget stop")
	}
	if d := must(agent.AskTurnStopping(e.root, e.create(t, "b").Agent().ScopeKey(), agent.StopRequest{Reason: event.ReasonCompleted})); d != (agent.AllowStop{}) {
		t.Fatal("A's listener kept B's turn open")
	}
	must(agent.OnTurnStopping(a.Scope(), func(*cordis.Context, agent.StopRequest) (agent.StopDecision, error) { panic("boom") }))
	if _, err := agent.AskTurnStopping(e.root, a.ScopeKey(), agent.StopRequest{Reason: event.ReasonCompleted}); !errors.Is(err, agent.ErrExtensionFailed) {
		t.Fatalf("a panicking TurnStopping listener: %v", err)
	}
}

func sortedIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// DH-T-AGENT-10: AttemptFailed gives up with no listener, retries when a
// listener says so, is not asked for a cancelled attempt, and a listener panic
// is extension/failed.
func TestAttemptFailed(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	f := must(vocab.NewFailure(vocab.CodeServer, "overloaded"))
	req := agent.AttemptFailure{Agent: a.Ref(), Turn: 1, Step: 1, Attempt: 1, Failure: f, AttemptsLeft: 2}
	if d := must(agent.AskAttemptFailed(e.root, a.ScopeKey(), req)); d != (agent.GiveUp{}) {
		t.Fatalf("no listener: %T", d)
	}
	asked := 0
	h := must(agent.AttemptFailed.On(a.Scope(), func(_ *cordis.Context, r agent.AttemptFailure, _ func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
		asked++
		if vocab.Transient == r.Failure.Class && r.AttemptsLeft > 0 {
			return agent.Retry{After: 2 * time.Second, Reason: "transient"}, nil
		}
		return agent.GiveUp{}, nil
	}))
	if d := must(agent.AskAttemptFailed(e.root, a.ScopeKey(), req)); d != (agent.Retry{After: 2 * time.Second, Reason: "transient"}) {
		t.Fatalf("retry: %#v", d)
	}
	req.Failure = must(vocab.NewFailure(vocab.CodeAborted, "cancelled"))
	if d := must(agent.AskAttemptFailed(e.root, a.ScopeKey(), req)); d != (agent.GiveUp{}) || asked != 1 {
		t.Fatalf("a cancelled attempt was asked about: %T, asked %d", d, asked)
	}
	h.Dispose()
	// SP-2: a listener that retries a Permanent failure (llm/auth) is overruled.
	hp := must(agent.AttemptFailed.On(a.Scope(), func(*cordis.Context, agent.AttemptFailure, func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
		return agent.Retry{Reason: "another key"}, nil
	}))
	req.Failure = must(vocab.NewFailure(vocab.CodeAuth, "bad key"))
	if d := must(agent.AskAttemptFailed(e.root, a.ScopeKey(), req)); d != (agent.GiveUp{}) {
		t.Fatalf("an auth failure was retried: %#v", d)
	}
	hp.Dispose()
	must(agent.AttemptFailed.On(a.Scope(), func(*cordis.Context, agent.AttemptFailure, func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
		panic("retry bug")
	}))
	req.Failure = f
	var ext *agent.ExtensionError
	if _, err := agent.AskAttemptFailed(e.root, a.ScopeKey(), req); !errors.As(err, &ext) || ext.Point != agent.AttemptFailed.Name() {
		t.Fatalf("a panicking AttemptFailed: %v", err)
	}
}
