package driver_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// The exits the property injects.
type exit int

const (
	exitNone exit = iota
	exitCancelInModel
	exitCancelInTool
	exitBudget
	exitProvider
	exitLease
	exits
)

func (e exit) String() string {
	return [...]string{"none", "cancel-in-model", "cancel-in-tool", "budget", "provider-error", "lease-loss"}[e]
}

// plan is one property case: a random script and one random exit at step at.
type plan struct {
	rng   *rand.Rand
	exit  exit
	at    int
	scope agent.CancelScope
	inbox agent.InboxChoice
	// park sends input to the inbox just before the cancel.
	park bool
	// leaseAfter is how many appends after the first send the lease survives.
	leaseAfter int
}

func newPlan(seed uint64) *plan {
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	p := &plan{rng: rng, exit: exit(rng.IntN(int(exits))), at: 1 + rng.IntN(4),
		scope: agent.ScopeTurn, inbox: agent.InboxKeep, park: rng.IntN(2) == 0, leaseAfter: 1 + rng.IntN(40)}
	if rng.IntN(2) == 0 {
		p.scope = agent.ScopeStep
	}
	if rng.IntN(2) == 0 {
		p.inbox = agent.InboxDiscard
	}
	return p
}

// lazyModel scripts each turn when it is requested, so no scripted turn is
// ever left over whatever exit the case takes.
type lazyModel struct {
	p      *plan
	tr     *fakemodel.Transport
	mu     sync.Mutex
	n      int
	calls  int
	cancel func() // the case's cancel, injected at the exit
}

func (m *lazyModel) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	m.n++
	n := m.n
	turn := m.next(n)
	m.mu.Unlock()
	if m.p.exit == exitCancelInModel && n == m.p.at {
		m.cancel()
	}
	m.tr.Script(turn)
	return m.tr.RoundTrip(r)
}

// next is request n's turn; the caller holds mu.
func (m *lazyModel) next(n int) fakemodel.Turn {
	p := m.p
	switch {
	case p.exit == exitProvider && n == p.at:
		if p.rng.IntN(2) == 0 {
			return fakemodel.Status(500)
		}
		return fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("half"), fakemodel.Error(vocab.CodeServer, "boom")}}
	case p.exit == exitCancelInModel && n == p.at && p.rng.IntN(2) == 0:
		return fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("so far")}, Hang: true}
	case p.exit == exitCancelInTool && n == p.at:
		return fakemodel.Calls(m.call("echo"), m.call("trip"), m.call("echo"))
	case p.exit != exitBudget && n > m.gate() && (n > 6 || p.rng.IntN(4) == 0):
		return fakemodel.Reply(fmt.Sprintf("answer %d", n))
	}
	var calls []fakemodel.Event
	for range 1 + p.rng.IntN(3) {
		calls = append(calls, m.call("echo"))
	}
	return fakemodel.Calls(calls...)
}

// gate is the last request that may not be a final answer: the exit's step
// must be reached.
func (m *lazyModel) gate() int {
	switch m.p.exit {
	case exitCancelInModel, exitCancelInTool, exitProvider:
		return m.p.at
	}
	return 0
}

func (m *lazyModel) call(name string) fakemodel.Event {
	m.calls++
	return fakemodel.Tool(fmt.Sprintf("call_%d", m.calls), name, `{"text":"x"}`)
}

// lossy loses the session's lease to another writer before append number
// left, counted once armed.
type lossy struct {
	session.Session
	mu   sync.Mutex
	left int // 0 is disarmed
	lose func()
}

func (l *lossy) tick() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.left == 0 {
		return
	}
	if l.left--; l.left == 0 {
		l.lose()
	}
}

func (l *lossy) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	l.tick()
	return l.Session.Append(ctx, d)
}

func (l *lossy) AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) {
	l.tick()
	return l.Session.AppendGroup(ctx, ds)
}

// trip is the current case's in-tool cancel; cases run one at a time.
var trip atomic.Pointer[func()]

// TC-11a, DH-S-11: whatever exit a turn takes (a turn or step cancel in the
// model call or in a tool body, the step budget, a provider failure, a lost
// lease), every tool call in the history has a result and every bracket closes
// (after the next write-open's closers when the driver itself was cut off).
func TestNoToolCallLeftUnansweredOnAnyExit(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	must(svc.Register(root, tools.Define(tools.Spec[textIn, string]{
		Name: "trip", Description: "Cancels the case and waits for its cancel.",
		Run: func(ctx context.Context, in textIn, _ tools.Run) (string, error) {
			if f := trip.Load(); f != nil {
				(*f)()
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(10 * time.Second):
				return in.Text, nil
			}
		},
		Overlap: func(textIn) tools.Overlap { return tools.Free },
	})))
	cases := 500
	if testing.Short() {
		cases = 100
	}
	seen := map[exit]int{}
	for i := range cases {
		p := newPlan(uint64(i))
		seen[p.exit]++
		t.Run(fmt.Sprintf("%03d-%s", i, p.exit), func(t *testing.T) { runCase(t, root, svc, i, p) })
		if t.Failed() {
			t.Fatalf("case %d (%+v) failed; rerun with -run 'TestNoToolCallLeftUnansweredOnAnyExit/%03d'", i, *p, i)
		}
	}
	for e := exitNone; e < exits; e++ {
		if seen[e] == 0 {
			t.Fatalf("no case exited %s: %v", e, seen)
		}
	}
	if leaseCut.Load() == 0 {
		t.Fatal("no lease-loss case cut the driver off")
	}
}

func runCase(t *testing.T, root *cordis.Context, svc tools.Service, i int, p *plan) {
	id := fmt.Sprintf("p-%03d", i)
	st := memstore.New()
	model := &lazyModel{p: p, tr: fakemodel.NewTransport(t)}
	var lost atomic.Bool
	var wrapped *lossy
	o := rigOptions{root: root, svc: svc, id: id, store: st, model: model, failOK: true}
	if p.exit == exitBudget {
		o.budget = &driver.Budget{MaxSteps: p.at}
	}
	if p.exit == exitLease {
		o.wrap = func(s session.Session) session.Session {
			wrapped = &lossy{Session: s, lose: func() {
				w, err := st.OpenWrite(bg, id, store.OpenOptions{Takeover: store.Force})
				if err != nil {
					t.Errorf("take the lease: %v", err)
					return
				}
				_ = w
				lost.Store(true)
			}}
			return wrapped
		}
	}
	r := newRig(t, o)
	cancel := func() {
		if p.park {
			r.send(agent.ListNextStep, "parked step input")
			r.send(agent.ListNextTurn, "parked turn input")
		}
		if err := r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: p.scope, Inbox: p.inbox}); err != nil {
			t.Errorf("cancel: %v", err)
		}
	}
	model.cancel = cancel
	if p.exit == exitCancelInTool {
		trip.Store(&cancel)
		t.Cleanup(func() { trip.Store(nil) })
	}
	r.send(agent.ListNextTurn, "go")
	if wrapped != nil {
		wrapped.mu.Lock()
		wrapped.left = p.leaseAfter
		wrapped.mu.Unlock()
	}

	ctx, stop := context.WithTimeout(bg, 20*time.Second)
	defer stop()
	err := r.drv.WaitIdle(ctx)
	switch {
	case lost.Load():
		if err == nil {
			// The lease went after the last append; the next one would fail.
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("the driver hung after losing its lease: %v", err)
		}
		// The driver stopped; the next write-open closes what it left open.
		leaseCut.Add(1)
		h, rep, err := session.NewService(root, st, session.Config{}).Open(bg, nil, session.ID(id), session.OpenOptions{Takeover: store.Force})
		if err != nil {
			t.Fatalf("reopen after the lease loss: %v", err)
		}
		defer func() { _ = h.Close(bg) }()
		_ = rep
		checkClosed(t, h)
		return
	case err != nil:
		t.Fatalf("wait idle: %v", err)
	}
	checkClosed(t, r.sess)
	evs := r.events()
	ends := turnEnds(t, evs)
	switch p.exit {
	case exitBudget:
		if len(ends) != 1 || ends[0].Kind != event.ReasonBudget || ends[0].Value != p.at {
			t.Fatalf("budget case ended %+v", ends)
		}
	case exitProvider:
		if len(ends) != 1 || ends[0].Kind != event.ReasonError || ends[0].Failure == nil {
			t.Fatalf("provider case ended %+v", ends)
		}
	case exitCancelInModel, exitCancelInTool:
		cancelled := false
		for _, se := range stepEnds(t, evs) {
			cancelled = cancelled || (se.Step == p.at && se.Cause == string(agent.CauseUser))
		}
		if !cancelled {
			t.Fatalf("step %d did not close with cause user: %+v", p.at, stepEnds(t, evs))
		}
		if !(p.scope == agent.ScopeStep && p.park) && (len(ends) == 0 || ends[0].Kind != event.ReasonAborted || ends[0].Cause != string(agent.CauseUser)) {
			t.Fatalf("cancel case ended %+v", ends)
		}
	}
}

// leaseCut counts the cases whose driver was cut off by a lost lease.
var leaseCut atomic.Int32
