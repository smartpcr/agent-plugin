// Package store is the one storage contract behind the session log (DH-D-STORE-1,
// docs/harness/areas/session-store.md §6.1): Store, Reader and Writer, the batch,
// summary and listing types and the error values. The session core (area A01)
// calls it directly; every provider implements it and must pass the conformance
// suite in storetest. memstore is the in-memory reference used by tests.
//
// A Record is event.Record: the envelope fields, the payload version and the two
// payload parts as immutable strings holding the stored bytes. A store never
// re-encodes them, and neither side writes to a record after hand-over.
//
// The optional interfaces DegradedNotifier, DerivedCounter and DerivedWriter
// carry the derived-row rules of DH-D-VIEW-2: a refused derived row costs no
// event, and a derived row is written forward without the lease.
//
// Not yet in this contract (later deliverables): FileRequest, Requests and
// WatchRequest (DH-D-STORE-11), Follow and Changes (DH-D-STORE-8), Instances
// (DH-D-STORE-3) and the derived rows of a batch other than checkpoints
// (DH-D-VIEW-2).
package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Record is the stored form of one event.
type Record = event.Record

// Seq is a position in a session's log.
type Seq = event.Seq

// Header is a session's immutable header (core-session.md §6.2).
type Header struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Cwd       string    `json:"cwd,omitempty"`
	// Origin is empty or "subagent".
	Origin string `json:"origin,omitempty"`
	// Creator is a subagent's direct creator.
	Creator string `json:"creator,omitempty"`
	// ForkSource and ForkThrough are set on every fork: the parent and the last
	// inherited position.
	ForkSource      string `json:"fork_source,omitempty"`
	ForkThrough     Seq    `json:"fork_through,omitempty"`
	InheritedCount  int    `json:"inherited_count,omitempty"`
	DelegationDepth int    `json:"delegation_depth,omitempty"`
	Preset          string `json:"preset,omitempty"`
}

// ForkOf names a parent session and the last position of it the child inherits.
type ForkOf struct {
	ID      string
	Through Seq
}

// CreateOptions are Create's options.
type CreateOptions struct {
	// ForkOf, when set, copies the parent's records 0..Through into the child,
	// and the store fills the header's fork fields.
	ForkOf *ForkOf
}

// Takeover is what OpenWrite does when another writer holds the session.
type Takeover int

const (
	// IfFree refuses with session/owned while the holder is alive.
	IfFree Takeover = iota
	// Force takes the lease from a live holder: an explicit action, never
	// automatic. The displaced writer learns it at its next append or flush.
	Force
)

// OpenOptions are OpenWrite's options.
type OpenOptions struct{ Takeover Takeover }

// ReadOptions are Read's and ReadSeqs' options.
type ReadOptions struct {
	// WithAux also loads the cold part; without it Record.Cold is empty.
	WithAux bool
}

// DeleteOptions are Delete's options; none yet.
type DeleteOptions struct{}

// Checkpoint is a fold's state at a position. The store keeps the newest per
// (Key, StateVersion); keys under "core/" are the session core's.
type Checkpoint struct {
	Key          string
	StateVersion int
	Seq          Seq
	State        string
}

// Batch is one append: the records at positions First.. in order, which commit as
// one transaction with the checkpoints handed over with them.
type Batch struct {
	First       Seq
	Records     []Record
	Checkpoints []Checkpoint
}

// Holder identifies a lease holder.
type Holder struct {
	Instance string
	PID      int
	Kind     string
	Since    time.Time
}

// Summary describes one session without reading its events.
type Summary struct {
	Header Header
	// NextSeq is the number of stored events (accepted, in the writing process).
	NextSeq Seq
	// Durable is the number of durable events.
	Durable    Seq
	LeaseEpoch uint64
	// Holder is the lease holder, nil when none.
	Holder      *Holder
	LastEventAt time.Time
	// LastPromptAt is the time of the last PromptType record; zero when none.
	LastPromptAt time.Time
}

// PromptType is the record type of a prompt: the one that moves LastPromptAt
// (event.UserMessage).
var PromptType = event.UserMessage.Name()

// Order is the order of List's pages.
type Order int

const (
	// OrderRecent is the most recent activity first (LastEventAt, then
	// CreatedAt, descending), then ID.
	OrderRecent Order = iota
	// OrderPrompt is the last prompt first, "as of" the first page: a session's
	// key is the time of its last timed prompt before the cut's millisecond
	// (PromptCut; its CreatedAt when it has none), descending, then ID; a
	// session created after the cut is not listed. The cut travels in the
	// cursor, so a prompt appended while a caller pages never moves a session
	// across the cursor: no session is skipped or listed twice.
	OrderPrompt
)

// MaxPage is the largest page a store's List returns.
const MaxPage = 1000

// PageLimit is the page size q asks for: def when it asks for none, at most
// MaxPage.
func (q Query) PageLimit(def int) int {
	switch {
	case q.Limit <= 0:
		return def
	case q.Limit > MaxPage:
		return MaxPage
	}
	return q.Limit
}

// Query filters and pages List.
type Query struct {
	Cwd     string
	Creator string
	// Limit is the page size; 0 (or less) means 50, more than MaxPage means
	// MaxPage.
	Limit int
	// Cursor is the Next of the previous page; empty for the first.
	Cursor string
	// Order is the order of the pages; a cursor is valid only in its order.
	Order Order
	// AsOf is the cut of an OrderPrompt first page; zero is the store's clock
	// now. Later pages take the cut from Cursor.
	AsOf time.Time
}

// PromptCursor is an OrderPrompt List position: the cut (AsOf, Unix
// nanoseconds) and the key and id of the last summary returned.
type PromptCursor struct {
	AsOf, Key int64
	ID        string
}

// String is the cursor as List's Next.
func (c PromptCursor) String() string { return fmt.Sprintf("p.%d.%d.%s", c.AsOf, c.Key, c.ID) }

// Precedes reports whether c comes before the summary of key and id in
// OrderPrompt, that is whether that summary is still to be listed.
func (c PromptCursor) Precedes(key int64, id string) bool {
	return key < c.Key || (key == c.Key && id > c.ID)
}

// ParsePromptCursor is the PromptCursor v names; a cursor of another order is
// session/invalid-boundary.
func ParsePromptCursor(v string) (PromptCursor, error) {
	parts := strings.SplitN(v, ".", 4)
	if len(parts) == 4 && parts[0] == "p" {
		asOf, err1 := strconv.ParseInt(parts[1], 10, 64)
		key, err2 := strconv.ParseInt(parts[2], 10, 64)
		if err1 == nil && err2 == nil {
			return PromptCursor{AsOf: asOf, Key: key, ID: parts[3]}, nil
		}
	}
	return PromptCursor{}, Errorf(CodeInvalidBoundary, "list cursor %q is not a prompt-order cursor this store issued", v)
}

// PromptCut is the last prompt time (Unix nanoseconds) a cut takes in: a
// prompt is before the cut when its millisecond is. Record times are
// milliseconds, so a prompt appended after the first page, in the cut's own
// millisecond or later, is after the cut and never moves a session across the
// cursor.
func PromptCut(cut int64) int64 { return time.UnixMilli(time.Unix(0, cut).UnixMilli()).UnixNano() - 1 }

// PromptKey is a session's OrderPrompt key as of cut (Unix nanoseconds): the
// time of its last timed PromptType record at or before cut, else its
// creation. ok is false when the session was created after cut.
func PromptKey(created time.Time, recs []Record, cut int64) (key int64, ok bool) {
	if created.UnixNano() > cut {
		return 0, false
	}
	pcut := PromptCut(cut)
	for i := len(recs) - 1; i >= 0; i-- {
		if r := recs[i]; r.Type == PromptType && r.Time > 0 {
			if at := time.UnixMilli(r.Time).UnixNano(); at <= pcut {
				return at, true
			}
		}
	}
	return created.UnixNano(), true
}

// Page is one page of summaries; Next is empty after the last page.
type Page struct {
	Summaries []Summary
	Next      string
}

// Store is a session store.
type Store interface {
	// Create makes a new session and returns its writer, which holds the lease.
	Create(ctx context.Context, h Header, o CreateOptions) (Writer, error)
	// OpenWrite takes write ownership. An open is refused when a record has an
	// undeclared required type or a newer payload version.
	OpenWrite(ctx context.Context, id string, o OpenOptions) (Writer, error)
	// OpenRead observes without ownership; refused as OpenWrite is.
	OpenRead(ctx context.Context, id string) (Reader, error)
	// Stat summarises one session.
	Stat(ctx context.Context, id string) (Summary, error)
	// List returns a filtered page of summaries.
	List(ctx context.Context, q Query) (Page, error)
	// Delete removes a session; it is refused while a writer holds it.
	Delete(ctx context.Context, id string, o DeleteOptions) error
	// Flush is a barrier over every writer of this instance.
	Flush(ctx context.Context) error
}

// Reader reads one session. In the writing process it sees every accepted
// record, flushed or not.
type Reader interface {
	Header() Header
	// Read returns up to limit records from position from, in seq order; limit
	// 0 means all of them. A from at or past the end returns none.
	Read(ctx context.Context, from Seq, limit int, o ReadOptions) ([]Record, error)
	// ReadSeqs returns the records at seqs, in the order given.
	ReadSeqs(ctx context.Context, seqs []Seq, o ReadOptions) ([]Record, error)
	// Checkpoints is the newest checkpoint per key and state version, sorted.
	Checkpoints(ctx context.Context) ([]Checkpoint, error)
	// Close releases the reader; it is idempotent.
	Close(ctx context.Context) error
}

// Writer is a Reader that holds the session's lease.
type Writer interface {
	Reader
	// Epoch is the lease epoch this writer holds.
	Epoch() uint64
	// Append accepts and orders a batch; it is not yet durable.
	Append(ctx context.Context, b Batch) error
	// Flush returns when every record accepted before the call is durable.
	Flush(ctx context.Context) error
	// Close (from Reader) flushes, releases the lease and keeps the epoch.
}

// The derived rows of a batch (its checkpoints) are written apart from its
// records (DH-D-VIEW-2, session-views.md "Failure"): a derived row the database
// refuses rolls back every derived row of that batch, as a group, and the
// records commit anyway. The append succeeds, the refused folds' stored rows
// keep their older seq (a reader sees them behind), and the store reports the
// loss through a Degraded notice and the writer's DerivedDropped count. A
// failing view therefore never costs a log event.

// DegradedDerived is the Reason of a Degraded notice whose append committed its
// records without its derived rows.
const DegradedDerived = "derived-dropped"

// Degraded is the notice a store sends when it did less than it was asked
// without failing the call (session-store.md §6.2, "Events and errors"): today,
// an append whose records committed while its derived rows were refused.
type Degraded struct {
	// Session is the session the notice is about.
	Session string
	// Reason says what was not done: DegradedDerived.
	Reason string
	// Keys are the dropped derived rows, "<key>@<state version>", in batch
	// order.
	Keys []string
	// Err is the refusal.
	Err error
}

// DegradedNotifier is a store that sends Degraded notices.
type DegradedNotifier interface {
	// OnDegraded calls fn with every notice from now on, after the call that
	// caused it has committed and outside the store's locks; stop ends the
	// subscription.
	OnDegraded(fn func(Degraded)) (stop func())
}

// DerivedCounter is a writer that counts the derived rows its appends dropped.
type DerivedCounter interface {
	// DerivedDropped is the number of derived rows this writer's appends
	// committed without.
	DerivedDropped() uint64
}

// DerivedWriter is a store that writes derived rows without the session's lease
// (session-views.md, "Ownership and access"): a pure fold gives every process
// the same answer, so a row may be written by any of them. It is not a takeover:
// the write never touches events, and it only moves a row forward.
type DerivedWriter interface {
	// UpsertDerived writes each row of session id whose seq is past the stored
	// row of its key and state version (or that has none) and below the
	// session's durable next seq; any other row is skipped, unchanged. It
	// returns how many rows it wrote. A row with no key is session/invalid-event
	// and writes nothing; a missing session is session/not-found.
	UpsertDerived(ctx context.Context, id string, rows []Checkpoint) (int, error)
}

// DerivedKey names a derived row in a Degraded notice: "<key>@<state version>".
func DerivedKey(c Checkpoint) string { return c.Key + "@" + strconv.Itoa(c.StateVersion) }

// Notices is a provider's set of Degraded subscribers, behind its OnDegraded.
// The zero value is empty and ready.
type Notices struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(Degraded)
}

// Subscribe adds fn; stop removes it, and is idempotent.
func (n *Notices) Subscribe(fn func(Degraded)) (stop func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fns == nil {
		n.fns = map[int]func(Degraded){}
	}
	id := n.next
	n.next++
	n.fns[id] = fn
	return func() {
		n.mu.Lock()
		delete(n.fns, id)
		n.mu.Unlock()
	}
}

// Send calls every subscriber with d, in subscription order, without holding
// n's lock, so a subscriber may call the store.
func (n *Notices) Send(d Degraded) {
	n.mu.Lock()
	ids := make([]int, 0, len(n.fns))
	for id := range n.fns {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	fns := make([]func(Degraded), 0, len(ids))
	for _, id := range ids {
		fns = append(fns, n.fns[id])
	}
	n.mu.Unlock()
	for _, fn := range fns {
		fn(d)
	}
}

// Code is a "<domain>/<reason>" error code.
type Code string

// The codes a store raises. session/unknown-type and session/newer-payload are
// event's *UnknownTypeError and *NewerPayloadError.
const (
	CodeNotFound        Code = "session/not-found"
	CodeExists          Code = "session/exists"
	CodeOwned           Code = "session/owned"
	CodeLeaseLost       Code = "session/lease-lost"
	CodeSeqConflict     Code = "session/seq-conflict"
	CodeDeleted         Code = "session/deleted"
	CodeClosed          Code = "session/closed"
	CodeInvalidBoundary Code = "session/invalid-boundary"
	CodeInvalidBatch    Code = "session/invalid-event"
)

// Error carries a Code; errors.Is matches a sentinel of the same code.
type Error struct {
	Code Code
	Msg  string
	// Err is the underlying cause, when there is one.
	Err error
}

// Unwrap is the cause.
func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Error() string {
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Msg == "" && t.Code == e.Code
}

// Errorf is an *Error with a formatted message, for providers.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Sentinels for errors.Is.
var (
	ErrNotFound        = &Error{Code: CodeNotFound}
	ErrExists          = &Error{Code: CodeExists}
	ErrOwned           = &Error{Code: CodeOwned}
	ErrLeaseLost       = &Error{Code: CodeLeaseLost}
	ErrSeqConflict     = &Error{Code: CodeSeqConflict}
	ErrDeleted         = &Error{Code: CodeDeleted}
	ErrClosed          = &Error{Code: CodeClosed}
	ErrInvalidBoundary = &Error{Code: CodeInvalidBoundary}
	ErrInvalidBatch    = &Error{Code: CodeInvalidBatch}
)

// OwnedError refuses a second writer, naming the holder.
type OwnedError struct {
	ID     string
	Holder Holder
}

func (e *OwnedError) Error() string {
	return fmt.Sprintf("%s: session %q is held by instance %q (pid %d, %s) since %s",
		CodeOwned, e.ID, e.Holder.Instance, e.Holder.PID, e.Holder.Kind, e.Holder.Since.Format(time.RFC3339))
}

// Is matches ErrOwned.
func (e *OwnedError) Is(target error) bool { return target == ErrOwned }

// Admit is the structural admission rule of one record (session-store.md §6.2):
// a valid envelope, a registered or ignorable type, a known payload version (or an
// ignorable newer one) and, for a type and version this build declares, bytes that
// decode. An unknown type or newer version is returned as event's typed error; any
// other refusal matches ErrInvalidBatch and wraps event's error.
func Admit(reg *event.Registry, rec Record) error {
	if err := reg.Validate(rec); err != nil {
		return &Error{Code: CodeInvalidBatch, Msg: err.Error(), Err: err}
	}
	if err := reg.Classify(rec); err != nil {
		var unknown *event.UnknownTypeError
		var newer *event.NewerPayloadError
		if errors.As(err, &unknown) || errors.As(err, &newer) {
			return err
		}
		return &Error{Code: CodeInvalidBatch, Msg: err.Error(), Err: err}
	}
	info, ok := reg.Lookup(rec.Type)
	if !ok || rec.Version > info.Version {
		return nil // ignorable, kept as its raw strings
	}
	if _, err := reg.DecodeAny(rec); err != nil {
		return &Error{Code: CodeInvalidBatch, Msg: err.Error(), Err: err}
	}
	return nil
}

// CheckOpen judges every record of a session against reg before an open, as the
// read rule of §6.2 requires: the first refusal is returned. It applies Admit, so
// rows another build wrote are held to the same rule as appended ones.
func CheckOpen(reg *event.Registry, recs []Record) error {
	for _, r := range recs {
		if err := Admit(reg, r); err != nil {
			return err
		}
	}
	return nil
}

// BlobReferrer is a decoded payload that cites stored objects or spill files
// (storage.md §6.2, the blob_refs table): each reference is "<kind>:<id>". It
// has the method set of storage's blob.Referrer, named here because this
// package imports no storage package.
type BlobReferrer interface{ BlobRefs() []string }

// BlobRefs are the references rec's payload cites, decoded with reg: none for a
// type or version this build does not declare, a payload that does not decode
// (admission and the open rule judge it, not this) or one that cites nothing.
// A reference that is not "<kind>:<id>" is refused with ErrInvalidBatch.
func BlobRefs(reg *event.Registry, rec Record) ([]string, error) {
	info, ok := reg.Lookup(rec.Type)
	if !ok || rec.Version > info.Version {
		return nil, nil
	}
	v, err := reg.DecodeAny(rec)
	if err != nil {
		return nil, nil
	}
	r, ok := v.(BlobReferrer)
	if !ok {
		return nil, nil
	}
	refs := r.BlobRefs()
	for _, ref := range refs {
		if kind, id, ok := strings.Cut(ref, ":"); !ok || kind == "" || id == "" {
			return nil, Errorf(CodeInvalidBatch, "type %q at seq %d cites %q, which is not <kind>:<id>", rec.Type, rec.Seq, ref)
		}
	}
	return refs, nil
}
