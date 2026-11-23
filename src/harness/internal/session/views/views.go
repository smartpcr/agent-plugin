// Package views is the fold registry (DH-D-VIEW-1): any component keeps
// log-derived state by registering a fold once, and a reader gets every value
// it asks for at one cut, a single sequence number for all folds of a read.
//
// The registry is the session's Folder hook: it folds each accepted batch under
// the session's append lock, keeps the result pending until the session's
// position shows the batch committed, and serves reads from the committed state
// at the session's AsOf position. A session it has not followed (opened before
// the registry, a read-only open) is folded from its log at the read's cut.
//
// A fold declared Stored keeps its state in the session's checkpoint rows
// (DH-D-VIEW-2): Fold hands the rows of the folds due by their cadence to the
// session, which commits them in the batch's own transaction at the batch's
// last seq, so stored state is never ahead of the log. Each row records the
// seq it reflects and the fold's Version. When a session opens, Restore takes
// each stored fold up from its row and the log after it; a row of another
// Version, or none, is rebuilt from the log and rewritten at the log head. A
// read of a session the registry has not followed starts from the rows too.
//
// A stored fold has a Cadence (DH-ADR-VIEW-2): an EveryCommit fold (small state
// a list shows) writes its row with every batch, so its row's seq follows the
// log head; a Periodic fold (large or fast-changing state) writes its row every
// Config.CheckpointEveryEvents events, at a turn end and at session close.
//
// A reader names a Freshness (Snapshot): Stored never folds and never blocks,
// returning each value as its row holds it, current, behind (with the seq it
// reaches) or pending (no usable row); Exact catches up at most
// Config.MaxCatchUpEvents events and otherwise fails with view/behind. Read is
// the in-lease Exact read the session's holder uses: it is never refused.
package views

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Definition is one fold. Apply must be pure and must not mutate s: it returns
// the next state, and changed false when ev left it as it was.
type Definition[S any] struct {
	Key string
	// Version is bumped when the state's shape or the fold's meaning changes.
	Version int
	// Types are the event types the fold consumes; nil is every type.
	Types []string
	// Stored keeps the state in checkpoint rows. S must round-trip through
	// encoding/json. A state that does not encode (an error or a panic, e.g.
	// a NaN float or a map with unsupported keys) is invalid state: that fold
	// is failed for the session, and reads of it return view/failed from then
	// on, while the other folds go on (session-views.md §7). encoding/json
	// silently drops unexported fields, so S must keep all of its state in
	// exported fields; otherwise a restored state is wrong with no error.
	Stored bool
	// Cadence is when a stored fold's row is written; the zero value is
	// EveryCommit. Periodic needs Stored.
	Cadence Cadence
	Init    func(h session.Header) S
	Apply   func(s S, ev event.Event) (next S, changed bool)
}

// Cadence is when a stored fold's checkpoint row is written.
type Cadence int

const (
	// EveryCommit writes the row with every batch: small state a list shows,
	// whose row then always reaches the log head.
	EveryCommit Cadence = iota
	// Periodic writes the row every Config.CheckpointEveryEvents events, with
	// a batch that holds a turn end, and at session close: large or
	// fast-changing state, whose reader catches up the suffix.
	Periodic
)

// Freshness is what a Snapshot reader asks for.
type Freshness int

const (
	// Stored never folds and never blocks: each value is its row as stored,
	// current, behind (with the seq it reaches) or pending.
	Stored Freshness = iota
	// Exact catches up at most Config.MaxCatchUpEvents events, else fails with
	// view/behind carrying both positions.
	Exact
)

// ValueState is how a snapshot value stands against the log head.
type ValueState int

const (
	// Current reflects the whole log up to the snapshot's head.
	Current ValueState = iota
	// Behind is a stored row older than the head; Value.AsOfSeq says how old.
	Behind
	// Pending has no usable value: no row, a row of another version or one that
	// does not decode. It is never a zero value.
	Pending
	// Failed is a fold that panicked or produced invalid state for the session.
	Failed
)

// String is the state's name: current, behind, pending or failed.
func (v ValueState) String() string {
	switch v {
	case Current:
		return "current"
	case Behind:
		return "behind"
	case Pending:
		return "pending"
	case Failed:
		return "failed"
	}
	return fmt.Sprintf("ValueState(%d)", int(v))
}

// Value is how one key of a snapshot stands: its state and AsOfSeq, the next
// seq at the cut it reflects (it reflects events [0, AsOfSeq)); AsOfSeq is
// zero for a pending or failed value.
type Value struct {
	State   ValueState
	AsOfSeq event.Seq
}

// The defaults of Config (session-views.md §6.2, projection.Config).
const (
	DefaultCheckpointEveryEvents = 200
	DefaultMaxCatchUpEvents      = 2_000
	DefaultRebuildConcurrency    = 2
)

// Config tunes a registry; a field of 0 (or less) takes its default.
type Config struct {
	// CheckpointEveryEvents is how many events a Periodic fold's row may fall
	// behind before it is written again.
	CheckpointEveryEvents int
	// MaxCatchUpEvents bounds the log suffix an Exact snapshot folds.
	MaxCatchUpEvents int
	// RebuildConcurrency bounds the sessions a background rebuild folds at once.
	RebuildConcurrency int
}

// DefaultConfig is the configuration with every default.
func DefaultConfig() Config {
	return Config{CheckpointEveryEvents: DefaultCheckpointEveryEvents, MaxCatchUpEvents: DefaultMaxCatchUpEvents,
		RebuildConcurrency: DefaultRebuildConcurrency}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.CheckpointEveryEvents <= 0 {
		c.CheckpointEveryEvents = d.CheckpointEveryEvents
	}
	if c.MaxCatchUpEvents <= 0 {
		c.MaxCatchUpEvents = d.MaxCatchUpEvents
	}
	if c.RebuildConcurrency <= 0 {
		c.RebuildConcurrency = d.RebuildConcurrency
	}
	return c
}

// Option configures New.
type Option func(*Config)

// WithConfig sets the whole configuration; zero fields keep their defaults.
func WithConfig(c Config) Option { return func(o *Config) { *o = c } }

// WithCheckpointEveryEvents sets Config.CheckpointEveryEvents.
func WithCheckpointEveryEvents(n int) Option { return func(o *Config) { o.CheckpointEveryEvents = n } }

// WithMaxCatchUpEvents sets Config.MaxCatchUpEvents.
func WithMaxCatchUpEvents(n int) Option { return func(o *Config) { o.MaxCatchUpEvents = n } }

// WithRebuildConcurrency sets Config.RebuildConcurrency.
func WithRebuildConcurrency(n int) Option { return func(o *Config) { o.RebuildConcurrency = n } }

// Code is a closed registry error code.
type Code string

// The error codes.
const (
	CodeInvalid    Code = "view/invalid"
	CodeDuplicate  Code = "view/duplicate"
	CodeSealed     Code = "view/sealed"
	CodeUnknownKey Code = "view/unknown-key"
	CodeFailed     Code = "view/failed"
	// CodeBehind is an Exact read whose catch-up would fold more than
	// Config.MaxCatchUpEvents events; the error carries both positions.
	CodeBehind Code = "view/behind"
	// CodePending is a Get of a key the snapshot holds no value for.
	CodePending Code = "view/pending"
)

// Error is a registry error.
type Error struct {
	Code Code
	Msg  string
	// AsOfSeq and HeadSeq are set on view/behind: the next seq the stored state
	// reaches (the catch-up would start there) and the log head.
	AsOfSeq, HeadSeq event.Seq
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

// Is matches a sentinel by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Msg == ""
}

// Sentinels for errors.Is.
var (
	ErrInvalid    = &Error{Code: CodeInvalid}
	ErrDuplicate  = &Error{Code: CodeDuplicate}
	ErrSealed     = &Error{Code: CodeSealed}
	ErrUnknownKey = &Error{Code: CodeUnknownKey}
	ErrFailed     = &Error{Code: CodeFailed}
	ErrBehind     = &Error{Code: CodeBehind}
	ErrPending    = &Error{Code: CodePending}
)

// behind is the view/behind of a catch-up from asOf to head over limit.
func behind(id session.ID, asOf, head event.Seq, limit int) error {
	return &Error{Code: CodeBehind, AsOfSeq: asOf, HeadSeq: head,
		Msg: fmt.Sprintf("session %q: stored state reaches %d, the log %d; catching up %d events is over the %d allowed", id, asOf, head, head-asOf, limit)}
}

func errorf(c Code, format string, args ...any) error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...)}
}

// def is a Definition with its state type erased.
type def struct {
	key     string
	version int
	all     bool
	stored  bool
	cadence Cadence
	init    func(session.Header) any
	apply   func(any, event.Event) (any, bool)
	encode  func(any) (string, error)
	decode  func(string) (any, error)
}

// reservedPrefix is the session core's checkpoint keys (store.Checkpoint).
const reservedPrefix = "core/"

// Handle is the typed read of one registered fold.
type Handle[S any] struct{ key string }

// Key is the fold's key.
func (h Handle[S]) Key() string { return h.key }

// Get is the fold's state in snap: a current value, or a behind one as its row
// holds it (snap.Value says which). It is an error when snap does not carry the
// key, holds no value for it (view/pending) or the fold failed for that session.
func (h Handle[S]) Get(snap Snapshot) (S, error) {
	var zero S
	if err, failed := snap.failed[h.key]; failed {
		return zero, err
	}
	v, ok := snap.values[h.key]
	if !ok {
		if st, held := snap.states[h.key]; held && st.State == Pending {
			return zero, errorf(CodePending, "the snapshot holds no value of %q: it has no usable stored row", h.key)
		}
		return zero, errorf(CodeUnknownKey, "the snapshot does not carry %q", h.key)
	}
	s, _ := v.(S)
	return s, nil
}

// Snapshot is a read of several folds at one cut.
type Snapshot struct {
	// AsOf is the next seq at the cut: every current value reflects events
	// [0, AsOf). At Stored freshness it is the lowest AsOfSeq of the values the
	// snapshot holds (HeadSeq when it holds none).
	AsOf event.Seq
	// HeadSeq is the log's next seq when the snapshot was read.
	HeadSeq event.Seq
	values  map[string]any
	failed  map[string]error
	states  map[string]Value
}

// Keys is the keys the snapshot carries (pending and failed ones too), sorted.
func (s Snapshot) Keys() []string {
	return slices.Sorted(maps.Keys(s.states))
}

// Value is how key stands in the snapshot; false when it does not carry key.
func (s Snapshot) Value(key string) (Value, bool) {
	v, ok := s.states[key]
	return v, ok
}

// folded is the fold of every key over [0, next).
type folded struct {
	next   event.Seq
	values map[string]any
	failed map[string]error
	// written is, per stored key, the next seq its newest row reflects: the row
	// this registry wrote or found usable. A key without an entry has no row
	// known to the registry.
	written map[string]event.Seq
}

type tracked struct {
	committed *folded
	pending   *folded // a batch folded but not yet seen committed
}

// Registry is the fold registry; it implements session.Folder.
type Registry struct {
	cfg      Config
	mu       sync.Mutex
	sealed   bool
	defs     []*def
	byKey    map[string]*def
	byType   map[string][]*def
	sessions map[session.ID]*tracked
}

var _ session.Folder = (*Registry)(nil)

// New is an empty registry with DefaultConfig changed by opts.
func New(opts ...Option) *Registry {
	cfg := DefaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Registry{cfg: cfg.withDefaults(), byKey: map[string]*def{}, byType: map[string][]*def{}, sessions: map[session.ID]*tracked{}}
}

// Config is the registry's configuration, defaults filled in.
func (r *Registry) Config() Config { return r.cfg }

// Register registers d once. A second key is view/duplicate; registering after
// the registry has folded or served a read is view/sealed, since sessions it
// already follows would carry the new fold from the wrong start.
func Register[S any](r *Registry, d Definition[S]) (Handle[S], error) {
	if d.Key == "" || d.Version < 1 || d.Init == nil || d.Apply == nil {
		return Handle[S]{}, errorf(CodeInvalid, "a fold needs a key, a version of at least 1, Init and Apply")
	}
	if strings.HasPrefix(d.Key, reservedPrefix) {
		return Handle[S]{}, errorf(CodeInvalid, "fold key %q is under the session core's %q", d.Key, reservedPrefix)
	}
	if d.Types != nil && len(d.Types) == 0 {
		return Handle[S]{}, errorf(CodeInvalid, "fold %q consumes no event type", d.Key)
	}
	switch {
	case d.Cadence != EveryCommit && d.Cadence != Periodic:
		return Handle[S]{}, errorf(CodeInvalid, "fold %q has cadence %d, neither EveryCommit nor Periodic", d.Key, d.Cadence)
	case d.Cadence == Periodic && !d.Stored:
		return Handle[S]{}, errorf(CodeInvalid, "fold %q is Periodic but not Stored: a cadence is when a row is written", d.Key)
	}
	x := &def{
		key:     d.Key,
		version: d.Version,
		all:     d.Types == nil,
		stored:  d.Stored,
		cadence: d.Cadence,
		encode: func(s any) (string, error) {
			//lint:ignore typeassert the def built for S receives only states its own init and apply produced, all of type S
			b, err := json.Marshal(s.(S))
			return string(b), err
		},
		decode: func(raw string) (any, error) {
			var s S
			err := json.Unmarshal([]byte(raw), &s)
			return s, err
		},
		init: func(h session.Header) any { return d.Init(h) },
		//lint:ignore typeassert the def built for S receives only states its own init and apply produced, all of type S
		apply: func(s any, ev event.Event) (any, bool) { return d.Apply(s.(S), ev) },
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return Handle[S]{}, errorf(CodeSealed, "fold %q registered after the registry started folding", d.Key)
	}
	if _, dup := r.byKey[d.Key]; dup {
		return Handle[S]{}, errorf(CodeDuplicate, "fold %q is already registered", d.Key)
	}
	r.defs = append(r.defs, x)
	r.byKey[d.Key] = x
	for _, t := range slices.Compact(slices.Sorted(slices.Values(d.Types))) {
		r.byType[t] = append(r.byType[t], x)
	}
	return Handle[S]{key: d.Key}, nil
}

// seal fixes the definitions; the caller holds r.mu.
func (r *Registry) sealLocked() { r.sealed = true }

// foldsFor is the definitions that consume typ, in registration order.
func (r *Registry) foldsFor(typ string) []*def {
	var out []*def
	for _, d := range r.defs {
		if d.all || slices.Contains(r.byType[typ], d) {
			out = append(out, d)
		}
	}
	return out
}
