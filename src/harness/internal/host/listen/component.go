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
// the Ready of the boot App; with no App in the tree (a test) they are ready at once.
// The peer rule asks the boot tree's process registry (launch.ProcessesKey); with
// none it is not applied.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	ready := make(chan struct{})
	var gate <-chan struct{} = ready
	if app, ok := launch.AppKey.Lookup(ctx); ok {
		gate = app.Ready()
	} else {
		close(ready)
	}
	var trees Trees
	if procs, ok := launch.ProcessesKey.Lookup(ctx); ok && procs != nil {
		trees = procs
	}
	if treesHook != nil {
		trees = treesHook
	}
	var srv *Server
	_, err := ctx.Effect("listener", func() (cordis.Disposer, error) {
		s, err := StartWithTrees(ctx, cfg, gate, trees)
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
