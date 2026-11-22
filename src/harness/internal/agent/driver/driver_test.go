package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

const route vocab.RouteID = "r/a"

// rig is one driven agent on the scripted model.
type rig struct {
	t     *testing.T
	root  *cordis.Context
	store *memstore.Store
	sess  session.Handle
	inbox *agent.Inbox
	tr    *fakemodel.Transport
	drv   *driver.Driver
	// requests receives the number of each model request as it is sent.
	requests chan int
	// sent records every request the driver streamed, for the replay check.
	sent *recorder
	// skipReplay turns off the DH-T-AGENT-8 check run after the test, for a
	// case whose log another writer changed.
	skipReplay bool
}

type rigOptions struct {
	root   *cordis.Context
	svc    tools.Service
	budget *driver.Budget
	hooks  driver.Hooks
	clock  *clock.Manual
	turns  []fakemodel.Turn
	// id names the session; "" is the test's name.
	id string
	// store is the session store; nil is a new memstore.
	store *memstore.Store
	// wrap wraps the session the driver, the inbox and the model write.
	wrap func(session.Session) session.Session
	// model replaces the scripted transport (turns is then unused).
	model http.RoundTripper
	// failOK accepts a driver that stopped on a failure.
	failOK bool
	// options edits the driver's options before Start.
	options func(*driver.Options)
}

// counting reports each request on a channel once the script has answered it.
type counting struct {
	next http.RoundTripper
	mu   sync.Mutex
	n    int
	ch   chan int
}

func (c *counting) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(r)
	c.mu.Lock()
	c.n++
	n := c.n
	c.mu.Unlock()
	select {
	case c.ch <- n:
	default:
	}
	return resp, err
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	if o.root == nil {
		o.root, o.svc = setup(t)
		must(o.svc.Register(o.root, tool("echo")))
	}
	id := o.id
	if id == "" {
		id = "s-" + t.Name()
	}
	st := o.store
	if st == nil {
		st = memstore.New()
	}
	sess := must(session.NewService(o.root, st, session.Config{}).Create(bg, nil,
		session.CreateOptions{Header: session.Header{ID: id}}))
	t.Cleanup(func() { _ = sess.Close(bg) })
	var live session.Session = sess
	if o.wrap != nil {
		live = o.wrap(sess)
	}
	r := &rig{t: t, root: o.root, store: st, sess: sess, requests: make(chan int, 64)}
	model := o.model
	if model == nil {
		r.tr = fakemodel.NewTransport(t, o.turns...)
		model = r.tr
	}
	rt := llm.New()
	fakemodel.Register(t, rt, route, &counting{next: model, ch: r.requests})
	r.sent = &recorder{Models: rt}
	r.inbox = must(agent.OpenInbox(bg, live))
	mod := must(agent.OpenModel(bg, live, agent.ModelOptions{Initial: agent.ModelSelection{Route: route}}))
	opts := driver.Options{
		Context: o.root, Agent: stubAgent{ref: agent.Ref{Session: session.ID(id), Incarnation: 1}},
		Session: live, Inbox: r.inbox, Model: mod, Models: r.sent, Tools: o.svc,
		Budget: o.budget, Hooks: o.hooks,
	}
	if o.clock != nil {
		opts.Now, opts.After = o.clock.Now, o.clock.After
	}
	if o.options != nil {
		o.options(&opts)
	}
	r.drv = must(driver.Start(opts))
	// Cleanups run last first: the driver closes, then the replay check of
	// agent-loop.md §9.1 runs, then the session closes.
	t.Cleanup(func() {
		if r.skipReplay || r.drv.Err() != nil || t.Failed() {
			return
		}
		r.replayed()
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 10*time.Second)
		defer cancel()
		if err := r.drv.Close(ctx); err != nil && !o.failOK {
			t.Errorf("close the driver: %v", err)
		}
	})
	return r
}

func (r *rig) send(list agent.List, text string) {
	r.t.Helper()
	must(r.inbox.Send(bg, list, agent.InboxMessage{Source: agent.SourceUser, Text: text}))
}

// waitRequest waits for model request n to be sent.
func (r *rig) waitRequest(n int) {
	r.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case got := <-r.requests:
			if got >= n {
				return
			}
		case <-deadline:
			r.t.Fatalf("model request %d was never sent", n)
		}
	}
}

func (r *rig) idle() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		r.t.Fatalf("wait idle: %v", err)
	}
}

func (r *rig) events() []event.Event {
	r.t.Helper()
	return must(r.sess.Read(bg, 0, 0))
}

func decode[T any](t *testing.T, e event.Event) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(e.Raw()), &v); err != nil {
		t.Fatalf("decode %s: %v", e.Type(), err)
	}
	return v
}

func ofType(evs []event.Event, typ string) []event.Event {
	var out []event.Event
	for _, e := range evs {
		if e.Type() == typ {
			out = append(out, e)
		}
	}
	return out
}

// turnEnds are the turn/end reasons of the log, in order.
func turnEnds(t *testing.T, evs []event.Event) []event.Reason {
	var out []event.Reason
	for _, e := range ofType(evs, "turn/end") {
		out = append(out, decode[event.TurnEnd](t, e).Reason)
	}
	return out
}

func stepEnds(t *testing.T, evs []event.Event) []event.StepEnd {
	var out []event.StepEnd
	for _, e := range ofType(evs, "step/end") {
		out = append(out, decode[event.StepEnd](t, e))
	}
	return out
}

// checkClosed asserts the log's brackets balance and every tool call in the
// history has its result.
func checkClosed(t *testing.T, s session.ReadSession) {
	t.Helper()
	if b := s.Open(); b.IsOpen() {
		t.Fatalf("brackets still open: %+v", b)
	}
	evs := must(s.Read(bg, 0, 0))
	if ts, te := len(ofType(evs, "turn/start")), len(ofType(evs, "turn/end")); ts != te {
		t.Fatalf("%d turn/start, %d turn/end", ts, te)
	}
	if ss, se := len(ofType(evs, "step/start")), len(ofType(evs, "step/end")); ss != se {
		t.Fatalf("%d step/start, %d step/end", ss, se)
	}
	unanswered(t, s)
}

// unanswered fails t when a tool call of s's history has no result after it.
func unanswered(t *testing.T, s session.ReadSession) {
	t.Helper()
	msgs := must(s.History().Messages())
	open := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Blocks() {
			switch {
			case b.ToolUse != nil:
				open[b.ToolUse.ID] = true
			case b.ToolResult != nil:
				delete(open, b.ToolResult.CallID)
			}
		}
	}
	if len(open) != 0 {
		t.Fatalf("tool calls left unanswered in history: %v", open)
	}
}

func echo(id string) fakemodel.Event { return fakemodel.Tool(id, "echo", `{"text":"x"}`) }

// DH-T-AGENT-5, -7: a turn opens on a wake, brackets one step per model call
// and ends completed when the model answers without a tool call.
func TestTurnRunsStepsUntilTheModelAnswers(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "hi")
	r.idle()
	evs := r.events()
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v, want one completed", got)
	}
	if se := stepEnds(t, evs); len(se) != 2 || se[0].Cause != "" || se[1].Step != 2 {
		t.Fatalf("step/end %+v", se)
	}
	if n := len(ofType(evs, "tool/result")); n != 1 {
		t.Fatalf("%d tool results, want 1", n)
	}
	checkClosed(t, r.sess)
	if r.drv.Status() != agent.StatusIdle || r.drv.Err() != nil {
		t.Fatalf("status %s, err %v", r.drv.Status(), r.drv.Err())
	}
}

// DH-T-AGENT-8: a step budget of 3 with a model that always calls a tool runs
// exactly 3 steps, and turn/end records budget{steps, 3}.
func TestTurnBudgetClosesTheTurn(t *testing.T) {
	b := driver.Budget{MaxSteps: 3}
	r := newRig(t, rigOptions{budget: &b, turns: []fakemodel.Turn{
		fakemodel.Calls(echo("c1")), fakemodel.Calls(echo("c2")), fakemodel.Calls(echo("c3"))}})
	r.send(agent.ListNextTurn, "loop")
	r.idle()
	evs := r.events()
	if n := len(ofType(evs, "step/start")); n != 3 {
		t.Fatalf("%d steps ran, want 3", n)
	}
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonBudget || got[0].Limit != driver.LimitSteps || got[0].Value != 3 {
		t.Fatalf("turn/end %+v, want budget{steps, 3}", got)
	}
	checkClosed(t, r.sess)
	if n := len(r.sent.requests()); n != 3 {
		t.Fatalf("%d requests sent, want 3", n)
	}
	r.replayed()
}

// DH-T-AGENT-28: the defaults bound a turn; a turn inside them is not cut.
func TestDefaultBudgetLeavesAShortTurnAlone(t *testing.T) {
	if b := driver.DefaultBudget(); b.MaxSteps != driver.DefaultMaxSteps || b.MaxAttemptsPerStep != driver.DefaultMaxAttemptsPerStep || b.MaxDuration != 0 {
		t.Fatalf("defaults %+v", b)
	}
	if err := (driver.Budget{MaxSteps: -1}).Validate(); err == nil {
		t.Fatal("a negative limit was accepted")
	}
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Calls(echo("c2")), fakemodel.Reply("ok")}})
	if r.drv.Budget() != driver.DefaultBudget() {
		t.Fatalf("budget %+v", r.drv.Budget())
	}
	r.send(agent.ListNextTurn, "go")
	r.idle()
	if got := turnEnds(t, r.events()); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
}

// The time budget cancels the turn with cause budget: the stream stops, the
// step closes with cause budget and the turn/end is budget{duration}.
func TestTimeBudgetCancelsTheTurn(t *testing.T) {
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	b := driver.Budget{MaxDuration: time.Second}
	r := newRig(t, rigOptions{budget: &b, clock: clk, turns: []fakemodel.Turn{{Hang: true}}})
	r.send(agent.ListNextTurn, "slow")
	r.waitRequest(1)
	if !clk.BlockUntil(1, 10*time.Second) {
		t.Fatal("the time limit never waited")
	}
	clk.Advance(time.Second)
	r.idle()
	evs := r.events()
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonBudget || got[0].Limit != driver.LimitDuration || got[0].Value != 1000 {
		t.Fatalf("turn/end %+v, want budget{duration, 1000}", got)
	}
	if se := stepEnds(t, evs); len(se) != 1 || se[0].Cause != string(agent.CauseBudget) {
		t.Fatalf("step/end %+v", se)
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-16 (architecture.md §6.4 F4): a step cancel settles step 2's
// attempt cancelled, closes step 2 with cause user and writes no turn/end;
// point 14 is reached once for step 2 and, with input parked, step 3 opens
// with it.
func TestCancelStepResumesAtContinueOrStop(t *testing.T) {
	var mu sync.Mutex
	var points [][2]int
	hooks := driver.Hooks{ContinueOrStop: func(turn, step int) {
		mu.Lock()
		points = append(points, [2]int{turn, step})
		mu.Unlock()
	}}
	r := newRig(t, rigOptions{hooks: hooks, turns: []fakemodel.Turn{
		fakemodel.Calls(echo("c1")), {Hang: true}, fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "start")
	r.waitRequest(2)
	r.send(agent.ListNextStep, "parked")
	if err := r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeStep, Inbox: agent.InboxKeep}); err != nil {
		t.Fatal(err)
	}
	r.idle()
	evs := r.events()

	var step2 []event.Event
	in := false
	for _, e := range evs {
		if e.Type() == "step/start" {
			in = decode[event.StepStart](t, e).Step == 2
		}
		if in {
			step2 = append(step2, e)
		}
		if e.Type() == "step/end" {
			in = false
		}
	}
	atts := ofType(step2, "assistant/attempt")
	if len(atts) != 1 {
		t.Fatalf("step 2 settled %d attempts: %v", len(atts), step2)
	}
	if f := decode[event.AssistantAttemptPayload](t, atts[0]).Failure; f == nil || f.Class != vocab.Cancelled {
		t.Fatalf("step 2's attempt settled %+v, want cancelled", f)
	}
	se := stepEnds(t, evs)
	if len(se) != 3 || se[1].Step != 2 || se[1].Cause != string(agent.CauseUser) || se[2].Cause != "" {
		t.Fatalf("step/end %+v", se)
	}
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v: the cancel wrote one, or step 3 did not complete", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(points) != 3 || points[1] != [2]int{1, 2} || points[2] != [2]int{1, 3} {
		t.Fatalf("point 14 reached at %v", points)
	}
	if len(r.inbox.Pending(agent.ListNextStep)) != 0 {
		t.Fatal("the parked message was not claimed into step 3")
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-16, second run: with an empty inbox and a final answer, a step
// cancel that reaches the driver at point 14 finds no step to stop; the turn
// ends completed without a step 3.
func TestCancelStepAtContinueOrStopWithEmptyInboxCompletes(t *testing.T) {
	var r *rig
	var cancelErr error
	hooks := driver.Hooks{ContinueOrStop: func(_, step int) {
		if step == 2 {
			cancelErr = r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeStep, Inbox: agent.InboxKeep})
		}
	}}
	r = newRig(t, rigOptions{hooks: hooks, turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("final")}})
	r.send(agent.ListNextTurn, "start")
	r.idle()
	if cancelErr != nil {
		t.Fatal(cancelErr)
	}
	evs := r.events()
	if n := len(ofType(evs, "step/start")); n != 2 {
		t.Fatalf("%d steps, want 2", n)
	}
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-18: a step cancel mid-stream with nothing parked ends the turn
// aborted with the cause; the delivered text is kept as an interrupted message
// with no tool call.
func TestCancelStepMidStreamKeepsThePrefix(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{{Events: []fakemodel.Event{fakemodel.Text("partial")}, Hang: true}}})
	r.send(agent.ListNextTurn, "start")
	r.waitRequest(1)
	// The hanging body serves the prefix at once and then blocks; give the
	// stream time to deliver it before cancelling.
	time.Sleep(200 * time.Millisecond)
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeStep, Inbox: agent.InboxKeep}))
	r.idle()
	evs := r.events()
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseUser) {
		t.Fatalf("turn/end %+v, want aborted{user}", got)
	}
	msgs := ofType(evs, "assistant/message")
	if len(msgs) != 1 {
		t.Fatalf("%d assistant messages, want the interrupted prefix", len(msgs))
	}
	m := decode[struct {
		Message struct {
			Blocks []vocab.Block `json:"blocks"`
		} `json:"message"`
	}](t, msgs[0])
	if len(m.Message.Blocks) != 1 || m.Message.Blocks[0].Text == nil || m.Message.Blocks[0].Text.Text != "partial" {
		t.Fatalf("prefix %s", msgs[0].Raw())
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-15, -17: a turn cancel closes the step and ends the turn
// aborted{cause}; of two cancels the first cause is the one recorded; kept
// next-turn input stays parked until a new wake.
func TestCancelTurnRecordsTheFirstCause(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{{Hang: true}, fakemodel.Reply("next")}})
	r.send(agent.ListNextTurn, "first")
	r.waitRequest(1)
	r.send(agent.ListNextTurn, "second")
	keep := agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}
	must(0, r.drv.Cancel(agent.CauseParent, keep))
	must(0, r.drv.Cancel(agent.CauseUser, keep))
	r.idle()
	evs := r.events()
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseParent) {
		t.Fatalf("turn/end %+v, want aborted{parent}", got)
	}
	if se := stepEnds(t, evs); len(se) != 1 || se[0].Cause != string(agent.CauseParent) {
		t.Fatalf("step/end %+v", se)
	}
	if n := len(r.inbox.Pending(agent.ListNextTurn)); n != 1 {
		t.Fatalf("%d next-turn items pending, want the kept one", n)
	}
	checkClosed(t, r.sess)
	// A new wake runs the kept input.
	r.tr.Script(fakemodel.Reply("third"))
	r.send(agent.ListNextTurn, "third")
	r.idle()
	if got := turnEnds(t, r.events()); len(got) != 3 || got[1].Kind != event.ReasonCompleted || got[2].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
}

// DH-T-AGENT-17: a turn cancel with Discard logs each parked item discarded
// with the cause, from both lists.
func TestCancelTurnDiscardsParkedInput(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{{Hang: true}}})
	r.send(agent.ListNextTurn, "first")
	r.waitRequest(1)
	r.send(agent.ListNextTurn, "later")
	r.send(agent.ListNextStep, "steer")
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxDiscard}))
	r.idle()
	if len(r.inbox.Pending(agent.ListNextTurn))+len(r.inbox.Pending(agent.ListNextStep)) != 0 {
		t.Fatal("parked input survived a discard")
	}
	discarded := 0
	for _, e := range ofType(r.events(), agent.SplicedType.Name()) {
		s := decode[agent.Splice](t, e)
		if s.Outcome != nil && s.Outcome.Kind == agent.OutcomeDiscarded {
			if s.Outcome.Cause != string(agent.CauseUser) {
				t.Fatalf("discard cause %q", s.Outcome.Cause)
			}
			discarded++
		}
	}
	if discarded != 2 {
		t.Fatalf("%d discards logged, want 2", discarded)
	}
	if got := turnEnds(t, r.events()); len(got) != 1 || got[0].Kind != event.ReasonAborted {
		t.Fatalf("turn/end %+v", got)
	}
	checkClosed(t, r.sess)
}

// A cancel names a closed cause, a scope and an inbox choice; there is no
// default.
func TestCancelNeedsScopeAndInboxChoice(t *testing.T) {
	r := newRig(t, rigOptions{})
	for _, tc := range []struct {
		c agent.Cause
		o agent.CancelOptions
	}{
		{agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn}},
		{agent.CauseUser, agent.CancelOptions{Inbox: agent.InboxKeep}},
		{"timeout", agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}},
	} {
		if err := r.drv.Cancel(tc.c, tc.o); !errors.Is(err, agent.ErrInvalidCancel) {
			t.Fatalf("Cancel(%q, %+v) = %v, want invalid-cancel", tc.c, tc.o, err)
		}
	}
	if err := r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}); err != nil {
		t.Fatalf("a cancel with nothing running: %v", err)
	}
}

// A provider failure with no retry listener gives up: the step closes and the
// turn ends error with the failure.
func TestProviderFailureEndsTheTurn(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Status(500)}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	got := turnEnds(t, r.events())
	if len(got) != 1 || got[0].Kind != event.ReasonError || got[0].Failure == nil {
		t.Fatalf("turn/end %+v, want error with the failure", got)
	}
	checkClosed(t, r.sess)
}
