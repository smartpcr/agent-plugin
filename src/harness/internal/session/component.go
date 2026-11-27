package session

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// StoreKey is the service under which session.store provides the process's
// storage service (session-store.md §6.1; replaces ctx.sessionPersistence).
var StoreKey = cordis.NewServiceKey[store.Store]("session.store")

// Name is the catalogue name of Component.
const Name = "session.core"

// Component is session.core (core-session.md §6.1): the Service over the store
// provided under StoreKey, its live events dispatched through the component's
// fiber, provided under Key.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	st, err := StoreKey.Get(ctx)
	if err != nil {
		return err
	}
	_, err = Key.Provide(ctx, NewService(ctx, st, Config{}))
	return err
}, cordis.WithInject(StoreKey.Name()))
