package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/internal/body"
)

type echoIn struct {
	Text string `json:"text" dsh:"required"`
}

type echoOut struct {
	Text string `json:"text"`
}

func echo(desc string) *tools.Declaration {
	return tools.Define(tools.Spec[echoIn, echoOut]{
		Name: "echo", Description: desc,
		Run: func(_ context.Context, in echoIn, _ tools.Run) (echoOut, error) { return echoOut(in), nil },
	})
}

func named(name string) *tools.Declaration {
	return tools.Define(tools.Spec[echoIn, echoOut]{
		Name: name, Description: "Tool " + name + ".",
		Run: func(_ context.Context, in echoIn, _ tools.Run) (echoOut, error) { return echoOut(in), nil },
	})
}

type env struct {
	root *cordis.Context
	tab  scope.Table
	svc  tools.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		f, err := root.Use(c, cordis.None{})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Wait(); err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
	}
	e := &env{root: root}
	g, err := root.Inject([]string{scope.TableKey.Name(), tools.Key.Name()}, func(c *cordis.Context) error {
		var err error
		if e.tab, err = scope.TableKey.Get(c); err != nil {
			return err
		}
		e.svc, err = tools.Key.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) scope(t *testing.T, label string, enclosing ...scope.Key) *scope.Scope {
	t.Helper()
	s, err := e.tab.New(e.root, label, enclosing...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) register(t *testing.T, ctx *cordis.Context, d *tools.Declaration) *cordis.EffectHandle {
	t.Helper()
	h, err := e.svc.Register(ctx, d)
	if err != nil {
		t.Fatalf("Register %s: %v", d.Name(), err)
	}
	return h
}

func (e *env) resolve(t *testing.T, k scope.Key) tools.StepView {
	t.Helper()
	v, err := e.svc.Resolve(context.Background(), tools.Target{Scope: k})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return v
}

func names(v tools.StepView) []string {
	var out []string
	for _, d := range v.Tools() {
		out = append(out, d.Name())
	}
	return out
}

func lookup(t *testing.T, v tools.StepView, name string) *tools.Declaration {
	t.Helper()
	d, ok := v.Lookup(name)
	if !ok {
		t.Fatalf("%s not in the view %v", name, names(v))
	}
	return d
}

// DH-T-TOOLS-2, TC-10a: a duplicate name in one scope is refused and the first
// remains; registering it in a child scope shadows only that subtree.
func TestDuplicateRefusedInScope(t *testing.T) {
	e := newEnv(t)
	parent := e.scope(t, "parent")
	child := e.scope(t, "child", parent.Key())
	sibling := e.scope(t, "sibling", parent.Key())

	first, second, shadow := echo("First echo."), echo("Second echo."), echo("Child echo.")
	e.register(t, parent.Ctx(), first)
	h, err := e.svc.Register(parent.Ctx(), second)
	if !errors.Is(err, tools.ErrDuplicate) || h != nil {
		t.Fatalf("second echo in one scope: %v, %v; want tools/duplicate", h, err)
	}
	if got := lookup(t, e.resolve(t, parent.Key()), "echo"); got != first {
		t.Fatalf("after the refusal the parent sees %q", got.Description())
	}

	e.register(t, child.Ctx(), shadow)
	if got := lookup(t, e.resolve(t, child.Key()), "echo"); got != shadow {
		t.Fatalf("child sees %q, want the shadow", got.Description())
	}
	for _, k := range []scope.Key{parent.Key(), sibling.Key()} {
		if got := lookup(t, e.resolve(t, k), "echo"); got != first {
			t.Fatalf("%s sees %q, want the first", k, got.Description())
		}
	}

	// The same rule holds in the global layer, and a scoped tool shadows it.
	global := echo("Global echo.")
	e.register(t, e.root, global)
	if _, err := e.svc.Register(e.root, echo("Another global echo.")); !errors.Is(err, tools.ErrDuplicate) {
		t.Fatalf("second global echo: %v", err)
	}
	if got := lookup(t, e.resolve(t, scope.Key{}), "echo"); got != global {
		t.Fatalf("global view sees %q", got.Description())
	}
	if got := lookup(t, e.resolve(t, parent.Key()), "echo"); got != first {
		t.Fatalf("parent sees %q, want its own over the global one", got.Description())
	}

	// A set is all or nothing, and names one tool once.
	if _, err := e.svc.RegisterSet(parent.Ctx(), []*tools.Declaration{named("grep"), echo("x.")}); !errors.Is(err, tools.ErrDuplicate) {
		t.Fatalf("set with a taken name: %v", err)
	}
	if _, ok := e.resolve(t, parent.Key()).Lookup("grep"); ok {
		t.Fatal("a refused set left grep behind")
	}
	if _, err := e.svc.RegisterSet(parent.Ctx(), []*tools.Declaration{named("grep"), named("grep")}); !errors.Is(err, tools.ErrDuplicate) {
		t.Fatalf("set naming grep twice: %v", err)
	}
}

// DH-T-TOOLS-2: restrictions intersect and spare the scope's own tools; a tool
// registered through one agent's scope is invisible to another and gone after
// disposal; a failing When fails Resolve and publishes no partial view.
func TestScopedViews(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"read", "grep", "edit", "shell"} {
		e.register(t, e.root, named(n))
	}
	preset := e.scope(t, "preset")
	a := e.scope(t, "agent-a", preset.Key())
	b := e.scope(t, "agent-b")

	if _, err := e.svc.Restrict(e.root, tools.Filter{Deny: []string{"shell"}}); !errors.Is(err, tools.ErrUnscoped) {
		t.Fatalf("Restrict through an untagged context: %v", err)
	}
	if _, err := e.svc.Restrict(preset.Ctx(), tools.Filter{Allow: []string{"read", "grep", "edit"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Restrict(a.Ctx(), tools.Filter{Deny: []string{"edit"}}); err != nil {
		t.Fatal(err)
	}
	// The preset's own tool passes its own allow list; agent a's deny applies.
	e.register(t, preset.Ctx(), named("lint"))
	own := e.register(t, a.Ctx(), named("edit"))
	e.register(t, a.Ctx(), named("only-a"))

	if got, want := names(e.resolve(t, a.Key())), []string{"edit", "grep", "lint", "only-a", "read"}; !slices.Equal(got, want) {
		t.Fatalf("agent a sees %v, want %v (own edit spared, shell filtered)", got, want)
	}
	if got, want := names(e.resolve(t, preset.Key())), []string{"edit", "grep", "lint", "read"}; !slices.Equal(got, want) {
		t.Fatalf("preset sees %v, want %v", got, want)
	}
	if got, want := names(e.resolve(t, b.Key())), []string{"edit", "grep", "read", "shell"}; !slices.Equal(got, want) {
		t.Fatalf("agent b sees %v, want %v", got, want)
	}

	// Disposing a's own edit leaves the inherited one, which a's deny filters.
	own.Dispose()
	if got, want := names(e.resolve(t, a.Key())), []string{"grep", "lint", "only-a", "read"}; !slices.Equal(got, want) {
		t.Fatalf("after disposal agent a sees %v, want %v", got, want)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range e.svc.Catalogue() {
		if entry.Name == "only-a" {
			t.Fatal("a closed scope's tool is still in the catalogue")
		}
	}

	// When: false hides, an error fails the whole view.
	var calls int
	gated := tools.Define(tools.Spec[echoIn, echoOut]{
		Name: "gated", Description: "Gated.",
		Run:  func(context.Context, echoIn, tools.Run) (echoOut, error) { return echoOut{}, nil },
		When: func(tools.Target) (bool, error) { calls++; return calls > 1, nil },
	})
	e.register(t, b.Ctx(), gated)
	if _, ok := e.resolve(t, b.Key()).Lookup("gated"); ok {
		t.Fatal("When false still shows the tool")
	}
	if _, ok := e.resolve(t, b.Key()).Lookup("gated"); !ok {
		t.Fatal("When true hides the tool")
	}
	failing := tools.Define(tools.Spec[echoIn, echoOut]{
		Name: "failing", Description: "Failing.",
		Run:  func(context.Context, echoIn, tools.Run) (echoOut, error) { return echoOut{}, nil },
		When: func(tools.Target) (bool, error) { panic("boom") },
	})
	e.register(t, b.Ctx(), failing)
	if v, err := e.svc.Resolve(context.Background(), tools.Target{Scope: b.Key()}); !errors.Is(err, tools.ErrActivationFailed) || v != nil {
		t.Fatalf("failing When: %v, %v; want tools/activation-failed and no view", v, err)
	}
}

// DH-T-TOOLS-1: the catalogue lists each tool once; a declaration with an error is
// refused at registration.
func TestCatalogueAndRefusals(t *testing.T) {
	e := newEnv(t)
	s := e.scope(t, "s")
	e.register(t, e.root, echo("Global echo."))
	e.register(t, s.Ctx(), echo("Scoped echo."))
	e.register(t, s.Ctx(), named("read"))
	cat := e.svc.Catalogue()
	if len(cat) != 2 || cat[0].Name != "echo" || cat[1].Name != "read" {
		t.Fatalf("catalogue %+v", cat)
	}
	if cat[0].Description != "Global echo." || cat[0].Fingerprint == "" || len(cat[0].Parameters) == 0 {
		t.Fatalf("echo entry %+v: want the first registration, with schema", cat[0])
	}

	bad := tools.Define(tools.Spec[echoIn, echoOut]{Name: "bad name", Description: "Bad."})
	if _, err := e.svc.Register(e.root, bad); !errors.Is(err, tools.ErrInvalidDeclaration) {
		t.Fatalf("Register(bad) = %v", err)
	}
	if _, err := e.svc.Register(e.root, nil); !errors.Is(err, tools.ErrInvalidDeclaration) {
		t.Fatalf("Register(nil) = %v", err)
	}
}

// A view is immutable: later registrations do not change it, and Release ends
// lookups. Registration changes are notified through the changed scope.
func TestViewIsASnapshotAndChangesAreNotified(t *testing.T) {
	e := newEnv(t)
	s := e.scope(t, "s")
	var seen []scope.Key
	if _, err := tools.Changed.On(e.root, func(_ *cordis.Context, a tools.ChangedArgs) error {
		seen = append(seen, a.Scope)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v := e.resolve(t, s.Key())
	h := e.register(t, s.Ctx(), named("late"))
	if len(v.Tools()) != 0 {
		t.Fatalf("a published view changed: %v", names(v))
	}
	h.Dispose()
	if len(seen) != 2 || seen[0] != s.Key() || seen[1] != s.Key() {
		t.Fatalf("Changed saw %v, want the scope twice", seen)
	}
	e.register(t, s.Ctx(), named("kept"))
	v = e.resolve(t, s.Key())
	lookup(t, v, "kept")
	v.Release()
	v.Release()
	if _, ok := v.Lookup("kept"); ok {
		t.Fatal("Lookup after Release")
	}
}

// The body is reached through the internal seat only: arguments are decoded
// strictly, with every violation, before the body runs.
func TestBodySeat(t *testing.T) {
	d := echo("Echo.")
	b, ok := body.Of(d)
	if !ok {
		t.Fatal("no body for a good declaration")
	}
	if _, err := b.Decode([]byte(`{"text":1,"extra":true}`)); !errors.Is(err, tools.ErrInvalidArguments) || len(err.(*tools.Error).Issues) != 2 {
		t.Fatalf("Decode(bad) = %v; want both violations", err)
	}
	in, err := b.Decode([]byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := b.Run(context.Background(), in, tools.Run{CallID: "c1"})
	if err != nil || out.(echoOut).Text != "hi" {
		t.Fatalf("Run = %v, %v", out, err)
	}
	if _, ok := body.Of(tools.Define(tools.Spec[echoIn, echoOut]{Name: "nobody", Description: "No body."})); ok {
		t.Fatal("a refused declaration has a body")
	}
}

// A zero-value declaration has no Err of its own but no schema or body either: it
// is refused as invalid, alone or in a set, and the registry is left unchanged.
func TestZeroValueDeclarationRefused(t *testing.T) {
	e := newEnv(t)
	if err := (&tools.Declaration{}).Err(); !errors.Is(err, tools.ErrInvalidDeclaration) {
		t.Fatalf("Err of a zero value = %v; want tools/invalid-declaration", err)
	}
	if h, err := e.svc.Register(e.root, &tools.Declaration{}); !errors.Is(err, tools.ErrInvalidDeclaration) || h != nil {
		t.Fatalf("Register zero value: %v, %v", h, err)
	}
	if h, err := e.svc.RegisterSet(e.root, []*tools.Declaration{named("grep"), {}}); !errors.Is(err, tools.ErrInvalidDeclaration) || h != nil {
		t.Fatalf("RegisterSet with a zero value: %v, %v", h, err)
	}
	if got := names(e.resolve(t, scope.Key{})); len(got) != 0 {
		t.Fatalf("view after refusals: %v", got)
	}
	if got := e.svc.Catalogue(); len(got) != 0 {
		t.Fatalf("catalogue after refusals: %v", got)
	}
}

// RegisterSet keeps its own copy of the set: a caller changing its slice after a
// registration cannot change what disposal removes.
func TestRegisterSetSnapshotsInput(t *testing.T) {
	for _, replace := range []*tools.Declaration{named("other"), nil} {
		e := newEnv(t)
		d := echo("Echo.")
		ds := []*tools.Declaration{d}
		h := must(t)(e.svc.RegisterSet(e.root, ds))
		ds[0] = replace
		h.Dispose()
		if _, ok := e.resolve(t, scope.Key{}).Lookup("echo"); ok {
			t.Fatalf("echo still registered after disposal (slice entry replaced by %v)", replace)
		}
		if got := e.svc.Catalogue(); len(got) != 0 {
			t.Fatalf("catalogue after disposal: %v", got)
		}
		e.register(t, e.root, d)
	}
}

func must(t *testing.T) func(*cordis.EffectHandle, error) *cordis.EffectHandle {
	return func(h *cordis.EffectHandle, err error) *cordis.EffectHandle {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
}
