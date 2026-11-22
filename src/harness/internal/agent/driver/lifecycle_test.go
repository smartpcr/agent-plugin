package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// life is the driver's factory seated in a registry over a session service.
type life struct {
	t      *testing.T
	root   *cordis.Context
	drvCtx *cordis.Context
	drv    *cordis.Fiber
	svc    session.Service
	reg    agent.Registry
	tr     *fakemodel.Transport

	mu  sync.Mutex
	log []string
}

type lifeOptions struct {
	store      store.Store
	turns      []fakemodel.Turn
	flushLimit time.Duration
}

func newLife(t *testing.T, o lifeOptions) *life {
	t.Helper()
	root, tsvc := setup(t)
	must(tsvc.Register(root, tool("echo")))
	if o.store == nil {
		o.store = memstore.New()
	}
	l := &life{t: t, root: root}
	l.svc = session.NewService(root, o.store, session.Config{})
	l.reg = agent.NewRegistry(root)
	rt := llm.New()
	l.tr = fakemodel.NewTransport(t, o.turns...)
	fakemodel.Register(t, rt, route, l.tr)
	l.drv = must(root.Apply("driver", func(c *cordis.Context) error { l.drvCtx = c; return nil }))
	must(0, l.drv.Wait())
	f := must(driver.NewFactory(driver.FactoryOptions{
		Context: l.drvCtx, Sessions: l.svc, Scopes: scope.NewTable(root), Models: rt, Tools: tsvc,
		FinalFlushTimeout: o.flushLimit,
		Observe:           func(_ agent.Ref, p string) { l.note(p) },
	}))
	must(l.reg.SetFactory(root, f))
	return l
}

func (l *life) note(s string) {
	l.mu.Lock()
	l.log = append(l.log, s)
	l.mu.Unlock()
}

func (l *life) notes() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.log)
}

func (l *life) fiber(name string) (*cordis.Fiber, *cordis.Context) {
	l.t.Helper()
	var c *cordis.Context
	f := must(l.root.Apply(name, func(ctx *cordis.Context) error { c = ctx; return nil }))
	must(0, f.Wait())
	return f, c
}

func (l *life) create(owner *cordis.Context, id string, setup agent.SetupFunc) (agent.Handle, error) {
	return l.reg.Create(owner, agent.CreateOptions{Session: session.ID(id), Model: agent.ModelSelection{Route: route}, Setup: setup})
}

// released asserts the session's lease is free: a new holder can open it.
func (l *life) released(id string) {
	l.t.Helper()
	h, _, err := l.svc.Open(bg, nil, session.ID(id), session.OpenOptions{})
	if err != nil {
		l.t.Fatalf("the lease of %s was kept: %v", id, err)
	}
	_ = h.Close(bg)
}

func (l *life) absent(id string) {
	l.t.Helper()
	if _, ok := l.reg.Get(session.ID(id)); ok {
		l.t.Fatalf("%s is still addressable", id)
	}
	for _, a := range l.reg.List() {
		if a.Ref().Session == session.ID(id) {
			l.t.Fatalf("%s is still listed", id)
		}
	}
}

func (l *life) notices() (created, disposed *[]string) {
	created, disposed = new([]string), new([]string)
	must(agent.Created.On(l.root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		l.mu.Lock()
		*created = append(*created, string(n.Agent.Ref().Session))
		l.mu.Unlock()
		return nil
	}))
	must(agent.Disposed.On(l.root, func(_ *cordis.Context, n agent.DisposedNotice) error {
		l.mu.Lock()
		*disposed = append(*disposed, string(n.Ref.Session))
		l.log = append(l.log, "disposed")
		l.mu.Unlock()
		return nil
	}))
	return created, disposed
}

func waitRequests(t *testing.T, tr *fakemodel.Transport, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(tr.Requests()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("model request %d was never sent", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// DH-T-AGENT-19, TC-11a: a creation whose third registration fails leaves no
// agent, no Created notice and no agent event; the first two registrations
// were rolled back, last first, and the session's lease is free.
func TestFailedCreateLeavesNothing(t *testing.T) {
	l := newLife(t, lifeOptions{})
	created, disposed := l.notices()
	var undone []string
	setup := func(sc *cordis.Context, _ agent.Agent) (func() error, error) {
		for _, n := range []string{"one", "two"} {
			if _, err := sc.Effect(n, func() (cordis.Disposer, error) {
				return func() error { undone = append(undone, n); return nil }, nil
			}); err != nil {
				return nil, err
			}
		}
		_, err := sc.Effect("three", func() (cordis.Disposer, error) { return nil, errors.New("third fails") })
		return nil, err
	}
	if _, err := l.create(l.root, "a", setup); err == nil || !strings.Contains(err.Error(), "third fails") {
		t.Fatalf("Create: %v", err)
	}
	l.absent("a")
	if !slices.Equal(undone, []string{"two", "one"}) {
		t.Fatalf("rolled back %v", undone)
	}
	if len(*created) != 0 || len(*disposed) != 0 {
		t.Fatalf("notices created %v disposed %v", *created, *disposed)
	}
	rs, done := must3(l.svc.OpenRead(bg, "a"))
	defer done()
	if evs := must(rs.Read(bg, 0, 0)); len(ofType(evs, "turn/start")) != 0 {
		t.Fatalf("a partial agent ran: %v", evs)
	}
	l.released("a")
	if len(l.tr.Requests()) != 0 {
		t.Fatal("a rolled-back agent called the model")
	}
}

// DH-T-AGENT-19: a Created listener's error rolls back after Created, so
// Disposed follows; a listener that waits on the agent it is told about gets
// an error instead of a deadlock.
func TestCreatedVetoAndSelfWait(t *testing.T) {
	l := newLife(t, lifeOptions{})
	created, disposed := l.notices()
	var selfWait error
	veto := must(agent.Created.On(l.root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		selfWait = agent.WaitIdle(bg, n.Agent)
		return errors.New("vetoed")
	}))
	if _, err := l.create(l.root, "v", nil); err == nil {
		t.Fatal("a Created error did not fail the creation")
	}
	veto.Dispose()
	if !errors.Is(selfWait, agent.ErrSelfWait) {
		t.Fatalf("self-wait: %v", selfWait)
	}
	l.absent("v")
	if !slices.Equal(*created, []string{"v"}) || !slices.Equal(*disposed, []string{"v"}) {
		t.Fatalf("notices created %v disposed %v", *created, *disposed)
	}
	l.released("v")
}

// DH-T-AGENT-19: the owner unloading during Setup rolls the creation back.
func TestOwnerDisposedDuringCreate(t *testing.T) {
	l := newLife(t, lifeOptions{})
	of, owner := l.fiber("owner")
	setup := func(*cordis.Context, agent.Agent) (func() error, error) {
		go of.Dispose()
		<-owner.Lifetime().Done()
		return nil, nil
	}
	if _, err := l.create(owner, "o", setup); err == nil {
		t.Fatal("a creation outlived its owner")
	}
	l.absent("o")
	l.released("o")
}

// DH-T-AGENT-20: the teardown points occur in order T1 to T8, Disposed after
// the flush and before the lease is released; three concurrent disposers
// share the one run and input is refused from T1.
func TestTeardownOrderAndConcurrentDisposers(t *testing.T) {
	l := newLife(t, lifeOptions{turns: []fakemodel.Turn{fakemodel.Reply("hi")}})
	_, owner := l.fiber("owner")
	ownerFiber := owner
	h := must(l.create(owner, "t", nil))
	_, disposed := l.notices()
	must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	must(0, agent.WaitIdle(ctx, h.Agent()))

	var refused atomic.Value
	stop := must(session.Closed.On(l.root, func(_ *cordis.Context, a session.ClosedArgs) error {
		if a.ID == "t" {
			l.note("closed")
		}
		return nil
	}))
	defer stop.Dispose()
	a := h.Agent()
	l.mu.Lock()
	l.log = nil
	l.mu.Unlock()
	// Refuse-from-T1 is checked from the observer, between T1 and T2.
	go func() {
		for !slices.Contains(l.notes(), "T1") {
			time.Sleep(time.Millisecond)
		}
		_, err := agent.Send(bg, a, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "late"})
		refused.Store(&err)
	}()
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i, d := range []func() error{
		func() error { return h.Dispose(bg) },
		func() error { ownerFiber.Dispose(); return nil },
		func() error { l.drv.Dispose(); return nil },
	} {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = d() }()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if done := agent.Done(a); done != nil {
		select {
		case <-done:
		default:
			t.Fatal("the driver goroutine outlived the teardown")
		}
	}
	got := l.notes()
	var points []string
	for _, p := range got {
		if strings.HasPrefix(p, "T") {
			points = append(points, p)
		}
	}
	if want := []string{"T1", "T2", "T3", "T4", "T5", "T6", "T7", "T8"}; !slices.Equal(points, want) {
		t.Fatalf("teardown points %v", got)
	}
	at := func(s string) int { return slices.Index(got, s) }
	if !(at("T6") < at("disposed") && at("disposed") < at("T7")) {
		t.Fatalf("Disposed out of place: %v", got)
	}
	if c := at("closed"); c >= 0 && c < at("T7") {
		t.Fatalf("the session closed before the agent left: %v", got)
	}
	if !slices.Equal(*disposed, []string{"t"}) {
		t.Fatalf("Disposed %v", *disposed)
	}
	for deadline := time.Now().Add(5 * time.Second); refused.Load() == nil && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if p, _ := refused.Load().(*error); p == nil || !errors.Is(*p, agent.ErrClosing) {
		t.Fatalf("send after T1: %v", p)
	}
	l.absent("t")
	l.released("t")
}

// DH-T-AGENT-21, TC-11b: the host shutting down mid-turn on the SQLite store
// leaves a log that ends with closed brackets, turn/end{aborted, disposed}
// and the interrupted settlement, with nothing for the next open to close.
func TestShutdownKeepsTheLastEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	st := must(sqlite.Open(bg, path))
	l := newLife(t, lifeOptions{store: st, turns: []fakemodel.Turn{{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true}}})
	h := must(l.create(l.root, "s", nil))
	must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
	waitRequests(t, l.tr, 1)
	time.Sleep(200 * time.Millisecond)
	l.root.Dispose()
	must(0, st.Close(bg))

	st = must(sqlite.Open(bg, path))
	defer st.Close(bg)
	root := cordis.New()
	defer root.Dispose()
	sh, rep, err := session.NewService(root, st, session.Config{}).Open(bg, nil, "s", session.OpenOptions{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer sh.Close(bg)
	if rep.Interrupted || len(rep.Closers) != 0 {
		t.Fatalf("the reopen had to close the log: %+v", rep)
	}
	checkClosed(t, sh)
	evs := must(sh.Read(bg, 0, 0))
	ends := turnEnds(t, evs)
	if len(ends) == 0 || ends[len(ends)-1].Kind != event.ReasonAborted || ends[len(ends)-1].Cause != string(agent.CauseDisposed) {
		t.Fatalf("turn ends %+v", ends)
	}
	var last string
	for _, e := range ofType(evs, event.AssistantMessage.Name()) {
		p := must(event.AssistantMessage.Decode(e.Record()))
		for _, b := range p.Message.Blocks() {
			if b.Text != nil {
				last = b.Text.Text
			}
		}
	}
	if last != "par" {
		t.Fatalf("the last settlement is %q", last)
	}
}

// flaky fails or stalls every writer's Flush once armed.
type flaky struct {
	store.Store
	mode atomic.Int32 // 0 pass, 1 fail, 2 stall
}

type flakyWriter struct {
	store.Writer
	s *flaky
}

var errFlush = errors.New("disk gone")

func (s *flaky) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := s.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return &flakyWriter{w, s}, nil
}

func (s *flaky) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	w, err := s.Store.OpenWrite(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return &flakyWriter{w, s}, nil
}

func (w *flakyWriter) Flush(ctx context.Context) error {
	switch w.s.mode.Load() {
	case 1:
		return errFlush
	case 2:
		<-ctx.Done()
		return ctx.Err()
	}
	return w.Writer.Flush(ctx)
}

// DH-T-AGENT-21: a failing or slow final flush is returned with the last
// durable position, and the lease is released all the same.
func TestFailingFlushIsReturned(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode int32
	}{{"failing", 1}, {"slow", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			st := &flaky{Store: memstore.New()}
			l := newLife(t, lifeOptions{store: st, flushLimit: 50 * time.Millisecond, turns: []fakemodel.Turn{fakemodel.Reply("hi")}})
			h := must(l.create(l.root, "f", nil))
			must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
			ctx, cancel := context.WithTimeout(bg, 10*time.Second)
			defer cancel()
			must(0, agent.WaitIdle(ctx, h.Agent()))
			st.mode.Store(tc.mode)
			start := time.Now()
			err := h.Dispose(bg)
			var fe *agent.FlushError
			if !errors.Is(err, agent.ErrFlushFailed) || !errors.As(err, &fe) {
				t.Fatalf("dispose: %v", err)
			}
			if fe.Durable <= 0 {
				t.Fatalf("durable position %d", fe.Durable)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("the flush was not bounded")
			}
			st.mode.Store(0)
			l.absent("f")
			l.released("f")
		})
	}
}

func must3[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

// DH-T-AGENT-19: an owner unloading while its creation is blocked, in Setup
// or in a Created listener, does not finish before the creation has rolled
// back: the lease and registrations cannot outlive the owner's unload.
func TestOwnerUnloadJoinsABlockedCreation(t *testing.T) {
	for _, at := range []string{"setup", "created"} {
		t.Run(at, func(t *testing.T) {
			l := newLife(t, lifeOptions{})
			created, disposed := l.notices()
			of, owner := l.fiber("owner")
			entered, release := make(chan struct{}), make(chan struct{})
			var undone atomic.Bool
			block := func() { close(entered); <-release }
			setup := func(sc *cordis.Context, _ agent.Agent) (func() error, error) {
				if _, err := sc.Effect("reg", func() (cordis.Disposer, error) {
					return func() error { undone.Store(true); return nil }, nil
				}); err != nil {
					return nil, err
				}
				if at == "setup" {
					block()
				}
				return nil, nil
			}
			if at == "created" {
				must(agent.Created.On(l.root, func(_ *cordis.Context, n agent.CreatedNotice) error {
					if n.Agent.Ref().Session == "b" {
						block()
					}
					return nil
				}))
			}
			res := make(chan error, 1)
			go func() { _, err := l.create(owner, "b", setup); res <- err }()
			<-entered
			unloaded := make(chan struct{})
			go func() { of.Dispose(); _ = of.Wait(); close(unloaded) }()
			select {
			case <-unloaded:
				t.Fatal("the owner finished unloading while its creation still held the session")
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			select {
			case <-unloaded:
			case <-time.After(10 * time.Second):
				t.Fatal("the owner never finished unloading")
			}
			if !undone.Load() {
				t.Fatal("the registration outlived the owner's unload")
			}
			l.absent("b")
			l.released("b")
			if err := <-res; err == nil {
				t.Fatal("a creation outlived its owner")
			}
			want := []string(nil)
			if at == "created" {
				want = []string{"b"}
			}
			if !slices.Equal(*created, want) || !slices.Equal(*disposed, want) {
				t.Fatalf("notices created %v disposed %v", *created, *disposed)
			}
		})
	}
}

// DH-T-AGENT-19, -21: a rollback whose final flush fails reports both the
// cause of the rollback and the flush failure.
func TestRollbackKeepsTheFlushFailure(t *testing.T) {
	st := &flaky{Store: memstore.New()}
	l := newLife(t, lifeOptions{store: st, flushLimit: 50 * time.Millisecond})
	errVeto := errors.New("vetoed")
	must(agent.Created.On(l.root, func(*cordis.Context, agent.CreatedNotice) error {
		st.mode.Store(1)
		return errVeto
	}))
	_, err := l.create(l.root, "r", nil)
	st.mode.Store(0)
	if !errors.Is(err, errVeto) || !errors.Is(err, agent.ErrFlushFailed) {
		t.Fatalf("create: %v", err)
	}
	var fe *agent.FlushError
	if !errors.As(err, &fe) {
		t.Fatalf("no flush error in %v", err)
	}
	l.absent("r")
	l.released("r")
}

// interrupted leaves session id with an open turn and one queued message, as
// a crashed host would.
func (l *life) interrupted(id string) {
	l.t.Helper()
	h := must(l.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
	in := must(agent.OpenInbox(bg, h))
	must(in.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "queued"}))
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
	}))
	must(0, h.Close(bg))
}

// DH-D-AGENT-6, agent-loop 6.3: Resume closes the interrupted brackets and
// hands the recovery report to the carrier; the agent starts idle with its
// queued input parked, which the next waking input runs with.
func TestResumeReportsAndStartsIdle(t *testing.T) {
	l := newLife(t, lifeOptions{turns: []fakemodel.Turn{fakemodel.Reply("one"), fakemodel.Reply("two")}})
	l.interrupted("q")
	h, err := l.reg.Resume(l.root, agent.CreateOptions{Session: "q", Model: agent.ModelSelection{Route: route}})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	rep, ok := agent.OpenReport(h.Agent())
	if !ok || !rep.Interrupted || len(rep.Closers) == 0 {
		t.Fatalf("report %+v %v", rep, ok)
	}
	if _, ok := l.reg.Get("q"); !ok {
		t.Fatal("the resumed agent is not addressable")
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(l.tr.Requests()); n != 0 {
		t.Fatalf("a resumed agent ran without a wake: %d requests", n)
	}
	if s := h.Agent().Status(); s != agent.StatusIdle {
		t.Fatalf("status %s", s)
	}
	must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "wake"}))
	waitRequests(t, l.tr, 2)
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	must(0, agent.WaitIdle(ctx, h.Agent()))
	var texts []string
	for _, e := range ofType(must(h.Agent().Session().Read(bg, 0, 0)), event.UserMessage.Name()) {
		p := must(event.UserMessage.Decode(e.Record()))
		for _, b := range p.Message.Blocks() {
			if b.Text != nil {
				texts = append(texts, b.Text.Text)
			}
		}
	}
	if !slices.Equal(texts, []string{"queued", "wake"}) {
		t.Fatalf("user messages %v: the queued input was not kept", texts)
	}
	must(0, h.Dispose(bg))
	l.released("q")
}

// DH-D-AGENT-6: a resume whose Setup fails leaves no agent and frees the
// lease; the closers it wrote stay, so the next open has nothing to close.
func TestResumeRollsBackOnSetupFailure(t *testing.T) {
	l := newLife(t, lifeOptions{})
	created, _ := l.notices()
	l.interrupted("x")
	_, err := l.reg.Resume(l.root, agent.CreateOptions{Session: "x", Model: agent.ModelSelection{Route: route},
		Setup: func(*cordis.Context, agent.Agent) (func() error, error) { return nil, errors.New("no tools") }})
	if err == nil || !strings.Contains(err.Error(), "no tools") {
		t.Fatalf("resume: %v", err)
	}
	l.absent("x")
	if len(*created) != 0 {
		t.Fatalf("created %v", *created)
	}
	h, rep, err := l.svc.Open(bg, nil, "x", session.OpenOptions{})
	if err != nil {
		t.Fatalf("the lease was kept: %v", err)
	}
	defer h.Close(bg)
	if rep.Interrupted || len(rep.Closers) != 0 {
		t.Fatalf("the failed resume left the log open: %+v", rep)
	}
	checkClosed(t, h)
}
