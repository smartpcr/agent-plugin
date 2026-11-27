package stdio

import (
	"errors"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
)

// Name is the catalogue name of Component.
const Name = "wire.stdio"

// Key is the service under which wire.stdio provides the stream owner.
var Key = cordis.NewServiceKey[*Owner]("wire.stdio")

// Settings is wire.stdio's configuration.
type Settings struct {
	// WriteStall bounds one write to the client; unset is DefaultWriteStall.
	// A client that stops reading for longer ends the launch with status 125.
	WriteStall schema.Duration `json:"writeStall,omitempty"`
}

// Component is wire.stdio (C48): the owner of the client's streams, asking the
// launch's App to stop on the client's first ending. It claims the process's
// standard input and output, unless the launcher handed the tree a stream pair
// (launch.StdioKey), which it attaches to instead.
var Component = cordis.Define(Name, func(ctx *cordis.Context, set Settings) error {
	app, ok := launch.AppKey.Lookup(ctx)
	if !ok {
		return errors.New("wire.stdio: the tree provides no launch App to stop")
	}
	if set.WriteStall < 0 {
		return errors.New("wire.stdio: writeStall is negative")
	}
	cfg := Config{App: app, WriteStall: time.Duration(set.WriteStall)}
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
