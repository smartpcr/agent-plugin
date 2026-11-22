//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

const bccRoute vocab.RouteID = "r/bcc"

// bccWait bounds every real-time wait on the driver.
const bccWait = 20 * time.Second

// bccOuterT is the test running the suite; scenario TBs delegate to it.
var bccOuterT *testing.T

// bccTB is a testing.TB scoped to one scenario (or one property case): the
// test kit's cleanups run when it is disposed and its errors fail the
// scenario instead of the whole suite.
type bccTB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
	errs     []string
}

func newBccTB() *bccTB { return &bccTB{TB: bccOuterT} }

func (t *bccTB) Helper() {}

func (t *bccTB) Cleanup(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, f)
}

func (t *bccTB) Errorf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errs = append(t.errs, fmt.Sprintf(format, args...))
}

func (t *bccTB) Error(args ...any) { t.Errorf("%s", fmt.Sprint(args...)) }

func (t *bccTB) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	panic(fmt.Sprintf(format, args...))
}

func (t *bccTB) Fatal(args ...any) { t.Fatalf("%s", fmt.Sprint(args...)) }

func (t *bccTB) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.errs) > 0
}

// dispose runs the cleanups last first and reports the errors recorded.
func (t *bccTB) dispose() error {
	t.mu.Lock()
	cs := t.cleanups
	t.cleanups = nil
	t.mu.Unlock()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i]()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.errs) > 0 {
		return errors.New(strings.Join(t.errs, "; "))
	}
	return nil
}

type bccTextIn struct {
	Text string `json:"text" dsh:"required"`
}

// bccStubAgent is the agent the driver runs for; the driver reads its Ref.
type bccStubAgent struct {
	agent.Agent
	ref agent.Ref
}

func (a bccStubAgent) Ref() agent.Ref               { return a.ref }
func (a bccStubAgent) ScopeKey() scope.Key          { return scope.Key{} }
func (a bccStubAgent) Scope() *cordis.Context       { return nil }
func (a bccStubAgent) Session() session.ReadSession { return nil }

// bccCounting reports each model request number once the script answered it.
type bccCounting struct {
	next http.RoundTripper
	mu   sync.Mutex
	n    int
	ch   chan int
}

func (c *bccCounting) RoundTrip(r *http.Request) (*http.Response, error) {
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

// bccTrip is the current property case's in-tool cancel; cases run serially.
var bccTrip atomic.Pointer[func()]

// bccRig is one driver on the scripted model, memstore and real tools.
type bccRig struct {
	tb       *bccTB
	store    *memstore.Store
	sess     session.Handle
	inbox    *agent.Inbox
	tr       *fakemodel.Transport
	drv      *driver.Driver
	requests chan int
}

type bccRigOptions struct {
	id     string
	store  *memstore.Store
	turns  []fakemodel.Turn
	model  http.RoundTripper
	budget *driver.Budget
	hooks  driver.Hooks
	wrap   func(session.Session) session.Session
	failOK bool
}

type bccState struct {
	tb   *bccTB
	root *cordis.Context
	svc  tools.Service

	// Property.
	exits    []string
	cases    int
	seen     map[bccExit]int
	leaseCut int
	failures []string

	// Cancel step.
	run1, run2    *bccRig
	points1       [][2]int
	points2       [][2]int
	pointsMu      sync.Mutex
	parkedText    string
	run2CancelErr error

	// Budget.
	budget *driver.Budget
	turns  []fakemodel.Turn
	brig   *bccRig
}

func (s *bccState) boot() error {
	if s.root != nil {
		return nil
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.tb.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		f, err := root.Use(c, cordis.None{})
		if err != nil {
			return err
		}
		if err := f.Wait(); err != nil {
			return err
		}
	}
	g, err := root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		s.svc, err = tools.Key.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return err
	}
	echo := tools.Define(tools.Spec[bccTextIn, string]{
		Name: "echo", Description: "Echoes its text.",
		Run:     func(_ context.Context, in bccTextIn, _ tools.Run) (string, error) { return in.Text, nil },
		Overlap: func(bccTextIn) tools.Overlap { return tools.Free },
	})
	trip := tools.Define(tools.Spec[bccTextIn, string]{
		Name: "trip", Description: "Cancels the case and waits for its cancel.",
		Run: func(ctx context.Context, in bccTextIn, _ tools.Run) (string, error) {
			if f := bccTrip.Load(); f != nil {
				(*f)()
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(10 * time.Second):
				return in.Text, nil
			}
		},
		Overlap: func(bccTextIn) tools.Overlap { return tools.Free },
	})
	for _, d := range []*tools.Declaration{echo, trip} {
		if _, err := s.svc.Register(root, d); err != nil {
			return err
		}
	}
	s.root = root
	return nil
}

// newRig starts a driver whose test-kit cleanups belong to tb.
func (s *bccState) newRig(tb *bccTB, o bccRigOptions) (*bccRig, error) {
	ctx := context.Background()
	st := o.store
	if st == nil {
		st = memstore.New()
	}
	sess, err := session.NewService(s.root, st, session.Config{}).Create(ctx, nil,
		session.CreateOptions{Header: session.Header{ID: o.id}})
	if err != nil {
		return nil, fmt.Errorf("create the session: %w", err)
	}
	tb.Cleanup(func() { _ = sess.Close(context.Background()) })
	var live session.Session = sess
	if o.wrap != nil {
		live = o.wrap(sess)
	}
	r := &bccRig{tb: tb, store: st, sess: sess, requests: make(chan int, 64)}
	model := o.model
	if model == nil {
		r.tr = fakemodel.NewTransport(tb, o.turns...)
		model = r.tr
	}
	rt := llm.New()
	fakemodel.Register(tb, rt, bccRoute, &bccCounting{next: model, ch: r.requests})
	if r.inbox, err = agent.OpenInbox(ctx, live); err != nil {
		return nil, err
	}
	mod, err := agent.OpenModel(ctx, live, agent.ModelOptions{Initial: agent.ModelSelection{Route: bccRoute}})
	if err != nil {
		return nil, err
	}
	r.drv, err = driver.Start(driver.Options{
		Context: s.root, Agent: bccStubAgent{ref: agent.Ref{Session: session.ID(o.id), Incarnation: 1}},
		Session: live, Inbox: r.inbox, Model: mod, Models: rt, Tools: s.svc,
		Budget: o.budget, Hooks: o.hooks,
	})
	if err != nil {
		return nil, fmt.Errorf("start the driver: %w", err)
	}
	failOK := o.failOK
	tb.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.drv.Close(c); err != nil && !failOK {
			tb.Errorf("close the driver: %v", err)
		}
	})
	return r, nil
}

func (r *bccRig) send(list agent.List, text string) error {
	_, err := r.inbox.Send(context.Background(), list, agent.InboxMessage{Source: agent.SourceUser, Text: text})
	return err
}

func (r *bccRig) waitRequest(n int) error {
	deadline := time.After(bccWait)
	for {
		select {
		case got := <-r.requests:
			if got >= n {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("model request %d was never sent", n)
		}
	}
}

func (r *bccRig) idle() error {
	c, cancel := context.WithTimeout(context.Background(), bccWait)
	defer cancel()
	if err := r.drv.WaitIdle(c); err != nil {
		return fmt.Errorf("wait idle: %w", err)
	}
	return nil
}

func (r *bccRig) events() ([]event.Event, error) { return r.sess.Read(context.Background(), 0, 0) }

func bccDecode[T any](e event.Event) (T, error) {
	var v T
	err := json.Unmarshal([]byte(e.Raw()), &v)
	if err != nil {
		err = fmt.Errorf("decode %s: %w", e.Type(), err)
	}
	return v, err
}

func bccOfType(evs []event.Event, typ string) []event.Event {
	var out []event.Event
	for _, e := range evs {
		if e.Type() == typ {
			out = append(out, e)
		}
	}
	return out
}

func bccTurnEnds(evs []event.Event) ([]event.Reason, error) {
	var out []event.Reason
	for _, e := range bccOfType(evs, "turn/end") {
		te, err := bccDecode[event.TurnEnd](e)
		if err != nil {
			return nil, err
		}
		out = append(out, te.Reason)
	}
	return out, nil
}

func bccStepEnds(evs []event.Event) ([]event.StepEnd, error) {
	var out []event.StepEnd
	for _, e := range bccOfType(evs, "step/end") {
		se, err := bccDecode[event.StepEnd](e)
		if err != nil {
			return nil, err
		}
		out = append(out, se)
	}
	return out, nil
}

// bccBalanced reports brackets left open or unequal start/end counts.
func bccBalanced(s session.ReadSession) error {
	if b := s.Open(); b.IsOpen() {
		return fmt.Errorf("brackets still open: %+v", b)
	}
	evs, err := s.Read(context.Background(), 0, 0)
	if err != nil {
		return err
	}
	if ts, te := len(bccOfType(evs, "turn/start")), len(bccOfType(evs, "turn/end")); ts != te {
		return fmt.Errorf("%d turn/start, %d turn/end", ts, te)
	}
	if ss, se := len(bccOfType(evs, "step/start")), len(bccOfType(evs, "step/end")); ss != se {
		return fmt.Errorf("%d step/start, %d step/end", ss, se)
	}
	return nil
}

// bccUnanswered reports the tool calls of s's history fold with no result.
func bccUnanswered(s session.ReadSession) error {
	msgs, err := s.History().Messages()
	if err != nil {
		return err
	}
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
		return fmt.Errorf("tool calls left unanswered in history: %v", open)
	}
	return nil
}

func bccClosed(s session.ReadSession) error {
	if err := bccBalanced(s); err != nil {
		return err
	}
	return bccUnanswered(s)
}

func bccEcho(id string) fakemodel.Event { return fakemodel.Tool(id, "echo", `{"text":"x"}`) }

func bccSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ------------------------------------------------- property (TC-11a)

type bccExit int

const (
	bccExitNone bccExit = iota
	bccExitCancelInModel
	bccExitCancelInTool
	bccExitBudget
	bccExitProvider
	bccExitLease
	bccExits
)

func (e bccExit) String() string {
	return [...]string{"none", "cancel-in-model", "cancel-in-tool", "budget", "provider-error", "lease-loss"}[e]
}

type bccPlan struct {
	rng        *rand.Rand
	exit       bccExit
	at         int
	scope      agent.CancelScope
	inbox      agent.InboxChoice
	park       bool
	leaseAfter int
	// fired is set only once the planned exit actually happens; a case whose
	// model answers before the exit fires runs as a none case.
	fired atomic.Bool
}

// observed is the exit the case actually took.
func (p *bccPlan) observed() bccExit {
	if p.fired.Load() {
		return p.exit
	}
	return bccExitNone
}

func newBccPlan(seed uint64) *bccPlan {
	rng := rand.New(rand.NewPCG(seed, 0xe2e5eed))
	p := &bccPlan{rng: rng, exit: bccExit(rng.IntN(int(bccExits))), at: 1 + rng.IntN(4),
		scope: agent.ScopeTurn, inbox: agent.InboxKeep, park: rng.IntN(2) == 0, leaseAfter: 1 + rng.IntN(40)}
	if rng.IntN(2) == 0 {
		p.scope = agent.ScopeStep
	}
	if rng.IntN(2) == 0 {
		p.inbox = agent.InboxDiscard
	}
	return p
}

// bccLazyModel scripts each model turn as it is requested, so no scripted
// turn is left over whatever exit the case takes.
type bccLazyModel struct {
	p      *bccPlan
	tr     *fakemodel.Transport
	mu     sync.Mutex
	n      int
	calls  int
	cancel func()
}

func (m *bccLazyModel) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	m.n++
	n := m.n
	turn := m.next(n)
	m.mu.Unlock()
	if m.p.exit == bccExitCancelInModel && n == m.p.at {
		m.cancel()
	}
	m.tr.Script(turn)
	return m.tr.RoundTrip(r)
}

func (m *bccLazyModel) next(n int) fakemodel.Turn {
	p := m.p
	switch {
	case p.exit == bccExitProvider && n == p.at:
		p.fired.Store(true)
		if p.rng.IntN(2) == 0 {
			return fakemodel.Status(500)
		}
		return fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("half"), fakemodel.Error(vocab.CodeServer, "boom")}}
	case p.exit == bccExitCancelInModel && n == p.at && p.rng.IntN(2) == 0:
		return fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("so far")}, Hang: true}
	case p.exit == bccExitCancelInTool && n == p.at:
		return fakemodel.Calls(m.call("echo"), m.call("trip"), m.call("echo"))
	case p.exit != bccExitBudget && n > m.gate() && (n > 6 || p.rng.IntN(4) == 0):
		return fakemodel.Reply(fmt.Sprintf("answer %d", n))
	}
	var calls []fakemodel.Event
	for range 1 + p.rng.IntN(3) {
		calls = append(calls, m.call("echo"))
	}
	return fakemodel.Calls(calls...)
}

func (m *bccLazyModel) gate() int {
	switch m.p.exit {
	case bccExitCancelInModel, bccExitCancelInTool, bccExitProvider:
		return m.p.at
	}
	return 0
}

func (m *bccLazyModel) call(name string) fakemodel.Event {
	m.calls++
	return fakemodel.Tool(fmt.Sprintf("call_%d", m.calls), name, `{"text":"x"}`)
}

// bccLossy loses the session's lease to another writer before append number
// left, counted once armed.
type bccLossy struct {
	session.Session
	mu   sync.Mutex
	left int
	lose func()
}

func (l *bccLossy) tick() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.left == 0 {
		return
	}
	if l.left--; l.left == 0 {
		l.lose()
	}
}

func (l *bccLossy) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	l.tick()
	return l.Session.Append(ctx, d)
}

func (l *bccLossy) AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) {
	l.tick()
	return l.Session.AppendGroup(ctx, ds)
}

func (s *bccState) randomScripts(list string) error {
	s.exits = bccSplit(list)
	if len(s.exits) == 0 {
		return errors.New("no exits named")
	}
	return s.boot()
}

func (s *bccState) propertyCasesRun(n int) error {
	s.cases = n
	s.seen = map[bccExit]int{}
	for i := range n {
		p := newBccPlan(uint64(i))
		tb := newBccTB()
		err := s.runCase(tb, i, p)
		s.seen[p.observed()]++
		if derr := tb.dispose(); err == nil {
			err = derr
		}
		if err != nil {
			s.failures = append(s.failures, fmt.Sprintf("case %03d (%s, at %d, scope %s, inbox %s, park %v): %v",
				i, p.exit, p.at, p.scope, p.inbox, p.park, err))
			// The first failure is the evidence; later ones add only noise.
			return nil
		}
	}
	return nil
}

func (s *bccState) runCase(tb *bccTB, i int, p *bccPlan) error {
	id := fmt.Sprintf("bcc-p-%03d", i)
	st := memstore.New()
	model := &bccLazyModel{p: p, tr: fakemodel.NewTransport(tb)}
	var lost atomic.Bool
	var wrapped *bccLossy
	o := bccRigOptions{id: id, store: st, model: model, failOK: true}
	if p.exit == bccExitBudget {
		o.budget = &driver.Budget{MaxSteps: p.at}
	}
	if p.exit == bccExitLease {
		o.wrap = func(sess session.Session) session.Session {
			wrapped = &bccLossy{Session: sess, lose: func() {
				if _, err := st.OpenWrite(context.Background(), id, store.OpenOptions{Takeover: store.Force}); err != nil {
					tb.Errorf("take the lease: %v", err)
					return
				}
				lost.Store(true)
				p.fired.Store(true)
			}}
			return wrapped
		}
	}
	r, err := s.newRig(tb, o)
	if err != nil {
		return err
	}
	// Served by the model at step p.at (cancel-in-model) or by the trip tool
	// (cancel-in-tool), so calling it means the cancel exit fired.
	cancel := func() {
		p.fired.Store(true)
		if p.park {
			if err := r.send(agent.ListNextStep, "parked step input"); err != nil {
				tb.Errorf("park: %v", err)
			}
			if err := r.send(agent.ListNextTurn, "parked turn input"); err != nil {
				tb.Errorf("park: %v", err)
			}
		}
		if err := r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: p.scope, Inbox: p.inbox}); err != nil {
			tb.Errorf("cancel: %v", err)
		}
	}
	model.cancel = cancel
	if p.exit == bccExitCancelInTool {
		bccTrip.Store(&cancel)
		defer bccTrip.Store(nil)
	}
	if err := r.send(agent.ListNextTurn, "go"); err != nil {
		return err
	}
	if wrapped != nil {
		wrapped.mu.Lock()
		wrapped.left = p.leaseAfter
		wrapped.mu.Unlock()
	}

	ctx, stop := context.WithTimeout(context.Background(), bccWait)
	defer stop()
	werr := r.drv.WaitIdle(ctx)
	switch {
	case lost.Load() && werr != nil:
		if ctx.Err() != nil {
			return fmt.Errorf("the driver hung after losing its lease: %v", werr)
		}
		// The driver was cut off; the next write-open closes what it left.
		s.leaseCut++
		h, _, err := session.NewService(s.root, st, session.Config{}).Open(context.Background(), nil,
			session.ID(id), session.OpenOptions{Takeover: store.Force})
		if err != nil {
			return fmt.Errorf("reopen after the lease loss: %w", err)
		}
		defer func() { _ = h.Close(context.Background()) }()
		return bccClosed(h)
	case werr != nil && !lost.Load():
		return fmt.Errorf("wait idle: %w", werr)
	}
	if err := bccClosed(r.sess); err != nil {
		return err
	}
	evs, err := r.events()
	if err != nil {
		return err
	}
	ends, err := bccTurnEnds(evs)
	if err != nil {
		return err
	}
	switch p.exit {
	case bccExitBudget:
		if len(ends) != 1 || ends[0].Kind != event.ReasonBudget || ends[0].Value != p.at {
			return fmt.Errorf("budget case ended %+v", ends)
		}
		p.fired.Store(true)
	case bccExitProvider:
		if len(ends) != 1 || ends[0].Kind != event.ReasonError || ends[0].Failure == nil {
			return fmt.Errorf("provider case ended %+v", ends)
		}
	case bccExitCancelInModel, bccExitCancelInTool:
		ses, err := bccStepEnds(evs)
		if err != nil {
			return err
		}
		cancelled := false
		for _, se := range ses {
			cancelled = cancelled || (se.Step == p.at && se.Cause == string(agent.CauseUser))
		}
		if !cancelled {
			return fmt.Errorf("step %d did not close with cause user: %+v", p.at, ses)
		}
		if !(p.scope == agent.ScopeStep && p.park) &&
			(len(ends) == 0 || ends[0].Kind != event.ReasonAborted || ends[0].Cause != string(agent.CauseUser)) {
			return fmt.Errorf("cancel case ended %+v", ends)
		}
	}
	return nil
}

func (s *bccState) everyResultAnswered() error {
	if s.cases == 0 {
		return errors.New("no property case ran")
	}
	if len(s.failures) > 0 {
		return errors.New(strings.Join(s.failures, "\n"))
	}
	return nil
}

func (s *bccState) everyBracketBalanced() error {
	// runCase checks brackets and tool results together per case; a failure of
	// either is recorded in s.failures.
	return s.everyResultAnswered()
}

func (s *bccState) everyExitInjected(list string) error {
	for _, name := range bccSplit(list) {
		found := false
		for e := bccExitNone; e < bccExits; e++ {
			if e.String() == name {
				found = true
				if s.seen[e] == 0 {
					return fmt.Errorf("no case exited %s: %v", name, s.seen)
				}
			}
		}
		if !found {
			return fmt.Errorf("unknown exit %q", name)
		}
	}
	for _, name := range s.exits {
		hit := false
		for e := bccExitNone; e < bccExits; e++ {
			if strings.HasPrefix(e.String(), name) && s.seen[e] > 0 {
				hit = true
			}
		}
		if !hit {
			return fmt.Errorf("injected exit family %q never ran: %v", name, s.seen)
		}
	}
	return nil
}

func (s *bccState) leaseCutClosed() error {
	if s.leaseCut == 0 {
		return errors.New("no lease-loss case cut the driver off")
	}
	return nil
}

// ----------------------------------------- cancel step (DH-T-AGENT-16)

func (s *bccState) runningTurnInStep2() error {
	if err := s.boot(); err != nil {
		return err
	}
	hooks := driver.Hooks{ContinueOrStop: func(turn, step int) {
		s.pointsMu.Lock()
		s.points1 = append(s.points1, [2]int{turn, step})
		s.pointsMu.Unlock()
	}}
	r, err := s.newRig(s.tb, bccRigOptions{id: "bcc-cancel-1", hooks: hooks, turns: []fakemodel.Turn{
		fakemodel.Calls(bccEcho("c1")), {Hang: true}, fakemodel.Reply("done")}})
	if err != nil {
		return err
	}
	s.run1 = r
	if err := r.send(agent.ListNextTurn, "start"); err != nil {
		return err
	}
	return r.waitRequest(2)
}

func (s *bccState) hookRecordsPoint14() error {
	s.pointsMu.Lock()
	defer s.pointsMu.Unlock()
	if len(s.points1) != 1 || s.points1[0] != [2]int{1, 1} {
		return fmt.Errorf("before the cancel point 14 was reached at %v, want once after step 1", s.points1)
	}
	return nil
}

func (s *bccState) parkMessage(text string) error {
	s.parkedText = text
	return s.run1.send(agent.ListNextStep, text)
}

func bccCancelOptions(scope, inbox string) agent.CancelOptions {
	return agent.CancelOptions{Scope: agent.CancelScope(scope), Inbox: agent.InboxChoice(inbox)}
}

func (s *bccState) cancelCalled(cause, scope, inbox string) error {
	if err := s.run1.drv.Cancel(agent.Cause(cause), bccCancelOptions(scope, inbox)); err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	return s.run1.idle()
}

// step2 is the events between step 2's step/start and its step/end.
func bccStep(evs []event.Event, step int) ([]event.Event, error) {
	var out []event.Event
	in := false
	for _, e := range evs {
		if e.Type() == "step/start" {
			ss, err := bccDecode[event.StepStart](e)
			if err != nil {
				return nil, err
			}
			in = ss.Step == step
		}
		if in {
			out = append(out, e)
		}
		if e.Type() == "step/end" {
			in = false
		}
	}
	return out, nil
}

func (s *bccState) attemptSettles(class string) error {
	evs, err := s.run1.events()
	if err != nil {
		return err
	}
	step2, err := bccStep(evs, 2)
	if err != nil {
		return err
	}
	atts := bccOfType(step2, "assistant/attempt")
	if len(atts) != 1 {
		return fmt.Errorf("step 2 settled %d attempts", len(atts))
	}
	a, err := bccDecode[event.AssistantAttemptPayload](atts[0])
	if err != nil {
		return err
	}
	if a.Failure == nil || string(a.Failure.Class) != class {
		return fmt.Errorf("step 2's attempt settled %+v, want %s", a.Failure, class)
	}
	return nil
}

func (s *bccState) stepEndCause(step int, cause, closedSet string) error {
	set := bccSplit(closedSet)
	for _, c := range set {
		if !agent.Cause(c).Valid() {
			return fmt.Errorf("cause %q of the closed set is not valid in the driver", c)
		}
	}
	if !slices.Contains(set, cause) {
		return fmt.Errorf("cause %q is not in the closed set %v", cause, set)
	}
	if agent.Cause("timeout").Valid() {
		return errors.New("a cause outside the closed set is accepted")
	}
	evs, err := s.run1.events()
	if err != nil {
		return err
	}
	ses, err := bccStepEnds(evs)
	if err != nil {
		return err
	}
	for _, se := range ses {
		if se.Turn == 1 && se.Step == step {
			if se.Cause != cause {
				return fmt.Errorf("step/end for step %d records cause %q, want %q", step, se.Cause, cause)
			}
			return nil
		}
	}
	return fmt.Errorf("no step/end for step %d: %+v", step, ses)
}

func (s *bccState) noTurnEndByCancel() error {
	evs, err := s.run1.events()
	if err != nil {
		return err
	}
	ends, err := bccTurnEnds(evs)
	if err != nil {
		return err
	}
	if len(ends) != 1 || ends[0].Kind != event.ReasonCompleted {
		return fmt.Errorf("turn/end %+v: want only the completed end written after step 3", ends)
	}
	// The one turn/end must come after step 3's step/end, not at the cancel.
	lastStepEnd, turnEnd := -1, -1
	for i, e := range evs {
		switch e.Type() {
		case "step/end":
			lastStepEnd = i
		case "turn/end":
			turnEnd = i
		}
	}
	ses, err := bccStepEnds(evs)
	if err != nil {
		return err
	}
	if len(ses) == 0 || ses[len(ses)-1].Step != 3 || turnEnd < lastStepEnd {
		return fmt.Errorf("turn/end at %d is not after step 3's step/end at %d (%+v)", turnEnd, lastStepEnd, ses)
	}
	return nil
}

func (s *bccState) point14Once(step int) error {
	s.pointsMu.Lock()
	defer s.pointsMu.Unlock()
	n := 0
	for _, p := range s.points1 {
		if p[1] == step {
			n++
			if p[0] != 1 {
				return fmt.Errorf("point 14 for step %d was on turn %d, want 1", step, p[0])
			}
		}
	}
	if n != 1 {
		return fmt.Errorf("point 14 reached for step %d %d times (%v), want once", step, n, s.points1)
	}
	return nil
}

func (s *bccState) step3WithParked() error {
	evs, err := s.run1.events()
	if err != nil {
		return err
	}
	if n := len(bccOfType(evs, "turn/start")); n != 1 {
		return fmt.Errorf("%d turns opened, want step 3 in the same turn", n)
	}
	step3, err := bccStep(evs, 3)
	if err != nil {
		return err
	}
	if len(step3) == 0 {
		return errors.New("no step 3 opened")
	}
	if ss, err := bccDecode[event.StepStart](step3[0]); err != nil || ss.Turn != 1 {
		return fmt.Errorf("step 3 opened on %+v (%v), want turn 1", ss, err)
	}
	reqs := s.run1.tr.Requests()
	if len(reqs) != 3 {
		return fmt.Errorf("%d model requests, want 3", len(reqs))
	}
	// Step 3 is opened with the parked message: after step 2 closed, the
	// next-step list is claimed into the turn and the message is logged as user
	// input before step 3's step/start.
	step2End, claimed, userMsg, step3Start := -1, -1, -1, -1
	for i, e := range evs {
		switch e.Type() {
		case "step/end":
			if se, err := bccDecode[event.StepEnd](e); err == nil && se.Step == 2 {
				step2End = i
			}
		case "step/start":
			if ss, err := bccDecode[event.StepStart](e); err == nil && ss.Step == 3 {
				step3Start = i
			}
		case agent.SplicedType.Name():
			sp, err := bccDecode[agent.Splice](e)
			if err == nil && step2End >= 0 && claimed < 0 && sp.List == agent.ListNextStep &&
				sp.Outcome != nil && sp.Outcome.Kind == agent.OutcomeClaimed {
				claimed = i
			}
		case "user/message":
			if strings.Contains(e.Raw(), `"text":"`+s.parkedText+`"`) {
				userMsg = i
			}
		}
	}
	if !(step2End >= 0 && step2End < claimed && claimed < userMsg && userMsg < step3Start) {
		return fmt.Errorf("want step 2 end < next-step claim < user message %q < step 3 start, got %d, %d, %d, %d",
			s.parkedText, step2End, claimed, userMsg, step3Start)
	}
	msgs, err := s.run1.sess.History().Messages()
	if err != nil {
		return err
	}
	inHistory := false
	for _, m := range msgs {
		for _, b := range m.Blocks() {
			inHistory = inHistory || (b.Text != nil && b.Text.Text == s.parkedText)
		}
	}
	if !inHistory {
		return fmt.Errorf("the parked message %q is not in the history fold step 3 was sent", s.parkedText)
	}
	if n := len(s.run1.inbox.Pending(agent.ListNextStep)); n != 0 {
		return fmt.Errorf("%d next-step items still pending, want the parked one claimed", n)
	}
	return bccClosed(s.run1.sess)
}

func (s *bccState) secondRun(scope string) error {
	var r *bccRig
	hooks := driver.Hooks{ContinueOrStop: func(turn, step int) {
		s.pointsMu.Lock()
		s.points2 = append(s.points2, [2]int{turn, step})
		s.pointsMu.Unlock()
		if step == 2 {
			s.run2CancelErr = r.drv.Cancel(agent.CauseUser, bccCancelOptions(scope, string(agent.InboxKeep)))
		}
	}}
	r, err := s.newRig(s.tb, bccRigOptions{id: "bcc-cancel-2", hooks: hooks, turns: []fakemodel.Turn{
		fakemodel.Calls(bccEcho("c1")), fakemodel.Reply("final")}})
	if err != nil {
		return err
	}
	s.run2 = r
	if err := r.send(agent.ListNextTurn, "start"); err != nil {
		return err
	}
	if err := r.idle(); err != nil {
		return err
	}
	return s.run2CancelErr
}

func (s *bccState) secondRunPoint14(step int) error {
	s.pointsMu.Lock()
	defer s.pointsMu.Unlock()
	for _, p := range s.points2 {
		if p == [2]int{1, step} {
			return nil
		}
	}
	return fmt.Errorf("second run reached point 14 at %v, want step %d", s.points2, step)
}

func (s *bccState) secondRunCompleted(kind string) error {
	if n := len(s.run2.inbox.Pending(agent.ListNextStep)); n != 0 {
		return fmt.Errorf("the second run's inbox was not empty: %d", n)
	}
	evs, err := s.run2.events()
	if err != nil {
		return err
	}
	if n := len(bccOfType(evs, "step/start")); n != 2 {
		return fmt.Errorf("%d steps, want 2 (no step 3)", n)
	}
	ends, err := bccTurnEnds(evs)
	if err != nil {
		return err
	}
	if len(ends) != 1 || ends[0].Kind != kind {
		return fmt.Errorf("turn/end %+v, want one %s", ends, kind)
	}
	return bccClosed(s.run2.sess)
}

// ------------------------------------------------ turn budget (DH-T-AGENT-8)

func (s *bccState) stepBudget(n int) error {
	s.budget = &driver.Budget{MaxSteps: n}
	return s.boot()
}

func (s *bccState) alwaysCallsTool() error {
	// Exactly MaxSteps turns are scripted: a fourth request would be an
	// unscripted call the transport reports as a failure.
	s.turns = nil
	for i := range s.budget.MaxSteps {
		s.turns = append(s.turns, fakemodel.Calls(bccEcho("c"+strconv.Itoa(i+1))))
	}
	return nil
}

func (s *bccState) turnRuns() error {
	r, err := s.newRig(s.tb, bccRigOptions{id: "bcc-budget", budget: s.budget, turns: s.turns})
	if err != nil {
		return err
	}
	s.brig = r
	if err := r.send(agent.ListNextTurn, "loop"); err != nil {
		return err
	}
	return r.idle()
}

func (s *bccState) exactlySteps(n int) error {
	evs, err := s.brig.events()
	if err != nil {
		return err
	}
	if got := len(bccOfType(evs, "step/start")); got != n {
		return fmt.Errorf("%d steps ran, want %d", got, n)
	}
	if got := len(s.brig.tr.Requests()); got != n {
		return fmt.Errorf("%d model requests, want %d", got, n)
	}
	return nil
}

func (s *bccState) turnEndBudget(kind, limit string, value int) error {
	evs, err := s.brig.events()
	if err != nil {
		return err
	}
	ends, err := bccTurnEnds(evs)
	if err != nil {
		return err
	}
	if len(ends) != 1 || ends[0].Kind != kind || ends[0].Limit != limit || ends[0].Value != value {
		return fmt.Errorf("turn/end %+v, want %s{%s, %d}", ends, kind, limit, value)
	}
	if limit != driver.LimitSteps {
		return fmt.Errorf("limit %q is not the driver's step limit %q", limit, driver.LimitSteps)
	}
	return nil
}

func (s *bccState) budgetClosed() error { return bccClosed(s.brig.sess) }

func InitializeScenario_agent_loop_milestone_m1_agent_driver_brackets_and_cancellation(ctx *godog.ScenarioContext) {
	s := &bccState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = bccState{tb: newBccTB()}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if derr := s.tb.dispose(); derr != nil && err == nil {
			return c, fmt.Errorf("scenario cleanup: %w", derr)
		}
		return c, nil
	})

	ctx.Step(`^random scripts of model replies with tool calls and a random exit injected from "([^"]+)"$`, s.randomScripts)
	ctx.Step(`^(\d+) property cases run$`, s.propertyCasesRun)
	ctx.Step(`^every case's history fold has a result for every tool call$`, s.everyResultAnswered)
	ctx.Step(`^every case's brackets balance$`, s.everyBracketBalanced)
	ctx.Step(`^every exit kind "([^"]+)" was injected at least once$`, s.everyExitInjected)
	ctx.Step(`^at least one lease-loss case cut the driver off and the next write-open closed its brackets$`, s.leaseCutClosed)

	ctx.Step(`^a running turn in step 2 on the scripted fake model$`, s.runningTurnInStep2)
	ctx.Step(`^a test hook that records each time the driver reaches point 14$`, s.hookRecordsPoint14)
	ctx.Step(`^one user message "([^"]+)" parked in the inbox$`, s.parkMessage)
	ctx.Step(`^Cancel is called with cause "([^"]+)", scope "([^"]+)" and inbox "([^"]+)"$`, s.cancelCalled)
	ctx.Step(`^step 2's attempt settles "([^"]+)"$`, s.attemptSettles)
	ctx.Step(`^step/end for step (\d+) records cause "([^"]+)" from the closed cause set "([^"]+)"$`, s.stepEndCause)
	ctx.Step(`^no turn/end is written by the cancel$`, s.noTurnEndByCancel)
	ctx.Step(`^the point-14 hook fired once for step (\d+) of the same turn$`, s.point14Once)
	ctx.Step(`^the driver opened step 3 of the same turn with the parked message$`, s.step3WithParked)
	ctx.Step(`^a second run with an empty inbox and a scripted final answer in step 2 is cancelled with scope "([^"]+)" at point 14$`, s.secondRun)
	ctx.Step(`^the second run's point-14 hook fired for step (\d+)$`, s.secondRunPoint14)
	ctx.Step(`^the second run's turn ended "([^"]+)" without a step 3$`, s.secondRunCompleted)

	ctx.Step(`^a step budget of (\d+)$`, s.stepBudget)
	ctx.Step(`^a model that always calls a tool$`, s.alwaysCallsTool)
	ctx.Step(`^the turn runs$`, s.turnRuns)
	ctx.Step(`^exactly (\d+) steps run$`, s.exactlySteps)
	ctx.Step(`^turn/end records cause "([^"]+)" with limit "([^"]+)" and value (\d+)$`, s.turnEndBudget)
	ctx.Step(`^the turn's brackets balance and every tool call has a result$`, s.budgetClosed)
}

func TestE2E_agent_loop_milestone_m1_agent_driver_brackets_and_cancellation(t *testing.T) {
	bccOuterT = t
	t.Cleanup(func() { bccOuterT = nil })
	suite := godog.TestSuite{
		Name:                "agent_loop_milestone_m1_agent_driver_brackets_and_cancellation",
		ScenarioInitializer: InitializeScenario_agent_loop_milestone_m1_agent_driver_brackets_and_cancellation,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"agent_loop_milestone_m1_agent_driver_brackets_and_cancellation.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
