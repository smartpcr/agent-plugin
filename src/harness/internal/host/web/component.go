package web

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
)

// Name is the component's catalogue name.
const Name = "host.web"

// Prefix is the listener mount the table serves under: every path no longer mount
// (assets, sign-in) claims.
const Prefix = "/"

// Key is the route table.
var Key = cordis.NewServiceKey[*Routes]("host.web")

// Component is host.web: it mounts a new table on the listener as a Required mount,
// so a route runs only after admission with the peer in its request context, and
// provides the table under Key. Unloading it removes the mount, and every component
// that registered routes depends on Key, so its routes leave first.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	l, err := listen.Key.Get(ctx)
	if err != nil {
		return err
	}
	routes := New(cfg, ctx.Logger().Errorf)
	if err := l.Mount(ctx, listen.Mount{Prefix: Prefix, Stream: routes.Streams, Serve: routes}); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, routes)
	return err
}, cordis.WithInject(listen.Key.Name()))
