// Package watchfiber gives util/watch the ownership rules of a cordis component
// (docs/harness/libs/chokidar.md, "Ownership"). The service's fiber owns the
// Service: Own closes it when that fiber unloads. Each watch is an effect on the
// fiber that asked for it, so it closes when that consumer unloads even if the
// consumer forgets, and only that fiber may close it early. Access is separate
// from ownership: any fiber may ask a service for a watch.
//
// util/watch itself stays a leaf package with no cordis dependency; this adapter
// is the only place the two meet.
package watchfiber

import (
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// ErrNotOwner is returned by Handle.Close called from a fiber other than the one
// that asked for the watch.
var ErrNotOwner = errors.New("watchfiber: only the fiber that asked for a watch may close it")

// Own makes ctx's fiber the owner of s: s is closed, with every watch on it,
// when that fiber unloads or the returned handle is disposed.
func Own(ctx *cordis.Context, s *watch.Service) (*cordis.EffectHandle, error) {
	return ctx.Effect("watch service", func() (cordis.Disposer, error) {
		return s.Close, nil
	})
}

// Handle is a watch owned by one fiber.
type Handle struct {
	w      watch.Watch
	effect *cordis.EffectHandle
}

// Watch starts a watch on s owned by ctx's fiber. It runs under the fiber's
// lifetime and is registered as an effect, so it is closed when the fiber
// unloads. changed behaves as for watch.Service.Watch.
func Watch(ctx *cordis.Context, s *watch.Service, t watch.Target, changed func(error)) (*Handle, error) {
	h := &Handle{}
	effect, err := ctx.Effect("watch "+t.Path, func() (cordis.Disposer, error) {
		w, err := s.Watch(ctx.Lifetime(), t, changed)
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

// Mode is the watch's current mode.
func (h *Handle) Mode() watch.Mode { return h.w.Mode() }

// Close closes the watch early. by is the caller's context: from a fiber other
// than the owner it returns ErrNotOwner and the watch keeps running. Closing twice
// is harmless; no callback runs once Close returns.
func (h *Handle) Close(by *cordis.Context) error {
	if by == nil || by.Fiber() != h.effect.Fiber() {
		return ErrNotOwner
	}
	h.effect.Dispose()
	return nil
}
