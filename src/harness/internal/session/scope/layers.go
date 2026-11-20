package scope

import (
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Layers keeps one layer of type L per scope plus a global one, for a Layered
// registry (tools, prompts). A layer is created on first write through a scope's
// context and reclaimed when the scope closes.
type Layers[L any] struct {
	t        *table
	newLayer func() L

	mu     sync.RWMutex
	global L
	byKey  map[Key]L
}

// NewLayers is the layers of one registry over t; newLayer builds an empty layer.
func NewLayers[L any](t Table, newLayer func() L) *Layers[L] {
	return &Layers[L]{t: t.table(), newLayer: newLayer, global: newLayer(), byKey: map[Key]L{}}
}

// Global is the global layer.
func (ls *Layers[L]) Global() L { return ls.global }

// Peek is k's own layer, if it has one.
func (ls *Layers[L]) Peek(k Key) (L, bool) {
	if k.IsZero() {
		return ls.global, true
	}
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	l, ok := ls.byKey[k]
	return l, ok
}

// Chain is the layers visible at k, nearest first: k's own, those of its enclosing
// scopes in their linearised order, then the global layer. A fresh slice.
func (ls *Layers[L]) Chain(k Key) []L {
	keys := append([]Key{k}, ls.t.Enclosing(k)...)
	ls.mu.RLock()
	out := make([]L, 0, len(keys)+1)
	for _, key := range keys {
		if l, ok := ls.byKey[key]; ok && !key.IsZero() {
			out = append(out, l)
		}
	}
	ls.mu.RUnlock()
	return append(out, ls.global)
}

// Add applies mutate to the layer of ctx's scope (global for an untagged context)
// as an effect of ctx's fiber: the undo mutate returns runs when that fiber
// unloads, when the returned handle is disposed, or when the scope closes.
func (ls *Layers[L]) Add(ctx *cordis.Context, label string, mutate func(L) func()) (*cordis.EffectHandle, error) {
	if ctx == nil {
		return nil, errors.New("scope: a context is required")
	}
	k := Of(ctx)
	if !k.IsZero() && k.t != ls.t {
		return nil, errors.New("scope: the context's scope belongs to another table")
	}
	return ctx.Effect(label, func() (cordis.Disposer, error) {
		l, err := ls.layer(k)
		if err != nil {
			return nil, err
		}
		undo := mutate(l)
		return func() error {
			if undo != nil {
				undo()
			}
			return nil
		}, nil
	})
}

func (ls *Layers[L]) layer(k Key) (L, error) {
	if k.IsZero() {
		return ls.global, nil
	}
	if !ls.t.live(k) {
		var zero L
		return zero, &Error{Code: CodeClosed, Msg: k.String() + " is closed"}
	}
	ls.mu.Lock()
	l, ok := ls.byKey[k]
	if !ok {
		l = ls.newLayer()
		ls.byKey[k] = l
	}
	ls.mu.Unlock()
	if !ok {
		ls.t.onClose(k, func() {
			ls.mu.Lock()
			delete(ls.byKey, k)
			ls.mu.Unlock()
		})
	}
	return l, nil
}

// size is the number of scope layers held; for tests.
func (ls *Layers[L]) size() int {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return len(ls.byKey)
}

// Entry is one named value of a merge.
type Entry[V any] struct {
	Name  string
	Value V
}

// Named is a set of named values with its own lock; reads are snapshots.
type Named[V any] struct {
	mu   sync.RWMutex
	gen  uint64
	vals map[string]named[V]
}

type named[V any] struct {
	gen uint64
	v   V
}

// NewNamed is an empty set.
func NewNamed[V any]() *Named[V] { return &Named[V]{vals: map[string]named[V]{}} }

// Set binds name to v and returns the undo, which removes the binding only if it
// is still this one.
func (n *Named[V]) Set(name string, v V) func() {
	n.mu.Lock()
	n.gen++
	g := n.gen
	n.vals[name] = named[V]{gen: g, v: v}
	n.mu.Unlock()
	return func() {
		n.mu.Lock()
		if cur, ok := n.vals[name]; ok && cur.gen == g {
			delete(n.vals, name)
		}
		n.mu.Unlock()
	}
}

// Get is the value bound to name.
func (n *Named[V]) Get(name string) (V, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	e, ok := n.vals[name]
	return e.v, ok
}

// List is every binding, sorted by name; a fresh slice.
func (n *Named[V]) List() []Entry[V] {
	n.mu.RLock()
	out := make([]Entry[V], 0, len(n.vals))
	for name, e := range n.vals {
		out = append(out, Entry[V]{Name: name, Value: e.v})
	}
	n.mu.RUnlock()
	slices.SortFunc(out, func(a, b Entry[V]) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Merged is the named values visible at k: walking Chain(k) nearest first, the
// first layer to bind a name wins. Sorted by name; a fresh slice.
func Merged[L any, V any](ls *Layers[L], k Key, pick func(L) *Named[V]) []Entry[V] {
	seen := map[string]bool{}
	var out []Entry[V]
	for _, l := range ls.Chain(k) {
		n := pick(l)
		if n == nil {
			continue
		}
		for _, e := range n.List() {
			if !seen[e.Name] {
				seen[e.Name] = true
				out = append(out, e)
			}
		}
	}
	slices.SortFunc(out, func(a, b Entry[V]) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Event is an event about one scope. It is dispatched only with a Key: listeners
// registered through an untagged context, through that scope or an enclosing one,
// or with cordis.Global(), are called.
type Event[A any] struct{ k *cordis.EventKey[A] }

// NewEvent declares a scoped event.
func NewEvent[A any](name string) *Event[A] { return &Event[A]{k: cordis.NewEvent[A](name)} }

// Name is the event's name.
func (e *Event[A]) Name() string { return e.k.Name() }

// On registers listener as an effect of ctx's fiber.
func (e *Event[A]) On(ctx *cordis.Context, listener func(ctx *cordis.Context, args A) error, opts ...cordis.ListenOption) (*cordis.EffectHandle, error) {
	return e.k.On(ctx, listener, opts...)
}

// Notify calls the admitted listeners; failures are contained.
func (e *Event[A]) Notify(ctx *cordis.Context, k Key, args A) {
	e.k.Notify(ctx.WithFilter(admit(k)), args)
}

// Emit calls the admitted listeners in order; the first error stops it.
func (e *Event[A]) Emit(ctx *cordis.Context, k Key, args A) error {
	return e.k.Emit(ctx.WithFilter(admit(k)), args)
}

func admit(k Key) func(*cordis.Context) bool {
	return func(listener *cordis.Context) bool { return visible(Of(listener), k) }
}
