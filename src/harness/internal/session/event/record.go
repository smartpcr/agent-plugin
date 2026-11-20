// Package event is the session log's typed, versioned vocabulary (DH-D-SESSION-1,
// docs/harness/areas/core-session.md §6.1, §6.2): the stored envelope Record, the
// reader's view Event, the static Registry of declared types, the Type and
// SurfaceType handles whose Draft methods differ, the plugin-record grammar and the
// fourteen core payload structs.
//
// A type is declared once, in a package-level variable, with its name, current
// payload version, payload struct, converters from every older version, an
// event-local validator and whether it is ignorable. The registry is filled during
// program initialisation and sealed at boot; a later declaration panics
// (DH-ADR-SESSION-4). A record written by another build is either read faithfully
// (an older version converts) or refused by name: an undeclared required type with
// session/unknown-type and a newer payload version with session/newer-payload. An
// undeclared ignorable record is kept as its raw strings. Nothing is guessed.
package event

import (
	"fmt"
	"slices"
)

// Seq is an event's position in its session's log: dense from 0, assigned by the
// session.
type Seq uint64

// Offset is a count of events, or the position of the next one (Len).
type Offset = Seq

// OpKind is a surface operation. The values are those of the store's surface
// column: 0 log-only, 1 append, 2 replace.
type OpKind uint8

// The surface operations.
const (
	OpNone    OpKind = 0
	OpAppend  OpKind = 1
	OpReplace OpKind = 2
)

func (k OpKind) String() string {
	switch k {
	case OpNone:
		return "none"
	case OpAppend:
		return "append"
	case OpReplace:
		return "replace"
	}
	return fmt.Sprintf("op(%d)", uint8(k))
}

// Op is the operation a surface event performs on model history: append a node,
// or replace the nodes [Start, End] (inclusive, by Seq) with this event's content.
type Op struct {
	Kind       OpKind
	Start, End Seq
}

// Append is the append operation.
func Append() Op { return Op{Kind: OpAppend} }

// Replace is the operation replacing the history nodes from start to end.
func Replace(start, end Seq) Op { return Op{Kind: OpReplace, Start: start, End: end} }

// Record is the stored form of one event: the envelope of core-session.md §6.2. It
// carries no format version; each type has its own payload Version. Payload and
// Cold hold the stored JSON bytes as immutable strings, so storage never re-encodes
// them and neither side can change them after hand-over (DH-ADR-SESSION-3).
// Sources is the one slice: a provider or a reader that keeps a Record must not
// write to it, and Clone gives an independent copy.
type Record struct {
	Seq       Seq    `json:"seq"`
	Time      int64  `json:"time"` // Unix milliseconds; never less than the previous event's
	Type      string `json:"type"`
	Version   int    `json:"v"`
	Ignorable bool   `json:"ignorable,omitempty"`
	Op        OpKind `json:"op,omitempty"`
	Start     Seq    `json:"start,omitempty"`
	End       Seq    `json:"end,omitempty"`
	Sources   []Seq  `json:"sources,omitempty"`
	Payload   string `json:"payload"`
	Cold      string `json:"cold,omitempty"`
}

// Clone is a copy of r that shares nothing mutable with it.
func (r Record) Clone() Record {
	r.Sources = slices.Clone(r.Sources)
	return r
}

// Equal reports whether two records are the same bytes in every field.
func (r Record) Equal(o Record) bool {
	return r.Seq == o.Seq && r.Time == o.Time && r.Type == o.Type && r.Version == o.Version &&
		r.Ignorable == o.Ignorable && r.Op == o.Op && r.Start == o.Start && r.End == o.End &&
		slices.Equal(r.Sources, o.Sources) && r.Payload == o.Payload && r.Cold == o.Cold
}

// Event is the reader's view of a committed record. Its fields are reachable only
// through accessors; Raw and ColdRaw return the committed strings, and Sources a
// fresh slice, so nothing a reader does to a returned value changes the log.
type Event struct{ r Record }

// Of is the reader's view of r; it keeps its own copy.
func Of(r Record) Event { return Event{r.Clone()} }

// Seq is the event's position.
func (e Event) Seq() Seq { return e.r.Seq }

// Time is the event's commit time in Unix milliseconds.
func (e Event) Time() int64 { return e.r.Time }

// Type is the event's type name.
func (e Event) Type() string { return e.r.Type }

// Version is the payload version the writer used.
func (e Event) Version() int { return e.r.Version }

// Ignorable reports whether a reader that does not know the type may skip it.
func (e Event) Ignorable() bool { return e.r.Ignorable }

// Op is the surface operation, Kind OpNone for a log-only event.
func (e Event) Op() Op { return Op{Kind: e.r.Op, Start: e.r.Start, End: e.r.End} }

// Sources is a copy of the cited earlier events, nil when there are none.
func (e Event) Sources() []Seq { return slices.Clone(e.r.Sources) }

// Raw is the committed hot part, as JSON.
func (e Event) Raw() string { return e.r.Payload }

// ColdRaw is the committed cold part, empty when absent or not loaded.
func (e Event) ColdRaw() string { return e.r.Cold }

// Record is a copy of the stored form.
func (e Event) Record() Record { return e.r.Clone() }

// Receipt is what an append returns: the position and time the session assigned.
type Receipt struct {
	Seq  Seq
	Time int64
}
