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
// (DH-D-VIEW-2): Fold hands the rows of the folds a batch changed to the
// session, which commits them in the batch's own transaction at the batch's
// last seq, so stored state is never ahead of the log. Each row records the
// seq it reflects and the fold's Version. When a session opens, Restore takes
// each stored fold up from its row and the log after it; a row of another
// Version, or none, is rebuilt from the log and rewritten at the log head. A
// read of a session the registry has not followed starts from the rows too.
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
	Init   func(h session.Header) S
	Apply  func(s S, ev event.Event) (next S, changed bool)
}

// Code is a closed registry error code.
type Code string

// The error codes.
const (
	CodeInvalid    Code = "view/invalid"
	CodeDuplicate  Code = "view/duplicate"
	CodeSealed     Code = "view/sealed"
	CodeUnknownKey Code = "view/unknown-key"
	CodeFailed     Code = "view/failed"
)

// Error is a registry error.
type Error struct {
	Code Code
	Msg  string
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
)

func errorf(c Code, format string, args ...any) error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...)}
}

// def is a Definition with its state type erased.
type def struct {
	key     string
	version int
	all     bool
	stored  bool
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

// Get is the fold's state in snap; an error when snap does not carry the key or
// the fold failed for that session.
func (h Handle[S]) Get(snap Snapshot) (S, error) {
	var zero S
	if err, failed := snap.failed[h.key]; failed {
		return zero, err
	}
	v, ok := snap.values[h.key]
	if !ok {
		return zero, errorf(CodeUnknownKey, "the snapshot does not carry %q", h.key)
	}
	s, _ := v.(S)
	return s, nil
}

// Snapshot is a read of several folds at one cut.
type Snapshot struct {
	// AsOf is the next seq at the cut: every value reflects events [0, AsOf).
	AsOf   event.Seq
	values map[string]any
	failed map[string]error
}

// Keys is the keys the snapshot carries, sorted.
func (s Snapshot) Keys() []string {
	return slices.Sorted(maps.Keys(s.values))
}

// folded is the fold of every key over [0, next).
type folded struct {
	next   event.Seq
	values map[string]any
	failed map[string]error
}

type tracked struct {
	committed *folded
	pending   *folded // a batch folded but not yet seen committed
}

// Registry is the fold registry; it implements session.Folder.
type Registry struct {
	mu       sync.Mutex
	sealed   bool
	defs     []*def
	byKey    map[string]*def
	byType   map[string][]*def
	sessions map[session.ID]*tracked
}

var _ session.Folder = (*Registry)(nil)

// New is an empty registry.
func New() *Registry {
	return &Registry{byKey: map[string]*def{}, byType: map[string][]*def{}, sessions: map[session.ID]*tracked{}}
}

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
	x := &def{
		key:     d.Key,
		version: d.Version,
		all:     d.Types == nil,
		stored:  d.Stored,
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
