package workspace

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// The reasons of membership and flags (DH-D-STORAGE-7).
const (
	// ReasonNoSession is a session the store does not have (or is deleting).
	ReasonNoSession herr.Reason = "workspace/no-session"
	// ReasonOutside is a session whose recorded directory does not resolve to
	// the workspace's folder.
	ReasonOutside herr.Reason = "workspace/outside"
	// ReasonAttached is a session already attached to another workspace.
	ReasonAttached herr.Reason = "workspace/attached-elsewhere"
)

// More sentinels for errors.Is.
var (
	ErrNoSession = herr.New(ReasonNoSession, "")
	ErrOutside   = herr.New(ReasonOutside, "")
	ErrAttached  = herr.New(ReasonAttached, "")
)

// ArchiveState is a session's archive mark (DH-ADR-STORAGE-7).
type ArchiveState string

// The archive states. A pending mark is written before activity is asked
// about; the step gate already treats it as blocked.
const (
	ArchiveNone     ArchiveState = ""
	ArchivePending  ArchiveState = "pending"
	ArchiveArchived ArchiveState = "archived"
)

// Hidden reports whether s hides the session and blocks its steps.
func (s ArchiveState) Hidden() bool { return s == ArchivePending || s == ArchiveArchived }

// Flags are a session's flag rows; the zero value is a session never flagged.
type Flags struct {
	Archive ArchiveState
	// ArchiveAt is when Archive was last set, UTC; zero when never.
	ArchiveAt time.Time
	// Owner is the archive operation that holds a pending mark; "" otherwise.
	Owner  string
	Pinned bool
}

// Transition is a conditional change of an archive mark: it applies only when
// the mark is From and, when From is pending, Owner is exactly its holder.
// NewOwner holds the mark when To is pending.
type Transition struct {
	From     ArchiveState
	Owner    string
	To       ArchiveState
	NewOwner string
}

func (t Transition) valid() error {
	for _, s := range []ArchiveState{t.From, t.To} {
		if s != ArchiveNone && s != ArchivePending && s != ArchiveArchived {
			return herr.Newf(ReasonInvalid, "archive state %q", s)
		}
	}
	return nil
}

func (t Transition) matches(f Flags) bool {
	return f.Archive == t.From && (t.From != ArchivePending || f.Owner == t.Owner)
}

func (t Transition) owner() string {
	if t.To == ArchivePending {
		return t.NewOwner
	}
	return ""
}

// sessionUID is the store row of the active session id, read in tx.
func sessionUID(ctx context.Context, tx *sql.Tx, id session.ID) (uid int64, cwd string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT uid, cwd FROM sessions WHERE id = ? AND state = 'active'`, string(id)).Scan(&uid, &cwd)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", herr.Newf(ReasonNoSession, "no session %s", id)
	}
	return uid, cwd, err
}

// Attach makes session id a member of workspace wsID. The session's recorded
// working directory is resolved once, now, and must be the workspace's folder;
// the membership is never re-derived. Attaching again to the same workspace is
// a no-op; a session attached elsewhere is workspace/attached-elsewhere.
func (r *Registry) Attach(ctx context.Context, wsID string, id session.ID) error {
	w, err := r.Get(ctx, wsID)
	if err != nil {
		return err
	}
	now := r.now().UnixNano()
	err = r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		uid, cwd, err := sessionUID(ctx, tx, id)
		if err != nil {
			return err
		}
		if cwd == "" {
			return herr.Newf(ReasonOutside, "session %s records no working directory", id)
		}
		real, err := Resolve(cwd)
		if err != nil {
			return herr.Wrap(ReasonOutside, err, "session "+string(id))
		}
		if real != w.Path {
			return herr.Newf(ReasonOutside, "session %s works in %s, not in workspace %s (%s)", id, real, w.ID, w.Path)
		}
		var cur string
		switch err := tx.QueryRowContext(ctx, `SELECT workspace FROM workspace_sessions WHERE session = ?`, uid).Scan(&cur); {
		case err == nil && cur == w.ID:
			return nil
		case err == nil:
			return herr.Newf(ReasonAttached, "session %s is attached to workspace %s", id, cur)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_sessions (session, workspace, attached_at) VALUES (?, ?, ?)`, uid, w.ID, now); err != nil {
			return err
		}
		_, err = kv.BumpRevision(ctx, tx, Namespace)
		return err
	})
	return kv.Classify(err)
}

// Detach ends session id's membership; a session in no workspace is a no-op.
func (r *Registry) Detach(ctx context.Context, id session.ID) error {
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		uid, _, err := sessionUID(ctx, tx, id)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM workspace_sessions WHERE session = ?`, uid)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		_, err = kv.BumpRevision(ctx, tx, Namespace)
		return err
	})
	return kv.Classify(err)
}

// Members is the active sessions of workspace wsID, ordered by id.
func (r *Registry) Members(ctx context.Context, wsID string) ([]session.ID, error) {
	if _, err := r.Get(ctx, wsID); err != nil {
		return nil, err
	}
	var out []session.ID
	err := r.db.ReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id FROM workspace_sessions m JOIN sessions s ON s.uid = m.session
			WHERE m.workspace = ? AND s.state = 'active' ORDER BY s.id`, wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, session.ID(id))
		}
		return rows.Err()
	})
	return out, kv.Classify(err)
}

// WorkspaceOf is the workspace session id is attached to; ok is false for an
// ungrouped (or unknown) session.
func (r *Registry) WorkspaceOf(ctx context.Context, id session.ID) (wsID string, ok bool, err error) {
	err = r.db.ReadTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT m.workspace FROM workspace_sessions m JOIN sessions s ON s.uid = m.session
			WHERE s.id = ? AND s.state = 'active'`, string(id)).Scan(&wsID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		ok = err == nil
		return err
	})
	return wsID, ok, kv.Classify(err)
}

// Flags is session id's flags; a session never flagged has the zero Flags.
func (r *Registry) Flags(ctx context.Context, id session.ID) (Flags, error) {
	var f Flags
	err := r.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var (
			state, owner string
			at           int64
			pinned       int
		)
		err := tx.QueryRowContext(ctx, `SELECT f.archive, f.archive_at, f.archive_owner, f.pinned FROM session_flags f JOIN sessions s ON s.uid = f.session
			WHERE s.id = ?`, string(id)).Scan(&state, &at, &owner, &pinned)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		f = Flags{Archive: ArchiveState(state), Owner: owner, Pinned: pinned == 1}
		if at != 0 {
			f.ArchiveAt = stamp(at)
		}
		return nil
	})
	return f, kv.Classify(err)
}

// SetArchive sets session id's archive mark; any mark but none clears the pin
// (archiving clears the pin, DH-T-STORAGE-17).
func (r *Registry) SetArchive(ctx context.Context, id session.ID, s ArchiveState) error {
	if s != ArchiveNone && s != ArchivePending && s != ArchiveArchived {
		return herr.Newf(ReasonInvalid, "archive state %q", s)
	}
	now := r.now().UnixNano()
	return r.flag(ctx, id, func(tx *sql.Tx, uid int64) error { return writeArchive(ctx, tx, uid, s, "", now) })
}

func writeArchive(ctx context.Context, tx *sql.Tx, uid int64, s ArchiveState, owner string, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO session_flags (session, archive, archive_at, archive_owner, pinned, updated_at) VALUES (?, ?, ?, ?, 0, ?)
		ON CONFLICT (session) DO UPDATE SET archive = excluded.archive, archive_at = excluded.archive_at, archive_owner = excluded.archive_owner,
			pinned = CASE WHEN excluded.archive = '' THEN pinned ELSE 0 END, updated_at = excluded.updated_at`,
		uid, string(s), now, owner, now)
	return err
}

// TransitionArchive applies t to session id's archive mark in one write
// transaction, so it is atomic across processes; ok is false (and nothing is
// written) when the mark is not t.From held by t.Owner.
func (r *Registry) TransitionArchive(ctx context.Context, id session.ID, t Transition) (ok bool, err error) {
	if err := t.valid(); err != nil {
		return false, err
	}
	now := r.now().UnixNano()
	err = r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		uid, _, err := sessionUID(ctx, tx, id)
		if err != nil {
			return err
		}
		var state, owner string
		switch err := tx.QueryRowContext(ctx, `SELECT archive, archive_owner FROM session_flags WHERE session = ?`, uid).Scan(&state, &owner); {
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		case !t.matches(Flags{Archive: ArchiveState(state), Owner: owner}):
			return nil
		}
		if err := writeArchive(ctx, tx, uid, t.To, t.owner(), now); err != nil {
			return err
		}
		ok = true
		_, err = kv.BumpRevision(ctx, tx, Namespace)
		return err
	})
	return ok, kv.Classify(err)
}

// Pin sets or clears session id's pin. A hidden session is not pinned.
func (r *Registry) Pin(ctx context.Context, id session.ID, pinned bool) error {
	now := r.now().UnixNano()
	return r.flag(ctx, id, func(tx *sql.Tx, uid int64) error {
		var state string
		switch err := tx.QueryRowContext(ctx, `SELECT archive FROM session_flags WHERE session = ?`, uid).Scan(&state); {
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		case pinned && ArchiveState(state).Hidden():
			return herr.Newf(ReasonInvalid, "session %s is archived; unarchive it to pin it", id)
		}
		p := 0
		if pinned {
			p = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO session_flags (session, pinned, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (session) DO UPDATE SET pinned = excluded.pinned, updated_at = excluded.updated_at`, uid, p, now)
		return err
	})
}

// ClearPending removes every pending archive mark, returning how many: a
// pending mark a crash left behind is cleared at the next start.
func (r *Registry) ClearPending(ctx context.Context) (int, error) {
	var n int64
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE session_flags SET archive = '', archive_owner = '', updated_at = ? WHERE archive = 'pending'`, r.now().UnixNano())
		if err != nil {
			return err
		}
		if n, err = res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		_, err = kv.BumpRevision(ctx, tx, Namespace)
		return err
	})
	return int(n), kv.Classify(err)
}

func (r *Registry) flag(ctx context.Context, id session.ID, write func(tx *sql.Tx, uid int64) error) error {
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		uid, _, err := sessionUID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := write(tx, uid); err != nil {
			return err
		}
		_, err = kv.BumpRevision(ctx, tx, Namespace)
		return err
	})
	return kv.Classify(err)
}
