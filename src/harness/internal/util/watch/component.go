package watch

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Watcher is the service under Key (docs/harness/areas/util-test-support.md,
// "watch"): Watch takes the owning context explicitly, so each watch is an effect
// of the caller's fiber. It is closed when that fiber unloads even if the consumer
// forgets, and only that fiber may close it early (Handle.CloseFrom). Access is
// separate from ownership: any fiber may ask for a watch.
//
// Component provides the real service; watchtest.Manual provides the same key
// for tests, driven by hand.
type Watcher interface {
	Watch(owner *cordis.Context, t Target, changed func(err error)) (Watch, error)
}

// Key is the watch service's key.
var Key = cordis.NewServiceKey[Watcher]("watch")

// Component is util.watch: it starts a Service with the Options it is given as
// config, provides it under Key, and closes it (with every watch on it, whichever
// fiber asked) when its fiber unloads.
var Component = cordis.Define("util.watch", func(ctx *cordis.Context, opt Options) error {
	s := New(opt)
	if _, err := ctx.Effect("watch service", func() (cordis.Disposer, error) { return s.Close, nil }); err != nil {
		s.Close()
		return err
	}
	_, err := Key.Provide(ctx, Owned(s))
	return err
})

// ErrNotOwner is returned by Handle.CloseFrom called from a fiber other than the
// one that asked for the watch.
var ErrNotOwner = errors.New("watch: only the fiber that asked for a watch may close it")

// Engine is a source of watches bound to a standard context: a *Service, or a
// test double.
type Engine interface {
	Watch(ctx context.Context, t Target, changed func(err error)) (Watch, error)
}

// Owned is e as a Watcher: each watch runs under the owner's lifetime and is an
// effect of the owner's fiber.
func Owned(e Engine) Watcher { return owned{e} }

type owned struct{ e Engine }

func (o owned) Watch(owner *cordis.Context, t Target, changed func(err error)) (Watch, error) {
	if owner == nil {
		return nil, ErrBadTarget
	}
	h := &Handle{}
	effect, err := owner.Effect("watch "+t.Path, func() (cordis.Disposer, error) {
		w, err := o.e.Watch(owner.Lifetime(), t, changed)
		if err != nil {
			return nil, err
		}
		h.w = w
		return w.Close, nil
	})
	if err != nil {
		return nil, err
	}
	h.effect = effect
	return h, nil
}

// Handle is a watch owned by one fiber; it is the Watch a Watcher returns.
type Handle struct {
	w      Watch
	effect *cordis.EffectHandle
}

// Mode is the watch's current mode.
func (h *Handle) Mode() Mode { return h.w.Mode() }

// Close closes the watch: the holder of the handle is the fiber that asked for
// it. Use CloseFrom where the caller's fiber should be checked. No callback runs
// once Close returns; closing twice is harmless.
func (h *Handle) Close() error {
	h.effect.Dispose()
	return nil
}

// CloseFrom closes the watch early on behalf of by. From a fiber other than the
// owner (or with no context) it returns ErrNotOwner and the watch keeps running.
func (h *Handle) CloseFrom(by *cordis.Context) error {
	if by == nil || by.Fiber() != h.effect.Fiber() {
		return ErrNotOwner
	}
	return h.Close()
}

// Owner is the fiber the watch belongs to.
func (h *Handle) Owner() *cordis.Fiber { return h.effect.Fiber() }
