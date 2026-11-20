package cordis

import "sync"

// tree is the state shared by every context of one root.
//
// mu guards all fiber, effect, service, registry and event-hook state of the tree.
// It is never held while component code runs. Anything that must call out while the
// state is being changed (internal notifications, log lines) is posted to an ordered
// outbox and delivered after mu is released.
//
// Delivery is owned by the tree's dispatcher, a goroutine started on demand, so the
// goroutine doing lifecycle work (an acquisition, an unload, the root's teardown)
// never runs a handler as a side effect, and it delivers one notification at a time. A
// lifecycle call that must observe delivery (Fiber.Wait, Fiber.Dispose) uses a barrier
// instead: while only handlers that started before the call are running, it delivers
// queued notifications itself, in order, up to its target, so it never waits behind a
// handler that is blocked, possibly on that very call. It waits only for a delivery of
// a notification raised during its own call.
type tree struct {
	mu sync.Mutex

	outboxMu    sync.Mutex
	outbox      []notification
	posted      uint64              // sequence number of the last posted notification
	running     map[uint64]struct{} // notifications being delivered, by sequence
	dispatching bool                // the dispatcher goroutine is running
	delivered   sync.Cond           // signalled on outboxMu when a delivery returns

	fiberUID int

	root     *Context
	store    *store
	registry *Registry
	events   *eventHub
	loggers  *LoggerService
	internal *Internal
}

type notification struct {
	seq uint64
	fn  func()
}

func (t *tree) lock() { t.mu.Lock() }

// unlock releases the tree lock and wakes the dispatcher for queued notifications.
func (t *tree) unlock() {
	t.mu.Unlock()
	t.flush()
}

// unlockQuiet releases the tree lock without waking the dispatcher. Read-only
// operations use it; whoever posted a notification wakes the dispatcher.
func (t *tree) unlockQuiet() { t.mu.Unlock() }

// post queues fn for delivery outside the tree lock and returns its sequence number.
// Notifications start in the order they were posted.
func (t *tree) post(fn func()) uint64 {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	t.posted++
	t.outbox = append(t.outbox, notification{seq: t.posted, fn: fn})
	return t.posted
}

// mark returns the sequence number of the last posted notification.
func (t *tree) mark() uint64 {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	return t.posted
}

// flush starts the dispatcher if notifications are queued and it is not running. It
// never delivers on the calling goroutine, and must not be called with the tree lock
// held.
func (t *tree) flush() {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	if t.dispatching || len(t.outbox) == 0 {
		return
	}
	t.dispatching = true
	go t.dispatch()
}

// dispatch delivers queued notifications one at a time: it starts the next only once
// no delivery is running, so handlers never overlap on its account.
func (t *tree) dispatch() {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	for len(t.outbox) > 0 {
		if len(t.running) > 0 {
			t.condLocked().Wait()
			continue
		}
		t.deliverHeadLocked()
	}
	t.dispatching = false
}

// barrier returns once every notification posted up to hi has started, and every one
// posted after lo has also returned. A caller passes as lo the sequence number
// observed when its call began. While every running delivery started before that
// (the handler the caller may be running inside, or an unrelated handler that is
// blocked), the caller delivers queued notifications up to hi itself, in order, so it
// never waits for a handler that may be waiting for it. Otherwise it waits for the
// running delivery to return. It must not be called with the tree lock held.
func (t *tree) barrier(lo, hi uint64) {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	for {
		busy := t.runningLocked(lo, hi)
		if !busy && len(t.outbox) > 0 && t.outbox[0].seq <= hi {
			t.deliverHeadLocked()
			continue
		}
		if !busy && (len(t.outbox) == 0 || t.outbox[0].seq > hi) {
			return
		}
		t.condLocked().Wait()
	}
}

// runningLocked reports whether a notification posted after lo and up to hi is being
// delivered. Later notifications never hold a barrier back: they are not its target.
func (t *tree) runningLocked(lo, hi uint64) bool {
	for seq := range t.running {
		if seq > lo && seq <= hi {
			return true
		}
	}
	return false
}

func (t *tree) condLocked() *sync.Cond {
	if t.delivered.L == nil {
		t.delivered.L = &t.outboxMu
	}
	return &t.delivered
}

// deliverHeadLocked runs the oldest queued notification with outboxMu released.
func (t *tree) deliverHeadLocked() {
	n := t.outbox[0]
	t.outbox[0] = notification{}
	t.outbox = t.outbox[1:]
	if t.running == nil {
		t.running = map[uint64]struct{}{}
	}
	t.running[n.seq] = struct{}{}
	t.outboxMu.Unlock()
	if err := protect(func() error { n.fn(); return nil }); err != nil {
		t.root.Logger().Error(err)
	}
	t.outboxMu.Lock()
	delete(t.running, n.seq)
	t.condLocked().Broadcast()
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
// is released, in the order they happened, by the tree's dispatcher goroutine, one at a
// time. Fiber.Wait and Fiber.Dispose return once the notifications raised during the
// call have been delivered, delivering queued ones on the calling goroutine when the
// dispatcher is busy with a handler that started before the call, so a handler may
// dispose or wait for the fiber that raised it, or for a load that creates and waits
// for children. Such a delivery can re-enter a handler that called Wait or Dispose.
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

// announcePlugin posts the creation notification of fiber as one delivery that runs
// every creation handler, each guarded on its own, and then then, and returns its
// sequence number. then therefore runs only once every handler has returned, on
// whichever goroutine delivers the notification.
func (i *Internal) announcePlugin(fiber *Fiber, then func()) uint64 {
	handlers := i.plugin.snapshot()
	return i.t.post(func() {
		for _, fn := range handlers {
			if err := protect(func() error { fn(fiber); return nil }); err != nil {
				i.t.root.Logger().Error(err)
			}
		}
		then()
	})
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
