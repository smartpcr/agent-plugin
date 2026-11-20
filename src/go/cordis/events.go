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
	global  bool
	removed atomic.Bool
}

// ListenOption changes how a listener is registered.
type ListenOption func(*listenOptions)

type listenOptions struct {
	global bool
}

// Global registers a listener that every dispatch reaches, whatever filter the
// dispatching context carries (see Context.WithFilter): k.On(ctx, fn, cordis.Global()).
func Global() ListenOption { return func(o *listenOptions) { o.global = true } }

// eventHub holds the listeners of one tree, keyed by event key identity.
type eventHub struct {
	t     *tree
	hooks map[any][]*hook // guarded by the tree lock
}

// register adds a listener as an effect of ctx's fiber: it is removed when the fiber
// unloads or the handle is disposed.
func (e *eventHub) register(ctx *Context, key any, name string, fn any, prepend bool, opts []ListenOption) (*EffectHandle, error) {
	var options listenOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return ctx.Effect(fmt.Sprintf("ctx.On(%q)", name), func() (Disposer, error) {
		h := &hook{ctx: ctx, fn: fn, global: options.global}
		e.t.lock()
		if prepend {
			e.hooks[key] = slices.Insert(e.hooks[key], 0, h)
		} else {
			e.hooks[key] = append(e.hooks[key], h)
		}
		e.t.unlockQuiet()
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

// snapshot returns the listeners of key that ctx's filter admits, at dispatch time. A
// listener removed during the dispatch is skipped when its turn comes. The filter runs
// outside the tree lock; a panicking filter rejects the listener and the first such
// panic of the dispatch is logged on ctx's logger.
func (e *eventHub) snapshot(ctx *Context, key any, name string) []*hook {
	e.t.lock()
	hooks := slices.Clone(e.hooks[key])
	e.t.unlockQuiet()
	if ctx.filter == nil {
		return hooks
	}
	logged := false
	admitted := hooks[:0]
	for _, h := range hooks {
		if h.global {
			admitted = append(admitted, h)
			continue
		}
		ok, err := ctx.admits(h.ctx)
		if err != nil && !logged {
			logged = true
			ctx.Logger().Error(fmt.Errorf("cordis: dispatch filter of event %q panicked: %w", name, err))
		}
		if ok {
			admitted = append(admitted, h)
		}
	}
	return admitted
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
func (k *EventKey[A]) On(ctx *Context, listener func(ctx *Context, args A) error, opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, false, opts)
}

// Prepend registers a listener that runs before those already registered.
func (k *EventKey[A]) Prepend(ctx *Context, listener func(ctx *Context, args A) error, opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, true, opts)
}

// Once registers a listener that runs on the next event only.
func (k *EventKey[A]) Once(ctx *Context, listener func(ctx *Context, args A) error, opts ...ListenOption) (*EffectHandle, error) {
	return once(
		func(fn func(*Context, A) error) (*EffectHandle, error) { return k.On(ctx, fn, opts...) },
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

// Emit calls the listeners ctx admits one after another and returns the first error,
// which stops the dispatch. Use Notify when no listener may stop the others.
func (k *EventKey[A]) Emit(ctx *Context, args A) error {
	for _, h := range ctx.t.events.snapshot(ctx, k, k.name) {
		if h.removed.Load() {
			continue
		}
		if err := h.fn.(func(*Context, A) error)(h.ctx, args); err != nil {
			return err
		}
	}
	return nil
}

// EmitScoped is Emit dispatched only to the listeners registered from scope's subtree
// (and Global ones); see Context.ScopeTo.
func (k *EventKey[A]) EmitScoped(ctx *Context, scope *Fiber, args A) error {
	return k.Emit(ctx.ScopeTo(scope), args)
}

// Notify tells every listener ctx admits about a fact, in registration order. A
// listener's error or panic is logged on that listener's own logger and reaches
// neither the caller nor the later listeners: nothing a listener does can veto or
// abort the fact. A listener removed during the dispatch is skipped if it has not run.
func (k *EventKey[A]) Notify(ctx *Context, args A) {
	for _, h := range ctx.t.events.snapshot(ctx, k, k.name) {
		if h.removed.Load() {
			continue
		}
		if err := protect(func() error { return h.fn.(func(*Context, A) error)(h.ctx, args) }); err != nil {
			h.ctx.Logger().Error(fmt.Errorf("listener of event %q failed: %w", k.name, err))
		}
	}
}

// NotifyScoped is Notify dispatched only to the listeners registered from scope's
// subtree (and Global ones); see Context.ScopeTo.
func (k *EventKey[A]) NotifyScoped(ctx *Context, scope *Fiber, args A) {
	k.Notify(ctx.ScopeTo(scope), args)
}

// Parallel calls every listener ctx admits on its own goroutine and waits for all of
// them. The failures, panics included, are returned as one AggregateError naming the
// event.
func (k *EventKey[A]) Parallel(ctx *Context, args A) error {
	hooks := ctx.t.events.snapshot(ctx, k, k.name)
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
	unpark := ctx.t.park()
	wg.Wait()
	unpark()
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
func (k *BailKey[A, R]) On(ctx *Context, listener func(ctx *Context, args A) (result R, ok bool, err error), opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, false, opts)
}

// Prepend registers a listener that is asked before those already registered.
func (k *BailKey[A, R]) Prepend(ctx *Context, listener func(ctx *Context, args A) (result R, ok bool, err error), opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, true, opts)
}

// Bail asks the listeners ctx admits in order and returns the first answer. ok is
// false when no listener answered; an error stops the dispatch.
func (k *BailKey[A, R]) Bail(ctx *Context, args A) (result R, ok bool, err error) {
	for _, h := range ctx.t.events.snapshot(ctx, k, k.name) {
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
func (k *WaterfallKey[A]) On(ctx *Context, listener func(ctx *Context, value A, next func(A) (A, error)) (A, error), opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, false, opts)
}

// Prepend registers a middleware listener that wraps those already registered.
func (k *WaterfallKey[A]) Prepend(ctx *Context, listener func(ctx *Context, value A, next func(A) (A, error)) (A, error), opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, listener, true, opts)
}

// Waterfall runs the listeners ctx admits as a middleware chain around terminal. A
// listener that calls next more than once gets an error from the second call.
func (k *WaterfallKey[A]) Waterfall(ctx *Context, value A, terminal func(A) (A, error)) (A, error) {
	hooks := ctx.t.events.snapshot(ctx, k, k.name)
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
