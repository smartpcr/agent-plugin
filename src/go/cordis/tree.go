package cordis

import "sync"

// tree is the state shared by every context of one root.
//
// mu guards all fiber, effect, service, registry and event-hook state of the tree.
// It is never held while component code runs. Anything that must call out while the
// state is being changed (internal notifications, log lines) is posted to an ordered
// outbox and delivered after mu is released by the tree's dispatcher: one goroutine,
// started on demand, that owns delivery. A goroutine that raises a notification never
// delivers it, so a handler cannot block the lifecycle work that raised it (an
// acquisition, an unload, the root's teardown) by waiting for or disposing it.
type tree struct {
	mu sync.Mutex

	outboxMu   sync.Mutex
	outbox     []func()
	delivering bool // the dispatcher is running
	inHandler  bool // the dispatcher is running a handler
	posted     uint64
	delivered  uint64
	drained    sync.Cond // signalled on outboxMu as notifications are delivered

	fiberUID int

	root     *Context
	store    *store
	registry *Registry
	events   *eventHub
	loggers  *LoggerService
	internal *Internal
}

func (t *tree) lock() { t.mu.Lock() }

// unlock releases the tree lock and hands queued notifications to the dispatcher.
func (t *tree) unlock() {
	t.mu.Unlock()
	t.flush()
}

// unlockQuiet releases the tree lock without waking the dispatcher. Read-only
// operations use it; whoever posted a notification wakes the dispatcher.
func (t *tree) unlockQuiet() { t.mu.Unlock() }

// post queues fn for delivery outside the tree lock. Notifications are delivered in
// the order they were posted.
func (t *tree) post(fn func()) {
	t.outboxMu.Lock()
	t.outbox = append(t.outbox, fn)
	t.posted++
	t.outboxMu.Unlock()
}

// flush starts the dispatcher if notifications are queued and it is not running. It
// never delivers on the calling goroutine, and must not be called with the tree lock
// held.
func (t *tree) flush() {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	t.startLocked()
}

func (t *tree) startLocked() {
	if t.delivering || len(t.outbox) == 0 {
		return
	}
	t.delivering = true
	go t.dispatch()
}

// sync waits until every notification posted so far has been delivered, so a
// lifecycle call (Dispose, Wait) returns with its notifications observed, as upstream's
// synchronous events are. Called from a handler (or anything a handler waits for) it
// returns at once: the dispatcher is busy with that handler. It must be called only
// once the caller's lifecycle work is complete, never with the tree lock held.
func (t *tree) sync() {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	if t.inHandler {
		return
	}
	if t.drained.L == nil {
		t.drained.L = &t.outboxMu
	}
	target := t.posted
	t.startLocked()
	for t.delivered < target {
		t.drained.Wait()
	}
}

// dispatch delivers queued notifications one at a time, in order, until the outbox is
// empty. A handler that blocks holds back later notifications, never the goroutine
// that raised them.
func (t *tree) dispatch() {
	t.outboxMu.Lock()
	for len(t.outbox) > 0 {
		fn := t.outbox[0]
		t.outbox[0] = nil
		t.outbox = t.outbox[1:]
		t.inHandler = true
		t.outboxMu.Unlock()
		if err := protect(func() error { fn(); return nil }); err != nil {
			t.root.Logger().Error(err)
		}
		t.outboxMu.Lock()
		t.inHandler = false
		t.delivered++
		if t.drained.L != nil {
			t.drained.Broadcast()
		}
	}
	t.outbox = nil
	t.delivering = false
	t.outboxMu.Unlock()
}

// listeners is a small, ordered, thread-safe subscriber list.
type listeners[F any] struct {
	mu    sync.Mutex
	seq   int
	items []listener[F]
}

type listener[F any] struct {
	id int
	fn F
}

func (l *listeners[F]) add(fn F) func() {
	l.mu.Lock()
	l.seq++
	id := l.seq
	l.items = append(l.items, listener[F]{id: id, fn: fn})
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for i, item := range l.items {
			if item.id == id {
				l.items = append(l.items[:i:i], l.items[i+1:]...)
				return
			}
		}
	}
}

func (l *listeners[F]) snapshot() []F {
	l.mu.Lock()
	defer l.mu.Unlock()
	fns := make([]F, len(l.items))
	for i, item := range l.items {
		fns[i] = item.fn
	}
	return fns
}

// Internal exposes the runtime's own lifecycle notifications (upstream's internal/*
// events). Each On method returns a function that removes the subscription.
//
// Plugin, status, service and replace notifications are delivered after the tree lock
// is released, in the order they happened, by the tree's dispatcher goroutine; a
// handler may call back into the context, including disposing or waiting for the
// fiber that raised the notification. Fiber.Wait and Fiber.Dispose return once the
// notifications raised before them have been delivered.
// Commit notifications are delivered synchronously from Fiber.Update.
type Internal struct {
	t       *tree
	plugin  listeners[func(*Fiber)]
	status  listeners[func(*Fiber, FiberState, FiberState)]
	service listeners[func(string, any)]
	commit  listeners[func(*Fiber, any)]
	replace listeners[func(*Fiber)]
}

// OnPlugin subscribes to fibers being created and disposed.
func (i *Internal) OnPlugin(fn func(fiber *Fiber)) (remove func()) { return i.plugin.add(fn) }

// OnStatus subscribes to fiber state changes.
func (i *Internal) OnStatus(fn func(fiber *Fiber, old, next FiberState)) (remove func()) {
	return i.status.add(fn)
}

// OnService subscribes to a service binding being added, changed or withdrawn. value
// is the current binding of the realm that changed, or nil when it was withdrawn.
func (i *Internal) OnService(fn func(name string, value any)) (remove func()) {
	return i.service.add(fn)
}

// OnCommit subscribes to Fiber.Update calls that should be persisted. config is the
// config as it was handed in, before validation, so a loader can write it back.
func (i *Internal) OnCommit(fn func(fiber *Fiber, config any)) (remove func()) {
	return i.commit.add(fn)
}

// OnReplace subscribes to a fiber's component being swapped by Registry.Replace.
func (i *Internal) OnReplace(fn func(fiber *Fiber)) (remove func()) { return i.replace.add(fn) }

func (i *Internal) raisePlugin(fiber *Fiber) {
	for _, fn := range i.plugin.snapshot() {
		i.t.post(func() { fn(fiber) })
	}
}

func (i *Internal) raiseStatus(fiber *Fiber, old, next FiberState) {
	for _, fn := range i.status.snapshot() {
		i.t.post(func() { fn(fiber, old, next) })
	}
}

func (i *Internal) raiseService(name string, value any) {
	for _, fn := range i.service.snapshot() {
		i.t.post(func() { fn(name, value) })
	}
}

func (i *Internal) raiseReplace(fiber *Fiber) {
	for _, fn := range i.replace.snapshot() {
		i.t.post(func() { fn(fiber) })
	}
}

// raiseCommit runs synchronously; it must not be called with the tree lock held.
func (i *Internal) raiseCommit(fiber *Fiber, config any) {
	for _, fn := range i.commit.snapshot() {
		fn(fiber, config)
	}
}
