package sdkserver

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
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
// the runtime is provided, so no connection is served with unchecked routes. When the client's transport ends
// cleanly the owner asks the App to stop with InputClosed (status 0); when the
// connection fails (a framing error, a forwarding failure) the App is asked to stop
// with status Failed, so a broken connection never exits 0. It provides the
// interaction registry (human.Key) and the approval service (approval.Key), so a
// tool call a policy puts to approval reaches the client as approval/request.
// Disposing the fiber ends the connection and waits for it to close.
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
	// The carrier owns the human plane, as cli.headless does for its profile: the
	// interaction registry the server answers on the sdk channel, and the approval
	// service over it that the tool pipeline asks (approval.Key).
	interactions := human.New()
	grants := approval.NewGrants()
	approvals := approval.New(approval.Config{Registry: interactions, Grants: grants, Sessions: sessions})
	if _, err := approvals.Bind(ctx, sessions); err != nil {
		return err
	}
	if _, err := grants.Bind(ctx, sessions); err != nil {
		return err
	}
	if _, err := human.Key.Provide(ctx, interactions); err != nil {
		return err
	}
	if _, err := approval.Key.Provide(ctx, approvals); err != nil {
		return err
	}
	if _, err := ctx.Effect("wire.sdk.interactions", func() (cordis.Disposer, error) {
		return func() error { interactions.Close(); return nil }, nil
	}); err != nil {
		return err
	}
	o := Options{Owner: ctx, Agents: reg, Sessions: sessions, Routes: rt, Info: protocol.Implementation{Name: "dsh"}, Interactions: interactions}
	app, hasApp := launch.AppKey.Lookup(ctx)
	if hasApp {
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
			err := srv.Serve(serve, owner.Input(), owner.Output(), jsonrpc.Options{})
			if serve.Err() != nil {
				return
			}
			if err == nil {
				owner.InputClosed()
				return
			}
			ctx.Logger().Warnf("wire.sdk: the connection failed: %v", err)
			// An ending the owner already reported (the input's read error, a
			// failed or stalled write) came first and keeps its status: Stop joins it.
			if hasApp {
				app.Stop(launch.Completed, output.Failed)
				return
			}
			owner.InputClosed()
		}()
		return func() error { cancel(); <-done; return nil }, nil
	})
	return err
}, cordis.WithInject(agent.Agents.Name(), session.Key.Name(), stdio.Key.Name(), llm.RuntimeKey.Name()))
