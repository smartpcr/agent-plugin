package sqlite

import (
	"context"
	"database/sql"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// InsertUnchecked writes recs to session id in one write transaction exactly as
// an Append does after its checks -- the next_seq bump first, then every row --
// but skips the checks, so a test can make a row the database itself refuses
// and prove the whole transaction rolls back.
func (s *Store) InsertUnchecked(ctx context.Context, id string, recs []store.Record) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		r, err := liveByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET next_seq = next_seq + ? WHERE uid = ?`, len(recs), r.uid); err != nil {
			return err
		}
		_, err = insertRecords(ctx, tx, r.uid, recs)
		return err
	})
}
