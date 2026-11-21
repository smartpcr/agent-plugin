// Package workspace is the workspace registry of docs/harness/areas/storage.md
// §6.1: the folders dsh works in, kept as typed machine state in the
// `workspaces` table of the one dsh.db (DH-D-STORAGE-1). It is authoritative
// state: there is no other copy.
//
// Each change runs in one write transaction that also increases the
// "workspace" namespace revision, so a second instance sees a stale view by
// comparing Revision. Reads go to the database and are always fresh. Removing a
// workspace removes its row only; the folder on disk is never touched.
package workspace

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Namespace is the kv_ns row that carries the registry's revision.
const Namespace = "workspace"

// The reasons this package raises.
const (
	// ReasonNotFound is an unknown workspace id.
	ReasonNotFound herr.Reason = "workspace/not-found"
	// ReasonExists is a folder that is already registered.
	ReasonExists herr.Reason = "workspace/exists"
	// ReasonNotDirectory is a path that is not an existing folder.
	ReasonNotDirectory herr.Reason = "workspace/not-a-directory"
	// ReasonInvalid is a malformed argument.
	ReasonInvalid herr.Reason = "workspace/invalid"
)

// Sentinels for errors.Is.
var (
	ErrNotFound     = herr.New(ReasonNotFound, "")
	ErrExists       = herr.New(ReasonExists, "")
	ErrNotDirectory = herr.New(ReasonNotDirectory, "")
	ErrInvalid      = herr.New(ReasonInvalid, "")
)

// Workspace is one registered folder.
type Workspace struct {
	// ID is the stable identity, "ws_" and 16 hex digits.
	ID string
	// Path is the folder's absolute path with links resolved; unique.
	Path string
	// RequestedPath is the path as the user gave it.
	RequestedPath string
	// Title is the display name; the folder's base name unless renamed.
	Title string
	// Position orders the registry; new workspaces go last.
	Position int
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Registry is the workspace registry over one database. It is safe for
// concurrent use; the database's one writer serialises changes.
type Registry struct {
	db  kv.DB
	now func() time.Time
}

// Option configures Open.
type Option func(*Registry)

// WithClock stamps rows with now instead of time.Now.
func WithClock(now func() time.Time) Option { return func(r *Registry) { r.now = now } }

// Open registers the "workspace" namespace in db and returns the registry.
func Open(ctx context.Context, db kv.DB, opts ...Option) (*Registry, error) {
	if db == nil {
		return nil, herr.New(ReasonInvalid, "a workspace registry needs a database")
	}
	r := &Registry{db: db, now: time.Now}
	for _, o := range opts {
		o(r)
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		return kv.EnsureNamespace(ctx, tx, Namespace, kv.Authoritative)
	}); err != nil {
		return nil, kv.Classify(err)
	}
	return r, nil
}

const columns = `id, path, requested_path, title, position, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scan(s scanner) (Workspace, error) {
	var (
		w        Workspace
		cat, uat int64
	)
	if err := s.Scan(&w.ID, &w.Path, &w.RequestedPath, &w.Title, &w.Position, &cat, &uat); err != nil {
		return Workspace{}, err
	}
	w.CreatedAt, w.UpdatedAt = stamp(cat), stamp(uat)
	return w, nil
}

func stamp(n int64) time.Time { return time.Unix(0, n).UTC() }

// Resolve is path as the registry keys it: absolute, cleaned, links resolved.
// It must name an existing folder.
func Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", herr.New(ReasonInvalid, "an empty workspace path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", herr.Wrap(ReasonInvalid, err, "workspace path "+path)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", herr.Wrap(ReasonNotDirectory, err, "workspace path "+path)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", herr.Wrap(ReasonNotDirectory, err, "workspace path "+path)
	}
	if !fi.IsDir() {
		return "", herr.Newf(ReasonNotDirectory, "workspace path %s is not a folder", path)
	}
	return filepath.Clean(real), nil
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ws_" + hex.EncodeToString(b[:]), nil
}

// Add registers the folder at path, last in order. A folder that is already
// registered is workspace/exists, returned with the existing workspace.
func (r *Registry) Add(ctx context.Context, path string) (Workspace, error) {
	return r.AddWith(ctx, path, AddOptions{})
}

// AddOptions configures AddWith.
type AddOptions struct {
	// Adopt attaches, in the same transaction, every ungrouped active session
	// whose recorded working directory resolves to the new workspace's folder.
	Adopt bool
}

// AddWith is Add, adopting ungrouped sessions when o asks.
func (r *Registry) AddWith(ctx context.Context, path string, o AddOptions) (Workspace, error) {
	real, err := Resolve(path)
	if err != nil {
		return Workspace{}, err
	}
	id, err := newID()
	if err != nil {
		return Workspace{}, herr.Wrap(ReasonInvalid, err, "a workspace id")
	}
	now := r.now().UnixNano()
	w := Workspace{ID: id, Path: real, RequestedPath: path, Title: filepath.Base(real), CreatedAt: stamp(now), UpdatedAt: stamp(now)}
	var existing *Workspace
	err = r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		old, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM workspaces WHERE path = ?`, real))
		switch {
		case err == nil:
			existing = &old
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if err := insert(ctx, tx, &w, now); err != nil {
			return err
		}
		if o.Adopt {
			if err := adopt(ctx, tx, w, now); err != nil {
				return err
			}
		}
		// A workspace has existed: first use is over.
		return decideFirstUse(ctx, tx, "", now)
	})
	if err != nil {
		return Workspace{}, kv.Classify(err)
	}
	if existing != nil {
		return *existing, herr.Newf(ReasonExists, "the folder %s is already workspace %s", real, existing.ID)
	}
	return w, nil
}

// adopt attaches to w every active session in no workspace whose recorded
// working directory resolves to w's folder.
func adopt(ctx context.Context, tx *sql.Tx, w Workspace, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.uid, s.cwd FROM sessions s
		LEFT JOIN workspace_sessions m ON m.session = s.uid
		WHERE s.state = 'active' AND s.cwd <> '' AND m.session IS NULL`)
	if err != nil {
		return err
	}
	type cand struct {
		uid int64
		cwd string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.uid, &c.cwd); err != nil {
			rows.Close()
			return err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	resolved := map[string]bool{}
	for _, c := range cands {
		in, ok := resolved[c.cwd]
		if !ok {
			real, err := Resolve(c.cwd)
			in = err == nil && real == w.Path
			resolved[c.cwd] = in
		}
		if !in {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_sessions (session, workspace, attached_at) VALUES (?, ?, ?)`, c.uid, w.ID, now); err != nil {
			return err
		}
	}
	return nil
}

// insert adds w last in order and bumps the revision.
func insert(ctx context.Context, tx *sql.Tx, w *Workspace, now int64) error {
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM workspaces`).Scan(&w.Position); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (`+columns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Path, w.RequestedPath, w.Title, w.Position, now, now); err != nil {
		return err
	}
	_, err := kv.BumpRevision(ctx, tx, Namespace)
	return err
}

// decideFirstUse records the first-use decision unless one is recorded:
// created is the default workspace EnsureDefault made, "" for none.
func decideFirstUse(ctx context.Context, tx *sql.Tx, created string, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO workspace_first_use (id, workspace, decided_at) VALUES (1, ?, ?)`, created, now)
	return err
}

// EnsureDefault registers the folder at path as the first-use workspace when,
// checked inside one transaction, no first use was decided before, no workspace
// exists and no session ever existed (a deleted one counts: session uids are
// never reused). It remembers the decision either way, so it creates at most
// once per home and never again after that workspace is removed; an Add also
// ends first use. created reports whether it made the workspace now.
func (r *Registry) EnsureDefault(ctx context.Context, path string) (w Workspace, created bool, err error) {
	real, err := Resolve(path)
	if err != nil {
		return Workspace{}, false, err
	}
	id, err := newID()
	if err != nil {
		return Workspace{}, false, herr.Wrap(ReasonInvalid, err, "a workspace id")
	}
	now := r.now().UnixNano()
	w = Workspace{ID: id, Path: real, RequestedPath: path, Title: filepath.Base(real), CreatedAt: stamp(now), UpdatedAt: stamp(now)}
	err = r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var decided, used bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspace_first_use)`).Scan(&decided); err != nil || decided {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces)
			OR EXISTS (SELECT 1 FROM sessions)
			OR EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'sessions' AND seq > 0)`).Scan(&used); err != nil {
			return err
		}
		if used {
			return decideFirstUse(ctx, tx, "", now)
		}
		if err := insert(ctx, tx, &w, now); err != nil {
			return err
		}
		created = true
		return decideFirstUse(ctx, tx, w.ID, now)
	})
	if err != nil || !created {
		return Workspace{}, false, kv.Classify(err)
	}
	return w, true, nil
}

// Get is the workspace with id.
func (r *Registry) Get(ctx context.Context, id string) (Workspace, error) {
	return r.one(ctx, `WHERE id = ?`, id, "workspace "+id)
}

// ByPath is the workspace registered for the folder at path.
func (r *Registry) ByPath(ctx context.Context, path string) (Workspace, error) {
	real, err := Resolve(path)
	if err != nil {
		return Workspace{}, err
	}
	return r.one(ctx, `WHERE path = ?`, real, "the folder "+real)
}

func (r *Registry) one(ctx context.Context, where, arg, what string) (Workspace, error) {
	var w Workspace
	err := r.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		w, err = scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM workspaces `+where, arg))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, herr.Newf(ReasonNotFound, "%s is not registered", what)
	}
	return w, kv.Classify(err)
}

// List is every workspace in order.
func (r *Registry) List(ctx context.Context) ([]Workspace, error) {
	var out []Workspace
	err := r.db.ReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM workspaces ORDER BY position, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			w, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, kv.Classify(err)
}

// Rename sets the workspace's title.
func (r *Registry) Rename(ctx context.Context, id, title string) (Workspace, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Workspace{}, herr.New(ReasonInvalid, "an empty workspace title")
	}
	now := r.now().UnixNano()
	if err := r.change(ctx, id, `UPDATE workspaces SET title = ?, updated_at = ? WHERE id = ?`, title, now, id); err != nil {
		return Workspace{}, err
	}
	return r.Get(ctx, id)
}

// Remove unregisters the workspace. The folder is not touched.
func (r *Registry) Remove(ctx context.Context, id string) error {
	return r.change(ctx, id, `DELETE FROM workspaces WHERE id = ?`, id)
}

func (r *Registry) change(ctx context.Context, id, stmt string, args ...any) error {
	var missing bool
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, stmt, args...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			missing = err == nil
			return err
		}
		_, err = kv.BumpRevision(ctx, tx, Namespace)
		return err
	})
	if err != nil {
		return kv.Classify(err)
	}
	if missing {
		return herr.Newf(ReasonNotFound, "workspace %s is not registered", id)
	}
	return nil
}

// Revision is the registry's stored revision; it increases with every change.
func (r *Registry) Revision(ctx context.Context) (int64, error) {
	var rev int64
	err := r.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rev, err = kv.Revision(ctx, tx, Namespace)
		return err
	})
	return rev, kv.Classify(err)
}
