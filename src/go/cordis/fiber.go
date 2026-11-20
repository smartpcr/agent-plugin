package cordis

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

// inactiveEpoch marks a fiber that should not be loaded.
const inactiveEpoch = "\x00inactive"

// unloadedLifetime and disposedLifetime stand in for the lifetime of a fiber that is
// not loaded: already cancelled, with the cause a loaded fiber would have had.
var (
	unloadedLifetime = cancelledContext(ErrUnloading)
	disposedLifetime = cancelledContext(ErrInactive)
)

func cancelledContext(cause error) context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	return ctx
}

// Initializer is implemented by component instances that finish loading
// asynchronously, such as a service opening a connection. The fiber stays Loading,
// and its services invisible to dependents, until Init returns; an error fails the
// load.
type Initializer interface {
	Init() error
}

// UpdateHandler is a hook registered with Context.OnUpdate. It receives the resolved
// new config; it accepts the config in place by returning without calling next, or
// calls next to defer to the following hook and finally to a restart.
type UpdateHandler func(resolved any, next func() error) error

// Dependency is one declared service of a component, with the optional per-dependency
// config the component attached to it (see WithInjectConfig).
type Dependency struct {
	Name   string
	Config any
}

type updateHook struct {
	handler UpdateHandler
	removed bool
}

// Fiber is one live instantiation of a component. It owns the effects the component
// created, tracks the identity of every injected provider as an epoch, and reloads or
// unloads itself whenever that epoch changes. Transitions run on their own goroutine;
// one transition at a time (the inertia), so a change requested mid-transition is
// reconciled when the transition ends.
type Fiber struct {
	t       *tree
	parent  *Context
	ctx     *Context
	isRoot  bool
	extra   []string
	runtime atomic.Pointer[Runtime]

	// guarded by the tree lock
	uid         int
	disposed    bool
	state       FiberState
	err         error
	config      any
	inject      []Dependency
	instance    any
	epoch       string
	revision    int
	inertia     chan struct{}
	release     chan struct{}
	candidates  map[string]*serviceImpl
	committed   map[string]*serviceImpl
	provisions  []*serviceImpl
	effects     []*EffectHandle
	releasing   []*EffectHandle
	loadTasks   []*task
	updateHooks []*updateHook
	anchor      *EffectHandle

	// announced is set once the fiber's creation handlers have returned: the creation
	// notification, posted with sequence announceSeq, announces the fiber after running
	// them, and the fiber does not load before that. createdSeq is the last sequence
	// posted before it.
	announced   bool
	createdSeq  uint64
	announceSeq uint64
	// rootDone is closed once the root's Dispose has released every effect.
	rootDone chan struct{}

	// lifetime is live while a load is the fiber's target; cancel is nil otherwise
	lifetime      context.Context
	cancel        context.CancelCauseFunc
	lifetimeEpoch string
}

func newRootFiber(root *Context) *Fiber {
	lifetime, cancel := context.WithCancelCause(context.Background())
	return &Fiber{
		t:          root.t,
		parent:     root,
		ctx:        root,
		isRoot:     true,
		state:      Active,
		epoch:      "0",
		candidates: map[string]*serviceImpl{},
		committed:  map[string]*serviceImpl{},
		lifetime:   lifetime,
		cancel:     cancel,
		announced:  true,
		rootDone:   make(chan struct{}),
	}
}

func newFiber(parent *Context, runtime *Runtime, config any, inject []Dependency, extra []string) (*Fiber, error) {
	t := parent.t
	t.lock()
	if parent.fiber.disposed {
		t.unlock()
		return nil, ErrInactive
	}
	t.fiberUID++
	f := &Fiber{
		t:          t,
		parent:     parent,
		uid:        t.fiberUID,
		extra:      extra,
		state:      Pending,
		config:     config,
		inject:     inject,
		epoch:      inactiveEpoch,
		candidates: map[string]*serviceImpl{},
	}
	f.runtime.Store(runtime)
	f.ctx = parent.derive(f, nil, nil)
	t.unlock()

	// The anchor is owned by the parent before the fiber is announced, so a creation
	// handler that disposes the fiber removes it. The creation notification starts the
	// load itself after its handlers return, so the load waits for every creation
	// handler whichever goroutine delivers it. The acquisition does not
	// deliver notifications itself: a handler may dispose the fiber, which waits for
	// the acquisition.
	_, err := parent.fiber.run("ctx.Use()", func(anchor *EffectHandle) (*task, error) {
		anchor.collect(f.disposeAnchor)
		t.lock()
		defer t.unlockQuiet()
		if f.disposed { // the parent released the anchor already
			f.markAnnouncedLocked()
			return nil, nil
		}
		f.anchor = anchor
		f.runtime.Load().attachLocked(f)
		f.createdSeq = t.mark()
		f.announceSeq = t.internal.announcePlugin(f, f.announce)
		return nil, nil
	})
	if err != nil {
		t.lock()
		if len(runtime.fibers) == 0 {
			t.registry.unregisterLocked(runtime)
		}
		t.unlock()
		return nil, err
	}
	return f, nil
}

// announce runs at the end of the creation notification, after its handlers, and
// starts its first load unless a handler disposed it.
func (f *Fiber) announce() {
	t := f.t
	t.lock()
	defer t.unlock()
	f.markAnnouncedLocked()
	if f.disposed {
		return
	}
	// The fiber joined its runtime's notification set before it was announced, and
	// providers are read again here, so one that activated in between is not missed.
	for _, dependency := range f.inject {
		f.checkImplLocked(dependency.Name)
	}
	f.refreshLocked()
}

func (f *Fiber) markAnnouncedLocked() { f.announced = true }

// disposeAnchor removes the fiber from its parent: the cleanup of the parent's effect.
func (f *Fiber) disposeAnchor() error {
	t := f.t
	t.lock()
	f.disposed = true
	t.internal.raisePlugin(f)
	runtime := f.runtime.Load()
	runtime.detachLocked(f)
	if len(runtime.fibers) == 0 {
		t.registry.unregisterLocked(runtime)
	}
	f.setEpochLocked(inactiveEpoch)
	// A fiber that was not loaded starts no unload above, but may still own effects
	// created on its context; release them through the same unload.
	if f.inertia == nil && (len(f.effects) > 0 || len(f.releasing) > 0) {
		f.updateStateLocked(func() (FiberState, bool) {
			f.startLocked(f.unload)
			return Unloading, true
		})
	}
	t.unlock()

	f.waitInertia()

	// setEpochLocked returns early for a fiber that was not loaded, so the cached state
	// is not refreshed; re-derive it now that the fiber is disposed.
	t.lock()
	f.updateStateLocked(nil)
	t.unlock()
	return nil
}

// -------------------------------------------------------------- accessors

// ID returns the fiber's identity, assigned by the registry; the root fiber is 0.
func (f *Fiber) ID() int { return f.uid }

// Parent returns the context the fiber was created from.
func (f *Fiber) Parent() *Context { return f.parent }

// Ctx returns the context handed to the component body; it is owned by this fiber.
func (f *Fiber) Ctx() *Context { return f.ctx }

// Lifetime returns the cancellation context of the fiber's current load. It is
// derived from the parent fiber's lifetime, and cancelled as soon as the fiber is
// asked to leave the load, before the unload waits for dependents or runs cleanups.
// context.Cause reports ErrInactive for a disposed fiber and ErrUnloading otherwise.
// A fiber that is not loading or loaded returns an already-cancelled context; each
// load gets a fresh one. The root's lifetime ends with the root's Dispose.
func (f *Fiber) Lifetime() context.Context {
	f.t.lock()
	defer f.t.unlockQuiet()
	return f.lifetimeLocked()
}

func (f *Fiber) lifetimeLocked() context.Context {
	switch {
	case f.cancel != nil:
		return f.lifetime
	case f.disposed || f.isRoot:
		return disposedLifetime
	default:
		return unloadedLifetime
	}
}

// cancelLifetimeLocked ends the current load's lifetime, if it is still live.
func (f *Fiber) cancelLifetimeLocked() {
	if f.cancel == nil {
		return
	}
	cause := ErrUnloading
	if f.disposed || f.isRoot {
		cause = ErrInactive
	}
	f.cancel(cause)
	f.cancel = nil
	f.lifetime = nil
}

// Runtime returns the component runtime behind the fiber, or nil for the root fiber.
// It changes when Registry.Replace swaps the component.
func (f *Fiber) Runtime() *Runtime { return f.runtime.Load() }

// Name returns the name of the fiber's component; the root fiber is named "root".
func (f *Fiber) Name() string {
	if runtime := f.runtime.Load(); runtime != nil {
		return runtime.name
	}
	return "root"
}

// State returns the current lifecycle state.
func (f *Fiber) State() FiberState {
	f.t.lock()
	defer f.t.unlockQuiet()
	return f.state
}

// Err returns the error of the last failed load, or nil.
func (f *Fiber) Err() error {
	f.t.lock()
	defer f.t.unlockQuiet()
	return f.err
}

// Config returns the config as it was handed in, before resolution. It is resolved
// through the component's schema on every load.
func (f *Fiber) Config() any {
	f.t.lock()
	defer f.t.unlockQuiet()
	return f.config
}

// Instance returns the value the component's constructor returned on its last load,
// or nil while unloaded.
func (f *Fiber) Instance() any {
	f.t.lock()
	defer f.t.unlockQuiet()
	return f.instance
}

// Inject returns the fiber's declared dependencies: the component's own, followed by
// those added at the use site.
func (f *Fiber) Inject() []Dependency {
	f.t.lock()
	defer f.t.unlockQuiet()
	return slices.Clone(f.inject)
}

// Effects returns a snapshot of the effects the fiber currently owns.
func (f *Fiber) Effects() []*EffectHandle {
	f.t.lock()
	defer f.t.unlockQuiet()
	return slices.Clone(f.effects)
}

func (f *Fiber) String() string { return fmt.Sprintf("Fiber <%s> (%s)", f.Name(), f.State()) }

// ------------------------------------------------------------ public API

// Wait blocks until the fiber settles (no load or unload is in flight) and returns
// the error of a failed load. A fiber still waiting for dependencies settles as
// Pending with a nil error. A fiber that was just created first has its creation
// notification delivered, on the calling goroutine if the dispatcher has not reached
// it, so its creation handlers run before it loads without Wait depending on the
// dispatcher. Wait returns once the notifications raised during the call have been
// delivered. A creation handler must not wait for the fiber it announces: that fiber
// loads only after the handler returns.
func (f *Fiber) Wait() error {
	t := f.t
	lo := t.mark()
	t.lock()
	announced, created, seq := f.announced, f.createdSeq, f.announceSeq
	t.unlockQuiet()
	if !announced {
		// Wait for the creation handlers too, so they still run before the load.
		t.barrier(created, seq)
	}
	f.waitInertia()
	t.barrier(lo, t.mark())
	return f.Err()
}

// Restart reloads the component with its current config, and waits for the fiber to
// settle. A restart requested while a load is in flight is never lost: the reload
// revision is part of the epoch.
func (f *Fiber) Restart() error {
	if f.isRoot {
		return ErrRootFiber
	}
	f.t.lock()
	if f.disposed {
		f.t.unlock()
		return ErrInactive
	}
	f.revision++
	f.refreshLocked()
	f.t.unlock()
	return f.Wait()
}

// Update replaces the config. On a loaded fiber the new config is resolved first (a
// rejected config returns its error and changes nothing), then offered to the fiber's
// update hooks, which may accept it in place; otherwise the fiber restarts. Unless
// noSave is set, commit subscribers (Internal.OnCommit) see the original config so a
// loader can persist it. Update clears a previous load error.
func (f *Fiber) Update(config any, noSave bool) error {
	if f.isRoot {
		return ErrRootFiber
	}
	t := f.t
	t.lock()
	if f.disposed {
		t.unlock()
		return ErrInactive
	}
	loaded := f.epoch != inactiveEpoch
	t.unlock()

	var resolved any
	if loaded {
		var err error
		if resolved, err = f.resolveConfig(config); err != nil {
			return err
		}
	}
	if !noSave {
		t.internal.raiseCommit(f, config)
	}

	t.lock()
	f.config = config
	f.err = nil
	if f.inertia == nil {
		f.updateStateLocked(nil) // a cleared error changes the state even without a reload
	}
	t.unlock()

	if loaded {
		return f.runUpdateHooks(resolved, f.Restart)
	}
	return f.Restart()
}

// Dispose disposes the fiber. A component fiber is removed from its parent: it
// unloads (waiting for its dependents, then releasing its effects), becomes Disposed,
// and its component is unregistered once no fiber uses it. Disposing the root fiber
// is final: it cancels the root's lifetime, refuses new effects and fibers on the
// root, and releases all of its effects in reverse order, tearing the whole tree down.
// Dispose returns only once every cleanup the fiber owned has returned, also when
// another goroutine started the disposal. Failures are logged, not returned.
//
// Dispose returns once the notifications raised during the call have been delivered:
// queued ones are delivered on the calling goroutine once the lifecycle work is done,
// so a handler may wait for or dispose the fiber whose transition raised it.
func (f *Fiber) Dispose() {
	lo := f.t.mark()
	defer func() { f.t.barrier(lo, f.t.mark()) }()
	if !f.isRoot {
		f.t.lock()
		anchor := f.anchor
		f.t.unlock()
		if anchor != nil {
			anchor.Dispose()
		}
		return
	}
	f.t.lock()
	if f.disposed {
		f.t.unlockQuiet()
		<-f.rootDone
		return
	}
	// The teardown is the root's transition: Wait follows it and the state reports
	// Unloading, then Disposed. The root's services are withdrawn by their own
	// cleanups, in reverse order, so leaving Active notifies no dependents here.
	f.disposed = true
	f.inertia = f.rootDone
	f.setRootStateLocked(Unloading)
	f.cancelLifetimeLocked()
	effects := f.effects
	f.effects = nil
	f.t.unlock()
	for i := len(effects) - 1; i >= 0; i-- {
		effects[i].Dispose()
	}
	f.waitReleasing()
	// Completion is published before the terminal notification is handed over, so a
	// Disposed handler that calls Dispose or Wait returns at once.
	f.t.lock()
	f.inertia = nil
	f.setRootStateLocked(Disposed)
	f.t.unlockQuiet()
	close(f.rootDone)
	f.t.flush()
}

func (f *Fiber) setRootStateLocked(next FiberState) {
	old := f.state
	f.state = next
	f.t.internal.raiseStatus(f, old, next)
}

// ---------------------------------------------------------------- effects

func label(name string) string {
	if name == "" {
		return "anonymous"
	}
	return name
}

// run registers a new effect and runs body to acquire it. body returns the pending
// acquisition of an asynchronous effect (started with spawn), or nil. A fiber that is
// disposed or unloading refuses new effects, so a cleanup cannot create one that
// outlives the unload. The handle carries its acquisition from the moment it is
// published, so an unload that reaches it waits for the acquisition and runs the
// cleanup it collects before the fiber's providers are released. Notifications raised
// during the acquisition are delivered once it is complete.
func (f *Fiber) run(name string, body func(h *EffectHandle) (*task, error)) (*EffectHandle, error) {
	t := f.t
	h := newEffectHandle(f, label(name))
	acquisition := &task{done: make(chan struct{})}
	h.pending = acquisition
	t.lock()
	if f.disposed || f.state == Unloading {
		t.unlock()
		return nil, ErrInactive
	}
	f.effects = append(f.effects, h)
	t.unlockQuiet() // the acquisition is already pending: deliver only once it ends

	pending, err := body(h)
	if pending == nil {
		acquisition.finish(nil) // a synchronous failure is returned, not reported
	}
	t.flush()
	if err != nil {
		h.Dispose()
		return nil, err
	}
	if pending == nil {
		return h, nil
	}

	// An asynchronous effect started while the fiber loads keeps it Loading until it
	// settles, and its failure fails the load. One started later cannot.
	t.lock()
	if f.state == Loading {
		f.loadTasks = append(f.loadTasks, pending)
		t.unlock()
		return h, nil
	}
	t.unlock()
	go func() {
		<-pending.done
		if pending.err != nil {
			f.ctx.Logger().Error(pending.err)
			h.Dispose()
		}
	}()
	return h, nil
}

func (f *Fiber) effect(name string, acquire func() (Disposer, error)) (*EffectHandle, error) {
	return f.run(name, func(h *EffectHandle) (*task, error) {
		var dispose Disposer
		err := protect(func() (err error) {
			dispose, err = acquire()
			return err
		})
		if err != nil {
			return nil, err
		}
		h.collect(dispose)
		return nil, nil
	})
}

func (f *Fiber) effectAsync(name string, acquire func() (Disposer, error)) (*EffectHandle, error) {
	return f.run(name, func(h *EffectHandle) (*task, error) {
		return h.spawn(func() error {
			dispose, err := acquire()
			if err != nil {
				return err
			}
			h.collect(dispose)
			return nil
		}), nil
	})
}

func (f *Fiber) effectIter(name string, seq EffectSeq) (*EffectHandle, error) {
	return f.run(name, func(h *EffectHandle) (*task, error) {
		return h.spawn(func() error { return seq(h.collect) }), nil
	})
}

// attachDisposer ties a cleanup to the fiber without the inactive check: used for the
// instance a load produced.
func (f *Fiber) attachDisposer(name string, dispose Disposer) {
	h := newEffectHandle(f, name)
	f.t.lock()
	f.effects = append(f.effects, h)
	f.t.unlock()
	h.collect(dispose)
}

// detachLocked moves a handle being disposed from the fiber's effects to the handles
// whose cleanups are still running.
func (f *Fiber) detachLocked(h *EffectHandle) {
	if i := slices.Index(f.effects, h); i >= 0 {
		f.effects = slices.Delete(f.effects, i, i+1)
	}
	f.releasing = append(f.releasing, h)
}

// settleLocked forgets a handle whose cleanups have all returned.
func (f *Fiber) settleLocked(h *EffectHandle) {
	if i := slices.Index(f.releasing, h); i >= 0 {
		f.releasing = slices.Delete(f.releasing, i, i+1)
	}
}

// waitReleasing returns once no handle of the fiber is still running its cleanups,
// including handles another goroutine started to dispose.
func (f *Fiber) waitReleasing() {
	for {
		f.t.lock()
		if len(f.releasing) == 0 {
			f.t.unlockQuiet()
			return
		}
		h := f.releasing[0]
		f.t.unlockQuiet()
		<-h.released
	}
}

// ----------------------------------------------------------- update hooks

func (f *Fiber) addUpdateHook(handler UpdateHandler, prepend bool) (*EffectHandle, error) {
	return f.effect("ctx.OnUpdate()", func() (Disposer, error) {
		hook := &updateHook{handler: handler}
		f.t.lock()
		if prepend {
			f.updateHooks = slices.Insert(f.updateHooks, 0, hook)
		} else {
			f.updateHooks = append(f.updateHooks, hook)
		}
		f.t.unlockQuiet()
		return func() error {
			f.t.lock()
			hook.removed = true
			if i := slices.Index(f.updateHooks, hook); i >= 0 {
				f.updateHooks = slices.Delete(f.updateHooks, i, i+1)
			}
			f.t.unlock()
			return nil
		}, nil
	})
}

func (f *Fiber) runUpdateHooks(resolved any, terminal func() error) error {
	f.t.lock()
	hooks := slices.Clone(f.updateHooks)
	f.t.unlock()

	index := 0
	var dispatch func() error
	dispatch = func() error {
		for index < len(hooks) && f.hookRemoved(hooks[index]) {
			index++
		}
		if index >= len(hooks) {
			return terminal()
		}
		hook := hooks[index]
		index++
		called := false
		return hook.handler(resolved, func() error {
			if called {
				return fmt.Errorf("cordis: next() called multiple times")
			}
			called = true
			return dispatch()
		})
	}
	return dispatch()
}

func (f *Fiber) hookRemoved(hook *updateHook) bool {
	f.t.lock()
	defer f.t.unlockQuiet()
	return hook.removed
}

// ------------------------------------------------------- dependency graph

func (f *Fiber) hasInjectLocked(name string) bool {
	for _, dependency := range f.inject {
		if dependency.Name == name {
			return true
		}
	}
	return false
}

func (f *Fiber) injectConfigLocked(name string) any {
	for _, dependency := range f.inject {
		if dependency.Name == name {
			return dependency.Config
		}
	}
	return nil
}

// checkImplLocked re-evaluates whether name currently has a usable provider.
func (f *Fiber) checkImplLocked(name string) {
	impl := f.t.store.getImplLocked(f.ctx, name, true)
	if impl == nil || !impl.availableLocked() {
		delete(f.candidates, name)
		return
	}
	f.candidates[name] = impl
}

// refreshLocked recomputes the epoch: the reload revision followed by the identity of
// every injected binding.
func (f *Fiber) refreshLocked() {
	if f.disposed || f.isRoot || !f.announced {
		return
	}
	var b strings.Builder
	b.WriteString(strconv.Itoa(f.revision))
	for _, dependency := range f.inject {
		impl, ok := f.candidates[dependency.Name]
		if !ok {
			f.setEpochLocked(inactiveEpoch)
			return
		}
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(impl.uid))
	}
	f.setEpochLocked(b.String())
}

// replaceLocked swaps in a replacement component; see Registry.Replace.
func (f *Fiber) replaceLocked(runtime *Runtime, inject []Dependency) {
	if old := f.runtime.Load(); old != runtime {
		old.detachLocked(f)
		runtime.attachLocked(f)
		f.runtime.Store(runtime)
	}
	f.inject = inject
	f.t.internal.raiseReplace(f)
	f.candidates = map[string]*serviceImpl{}
	for _, dependency := range f.inject {
		f.checkImplLocked(dependency.Name)
	}
	f.err = nil
	f.revision++
	f.refreshLocked()
	// an unchanged epoch starts no transition, but a cleared error still changes the state
	if f.inertia == nil {
		f.updateStateLocked(nil)
	}
}

func (f *Fiber) setEpochLocked(epoch string) {
	old := f.epoch
	if epoch == old {
		return
	}
	// a failed fiber only recovers through Update or Replace, which clear the error
	if f.err != nil {
		return
	}
	f.epoch = epoch
	// the fiber is asked to leave its load: tell the body before anything waits for it
	if epoch != f.lifetimeEpoch {
		f.cancelLifetimeLocked()
	}
	// a transition is already running; it reconciles with the new epoch when it ends
	if f.inertia != nil {
		return
	}
	f.updateStateLocked(func() (FiberState, bool) {
		if epoch != inactiveEpoch && old == inactiveEpoch {
			f.startReloadLocked()
			return Loading, true
		}
		f.startLocked(f.unload)
		return Unloading, true
	})
}

// startLocked runs a transition on a new goroutine. The transition closes done once
// it has handed over to the next transition or settled.
func (f *Fiber) startLocked(transition func(done chan struct{})) {
	done := make(chan struct{})
	f.inertia = done
	go transition(done)
}

// startReloadLocked starts a load for the current epoch, with a fresh lifetime. The
// load is skipped if the epoch has moved on by the time its goroutine runs (upstream's
// check after its microtask yield), e.g. for a fiber disposed right after Use.
func (f *Fiber) startReloadLocked() {
	epoch := f.epoch
	f.cancelLifetimeLocked()
	f.lifetime, f.cancel = context.WithCancelCause(f.parent.fiber.lifetimeLocked())
	f.lifetimeEpoch = epoch
	f.startLocked(func(done chan struct{}) { f.reload(done, epoch) })
}

func (f *Fiber) waitInertia() {
	for {
		f.t.lock()
		inertia := f.inertia
		f.t.unlockQuiet()
		if inertia == nil {
			return
		}
		<-inertia
	}
}

func (f *Fiber) computeStateLocked() FiberState {
	switch {
	case f.disposed:
		return Disposed
	case f.err != nil:
		return Failed
	case f.epoch != inactiveEpoch:
		return Active
	default:
		return Pending
	}
}

// updateStateLocked applies a transition (or re-derives the state when transition is
// nil or declines), reports the change, and notifies dependents when the fiber enters
// or leaves Active.
func (f *Fiber) updateStateLocked(transition func() (FiberState, bool)) {
	old := f.state
	next, ok := FiberState(0), false
	if transition != nil {
		next, ok = transition()
	}
	if !ok {
		next = f.computeStateLocked()
	}
	f.state = next
	if old == next {
		return
	}
	f.t.internal.raiseStatus(f, old, next)

	// only (de)activation is observable by dependents
	if old != Active && next != Active {
		return
	}
	for _, impl := range slices.Clone(f.provisions) {
		if f.t.store.isCurrentLocked(impl) {
			f.t.store.notifyLocked(impl.name, impl.isolate)
		}
	}
}

func (f *Fiber) hasDependentsLocked() bool {
	for _, impl := range f.provisions {
		if len(impl.dependents) > 0 {
			return true
		}
	}
	return false
}

// signalReleaseLocked wakes this fiber's unload if it waits for dependents.
func (f *Fiber) signalReleaseLocked() {
	if f.release != nil {
		close(f.release)
		f.release = nil
	}
}

func (f *Fiber) reload(done chan struct{}, epoch string) {
	t := f.t
	t.lock()
	f.committed = make(map[string]*serviceImpl, len(f.candidates))
	for name, impl := range f.candidates {
		f.committed[name] = impl
		if impl.ctx.fiber != f {
			impl.dependents[f] = struct{}{}
		}
	}
	superseded := f.epoch != epoch
	t.unlock()

	// A load superseded before it started is not run; the changed epoch hands over to
	// an unload below.
	if !superseded {
		if err := f.execute(); err != nil {
			f.ctx.Logger().Error(err)
			t.lock()
			// a superseded load still needs cleanup, but cannot fail the new target
			if f.epoch == epoch {
				f.err = err
				f.epoch = inactiveEpoch
				f.cancelLifetimeLocked()
			}
			t.unlock()
		}
	}

	t.lock()
	f.updateStateLocked(func() (FiberState, bool) {
		// An epoch that moved away and back cancelled this load's lifetime; the load
		// cannot stay with it, so it unloads and loads again.
		if f.epoch == epoch && f.cancel != nil {
			f.inertia = nil
			return 0, false
		}
		f.startLocked(f.unload)
		return Unloading, true
	})
	t.unlock()
	close(done)
}

func (f *Fiber) unload(done chan struct{}) {
	t := f.t

	t.lock()
	f.cancelLifetimeLocked()
	t.unlock()

	// Dependents are notified when this fiber leaves Active, but may still use its
	// services during their own cleanup, so this fiber's effects are released only once
	// every dependent has unloaded.
	for {
		t.lock()
		if !f.hasDependentsLocked() {
			f.release = nil
			t.unlock()
			break
		}
		release := make(chan struct{})
		f.release = release
		t.unlock()
		<-release
	}

	t.lock()
	effects := f.effects
	f.effects = nil
	f.loadTasks = nil
	t.unlock()

	// Effects are released one at a time in reverse order of registration, so a tree
	// always unloads from the leaves up.
	for i := len(effects) - 1; i >= 0; i-- {
		effects[i].Dispose()
	}
	// handles another goroutine started to dispose must finish before providers go
	f.waitReleasing()

	t.lock()
	// release the providers waiting for this fiber to unload
	for _, impl := range f.committed {
		if _, ok := impl.dependents[f]; !ok {
			continue
		}
		delete(impl.dependents, f)
		if len(impl.dependents) > 0 {
			continue
		}
		impl.signalReleaseLocked()
		impl.ctx.fiber.signalReleaseLocked()
	}
	f.committed = nil
	f.instance = nil
	f.updateStateLocked(func() (FiberState, bool) {
		if f.epoch == inactiveEpoch {
			f.inertia = nil
			return 0, false
		}
		f.startReloadLocked()
		return Loading, true
	})
	t.unlock()
	close(done)
}

func (f *Fiber) resolveConfig(config any) (any, error) {
	runtime := f.runtime.Load()
	if runtime == nil || runtime.component.resolve == nil {
		return config, nil
	}
	var resolved any
	err := protect(func() (err error) {
		resolved, err = runtime.component.resolve(config)
		return err
	})
	return resolved, err
}

// execute runs one load: resolve the config, run the component, attach the instance's
// cleanup, run its Init, and wait for the asynchronous effects the load started.
func (f *Fiber) execute() error {
	t := f.t
	t.lock()
	config := f.config
	runtime := f.runtime.Load()
	t.unlock()

	resolved, err := f.resolveConfig(config)
	if err != nil {
		return err
	}
	var instance any
	if err := protect(func() (err error) {
		instance, err = runtime.component.apply(f.ctx, resolved)
		return err
	}); err != nil {
		return err
	}

	t.lock()
	f.instance = instance
	t.unlock()

	if closer, ok := instance.(io.Closer); ok {
		f.attachDisposer(fmt.Sprintf("close(%T)", instance), closer.Close)
	}
	if initializer, ok := instance.(Initializer); ok {
		if err := protect(initializer.Init); err != nil {
			return err
		}
	}

	// wait for the asynchronous effects started during the load (they may start more)
	var first error
	for {
		t.lock()
		pending := f.loadTasks
		f.loadTasks = nil
		t.unlock()
		if len(pending) == 0 {
			return first
		}
		for _, task := range pending {
			<-task.done
			if task.err != nil && first == nil {
				first = task.err
			}
		}
	}
}
