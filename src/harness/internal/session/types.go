package session

import (
	"context"
	"encoding/json"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// ID is a session id.
type ID string

// Offset is a log position.
type Offset = event.Offset

// Header is the session header: scalars and strings, immutable after creation.
type Header = store.Header

// ReadOption is an option of Read.
type ReadOption func(*readOptions)

type readOptions struct{ cold bool }

// WithCold adds the cold parts to the events Read returns.
func WithCold() ReadOption { return func(o *readOptions) { o.cold = true } }

// ReadSession reads one session at the position its folds reflect.
type ReadSession interface {
	ID() ID
	Header() Header
	// Len is the next seq.
	Len() Offset
	// Durable is the first seq not yet flushed.
	Durable() Offset
	// Read returns up to limit events from position from (0 is all of them).
	Read(ctx context.Context, from Offset, limit int, opts ...ReadOption) ([]event.Event, error)
	History() surface.History
	RequestHeader() (surface.RequestHeader, bool)
	ToolHistory() surface.ToolHistory
	Open() surface.Brackets
	Scope() scope.Key
}

// Session is a live session this process holds.
type Session interface {
	ReadSession
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
	// AppendGroup appends all drafts or none, as one storage transaction.
	AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error)
	AppendPluginRecord(ctx context.Context, typ string, data json.RawMessage) (event.Receipt, error)
	// CloseOpen appends the closers of the open turn as one group.
	CloseOpen(ctx context.Context, cause surface.Cause) ([]event.Receipt, error)
	// Flush is the durability barrier; it returns the new Durable.
	Flush(ctx context.Context) (Offset, error)
	NextRequest() (Request, error)
	RequestFrom(seq event.Seq) (Request, error)
	// AsOf runs fn at one consistent position; fn must not block or append.
	AsOf(fn func(at Offset))
}

// Handle is the creator's ownership of a live session.
type Handle interface {
	Session
	Close(ctx context.Context) error
}

// Derived is one row a Folder wants committed with a batch: a fold checkpoint.
type Derived struct {
	Key          string
	StateVersion int
	State        string
}

// Folder is A06's hook, called under the append lock, in seq order, exactly once
// per batch, with the batch's events (cold parts absent). It must be pure CPU; a
// panic is contained and its rows are dropped.
type Folder interface {
	Fold(s ReadSession, batch []event.Event) []Derived
}

// Restorer is a Folder that keeps stored fold state (DH-D-VIEW-2). Open calls
// Restore once with the reopened session, before anyone sees it, and the
// session's stored checkpoints: the Folder takes up the session from them (and
// the log after them) and returns the rows to rewrite, the folds whose stored
// state was missing, of another fold version, or behind the log. Open commits
// them at the log head (Len-1); a failure is logged, not fatal, since stored
// state is derived.
type Restorer interface {
	Folder
	Restore(ctx context.Context, s ReadSession, cps []store.Checkpoint) ([]Derived, error)
}

// Checkpointed is a ReadSession that can list its stored checkpoints: a live
// session and a read-only open are.
type Checkpointed interface {
	Checkpoints(ctx context.Context) ([]store.Checkpoint, error)
}

// CreateOptions are Create's options.
type CreateOptions struct {
	// Header is the new session's header; an empty ID is generated.
	Header Header
	// Scope is the agent scope the session's live events are dispatched with.
	Scope scope.Key
}

// OpenOptions are Open's options.
type OpenOptions struct {
	Scope    scope.Key
	Takeover store.Takeover
}

// OpenReport tells the caller of Open what was closed.
type OpenReport struct {
	// Interrupted is set when the previous holder left a turn, step or call open.
	Interrupted bool
	// Was is the brackets as found.
	Was surface.Brackets
	// Closers are the receipts of the closers and end seed the open appended.
	Closers []event.Receipt
	// FromCheckpoint is set when the folds were restored from a checkpoint.
	FromCheckpoint bool
}

// Request is the one type of a model request (ADR-7). It has no exported fields
// and no constructor: NextRequest and RequestFrom build it from the log, and A04
// reads it through accessors.
type Request struct{ b surface.Built }

// IsZero reports whether r was never built.
func (r Request) IsZero() bool { return r.b.Digest() == "" }

// Route is the route id.
func (r Request) Route() vocab.RouteID { return r.b.Route() }

// Controls is a copy of the per-call controls.
func (r Request) Controls() vocab.Controls { return r.b.Controls() }

// Messages decodes fresh messages.
func (r Request) Messages() ([]vocab.Message, error) { return r.b.Messages() }

// Tools is a fresh copy of the tool schemas.
func (r Request) Tools() []vocab.ToolSchema { return r.b.Tools() }

// ToolHistory is a copy of the tool history.
func (r Request) ToolHistory() surface.ToolHistory { return r.b.ToolHistory() }

// Meta is the session and purpose labels and, for an auxiliary request, the
// builder event.
func (r Request) Meta() surface.Meta { return r.b.Meta() }

// AsOf is requestAsOf, the last log position the request reflects.
func (r Request) AsOf() event.Seq { return r.b.AsOf() }

// Digest is requestDigest, "sha256:" and the hex SHA-256 of Canonical.
func (r Request) Digest() string { return r.b.Digest() }

// Canonical is the canonical encoding.
func (r Request) Canonical() string { return r.b.Canonical() }

// CreatedArgs is Created's argument.
type CreatedArgs struct{ Session Session }

// CommittedArgs is Committed's argument: one committed event, delivered once, in
// seq order.
type CommittedArgs struct {
	Session ReadSession
	Event   event.Event
}

// ClosedArgs is Closed's argument; Cause is nil for an ordinary close.
type ClosedArgs struct {
	ID    ID
	Cause error
}

// The live events, scoped by the session's agent scope (ADR-9). Created is
// ordered and its first error vetoes publication; Committed and Closed are
// notifications.
var (
	Created   = scope.NewEvent[CreatedArgs]("session.created")
	Committed = scope.NewEvent[CommittedArgs]("session.committed")
	Closed    = scope.NewEvent[ClosedArgs]("session.closed")
)
