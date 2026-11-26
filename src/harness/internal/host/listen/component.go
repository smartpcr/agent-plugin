package listen

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
)

// Name is the component's catalogue name.
const Name = "host.listen"

// Key is the listener service.
var Key = cordis.NewServiceKey[Listener]("host.listen")

// Component is host.listen: it starts the listener as an effect of its fiber and
// provides it under Key; unloading it closes the listener. Stream requests wait for
// the boot App's Ready; with no App in the tree (a test) they are ready at once.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	ready := make(chan struct{})
	var gate <-chan struct{} = ready
	if app, ok := launch.AppKey.Lookup(ctx); ok {
		gate = app.Ready()
	} else {
		close(ready)
	}
	var srv *Server
	_, err := ctx.Effect("listener", func() (cordis.Disposer, error) {
		s, err := Start(ctx, cfg, gate)
		if err != nil {
			return nil, err
		}
		srv = s
		return s.Close, nil
	})
	if err != nil {
		return err
	}
	_, err = Key.Provide(ctx, Listener(srv))
	return err
})
