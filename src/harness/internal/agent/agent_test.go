package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type env struct {
	root *cordis.Context
	logs *cordis.MemoryExporter
	tab  scope.Table
	svc  session.Service
	reg  agent.Registry
	// built counts factory builds; closed counts instance closes.
	built, closed atomic.Int32
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{root: cordis.New(), logs: &cordis.MemoryExporter{}}
	e.root.Loggers().ClearExporters()
	e.root.Loggers().AddExporter(e.logs)
	t.Cleanup(e.root.Dispose)
	e.tab = scope.NewTable(e.root)
	e.svc = session.NewService(e.root, memstore.New(), session.Config{})
	e.reg = agent.NewRegistry(e.root)
	return e
}

// stub is the stub factory's agent: a real session and scope, no loop.
type stub struct {
	e    *env
	ref  agent.Ref
	h    session.Handle
	sc   *scope.Scope
	done atomic.Bool
}

func (s *stub) Ref() agent.Ref               { return s.ref }
func (s *stub) Session() session.ReadSession { return s.h }
func (s *stub) Scope() *cordis.Context       { return s.sc.Ctx() }
func (s *stub) ScopeKey() scope.Key          { return s.sc.Key() }
func (s *stub) Status() agent.Status         { return agent.StatusIdle }
func (s *stub) State() agent.State {
	if s.done.Load() {
		return agent.StateDisposed
	}
	return agent.StateLive
}
func (s *stub) Close(ctx context.Context) error {
	s.done.Store(true)
	s.e.closed.Add(1)
	_ = s.sc.Close()
	return s.h.Close(ctx)
}

// stubFactory replaces the driver: registry consumers run unchanged on it.
func (e *env) stubFactory() agent.Factory {
	return agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		e.built.Add(1)
		h, err := e.svc.Create(bg, nil, session.CreateOptions{})
		if err != nil {
			return nil, err
		}
		sc, err := e.tab.New(owner, string(o.Session))
		if err != nil {
			return nil, err
		}
		return &stub{e: e, ref: ref, h: h, sc: sc}, nil
	})
}

func (e *env) seat(t *testing.T) {
	t.Helper()
	must(e.reg.SetFactory(e.root, e.stubFactory()))
}

func (e *env) create(t *testing.T, id string) agent.Handle {
	t.Helper()
	h := must(e.reg.Create(e.root, agent.CreateOptions{Session: session.ID(id)}))
	t.Cleanup(func() { _ = h.Dispose(bg) })
	return h
}

// DH-T-AGENT-1: create, get, list; a second live agent with one id is refused;
// Get gives no way to dispose; the owner's unload disposes; one factory seat.
func TestRegistryCreateGetList(t *testing.T) {
	e := newEnv(t)
	if _, err := e.reg.Create(e.root, agent.CreateOptions{Session: "a"}); !errors.Is(err, agent.ErrNoFactory) {
		t.Fatalf("no factory: %v", err)
	}
	e.seat(t)
	if _, err := e.reg.SetFactory(e.root, e.stubFactory()); !errors.Is(err, agent.ErrFactoryTaken) {
		t.Fatalf("a second seat: %v", err)
	}
	if _, err := e.reg.Create(e.root, agent.CreateOptions{}); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("no session id: %v", err)
	}
	hb := e.create(t, "b")
	ha := e.create(t, "a")
	if _, err := e.reg.Create(e.root, agent.CreateOptions{Session: "a"}); !errors.Is(err, agent.ErrDuplicate) {
		t.Fatalf("a second live agent: %v", err)
	}
	if e.built.Load() != 2 {
		t.Fatal("a refused Create reached the factory")
	}
	got, ok := e.reg.Get("a")
	if !ok || got.Ref() != ha.Agent().Ref() || got.State() != agent.StateLive {
		t.Fatalf("Get: %v %v", got, ok)
	}
	if _, ok := got.(agent.Handle); ok {
		t.Fatal("Get hands out a Handle")
	}
	if _, ok := got.(interface{ Close(context.Context) error }); ok {
		t.Fatal("Get hands out the instance's Close")
	}
	if l := e.reg.List(); len(l) != 2 || l[0].Ref().Session != "a" || l[1].Ref().Session != "b" {
		t.Fatalf("List: %v", l)
	}
	if err := hb.Dispose(bg); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.reg.Get("b"); ok || e.closed.Load() != 1 || hb.Dispose(bg) != nil || e.closed.Load() != 1 {
		t.Fatal("Dispose did not remove the agent exactly once")
	}

	// An agent owned by a fiber is disposed when that fiber unloads.
	var owner *cordis.Context
	fiber := must(e.root.Apply("owner", func(c *cordis.Context) error { owner = c; return nil }))
	if err := fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	must(e.reg.Create(owner, agent.CreateOptions{Session: "c"}))
	fiber.Dispose()
	if _, ok := e.reg.Get("c"); ok || e.closed.Load() != 2 {
		t.Fatal("the owner's unload left its agent")
	}
}

// DH-T-AGENT-2: a tool body reads its initiator, a shared worker reads none;
// Resolve refuses a disposed or replaced incarnation.
func TestInitiatorAndResolve(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	h := must(e.reg.Create(e.root, agent.CreateOptions{Session: "a"}))
	ref := h.Agent().Ref()
	tool := func(ctx context.Context) (agent.Ref, bool) { return agent.Initiator(ctx) }
	if got, ok := tool(agent.WithInitiator(bg, ref)); !ok || got != ref {
		t.Fatalf("the tool body read %v %v", got, ok)
	}
	if _, ok := tool(bg); ok {
		t.Fatal("a shared worker read an initiator")
	}
	if a, err := e.reg.Resolve(ref); err != nil || a.Ref() != ref {
		t.Fatalf("Resolve live: %v", err)
	}
	must(0, h.Dispose(bg))
	if _, err := e.reg.Resolve(ref); !errors.Is(err, agent.ErrStale) {
		t.Fatalf("Resolve disposed: %v", err)
	}
	h2 := e.create(t, "a")
	if h2.Agent().Ref().Incarnation != ref.Incarnation+1 {
		t.Fatal("a new incarnation reuses the old ref")
	}
	if _, err := e.reg.Resolve(ref); !errors.Is(err, agent.ErrStale) {
		t.Fatalf("Resolve replaced: %v", err)
	}
	if _, err := e.reg.Resolve(h2.Agent().Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.Resolve(agent.Ref{Session: "zz", Incarnation: 1}); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("Resolve unknown: %v", err)
	}
	wire := must(json.Marshal(ref))
	var back agent.Ref
	must(0, json.Unmarshal(wire, &back))
	if back != ref {
		t.Fatal("a ref does not cross the wire")
	}
}

// DH-T-AGENT-3: a listener in agent A's scope sees none of agent B's events; a
// global listener sees both. Steering listeners are scoped the same way.
func TestEventsAreScopedPerAgent(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a, b := e.create(t, "a").Agent(), e.create(t, "b").Agent()
	var inA, global []agent.Ref
	must(agent.StatusChanged.On(a.Scope(), func(_ *cordis.Context, n agent.StatusNotice) error { inA = append(inA, n.Agent); return nil }))
	must(agent.StatusChanged.On(e.root, func(_ *cordis.Context, n agent.StatusNotice) error { global = append(global, n.Agent); return nil }))
	for _, x := range []agent.Agent{a, b} {
		agent.Notify(e.root, agent.StatusChanged, x, agent.StatusNotice{Agent: x.Ref(), Status: agent.StatusRunning})
	}
	if len(inA) != 1 || inA[0] != a.Ref() || len(global) != 2 {
		t.Fatalf("scoped %v global %v", inA, global)
	}
	must(agent.PreStep.On(a.Scope(), func(_ *cordis.Context, _ agent.PreStepRequest, _ func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		return agent.Reject{Reason: "a only"}, nil
	}))
	if d := must(agent.AskPreStep(e.root, b.ScopeKey(), agent.PreStepRequest{})); fmt.Sprintf("%T", d) != "agent.Enter" {
		t.Fatalf("A's PreStep listener steered B: %#v", d)
	}
	if d := must(agent.AskPreStep(e.root, a.ScopeKey(), agent.PreStepRequest{})); fmt.Sprintf("%T", d) != "agent.Reject" {
		t.Fatalf("A's PreStep listener was not asked for A: %#v", d)
	}
}

// DH-T-AGENT-27: a stub factory replaces the driver and a registry consumer, a
// creation listener and a dispose listener run unchanged; a creation listener's
// error rolls the creation back.
func TestCompositionWithAStubFactory(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	var disposed []agent.Ref
	must(agent.Disposed.On(e.root, func(_ *cordis.Context, n agent.DisposedNotice) error { disposed = append(disposed, n.Ref); return nil }))
	veto := must(agent.Created.On(e.root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		if n.Agent.Ref().Session == "vetoed" {
			return errors.New("not this one")
		}
		return nil
	}))
	defer veto.Dispose()
	consumer := func(r agent.Registry) []string {
		var ids []string
		for _, a := range r.List() {
			ids = append(ids, string(a.Ref().Session)+":"+string(a.Status()))
		}
		return ids
	}
	h := e.create(t, "a")
	if got := strings.Join(consumer(e.reg), ","); got != "a:idle" {
		t.Fatalf("consumer saw %q", got)
	}
	if _, err := e.reg.Create(e.root, agent.CreateOptions{Session: "vetoed"}); err == nil {
		t.Fatal("a Created error did not fail the creation")
	}
	if _, ok := e.reg.Get("vetoed"); ok || e.closed.Load() != 1 {
		t.Fatal("a vetoed creation was not rolled back")
	}
	must(0, h.Dispose(bg))
	// The vetoed creation had begun Created, so its rollback sent Disposed too
	// (DH-T-AGENT-19).
	if len(disposed) != 2 || disposed[0].Session != "vetoed" || disposed[1] != h.Agent().Ref() {
		t.Fatalf("Disposed notices %v", disposed)
	}
}

// DH-T-AGENT-19: the owner unloading while the creation's owner guard is
// still being acquired neither deadlocks the creation nor the unload: cordis
// releases the guard inline, the creation is refused before anything is
// built, and the owner's unload completes.
func TestOwnerUnloadDuringGuardAcquisition(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	var owner *cordis.Context
	of := must(e.root.Apply("owner", func(c *cordis.Context) error { owner = c; return nil }))
	must(0, of.Wait())
	unloaded := make(chan struct{})
	var fired atomic.Bool
	defer agent.SetOwnHook(func(r agent.Ref) {
		if r.Session != "g" || !fired.CompareAndSwap(false, true) {
			return
		}
		go func() { of.Dispose(); _ = of.Wait(); close(unloaded) }()
		<-owner.Lifetime().Done()
		// The owner's unload marks this guard dead, then waits for the acquisition.
		time.Sleep(100 * time.Millisecond)
	})()
	res := make(chan error, 1)
	go func() { _, err := e.reg.Create(owner, agent.CreateOptions{Session: "g"}); res <- err }()
	select {
	case err := <-res:
		if !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("create: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the creation deadlocked on its owner's unload")
	}
	select {
	case <-unloaded:
	case <-time.After(5 * time.Second):
		t.Fatal("the owner's unload deadlocked on the creation")
	}
	if e.built.Load() != 0 {
		t.Fatal("a creation whose owner unloaded reached the factory")
	}
	if _, ok := e.reg.Get("g"); ok {
		t.Fatal("g is addressable")
	}
	e.create(t, "g")
}
