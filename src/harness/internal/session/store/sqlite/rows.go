package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// maxSeq is the largest seq SQLite can hold (INTEGER is signed 64-bit). A seq
// past it is answered in Go, never bound.
const maxSeq = store.Seq(math.MaxInt64)

// defaultLimit is a List page when the query names none.
const defaultLimit = 50

// sessionCols is the column list scanRow reads.
const sessionCols = `uid, id, created_at, cwd, origin, creator, fork_source, fork_through, inherited_count,
	delegation_depth, preset, next_seq, lease_epoch, lease_holder, lease_pid, lease_kind, lease_since, last_event_at, last_prompt_at`

// promptKey is a session's store.OrderPrompt key with nothing appended after
// the cut; the sessions_prompt index is on it.
const promptKey = `(CASE WHEN last_prompt_at > 0 THEN last_prompt_at ELSE created_at END)`

// row is one sessions row.
type row struct {
	uid     int64
	sum     store.Summary
	created int64 // created_at, Unix nanoseconds
	last    int64 // last_event_at, Unix nanoseconds; 0 when no event has a time
	prompt  int64 // last_prompt_at, Unix nanoseconds; 0 when no prompt
}

type scanner interface{ Scan(dest ...any) error }

func scanRow(sc scanner) (row, error) {
	var (
		r                        row
		forkThrough, next, epoch int64
		holder                   sql.NullString
		pid                      int64
		kind                     string
		since                    int64
		inherited, depth         int64
	)
	h := &r.sum.Header
	if err := sc.Scan(&r.uid, &h.ID, &r.created, &h.Cwd, &h.Origin, &h.Creator, &h.ForkSource, &forkThrough,
		&inherited, &depth, &h.Preset, &next, &epoch, &holder, &pid, &kind, &since, &r.last, &r.prompt); err != nil {
		return row{}, err
	}
	h.CreatedAt = time.Unix(0, r.created)
	h.ForkThrough, h.InheritedCount, h.DelegationDepth = store.Seq(forkThrough), int(inherited), int(depth)
	// Every commit is synced before it returns (synchronous=FULL), so every
	// committed record is durable, whichever instance committed it.
	r.sum.NextSeq, r.sum.Durable, r.sum.LeaseEpoch = store.Seq(next), store.Seq(next), uint64(epoch)
	if holder.Valid {
		r.sum.Holder = &store.Holder{Instance: holder.String, PID: int(pid), Kind: kind, Since: time.Unix(0, since)}
	}
	if r.last != 0 {
		r.sum.LastEventAt = time.Unix(0, r.last)
	}
	if r.prompt != 0 {
		r.sum.LastPromptAt = time.Unix(0, r.prompt)
	}
	return r, nil
}

// replaceOp is the events.surface value of a replacing record, which is never
// a prompt (store.IsPrompt), for the queries that classify prompts in SQL.
const replaceOp = int64(event.OpReplace)

// promptNanos is the last_prompt_at recs give a session whose value was prev:
// the time of their last timed prompt (store.IsPrompt).
func promptNanos(prev int64, recs []store.Record) int64 {
	for i := len(recs) - 1; i >= 0; i-- {
		if r := recs[i]; store.IsPrompt(r.Type, r.Op) && r.Time > 0 {
			return time.UnixMilli(r.Time).UnixNano()
		}
	}
	return prev
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// liveByID is the active session id, or session/not-found.
func liveByID(ctx context.Context, q querier, id string) (row, error) {
	r, err := scanRow(q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ? AND state = 'active'`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, store.Errorf(store.CodeNotFound, "session %q", id)
	}
	return r, err
}

// liveByUID is the session a handle opened, or session/deleted.
func liveByUID(ctx context.Context, q querier, uid int64, id string) (row, error) {
	r, err := scanRow(q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE uid = ? AND state = 'active'`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, store.Errorf(store.CodeDeleted, "session %q was deleted", id)
	}
	return r, err
}

// envelope is the env column: the operation's range and the sources, NULL when
// all are empty.
type envelope struct {
	Start   store.Seq   `json:"start,omitempty"`
	End     store.Seq   `json:"end,omitempty"`
	Sources []store.Seq `json:"sources,omitempty"`
}

func envOf(r store.Record) (any, error) {
	if r.Start == 0 && r.End == 0 && len(r.Sources) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(envelope{Start: r.Start, End: r.End, Sources: r.Sources})
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const insertEvent = `INSERT INTO events (session, seq, time, type, pv, ignorable, surface, env, data, cold)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const insertRef = `INSERT OR IGNORE INTO blob_refs (session, seq, ref) VALUES (?, ?, ?)`

// insertRecords writes recs to session uid as they are, with the blob_refs rows
// of the references their payloads cite (decoded with reg), and returns their
// size.
func insertRecords(ctx context.Context, tx *sql.Tx, reg *event.Registry, uid int64, recs []store.Record) (int64, error) {
	if len(recs) == 0 {
		return 0, nil
	}
	stmt, err := tx.PrepareContext(ctx, insertEvent)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var refStmt *sql.Stmt
	defer func() {
		if refStmt != nil {
			_ = refStmt.Close()
		}
	}()
	var size int64
	for _, r := range recs {
		if r.Seq > maxSeq {
			return 0, store.Errorf(store.CodeSeqConflict, "seq %d is past the largest the store holds", r.Seq)
		}
		env, err := envOf(r)
		if err != nil {
			return 0, err
		}
		if _, err := stmt.ExecContext(ctx, uid, int64(r.Seq), r.Time, r.Type, r.Version, boolInt(r.Ignorable),
			int(r.Op), env, r.Payload, nullable(r.Cold)); err != nil {
			return 0, err
		}
		refs, err := store.BlobRefs(reg, r)
		if err != nil {
			return 0, err
		}
		for _, ref := range refs {
			if refStmt == nil {
				if refStmt, err = tx.PrepareContext(ctx, insertRef); err != nil {
					return 0, err
				}
			}
			if _, err := refStmt.ExecContext(ctx, uid, int64(r.Seq), ref); err != nil {
				return 0, err
			}
		}
		size += int64(len(r.Payload) + len(r.Cold))
	}
	return size, nil
}

// selectRecords reads up to limit records of session uid from seq from (limit
// -1: all of them).
func selectRecords(ctx context.Context, q querier, uid int64, from store.Seq, limit int64, withAux bool) ([]store.Record, error) {
	cold := "NULL"
	if withAux {
		cold = "cold"
	}
	rows, err := q.QueryContext(ctx, `SELECT seq, time, type, pv, ignorable, surface, env, data, `+cold+`
		FROM events WHERE session = ? AND seq >= ? ORDER BY seq LIMIT ?`, uid, int64(from), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRecord(sc scanner) (store.Record, error) {
	var (
		r             store.Record
		seq           int64
		ignorable, op int64
		env, cold     sql.NullString
	)
	if err := sc.Scan(&seq, &r.Time, &r.Type, &r.Version, &ignorable, &op, &env, &r.Payload, &cold); err != nil {
		return store.Record{}, err
	}
	r.Seq, r.Ignorable, r.Op, r.Cold = store.Seq(seq), ignorable != 0, event.OpKind(op), cold.String
	if env.Valid {
		var e envelope
		if err := json.Unmarshal([]byte(env.String), &e); err != nil {
			return store.Record{}, store.Errorf(CodeCorrupt, "event %d: envelope: %v", seq, err)
		}
		r.Start, r.End, r.Sources = e.Start, e.End, e.Sources
	}
	return r, nil
}

// eventNanos is the LastEventAt a record of time ms gives: its time, or now
// when it carries none.
func (s *Store) eventNanos(ms int64) int64 {
	if ms > 0 {
		return time.UnixMilli(ms).UnixNano()
	}
	return s.now().UnixNano()
}

// register and untrack keep the writers Store.Close releases. Both run with
// wmu held: register after admit and the lease's commit, untrack after the
// release's commit.
func (s *Store) register(w *writer) {
	s.mu.Lock()
	s.writers[w] = struct{}{}
	s.mu.Unlock()
}

func (s *Store) untrack(w *writer) {
	s.mu.Lock()
	delete(s.writers, w)
	s.mu.Unlock()
}
