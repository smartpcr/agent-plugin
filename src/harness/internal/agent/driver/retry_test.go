package driver_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

const backoff = time.Second

// retrying registers an AttemptFailed listener that retries every failure
// after wait, and returns the attempts it was asked about.
func retrying(t *testing.T, root *cordis.Context, wait time.Duration) func() []int {
	t.Helper()
	var mu sync.Mutex
	var asked []int
	must(agent.AttemptFailed.On(root, func(_ *cordis.Context, f agent.AttemptFailure, _ func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
		mu.Lock()
		asked = append(asked, f.Attempt)
		mu.Unlock()
		return agent.Retry{After: wait, Reason: "transient"}, nil
	}))
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), asked...)
	}
}

// waitBackoff waits for the driver to park on the retry wait, then ends it.
func waitBackoff(t *testing.T, clk *clock.Manual) {
	t.Helper()
	if !clk.BlockUntil(1, 10*time.Second) {
		t.Fatal("the driver never waited to retry")
	}
	clk.Advance(backoff)
}

// DH-T-AGENT-10: two failures then success give two assistant/attempt and one
// assistant/message in one step; the user message is admitted once and each
// retry waits on the clock.
func TestRetryAfterTwoFailuresSucceedsInOneStep(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	asked := retrying(t, root, backoff)
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	r := newRig(t, rigOptions{root: root, svc: svc, clock: clk, turns: []fakemodel.Turn{
		fakemodel.Status(503), fakemodel.Status(503), fakemodel.Reply("ok")}})
	r.send(agent.ListNextTurn, "go")
	waitBackoff(t, clk)
	waitBackoff(t, clk)
	r.idle()
	evs := r.events()
	if n := len(ofType(evs, "assistant/attempt")); n != 2 {
		t.Fatalf("%d assistant/attempt, want 2", n)
	}
	if n := len(ofType(evs, "assistant/message")); n != 1 {
		t.Fatalf("%d assistant/message, want 1", n)
	}
	if n := len(ofType(evs, "step/start")); n != 1 {
		t.Fatalf("%d steps, want 1", n)
	}
	if n := len(ofType(evs, "user/message")); n != 1 {
		t.Fatalf("%d user messages, want the input admitted once", n)
	}
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
	if a := asked(); len(a) != 2 || a[0] != 1 || a[1] != 2 {
		t.Fatalf("AttemptFailed asked about attempts %v, want [1 2]", a)
	}
	if n := len(r.sent.requests()); n != 3 {
		t.Fatalf("%d requests sent, want 3", n)
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-10: the attempt cap ends the turn with the last failure although
// the listener would retry again.
func TestAttemptCapEndsTheTurn(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	asked := retrying(t, root, backoff)
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	b := driver.Budget{MaxAttemptsPerStep: 2}
	r := newRig(t, rigOptions{root: root, svc: svc, clock: clk, budget: &b, turns: []fakemodel.Turn{
		fakemodel.Status(503), fakemodel.Status(503)}})
	r.send(agent.ListNextTurn, "go")
	waitBackoff(t, clk)
	r.idle()
	evs := r.events()
	if n := len(ofType(evs, "assistant/attempt")); n != 2 {
		t.Fatalf("%d assistant/attempt, want the cap of 2", n)
	}
	if n := len(ofType(evs, "step/start")); n != 1 {
		t.Fatalf("%d steps, want 1", n)
	}
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonError || got[0].Failure == nil {
		t.Fatalf("turn/end %+v, want error with the failure", got)
	}
	if a := asked(); len(a) != 2 {
		t.Fatalf("AttemptFailed asked %v", a)
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-10: the retry wait is cancellable: a turn cancel during it ends
// the step with the cause and the turn aborted, and no further attempt runs.
func TestCancelDuringRetryWait(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	retrying(t, root, time.Hour)
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	r := newRig(t, rigOptions{root: root, svc: svc, clock: clk, turns: []fakemodel.Turn{fakemodel.Status(503)}})
	r.send(agent.ListNextTurn, "go")
	if !clk.BlockUntil(1, 10*time.Second) {
		t.Fatal("the driver never waited to retry")
	}
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}))
	r.idle()
	evs := r.events()
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseUser) {
		t.Fatalf("turn/end %+v, want aborted{user}", got)
	}
	if se := stepEnds(t, evs); len(se) != 1 || se[0].Cause != string(agent.CauseUser) {
		t.Fatalf("step/end %+v", se)
	}
	if n := len(r.sent.requests()); n != 1 {
		t.Fatalf("%d requests sent, want no attempt after the cancel", n)
	}
	checkClosed(t, r.sess)
}

// A step cancel the turn survives keeps nothing: a next-turn item parked after
// it and before a later Turn/Keep cancel is input that cancel keeps, so it
// waits for a new wake instead of opening a turn at once.
func TestTurnCancelAfterSurvivedStepCancelKeepsNewlyParkedInput(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{{Hang: true}, {Hang: true}, fakemodel.Reply("parked"), fakemodel.Reply("wake")}})
	r.send(agent.ListNextTurn, "start")
	r.waitRequest(1)
	r.send(agent.ListNextStep, "steer")
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeStep, Inbox: agent.InboxKeep}))
	r.waitRequest(2)
	r.send(agent.ListNextTurn, "parked")
	must(0, r.drv.Cancel(agent.CauseParent, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}))
	r.idle()
	evs := r.events()
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseParent) {
		t.Fatalf("turn/end %+v: the kept item ran without a new wake", got)
	}
	if se := stepEnds(t, evs); len(se) != 2 || se[0].Cause != string(agent.CauseUser) || se[1].Cause != string(agent.CauseParent) {
		t.Fatalf("step/end %+v", se)
	}
	if n := len(r.inbox.Pending(agent.ListNextTurn)); n != 1 {
		t.Fatalf("%d next-turn items pending, want the kept one", n)
	}
	r.send(agent.ListNextTurn, "wake")
	r.idle()
	if got := turnEnds(t, r.events()); len(got) != 3 || got[1].Kind != event.ReasonCompleted || got[2].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
	checkClosed(t, r.sess)
}

// architecture.md F4: a cancel that lands while the driver is making a claim
// durable stops that turn at once. The claimed input is not lost: it is in the
// aborted turn's history and durable after point 16; parked input stays.
func TestCancelDuringClaimRecoveryAbortsTheTurn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		match func([]event.Draft) bool
		users int
	}{
		{"turn", func(ds []event.Draft) bool { return has(ds, "turn/start") }, 1},
		{"step", func(ds []event.Draft) bool {
			for _, d := range ds {
				if d.Type() == agent.SplicedType.Name() && strings.Contains(d.Raw(), `"`+agent.OutcomeClaimed+`"`) {
					return !has(ds, "turn/start")
				}
			}
			return false
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &faulty{}
			f.set(nil, once(tc.match, fail))
			clk := clock.NewManual(time.Unix(1_700_000_000, 0))
			r := newRig(t, rigOptions{wrap: f.wrap, clock: clk})
			// Steering parked while idle opens no turn; the turn's first
			// claim of next-step input takes it.
			if tc.name == "step" {
				r.send(agent.ListNextStep, "steer")
			}
			r.send(agent.ListNextTurn, "go")
			// The failed claim's flush parks the driver on its Sync backoff.
			if !clk.BlockUntil(1, 10*time.Second) {
				t.Fatal("the driver never waited to sync the claim")
			}
			if tc.name == "turn" {
				r.send(agent.ListNextStep, "parked steer")
			}
			must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}))
			r.idle()
			if err := r.drv.Err(); err != nil {
				t.Fatalf("the driver stopped: %v", err)
			}
			evs := r.events()
			if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseUser) {
				t.Fatalf("turn/end %+v, want aborted{user}", got)
			}
			if n := len(ofType(evs, "user/message")); n != tc.users {
				t.Fatalf("%d user messages, want %d claimed", n, tc.users)
			}
			if !r.inbox.Durable() {
				t.Fatal("the claim was left not durable")
			}
			if n := len(ofType(evs, "step/start")); n != 0 {
				t.Fatalf("%d steps ran in a turn cancelled at its claim", n)
			}
			if tc.name == "turn" {
				if n := len(r.inbox.Pending(agent.ListNextStep)); n != 1 {
					t.Fatalf("%d next-step items pending, want the parked steer kept", n)
				}
			}
			checkClosed(t, r.sess)
		})
	}
}

// architecture.md F4 "cancels the turn context; returns at once": a cancel
// that lands while the claim's recovery Sync is stalled inside the store's
// flush (holding the inbox's lock) returns at once and stops that Sync, with
// either inbox choice: a Discard's own inbox writes follow the signal. The
// turn ends aborted{user} long before CleanupTimeout.
func TestCancelDuringStalledRecoverySyncReturnsAtOnce(t *testing.T) {
	for _, choice := range []agent.InboxChoice{agent.InboxKeep, agent.InboxDiscard} {
		t.Run(string(choice), func(t *testing.T) {
			var (
				mu      sync.Mutex
				flushes int
			)
			entered := make(chan struct{})
			f := &faulty{}
			f.set(nil, func(ctx context.Context, ds []event.Draft) error {
				mu.Lock()
				claim := flushes == 0 && has(ds, "turn/start")
				retry := flushes == 1
				if claim || retry {
					flushes++
				}
				mu.Unlock()
				switch {
				case claim:
					return errFlaky
				case retry:
					close(entered)
					return stall(ctx)
				}
				return nil
			})
			long := func(o *driver.Options) { o.CleanupTimeout = time.Minute }
			r := newRig(t, rigOptions{wrap: f.wrap, options: long})
			r.send(agent.ListNextTurn, "go")
			within(t, entered, "the recovery Sync")
			returned := make(chan error, 1)
			start := time.Now()
			go func() {
				returned <- r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: choice})
			}()
			select {
			case err := <-returned:
				must(0, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Cancel waited behind a Sync stalled in the store")
			}
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := r.drv.WaitIdle(ctx); err != nil {
				t.Fatalf("the turn did not close promptly: %v", err)
			}
			if el := time.Since(start); el > 5*time.Second {
				t.Fatalf("the cancelled turn took %v to close", el)
			}
			evs := r.events()
			if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseUser) {
				t.Fatalf("turn/end %+v, want aborted{user}", got)
			}
			if n := len(ofType(evs, "step/start")); n != 0 {
				t.Fatalf("%d steps ran in a turn cancelled at its claim", n)
			}
			if !r.inbox.Durable() {
				t.Fatal("the claim was left not durable")
			}
			checkClosed(t, r.sess)
		})
	}
}
