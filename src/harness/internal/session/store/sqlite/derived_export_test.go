package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
)

// SetCommitTx replaces the commit of every write transaction, so a test can fail
// it. It returns the restore.
func SetCommitTx(fn func(tx *sql.Tx) error) func() {
	old := commitTx
	commitTx = fn
	return func() { commitTx = old }
}

// SetRollbackTx replaces the rollback of a failed write transaction, so a test
// can fail it. It returns the restore.
func SetRollbackTx(fn func(tx *sql.Tx) error) func() {
	old := rollbackTx
	rollbackTx = fn
	return func() { rollbackTx = old }
}

var refusals atomic.Int64

// RefuseDerived implements storetest.DerivedRefuser: two triggers make the
// database itself refuse every insert and every update of a checkpoint under
// key, so the refusal is the database's, met inside the append transaction
// after its records were inserted.
func (s *Store) RefuseDerived(key string) error {
	n := refusals.Add(1)
	quoted := "'" + strings.ReplaceAll(key, "'", "''") + "'"
	return s.write(context.Background(), func(tx *sql.Tx) error {
		for _, op := range []string{"INSERT", "UPDATE"} {
			if _, err := tx.Exec(fmt.Sprintf(`CREATE TRIGGER refuse_derived_%d_%s BEFORE %s ON checkpoints
				WHEN NEW.key = %s BEGIN SELECT RAISE(ABORT, 'test: the derived row is refused'); END`, n, op, op, quoted)); err != nil {
				return err
			}
		}
		return nil
	})
}
