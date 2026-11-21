package egress

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Name is the component's catalogue name.
const Name = "util.egress"

// Key is the egress service's key.
var Key = cordis.NewServiceKey[*Service]("egress")

// LaunchKey is the launch lookup boot provides on the root: the trusted view of
// the launch snapshot, which Resolve reads the proxy names through.
var LaunchKey = cordis.NewServiceKey[Lookup]("egress.launch")

// Component is util.egress: it resolves the service from the launch lookup and
// the Config it is given, warns once per refused proxy value, provides it under
// Key, and closes its idle connections when its fiber unloads. An unreadable
// certificate file fails the load.
var Component = cordis.Define(Name, func(ctx *cordis.Context, c Config) error {
	lookup, _ := LaunchKey.Lookup(ctx)
	s, diags, err := Resolve(lookup, c)
	if err != nil {
		return err
	}
	for _, d := range diags {
		ctx.Logger().Warnf("%s", d)
	}
	if _, err := ctx.Effect("egress service", func() (cordis.Disposer, error) {
		return func() error { s.CloseIdleConnections(); return nil }, nil
	}); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, s)
	return err
})
