package sqlite

import (
	"context"
	"database/sql"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// The fold status query (M1-G-STORE-17) and the rebuild's work list
// (M1-G-STORE-15): how each view's rows stand against sessions.next_seq. A
// session counts when it is active and has an event (next_seq > 0); its row of a
// key and state version is current when its seq is next_seq-1, behind when it is
// lower, and pending when there is none. Appends commit synchronously, so
// next_seq is the durable head every instance reads.

var _ store.FoldRows = (*Store)(nil)

// foldCount is one key's counts, in one pass over the sessions.
const foldCount = `SELECT
	COALESCE(SUM(c.seq IS NOT NULL AND c.seq + 1 >= s.next_seq), 0),
	COALESCE(SUM(c.seq IS NOT NULL AND c.seq + 1 < s.next_seq), 0),
	COALESCE(SUM(c.seq IS NULL), 0)
	FROM sessions s LEFT JOIN checkpoints c ON c.session = s.uid AND c.key = ? AND c.state_version = ?
	WHERE s.state = 'active' AND s.next_seq > 0`

// foldPending is a page of the sessions with no row of a key and version.
const foldPending = `SELECT s.id FROM sessions s
	WHERE s.state = 'active' AND s.next_seq > 0 AND s.id > ?
	AND NOT EXISTS (SELECT 1 FROM checkpoints c WHERE c.session = s.uid AND c.key = ? AND c.state_version = ?)
	ORDER BY s.id LIMIT ?`

// FoldCounts implements store.FoldRows: every key is counted in one read
// transaction, so the counts are of one cut.
func (s *Store) FoldCounts(ctx context.Context, want map[string]int) ([]store.FoldCount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []store.FoldCount
	err := s.read(ctx, func(tx *sql.Tx) error {
		out = make([]store.FoldCount, 0, len(keys))
		for _, k := range keys {
			c := store.FoldCount{Key: k, StateVersion: want[k]}
			if err := tx.QueryRowContext(ctx, foldCount, k, want[k]).Scan(&c.Current, &c.Behind, &c.Pending); err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FoldPending implements store.FoldRows.
func (s *Store) FoldPending(ctx context.Context, key string, version int, after string, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	var out []string
	err := s.read(ctx, func(tx *sql.Tx) error {
		out = nil
		rows, err := tx.QueryContext(ctx, foldPending, after, key, version, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
