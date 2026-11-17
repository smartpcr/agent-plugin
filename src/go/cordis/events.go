package cordis

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// hook is one registered listener.
type hook struct {
	ctx     *Context
	fn      any
	removed atomic.Bool
}

// eventHub holds the listeners of one tree, keyed by event key identity.
type eventHub struct {
	t     *tree
	hooks map[any][]*hook // guarded by the tree lock
}

// register adds a listener as an effect of ctx's fiber: it is removed when the fiber
// unloads or the handle is disposed.
func (e *eventHub) register(ctx *Context, key any, name string, fn any, prepend bool) (*EffectHandle, error) {
	return ctx.Effect(fmt.Sprintf("ctx.On(%q)", name), func() (Disposer, error) {
		h := &hook{ctx: ctx, fn: fn}
		e.t.lock()
		if prepend {
			e.hooks[key] = slices.Insert(e.hooks[key], 0, h)
		} else {
			e.hooks[key] = append(e.hooks[key], h)
		}
		e.t.unlock()
		return func() error {
			h.removed.Store(true)
			e.t.lock()
			list := e.hooks[key]
			if i := slices.Index(list, h); i >= 0 {
				list = slices.Delete(list, i, i+1)
				if len(list) == 0 {
					delete(e.hooks, key)
				} else {
					e.hooks[key] = list
				}
			}
			e.t.unlock()
			return nil
		}, nil
	})
}

// snapshot returns the listeners of key at dispatch time. A listener removed during
// the dispatch is skipped when its turn comes.
func (e *eventHub) snapshot(key any) []*hook {
	e.t.lock()
	defer e.t.unlockQuiet()
	return slices.Clone(e.hooks[key])
}

// once wraps register so the listener runs at most once and then removes itself.
func once[F any](register func(F) (*EffectHandle, error), wrap func(guard func() bool, done func()) F) (*EffectHandle, error) {
	var fired atomic.Bool
	var handle atomic.Pointer[EffectHandle]
	guard := func() bool { return fired.CompareAndSwap(false, true) }
	done := func() {
		if h := handle.Load(); h != nil {
			h.Dispose()
		}
	}
	h, err := register(wrap(guard, done))
	if err != nil {
		return nil, err
	}
	handle.Store(h)
	if fired.Load() {
		h.Dispose()
	}
	return h, nil
}

// --------------------------------------------------------------------- emit

// EventKey identifies a notification event whose listeners take an A. Keys compare by
// identity; define each once, typically as a package-level variable.
type EventKey[A any] struct {
	name string
}

// NewEvent returns a new event key.
func NewEvent[A any](name string) *EventKey[A] { return &EventKey[A]{name: name} }

// Name returns the event name.
func (k *EventKey[A]) Name() string { return k.name }

// On registers a listener, owned by ctx's fiber. The listener receives its own context.
func (k *EventKey[A]) On(ctx *Context, listener func(ctx *Context, args A) error) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, false)
}

// Prepend registers a listener that runs before those already registered.
func (k *EventKey[A]) Prepend(ctx *Context, listener func(ctx *Context, args A) error) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, true)
}

// Once registers a listener that runs on the next event only.
func (k *EventKey[A]) Once(ctx *Context, listener func(ctx *Context, args A) error) (*EffectHandle, error) {
	return once(
		func(fn func(*Context, A) error) (*EffectHandle, error) { return k.On(ctx, fn) },
		func(guard func() bool, done func()) func(*Context, A) error {
			return func(c *Context, args A) error {
				if !guard() {
					return nil
				}
				done()
				return listener(c, args)
			}
		})
}

// Emit calls the listeners one after another and returns the first error, which stops
// the dispatch.
func (k *EventKey[A]) Emit(ctx *Context, args A) error {
	for _, h := range ctx.t.events.snapshot(k) {
		if h.removed.Load() {
			continue
		}
		if err := h.fn.(func(*Context, A) error)(h.ctx, args); err != nil {
			return err
		}
	}
	return nil
}

// Parallel calls every listener on its own goroutine and waits for all of them. The
// failures, panics included, are returned as one AggregateError naming the event.
func (k *EventKey[A]) Parallel(ctx *Context, args A) error {
	hooks := ctx.t.events.snapshot(k)
	errs := make([]error, len(hooks))
	var wg sync.WaitGroup
	for i, h := range hooks {
		if h.removed.Load() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = protect(func() error { return h.fn.(func(*Context, A) error)(h.ctx, args) })
		}()
	}
	wg.Wait()
	var failed []error
	for _, err := range errs {
		if err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &AggregateError{
		Message: fmt.Sprintf("one or more listeners of event %q failed", k.name),
		Errors:  failed,
	}
}

// --------------------------------------------------------------------- bail

// BailKey identifies an event whose listeners are asked in turn until one answers.
type BailKey[A, R any] struct {
	name string
}

// NewBail returns a new bail event key.
func NewBail[A, R any](name string) *BailKey[A, R] { return &BailKey[A, R]{name: name} }

// Name returns the event name.
func (k *BailKey[A, R]) Name() string { return k.name }

// On registers a listener. It answers by returning ok = true.
func (k *BailKey[A, R]) On(ctx *Context, listener func(ctx *Context, args A) (result R, ok bool, err error)) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, false)
}

// Bail asks the listeners in order and returns the first answer. ok is false when no
// listener answered; an error stops the dispatch.
func (k *BailKey[A, R]) Bail(ctx *Context, args A) (result R, ok bool, err error) {
	for _, h := range ctx.t.events.snapshot(k) {
		if h.removed.Load() {
			continue
		}
		result, ok, err = h.fn.(func(*Context, A) (R, bool, error))(h.ctx, args)
		if err != nil || ok {
			return result, ok, err
		}
	}
	var zero R
	return zero, false, nil
}

// ---------------------------------------------------------------- waterfall

// WaterfallKey identifies a middleware event: each listener receives the value and a
// next function, and decides what to pass on and what to return.
type WaterfallKey[A any] struct {
	name string
}

// NewWaterfall returns a new waterfall event key.
func NewWaterfall[A any](name string) *WaterfallKey[A] { return &WaterfallKey[A]{name: name} }

// Name returns the event name.
func (k *WaterfallKey[A]) Name() string { return k.name }

// On registers a middleware listener.
func (k *WaterfallKey[A]) On(ctx *Context, listener func(ctx *Context, value A, next func(A) (A, error)) (A, error)) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, false)
}

// Waterfall runs the listeners as a middleware chain around terminal. A listener that
// calls next more than once gets an error from the second call.
func (k *WaterfallKey[A]) Waterfall(ctx *Context, value A, terminal func(A) (A, error)) (A, error) {
	hooks := ctx.t.events.snapshot(k)
	index := 0
	var dispatch func(A) (A, error)
	dispatch = func(value A) (A, error) {
		for index < len(hooks) && hooks[index].removed.Load() {
			index++
		}
		if index >= len(hooks) {
			return terminal(value)
		}
		h := hooks[index]
		index++
		called := false
		return h.fn.(func(*Context, A, func(A) (A, error)) (A, error))(h.ctx, value, func(next A) (A, error) {
			if called {
				var zero A
				return zero, fmt.Errorf("next() called multiple times")
			}
			called = true
			return dispatch(next)
		})
	}
	return dispatch(value)
}
