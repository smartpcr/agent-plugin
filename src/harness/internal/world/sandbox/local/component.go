package local

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Name is the catalogue name of Component.
const Name = "sandbox.local"

// Component is sandbox.local: the local sandbox seam over this platform's chain
// (Platform), probed on load and provided under sandbox.ServiceKey. Its config is
// Config; an empty Home is the launch's home (home.Key), when there is one. On
// unload the service is closed, which removes the canary directory it created.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	if cfg.Home == "" {
		if layout, ok := home.Key.Lookup(ctx); ok {
			cfg.Home = layout.Root()
		}
	}
	svc, err := New(ctx.Lifetime(), cfg, Platform())
	if err != nil {
		return err
	}
	if _, err := ctx.Effect("sandbox service", func() (cordis.Disposer, error) {
		return svc.Close, nil
	}); err != nil {
		_ = svc.Close()
		return err
	}
	_, err = sandbox.ServiceKey.Provide(ctx, svc)
	return err
})
