package workdir

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// Name is the component's catalogue name.
const Name = "session.workdir"

// ServiceKey is the host's handle on the component: the *Service that attaches
// sessions and moves their directory (Attach, Change). Readers use Key, whose
// Lookup has no setter.
var ServiceKey = cordis.NewServiceKey[*Service]("session.workdir.service")

// Component is session.workdir (C31, cycle C-3): it registers the
// workingDirectory fold in the process's fold registry (views.Key) and provides
// the readers' Lookup under Key and the host's Service under ServiceKey.
//
// It injects only the registry. A registry takes the fold once and none after
// it has started folding (view/duplicate, view/sealed), so the fold, and the
// sessions attached to the service, must outlive every restart of the
// filesystem provider: the service validates through the seam provided under
// fs.Key at each call (liveSeam), and a replaced provider is used from the next
// call without reloading this component. A check made while no provider is
// mounted fails as a seam failure (fs/io), never as the directory's absence.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	reg, err := views.Key.Get(ctx)
	if err != nil {
		return err
	}
	s, err := New(reg, liveSeam{ctx})
	if err != nil {
		return err
	}
	if _, err := Key.Provide(ctx, s); err != nil {
		return err
	}
	_, err = ServiceKey.Provide(ctx, s)
	return err
}, cordis.WithInject(views.Key.Name()))

// liveSeam is the Stater over whichever filesystem provider is mounted under
// fs.Key when a call is made.
type liveSeam struct{ ctx *cordis.Context }

func (l liveSeam) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	seam, ok := fs.Key.Lookup(l.ctx)
	if !ok {
		return fs.Info{}, herr.Newf(fs.CodeIO, "workdir: no filesystem seam is mounted (%s)", fs.Key.Name())
	}
	return seam.Stat(ctx, r)
}
