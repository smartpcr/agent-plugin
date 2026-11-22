package question_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
)

var errInjected = errors.New("injected write failure")

// faultyLog fails every write carrying a question/answered while failing is set,
// and the next flushFails flushes (the appends stay buffered in the session).
type faultyLog struct {
	session.Handle
	failing    atomic.Bool
	flushFails atomic.Int32
	// appendFails fails the next single (ungrouped) appends.
	appendFails atomic.Int32
}

func (l *faultyLog) Flush(ctx context.Context) (event.Offset, error) {
	if l.flushFails.Add(-1) >= 0 {
		return 0, errInjected
	}
	l.flushFails.Store(0)
	return l.Handle.Flush(ctx)
}

// newService is a question service whose interactions end with the test.
func newService(t *testing.T, cfg question.Config) *question.Service {
	t.Helper()
	svc := question.New(cfg)
	t.Cleanup(svc.Close)
	return svc
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
	if l.refuse(d) || l.appendFails.Add(-1) >= 0 {
		return event.Receipt{}, errInjected
	}
	l.appendFails.Store(0)
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
	svc := newService(t, question.Config{Registry: f.reg})
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
	svc := newService(t, question.Config{Registry: f.reg})
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
	svc := newService(t, question.Config{Registry: f.reg})

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
	svc := newService(t, question.Config{Registry: f.reg, OnError: func(_ string, err error) {
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
	a := f.bind(t, answerer.Never())
	svc := newService(t, question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the question never reached the answerer", func() bool { return len(a.Asked()) == 1 })
	if _, err := f.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{ID: "ans-" + r.ID, Source: agent.SourceQuestion, By: "bob", Text: "yes"}); err != nil {
		t.Fatal(err)
	}
	svc2 := newService(t, question.Config{})
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
	svc := newService(t, question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Colour?", Wait: true})
	if err != nil || r.Outcome != question.OutcomeAnswered {
		t.Fatalf("result %+v, %v", r, err)
	}
	for i := 0; i < 2; i++ {
		svc2 := newService(t, question.Config{})
		if _, err := svc2.Restore(bg, f.h, f.inbox); err != nil {
			t.Fatal(err)
		}
	}
	p := f.inbox.Pending(agent.ListNextTurn)
	if len(p) != 1 || p[0].Text != "blue" || p[0].Call != "call-q" || p[0].By != "testkit" {
		t.Fatalf("redelivered %+v", p)
	}
}

// Without OnError a failed background settlement is not dropped: it is logged
// to the Owner's logger (and to slog with no Owner).
func TestFailedBackgroundSettlementIsLoggedByDefault(t *testing.T) {
	f := setup(t)
	a := f.bind(t, answerer.Deny())
	e := &cordis.MemoryExporter{}
	f.root.Loggers().ClearExporters()
	f.root.Loggers().AddExporter(e)
	log := &faultyLog{Handle: f.h}
	log.failing.Store(true)
	inbox, err := agent.OpenInbox(bg, log)
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(t, question.Config{Registry: f.reg, Owner: f.root})
	r, err := svc.Ask(bg, log, inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?"})
	if err != nil || r.Outcome != question.OutcomePending {
		t.Fatalf("result %+v, %v", r, err)
	}
	until(t, "the question never reached the answerer", func() bool { return len(a.Asked()) == 1 })
	until(t, "the failed settlement was not logged", func() bool {
		for _, m := range e.Messages() {
			if m.Level == cordis.LevelError && errors.Is(m.Err, errInjected) && strings.Contains(m.Text, r.ID) {
				return true
			}
		}
		return false
	})
	if len(svc.Open()) != 1 {
		t.Fatalf("open %v", svc.Open())
	}
}

// An answer whose flush fails is admitted but not yet durable: Reply reports
// the failure and the idle agent is not woken. A retry completes the
// durability with the same receipt, wakes the agent once, and admits nothing
// twice; afterwards the question refuses a second answer.
func TestRetryAfterAFailedFlushCompletesTheAnswer(t *testing.T) {
	f := setup(t)
	a := f.bind(t, answerer.Never())
	log := &faultyLog{Handle: f.h}
	inbox, err := agent.OpenInbox(bg, log)
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(t, question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, log, inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the question never reached the answerer", func() bool { return len(a.Asked()) == 1 })
	log.flushFails.Store(1)
	first, err := svc.Reply(bg, r.ID, human.Actor{Channel: "ui", Name: "bob"}, "yes")
	if !errors.Is(err, errInjected) {
		t.Fatalf("reply %v", err)
	}
	select {
	case <-inbox.Wake():
		t.Fatal("an answer that is not durable woke the agent")
	default:
	}
	again, err := svc.Reply(bg, r.ID, human.Actor{Channel: "ui", Name: "bob"}, "yes")
	if err != nil || again != first {
		t.Fatalf("retry %+v, %v; first %+v", again, err, first)
	}
	select {
	case <-inbox.Wake():
	default:
		t.Fatal("the completed answer did not wake the agent")
	}
	if p := inbox.Pending(agent.ListNextTurn); len(p) != 1 || !inbox.Durable() || len(f.answered(t)) != 1 {
		t.Fatalf("pending %+v, durable %v, answered %+v", p, inbox.Durable(), f.answered(t))
	}
	if _, err := svc.Reply(bg, r.ID, human.Actor{Channel: "ui"}, "no"); !errors.Is(err, question.ErrReplyQueued) {
		t.Fatalf("second answer %v", err)
	}
	if open, err := newService(t, question.Config{}).Restore(bg, f.h, inbox); err != nil || len(open) != 0 {
		t.Fatalf("restored %v, %v", open, err)
	}
}

// A hand-over that cannot be made durable, whose redelivery cannot even be
// admitted (flush, sync and requeue all fail), owes the redelivery: the call
// gets the error, and a retried Reply first admits the redelivery and only
// then reports recovery, so the live agent sees the answer without a restart,
// once.
func TestRetryAdmitsARedeliveryWhoseRequeueFailed(t *testing.T) {
	f := setup(t)
	a := f.bind(t, answerer.Never())
	log := &faultyLog{Handle: f.h}
	inbox, err := agent.OpenInbox(bg, log)
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(t, question.Config{Registry: f.reg})
	ch := askAsync(t, svc, log, inbox, question.Question{Agent: me, Call: "call-q", Text: "Colour?", Wait: true, Timeout: time.Hour})
	until(t, "the question never reached the answerer", func() bool { return len(a.Asked()) == 1 })
	id := svc.Open()[0]
	log.flushFails.Store(2)
	log.appendFails.Store(1)
	first, err := svc.Reply(bg, id, human.Actor{Channel: "ui", Name: "bob"}, "blue")
	if !errors.Is(err, errInjected) {
		t.Fatalf("reply %v", err)
	}
	if o := receive(t, ch); o.err == nil {
		t.Fatalf("the call was told %+v although the answer is not durable", o.r)
	}
	if n := len(inbox.Pending(agent.ListNextStep)) + len(inbox.Pending(agent.ListNextTurn)); n != 0 {
		t.Fatalf("%d items pending before the retry", n)
	}
	again, err := svc.Reply(bg, id, human.Actor{Channel: "ui", Name: "bob"}, "blue")
	if err != nil || again != first {
		t.Fatalf("retry %+v, %v; first %+v", again, err, first)
	}
	p := inbox.Pending(agent.ListNextTurn)
	if len(p) != 1 || p[0].ID != "ans-"+id+"-again" || p[0].Text != "blue" || p[0].Call != "call-q" || !inbox.Durable() {
		t.Fatalf("pending %+v, durable %v", p, inbox.Durable())
	}
	select {
	case <-inbox.Wake():
	default:
		t.Fatal("the redelivery did not wake the idle agent")
	}
	if _, err := svc.Reply(bg, id, human.Actor{Channel: "ui"}, "red"); !errors.Is(err, question.ErrReplyQueued) {
		t.Fatalf("second answer %v", err)
	}
	fresh, err := agent.OpenInbox(bg, f.h)
	if err != nil {
		t.Fatal(err)
	}
	if open, err := newService(t, question.Config{}).Restore(bg, f.h, fresh); err != nil || len(open) != 0 || len(fresh.Pending(agent.ListNextTurn)) != 1 {
		t.Fatalf("restored %v, %v, pending %+v", open, err, fresh.Pending(agent.ListNextTurn))
	}
}
