package stdio

import (
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
)

// Name is the catalogue name of Component.
const Name = "wire.stdio"

// Key is the service under which wire.stdio provides the stream owner.
var Key = cordis.NewServiceKey[*Owner]("wire.stdio")

// Component is wire.stdio (C48): the owner of the client's streams, asking the
// launch's App to stop on the client's first ending. It claims the process's
// standard input and output, unless the launcher handed the tree a stream pair
// (launch.StdioKey), which it attaches to instead.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	app, ok := launch.AppKey.Lookup(ctx)
	if !ok {
		return errors.New("wire.stdio: the tree provides no launch App to stop")
	}
	cfg := Config{App: app}
	var (
		o   *Owner
		err error
	)
	if s, ok := launch.StdioKey.Lookup(ctx); ok {
		o, err = Attach(cfg, s.In, s.Out)
	} else {
		o, err = Claim(cfg)
	}
	if err != nil {
		return err
	}
	_, err = Key.Provide(ctx, o)
	return err
})
