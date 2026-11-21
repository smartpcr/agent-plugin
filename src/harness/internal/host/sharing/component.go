package sharing

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// Name is the component's catalogue name.
const Name = "host.sharing"

// Key is the launch's gate. Boot provides it on the root before the tree loads
// (built over the launch environment, so DSH_SHARING_OFF is read once), so every
// fiber and the boot audit read the same gate.
var Key = cordis.NewServiceKey[*Gate]("host.sharing")

// Component is host.sharing: the entry whose config is `sharing.Config`, the
// visible settings `sharing.off` and `sharing.kinds.<kind>.enabled|endpoint`. On
// load it applies its config to the gate under Key. A live config change is
// accepted in place: the gate is reconfigured without restarting the fiber, so
// (per Gate.Configure) only kinds whose setting or endpoint changed lose their
// permits and unchanged kinds are never briefly denied. On unload (the entry
// removed) it puts the gate back to the default, every kind off, which withdraws
// every permit the config allowed. One entry configures the gate: a second one
// fails with sharing/claimed.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	g, err := Key.Get(ctx)
	if err != nil {
		return err
	}
	if err := g.claim(); err != nil {
		return err
	}
	_, err = ctx.Effect("sharing settings", func() (cordis.Disposer, error) {
		g.Configure(cfg)
		return func() error {
			g.Configure(Config{})
			g.unclaim()
			return nil
		}, nil
	})
	if err != nil {
		g.unclaim()
		return err
	}
	_, err = cordis.AcceptUpdate(ctx, func(next Config) { g.Configure(next) })
	return err
})