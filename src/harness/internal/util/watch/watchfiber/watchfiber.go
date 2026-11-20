// Package watchfiber is the iteration-4 entry point to the ownership rules of
// util/watch, kept as a thin shim. The rules now live in package watch itself,
// behind the specified seam: component util.watch provides watch.Key, whose
// Watcher takes the owning *cordis.Context (watch.Owned, watch.Handle).
//
// Deprecated: use watch.Component and watch.Key, or watch.Owned.
package watchfiber

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// ErrNotOwner is watch.ErrNotOwner.
var ErrNotOwner = watch.ErrNotOwner

// Own makes ctx's fiber the owner of s: s is closed, with every watch on it,
// when that fiber unloads or the returned handle is disposed.
func Own(ctx *cordis.Context, s *watch.Service) (*cordis.EffectHandle, error) {
	return ctx.Effect("watch service", func() (cordis.Disposer, error) {
		return s.Close, nil
	})
}

// Handle is a watch owned by one fiber.
type Handle struct{ h *watch.Handle }

// Watch is watch.Owned(s).Watch(ctx, t, changed).
func Watch(ctx *cordis.Context, s *watch.Service, t watch.Target, changed func(error)) (*Handle, error) {
	w, err := watch.Owned(s).Watch(ctx, t, changed)
	if err != nil {
		return nil, err
	}
	return &Handle{w.(*watch.Handle)}, nil
}

// Mode is the watch's current mode.
func (h *Handle) Mode() watch.Mode { return h.h.Mode() }

// Close is watch.Handle.CloseFrom.
func (h *Handle) Close(by *cordis.Context) error { return h.h.CloseFrom(by) }
