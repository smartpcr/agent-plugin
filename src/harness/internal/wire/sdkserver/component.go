package sdkserver

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/stdio"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// Name is the catalogue name of Component.
const Name = "wire.sdk"

// Component is wire.sdk (C49): the SDK server over the streams wire.stdio owns,
// one connection for the life of the fiber. initialize waits for the tree's ready;
// routes are checked against llm.runtime's, which it injects: it loads only once
// the runtime is provided, so no connection is served with unchecked routes. When the client's transport ends the
// owner asks the App to stop with InputClosed; disposing the fiber ends the
// connection and waits for it to close.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	reg, err := agent.Agents.Get(ctx)
	if err != nil {
		return err
	}
	sessions, err := session.Key.Get(ctx)
	if err != nil {
		return err
	}
	owner, err := stdio.Key.Get(ctx)
	if err != nil {
		return err
	}
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	o := Options{Owner: ctx, Agents: reg, Sessions: sessions, Routes: rt, Info: protocol.Implementation{Name: "dsh"}}
	if app, ok := launch.AppKey.Lookup(ctx); ok {
		o.Ready = app.Ready()
	}
	srv, err := New(o)
	if err != nil {
		return err
	}
	_, err = ctx.Effect("wire.sdk.serve", func() (cordis.Disposer, error) {
		serve, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := srv.Serve(serve, owner.Input(), owner.Output(), jsonrpc.Options{}); err != nil && serve.Err() == nil {
				ctx.Logger().Warnf("wire.sdk: the connection ended: %v", err)
			}
			if serve.Err() == nil {
				owner.InputClosed()
			}
		}()
		return func() error { cancel(); <-done; return nil }, nil
	})
	return err
}, cordis.WithInject(agent.Agents.Name(), session.Key.Name(), stdio.Key.Name(), llm.RuntimeKey.Name()))
