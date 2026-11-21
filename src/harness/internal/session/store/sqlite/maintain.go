package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// DefaultDeleteChunk is the number of events one deletion transaction removes.
const DefaultDeleteChunk = 1000

// DutyRetention is the duty that finishes tombstones and retires old sessions.
const DutyRetention = "retention"

// WithSpillDir names the spill directory (<home>/spill) whose per-session
// subdirectory a deletion removes. OpenHome sets it; without it a deletion
// removes rows only.
func WithSpillDir(dir string) Option { return func(s *Store) { s.spill = dir } }

// WithDeleteChunk replaces DefaultDeleteChunk.
func WithDeleteChunk(n int) Option { return func(s *Store) { s.chunk = n } }

// afterDeleteChunk is the crash-injection hook of DH-T-STORE-11: it runs after
// each committed chunk of a deletion with the events removed so far.
var afterDeleteChunk func(id string, deleted int64)

// removeAll removes a spill directory; a test swaps it to inject a failure.
var removeAll = os.RemoveAll

// afterRetireShortlist, when set, runs between Retire's shortlist and its
// deletions, so a test can refresh or replace a shortlisted session there.
var afterRetireShortlist func(ids []string)

// SpillName is the directory under <home>/spill that holds session id's spill
// files: the first 16 hex digits of the SHA-256 of the id (storage.md).
func SpillName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:16]
}

// tomb is a session marked deleting, to be removed.
type tomb struct {
	uid int64
	id  string
}

// Delete implements store.Store as DH-ADR-STORE-7 requires. One transaction takes
// the lease of the session and of every subagent child it created (transitively),
// refusing with session/owned when a live instance holds any of them, and marks
// each row deleting with its epoch raised: from that commit the session is
// invisible (not-found to Stat, OpenRead and List; session/deleted to a handle
// opened earlier) and its old holder's writes are lease-lost. Then its spill
// directory goes, then its events in chunks of the delete chunk, one transaction
// each, so no other writer of the home waits long, and last its checkpoints, its
// requests and its row. A fork is not deleted with its parent: it holds its own
// copy of the prefix. A deletion interrupted on the way leaves the tombstone,
// which the next Open, FinishDeletions or the retention duty completes.
func (s *Store) Delete(ctx context.Context, id string, _ store.DeleteOptions) error {
	_, err := s.deleteIf(ctx, id, nil)
	return err
}

// errSkipped is deleteIf's answer when its guard declined the session.
var errSkipped = errors.New("sqlite: deletion declined by its guard")

// deleteIf is Delete, with guard (when set) judging, inside the tombstoning
// transaction, the session's row and the row of every subagent child the
// deletion would cascade to: when it declines any of them, nothing changes and
// deleteIf returns errSkipped. It returns the ids it tombstoned, the session's
// first and then its cascaded children's.
func (s *Store) deleteIf(ctx context.Context, id string, guard func(ctx context.Context, tx *sql.Tx, r row) (bool, error)) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		doomed  []tomb
		skipped bool
	)
	err := s.write(ctx, func(tx *sql.Tx) error {
		r, err := liveByID(ctx, tx, id)
		if err != nil {
			return err
		}
		rows := []row{r}
		seen := map[string]bool{id: true}
		for i := 0; i < len(rows); i++ {
			kids, err := subagents(ctx, tx, rows[i].sum.Header.ID)
			if err != nil {
				return err
			}
			for _, k := range kids {
				if !seen[k.sum.Header.ID] {
					seen[k.sum.Header.ID] = true
					rows = append(rows, k)
				}
			}
		}
		if guard != nil {
			for _, c := range rows {
				ok, err := guard(ctx, tx, c)
				if err != nil {
					return err
				}
				if !ok {
					skipped = true
					return nil
				}
			}
		}
		for _, c := range rows {
			if err := s.claimable(ctx, tx, c.sum.Header.ID, c, store.OpenOptions{}); err != nil {
				return err
			}
		}
		for _, c := range rows {
			if err := s.tombstone(ctx, tx, c.uid); err != nil {
				return err
			}
			doomed = append(doomed, tomb{uid: c.uid, id: c.sum.Header.ID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if skipped {
		return nil, errSkipped
	}
	ids := make([]string, len(doomed))
	for i, t := range doomed {
		ids[i] = t.id
	}
	return ids, s.finishAll(ctx, doomed)
}

// subagents are the active sessions created as subagent children of id.
func subagents(ctx context.Context, tx *sql.Tx, id string) ([]row, error) {
	rs, err := tx.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE origin = 'subagent' AND creator = ? AND state = 'active' ORDER BY uid`, id)
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

// tombstone marks session uid deleting under this instance's lease, epoch raised.
func (s *Store) tombstone(ctx context.Context, tx *sql.Tx, uid int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE sessions SET state = 'deleting', lease_epoch = lease_epoch + 1,
		lease_holder = ?, lease_pid = ?, lease_kind = ?, lease_since = ? WHERE uid = ?`,
		s.instance, s.pid, s.kind, s.now().UnixNano(), uid)
	return err
}

func (s *Store) finishAll(ctx context.Context, doomed []tomb) error {
	var errs []error
	for _, t := range doomed {
		if err := s.finish(ctx, t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// finish removes a tombstoned session: its spill directory, its events chunk by
// chunk, then its checkpoints, requests and row. Every step can be repeated, so a
// second instance finishing the same tombstone, or a resumed one, is harmless.
func (s *Store) finish(ctx context.Context, t tomb) error {
	if s.spill != "" {
		if err := removeAll(filepath.Join(s.spill, SpillName(t.id))); err != nil {
			return ioErr("delete "+t.id+": spill", err)
		}
	}
	chunk := s.chunk
	if chunk <= 0 {
		chunk = DefaultDeleteChunk
	}
	var total int64
	for {
		var n int64
		if err := s.write(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE session = ? AND seq IN
				(SELECT seq FROM events WHERE session = ? ORDER BY seq LIMIT ?)`, t.uid, t.uid, chunk)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		}); err != nil {
			return err
		}
		if n == 0 {
			break
		}
		total += n
		if afterDeleteChunk != nil {
			afterDeleteChunk(t.id, total)
		}
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM sessions WHERE uid = ?`, t.uid).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // another instance finished it
		}
		if err != nil {
			return err
		}
		if state != "deleting" {
			return store.Errorf(CodeCorrupt, "session %q: tombstone lost (state %q)", t.id, state)
		}
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM events WHERE session = ?`, t.uid},
			{`DELETE FROM checkpoints WHERE session = ?`, t.uid},
			{`DELETE FROM requests WHERE session = ?`, t.id},
			{`DELETE FROM sessions WHERE uid = ?`, t.uid},
		} {
			if _, err := tx.ExecContext(ctx, q.sql, q.arg); err != nil {
				return err
			}
		}
		return nil
	})
}

// FinishDeletions completes every tombstone whose deleter is gone (or is this
// instance), taking its lease first, and reports how many it finished. Open runs
// it; so does the retention duty.
func (s *Store) FinishDeletions(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var doomed []tomb
	err := s.write(ctx, func(tx *sql.Tx) error {
		rs, err := tx.QueryContext(ctx, `SELECT uid, id, lease_holder FROM sessions WHERE state = 'deleting' ORDER BY uid`)
		if err != nil {
			return err
		}
		type pending struct {
			t      tomb
			holder sql.NullString
		}
		var all []pending
		for rs.Next() {
			var p pending
			if err := rs.Scan(&p.t.uid, &p.t.id, &p.holder); err != nil {
				rs.Close()
				return err
			}
			all = append(all, p)
		}
		if err := errors.Join(rs.Err(), rs.Close()); err != nil {
			return err
		}
		for _, p := range all {
			if p.holder.Valid && p.holder.String != s.instance {
				live, err := s.alive(ctx, tx, p.holder.String)
				if err != nil {
					return err
				}
				if live {
					continue // its deleter is still at it
				}
			}
			if !p.holder.Valid || p.holder.String != s.instance {
				if err := s.tombstone(ctx, tx, p.t.uid); err != nil {
					return err
				}
			}
			doomed = append(doomed, p.t)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(doomed), s.finishAll(ctx, doomed)
}

// Retire is the retention pass (DH-ADR-STORE-7): it finishes tombstones, then,
// when maxAge is positive, deletes every session no live instance holds whose
// last event (or, with none, its creation) is older than maxAge. Retention is off
// by default: a maxAge of 0 deletes nothing that was not already being deleted.
// It returns the ids it deleted.
func (s *Store) Retire(ctx context.Context, maxAge time.Duration) ([]string, error) {
	if _, err := s.FinishDeletions(ctx); err != nil {
		return nil, err
	}
	if maxAge <= 0 {
		return nil, nil
	}
	cut := s.now().Add(-maxAge).UnixNano()
	type candidate struct {
		uid int64
		id  string
	}
	var cands []candidate
	if err := s.read(ctx, func(tx *sql.Tx) error {
		rs, err := tx.QueryContext(ctx, `SELECT uid, id FROM sessions WHERE state = 'active'
			AND (CASE WHEN last_event_at > 0 THEN last_event_at ELSE created_at END) < ? ORDER BY uid`, cut)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var c candidate
			if err := rs.Scan(&c.uid, &c.id); err != nil {
				return err
			}
			cands = append(cands, c)
		}
		return rs.Err()
	}); err != nil {
		return nil, err
	}
	if afterRetireShortlist != nil {
		ids := make([]string, len(cands))
		for i, c := range cands {
			ids[i] = c.id
		}
		afterRetireShortlist(ids)
	}
	var deleted []string
	for _, c := range cands {
		// The snapshot above is only a shortlist: inside the tombstoning
		// transaction the session must still be the same one (its uid; an id
		// deleted and created again is another session) and still be old (no
		// event appended since), and claimable checks its lease there too.
		// Every subagent child the deletion cascades to must be old as well,
		// or the whole candidate is skipped: retention removes no recent
		// history.
		gone, err := s.deleteIf(ctx, c.id, func(ctx context.Context, tx *sql.Tx, r row) (bool, error) {
			if r.sum.Header.ID == c.id && r.uid != c.uid {
				return false, nil
			}
			var at int64
			err := tx.QueryRowContext(ctx, `SELECT CASE WHEN last_event_at > 0 THEN last_event_at ELSE created_at END
				FROM sessions WHERE uid = ?`, r.uid).Scan(&at)
			return err == nil && at < cut, err
		})
		switch {
		case err == nil:
			deleted = append(deleted, gone...)
		case errors.Is(err, errSkipped), errors.Is(err, store.ErrOwned), errors.Is(err, store.ErrNotFound):
			// refreshed or replaced since the shortlist, held by a live
			// instance, or gone with its creator
		default:
			return deleted, err
		}
	}
	return deleted, nil
}

// RunRetention runs Retire as the retention duty: on one instance of the home,
// at most once per every.
func (s *Store) RunRetention(ctx context.Context, maxAge, every time.Duration) (bool, error) {
	return s.RunDuty(ctx, DutyRetention, every, func(ctx context.Context) error {
		_, err := s.Retire(ctx, maxAge)
		return err
	})
}

// CheckReport is what Check found.
type CheckReport struct {
	// Sessions is the number of active sessions checked.
	Sessions int
	// Integrity holds SQLite's integrity_check findings; empty when it said ok.
	Integrity []string
	// Problems is the damage found in the store's own invariants.
	Problems []string
	// Deleting names the sessions still tombstoned (not damage).
	Deleting []string
}

// Clean reports whether nothing is damaged.
func (r CheckReport) Clean() bool { return len(r.Integrity) == 0 && len(r.Problems) == 0 }

// Check is the store check of DH-D-STORE-6 (`dsh store check`), read-only, in one
// snapshot: SQLite's integrity check; for each active session, its row count and
// last seq against next_seq; checkpoints past a session's end; rows of no session.
func (s *Store) Check(ctx context.Context) (CheckReport, error) {
	if err := ctx.Err(); err != nil {
		return CheckReport{}, err
	}
	var rep CheckReport
	err := s.read(ctx, func(tx *sql.Tx) error {
		if err := eachString(ctx, tx, `PRAGMA integrity_check`, func(v string) {
			if v != "ok" {
				rep.Integrity = append(rep.Integrity, v)
			}
		}); err != nil {
			return err
		}
		rs, err := tx.QueryContext(ctx, `SELECT s.id, s.next_seq, count(e.seq), coalesce(max(e.seq), -1)
			FROM sessions s LEFT JOIN events e ON e.session = s.uid WHERE s.state = 'active'
			GROUP BY s.uid ORDER BY s.id`)
		if err != nil {
			return err
		}
		for rs.Next() {
			var (
				id               string
				next, count, top int64
			)
			if err := rs.Scan(&id, &next, &count, &top); err != nil {
				rs.Close()
				return err
			}
			rep.Sessions++
			if count != next || top != next-1 {
				rep.Problems = append(rep.Problems, fmt.Sprintf(
					"session %q: %d events, last seq %d, next seq %d", id, count, top, next))
			}
		}
		if err := errors.Join(rs.Err(), rs.Close()); err != nil {
			return err
		}
		rs, err = tx.QueryContext(ctx, `SELECT s.id, c.key, c.state_version, c.seq FROM checkpoints c
			JOIN sessions s ON s.uid = c.session WHERE s.state = 'active' AND c.seq >= s.next_seq ORDER BY s.id, c.key`)
		if err != nil {
			return err
		}
		for rs.Next() {
			var (
				id, key  string
				ver, seq int64
			)
			if err := rs.Scan(&id, &key, &ver, &seq); err != nil {
				rs.Close()
				return err
			}
			rep.Problems = append(rep.Problems, fmt.Sprintf(
				"session %q: checkpoint %s v%d at seq %d, past the end", id, key, ver, seq))
		}
		if err := errors.Join(rs.Err(), rs.Close()); err != nil {
			return err
		}
		for _, q := range []struct{ what, sql string }{
			{"events", `SELECT count(*) FROM events WHERE session NOT IN (SELECT uid FROM sessions)`},
			{"checkpoints", `SELECT count(*) FROM checkpoints WHERE session NOT IN (SELECT uid FROM sessions)`},
			{"requests", `SELECT count(*) FROM requests WHERE session NOT IN (SELECT id FROM sessions)`},
		} {
			var n int64
			if err := tx.QueryRowContext(ctx, q.sql).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				rep.Problems = append(rep.Problems, fmt.Sprintf("%d %s of no session", n, q.what))
			}
		}
		return eachString(ctx, tx, `SELECT id FROM sessions WHERE state = 'deleting' ORDER BY id`, func(v string) {
			rep.Deleting = append(rep.Deleting, v)
		})
	})
	return rep, err
}

func eachString(ctx context.Context, tx *sql.Tx, q string, fn func(string)) error {
	rs, err := tx.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rs.Close()
	for rs.Next() {
		var v string
		if err := rs.Scan(&v); err != nil {
			return err
		}
		fn(v)
	}
	return rs.Err()
}

// coreKeyPrefix is the checkpoint key prefix the session core owns (see
// store.Checkpoint); every other key in the checkpoints table is a view's.
const coreKeyPrefix = "core/"

// Refold replays a session's events and returns the checkpoints an incremental
// build would hold for it under the core's keys ("core/..."). The session core
// provides it.
type Refold func(ctx context.Context, r store.Reader) ([]store.Checkpoint, error)

// RebuildOptions are Rebuild's options.
type RebuildOptions struct {
	// Session limits the rebuild to one session; empty is the whole home.
	Session string
	// Verify refolds and compares with the stored rows, and changes nothing.
	Verify bool
	// Refold computes the checkpoints of a session from its events.
	Refold Refold
}

// RebuildReport is what Rebuild did or, with Verify, found.
type RebuildReport struct {
	// Sessions is the number of sessions refolded.
	Sessions int
	// Differences names every stored checkpoint that differs from the refold
	// (with Verify) or that the rebuild replaced.
	Differences []string
	// Conflicts names every session a whole-home rebuild left as it was because
	// it grew during its refold (session/seq-conflict); the others still ran.
	Conflicts []string
}

// Rebuild is the one operation that discards derived data and refolds it
// (DH-D-STORE-6, `dsh store rebuild`): for one session or every active one it
// replays the events through o.Refold and, in one write transaction per session,
// replaces the stored checkpoints with the refolded ones. A session that grew
// between the refold and the write is refused with session/seq-conflict and left
// as it was: a one-session rebuild returns that error, a whole-home rebuild
// records the session in RebuildReport.Conflicts and goes on with the others.
// With o.Verify it compares and changes nothing. Only the core's checkpoints
// (keys under "core/") are selected, compared, deleted and rewritten; o.Refold
// must not return any other key. A06's view rows share the checkpoints table
// under their own keys and are left as they are: a view whose stored row is
// stale or of an old version refolds itself on the next open. The search
// documents join this operation when their table exists.
func (s *Store) Rebuild(ctx context.Context, o RebuildOptions) (RebuildReport, error) {
	if err := ctx.Err(); err != nil {
		return RebuildReport{}, err
	}
	if o.Refold == nil {
		return RebuildReport{}, store.Errorf(store.CodeInvalidBatch, "rebuild needs a refold")
	}
	var targets []row
	if err := s.read(ctx, func(tx *sql.Tx) error {
		if o.Session != "" {
			r, err := liveByID(ctx, tx, o.Session)
			targets = []row{r}
			return err
		}
		rs, err := tx.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE state = 'active' ORDER BY id`)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			r, err := scanRow(rs)
			if err != nil {
				return err
			}
			targets = append(targets, r)
		}
		return rs.Err()
	}); err != nil {
		return RebuildReport{}, err
	}
	var rep RebuildReport
	for _, r := range targets {
		id := r.sum.Header.ID
		got, err := o.Refold(ctx, &reader{s: s, id: id, uid: r.uid, header: r.sum.Header})
		if err != nil {
			return rep, fmt.Errorf("rebuild %q: %w", id, err)
		}
		for _, c := range got {
			if !strings.HasPrefix(c.Key, coreKeyPrefix) {
				return rep, store.Errorf(store.CodeInvalidBatch, "rebuild %q: refold returned checkpoint %s outside %q",
					id, c.Key, coreKeyPrefix)
			}
		}
		got = newestCheckpoints(got)
		var stored []store.Checkpoint
		if err := s.read(ctx, func(tx *sql.Tx) error {
			var err error
			stored, err = selectCheckpoints(ctx, tx, r.uid)
			return err
		}); err != nil {
			return rep, err
		}
		diff := diffCheckpoints(id, stored, got)
		rep.Sessions++
		if o.Verify || len(diff) == 0 {
			rep.Differences = append(rep.Differences, diff...)
			continue
		}
		if err := s.write(ctx, func(tx *sql.Tx) error {
			cur, err := liveByUID(ctx, tx, r.uid, id)
			if err != nil {
				return err
			}
			if cur.sum.NextSeq != r.sum.NextSeq {
				return store.Errorf(store.CodeSeqConflict, "rebuild %q: it grew from %d to %d records during the refold",
					id, r.sum.NextSeq, cur.sum.NextSeq)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM checkpoints WHERE session = ? AND substr(key, 1, ?) = ?`,
				r.uid, len(coreKeyPrefix), coreKeyPrefix); err != nil {
				return err
			}
			for _, c := range got {
				if c.Seq >= cur.sum.NextSeq {
					return store.Errorf(store.CodeInvalidBoundary, "rebuild %q: checkpoint %s at seq %d, past the end", id, c.Key, c.Seq)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoints (session, key, state_version, seq, state)
					VALUES (?, ?, ?, ?, ?)`, r.uid, c.Key, c.StateVersion, int64(c.Seq), c.State); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			if o.Session == "" && errors.Is(err, store.ErrSeqConflict) {
				rep.Conflicts = append(rep.Conflicts, id)
				continue
			}
			return rep, err
		}
		rep.Differences = append(rep.Differences, diff...)
	}
	return rep, nil
}

// selectCheckpoints returns a session's core checkpoints (keys under "core/").
func selectCheckpoints(ctx context.Context, q querier, uid int64) ([]store.Checkpoint, error) {
	rs, err := q.QueryContext(ctx, `SELECT key, state_version, seq, state FROM checkpoints
		WHERE session = ? AND substr(key, 1, ?) = ? ORDER BY key, state_version`, uid, len(coreKeyPrefix), coreKeyPrefix)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []store.Checkpoint
	for rs.Next() {
		var (
			c   store.Checkpoint
			seq int64
		)
		if err := rs.Scan(&c.Key, &c.StateVersion, &seq, &c.State); err != nil {
			return nil, err
		}
		c.Seq = store.Seq(seq)
		out = append(out, c)
	}
	return out, rs.Err()
}

// newestCheckpoints keeps the last checkpoint per (Key, StateVersion), as the
// store does, sorted as selectCheckpoints returns them.
func newestCheckpoints(cs []store.Checkpoint) []store.Checkpoint {
	type k struct {
		key string
		ver int
	}
	last := map[k]store.Checkpoint{}
	for _, c := range cs {
		last[k{c.Key, c.StateVersion}] = c
	}
	out := make([]store.Checkpoint, 0, len(last))
	for _, c := range last {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b store.Checkpoint) int {
		if a.Key != b.Key {
			if a.Key < b.Key {
				return -1
			}
			return 1
		}
		return a.StateVersion - b.StateVersion
	})
	return out
}

func diffCheckpoints(id string, stored, want []store.Checkpoint) []string {
	type k struct {
		key string
		ver int
	}
	have := map[k]store.Checkpoint{}
	for _, c := range stored {
		have[k{c.Key, c.StateVersion}] = c
	}
	var out []string
	for _, w := range want {
		key := k{w.Key, w.StateVersion}
		c, ok := have[key]
		delete(have, key)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("session %q: checkpoint %s v%d missing", id, w.Key, w.StateVersion))
		case c != w:
			out = append(out, fmt.Sprintf("session %q: checkpoint %s v%d differs (stored seq %d, refolded seq %d)",
				id, w.Key, w.StateVersion, c.Seq, w.Seq))
		}
	}
	extra := make([]string, 0, len(have))
	for _, c := range have {
		extra = append(extra, fmt.Sprintf("session %q: checkpoint %s v%d not produced by the refold", id, c.Key, c.StateVersion))
	}
	slices.Sort(extra)
	return append(out, extra...)
}
