package cordis

import (
	"sync"
	"sync/atomic"
)

// tree is the state shared by every context of one root.
//
// mu guards all fiber, effect, service, registry and event-hook state of the tree.
// It is never held while component code runs. Anything that must call out while the
// state is being changed (internal notifications, log lines) is posted to an ordered
// outbox and delivered after mu is released.
//
// Delivery is owned by the tree's dispatcher, a goroutine started on demand. It runs
// each notification on a fresh goroutine, never on the goroutine doing lifecycle work
// (an acquisition, an unload, the root's teardown), and starts a notification only
// once every earlier one has returned or is parked: blocked in a lifecycle wait of
// this tree (Fiber.Wait, Fiber.Dispose, an unload or acquisition it waits for). So
// handlers run one at a time, in order, and a handler that waits for lifecycle work
// never holds back the notifications that work raises. A lifecycle call that must
// observe delivery (Fiber.Wait, Fiber.Dispose) waits, through barrier, until the
// notifications raised during the call have returned or parked.
type tree struct {
	mu sync.Mutex

	outboxMu    sync.Mutex
	outbox      []notification
	posted      uint64               // sequence number of the last posted notification
	running     map[uint64]*delivery // notifications being delivered, by sequence
	byGoroutine map[uint64]*delivery // the same deliveries, by the goroutine running them
	claims      map[uint64]int       // running deliveries claiming each goroutine identity
	dispatching bool                 // the dispatcher goroutine is running
	changed     sync.Cond            // signalled on outboxMu when a delivery returns or parks

	fiberUID int
	// unattributed is set once a delivery could not be attributed to its goroutine.
	unattributed atomic.Bool
	// cutShort is set once a barrier returned early because its caller may be running
	// an unattributed delivery.
	cutShort atomic.Bool

	root     *Context
	store    *store
	registry *Registry
	events   *eventHub
	loggers  *LoggerService
	internal *Internal

	// strict is set by WithStrictConfig: JSON config is decoded strictly.
	strict bool
	// configHooks are the tree's OnConfig hooks in registration order; guarded by mu.
	configHooks []*configHook
}

// TreeOption configures a tree created by New.
type TreeOption func(*treeOptions)

type treeOptions struct {
	strict       bool
	exporters    []Exporter
	setExporters bool
}

// WithStrictConfig makes the tree decode JSON config strictly: a key the component's
// config type does not have, or data after the first JSON value, fails the load with
// a ConfigError naming it. A component declared with LenientConfig is exempt. Config
// handed in as a typed Go value is unaffected.
func WithStrictConfig() TreeOption {
	return func(o *treeOptions) { o.strict = true }
}

// WithExporters creates the tree's logger with exactly the given exporters, and with
// none when none is given, in place of the default console exporter. The tree never
// closes them: the creator owns the exporters it passes.
func WithExporters(exporters ...Exporter) TreeOption {
	return func(o *treeOptions) {
		o.exporters = append(o.exporters, exporters...)
		o.setExporters = true
	}
}

type notification struct {
	seq uint64
	fn  func()
}

// delivery is a notification being run; parked counts the lifecycle waits its
// goroutine is blocked in. unattributed is set when the delivery is not registered
// under its goroutine, so its waits cannot park it.
type delivery struct {
	seq          uint64
	parked       int
	unattributed bool
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

func (t *tree) dispatch() {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	for len(t.outbox) > 0 {
		if t.busyLocked(0, ^uint64(0)) {
			t.condLocked().Wait()
			continue
		}
		n := t.outbox[0]
		t.outbox[0] = notification{}
		t.outbox = t.outbox[1:]
		if t.running == nil {
			t.running = map[uint64]*delivery{}
			t.byGoroutine = map[uint64]*delivery{}
			t.claims = map[uint64]int{}
		}
		d := &delivery{seq: n.seq}
		t.running[n.seq] = d
		started := make(chan struct{})
		go t.deliver(n, d, started)
		// Wait until the delivery is registered under its goroutine, so a park on it
		// is never missed.
		t.outboxMu.Unlock()
		<-started
		t.outboxMu.Lock()
	}
	t.dispatching = false
}

// deliver runs a notification. It registers the delivery under its goroutine's
// identity so a lifecycle wait of the handler parks it. A delivery whose identity is
// unavailable is not registered. An identity claimed by two running deliveries is
// ambiguous: it is unregistered for both until every claimant has returned, so neither
// can park the other. A handler's waits then never park its delivery, which keeps the
// order of delivery but holds back later notifications while the handler waits. As
// those notifications cannot start until the handler returns, a barrier called from
// such a handler does not wait for them: see settle.
func (t *tree) deliver(n notification, d *delivery, started chan<- struct{}) {
	g, identified := goroutineID()
	t.outboxMu.Lock()
	attributed := identified && t.claimLocked(g, d)
	d.unattributed = !attributed
	t.outboxMu.Unlock()
	close(started)
	if !attributed && t.unattributed.CompareAndSwap(false, true) {
		t.root.Logger().Warnf("cannot identify the goroutine delivering a notification; " +
			"a handler that waits for lifecycle work holds back later notifications, " +
			"and its barriers do not wait for them")
	}
	if err := protect(func() error { n.fn(); return nil }); err != nil {
		t.root.Logger().Error(err)
	}
	t.outboxMu.Lock()
	if identified {
		t.releaseClaimLocked(g)
	}
	delete(t.running, n.seq)
	t.condLocked().Broadcast()
	t.outboxMu.Unlock()
}

// claimLocked claims identity g for delivery d and reports whether d is attributed to
// it, that is whether d is its only claimant. A second claimant withdraws the
// registration of the first.
func (t *tree) claimLocked(g uint64, d *delivery) bool {
	t.claims[g]++
	if t.claims[g] > 1 {
		if prev := t.byGoroutine[g]; prev != nil {
			prev.unattributed = true
		}
		delete(t.byGoroutine, g)
		return false
	}
	t.byGoroutine[g] = d
	return true
}

// releaseClaimLocked drops a claim on identity g. An identity that was ambiguous stays
// unregistered until its last claimant has released it.
func (t *tree) releaseClaimLocked(g uint64) {
	if t.claims[g]--; t.claims[g] == 0 {
		delete(t.claims, g)
		delete(t.byGoroutine, g)
	}
}

// busyLocked reports whether a notification posted after lo and up to hi is being
// delivered and not parked.
func (t *tree) busyLocked(lo, hi uint64) bool {
	for seq, d := range t.running {
		if seq > lo && seq <= hi && d.parked == 0 {
			return true
		}
	}
	return false
}

// barrier returns once every notification posted after lo and up to hi has started,
// and has returned or parked. A caller passes as lo the sequence number observed when
// its call began, and as hi the one observed once its lifecycle work is done. The
// wait parks the caller's own delivery, if it runs in a handler, so the dispatcher
// can deliver the notifications waited for. It must not be called with the tree lock
// held.
//
// A caller that may be running an unattributed delivery (its goroutine identity is
// unavailable or ambiguous) cannot park it. If the dispatcher is held back only by
// unattributed deliveries, one of which may be the caller's own, the notifications
// still queued cannot start before the caller returns, so the barrier returns once
// those already started have returned or parked, and logs this once.
func (t *tree) barrier(lo, hi uint64) { t.settle(lo, hi, false) }

// barrierReturned is barrier, except that it also waits for parked notifications to
// return.
func (t *tree) barrierReturned(lo, hi uint64) { t.settle(lo, hi, true) }

func (t *tree) settle(lo, hi uint64, returned bool) {
	if hi <= lo {
		return
	}
	defer t.park()()
	if t.waitSettled(lo, hi, returned) && t.cutShort.CompareAndSwap(false, true) {
		t.root.Logger().Warnf("a lifecycle wait in a notification handler that cannot be " +
			"identified returned before later notifications were delivered")
	}
}

// waitSettled waits for the condition of settle and reports whether it returned
// early, without waiting for queued notifications, because the caller may be running
// the unattributed delivery that holds back the dispatcher.
func (t *tree) waitSettled(lo, hi uint64, returned bool) (cutShort bool) {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	fallback := t.callerUnattributedLocked()
	for {
		if !t.busyLocked(lo, hi) && !(returned && t.runningLocked(lo, hi)) {
			if len(t.outbox) == 0 || t.outbox[0].seq > hi {
				return false
			}
			if fallback && t.stalledOnUnattributedLocked(lo, hi) {
				return true
			}
		}
		t.condLocked().Wait()
	}
}

// callerUnattributedLocked reports whether the calling goroutine may be running a
// delivery that is not registered under it: its identity is unavailable, or claimed
// by more than one running delivery.
func (t *tree) callerUnattributedLocked() bool {
	g, ok := goroutineID()
	return !ok || t.claims[g] > 1
}

// stalledOnUnattributedLocked reports whether the dispatcher is held back only by
// unparked, unattributed deliveries outside lo and hi.
func (t *tree) stalledOnUnattributedLocked(lo, hi uint64) bool {
	stalled := false
	for seq, d := range t.running {
		if d.parked > 0 {
			continue
		}
		if (seq > lo && seq <= hi) || !d.unattributed {
			return false
		}
		stalled = true
	}
	return stalled
}

// park marks the calling goroutine's delivery, if it is running one, as blocked in a
// lifecycle wait until the returned function is called, so the dispatcher can start
// the next notification meanwhile.
func (t *tree) park() (unpark func()) {
	t.outboxMu.Lock()
	defer t.outboxMu.Unlock()
	if len(t.byGoroutine) == 0 {
		return func() {}
	}
	g, ok := goroutineID()
	if !ok {
		return func() {}
	}
	d := t.byGoroutine[g]
	if d == nil {
		return func() {}
	}
	d.parked++
	t.condLocked().Broadcast()
	return func() {
		t.outboxMu.Lock()
		d.parked--
		t.outboxMu.Unlock()
	}
}

// await blocks until ch is closed, parking the calling goroutine's delivery meanwhile.
func (t *tree) await(ch <-chan struct{}) {
	select {
	case <-ch:
		return
	default:
	}
	defer t.park()()
	<-ch
}

func (t *tree) runningLocked(lo, hi uint64) bool {
	for seq := range t.running {
		if seq > lo && seq <= hi {
			return true
		}
	}
	return false
}

func (t *tree) condLocked() *sync.Cond {
	if t.changed.L == nil {
		t.changed.L = &t.outboxMu
	}
	return &t.changed
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
// is released, in the order they happened, by the tree's dispatcher, never on the
// stack of a lifecycle call. A notification starts once every earlier one has returned
// or is parked in a lifecycle wait (Fiber.Wait, Fiber.Dispose, EffectHandle.Dispose),
// so a handler may dispose or wait for the fiber that raised it, or for work that
// raises further notifications. A parked handler resumes concurrently with later ones.
// A handler blocked outside cordis holds back every later notification. Fiber.Wait and
// Fiber.Dispose return once the notifications raised during the call have returned or
// parked; in a handler whose goroutine cannot be identified, they do not wait for
// notifications that cannot start before the handler returns.
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
