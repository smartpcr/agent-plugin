package human_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// carrier is a recording answerer.
type carrier struct {
	mu        sync.Mutex
	delivered []human.Item
	withdrawn map[human.ID]human.Reason
	fail      error
	got       chan human.Item
	ctxs      map[human.ID]context.Context // the context each Deliver was given
}

func newCarrier() *carrier {
	return &carrier{withdrawn: map[human.ID]human.Reason{}, got: make(chan human.Item, 16), ctxs: map[human.ID]context.Context{}}
}

func (c *carrier) Deliver(ctx context.Context, it human.Item) error {
	if c.fail != nil {
		return c.fail
	}
	c.mu.Lock()
	c.ctxs[it.ID] = ctx
	c.delivered = append(c.delivered, it)
	c.mu.Unlock()
	c.got <- it
	return nil
}

func (c *carrier) Withdraw(id human.ID, why human.Reason) {
	c.mu.Lock()
	c.withdrawn[id] = why
	c.mu.Unlock()
}

func (c *carrier) deliveries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.delivered)
}

type env struct {
	t    *testing.T
	root *cordis.Context
	reg  human.Registry
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := cordis.New()
	t.Cleanup(func() { root.Dispose() })
	return &env{t: t, root: root, reg: human.New()}
}

// fiber is a child fiber of root that the test can unload, and its context.
func (e *env) fiber(name string) (*cordis.Fiber, *cordis.Context) {
	e.t.Helper()
	var ctx *cordis.Context
	f, err := e.root.Apply(name, func(c *cordis.Context) error { ctx = c; return nil })
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		e.t.Fatalf("fiber %s: %v", name, err)
	}
	return f, ctx
}

func ref(id string) agent.Ref { return agent.Ref{Session: session.ID(id), Incarnation: 1} }

func (e *env) answerer(ch human.Channel) (*carrier, *cordis.EffectHandle) {
	e.t.Helper()
	c := newCarrier()
	h, err := e.reg.RegisterAnswerer(e.root, ch, c)
	if err != nil {
		e.t.Fatalf("RegisterAnswerer(%s): %v", ch, err)
	}
	return c, h
}

func (e *env) bind(r agent.Ref, b human.Binding) *cordis.EffectHandle {
	e.t.Helper()
	h, err := e.reg.Bind(e.root, r, b)
	if err != nil {
		e.t.Fatalf("Bind(%s): %v", r, err)
	}
	return h
}

type asked struct {
	ans human.Answer
	by  human.Actor
	err error
}

func (e *env) ask(ctx context.Context, it human.Item) <-chan asked {
	out := make(chan asked, 1)
	go func() {
		a, by, err := e.reg.Ask(ctx, it)
		out <- asked{a, by, err}
	}()
	return out
}

func wait(t *testing.T, ch <-chan asked) asked {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("Ask did not return")
		return asked{}
	}
}

func delivered(t *testing.T, c *carrier) human.Item {
	t.Helper()
	select {
	case it := <-c.got:
		return it
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was delivered")
		return human.Item{}
	}
}

// Scenario: Pending interaction is addressable by agent (DH-D-HUMAN-1).
func TestPendingIsAddressableByAgent(t *testing.T) {
	e := newEnv(t)
	ui, _ := e.answerer("ui")
	a, b := ref("ses-a"), ref("ses-b")
	e.bind(a, human.Binding{Channel: "ui"})
	e.bind(b, human.Binding{Channel: "ui"})

	askA := e.ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: a}, Payload: "rm -rf build"})
	delivered(t, ui)
	askB := e.ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: b}, Payload: "git push"})
	delivered(t, ui)

	got := e.reg.Pending(a)
	if len(got) != 1 || got[0].To.Agent != a || got[0].Payload != "rm -rf build" {
		t.Fatalf("Pending(a) = %+v, want only A's approval", got)
	}
	if all := e.reg.PendingAll(); len(all) != 2 || all[0].ID != got[0].ID {
		t.Fatalf("PendingAll = %+v, want A's then B's", all)
	}

	by := human.Actor{Channel: "ui", Name: "alice"}
	if err := e.reg.Answer(context.Background(), by, got[0].ID, human.Answer{Outcome: human.OutcomeAllowedOnce}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	res := wait(t, askA)
	if res.err != nil || res.ans.Outcome != human.OutcomeAllowedOnce || res.by != by {
		t.Fatalf("A's ask = %+v, want allowed-once by alice", res)
	}
	if p := e.reg.Pending(a); len(p) != 0 {
		t.Fatalf("Pending(a) after answer = %+v", p)
	}
	if p := e.reg.Pending(b); len(p) != 1 {
		t.Fatalf("Pending(b) = %+v, want B's approval untouched", p)
	}
	select {
	case r := <-askB:
		t.Fatalf("B's ask settled by A's answer: %+v", r)
	default:
	}
	e.reg.Close()
	if r := wait(t, askB); r.ans.Outcome != human.OutcomeUnavailable || r.ans.Detail != "host-stopping" {
		t.Fatalf("B after Close = %+v", r)
	}
}

// DH-T-HUMAN-1: two concurrent answers, one wins.
//
//dsh:test DH-T-HUMAN-1
func TestConcurrentAnswersOneWins(t *testing.T) {
	e := newEnv(t)
	ui, _ := e.answerer("ui")
	a := ref("ses-a")
	e.bind(a, human.Binding{Channel: "ui"})
	for range 50 {
		res := e.ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: a}})
		it := delivered(t, ui)
		var wins, settledErrs atomic.Int32
		var wg sync.WaitGroup
		t.Cleanup(wg.Wait) // P-C: nothing gates the answers; the cleanup joins them on every exit path
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out := human.OutcomeDenied
				if i%2 == 0 {
					out = human.OutcomeAllowedOnce
				}
				err := e.reg.Answer(context.Background(), human.Actor{Channel: "ui"}, it.ID, human.Answer{Outcome: out})
				switch {
				case err == nil:
					wins.Add(1)
				case errors.Is(err, human.ErrSettled):
					settledErrs.Add(1)
				default:
					t.Errorf("Answer: %v", err)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 || settledErrs.Load() != 7 {
			t.Fatalf("wins=%d settled=%d, want 1 and 7", wins.Load(), settledErrs.Load())
		}
		wait(t, res)
	}
}

//dsh:test DH-T-HUMAN-2
func TestCancelUnbindUnloadAndRefusals(t *testing.T) {
	e := newEnv(t)
	ui, _ := e.answerer("ui")
	sdk, _ := e.answerer("sdk")
	a, child, lone := ref("ses-a"), ref("ses-child"), ref("ses-lone")
	bindA := e.bind(a, human.Binding{Channel: "sdk"})
	e.bind(child, human.Binding{Via: a})

	// Unbound: unavailable at once, no delivery.
	r, _, err := e.reg.Ask(context.Background(), human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: lone}})
	if err != nil || r.Outcome != human.OutcomeUnavailable || r.Detail != "unbound" {
		t.Fatalf("unbound ask = %+v, %v", r, err)
	}

	// Cancel: the ask settles cancelled; a late answer changes nothing.
	ctx, cancel := context.WithCancel(context.Background())
	res := e.ask(ctx, human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: a}})
	it := delivered(t, sdk)
	cancel()
	if got := wait(t, res); got.ans.Outcome != human.OutcomeCancelled {
		t.Fatalf("cancelled ask = %+v", got)
	}
	if err := e.reg.Answer(context.Background(), human.Actor{Channel: "sdk"}, it.ID, human.Answer{Outcome: human.OutcomeAnswered, Text: "late"}); !errors.Is(err, human.ErrSettled) {
		t.Fatalf("late answer = %v, want interaction/settled", err)
	}
	if err := e.reg.Answer(context.Background(), human.Actor{Channel: "sdk"}, "nope", human.Answer{Outcome: human.OutcomeDenied}); !errors.Is(err, human.ErrUnknown) {
		t.Fatalf("unknown answer = %v", err)
	}

	// A child's item goes to its root's channel; another channel cannot settle it.
	res = e.ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: child}})
	it = delivered(t, sdk)
	if ui.deliveries() != 0 {
		t.Fatal("the ui answerer saw the sdk agent's item")
	}
	if err := e.reg.Answer(context.Background(), human.Actor{Channel: "ui"}, it.ID, human.Answer{Outcome: human.OutcomeAllowedOnce}); !errors.Is(err, human.ErrNotYours) {
		t.Fatalf("ui answer = %v, want interaction/not-yours", err)
	}
	if err := e.reg.Answer(context.Background(), human.Actor{Channel: "sdk"}, it.ID, human.Answer{Outcome: human.OutcomeAnswered}); !errors.Is(err, human.ErrInvalid) {
		t.Fatalf("answered on an approval = %v, want interaction/invalid", err)
	}

	// Unbinding the root settles the child's item unavailable and withdraws it.
	bindA.Dispose()
	if got := wait(t, res); got.ans.Outcome != human.OutcomeUnavailable || got.ans.Detail != "unbound" {
		t.Fatalf("after unbind = %+v", got)
	}
	sdk.mu.Lock()
	why := sdk.withdrawn[it.ID]
	sdk.mu.Unlock()
	if why != human.Reason(human.OutcomeUnavailable) {
		t.Fatalf("withdrawn = %q", why)
	}
	if r, _, _ := e.reg.Ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: child}}); r.Outcome != human.OutcomeUnavailable {
		t.Fatalf("child with its root unbound = %+v", r)
	}

	// One binding per agent, one answerer per channel.
	if _, err := e.reg.Bind(e.root, child, human.Binding{Channel: "ui"}); !errors.Is(err, human.ErrTaken) {
		t.Fatalf("second Bind = %v", err)
	}
	if _, err := e.reg.RegisterAnswerer(e.root, "ui", newCarrier()); !errors.Is(err, human.ErrTaken) {
		t.Fatalf("second answerer = %v", err)
	}
}

func TestUnloadingTheCarrierSettlesItsItems(t *testing.T) {
	e := newEnv(t)
	f, fc := e.fiber("carrier")
	c := newCarrier()
	if _, err := e.reg.RegisterAnswerer(fc, "cli", c); err != nil {
		t.Fatal(err)
	}
	a := ref("ses-a")
	e.bind(a, human.Binding{Channel: "cli"})
	res := e.ask(context.Background(), human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: a}})
	delivered(t, c)
	f.Dispose()
	if got := wait(t, res); got.ans.Outcome != human.OutcomeUnavailable || got.ans.Detail != "answerer-unloaded" {
		t.Fatalf("after unload = %+v", got)
	}
	if r, _, _ := e.reg.Ask(context.Background(), human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: a}}); r.Detail != "no-answerer" {
		t.Fatalf("ask with no answerer = %+v", r)
	}
}

func TestFailingAnswererIsUnavailable(t *testing.T) {
	e := newEnv(t)
	c, _ := e.answerer("ui")
	c.fail = errors.New("socket closed")
	a := ref("ses-a")
	e.bind(a, human.Binding{Channel: "ui"})
	r, _, err := e.reg.Ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: a}})
	if err != nil || r.Outcome != human.OutcomeUnavailable {
		t.Fatalf("failing answerer = %+v, %v", r, err)
	}
	if p := e.reg.PendingAll(); len(p) != 0 {
		t.Fatalf("pending after failure = %+v", p)
	}
}

// A minted id never collides with a caller-supplied one: an explicit "int-2"
// stays pending and distinct when a later ask mints its id.
func TestMintedIDsSkipCallerSuppliedOnes(t *testing.T) {
	e := newEnv(t)
	ui, _ := e.answerer("ui")
	a := ref("ses-a")
	e.bind(a, human.Binding{Channel: "ui"})

	first := e.ask(context.Background(), human.Item{ID: "int-2", Kind: human.KindApproval, To: human.Audience{Agent: a}, Payload: "explicit"})
	delivered(t, ui)
	for range 3 {
		e.ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: a}, Payload: "minted"})
		delivered(t, ui)
	}
	p := e.reg.Pending(a)
	ids := map[human.ID]bool{}
	for _, it := range p {
		ids[it.ID] = true
	}
	if len(p) != 4 || len(ids) != 4 || !ids["int-2"] {
		t.Fatalf("Pending = %+v, want four distinct items including int-2", p)
	}
	if err := e.reg.Answer(context.Background(), human.Actor{Channel: "ui"}, "int-2", human.Answer{Outcome: human.OutcomeDenied}); err != nil {
		t.Fatal(err)
	}
	if r := wait(t, first); r.ans.Outcome != human.OutcomeDenied {
		t.Fatalf("the explicit ask = %+v", r)
	}
	if p := e.reg.Pending(a); len(p) != 3 {
		t.Fatalf("answering int-2 settled %d items", 4-len(p))
	}
	for _, it := range e.reg.Pending(a) {
		if it.Payload != "minted" {
			t.Fatalf("a minted item was replaced: %+v", it)
		}
	}
	// A settled explicit id is not reminted either.
	e.ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: a}})
	if it := delivered(t, ui); it.ID == "int-2" {
		t.Fatal("a settled id was reminted")
	}
	e.reg.Close()
}

// The context Deliver is given is cancelled when the item settles, by answer,
// unbind, answerer unload or Close, even though the asker's context never is.
func TestDeliveryContextEndsWhenTheItemSettles(t *testing.T) {
	e := newEnv(t)
	f, fc := e.fiber("carrier")
	cli := newCarrier()
	if _, err := e.reg.RegisterAnswerer(fc, "cli", cli); err != nil {
		t.Fatal(err)
	}
	ui, _ := e.answerer("ui")
	a, b, c := ref("ses-a"), ref("ses-b"), ref("ses-c")
	e.bind(a, human.Binding{Channel: "ui"})
	bindB := e.bind(b, human.Binding{Channel: "ui"})
	e.bind(c, human.Binding{Channel: "cli"})
	bg := context.Background()
	done := func(cr *carrier, id human.ID) bool {
		cr.mu.Lock()
		ctx := cr.ctxs[id]
		cr.mu.Unlock()
		select {
		case <-ctx.Done():
			return true
		default:
			return false
		}
	}
	askQ := func(cr *carrier, r agent.Ref) (<-chan asked, human.ID) {
		res := e.ask(bg, human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: r}})
		it := delivered(t, cr)
		if done(cr, it.ID) {
			t.Fatal("the delivery context ended while the item was pending")
		}
		return res, it.ID
	}

	res, id := askQ(ui, a)
	if err := e.reg.Answer(bg, human.Actor{Channel: "ui"}, id, human.Answer{Outcome: human.OutcomeAnswered, Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	wait(t, res)
	if !done(ui, id) {
		t.Fatal("answer: the delivery context is still live")
	}

	res, id = askQ(ui, b)
	bindB.Dispose()
	wait(t, res)
	if !done(ui, id) {
		t.Fatal("unbind: the delivery context is still live")
	}

	res, id = askQ(cli, c)
	f.Dispose()
	wait(t, res)
	if !done(cli, id) {
		t.Fatal("unload: the delivery context is still live")
	}

	res, id = askQ(ui, a)
	e.reg.Close()
	wait(t, res)
	if !done(ui, id) {
		t.Fatal("Close: the delivery context is still live")
	}
}
