package cordis

import (
	"fmt"
	"slices"
)

// Checker is implemented by service values that can be present but unusable. A
// dependent loads against the service only while Available reports true; the check
// is repeated whenever the binding changes. Available runs under the tree lock, so it
// must be quick and must not call into the context.
type Checker interface {
	Available() bool
}

// Scoped is implemented by service values that specialise themselves per caller, such
// as a timer whose effects must belong to the calling fiber. Get returns
// ForContext(caller) instead of the provided value.
type Scoped interface {
	ForContext(ctx *Context) any
}

// serviceImpl is one binding made by one Provide call.
type serviceImpl struct {
	uid     int
	name    string
	ctx     *Context
	isolate *IsolateKey

	// guarded by the tree lock
	value      any
	dependents map[*Fiber]struct{}
	release    chan struct{}
}

func (i *serviceImpl) availableLocked() bool {
	checker, ok := i.value.(Checker)
	if !ok {
		return true
	}
	available := false
	if err := protect(func() error { available = checker.Available(); return nil }); err != nil {
		logger := i.ctx.Logger()
		i.ctx.t.post(func() { logger.Error(err) })
		return false
	}
	return available
}

func (i *serviceImpl) signalReleaseLocked() {
	if i.release != nil {
		close(i.release)
		i.release = nil
	}
}

// store holds the bindings of one tree, keyed by realm.
type store struct {
	t       *tree
	byKey   map[*IsolateKey]*serviceImpl
	counter int
}

func (s *store) provide(ctx *Context, name string, value any) (*EffectHandle, error) {
	t := s.t
	return ctx.fiber.effect(fmt.Sprintf("ctx.Provide(%q)", name), func() (Disposer, error) {
		t.lock()
		key := ctx.keyForLocked(name)
		if existing := s.byKey[key]; existing != nil {
			t.unlock()
			return nil, fmt.Errorf("cordis: service %q is already registered at <%s>", name, existing.ctx.fiber.Name())
		}
		s.counter++
		impl := &serviceImpl{
			uid:        s.counter,
			name:       name,
			ctx:        ctx,
			isolate:    key,
			value:      value,
			dependents: map[*Fiber]struct{}{},
		}
		s.byKey[key] = impl
		ctx.fiber.provisions = append(ctx.fiber.provisions, impl)
		if ctx.fiber.state == Active {
			s.notifyLocked(name, key)
		}
		t.unlock()

		return func() error {
			release := make(chan struct{})
			t.lock()
			if s.isCurrentLocked(impl) {
				delete(s.byKey, impl.isolate)
			}
			s.notifyLocked(name, impl.isolate)
			// A withdrawn binding gains no new dependents; wait only for the existing
			// ones to finish their cleanup.
			impl.release = release
			if len(impl.dependents) == 0 {
				impl.signalReleaseLocked()
			}
			t.unlock()

			<-release

			t.lock()
			impl.release = nil
			if i := slices.Index(ctx.fiber.provisions, impl); i >= 0 {
				ctx.fiber.provisions = slices.Delete(ctx.fiber.provisions, i, i+1)
			}
			t.unlock()
			return nil
		}, nil
	})
}

func (s *store) set(ctx *Context, name string, value any) error {
	s.t.lock()
	defer s.t.unlock()
	key := ctx.keyForLocked(name)
	impl := s.byKey[key]
	if impl == nil {
		return fmt.Errorf("cordis: cannot set service %q without Provide", name)
	}
	if impl.ctx.fiber != ctx.fiber {
		return fmt.Errorf("cordis: cannot set service %q owned by <%s>", name, impl.ctx.fiber.Name())
	}
	impl.value = value
	s.notifyLocked(name, key)
	return nil
}

// locateLocked resolves an access to name from ctx by walking up its fibers
// (upstream's locate()):
//   - (nil, impl, true): a fiber provides impl itself in the same realm;
//   - (fiber, impl, true): impl comes from the committed view of fiber;
//   - (fiber, nil, true): fiber declares the service but is not loaded against it;
//   - (nil, nil, false): no fiber declares the service, or the walk reaches a context
//     that isolates name again.
func (s *store) locateLocked(ctx *Context, name string) (*Fiber, *serviceImpl, bool) {
	key := ctx.keyForLocked(name)
	table := ctx.isolate
	fiber := ctx.fiber
	for {
		var impl *serviceImpl
		for _, provision := range fiber.provisions {
			if provision.name == name && provision.isolate == key {
				impl = provision
			}
		}
		if impl != nil {
			return nil, impl, true
		}
		for table != fiber.ctx.isolate {
			if table == nil || table.hasOwn(name) {
				return nil, nil, false
			}
			table = table.parent
		}
		if impl, ok := fiber.committed[name]; ok {
			return fiber, impl, true
		}
		if fiber.hasInjectLocked(name) {
			return fiber, nil, true
		}
		if fiber.isRoot {
			return nil, nil, false
		}
		fiber = fiber.parent.fiber
	}
}

func (s *store) resolve(ctx *Context, name string) (any, error) {
	s.t.lock()
	var impl *serviceImpl
	if ctx.fiber.isRoot {
		impl = s.getImplLocked(ctx, name, true)
		if impl == nil {
			s.t.unlockQuiet()
			return nil, &ServiceError{Name: name, Err: ErrUnavailable}
		}
	} else {
		_, located, found := s.locateLocked(ctx, name)
		if !found {
			s.t.unlockQuiet()
			return nil, &ServiceError{Name: name, Err: ErrNotInjected}
		}
		if located == nil {
			s.t.unlockQuiet()
			return nil, &ServiceError{Name: name, Err: ErrUnavailable}
		}
		impl = located
	}
	value := impl.value
	s.t.unlockQuiet()

	if scoped, ok := value.(Scoped); ok {
		return scoped.ForContext(ctx), nil
	}
	return value, nil
}

func (s *store) getImplLocked(ctx *Context, name string, strict bool) *serviceImpl {
	impl := s.byKey[ctx.keyForLocked(name)]
	if impl == nil {
		return nil
	}
	if strict && impl.ctx.fiber.state != Active {
		return nil
	}
	return impl
}

func (s *store) isCurrentLocked(impl *serviceImpl) bool {
	return s.byKey[impl.isolate] == impl
}

// notifyLocked re-evaluates every fiber that injects name in the realm key, and
// reports the change.
func (s *store) notifyLocked(name string, key *IsolateKey) {
	for _, runtime := range slices.Clone(s.t.registry.order) {
		for _, fiber := range slices.Clone(runtime.fibers) {
			if !fiber.hasInjectLocked(name) {
				continue
			}
			if fiber.ctx.keyForLocked(name) != key {
				continue
			}
			fiber.checkImplLocked(name)
			fiber.refreshLocked()
		}
	}
	var value any
	if impl := s.byKey[key]; impl != nil {
		value = impl.value
	}
	s.t.internal.raiseService(name, value)
}
