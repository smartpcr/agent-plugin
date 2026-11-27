package kv

import (
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Name is the catalogue name of Component.
const Name = "storage.db"

// Component is storage.db: the one database handle of the home, provided under
// DBKey for a component that keeps a machine-state namespace of its own. It is
// session.store's database (the one dsh.db), not a second connection, so it opens
// and closes nothing itself and leaves with session.store.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	st, err := session.StoreKey.Get(ctx)
	if err != nil {
		return err
	}
	db, ok := st.(DB)
	if !ok {
		return fmt.Errorf("kv: %s is a %T, not the home database storage.db shares", session.StoreKey.Name(), st)
	}
	_, err = DBKey.Provide(ctx, db)
	return err
}, cordis.WithInject(session.StoreKey.Name()))
