package sqlite

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// Name is the component's catalogue name.
const Name = "session.store"

// Component is session.store (session-store.md §6.1): it opens the store of the
// home boot provides on the root (home.Key), provides it under session.StoreKey
// and closes it when its fiber unloads. It injects nothing: the home is the
// launch's, not an entry's.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	layout, ok := home.Key.Lookup(ctx)
	if !ok || layout.Root() == "" {
		return errors.New("sqlite: session.store is mounted without the launch's home (" + home.Key.Name() + ")")
	}
	s, err := OpenHome(ctx.Lifetime(), layout)
	if err != nil {
		return err
	}
	if _, err := ctx.Effect("session store", func() (cordis.Disposer, error) {
		return func() error { return s.Close(context.Background()) }, nil
	}); err != nil {
		_ = s.Close(context.Background())
		return err
	}
	_, err = session.StoreKey.Provide(ctx, s)
	return err
})
