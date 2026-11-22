package driver_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// errFlaky is a transient storage failure.
var errFlaky = errors.New("storage hiccup")

// faulty is a session whose writes and flushes a test can fail or stall.
// write runs before each append with its drafts; flush runs before each
// flush with the drafts of the last append. A non-nil error fails the call
// without reaching the session.
type faulty struct {
	session.Session
	mu    sync.Mutex
	last  []event.Draft
	write func(ctx context.Context, ds []event.Draft) error
	flush func(ctx context.Context, last []event.Draft) error
}

func (f *faulty) wrap(s session.Session) session.Session { f.Session = s; return f }

func (f *faulty) set(write func(context.Context, []event.Draft) error, flush func(context.Context, []event.Draft) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.write, f.flush = write, flush
}

func (f *faulty) before(ctx context.Context, ds []event.Draft) error {
	f.mu.Lock()
	f.last = ds
	fn := f.write
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, ds)
	}
	return nil
}

func (f *faulty) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if err := f.before(ctx, []event.Draft{d}); err != nil {
		return event.Receipt{}, err
	}
	return f.Session.Append(ctx, d)
}

func (f *faulty) AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) {
	if err := f.before(ctx, ds); err != nil {
		return nil, err
	}
	return f.Session.AppendGroup(ctx, ds)
}

func (f *faulty) Flush(ctx context.Context) (event.Offset, error) {
	f.mu.Lock()
	fn, last := f.flush, f.last
	f.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, last); err != nil {
			return f.Session.Durable(), err
		}
	}
	return f.Session.Flush(ctx)
}

func has(ds []event.Draft, typ string) bool {
	for _, d := range ds {
		if d.Type() == typ {
			return true
		}
	}
	return false
}

// once is a hook that fires fn the first time match holds, and passes after.
func once(match func([]event.Draft) bool, fn func(context.Context) error) func(context.Context, []event.Draft) error {
	var mu sync.Mutex
	fired := false
	return func(ctx context.Context, ds []event.Draft) error {
		mu.Lock()
		if fired || !match(ds) {
			mu.Unlock()
			return nil
		}
		fired = true
		mu.Unlock()
		return fn(ctx)
	}
}

func fail(context.Context) error { return errFlaky }

// stall blocks until its context ends, as a store waiting on a dead disk does.
func stall(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// architecture.md F4: a waking Send that arrives while a cancel's cleanup is
// still writing is queued as a new turn; only the input parked when the cancel
// landed waits for a new wake. The cleanup is held at its first write, so the
// Send lands inside it on every run.
func TestSendDuringCancelCleanupOpensANewTurn(t *testing.T) {
	f := &faulty{}
	r := newRig(t, rigOptions{wrap: f.wrap, turns: []fakemodel.Turn{{Hang: true}, fakemodel.Reply("kept"), fakemodel.Reply("later")}})
	r.send(agent.ListNextTurn, "first")
	r.waitRequest(1)
	r.send(agent.ListNextTurn, "kept")
	reached, release := make(chan struct{}), make(chan struct{})
	f.set(once(func([]event.Draft) bool { return true }, func(context.Context) error {
		close(reached)
		<-release
		return nil
	}), nil)
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}))
	within(t, reached, "the cancel's cleanup write")
	r.send(agent.ListNextTurn, "later")
	close(release)
	r.idle()
	got := turnEnds(t, r.events())
	if len(got) != 3 || got[0].Kind != event.ReasonAborted || got[1].Kind != event.ReasonCompleted || got[2].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v: the send during the cleanup did not wake a turn", got)
	}
	if n := len(r.inbox.Pending(agent.ListNextTurn)); n != 0 {
		t.Fatalf("%d next-turn items left pending", n)
	}
	checkClosed(t, r.sess)
}

// inbox.go ClaimTurn/ClaimStep: a claim whose append succeeded but whose flush
// failed has taken its items; the driver makes it durable through Sync and
// runs them, once, instead of stopping and losing them.
func TestClaimWhoseFlushFailsStillRuns(t *testing.T) {
	claim := func(ds []event.Draft) bool {
		for _, d := range ds {
			if d.Type() == agent.SplicedType.Name() && strings.Contains(d.Raw(), `"`+agent.OutcomeClaimed+`"`) {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name  string
		match func([]event.Draft) bool
	}{
		{"turn", func(ds []event.Draft) bool { return has(ds, "turn/start") }},
		{"step", func(ds []event.Draft) bool { return claim(ds) && !has(ds, "turn/start") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &faulty{}
			var mu sync.Mutex
			failed := 0
			f.set(nil, once(tc.match, func(context.Context) error {
				mu.Lock()
				failed++
				mu.Unlock()
				return errFlaky
			}))
			r := newRig(t, rigOptions{wrap: f.wrap, turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("done")}})
			r.send(agent.ListNextTurn, "go")
			r.send(agent.ListNextStep, "steer")
			r.idle()
			mu.Lock()
			defer mu.Unlock()
			if failed != 1 {
				t.Fatalf("the claim's flush failed %d times, want 1", failed)
			}
			if err := r.drv.Err(); err != nil {
				t.Fatalf("the driver stopped: %v", err)
			}
			evs := r.events()
			if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
				t.Fatalf("turn/end %+v", got)
			}
			if n := len(ofType(evs, "user/message")); n != 2 {
				t.Fatalf("%d user messages, want both claimed items once", n)
			}
			if len(r.inbox.Pending(agent.ListNextTurn))+len(r.inbox.Pending(agent.ListNextStep)) != 0 || !r.inbox.Durable() {
				t.Fatal("the claim was left pending or not durable")
			}
			checkClosed(t, r.sess)
		})
	}
}

// DH-T-AGENT-9: a log write that stops the tool batch is not swallowed: point
// 13 answers the call the batch left, step/end follows, and the turn ends error
// by driver/tools; the next turn's request is paired.
func TestToolBatchLogFailureEndsTheTurn(t *testing.T) {
	f := &faulty{}
	f.set(once(func(ds []event.Draft) bool { return has(ds, "tool/result") }, fail), nil)
	r := newRig(t, rigOptions{wrap: f.wrap, turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("after")}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonError || got[0].By != "driver/tools" || !strings.Contains(got[0].Detail, errFlaky.Error()) {
		t.Fatalf("turn/end %+v, want error by driver/tools", got)
	}
	var order []string
	for _, e := range evs {
		switch e.Type() {
		case "tool/call", "tool/result", "step/end":
			order = append(order, e.Type())
		}
	}
	if strings.Join(order, ",") != "tool/call,tool/result,step/end" {
		t.Fatalf("events %v: the closer must answer the call before step/end", order)
	}
	checkClosed(t, r.sess)
	r.send(agent.ListNextTurn, "again")
	r.idle()
	if got := turnEnds(t, r.events()); len(got) != 2 || got[1].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
	checkClosed(t, r.sess)
}

// architecture.md F4, tech-spec.md §5.5: the cleanup writes and the final
// flush run on a bounded context that is not the cancelled one, so a store
// that waits on its context cannot keep the driver goroutine.
func TestStalledCleanupIsBounded(t *testing.T) {
	short := func(o *driver.Options) {
		o.CleanupTimeout, o.FinalFlushTimeout = 200*time.Millisecond, 200*time.Millisecond
	}
	t.Run("final flush", func(t *testing.T) {
		f := &faulty{}
		f.set(nil, once(func(ds []event.Draft) bool { return has(ds, "turn/end") }, stall))
		r := newRig(t, rigOptions{wrap: f.wrap, failOK: true, options: short, turns: []fakemodel.Turn{fakemodel.Reply("x")}})
		r.send(agent.ListNextTurn, "go")
		within(t, r.drv.Done(), "the driver's exit after a stalled flush")
		if err := r.drv.Err(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("driver error %v, want the flush's deadline", err)
		}
	})
	t.Run("cancel closers", func(t *testing.T) {
		f := &faulty{}
		f.set(once(func(ds []event.Draft) bool { return has(ds, "step/end") }, stall), nil)
		r := newRig(t, rigOptions{wrap: f.wrap, failOK: true, options: short, turns: []fakemodel.Turn{{Hang: true}}})
		r.send(agent.ListNextTurn, "go")
		r.waitRequest(1)
		must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}))
		within(t, r.drv.Done(), "the driver's exit after a stalled closer")
		if err := r.drv.Err(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("driver error %v, want the closer's deadline", err)
		}
	})
	if _, err := driver.Start(driver.Options{CleanupTimeout: -1}); err == nil {
		t.Fatal("Start accepted options without the required parts")
	}
}

// DH-T-AGENT-12: Enter.StartsSeries reaches the request header: the step's
// request opens with a request/header although route, tools and controls are
// unchanged.
func TestStartsSeriesReachesTheHeader(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	must(agent.PreStep.On(root, func(_ *cordis.Context, req agent.PreStepRequest, next func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		if req.Step == 2 {
			return agent.Enter{Messages: req.Messages, StartsSeries: true}, nil
		}
		return next(req)
	}))
	r := newRig(t, rigOptions{root: root, svc: svc, turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	var seq []string
	for _, e := range evs {
		switch e.Type() {
		case "request/header":
			seq = append(seq, "header")
		case "step/start":
			seq = append(seq, "step")
		}
	}
	if strings.Join(seq, ",") != "step,header,step,header" {
		t.Fatalf("headers and steps %v: step 2 started no series", seq)
	}
	checkClosed(t, r.sess)
}
