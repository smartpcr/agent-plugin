package cordis

// Disposer releases one resource. Its error, like a panic, is logged rather than
// propagated, and never stops the remaining cleanups.
type Disposer func() error

// EffectSeq is a generator effect: it yields one Disposer per acquired resource and
// returns when it is done, or as soon as yield returns false.
type EffectSeq func(yield func(Disposer) bool) error

// EffectHandle is a resource owned by a fiber. Its cleanups run, in reverse order of
// collection, when the handle is disposed: explicitly, or when its fiber unloads.
type EffectHandle struct {
	fiber *Fiber
	label string
	// released is closed once the handle's last cleanup has returned.
	released chan struct{}

	// guarded by the tree lock
	alive     bool
	disposers []Disposer
	pending   *task
}

func newEffectHandle(f *Fiber, label string) *EffectHandle {
	return &EffectHandle{fiber: f, label: label, alive: true, released: make(chan struct{})}
}

// Label returns the label the effect was created with.
func (h *EffectHandle) Label() string { return h.label }

// Fiber returns the fiber that owns the effect.
func (h *EffectHandle) Fiber() *Fiber { return h.fiber }

// Alive reports whether the effect has not been disposed yet.
func (h *EffectHandle) Alive() bool {
	h.fiber.t.lock()
	defer h.fiber.t.unlockQuiet()
	return h.alive
}

// Disposer returns a Disposer that disposes the handle, so it can be yielded from
// another effect and released together with it.
func (h *EffectHandle) Disposer() Disposer {
	return func() error {
		h.Dispose()
		return nil
	}
}

// Dispose releases the effect: it waits for its acquisition (also a synchronous one
// running on another goroutine), then runs the collected cleanups in reverse order,
// logging their failures. A second caller, concurrent or later, waits until the first
// caller's cleanups have returned. A cleanup or acquisition must not dispose its own
// handle, or wait for something that does.
func (h *EffectHandle) Dispose() {
	t := h.fiber.t
	t.lock()
	if !h.alive {
		t.unlock()
		<-h.released
		return
	}
	h.alive = false
	pending := h.pending
	h.fiber.detachLocked(h)
	t.unlock()

	if pending != nil {
		<-pending.done // a failure is reported by the runner
	}

	t.lock()
	disposers := h.disposers
	h.disposers = nil
	t.unlock()

	for i := len(disposers) - 1; i >= 0; i-- {
		h.release(disposers[i])
	}

	t.lock()
	h.fiber.settleLocked(h)
	t.unlock()
	close(h.released)
}

func (h *EffectHandle) String() string { return "Effect <" + h.label + ">" }

// collect adds a cleanup to the effect. On a disposed effect the cleanup runs at once.
// It reports whether the effect is still alive. It runs on the acquisition's stack and
// leaves waking the notification dispatcher to run and spawn, once the acquisition
// has ended.
func (h *EffectHandle) collect(dispose Disposer) bool {
	t := h.fiber.t
	t.lock()
	if !h.alive {
		t.unlockQuiet()
		if dispose != nil {
			h.release(dispose)
		}
		return false
	}
	if dispose != nil {
		h.disposers = append(h.disposers, dispose)
	}
	t.unlockQuiet()
	return true
}

func (h *EffectHandle) release(dispose Disposer) {
	if err := protect(dispose); err != nil {
		h.fiber.ctx.Logger().Error(err)
	}
}

// spawn runs fn on a new goroutine as the effect's pending acquisition, the one run
// attached to the handle before publishing it.
func (h *EffectHandle) spawn(fn func() error) *task {
	pending := h.pending
	go func() {
		pending.finish(protect(fn))
		h.fiber.t.flush() // deliver what the acquisition raised, now that it is complete
	}()
	return pending
}

// task is the outcome of work running on another goroutine.
type task struct {
	done chan struct{}
	err  error
}

func (t *task) finish(err error) {
	t.err = err
	close(t.done)
}
