// Package kv is the typed machine-state store of docs/harness/areas/storage.md
// (DH-D-STORAGE-1, DH-ADR-STORAGE-1, DH-ADR-STORAGE-2): a component declares a
// namespace and a Go record type and gets durable, transactional records with
// change notices, kept in the `kv` table of the one dsh.db.
//
// There is no hub and no backend: a Namespace is a thin layer over the database
// handle of internal/session/store/sqlite (its WriteTx and ReadTx, which pass
// through the store's one writer and bounded busy wait), and its tables join that
// database's ordered layout migrations.
//
//   - Open registers the namespace row (with its authority) and loads a snapshot.
//   - Update runs a callback in one write transaction, increases the namespace
//     revision in the same transaction, then swaps the snapshot and raises a
//     Change per key. A failed commit leaves memory unchanged and raises nothing.
//   - Every record carries the version it was written at. A record of an older
//     version is converted on read by a registered Converter and rewritten only
//     when next written; a record of a newer version fails that record's read
//     (storage/record-version) and nothing else.
//   - A record that cannot be decoded fails Open of an authoritative namespace
//     and is dropped (reported to OnDrop) from a derived one; nothing is deleted
//     from the database automatically.
//   - Memory is a cache tagged with the revision: Fresh compares it with the
//     stored revision and reloads, raising Changes with origin Foreign; Run does
//     the same on a timer. A read that gates an action calls Fresh first.
//
// Reads return copies: each Get decodes the stored bytes afresh.
package kv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// The reasons this package raises.
const (
	// ReasonBusy is a write that waited past the database's busy bound.
	ReasonBusy herr.Reason = "storage/busy"
	// ReasonRecordVersion is a record written by a newer build, or an older one
	// with no converter.
	ReasonRecordVersion herr.Reason = "storage/record-version"
	// ReasonUndecodable is a stored record whose bytes do not decode.
	ReasonUndecodable herr.Reason = "storage/undecodable"
	// ReasonAuthority is a namespace opened with an authority other than the
	// one it was created with.
	ReasonAuthority herr.Reason = "storage/authority"
	// ReasonInvalid is a malformed Spec or key.
	ReasonInvalid herr.Reason = "storage/invalid"
)

// Sentinels for errors.Is.
var (
	ErrBusy          = herr.New(ReasonBusy, "")
	ErrRecordVersion = herr.New(ReasonRecordVersion, "")
	ErrUndecodable   = herr.New(ReasonUndecodable, "")
	ErrAuthority     = herr.New(ReasonAuthority, "")
	ErrInvalid       = herr.New(ReasonInvalid, "")
)

// DB is the one database handle; *sqlite.Store satisfies it.
type DB interface {
	WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error
	ReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error
}

var _ DB = (*sqlite.Store)(nil)

// Authority says what a damaged record means for its namespace.
type Authority string

// The two authorities.
const (
	// Authoritative state is the only copy: an undecodable record fails Open.
	Authoritative Authority = "authoritative"
	// Derived state can be rebuilt: an undecodable record is dropped.
	Derived Authority = "derived"
)

// Op is what a Change did.
type Op string

// The operations.
const (
	OpPut    Op = "put"
	OpDelete Op = "delete"
)

// Origin says who made a Change.
type Origin string

// The origins.
const (
	// Local is a write through this Namespace.
	Local Origin = "local"
	// Foreign is a write another instance made, seen by Fresh.
	Foreign Origin = "foreign"
)

// Change is the notice raised after a commit, in commit order per namespace. It
// carries no value: a listener calls Get.
type Change struct {
	Namespace string
	Key       string
	Op        Op
	Revision  int64
	Origin    Origin
}

// Converter turns a record stored at an older version into the current type.
type Converter[T any] func(raw json.RawMessage) (T, error)

// Spec declares a namespace.
type Spec[T any] struct {
	// Namespace is the namespace's name; not empty.
	Namespace string
	// Version is the record version this build writes; at least 1.
	Version int
	// Authority is Authoritative or Derived.
	Authority Authority
	// Convert holds a converter per older version still readable.
	Convert map[int]Converter[T]
	// Now stamps updated_at; nil is time.Now.
	Now func() time.Time
	// OnDrop is told of each undecodable record dropped from a derived
	// namespace; nil ignores it.
	OnDrop func(key string, err error)
}

// querier is a *sql.Tx or anything else that runs one-row queries.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// EnsureNamespace registers namespace with authority in tx when it is new and
// refuses one created with another authority. Other stores of this area that
// keep their own tables (the workspace registry) use it for their revision row.
func EnsureNamespace(ctx context.Context, tx *sql.Tx, namespace string, a Authority) error {
	if namespace == "" || (a != Authoritative && a != Derived) {
		return herr.Newf(ReasonInvalid, "namespace %q with authority %q", namespace, a)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO kv_ns (namespace, revision, authority) VALUES (?, 0, ?)
		ON CONFLICT (namespace) DO NOTHING`, namespace, string(a)); err != nil {
		return err
	}
	var got string
	if err := tx.QueryRowContext(ctx, `SELECT authority FROM kv_ns WHERE namespace = ?`, namespace).Scan(&got); err != nil {
		return err
	}
	if Authority(got) != a {
		return herr.Newf(ReasonAuthority, "namespace %q is %s, not %s", namespace, got, a)
	}
	return nil
}

// Revision is the stored revision of namespace, read in q.
func Revision(ctx context.Context, q querier, namespace string) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx, `SELECT revision FROM kv_ns WHERE namespace = ?`, namespace).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, herr.Newf(ReasonInvalid, "namespace %q is not registered", namespace)
	}
	return rev, err
}

// BumpRevision increases namespace's revision in tx and returns the new one.
func BumpRevision(ctx context.Context, tx *sql.Tx, namespace string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE kv_ns SET revision = revision + 1 WHERE namespace = ?`, namespace); err != nil {
		return 0, err
	}
	return Revision(ctx, tx, namespace)
}

// Classify gives a database error its storage reason: a write that waited past
// the busy bound is storage/busy; anything else is returned as it is.
func Classify(err error) error {
	var se *store.Error
	if errors.As(err, &se) && se.Code == sqlite.CodeBusy {
		return herr.Wrap(ReasonBusy, err, "the database stayed locked past the busy bound")
	}
	return err
}
