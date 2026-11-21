package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// Create implements store.Store: the session row and, for a fork, the copy of
// the parent's prefix are written in one transaction, and the writer holds the
// lease at epoch 1.
func (s *Store) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.ID == "" {
		return nil, store.Errorf(store.CodeInvalidBatch, "a session needs an id")
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.now()
	}
	since := s.now().UnixNano()
	var uid int64
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.admit(); err != nil {
		return nil, err
	}
	err := s.writeLocked(ctx, func(tx *sql.Tx) error {
		if err := absent(ctx, tx, h.ID); err != nil {
			return err
		}
		var (
			parent     row
			through    store.Seq
			next, last int64
			prompt     int64
			size       int64
			fork       = o.ForkOf
		)
		if fork != nil {
			var err error
			if parent, err = liveByID(ctx, tx, fork.ID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return store.Errorf(store.CodeNotFound, "fork parent %q", fork.ID)
				}
				return err
			}
			if fork.Through >= parent.sum.NextSeq {
				return store.Errorf(store.CodeInvalidBoundary, "fork of %q through seq %d, which has %d records",
					fork.ID, fork.Through, parent.sum.NextSeq)
			}
			through = fork.Through
			inherited, err := selectRecords(ctx, tx, parent.uid, 0, int64(through)+1, true)
			if err != nil {
				return err
			}
			if len(inherited) != int(through)+1 || inherited[len(inherited)-1].Seq != through {
				return store.Errorf(CodeCorrupt, "fork parent %q: %d records through seq %d, want %d below next seq %d",
					fork.ID, len(inherited), through, int64(through)+1, parent.sum.NextSeq)
			}
			if err := store.CheckOpen(s.reg, inherited); err != nil {
				return err
			}
			next = int64(len(inherited))
			if ms := inherited[len(inherited)-1].Time; ms > 0 {
				last = s.eventNanos(ms)
			} else {
				last = parent.last
			}
			for _, r := range inherited {
				size += int64(len(r.Payload) + len(r.Cold))
			}
			prompt = promptNanos(0, inherited)
			h.ForkSource, h.ForkThrough, h.InheritedCount = fork.ID, through, len(inherited)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, created_at, cwd, origin, creator, fork_source,
			fork_through, inherited_count, delegation_depth, preset, next_seq, lease_epoch, lease_holder, lease_pid,
			lease_kind, lease_since, last_event_at, last_prompt_at, bytes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?)`,
			h.ID, h.CreatedAt.UnixNano(), h.Cwd, h.Origin, h.Creator, h.ForkSource, int64(h.ForkThrough),
			h.InheritedCount, h.DelegationDepth, h.Preset, next, s.instance, s.pid, s.kind, since, last, prompt, size)
		if err != nil {
			return err
		}
		if uid, err = res.LastInsertId(); err != nil {
			return err
		}
		if fork != nil {
			if _, err = tx.ExecContext(ctx, `INSERT INTO events (session, seq, time, type, pv, ignorable, surface, env, data, cold)
				SELECT ?, seq, time, type, pv, ignorable, surface, env, data, cold FROM events
				WHERE session = ? AND seq <= ?`, uid, parent.uid, int64(through)); err != nil {
				return err
			}
			// The inherited prefix cites what the parent's did: its rows are the
			// fork's own, so the fork keeps its objects after the parent goes.
			if _, err = tx.ExecContext(ctx, `INSERT INTO blob_refs (session, seq, ref)
				SELECT ?, seq, ref FROM blob_refs WHERE session = ? AND seq <= ?`, uid, parent.uid, int64(through)); err != nil {
				return err
			}
			err = recordLineage(ctx, tx, uid, fork.ID)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	// Registered before wmu is released: a Close that began meanwhile waits
	// for wmu and then releases this lease.
	w := &writer{reader: reader{s: s, id: h.ID, uid: uid, header: h}, epoch: 1, holder: s.instance}
	s.register(w)
	return w, nil
}

// recordLineage writes the ancestry of the session row uid whose fork source is
// source: source itself, then source's own recorded ancestry when this home
// holds source. Every path that inserts a session row with a fork source (a
// fork, an import, a seed) calls it, so the ancestry is kept for the session
// alone and survives the deletion of any ancestor (the spill store authorizes
// a fork's reads by it). No source, no rows.
func recordLineage(ctx context.Context, tx *sql.Tx, uid int64, source string) error {
	if source == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lineage (session, depth, ancestor) VALUES (?, 1, ?)`, uid, source); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO lineage (session, depth, ancestor)
		SELECT ?, l.depth + 1, l.ancestor FROM lineage l JOIN sessions s ON s.uid = l.session
		WHERE s.id = ? AND s.uid <> ? AND l.depth < 256`, uid, source, uid)
	return err
}

// absent refuses an id an active session holds.
func absent(ctx context.Context, tx *sql.Tx, id string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return store.Errorf(store.CodeExists, "session %q exists", id)
	}
	return nil
}

// OpenWrite implements store.Store (session-store.md, the lease, rules 2 to 4):
// in one write transaction, a recorded holder is probed; a live one refuses the
// lease with session/owned unless o forces the takeover, and a dead one's lease
// is taken. Taking a lease raises the epoch, so the displaced writer's next
// append or flush is session/lease-lost.
func (s *Store) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	since := s.now().UnixNano()
	var (
		r     row
		epoch uint64
	)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.admit(); err != nil {
		return nil, err
	}
	err := s.writeLocked(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = liveByID(ctx, tx, id); err != nil {
			return err
		}
		if err := s.claimable(ctx, tx, id, r, o); err != nil {
			return err
		}
		recs, err := selectRecords(ctx, tx, r.uid, 0, -1, true)
		if err != nil {
			return err
		}
		if err := store.CheckOpen(s.reg, recs); err != nil {
			return err
		}
		epoch = r.sum.LeaseEpoch + 1
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET lease_epoch = ?, lease_holder = ?, lease_pid = ?,
			lease_kind = ?, lease_since = ? WHERE uid = ?`, int64(epoch), s.instance, s.pid, s.kind, since, r.uid)
		return err
	})
	if err != nil {
		return nil, err
	}
	w := &writer{reader: reader{s: s, id: id, uid: r.uid, header: r.sum.Header}, epoch: epoch, holder: s.instance}
	s.register(w)
	return w, nil
}

// OpenRead implements store.Store.
func (s *Store) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var r row
	err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = liveByID(ctx, tx, id); err != nil {
			return err
		}
		recs, err := selectRecords(ctx, tx, r.uid, 0, -1, true)
		if err != nil {
			return err
		}
		return store.CheckOpen(s.reg, recs)
	})
	if err != nil {
		return nil, err
	}
	return &reader{s: s, id: id, uid: r.uid, header: r.sum.Header}, nil
}

// Stat implements store.Store.
func (s *Store) Stat(ctx context.Context, id string) (store.Summary, error) {
	if err := ctx.Err(); err != nil {
		return store.Summary{}, err
	}
	var r row
	err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = liveByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return store.Summary{}, err
	}
	return r.sum, nil
}

// Cites reports whether active session id logs an event citing ref, a blob_refs
// reference "<kind>:<id>" (storage.md §6.2). A fork's inherited prefix counts:
// its rows were copied with it. It implements storage's blob.Index, behind
// blob.Store.ReferencedBy.
func (s *Store) Cites(ctx context.Context, id, ref string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var n int
	err := s.read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM blob_refs b JOIN sessions s ON s.uid = b.session
			WHERE s.id = ? AND s.state = 'active' AND b.ref = ?)`, id, ref).Scan(&n)
	})
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// List implements store.Store: most recent first (LastEventAt, then CreatedAt,
// then id), paged by a keyset cursor.
func (s *Store) List(ctx context.Context, q store.Query) (store.Page, error) {
	if err := ctx.Err(); err != nil {
		return store.Page{}, err
	}
	limit := q.PageLimit(defaultLimit)
	where := []string{"state = 'active'"}
	var args []any
	if q.Cwd != "" {
		where, args = append(where, "cwd = ?"), append(args, q.Cwd)
	}
	if q.Creator != "" {
		where, args = append(where, "creator = ?"), append(args, q.Creator)
	}
	if q.Order == store.OrderPrompt {
		return s.listByPrompt(ctx, q, limit, where, args)
	}
	if q.Cursor != "" {
		c, err := parseCursor(q.Cursor)
		if err != nil {
			return store.Page{}, err
		}
		where = append(where, "(last_event_at < ? OR (last_event_at = ? AND (created_at < ? OR (created_at = ? AND id > ?))))")
		args = append(args, c.last, c.last, c.created, c.created, c.id)
	}
	args = append(args, int64(limit)+1)
	var rows []row
	err := s.read(ctx, func(tx *sql.Tx) error {
		rs, err := tx.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE `+strings.Join(where, " AND ")+`
			ORDER BY last_event_at DESC, created_at DESC, id LIMIT ?`, args...)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			r, err := scanRow(rs)
			if err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return rs.Err()
	})
	if err != nil {
		return store.Page{}, err
	}
	var page store.Page
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			page.Next = cursor{last: last.last, created: last.created, id: last.sum.Header.ID}.String()
			break
		}
		page.Summaries = append(page.Summaries, r.sum)
	}
	return page, nil
}

// listByPrompt is List in store.OrderPrompt, in one read transaction: the
// sessions_prompt index scanned past the cursor for the sessions with no prompt
// after the cut, merged with the few sessions prompted since the cut, whose key
// as of the cut is read from their logs.
func (s *Store) listByPrompt(ctx context.Context, q store.Query, limit int, where []string, args []any) (store.Page, error) {
	var after *store.PromptCursor
	cut := q.AsOf
	if q.Cursor != "" {
		c, err := store.ParsePromptCursor(q.Cursor)
		if err != nil {
			return store.Page{}, err
		}
		after, cut = &c, time.Unix(0, c.AsOf)
	}
	if cut.IsZero() {
		cut = s.now()
	}
	at := cut.UnixNano()
	pcut := store.PromptCut(at)
	where = append(slices.Clip(where), "created_at <= ?")
	args = append(slices.Clip(args), at)
	type keyed struct {
		key int64
		r   row
	}
	var all []keyed
	err := s.read(ctx, func(tx *sql.Tx) error {
		scan := func(query string, args ...any) ([]row, error) {
			rs, err := tx.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, err
			}
			defer rs.Close()
			var out []row
			for rs.Next() {
				r, err := scanRow(rs)
				if err != nil {
					return nil, err
				}
				out = append(out, r)
			}
			return out, rs.Err()
		}
		w, a := append(slices.Clip(where), "last_prompt_at <= ?"), append(slices.Clip(args), pcut)
		if after != nil {
			w = append(w, "("+promptKey+" < ? OR ("+promptKey+" = ? AND id > ?))")
			a = append(a, after.Key, after.Key, after.ID)
		}
		a = append(a, int64(limit)+1)
		stable, err := scan(`SELECT `+sessionCols+` FROM sessions WHERE `+strings.Join(w, " AND ")+`
			ORDER BY `+promptKey+` DESC, id LIMIT ?`, a...)
		if err != nil {
			return err
		}
		for _, r := range stable {
			all = append(all, keyed{key: cmp.Or(r.prompt, r.created), r: r})
		}
		touched, err := scan(`SELECT `+sessionCols+` FROM sessions WHERE `+strings.Join(append(slices.Clip(where), "last_prompt_at > ?"), " AND "),
			append(slices.Clip(args), pcut)...)
		if err != nil {
			return err
		}
		for _, r := range touched {
			var ms sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT MAX(time) FROM events WHERE session = ? AND type = ? AND time > 0 AND time <= ?`,
				r.uid, store.PromptType, time.Unix(0, pcut).UnixMilli()).Scan(&ms); err != nil {
				return err
			}
			key := r.created
			if ms.Valid {
				key = time.UnixMilli(ms.Int64).UnixNano()
			}
			if after == nil || after.Precedes(key, r.sum.Header.ID) {
				all = append(all, keyed{key: key, r: r})
			}
		}
		return nil
	})
	if err != nil {
		return store.Page{}, err
	}
	slices.SortFunc(all, func(a, b keyed) int {
		if c := cmp.Compare(b.key, a.key); c != 0 {
			return c
		}
		return strings.Compare(a.r.sum.Header.ID, b.r.sum.Header.ID)
	})
	var page store.Page
	for i, k := range all {
		if i == limit {
			last := all[limit-1]
			page.Next = store.PromptCursor{AsOf: at, Key: last.key, ID: last.r.sum.Header.ID}.String()
			break
		}
		page.Summaries = append(page.Summaries, k.r.sum)
	}
	return page, nil
}

// cursor is a List position: the sort key of the last summary returned.
type cursor struct {
	last, created int64
	id            string
}

func (c cursor) String() string { return fmt.Sprintf("%d.%d.%s", c.last, c.created, c.id) }

func parseCursor(v string) (cursor, error) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) == 3 {
		last, err1 := strconv.ParseInt(parts[0], 10, 64)
		created, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 == nil && err2 == nil {
			return cursor{last: last, created: created, id: parts[2]}, nil
		}
	}
	return cursor{}, store.Errorf(store.CodeInvalidBoundary, "list cursor %q is not one this store issued", v)
}

// Flush implements store.Store: it returns once every commit of this instance
// is durable.
func (s *Store) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.barrier(ctx)
}

// Seed stores a durable, unheld session holding recs as they are, without the
// admission rule an Append applies: it stands in for rows another build wrote
// (storetest.Seeder). It is for fixtures.
func (s *Store) Seed(ctx context.Context, h store.Header, recs []store.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.ID == "" {
		return store.Errorf(store.CodeInvalidBatch, "a session needs an id")
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.now()
	}
	var last int64
	if n := len(recs); n > 0 && recs[n-1].Time > 0 {
		last = s.eventNanos(recs[n-1].Time)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := absent(ctx, tx, h.ID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, created_at, cwd, origin, creator, fork_source,
			fork_through, inherited_count, delegation_depth, preset, next_seq, last_event_at, last_prompt_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			h.ID, h.CreatedAt.UnixNano(), h.Cwd, h.Origin, h.Creator, h.ForkSource, int64(h.ForkThrough),
			h.InheritedCount, h.DelegationDepth, h.Preset, len(recs), last, promptNanos(0, recs))
		if err != nil {
			return err
		}
		uid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := recordLineage(ctx, tx, uid, h.ForkSource); err != nil {
			return err
		}
		size, err := insertRecords(ctx, tx, s.reg, uid, recs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET bytes = ? WHERE uid = ?`, size, uid)
		return err
	})
}
