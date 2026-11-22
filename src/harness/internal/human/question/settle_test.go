package question_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
)

var errInjected = errors.New("injected write failure")

// faultyLog fails every write carrying a question/answered while failing is set.
type faultyLog struct {
	session.Handle
	failing atomic.Bool
}

func (l *faultyLog) refuse(ds ...event.Draft) bool {
	if !l.failing.Load() {
		return false
	}
	for _, d := range ds {
		if d.Type() == question.AnsweredType.Name() {
			return true
		}
	}
	return false
}

func (l *faultyLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if l.refuse(d) {
		return event.Receipt{}, errInjected
	}
	return l.Handle.Append(ctx, d)
}

func (l *faultyLog) AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) {
	if l.refuse(ds...) {
		return nil, errInjected
	}
	return l.Handle.AppendGroup(ctx, ds)
}

// askAsync runs a waiting Ask; the result arrives on the returned channel.
func askAsync(t *testing.T, svc *question.Service, log question.Log, inbox question.Inbox, q question.Question) <-chan askOut {
	t.Helper()
	out := make(chan askOut, 1)
	exited := make(chan struct{})
	t.Cleanup(func() { <-exited })
	go func() {
		defer close(exited)
		r, err := svc.Ask(bg, log, inbox, q)
		out <- askOut{r, err}
	}()
	return out
}

type askOut struct {
	r   question.Result
	err error
}

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-tick.C:
		case <-timeout:
			t.Fatal(what)
		}
	}
}

func receive(t *testing.T, ch <-chan askOut) askOut {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting call was not released")
		return askOut{}
	}
}

// A Reply from outside the registry (another client) releases the waiting call
// at once with the answer and withdraws the still-unanswered registry item.
func TestReplyReleasesAWaitingAsk(t *testing.T) {
	f := setup(t)
	a := f.bind(t, answerer.Never())
	svc := question.New(question.Config{Registry: f.reg})
	ch := askAsync(t, svc, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?", Wait: true, Timeout: time.Hour})
	until(t, "the question never reached the answerer", func() bool { return len(a.Asked()) == 1 })
	id := string(a.Asked()[0].ID)
	if _, err := svc.Reply(bg, id, human.Actor{Channel: "cli", Name: "bob"}, "yes"); err != nil {
		t.Fatal(err)
	}
	o := receive(t, ch)
	if o.err != nil || o.r.Outcome != question.OutcomeAnswered || o.r.Text != "yes" || o.r.By.Principal != "bob" {
		t.Fatalf("result %+v, %v", o.r, o.err)
	}
	until(t, "the registry item was not withdrawn", func() bool { _, ok := a.Withdrawn(human.ID(id)); return ok })
	if p := f.reg.PendingAll(); len(p) != 0 {
		t.Fatalf("registry still holds %+v", p)
	}
	if an := f.answered(t); len(an) != 1 || !an[0].ToCall || len(svc.Open()) != 0 {
		t.Fatalf("answered %+v, open %v", an, svc.Open())
	}
}

// A late answer into a running turn is next-step input, not a turn of its own.
func TestLateAnswerIntoAnOpenTurnIsNextStep(t *testing.T) {
	f := setup(t)
	a := f.bind(t, answerer.Never())
	if _, err := f.h.Append(bg, mustDraft(event.TurnStartType.Draft(event.TurnStart{Turn: 1}))); err != nil {
		t.Fatal(err)
	}
	svc := question.New(question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the question never reached the answerer", func() bool { return len(a.Asked()) == 1 })
	if _, err := svc.Reply(bg, r.ID, human.Actor{Channel: "ui", Name: "bob"}, "yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.inbox.Pending(agent.ListNextStep)) != 1 || len(f.inbox.Pending(agent.ListNextTurn)) != 0 {
		t.Fatalf("next-step %v, next-turn %v", f.inbox.Pending(agent.ListNextStep), f.inbox.Pending(agent.ListNextTurn))
	}
}

// A settlement whose write fails is reported, writes nothing, and leaves the
// question open for a retry: no answer is queued for the model while the log
// says the question is open.
func TestFailedSettlementLeavesTheQuestionOpen(t *testing.T) {
	f := setup(t)
	a := f.bind(t, answerer.Never(), answerer.Never())
	log := &faultyLog{Handle: f.h}
	inbox, err := agent.OpenInbox(bg, log)
	if err != nil {
		t.Fatal(err)
	}
	svc := question.New(question.Config{Registry: f.reg})

	// A waiting call: the failed hand-over returns pending with the error.
	ch := askAsync(t, svc, log, inbox, question.Question{Agent: me, Call: "call-w", Text: "Wait?", Wait: true, Timeout: time.Hour})
	until(t, "the question never opened", func() bool { return len(a.Asked()) == 1 })
	wid := svc.Open()[0]
	log.failing.Store(true)
	if _, err := svc.Reply(bg, wid, human.Actor{Channel: "ui", Name: "bob"}, "yes"); !errors.Is(err, errInjected) {
		t.Fatalf("reply %v", err)
	}
	if o := receive(t, ch); !errors.Is(o.err, errInjected) || o.r.Outcome != question.OutcomePending {
		t.Fatalf("waiting call %+v, %v", o.r, o.err)
	}

	// A call that moved on: the failed reply queues nothing.
	r, err := svc.Ask(bg, log, inbox, question.Question{Agent: me, Call: "call-m", Text: "Later?"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the second question never reached the answerer", func() bool { return len(a.Asked()) == 2 })
	if _, err := svc.Reply(bg, r.ID, human.Actor{Channel: "ui", Name: "bob"}, "yes"); !errors.Is(err, errInjected) {
		t.Fatalf("reply %v", err)
	}
	for _, id := range []string{wid, r.ID} {
		if _, ok := inbox.Seen("ans-" + id); ok {
			t.Fatalf("a failed settlement admitted the answer of %s", id)
		}
	}
	if len(inbox.Pending(agent.ListNextStep))+len(inbox.Pending(agent.ListNextTurn)) != 0 || len(f.answered(t)) != 0 || len(svc.Open()) != 2 {
		t.Fatalf("after failures: answered %+v, open %v", f.answered(t), svc.Open())
	}

	// The fault clears: both questions take their answer, once.
	log.failing.Store(false)
	for _, id := range []string{wid, r.ID} {
		if _, err := svc.Reply(bg, id, human.Actor{Channel: "ui", Name: "bob"}, "yes"); err != nil {
			t.Fatalf("retry %s: %v", id, err)
		}
	}
	if len(inbox.Pending(agent.ListNextTurn)) != 2 || len(f.answered(t)) != 2 || len(svc.Open()) != 0 {
		t.Fatalf("after retry: pending %v, open %v", inbox.Pending(agent.ListNextTurn), svc.Open())
	}
}

// A close the registry produces (a refusal) whose audit fails is returned to the
// waiting call and reported; the question stays open.
func TestFailedCloseIsReported(t *testing.T) {
	f := setup(t)
	f.bind(t, answerer.Deny())
	log := &faultyLog{Handle: f.h}
	log.failing.Store(true)
	inbox, err := agent.OpenInbox(bg, log)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var reported []error
	svc := question.New(question.Config{Registry: f.reg, OnError: func(_ string, err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	}})
	r, err := svc.Ask(bg, log, inbox, question.Question{Agent: me, Text: "Deploy?", Wait: true})
	if !errors.Is(err, errInjected) || r.Outcome != question.OutcomePending {
		t.Fatalf("result %+v, %v", r, err)
	}
	until(t, "the failure was not reported", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reported) == 1 && errors.Is(reported[0], errInjected)
	})
	if len(svc.Open()) != 1 || len(f.answered(t)) != 0 {
		t.Fatalf("open %v, answered %+v", svc.Open(), f.answered(t))
	}
}

// Restore trusts the inbox: an answer admitted for a question is its
// settlement even if a question/answered cannot be found beside it, so the
// question is not reopened and a second answer is refused, not a duplicate.
func TestRestoreClosesAQuestionWhoseAnswerWasAdmitted(t *testing.T) {
	f := setup(t)
	f.bind(t, answerer.Never())
	svc := question.New(question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{ID: "ans-" + r.ID, Source: agent.SourceQuestion, By: "bob", Text: "yes"}); err != nil {
		t.Fatal(err)
	}
	svc2 := question.New(question.Config{})
	if open, err := svc2.Restore(bg, f.h, f.inbox); err != nil || len(open) != 0 {
		t.Fatalf("restored %v, %v", open, err)
	}
	if _, err := svc2.Reply(bg, r.ID, human.Actor{Channel: "ui"}, "again"); !errors.Is(err, question.ErrReplyQueued) {
		t.Fatalf("second answer %v", err)
	}
}

// An answer handed to a waiting call the crash ended before its tool result is
// delivered again on resume, next-turn, exactly once.
func TestRestoreRedeliversAnAnswerTheCrashTookFromItsCall(t *testing.T) {
	f := setup(t)
	f.bind(t, answerer.Text("blue"))
	svc := question.New(question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Colour?", Wait: true})
	if err != nil || r.Outcome != question.OutcomeAnswered {
		t.Fatalf("result %+v, %v", r, err)
	}
	for i := 0; i < 2; i++ {
		svc2 := question.New(question.Config{})
		if _, err := svc2.Restore(bg, f.h, f.inbox); err != nil {
			t.Fatal(err)
		}
	}
	p := f.inbox.Pending(agent.ListNextTurn)
	if len(p) != 1 || p[0].Text != "blue" || p[0].Call != "call-q" || p[0].By != "testkit" {
		t.Fatalf("redelivered %+v", p)
	}
}
