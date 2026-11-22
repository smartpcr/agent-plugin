package subagent_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/delegate/subagent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

var bg = context.Background()

const route vocab.RouteID = "r/sub"

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type env struct {
	t    *testing.T
	root *cordis.Context
	reg  agent.Registry
	rt   subagent.Runtime
	tr   *fakemodel.Transport
}

func newEnv(t *testing.T, turns ...fakemodel.Turn) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		must(0, must(root.Use(c, cordis.None{})).Wait())
	}
	var tsvc tools.Service
	g := must(root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		tsvc, err = tools.Key.Get(c)
		return err
	}))
	must(0, g.Wait())
	models := llm.New()
	e := &env{t: t, root: root, reg: agent.NewRegistry(root), tr: fakemodel.NewTransport(t, turns...)}
	fakemodel.Register(t, models, route, e.tr)
	f := must(driver.NewFactory(driver.FactoryOptions{
		Context: root, Sessions: session.NewService(root, memstore.New(), session.Config{}),
		Scopes: scope.NewTable(root), Models: models, Tools: tsvc,
	}))
	must(e.reg.SetFactory(root, f))
	e.rt = subagent.New(e.reg)
	return e
}

// component is a loaded fiber standing for a component that delegates.
func (e *env) component(name string) (*cordis.Fiber, *cordis.Context) {
	e.t.Helper()
	var c *cordis.Context
	f := must(e.root.Apply(name, func(ctx *cordis.Context) error { c = ctx; return nil }))
	must(0, f.Wait())
	return f, c
}

func (e *env) spec(id string) subagent.Spec {
	return subagent.Spec{Session: session.ID(id), Prompt: "do it", Model: &agent.ModelSelection{Route: route}}
}

// waitRequests blocks until the scripted model has received n requests.
func waitRequests(t *testing.T, tr *fakemodel.Transport, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	if err := tr.WaitRequests(ctx, n); err != nil {
		t.Fatalf("model request %d was never sent: %v", n, err)
	}
}

// within waits for ch, failing the test after 10 s.
func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// goJoined runs fn on a new goroutine and registers its release with t.Cleanup
// right after the start (P-C, tools/harness-gate/README.md "Waiting and releasing
// in tests"): the cleanup calls unblock, which must make fn return (nil when fn
// returns by itself), then joins fn within 10 s. The returned channel is closed
// when fn returns.
func goJoined(t *testing.T, unblock func(), fn func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() {
		if unblock != nil {
			unblock()
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("a released test goroutine did not return within 10s")
		}
	})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

func settled(t *testing.T, a subagent.Activation) subagent.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	r, err := a.Result(ctx)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	return r
}

// TC-11a, DH-D-SUBAGENT-1: the child of component C has C as its one owner,
// and when C unloads the child's driver goroutine has exited, the child has
// left the registry and its result is settled before C's Dispose returns.
func TestChildTornDownWithItsOwner(t *testing.T) {
	e := newEnv(t, fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true})
	cf, c := e.component("C")
	_, d := e.component("D")
	act := must(e.rt.Start(c, e.spec("K")))
	waitRequests(t, e.tr, 1)
	within(t, e.tr.PrefixServed(1), "the streamed prefix landing")

	if o, ok := e.rt.OwnerOf("K"); !ok || o != c {
		t.Fatalf("owner of K: %v %v", o, ok)
	}
	if got := e.rt.Children(c); !slices.Equal(got, []session.ID{"K"}) {
		t.Fatalf("children of C: %v", got)
	}
	if _, err := e.rt.Start(d, e.spec("K")); !errors.Is(err, subagent.ErrNameTaken) {
		t.Fatalf("a second owner for K: %v", err)
	}
	if len(e.rt.Children(d)) != 0 {
		t.Fatal("D gained a child")
	}
	k := act.Agent()

	cf.Dispose()
	must(0, cf.Wait())

	select {
	case <-agent.Done(k):
	default:
		t.Fatal("K's driver goroutine outlived its owner")
	}
	if _, ok := e.reg.Get("K"); ok {
		t.Fatal("K is still addressable")
	}
	if _, ok := e.rt.OwnerOf("K"); ok {
		t.Fatal("K kept an owner")
	}
	ctx, cancel := context.WithTimeout(bg, time.Millisecond)
	defer cancel()
	r, err := act.Result(ctx)
	if err != nil {
		t.Fatalf("the result was not settled with the owner: %v", err)
	}
	if r.Stop != subagent.StopAborted || r.Cleanup != subagent.CleanupOK || r.Diagnostic != "aborted parent" || r.Output != "par" {
		t.Fatalf("result %+v", r)
	}
	if k.State() != agent.StateDisposed {
		t.Fatalf("K is %s", k.State())
	}
}

// A run whose child finishes its task closes by itself with the last answer.
func TestChildCompletesAndCloses(t *testing.T) {
	e := newEnv(t, fakemodel.Reply("done"))
	_, c := e.component("C")
	act := must(e.rt.Start(c, subagent.Spec{Name: "worker", Prompt: "do it", Model: &agent.ModelSelection{Route: route}}))
	if act.Name() != "worker" || act.Child() == "" {
		t.Fatalf("activation %q %q", act.Name(), act.Child())
	}
	r := settled(t, act)
	if r.Stop != subagent.StopCompleted || r.Output != "done" || r.Cleanup != subagent.CleanupOK {
		t.Fatalf("result %+v", r)
	}
	if _, ok := e.reg.Get(act.Child()); ok {
		t.Fatal("a closed run kept its agent")
	}
	if len(e.rt.Children(c)) != 0 {
		t.Fatal("a closed run kept its owner")
	}
}

// Dispose releases one run without its owner.
func TestDisposeReleasesTheRun(t *testing.T) {
	e := newEnv(t, fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true})
	_, c := e.component("C")
	act := must(e.rt.Start(c, e.spec("K")))
	waitRequests(t, e.tr, 1)
	must(0, act.Dispose(bg))
	if r := settled(t, act); r.Stop != subagent.StopAborted {
		t.Fatalf("result %+v", r)
	}
	if c.Lifetime().Err() != nil {
		t.Fatal("releasing a run unloaded its owner")
	}
	if _, ok := e.reg.Get("K"); ok {
		t.Fatal("K is still addressable")
	}
}

func TestStartRefusals(t *testing.T) {
	e := newEnv(t)
	cf, c := e.component("C")
	if _, err := e.rt.Start(c, subagent.Spec{}); !errors.Is(err, subagent.ErrInvalid) {
		t.Fatalf("no prompt: %v", err)
	}
	if _, err := e.rt.Start(nil, e.spec("K")); !errors.Is(err, subagent.ErrInvalid) {
		t.Fatalf("no owner: %v", err)
	}
	cf.Dispose()
	if _, err := e.rt.Start(c, e.spec("K")); !errors.Is(err, subagent.ErrInactive) {
		t.Fatalf("unloaded owner: %v", err)
	}
	if _, ok := e.reg.Get("K"); ok {
		t.Fatal("a refused start created an agent")
	}
}

// WaitQuiet waits for a start still in Setup: it neither skips the reserved
// child nor returns before that child has run its task and gone idle.
func TestWaitQuietWaitsForAPendingStart(t *testing.T) {
	// In a synctest bubble, synctest.Wait shows WaitQuiet still blocked with no
	// settle time: every goroutine is then durably blocked.
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t, fakemodel.Reply("done"))
		_, c := e.component("C")
		entered, release := make(chan struct{}), make(chan struct{})
		free := sync.OnceFunc(func() { close(release) })
		s := e.spec("K")
		s.Setup = func(*cordis.Context, agent.Agent) (func() error, error) {
			close(entered)
			<-release
			return nil, nil
		}
		started := make(chan error, 1)
		goJoined(t, free, func() { _, err := e.rt.Start(c, s); started <- err })
		<-entered
		quiet := make(chan error, 1)
		qctx, stopQuiet := context.WithCancel(bg)
		goJoined(t, func() { free(); stopQuiet() }, func() { quiet <- e.rt.WaitQuiet(qctx, c) })
		synctest.Wait()
		select {
		case err := <-quiet:
			t.Fatalf("WaitQuiet returned during the child's Setup: %v", err)
		default:
		}
		free()
		if err := <-started; err != nil {
			t.Fatalf("start: %v", err)
		}
		select {
		case err := <-quiet:
			if err != nil {
				t.Fatalf("WaitQuiet: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("WaitQuiet never returned")
		}
		if len(e.tr.Requests()) != 1 {
			t.Fatalf("WaitQuiet returned before the child ran: %d requests", len(e.tr.Requests()))
		}
		ctx, cancel := context.WithTimeout(bg, time.Second)
		defer cancel()
		if err := e.rt.WaitQuiet(ctx, c); err != nil {
			t.Fatalf("WaitQuiet with nothing pending: %v", err)
		}
	})
}

// DH-D-SUBAGENT-1: subagent.manager injects the agent registry and provides
// the seam under Key; a component that injects Key delegates through it and
// its child goes with it.
func TestManagerComponentProvidesTheSeam(t *testing.T) {
	e := newEnv(t, fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true})
	must(agent.Agents.Provide(e.root, e.reg))
	mf := must(e.root.Use(subagent.Component, cordis.None{}))
	must(0, mf.Wait())

	var rt subagent.Runtime
	var c *cordis.Context
	cf := must(e.root.Inject([]string{subagent.Key.Name()}, func(ctx *cordis.Context) (err error) {
		c = ctx
		rt, err = subagent.Key.Get(ctx)
		return err
	}))
	must(0, cf.Wait())
	act := must(rt.Start(c, e.spec("K")))
	waitRequests(t, e.tr, 1)
	if o, ok := rt.OwnerOf("K"); !ok || o != c {
		t.Fatalf("owner of K: %v %v", o, ok)
	}
	k := act.Agent()

	cf.Dispose()
	must(0, cf.Wait())
	select {
	case <-agent.Done(k):
	default:
		t.Fatal("K's driver goroutine outlived its owner")
	}
	if _, ok := e.reg.Get("K"); ok {
		t.Fatal("K is still addressable")
	}
	if r := settled(t, act); r.Stop != subagent.StopAborted {
		t.Fatalf("result %+v", r)
	}
}

// Unloading the manager refuses new runs and releases every run it admitted,
// a start still in Setup included, before its unload completes.
func TestManagerUnloadReleasesItsRuns(t *testing.T) {
	// In a synctest bubble, synctest.Wait shows the unload still waiting for the
	// pending start with no settle time.
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t, fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true})
		must(agent.Agents.Provide(e.root, e.reg))
		mf := must(e.root.Use(subagent.Component, cordis.None{}))
		must(0, mf.Wait())
		var rt subagent.Runtime
		g := must(e.root.Inject([]string{subagent.Key.Name()}, func(ctx *cordis.Context) (err error) {
			rt, err = subagent.Key.Get(ctx)
			return err
		}))
		must(0, g.Wait())
		_, c := e.component("C")
		act := must(rt.Start(c, e.spec("K")))
		waitRequests(t, e.tr, 1)

		// P is admitted before the unload and still in Setup when it begins.
		entered, release := make(chan struct{}), make(chan struct{})
		free := sync.OnceFunc(func() { close(release) })
		t.Cleanup(free)
		ps := e.spec("P")
		ps.Setup = func(*cordis.Context, agent.Agent) (func() error, error) {
			close(entered)
			<-release
			return nil, errors.New("setup failed")
		}
		pending := make(chan error, 1)
		goJoined(t, free, func() { _, err := rt.Start(c, ps); pending <- err })
		<-entered

		unloaded := goJoined(t, free, func() { mf.Dispose(); _ = mf.Wait() })
		// Starts racing the unload are either admitted, and then released with
		// it, or refused once it has begun: the first races it, and once the
		// manager signals that it is refusing, the next is refused.
		var admitted []subagent.Activation
		ids := []session.ID{"K", "P"}
		late := func(n int) (subagent.Activation, error) {
			s := e.spec(fmt.Sprintf("late-%d", n))
			// An admitted late start fails in Setup, so it calls no model.
			s.Setup = func(*cordis.Context, agent.Agent) (func() error, error) { return nil, errors.New("late") }
			return rt.Start(c, s)
		}
		if a, err := late(0); err == nil {
			admitted = append(admitted, a)
			ids = append(ids, a.Child())
		}
		within(t, subagent.Refusing(rt), "the manager's refusal of new runs")
		if _, err := late(1); !errors.Is(err, subagent.ErrInactive) {
			t.Fatalf("starts were not refused during the manager's unload: %v", err)
		}
		synctest.Wait()
		select {
		case <-unloaded:
			t.Fatal("the manager unloaded while an admitted start was still pending")
		default:
		}
		free()
		select {
		case <-unloaded:
		case <-time.After(10 * time.Second):
			t.Fatal("the manager never finished unloading")
		}
		for _, id := range ids {
			if _, ok := e.reg.Get(id); ok {
				t.Fatalf("%s outlived the manager", id)
			}
			if _, ok := rt.OwnerOf(id); ok {
				t.Fatalf("%s kept an owner", id)
			}
		}
		if r := settled(t, act); r.Stop != subagent.StopAborted {
			t.Fatalf("result %+v", r)
		}
		for _, a := range admitted {
			settled(t, a)
		}
		select {
		case err := <-pending:
			if err == nil || !strings.Contains(err.Error(), "setup failed") {
				t.Fatalf("pending start: %v", err)
			}
		default:
			t.Fatal("the manager unloaded before the pending start returned")
		}
		if _, err := rt.Start(c, e.spec("after")); !errors.Is(err, subagent.ErrInactive) {
			t.Fatalf("a start after the manager unloaded: %v", err)
		}
		if len(rt.Children(c)) != 0 {
			t.Fatalf("children after the unload: %v", rt.Children(c))
		}
	})
}
