package driver_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// keepOnce keeps the first stopping turn open with msgs and lets every later
// one stop. It returns how often it was asked.
func keepOnce(t *testing.T, root *cordis.Context, msgs ...vocab.Message) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	must(agent.OnTurnStopping(root, func(*cordis.Context, agent.StopRequest) (agent.StopDecision, error) {
		if asked.Add(1) == 1 {
			return agent.KeepOpen{Messages: msgs, Reason: "more to do"}, nil
		}
		return agent.AllowStop{}, nil
	}))
	return &asked
}

// Point 14: a turn-stopping listener that keeps the turn open has its user,
// developer and system messages inserted, in order and in one group, and the
// next step runs on them; the turn then completes.
func TestKeepOpenInsertsItsMessagesAndRunsAStep(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Reply("one"), fakemodel.Reply("two")}})
	user := must(vocab.NewMessage(vocab.RoleUser, vocab.TextBlock("keep going")))
	dev := must(vocab.NewMessage(vocab.RoleDeveloper, vocab.TextBlock("a developer note")))
	sys := must(vocab.NewMessage(vocab.RoleSystem, vocab.TextBlock("a system note")))
	asked := keepOnce(t, r.root, user, dev, sys)
	r.send(agent.ListNextTurn, "start")
	r.idle()

	evs := r.events()
	if n := len(ofType(evs, "step/start")); n != 2 || asked.Load() != 2 {
		t.Fatalf("%d steps and %d stopping asks, want 2 and 2", n, asked.Load())
	}
	var order []string
	for _, e := range evs {
		switch e.Type() {
		case event.UserMessage.Name(), event.DeveloperMessage.Name(), event.SystemMessage.Name():
			order = append(order, e.Type())
		}
	}
	want := []string{event.UserMessage.Name(), event.UserMessage.Name(), event.DeveloperMessage.Name(), event.SystemMessage.Name()}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("inserted messages %v, want the claimed user message, then %v", order, want[1:])
	}
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v, want completed", got)
	}
	if n := len(r.tr.Requests()); n != 2 {
		t.Fatalf("%d model requests, want the second step's too", n)
	}
	checkClosed(t, r.sess)
}

// A kept-open turn cannot insert an assistant message: the driver stops with
// that error and calls the model no more.
func TestKeepOpenWithAnAssistantMessageStopsTheDriver(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Reply("one")}, failOK: true})
	bad := must(vocab.NewMessage(vocab.RoleAssistant, vocab.TextBlock("I said so")))
	keepOnce(t, r.root, bad)
	r.send(agent.ListNextTurn, "start")
	within(t, r.drv.Done(), "the driver's stop")
	if err := r.drv.Err(); err == nil || !strings.Contains(err.Error(), "cannot insert a assistant message") {
		t.Fatalf("driver error %v, want the refused insert", err)
	}
	if n := len(r.tr.Requests()); n != 1 {
		t.Fatalf("%d model requests, want only the first step's", n)
	}
}

// A turn-stopping listener's error ends the turn error{by the point} with the
// error as its detail, and is reported through agent.Failed.
func TestTurnStoppingErrorEndsTheTurn(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Reply("one")}})
	errListener := errors.New("stopping listener broke")
	must(agent.OnTurnStopping(r.root, func(*cordis.Context, agent.StopRequest) (agent.StopDecision, error) {
		return nil, errListener
	}))
	failed := make(chan error, 1)
	must(agent.Failed.On(r.root, func(_ *cordis.Context, n agent.FailedNotice) error {
		select {
		case failed <- n.Err:
		default:
		}
		return nil
	}))
	r.send(agent.ListNextTurn, "start")
	r.idle()
	ends := turnEnds(t, r.events())
	if len(ends) != 1 || ends[0].Kind != event.ReasonError || ends[0].By == "" || !strings.Contains(ends[0].Detail, errListener.Error()) {
		t.Fatalf("turn/end %+v, want error by the turn-stopping point", ends)
	}
	select {
	case err := <-failed:
		if !errors.Is(err, errListener) {
			t.Fatalf("Failed notice error %v, want the listener's", err)
		}
	default:
		t.Fatal("no Failed notice for the listener's error")
	}
	checkClosed(t, r.sess)
}

// A step whose reply stopped at the token limit ends its turn max_tokens.
func TestMaxTokensEndsTheTurn(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{{Events: []fakemodel.Event{
		fakemodel.Text("cut"), fakemodel.Usage(vocab.Usage{Input: 1, Output: 1}), fakemodel.Finish(vocab.FinishMaxTokens),
	}}}})
	r.send(agent.ListNextTurn, "start")
	r.idle()
	if ends := turnEnds(t, r.events()); len(ends) != 1 || ends[0].Kind != event.ReasonMaxTokens {
		t.Fatalf("turn/end %+v, want max_tokens", ends)
	}
	checkClosed(t, r.sess)
}
