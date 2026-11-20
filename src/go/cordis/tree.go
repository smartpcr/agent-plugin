package cordis

import (
	"runtime"
	"sync"
)

// tree is the state shared by every context of one root.
//
// mu guards all fiber, effect, service, registry and event-hook state of the tree.
// It is never held while component code runs. Anything that must call out while the
// state is being changed (internal notifications, log lines) is posted to an ordered
// outbox and delivered after mu is released, by whichever goroutine releases it first.
type tree struct {
	mu sync.Mutex

	outboxMu   sync.Mutex
	outbox     []func()
	delivering bool
	// acquiring counts the effect acquisitions running on each goroutine (by id);
	// guarded by outboxMu. Such a goroutine never delivers notifications itself.
	acquiring map[uint64]int

	fiberUID int

	root     *Context
	store    *store
	registry *Registry
	events   *eventHub
	loggers  *LoggerService
	internal *Internal
}

func (t *tree) lock() { t.mu.Lock() }

// unlock releases the tree lock and delivers queued notifications.
func (t *tree) unlock() {
	t.mu.Unlock()
	t.flush()
}

// unlockQuiet releases the tree lock without delivering notifications. Read-only
// operations use it, so reading state never runs handlers on the caller's goroutine;
// whoever posted a notification delivers it.
func (t *tree) unlockQuiet() { t.mu.Unlock() }

// post queues fn for delivery outside the tree lock. Notifications are delivered in
// the order they were posted.
func (t *tree) post(fn func()) {
	t.outboxMu.Lock()
	t.outbox = append(t.outbox, fn)
	t.outboxMu.Unlock()
}

// flush delivers queued notifications. Only one goroutine delivers at a time, so
// notifications posted while a handler runs are delivered after it returns rather
// than re-entering it. It must not be called with the tree lock held.
//
// A goroutine running an effect acquisition hands delivery to a new goroutine: a
// handler may dispose the effect's fiber, which waits for that acquisition, so it must
// not run on the acquisition's stack.
func (t *tree) flush() {
	t.outboxMu.Lock()
	if t.delivering {
		t.outboxMu.Unlock()
		return
	}
	if len(t.acquiring) > 0 && t.acquiring[goid()] > 0 {
		pending := len(t.outbox) > 0
		t.outboxMu.Unlock()
		if pending {
			go t.flush()
		}
		return
	}
	t.delivering = true
	for len(t.outbox) > 0 {
		fn := t.outbox[0]
		t.outbox[0] = nil
		t.outbox = t.outbox[1:]
		t.outboxMu.Unlock()
		if err := protect(func() error { fn(); return nil }); err != nil {
			t.root.Logger().Error(err)
		}
		t.outboxMu.Lock()
	}
	t.outbox = nil
	t.delivering = false
	t.outboxMu.Unlock()
}

// acquire runs fn as an effect acquisition on the calling goroutine; see flush.
func (t *tree) acquire(fn func()) {
	id := goid()
	t.outboxMu.Lock()
	if t.acquiring == nil {
		t.acquiring = map[uint64]int{}
	}
	t.acquiring[id]++
	t.outboxMu.Unlock()
	defer func() {
		t.outboxMu.Lock()
		if t.acquiring[id]--; t.acquiring[id] == 0 {
			delete(t.acquiring, id)
		}
		t.outboxMu.Unlock()
	}()
	fn()
}

// goid returns the id of the calling goroutine, from the header of its stack trace
// ("goroutine 123 [running]:").
func goid() uint64 {
	var buf [32]byte
	n := runtime.Stack(buf[:], false)
	var id uint64
	for _, c := range buf[len("goroutine "):n] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
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
// is released, in the order they happened; a handler may call back into the context.
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
