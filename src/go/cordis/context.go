package cordis

import (
	"sync"
)

// Context is the handle a component works through: it resolves services, creates
// effects and loads child components. Every component body receives a context owned
// by its fiber; Isolate and Intercept derive contexts that share the fiber but change
// how services are resolved below them.
//
// A Context is safe for concurrent use.
type Context struct {
	t         *tree
	root      *Context
	parent    *Context
	fiber     *Fiber
	isolate   *chainMap[*IsolateKey]
	intercept *chainMap[any]

	loggerOnce sync.Once
	logger     *Logger
}

// New creates a root context: a new tree with its own service store, registry, event
// bus and logger. The logger is provided as the "logger" service.
func New() *Context {
	t := &tree{}
	root := &Context{
		t:         t,
		isolate:   newChainMap[*IsolateKey](nil),
		intercept: newChainMap[any](nil),
	}
	root.root = root
	t.root = root
	t.internal = &Internal{t: t}
	t.loggers = newLoggerService()
	t.store = &store{t: t, byKey: map[*IsolateKey]*serviceImpl{}}
	t.registry = &Registry{t: t, runtimes: map[*Component]*Runtime{}, aliases: map[*Component]*Component{}}
	t.events = &eventHub{t: t, hooks: map[any][]*hook{}}
	root.fiber = newRootFiber(root)
	if _, err := root.Provide(LoggerServiceName, t.loggers); err != nil {
		panic(err) // a fresh root is always active
	}
	return root
}

func (c *Context) derive(fiber *Fiber, isolate *chainMap[*IsolateKey], intercept *chainMap[any]) *Context {
	if isolate == nil {
		isolate = c.isolate
	}
	if intercept == nil {
		intercept = c.intercept
	}
	return &Context{
		t:         c.t,
		root:      c.root,
		parent:    c,
		fiber:     fiber,
		isolate:   isolate,
		intercept: intercept,
	}
}

// Root returns the root context of the tree.
func (c *Context) Root() *Context { return c.root }

// Parent returns the context this one was derived from, or nil for the root.
func (c *Context) Parent() *Context { return c.parent }

// Fiber returns the fiber that owns this context's effects.
func (c *Context) Fiber() *Fiber { return c.fiber }

// Registry returns the tree's component registry.
func (c *Context) Registry() *Registry { return c.t.registry }

// Loggers returns the tree's logger service (its exporters and default level).
func (c *Context) Loggers() *LoggerService { return c.t.loggers }

// Internal returns the tree's lifecycle notifications.
func (c *Context) Internal() *Internal { return c.t.internal }

// Logger returns a logger named after this context's fiber. Its threshold follows the
// nearest LoggerConfig intercepted for the "logger" service, then the service level.
func (c *Context) Logger() *Logger {
	c.loggerOnce.Do(func() { c.logger = c.t.loggers.For(c, c.fiber.Name()) })
	return c.logger
}

func (c *Context) String() string { return "Context <" + c.fiber.Name() + ">" }

// ------------------------------------------------------------------ scoping

// Isolate returns a context in which name resolves in a new, private realm: services
// provided under name from it (and below it) are invisible outside, and outer
// providers of name are invisible inside.
func (c *Context) Isolate(name string) *Context {
	return c.IsolateWith(name, NewIsolateKey(name))
}

// IsolateWith is Isolate with an explicit realm key, so several subtrees can share a
// realm.
func (c *Context) IsolateWith(name string, key *IsolateKey) *Context {
	isolate := newChainMap(c.isolate)
	isolate.set(name, key)
	return c.derive(c.fiber, isolate, nil)
}

// Intercept returns a context that layers config on the service name. Services read
// it through InterceptChain or GetIntercept; the logger reads a LoggerConfig.
func (c *Context) Intercept(name string, config any) *Context {
	intercept := newChainMap(c.intercept)
	intercept.set(name, config)
	return c.derive(c.fiber, nil, intercept)
}

// InterceptChain returns every config layered on name, highest priority first: context
// intercepts from the nearest outwards, then the per-dependency config the declaring
// fiber attached with WithInjectConfig.
func (c *Context) InterceptChain(name string) []any {
	values := c.intercept.chain(name)
	c.t.lock()
	fiber, _, found := c.t.store.locateLocked(c, name)
	var metadata any
	if found && fiber != nil {
		metadata = fiber.injectConfigLocked(name)
	}
	c.t.unlockQuiet()
	if metadata != nil {
		values = append(values, metadata)
	}
	return values
}

// GetIntercept returns the highest-priority config of type T layered on name.
func GetIntercept[T any](ctx *Context, name string) (T, bool) {
	for _, value := range ctx.InterceptChain(name) {
		if typed, ok := value.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

// keyForLocked returns the realm key name resolves to from c, allocating the root
// realm on first use. The tree lock must be held.
func (c *Context) keyForLocked(name string) *IsolateKey {
	if key, ok := c.isolate.get(name); ok {
		return key
	}
	key := NewIsolateKey(name)
	c.root.isolate.set(name, key)
	return key
}

// ----------------------------------------------------------------- services

// Provide registers value under name, as an effect of this context's fiber: the
// binding is withdrawn when the fiber unloads or the handle is disposed, after every
// fiber that loaded against it has finished its own cleanup. Dependents see the
// binding while this fiber is Active. A value implementing Checker is consulted before
// each dependent loads; one implementing Scoped is specialised per caller on Get.
func (c *Context) Provide(name string, value any) (*EffectHandle, error) {
	return c.t.store.provide(c, name, value)
}

// Set replaces the value of a service this context's fiber provides, and re-evaluates
// its dependents.
func (c *Context) Set(name string, value any) error { return c.t.store.set(c, name, value) }

// Get resolves a service. From inside a component it walks from this fiber to the
// root, like upstream's locate(): it succeeds when a fiber on the walk declares name
// (returning the binding it loaded against) or provides name itself in the same
// realm, and stops at a context that isolates name again. Services provided on the
// root are reachable from every fiber. From the root (or a context derived from it
// without a fiber) any active service is reachable.
//
// The error wraps ErrNotInjected or ErrUnavailable.
func (c *Context) Get(name string) (any, error) { return c.t.store.resolve(c, name) }

// Lookup is the optional, unchecked lookup (upstream's ctx.get(name)): the value of
// the active provider of name in this context's realm, or ok=false when there is
// none. Unlike Get it does not check inject declarations and never fails, so a
// component can probe for a service it does not depend on. A Scoped value is
// specialised for this context, as Get does. Declare a dependency (inject) when the
// component must reload with the service; Lookup only reads the current binding.
func (c *Context) Lookup(name string) (any, bool) { return c.t.store.lookup(c, name) }

// Has reports whether name currently has an active provider in this context's realm.
func (c *Context) Has(name string) bool {
	c.t.lock()
	defer c.t.unlockQuiet()
	return c.t.store.getImplLocked(c, name, true) != nil
}

// --------------------------------------------------------------- components

// Use loads component as a child of this context with the given config. extraInject
// adds dependencies on top of the component's own declaration. The returned fiber
// loads on its own goroutine once its dependencies are available; call Wait to block
// until it settles.
func (c *Context) Use(component *Component, config any, extraInject ...string) (*Fiber, error) {
	return c.t.registry.use(c, component, config, extraInject)
}

// Apply loads an inline component body named name that depends on inject.
func (c *Context) Apply(name string, body func(ctx *Context) error, inject ...string) (*Fiber, error) {
	component := Define(name, func(ctx *Context, _ None) error { return body(ctx) }, WithInject(inject...))
	return c.Use(component, nil)
}

// Inject loads an inline component body that depends on names (upstream's
// ctx.inject()).
func (c *Context) Inject(names []string, body func(ctx *Context) error) (*Fiber, error) {
	return c.Apply("inject", body, names...)
}

// OnUpdate registers a hook, owned by this context's fiber, that sees each Update of a
// loaded fiber. The hook receives the resolved config and may accept it in place, or
// call next to fall through to the following hook and finally to a restart. Hooks are
// dropped when the fiber unloads.
func (c *Context) OnUpdate(handler UpdateHandler, prepend bool) (*EffectHandle, error) {
	return c.fiber.addUpdateHook(handler, prepend)
}

// AcceptUpdate registers an update hook that accepts every config of type C in place,
// so Update does not restart the fiber.
func AcceptUpdate[C any](ctx *Context, accept func(config C)) (*EffectHandle, error) {
	return ctx.OnUpdate(func(resolved any, next func() error) error {
		typed, ok := resolved.(C)
		if !ok {
			return next()
		}
		accept(typed)
		return nil
	}, false)
}

// ------------------------------------------------------------------ effects

// Effect acquires a resource synchronously and returns a handle for it. The cleanup
// returned by acquire runs when the fiber unloads or the handle is disposed. If
// acquire fails, the effect is disposed and its error returned.
func (c *Context) Effect(label string, acquire func() (Disposer, error)) (*EffectHandle, error) {
	return c.fiber.effect(label, acquire)
}

// EffectAsync acquires a resource on a new goroutine and returns its handle at once.
// Started while the fiber is loading (from the body or Init), the effect keeps the
// fiber Loading until it completes, and its failure fails the load. Started later, a
// failure is logged and the effect disposed.
func (c *Context) EffectAsync(label string, acquire func() (Disposer, error)) (*EffectHandle, error) {
	return c.fiber.effectAsync(label, acquire)
}

// EffectIter runs a generator effect on a new goroutine: seq yields one cleanup per
// acquired resource (upstream's async function* effects). Once the handle is
// disposed, yield returns false and seq must return; the step it was about to hand
// over is released at once, and seq's deferred calls run before the collected
// cleanups do. Loading and failure behave as for EffectAsync.
func (c *Context) EffectIter(label string, seq EffectSeq) (*EffectHandle, error) {
	return c.fiber.effectIter(label, seq)
}

// Dispose disposes this context's fiber; see Fiber.Dispose.
func (c *Context) Dispose() { c.fiber.Dispose() }
