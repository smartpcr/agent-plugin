package question_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question/tool"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

var bg = context.Background()

var me = agent.Ref{Session: "s-question", Incarnation: 1}

type fixture struct {
	root     *cordis.Context
	sessions session.Service
	h        session.Handle
	inbox    *agent.Inbox
	reg      human.Registry
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: cordis.New(), reg: human.New()}
	t.Cleanup(f.root.Dispose)
	f.sessions = session.NewService(f.root, memstore.New(), session.Config{})
	h, err := f.sessions.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s-question"}})
	if err != nil {
		t.Fatal(err)
	}
	f.h = h
	t.Cleanup(func() { f.h.Close(bg) })
	if f.inbox, err = agent.OpenInbox(bg, h); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) bind(t *testing.T, replies ...answerer.Reply) *answerer.Answerer {
	t.Helper()
	a := answerer.Register(t, f.root, f.reg, "ui", replies...)
	if _, err := f.reg.Bind(f.root, me, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fixture) types(t *testing.T) string {
	t.Helper()
	evs, err := f.h.Read(bg, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type())
	}
	return strings.Join(out, ",")
}

func (f *fixture) answered(t *testing.T) []question.Answered {
	t.Helper()
	evs, _ := f.h.Read(bg, 0, 100)
	var out []question.Answered
	for _, ev := range evs {
		if ev.Type() == question.AnsweredType.Name() {
			a, err := question.AnsweredType.As(ev)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, a)
		}
	}
	return out
}

// TC-10a, DH-D-HUMAN-5: the model asks with wait through the tool; the fake
// answerer replies; the reply enters the inbox, the waiting call returns the
// answer, and question/asked and question/answered are both logged, the answer
// attributed to its principal and channel.
func TestQuestionAnsweredThroughTheInbox(t *testing.T) {
	f := setup(t)
	f.bind(t, answerer.Text("blue"))
	svc := question.New(question.Config{Registry: f.reg})
	resolve := func(agent.Ref) (question.Log, question.Inbox, error) { return f.h, f.inbox, nil }
	if err := tool.Declare(svc, resolve).Err(); err != nil {
		t.Fatalf("declaration: %v", err)
	}
	out, err := tool.Run(bg, svc, resolve, tool.In{Question: "Which colour?", Choices: "blue|red", Wait: true}, tools.Run{CallID: "call-q", Agent: me})
	if err != nil || out.Status != "answered" || out.Answer != "blue" || out.By != "testkit" {
		t.Fatalf("tool result %+v, %v", out, err)
	}
	if got := f.types(t); got != "question/asked,agent/inbox/spliced,agent/inbox/spliced,question/answered" {
		t.Fatalf("log %s", got)
	}
	a := f.answered(t)[0]
	if a.ID != out.ID || a.Call != "call-q" || a.By != (question.By{Principal: "testkit", Channel: "ui"}) || !a.ToCall || a.Message == "" {
		t.Fatalf("answered %+v", a)
	}
	if _, ok := f.inbox.Seen(a.Message); !ok || len(f.inbox.Pending(agent.ListNextStep)) != 0 {
		t.Fatal("the answer did not pass through the inbox, or is still pending after the call took it")
	}
	if _, err := svc.Reply(bg, out.ID, human.Actor{Channel: "ui", Name: "bob"}, "red"); !errors.Is(err, question.ErrReplyQueued) {
		t.Fatalf("second answer %v", err)
	}
}

// DH-T-HUMAN-8 (subset), the in-process half of DH-E-HUMAN-6: a timed question
// returns pending at its deadline; after a restart the question is open again;
// the late answer becomes one user message naming the call, which wakes the
// idle agent and starts a turn; a second answer is question/reply-queued.
func TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage(t *testing.T) {
	f := setup(t)
	f.bind(t, answerer.Never())
	clk := clock.NewManual(time.Unix(0, 0))
	svc := question.New(question.Config{Registry: f.reg, After: clk.After})
	done := make(chan question.Result, 1)
	exited := make(chan struct{})
	t.Cleanup(func() { <-exited })
	go func() {
		defer close(exited)
		r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-q", Text: "Deploy?", Wait: true, Timeout: 10 * time.Second})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	if !clk.BlockUntil(1, 5*time.Second) {
		t.Fatal("the ask never armed its deadline")
	}
	clk.Advance(10 * time.Second)
	r := <-done
	if r.Outcome != question.OutcomePending {
		t.Fatalf("result %+v", r)
	}

	// Restart: the host stops, the session is resumed, the service restored.
	if err := f.h.Close(bg); err != nil {
		t.Fatal(err)
	}
	h2, _, err := f.sessions.Open(bg, f.root, "s-question", session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.h = h2
	inbox2, err := agent.OpenInbox(bg, h2)
	if err != nil {
		t.Fatal(err)
	}
	svc2 := question.New(question.Config{Registry: f.reg})
	reopened, err := svc2.Restore(bg, h2, inbox2)
	if err != nil || len(reopened) != 1 || reopened[0] != r.ID {
		t.Fatalf("restored %v, %v", reopened, err)
	}
	rec, err := svc2.Reply(bg, r.ID, human.Actor{Channel: "ui", Name: "alice"}, "yes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.Reply(bg, r.ID, human.Actor{Channel: "ui", Name: "alice"}, "no"); !errors.Is(err, question.ErrReplyQueued) {
		t.Fatalf("second answer %v", err)
	}
	// The resumed agent is idle: the late answer is next-turn input, wakes it,
	// and the wake starts the answer's own turn — no turn was opened by hand.
	select {
	case <-inbox2.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("the late answer did not wake the idle agent")
	}
	m, ok, err := inbox2.ClaimTurn(bg, 1)
	if err != nil || !ok || m.ID != rec.MessageID || m.Call != "call-q" || m.Text != "yes" || m.By != "alice" {
		t.Fatalf("claimed %+v, %v, %v", m, ok, err)
	}
	got := []agent.InboxMessage{m}
	msg, _ := got[0].Message()
	src, _ := msg.Source()
	if src.Kind != agent.SourceQuestion || !strings.Contains(string(src.Data), "call-q") {
		t.Fatalf("user message source %+v", src)
	}
	if a := f.answered(t); len(a) != 1 || a[0].ToCall || a[0].By.Principal != "alice" {
		t.Fatalf("answered %+v", a)
	}
	// A third service restored from the log refuses the answered question too.
	svc3 := question.New(question.Config{})
	if open, err := svc3.Restore(bg, h2, inbox2); err != nil || len(open) != 0 {
		t.Fatalf("restored %v, %v", open, err)
	}
	if _, err := svc3.Reply(bg, r.ID, human.Actor{Channel: "ui"}, "again"); !errors.Is(err, question.ErrReplyQueued) {
		t.Fatalf("answer after a second restart %v", err)
	}
}

// DH-T-HUMAN-8 (subset): a blocking call with no binding closes no-answerer; a
// call that moves on returns pending at once and its answer is queued.
func TestNoBindingAndMovingOn(t *testing.T) {
	f := setup(t)
	svc := question.New(question.Config{Registry: f.reg})
	r, err := svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Text: "Anyone?", Wait: true})
	if err != nil || r.Outcome != question.OutcomeNoAnswerer {
		t.Fatalf("unbound %+v, %v", r, err)
	}
	if a := f.answered(t); len(a) != 1 || a[0].Outcome != question.OutcomeNoAnswerer || a[0].By.Principal != question.PrincipalHarness {
		t.Fatalf("answered %+v", a)
	}

	a := f.bind(t, answerer.Text("later"))
	r, err = svc.Ask(bg, f.h, f.inbox, question.Question{Agent: me, Call: "call-2", Text: "When?"})
	if err != nil || r.Outcome != question.OutcomePending {
		t.Fatalf("moved on %+v, %v", r, err)
	}
	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for len(f.inbox.Pending(agent.ListNextTurn)) == 0 {
		select {
		case <-tick.C:
		case <-timeout:
			t.Fatal("the answer never reached the inbox")
		}
	}
	p := f.inbox.Pending(agent.ListNextTurn)
	if len(p) != 1 || p[0].Text != "later" || p[0].Call != "call-2" || len(a.Asked()) != 1 {
		t.Fatalf("pending %+v", p)
	}
}

func mustDraft(d event.Draft, err error) event.Draft {
	if err != nil {
		panic(err)
	}
	return d
}
