package scope

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

func newRoot(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(r.Dispose)
	return r
}

// mount loads Component and returns its table.
func mount(t *testing.T, root *cordis.Context) Table {
	t.Helper()
	f, err := root.Use(Component, cordis.None{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	var tab Table
	g, err := root.Inject([]string{TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		tab, err = TableKey.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	return tab
}

type layer struct{ tools *Named[string] }

func newLayer() layer { return layer{tools: NewNamed[string]()} }

func tools(l layer) *Named[string] { return l.tools }

func names(es []Entry[string]) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name+"="+e.Value)
	}
	return out
}

func set(t *testing.T, ls *Layers[layer], ctx *cordis.Context, name, v string) *cordis.EffectHandle {
	t.Helper()
	h, err := ls.Add(ctx, "tool "+name, func(l layer) func() { return l.tools.Set(name, v) })
	if err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
	return h
}

// DH-T-SESSION-17: visibility, nearest-wins, order, cycles, Close and reclaim.
func TestScopedRegistrations(t *testing.T) {
	root := newRoot(t)
	tab := mount(t, root)
	ls := NewLayers(tab, newLayer)

	preset, err := tab.New(root, "preset")
	if err != nil {
		t.Fatal(err)
	}
	other, err := tab.New(root, "other")
	if err != nil {
		t.Fatal(err)
	}
	a, err := tab.New(root, "agent-a", preset.Key(), other.Key())
	if err != nil {
		t.Fatal(err)
	}
	b, err := tab.New(root, "agent-b")
	if err != nil {
		t.Fatal(err)
	}
	if Of(a.Ctx()) != a.Key() || !Of(root).IsZero() {
		t.Fatal("Of does not read the tag")
	}

	set(t, ls, root, "read", "global")
	set(t, ls, root, "grep", "global")
	set(t, ls, preset.Ctx(), "grep", "preset")
	set(t, ls, preset.Ctx(), "edit", "preset")
	set(t, ls, other.Ctx(), "edit", "other")
	set(t, ls, other.Ctx(), "lint", "other")
	set(t, ls, a.Ctx(), "only-a", "a")
	set(t, ls, b.Ctx(), "only-b", "b")

	// Nearest wins; two unrelated enclosing scopes apply in the given order.
	got := names(Merged(ls, a.Key(), tools))
	want := []string{"edit=preset", "grep=preset", "lint=other", "only-a=a", "read=global"}
	if !slices.Equal(got, want) {
		t.Fatalf("Merged(a) = %v, want %v", got, want)
	}
	if got := names(Merged(ls, b.Key(), tools)); !slices.Equal(got, []string{"grep=global", "only-b=b", "read=global"}) {
		t.Fatalf("Merged(b) = %v", got)
	}
	if got := names(Merged(ls, Key{}, tools)); !slices.Equal(got, []string{"grep=global", "read=global"}) {
		t.Fatalf("Merged(global) = %v", got)
	}
	if err := a.SetEnclosing(other.Key(), preset.Key()); err != nil {
		t.Fatal(err)
	}
	if got := names(Merged(ls, a.Key(), tools)); !slices.Contains(got, "edit=other") {
		t.Fatalf("reordered Merged(a) = %v", got)
	}

	// A cycle is refused and the list is unchanged.
	if err := preset.SetEnclosing(a.Key()); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle = %v", err)
	}
	if err := a.SetEnclosing(a.Key()); !errors.Is(err, ErrCycle) {
		t.Fatalf("self cycle = %v", err)
	}
	if got := tab.Enclosing(a.Key()); !slices.Equal(got, []Key{other.Key(), preset.Key()}) {
		t.Fatalf("Enclosing(a) = %v", got)
	}

	// Close is idempotent under concurrent callers; the layer is reclaimed and
	// the entries are gone.
	if ls.size() != 4 {
		t.Fatalf("layers = %d, want 4", ls.size())
	}
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait) // P-C: nothing gates Close; the cleanup joins on every exit path
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = preset.Close() }()
	}
	wg.Wait()
	if !preset.Closed() || ls.size() != 3 {
		t.Fatalf("closed=%v layers=%d", preset.Closed(), ls.size())
	}
	if _, ok := ls.Peek(preset.Key()); ok {
		t.Fatal("closed scope keeps a layer")
	}
	if got := names(Merged(ls, a.Key(), tools)); !slices.Equal(got, []string{"edit=other", "grep=global", "lint=other", "only-a=a", "read=global"}) {
		t.Fatalf("Merged(a) after close = %v", got)
	}
	if _, err := ls.Add(preset.Ctx(), "late", func(l layer) func() { return l.tools.Set("x", "y") }); err == nil {
		t.Fatal("Add through a closed scope succeeded")
	}
	if err := a.SetEnclosing(preset.Key()); !errors.Is(err, ErrClosed) {
		t.Fatalf("enclose closed = %v", err)
	}
	if _, err := tab.New(root, "late", preset.Key()); !errors.Is(err, ErrClosed) {
		t.Fatalf("New enclosing closed = %v", err)
	}

	// Unloading the owner closes the scope.
	owner, err := root.Apply("owner", func(*cordis.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	c, err := tab.New(owner.Ctx(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	set(t, ls, c.Ctx(), "owned", "c")
	owner.Dispose()
	if !c.Closed() {
		t.Fatal("disposing the owner did not close the scope")
	}
	if _, ok := ls.Peek(c.Key()); ok {
		t.Fatal("owner-closed scope keeps a layer")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// An explicit handle dispose undoes one entry.
	h := set(t, ls, b.Ctx(), "tmp", "b")
	h.Dispose()
	if _, ok := mustPeek(t, ls, b.Key()).tools.Get("tmp"); ok {
		t.Fatal("disposed entry is still bound")
	}
}

func mustPeek(t *testing.T, ls *Layers[layer], k Key) layer {
	t.Helper()
	l, ok := ls.Peek(k)
	if !ok {
		t.Fatalf("no layer for %s", k)
	}
	return l
}

// DH-T-SESSION-6 (scope clauses): a scoped event reaches the scope's listeners,
// enclosing and untagged ones and global ones, not another scope's.
func TestScopedEvent(t *testing.T) {
	root := newRoot(t)
	tab := mount(t, root)
	ev := NewEvent[string]("test.committed")
	preset, _ := tab.New(root, "preset")
	a, _ := tab.New(root, "a", preset.Key())
	b, _ := tab.New(root, "b")

	var mu sync.Mutex
	var heard []string
	listen := func(ctx *cordis.Context, who string, opts ...cordis.ListenOption) {
		if _, err := ev.On(ctx, func(_ *cordis.Context, s string) error {
			mu.Lock()
			heard = append(heard, who+":"+s)
			mu.Unlock()
			return nil
		}, opts...); err != nil {
			t.Fatal(err)
		}
	}
	listen(root, "untagged")
	listen(preset.Ctx(), "preset")
	listen(a.Ctx(), "a")
	listen(b.Ctx(), "b")
	listen(b.Ctx(), "b-global", cordis.Global())

	if err := ev.Emit(root, a.Key(), "x"); err != nil {
		t.Fatal(err)
	}
	slices.Sort(heard)
	if want := []string{"a:x", "b-global:x", "preset:x", "untagged:x"}; !slices.Equal(heard, want) {
		t.Fatalf("heard %v, want %v", heard, want)
	}
}

type spine struct{}

// DH-T-SESSION-18: a scope context reaches no service; a component mounted under
// it resolves exactly what it declares; CheckMount reports a Shared registry.
func TestNoBorrowedAuthority(t *testing.T) {
	root := newRoot(t)
	spineKey := cordis.NewServiceKey[*spine]("test.spine")
	registry := cordis.NewServiceKey[*spine]("test.registry")
	providers := cordis.Define("test.providers", func(ctx *cordis.Context, _ cordis.None) error {
		if _, err := spineKey.Provide(ctx, &spine{}); err != nil {
			return err
		}
		_, err := registry.Provide(ctx, &spine{})
		return err
	})
	if f, err := root.Use(providers, cordis.None{}); err != nil || f.Wait() != nil {
		t.Fatalf("providers: %v", err)
	}
	tab := mount(t, root)

	// The driver injects the spine; it owns the scope but is not its parent.
	var s *Scope
	driver := cordis.Define("test.driver", func(ctx *cordis.Context, _ cordis.None) error {
		if _, err := spineKey.Get(ctx); err != nil {
			return err
		}
		var err error
		s, err = tab.New(ctx, "agent")
		return err
	}, cordis.WithInject(spineKey.Name()))
	if f, err := root.Use(driver, cordis.None{}); err != nil || f.Wait() != nil {
		t.Fatalf("driver: %v / %v", err, f.Wait())
	}

	for _, k := range []cordis.ServiceKey[*spine]{spineKey, registry} {
		if _, err := k.Get(s.Ctx()); !errors.Is(err, cordis.ErrNotInjected) {
			t.Fatalf("%s through Scope.Ctx() = %v, want ErrNotInjected", k.Name(), err)
		}
	}
	if _, err := TableKey.Get(s.Ctx()); !errors.Is(err, cordis.ErrNotInjected) {
		t.Fatalf("table through Scope.Ctx() = %v", err)
	}

	var gotRegistry, gotSpine error
	tool := cordis.Define("test.tool", func(ctx *cordis.Context, _ cordis.None) error {
		_, gotRegistry = registry.Get(ctx)
		_, gotSpine = spineKey.Get(ctx)
		return nil
	}, cordis.WithInject(registry.Name()))
	f, err := s.Ctx().Use(tool, cordis.None{})
	if err != nil || f.Wait() != nil {
		t.Fatalf("tool: %v", err)
	}
	if gotRegistry != nil || !errors.Is(gotSpine, cordis.ErrNotInjected) {
		t.Fatalf("mounted component: registry=%v spine=%v", gotRegistry, gotSpine)
	}

	tab.Declare(registry.Name(), Shared)
	tab.Declare("test.tools", Layered)
	d := tab.CheckMount([]string{"test.tools", registry.Name()})
	if len(d) != 1 || d[0].Service != registry.Name() || d[0].Code != CodeSharedRegistry {
		t.Fatalf("CheckMount = %+v", d)
	}
	if d := tab.CheckMount([]string{"test.tools"}); len(d) != 0 {
		t.Fatalf("CheckMount(layered) = %+v", d)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Ctx().Lifetime().Err() == nil || f.Lifetime().Err() == nil {
		t.Fatal("the scope or its mounted component survived Close")
	}
}
