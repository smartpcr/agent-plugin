package subagent_test

import (
	"context"
	"errors"
	"slices"
	"testing"
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
	time.Sleep(200 * time.Millisecond) // the streamed prefix lands

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
