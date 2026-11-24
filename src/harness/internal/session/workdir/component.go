package workdir

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// Name is the component's catalogue name.
const Name = "session.workdir"

// ServiceKey is the host's handle on the component: the *Service that attaches
// sessions and moves their directory (Attach, Change). Readers use Key, whose
// Lookup has no setter.
var ServiceKey = cordis.NewServiceKey[*Service]("session.workdir.service")

// Component is session.workdir (C31, cycle C-3): it registers the
// workingDirectory fold in the process's fold registry (views.Key) and validates
// through the filesystem seam (fs.Key), then provides the readers' Lookup under
// Key and the host's Service under ServiceKey. It mounts before the first
// session is folded: a registry that has started folding refuses the fold
// (view/sealed), and a registry that already holds it refuses a second
// (view/duplicate), so a reload needs a fresh registry.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	reg, err := views.Key.Get(ctx)
	if err != nil {
		return err
	}
	seam, err := fs.Key.Get(ctx)
	if err != nil {
		return err
	}
	s, err := New(reg, seam)
	if err != nil {
		return err
	}
	if _, err := Key.Provide(ctx, s); err != nil {
		return err
	}
	_, err = ServiceKey.Provide(ctx, s)
	return err
}, cordis.WithInject(views.Key.Name(), fs.Key.Name()))
