package cordis

import (
	"errors"
	"slices"
)

// Runtime is the registry's record of a component in use: the component and the
// fibers currently instantiating it.
type Runtime struct {
	t         *tree
	component *Component
	name      string

	fibers []*Fiber // guarded by the tree lock
}

// Component returns the component.
func (r *Runtime) Component() *Component { return r.component }

// Name returns the component's name.
func (r *Runtime) Name() string { return r.name }

// Fibers returns a snapshot of the fibers instantiating the component.
func (r *Runtime) Fibers() []*Fiber {
	r.t.lock()
	defer r.t.unlockQuiet()
	return slices.Clone(r.fibers)
}

func (r *Runtime) attachLocked(fiber *Fiber) { r.fibers = append(r.fibers, fiber) }

func (r *Runtime) detachLocked(fiber *Fiber) {
	if i := slices.Index(r.fibers, fiber); i >= 0 {
		r.fibers = slices.Delete(r.fibers, i, i+1)
	}
}

// Registry tracks the components in use in a tree. A component is registered when
// its first fiber is created and unregistered when its last fiber is disposed.
// Components are keyed by identity (the *Component pointer).
type Registry struct {
	t        *tree
	runtimes map[*Component]*Runtime
	order    []*Runtime
	aliases  map[*Component]*Component
}

// Len returns the number of registered components.
func (r *Registry) Len() int {
	r.t.lock()
	defer r.t.unlockQuiet()
	return len(r.order)
}

// Runtimes returns a snapshot of the registered components, in registration order.
func (r *Registry) Runtimes() []*Runtime {
	r.t.lock()
	defer r.t.unlockQuiet()
	return slices.Clone(r.order)
}

// Get returns the runtime of component, following replacements; nil when the
// component is not in use.
func (r *Registry) Get(component *Component) *Runtime {
	r.t.lock()
	defer r.t.unlockQuiet()
	return r.runtimes[r.forwardLocked(component)]
}

// Has reports whether component (or its replacement) is in use.
func (r *Registry) Has(component *Component) bool { return r.Get(component) != nil }

// Replace swaps the implementation behind every fiber of old for next, in place:
// each fiber keeps its parent, config and effects owner, takes next's dependencies
// (plus those added at its use site), clears a previous load error, and reloads.
// Later uses of old are forwarded to next. It returns the fibers that were moved.
func (r *Registry) Replace(old, next *Component) []*Fiber {
	if old == nil || next == nil {
		return nil
	}
	r.t.lock()
	defer r.t.unlock()
	oldKey := r.forwardLocked(old)

	// next is an implementation of its own again
	delete(r.aliases, next)
	runtime := r.runtimes[oldKey]
	target := r.runtimes[next]
	if oldKey != next {
		r.removeLocked(oldKey)
		r.aliases[oldKey] = next
	}

	if runtime == nil || len(runtime.fibers) == 0 {
		return nil
	}
	fibers := slices.Clone(runtime.fibers)
	if target == nil {
		target = r.createLocked(next)
	}
	for _, fiber := range fibers {
		fiber.replaceLocked(target, mergeInject(next, fiber.extra))
	}
	return fibers
}

// Remove disposes every fiber of component and unregisters it.
func (r *Registry) Remove(component *Component) {
	r.t.lock()
	key := r.forwardLocked(component)
	runtime := r.runtimes[key]
	if runtime == nil {
		r.t.unlock()
		return
	}
	r.removeLocked(key)
	fibers := slices.Clone(runtime.fibers)
	r.t.unlock()
	for _, fiber := range fibers {
		fiber.Dispose()
	}
}

func (r *Registry) use(ctx *Context, component *Component, config any, extra []string) (*Fiber, error) {
	if component == nil || component.apply == nil {
		return nil, errors.New("cordis: invalid component, expect one made by Define or Construct")
	}
	r.t.lock()
	if ctx.fiber.disposed {
		r.t.unlock()
		return nil, ErrInactive
	}
	component = r.forwardLocked(component)
	runtime := r.runtimes[component]
	if runtime == nil {
		runtime = r.createLocked(component)
	}
	r.t.unlock()
	return newFiber(ctx, runtime, config, mergeInject(component, extra), slices.Clone(extra))
}

func (r *Registry) forwardLocked(component *Component) *Component {
	for {
		next, ok := r.aliases[component]
		if !ok {
			return component
		}
		component = next
	}
}

func (r *Registry) createLocked(component *Component) *Runtime {
	runtime := &Runtime{t: r.t, component: component, name: component.name}
	r.runtimes[component] = runtime
	r.order = append(r.order, runtime)
	return runtime
}

func (r *Registry) removeLocked(component *Component) {
	runtime, ok := r.runtimes[component]
	if !ok {
		return
	}
	delete(r.runtimes, component)
	if i := slices.Index(r.order, runtime); i >= 0 {
		r.order = slices.Delete(r.order, i, i+1)
	}
}

func (r *Registry) unregisterLocked(runtime *Runtime) {
	if r.runtimes[runtime.component] == runtime {
		r.removeLocked(runtime.component)
	}
}

func mergeInject(component *Component, extra []string) []Dependency {
	inject := slices.Clone(component.inject)
	for _, name := range extra {
		if !slices.ContainsFunc(inject, func(d Dependency) bool { return d.Name == name }) {
			inject = append(inject, Dependency{Name: name})
		}
	}
	return inject
}
