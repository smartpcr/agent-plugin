package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question/tool"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

var bg = context.Background()

var me = agent.Ref{Session: "s-ask", Incarnation: 1}

// flakyFlush fails the next fails flushes; the appends stay buffered.
type flakyFlush struct {
	session.Handle
	fails int
}

func (l *flakyFlush) Flush(ctx context.Context) (event.Offset, error) {
	if l.fails > 0 {
		l.fails--
		return 0, errors.New("injected flush failure")
	}
	return l.Handle.Flush(ctx)
}

// rig is a session whose model called name with args as c1, an executor with the
// ask_user_question tool (and failing, which asks then fails) over a question
// service whose first fails flushes fail.
type rig struct {
	h     session.Handle
	inbox *agent.Inbox
	svc   tools.Service
}

func newRig(t *testing.T, name, args string, fails int) *rig {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	reg := human.New()
	answerer.Register(t, root, reg, "ui", answerer.Text("blue"))
	if _, err := reg.Bind(root, me, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	h, err := session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: string(me.Session)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(bg) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", name, json.RawMessage(args)))
	if err != nil {
		t.Fatal(err)
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err == nil {
			_, err = h.Append(bg, d)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	flaky := &flakyFlush{Handle: h, fails: fails}
	inbox, err := agent.OpenInbox(bg, flaky)
	if err != nil {
		t.Fatal(err)
	}
	qs := question.New(question.Config{Registry: reg})
	t.Cleanup(qs.Close)
	resolve := func(agent.Ref) (question.Log, question.Inbox, error) { return flaky, inbox, nil }
	svc := core.NewWith(root, scope.NewTable(root), core.Config{})
	failing := tools.Define(tools.Spec[tool.In, tool.Out]{
		Name:        "failing",
		Description: "Asks, then fails after the answer was handed over.",
		Run: func(ctx context.Context, in tool.In, run tools.Run) (tool.Out, error) {
			if _, err := tool.Run(ctx, qs, resolve, in, run); err != nil {
				return tool.Out{}, err
			}
			return tool.Out{}, errors.New("failed after the hand-over")
		},
		Effects: []tools.Effect{tools.EffectSession},
	})
	for _, d := range []*tools.Declaration{tool.Declare(qs, resolve), failing} {
		if _, err := svc.Register(root, d); err != nil {
			t.Fatalf("register %s: %v", d.Name(), err)
		}
	}
	return &rig{h: h, inbox: inbox, svc: svc}
}

func (r *rig) execute(t *testing.T, name, args string) tools.Result {
	t.Helper()
	v, err := r.svc.Resolve(bg, tools.Target{Agent: me})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	res, err := r.svc.ExecuteBatch(bg, tools.Batch{View: v, Log: r.h, Calls: []tools.CallRequest{{ID: "c1", Name: name, Arguments: args}}})
	if err != nil {
		t.Fatal(err)
	}
	return res.Results[0]
}

// resume folds the session into a fresh inbox and question service, as a restart.
func (r *rig) resume(t *testing.T) ([]string, *agent.Inbox) {
	t.Helper()
	inbox, err := agent.OpenInbox(bg, r.h)
	if err != nil {
		t.Fatal(err)
	}
	qs := question.New(question.Config{})
	t.Cleanup(qs.Close)
	open, err := qs.Restore(bg, r.h, inbox)
	if err != nil {
		t.Fatal(err)
	}
	return open, inbox
}

const askArgs = `{"question":"Colour?","wait":true}`

// answeredAt is the offset of the session's question/answered.
func (r *rig) answeredAt(t *testing.T) event.Offset {
	t.Helper()
	evs, err := r.h.Read(bg, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range evs {
		if ev.Type() == question.AnsweredType.Name() {
			return event.Offset(i)
		}
	}
	t.Fatal("no question/answered")
	return 0
}

// A hand-over whose flush fails is acknowledged only once it is durable: Reply
// syncs, so when the tool returns the answer the group is already in storage
// (the session's durable offset is past question/answered); a resume neither
// reopens the question nor delivers the answer a second time.
func TestHandedAnswerUnderAFailedFlushReachesTheModelOnce(t *testing.T) {
	r := newRig(t, tool.Name, askArgs, 1)
	res := r.execute(t, tool.Name, askArgs)
	if res.IsError || !strings.Contains(res.Text, `"answer":"blue"`) {
		t.Fatalf("result %+v", res)
	}
	if at := r.answeredAt(t); r.h.Durable() <= at || !r.inbox.Durable() {
		t.Fatalf("answer acknowledged at offset %d with durable %d", at, r.h.Durable())
	}
	open, inbox := r.resume(t)
	if len(open) != 0 || len(inbox.Pending(agent.ListNextTurn)) != 0 {
		t.Fatalf("after resume: open %v, next-turn %+v", open, inbox.Pending(agent.ListNextTurn))
	}
}

// A call that fails after the hand-over writes an error result: the model never
// saw the answer, so a resume delivers it again next-turn, once.
func TestAnswerOfAFailedCallIsDeliveredAgainOnResume(t *testing.T) {
	r := newRig(t, "failing", askArgs, 0)
	if res := r.execute(t, "failing", askArgs); !res.IsError {
		t.Fatalf("result %+v, want an error", res)
	}
	for i := 0; i < 2; i++ {
		open, inbox := r.resume(t)
		p := inbox.Pending(agent.ListNextTurn)
		if len(open) != 0 || len(p) != 1 || p[0].Text != "blue" || p[0].Call != "c1" {
			t.Fatalf("resume %d: open %v, next-turn %+v", i, open, p)
		}
	}
}

// When the hand-over cannot be made durable (the flush and the sync both fail),
// the call is not told it succeeded: its result is an error, and the answer is
// queued again for the next step so this run still shows it to the model. Once
// storage recovers, a resume reopens nothing and delivers no third copy.
func TestHandOverThatCannotBeMadeDurableFailsTheCall(t *testing.T) {
	r := newRig(t, tool.Name, askArgs, 2)
	if res := r.execute(t, tool.Name, askArgs); !res.IsError {
		t.Fatalf("result %+v, want an error", res)
	}
	p := r.inbox.Pending(agent.ListNextStep)
	if len(p) != 1 || p[0].Text != "blue" || p[0].Call != "c1" {
		t.Fatalf("next-step %+v", p)
	}
	if _, err := r.h.Flush(bg); err != nil {
		t.Fatal(err)
	}
	open, inbox := r.resume(t)
	if len(open) != 0 || len(inbox.Pending(agent.ListNextTurn)) != 0 || len(inbox.Pending(agent.ListNextStep)) != 1 {
		t.Fatalf("after resume: open %v, next-turn %+v, next-step %+v", open, inbox.Pending(agent.ListNextTurn), inbox.Pending(agent.ListNextStep))
	}
}
