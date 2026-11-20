package cordis_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

type scopeTag struct{}

var (
	agentKey = cordis.NewValueKey[string]("agent")
	fact     = cordis.NewEvent[string]("fact")
	ask      = cordis.NewBail[string, string]("ask")
	refine   = cordis.NewWaterfall[string]("refine")
	guard    = cordis.NewAround[string, verdict]("guard")
)

// verdict is the result type of guard: an around key's result differs from its request.
type verdict struct {
	Text string
	Seen string
}

// counter counts calls per label; listeners run on several goroutines in Parallel.
type counter struct {
	mu    sync.Mutex
	calls map[string]int
}

func (c *counter) hit(label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[label]++
}

func (c *counter) take() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var parts []string
	for _, label := range []string{"A", "B", "untagged", "global"} {
		if n := c.calls[label]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", label, n))
		}
	}
	c.calls = nil
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------- DH-T-CORDIS-5

func TestContextValueIsVisibleToItsSubtreeOnly(t *testing.T) {
	root, _ := Quiet()
	var tagged, untagged, derived, child, sibling any
	var typed string
	var typedOK bool

	var childFiber *cordis.Fiber
	a := MustWait(t, Must(root.Apply("a", func(ctx *cordis.Context) error {
		scoped := ctx.WithValue(scopeTag{}, "A")
		tagged = scoped.Value(scopeTag{})
		untagged = ctx.Value(scopeTag{})
		derived = scoped.Isolate("x").Intercept("y", 1).Value(scopeTag{})
		var err error
		childFiber, err = scoped.Apply("a-child", func(c *cordis.Context) error {
			child = c.Value(scopeTag{})
			return nil
		})
		return err
	})))
	MustWait(t, childFiber)
	True(t, childFiber.Ctx().Within(a) && !a.Ctx().Within(childFiber), "Within follows the subtree")
	MustWait(t, Must(root.Apply("b", func(ctx *cordis.Context) error {
		sibling = ctx.Value(scopeTag{})
		return nil
	})))

	Equal(t, any("A"), tagged, "the tagged context reads its value")
	Equal(t, any("A"), derived, "contexts derived from it read the value")
	Equal(t, any("A"), child, "a fiber loaded from it reads the value")
	Equal(t, nil, untagged, "the parent does not see the value")
	Equal(t, nil, sibling, "a sibling does not see the value")
	Equal(t, nil, root.Value(scopeTag{}), "the root does not see the value")

	outer := agentKey.With(root, "outer")
	inner := agentKey.With(outer.Isolate("z"), "inner")
	typed, typedOK = agentKey.Get(inner)
	True(t, typedOK, "typed key is found")
	Equal(t, "inner", typed, "a nearer value shadows an outer one")
	typed, _ = agentKey.Get(outer)
	Equal(t, "outer", typed)
	_, typedOK = agentKey.Get(root)
	True(t, !typedOK, "typed key is absent from the root")
	_, typedOK = cordis.NewValueKey[string]("agent").Get(inner)
	True(t, !typedOK, "value keys compare by identity, not by name")
	_, typedOK = cordis.NewValueKey[int]("n").Get(root.WithValue(agentKey, "wrong key"))
	True(t, !typedOK, "a missing typed key is not found")

	for name, key := range map[string]any{"nil": nil, "slice": []string{"x"}} {
		func() {
			defer func() { True(t, recover() != nil, name+" key panics") }()
			root.WithValue(key, 1)
		}()
	}
}

// ---------------------------------------------------------- DH-T-CORDIS-6

func TestScopedDispatchReachesOneSubtree(t *testing.T) {
	root, logs := Quiet()
	hits := &counter{}
	register := func(ctx *cordis.Context, label string, opts ...cordis.ListenOption) {
		_ = Must(fact.On(ctx, func(*cordis.Context, string) error { hits.hit(label); return nil }, opts...))
		_ = Must(ask.On(ctx, func(*cordis.Context, string) (string, bool, error) { hits.hit(label); return "", false, nil }, opts...))
		_ = Must(refine.On(ctx, func(_ *cordis.Context, v string, next func(string) (string, error)) (string, error) {
			hits.hit(label)
			return next(v)
		}, opts...))
		_ = Must(guard.On(ctx, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
			hits.hit(label)
			return next(r)
		}, opts...))
	}
	agent := func(name string) *cordis.Fiber {
		return MustWait(t, Must(root.Apply(name, func(ctx *cordis.Context) error {
			tagged := agentKey.With(ctx, name)
			register(tagged, name)
			if name == "B" {
				register(tagged, "global", cordis.Global())
			}
			return nil
		})))
	}
	fiberA, fiberB := agent("A"), agent("B")
	register(root, "untagged")

	verbs := map[string]func(ctx *cordis.Context){
		"Emit":     func(ctx *cordis.Context) { NoError(t, fact.Emit(ctx, "x")) },
		"Notify":   func(ctx *cordis.Context) { fact.Notify(ctx, "x") },
		"Parallel": func(ctx *cordis.Context) { NoError(t, fact.Parallel(ctx, "x")) },
		"Bail":     func(ctx *cordis.Context) { _, _, err := ask.Bail(ctx, "x"); NoError(t, err) },
		"Waterfall": func(ctx *cordis.Context) {
			_, err := refine.Waterfall(ctx, "x", func(v string) (string, error) { return v, nil })
			NoError(t, err)
		},
		"Around": func(ctx *cordis.Context) {
			_, err := guard.Around(ctx, "x", func(string) (verdict, error) { return verdict{}, nil })
			NoError(t, err)
		},
	}
	admitA := func(l *cordis.Context) bool { v, ok := agentKey.Get(l); return !ok || v == "A" }
	for verb, dispatch := range verbs {
		dispatch(root)
		Equal(t, "A=1,B=1,untagged=1,global=1", hits.take(), verb+": an unfiltered dispatch reaches everyone")

		dispatch(root.ScopeTo(fiberA))
		Equal(t, "A=1,global=1", hits.take(), verb+": scoped to A, B's count stays 0")

		dispatch(root.ScopeTo(fiberB))
		Equal(t, "B=1,global=1", hits.take(), verb+": scoped to B")

		dispatch(root.WithFilter(admitA))
		Equal(t, "A=1,untagged=1,global=1", hits.take(), verb+": a value filter admits A and untagged listeners")

		dispatch(root.WithFilter(admitA).Isolate("q").WithFilter(func(l *cordis.Context) bool { return l.Within(fiberB) }))
		Equal(t, "global=1", hits.take(), verb+": nested filters must all admit")
	}
	if err := fact.EmitScoped(root, fiberA, "x"); err != nil {
		t.Fatal(err)
	}
	Equal(t, "A=1,global=1", hits.take(), "EmitScoped")
	fact.NotifyScoped(root, fiberB, "x")
	Equal(t, "B=1,global=1", hits.take(), "NotifyScoped")

	before := Count(logs, cordis.LevelError)
	panicky := root.WithFilter(func(*cordis.Context) bool { panic("filter bug") })
	NoError(t, fact.Emit(panicky, "x"))
	Equal(t, "global=1", hits.take(), "a panicking filter admits nothing but Global listeners")
	Equal(t, before+1, Count(logs, cordis.LevelError), "the filter panic is logged once per dispatch")

	var fromChild string
	MustWait(t, Must(root.ScopeTo(fiberA).Apply("loaded-from-filter", func(ctx *cordis.Context) error {
		NoError(t, fact.Emit(ctx, "x"))
		fromChild = hits.take()
		return nil
	})))
	Equal(t, "A=1,B=1,untagged=1,global=1", fromChild, "a fiber loaded from a filtered context does not inherit the filter")
}

// ---------------------------------------------------------- DH-T-CORDIS-7

func TestNotifyListenerCannotVetoTheFact(t *testing.T) {
	root, logs := Quiet()
	log := &Log{}
	var fifth *cordis.EffectHandle
	listen := func(name string, fn func() error) {
		MustWait(t, Must(root.Apply(name, func(ctx *cordis.Context) error {
			handle, err := fact.On(ctx, func(_ *cordis.Context, v string) error { log.Add(name + ":" + v); return fn() })
			if name == "l5" {
				fifth = handle
			}
			return err
		})))
	}
	listen("l1", func() error { fifth.Dispose(); return nil })
	listen("l2", func() error { panic("boom") })
	listen("l3", func() error { return nil })
	listen("l4", func() error { return errors.New("refused") })
	listen("l5", func() error { return nil })

	Within(t, "Notify returns", func() { fact.Notify(root, "f") })
	Equal(t, "l1:f,l2:f,l3:f,l4:f", log.String(), "every listener after the failures still runs; one removed mid-dispatch is skipped")

	var names, texts []string
	for _, m := range logs.Messages() {
		if m.Level == cordis.LevelError {
			names = append(names, m.Name)
			texts = append(texts, m.Text)
		}
	}
	Equal(t, "l2,l4", strings.Join(names, ","), "each failure is logged once, on its owner's logger")
	True(t, strings.Contains(texts[0], `"fact"`) && strings.Contains(texts[0], "boom"), texts[0])
	True(t, strings.Contains(texts[1], "refused"), texts[1])

	log = &Log{}
	func() {
		defer func() { True(t, recover() != nil, "Emit, unlike Notify, does not contain a panic") }()
		_ = fact.Emit(root, "e")
	}()
	Equal(t, "l1:e,l2:e", log.String(), "Emit stops at the failing listener")
}

// --------------------------------------------------------- DH-T-CORDIS-12

func TestAroundKeyWrapsInRegistrationOrder(t *testing.T) {
	root, _ := Quiet()
	wrap := func(ctx *cordis.Context, in, out string, opts ...cordis.ListenOption) *cordis.EffectHandle {
		return Must(guard.On(ctx, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
			v, err := next(r + in)
			v.Text += out
			return v, err
		}, opts...))
	}
	terminal := func(r string) (verdict, error) { return verdict{Text: "ok:", Seen: r}, nil }

	ha := wrap(root, "a", "A")
	hb := wrap(root, "b", "B")
	got := Must(guard.Around(root, "", terminal))
	Equal(t, "ab", got.Seen, "the terminal sees the request rewritten outermost first")
	Equal(t, "ok:BA", got.Text, "the caller receives the result suffixed BA")

	hc := Must(guard.Prepend(root, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
		return verdict{Text: "denied", Seen: r}, nil
	}))
	got = Must(guard.Around(root, "", func(string) (verdict, error) { t.Fatal("terminal must not run"); return verdict{}, nil }))
	Equal(t, verdict{Text: "denied"}, got, "a listener that does not call next short-circuits")
	hc.Dispose()

	refused := errors.New("refused")
	var outerSaw error
	hd := Must(guard.Prepend(root, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
		v, err := next(r)
		outerSaw = err
		return v, err
	}))
	_, err := guard.Around(root, "", func(string) (verdict, error) { return verdict{}, refused })
	True(t, err == refused, "an error reaches the caller unchanged")
	True(t, outerSaw == refused, "outer listeners see the error as next's result")

	_, err = guard.Around(root, "", func(string) (verdict, error) { panic("terminal bug") })
	var panicked *cordis.PanicError
	True(t, errors.As(err, &panicked), "a terminal panic becomes a PanicError")
	True(t, strings.Contains(err.Error(), "root"), "the terminal panic names the dispatching fiber: "+err.Error())
	True(t, errors.As(outerSaw, &panicked), "outer listeners see the terminal panic as an error")
	hd.Dispose()

	plugin := MustWait(t, Must(root.Apply("policy", func(ctx *cordis.Context) error {
		_, err := guard.On(ctx, func(*cordis.Context, string, func(string) (verdict, error)) (verdict, error) { panic("policy bug") })
		return err
	})))
	_, err = guard.Around(root, "", terminal)
	True(t, errors.As(err, &panicked), "a listener panic becomes a PanicError")
	True(t, strings.Contains(err.Error(), "policy"), "the listener panic names its fiber: "+err.Error())
	plugin.Dispose()
	got = Must(guard.Around(root, "", terminal))
	Equal(t, "ok:BA", got.Text, "an unloaded fiber's listener is gone")

	var twice error
	he := Must(guard.On(root, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
		v, _ := next(r)
		_, twice = next(r)
		return v, nil
	}))
	_ = Must(guard.Around(root, "", terminal))
	True(t, twice != nil, "a second next call fails")
	he.Dispose()

	hb.Dispose()
	var removedRan bool
	hf := Must(guard.On(root, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
		removedRan = true
		return next(r)
	}))
	ha.Dispose()
	hz := Must(guard.Prepend(root, func(_ *cordis.Context, r string, next func(string) (verdict, error)) (verdict, error) {
		hf.Dispose()
		return next(r + "z")
	}))
	got = Must(guard.Around(root, "", terminal))
	Equal(t, "z", got.Seen, "a listener removed mid-dispatch is skipped")
	True(t, !removedRan, "the removed listener did not run")
	hz.Dispose()

	agentA := MustWait(t, Must(root.Apply("A", func(ctx *cordis.Context) error { wrap(ctx, "a", "A"); return nil })))
	MustWait(t, Must(root.Apply("B", func(ctx *cordis.Context) error {
		wrap(ctx, "b", "B")
		wrap(ctx, "g", "G", cordis.Global())
		return nil
	})))
	got = Must(guard.Around(root.ScopeTo(agentA), "", terminal))
	Equal(t, "ag", got.Seen, "a scoped dispatch admits A's listener and the global one only")
	Equal(t, "ok:GA", got.Text)
}
