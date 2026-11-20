package cordis

import (
	"fmt"
	"runtime/debug"
)

// IsolateKey names a service realm. Two contexts see the same provider for a name only
// when they map that name to the same key; keys compare by identity, like upstream's
// symbols.
type IsolateKey struct {
	label string
}

// NewIsolateKey returns a fresh realm key. The label is for diagnostics only.
func NewIsolateKey(label string) *IsolateKey { return &IsolateKey{label: label} }

func (k *IsolateKey) String() string { return "IsolateKey(" + k.label + ")" }

// ServiceKey is a typed service name. It gives Get, Provide and Set a static type
// without changing how services are resolved: by name.
type ServiceKey[T any] struct {
	name string
}

// NewServiceKey returns the key for the service registered under name.
func NewServiceKey[T any](name string) ServiceKey[T] { return ServiceKey[T]{name: name} }

// Name returns the service name.
func (k ServiceKey[T]) Name() string { return k.name }

// Get resolves the service from ctx, as Context.Get does, and asserts its type.
func (k ServiceKey[T]) Get(ctx *Context) (T, error) {
	var zero T
	value, err := ctx.Get(k.name)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("cordis: service %q is %T, not %T", k.name, value, zero)
	}
	return typed, nil
}

// Lookup reads the service optionally, as Context.Lookup does. ok is false when there
// is no active provider, or when its value is not a T.
func (k ServiceKey[T]) Lookup(ctx *Context) (T, bool) {
	var zero T
	value, ok := ctx.Lookup(k.name)
	if !ok {
		return zero, false
	}
	typed, ok := value.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

// Provide registers value as the service, as an effect of ctx's fiber.
func (k ServiceKey[T]) Provide(ctx *Context, value T) (*EffectHandle, error) {
	return ctx.Provide(k.name, value)
}

// Set replaces the value of a service that ctx's fiber provides.
func (k ServiceKey[T]) Set(ctx *Context, value T) error { return ctx.Set(k.name, value) }

// ---------------------------------------------------------------- value keys

// ValueKey is a typed context value key (see Context.WithValue). Keys compare by
// identity; define each once, typically as a package-level variable.
type ValueKey[T any] struct {
	name string
}

// NewValueKey returns a new context value key. The name is for diagnostics only.
func NewValueKey[T any](name string) *ValueKey[T] { return &ValueKey[T]{name: name} }

// Name returns the key's name.
func (k *ValueKey[T]) Name() string { return k.name }

func (k *ValueKey[T]) String() string { return "ValueKey(" + k.name + ")" }

// With returns a context on ctx's fiber that carries value under k, visible to ctx's
// derived contexts and the fibers loaded from them.
func (k *ValueKey[T]) With(ctx *Context, value T) *Context { return ctx.WithValue(k, value) }

// Get returns the nearest value set under k on ctx's parent chain; ok is false when
// there is none.
func (k *ValueKey[T]) Get(ctx *Context) (value T, ok bool) {
	for cursor := ctx; cursor != nil; cursor = cursor.parent {
		if cursor.value != nil && cursor.value.key == any(k) {
			typed, ok := cursor.value.value.(T)
			return typed, ok
		}
	}
	return value, false
}

// --------------------------------------------------------------- around keys

// AroundKey identifies an interception point whose listeners wrap a call taking a Req
// and returning a Res: each listener receives the request and a next function, and
// returns a result of its own. Unlike WaterfallKey the result type differs from the
// request's, so an interception can return a decision rather than a rewritten request.
// Keys compare by identity; define each once, typically as a package-level variable.
type AroundKey[Req, Res any] struct {
	name string
}

// NewAround returns a new around key.
func NewAround[Req, Res any](name string) *AroundKey[Req, Res] {
	return &AroundKey[Req, Res]{name: name}
}

// Name returns the event name.
func (k *AroundKey[Req, Res]) Name() string { return k.name }

// AroundListener is a listener of an AroundKey. It may call next at most once to run
// the inner listeners and the terminal; not calling it short-circuits the chain.
type AroundListener[Req, Res any] func(ctx *Context, req Req, next func(Req) (Res, error)) (Res, error)

// On registers a listener as an effect of ctx's fiber. Listeners wrap one another in
// registration order: the first registered is outermost.
func (k *AroundKey[Req, Res]) On(ctx *Context, listener func(ctx *Context, req Req, next func(Req) (Res, error)) (Res, error), opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, AroundListener[Req, Res](listener), false, opts)
}

// Prepend registers a listener that wraps those already registered.
func (k *AroundKey[Req, Res]) Prepend(ctx *Context, listener func(ctx *Context, req Req, next func(Req) (Res, error)) (Res, error), opts ...ListenOption) (*EffectHandle, error) {
	return ctx.t.events.register(ctx, k, k.name, AroundListener[Req, Res](listener), true, opts)
}

// Around runs the listeners ctx admits (see Context.WithFilter) as a chain around
// terminal, outermost first, and returns the outermost result. A listener's error is
// returned unchanged to the listener outside it, as next's result, and so to the
// caller. A panic in a listener or in terminal is recovered and returned as an error
// that names the fiber it happened in and wraps the *PanicError, so a caller fails
// closed. A listener removed during the dispatch is skipped if it has not started; a
// listener that calls next more than once gets an error from the second call.
func (k *AroundKey[Req, Res]) Around(ctx *Context, req Req, terminal func(Req) (Res, error)) (Res, error) {
	hooks := ctx.t.events.snapshot(ctx, k, k.name)
	var dispatch func(index int, req Req) (Res, error)
	dispatch = func(index int, req Req) (res Res, err error) {
		for index < len(hooks) && hooks[index].removed.Load() {
			index++
		}
		if index >= len(hooks) {
			if panicked := capturePanic(func() { res, err = terminal(req) }); panicked != nil {
				var zero Res
				return zero, fmt.Errorf("cordis: terminal of around %q in %s panicked: %w", k.name, ctx.fiber.Name(), panicked)
			}
			return res, err
		}
		h := hooks[index]
		called := false
		next := func(inner Req) (Res, error) {
			if called {
				var zero Res
				return zero, fmt.Errorf("cordis: next() of around %q called multiple times", k.name)
			}
			called = true
			return dispatch(index+1, inner)
		}
		if panicked := capturePanic(func() { res, err = h.fn.(AroundListener[Req, Res])(h.ctx, req, next) }); panicked != nil {
			var zero Res
			return zero, fmt.Errorf("cordis: listener of around %q in %s panicked: %w", k.name, h.ctx.fiber.Name(), panicked)
		}
		return res, err
	}
	return dispatch(0, req)
}

// capturePanic runs fn and returns the panic it raised, if any, as a *PanicError.
func capturePanic(fn func()) (panicked *PanicError) {
	defer func() {
		if r := recover(); r != nil {
			panicked = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	fn()
	return nil
}
