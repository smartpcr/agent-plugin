package surface

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// PurposeStep is the purpose of a loop request built by NextRequest.
const PurposeStep = "step"

// Meta labels a built request: the session, the purpose and, for an auxiliary
// request, the one committed event that described it.
type Meta struct {
	Session string
	Purpose string
	// Event and EventType name the builder event of an auxiliary request;
	// EventType is empty for a loop request.
	Event     event.Seq
	EventType string
}

// Auxiliary reports whether the request was built from one builder event.
func (m Meta) Auxiliary() bool { return m.EventType != "" }

// Labels is the session and purpose labels as the vocabulary's Meta.
func (m Meta) Labels() vocab.Meta { return vocab.Meta{Session: m.Session, Purpose: m.Purpose} }

// Built is the content of one model request with its canonical encoding and
// digest. It has unexported fields only; package session wraps it in
// session.Request, which is the one type of a request (ADR-7).
type Built struct {
	route     vocab.RouteID
	controls  vocab.Controls
	messages  []string
	tools     []vocab.ToolSchema
	toolHist  ToolHistory
	meta      Meta
	asOf      event.Seq
	canonical string
	digest    string
}

// Route is the route id from the header in force.
func (b Built) Route() vocab.RouteID { return b.route }

// Controls is a copy of the per-call controls.
func (b Built) Controls() vocab.Controls { return b.controls.Clone() }

// Messages decodes fresh messages, in order.
func (b Built) Messages() ([]vocab.Message, error) {
	out := make([]vocab.Message, 0, len(b.messages))
	for _, raw := range b.messages {
		var m vocab.Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// Tools is a fresh copy of the tool schemas sent with the request.
func (b Built) Tools() []vocab.ToolSchema { return cloneTools(b.tools) }

// ToolHistory is a copy of the tool history the request reflects.
func (b Built) ToolHistory() ToolHistory {
	t := b.toolHist
	t.Declared = cloneTools(t.Declared)
	t.Changes = slices.Clone(t.Changes)
	for i, c := range t.Changes {
		t.Changes[i].Added, t.Changes[i].Removed = cloneTools(c.Added), slices.Clone(c.Removed)
	}
	return t
}

// Meta is the request's labels.
func (b Built) Meta() Meta { return b.meta }

// AsOf is the last log position the request reflects (requestAsOf).
func (b Built) AsOf() event.Seq { return b.asOf }

// Canonical is the canonical encoding the digest is taken over.
func (b Built) Canonical() string { return b.canonical }

// Digest is "sha256:" and the hex SHA-256 of Canonical (requestDigest).
func (b Built) Digest() string { return b.digest }

type canonicalTool struct {
	Seq     event.Seq          `json:"seq"`
	Header  event.Seq          `json:"header"`
	Added   []vocab.ToolSchema `json:"added,omitempty"`
	Removed []string           `json:"removed,omitempty"`
}

type canonicalRequest struct {
	Session   string             `json:"session"`
	Purpose   string             `json:"purpose"`
	Event     *event.Seq         `json:"event,omitempty"`
	EventType string             `json:"event_type,omitempty"`
	AsOf      event.Seq          `json:"as_of"`
	Route     vocab.RouteID      `json:"route,omitempty"`
	Controls  vocab.Controls     `json:"controls"`
	Messages  []json.RawMessage  `json:"messages"`
	Tools     []vocab.ToolSchema `json:"tools"`
	Base      *event.Seq         `json:"tool_base,omitempty"`
	Changes   []canonicalTool    `json:"tool_changes,omitempty"`
}

func (b *Built) seal() error {
	c := canonicalRequest{Session: b.meta.Session, Purpose: b.meta.Purpose, EventType: b.meta.EventType,
		AsOf: b.asOf, Route: b.route, Controls: b.controls, Messages: make([]json.RawMessage, len(b.messages)),
		Tools: b.tools}
	if c.Tools == nil {
		c.Tools = []vocab.ToolSchema{}
	}
	if b.meta.Auxiliary() {
		e := b.meta.Event
		c.Event = &e
	}
	for i, m := range b.messages {
		c.Messages[i] = json.RawMessage(m)
	}
	if b.toolHist.HasBase {
		base := b.toolHist.Base
		c.Base = &base
	}
	for _, ch := range b.toolHist.Changes {
		c.Changes = append(c.Changes, canonicalTool{ch.Seq, ch.Header, ch.Added, ch.Removed})
	}
	raw, err := vocab.Encode(c)
	if err != nil {
		return err
	}
	d, err := vocab.Digest(c)
	if err != nil {
		return err
	}
	b.canonical, b.digest = string(raw), d
	return nil
}

// Build is the loop request of s: the header in force, the derived history and
// the tool history, as of s.Len()-1. It is refused before any request/header and
// while a tool call is unanswered.
func Build(s *State, session string) (Built, error) {
	h, ok := s.RequestHeader()
	if !ok {
		return Built{}, errorf(CodeNoHeader, "no request/header has been appended")
	}
	if len(s.br.Calls) > 0 {
		return Built{}, errorf(CodeUnbalanced, "%d tool calls are unanswered", len(s.br.Calls))
	}
	b := Built{route: h.Route, controls: h.Controls, tools: h.Tools, toolHist: s.ToolHistory(),
		meta: Meta{Session: session, Purpose: PurposeStep}, asOf: s.len - 1}
	for _, n := range s.hist.nodes {
		b.messages = append(b.messages, n.msg)
	}
	if err := b.seal(); err != nil {
		return Built{}, err
	}
	return b, nil
}

// View is the folds a builder reads, as of its event.
type View interface {
	History() History
	RequestHeader() (RequestHeader, bool)
	ToolHistory() ToolHistory
	Open() Brackets
}

// AuxBuilder collects an auxiliary request. A builder starts from the header in
// force (route, controls, no tools) and an empty message list; it puts every
// message the request holds.
type AuxBuilder struct {
	b   Built
	err error
}

// Purpose sets the purpose label (required, e.g. "title" or "summary").
func (a *AuxBuilder) Purpose(p string) { a.b.meta.Purpose = p }

// Route sets the route.
func (a *AuxBuilder) Route(r vocab.RouteID) { a.b.route = r }

// Controls sets the controls.
func (a *AuxBuilder) Controls(c vocab.Controls) { a.b.controls = c.Clone() }

// Tools sets the tools sent.
func (a *AuxBuilder) Tools(ts ...vocab.ToolSchema) { a.b.tools = cloneTools(ts) }

// Message appends one message.
func (a *AuxBuilder) Message(m vocab.Message) {
	raw, err := vocab.Encode(m)
	if err != nil && a.err == nil {
		a.err = err
	}
	a.b.messages = append(a.b.messages, string(raw))
}

// History appends every node of h.
func (a *AuxBuilder) History(h History) {
	for _, n := range h.nodes {
		a.b.messages = append(a.b.messages, n.msg)
	}
}

// BuilderFunc is a pure builder of an auxiliary request from one committed
// event and the folds as of that event. It has no context and no service.
type BuilderFunc func(ev event.Event, at View, b *AuxBuilder) error

var builders = struct {
	sync.RWMutex
	m map[string]BuilderFunc
}{m: map[string]BuilderFunc{}}

// DeclareBuilder declares the builder of auxiliary requests described by events
// of type typ, which must be declared on reg. One builder per type.
func DeclareBuilder(reg *event.Registry, typ string, fn BuilderFunc) error {
	if reg == nil {
		reg = event.Default()
	}
	if _, ok := reg.Lookup(typ); !ok || fn == nil {
		return errorf(event.CodeInvalidDeclaration, "a request builder needs a declared type and a function; %q is not declared", typ)
	}
	builders.Lock()
	defer builders.Unlock()
	if _, dup := builders.m[typ]; dup {
		return errorf(event.CodeInvalidDeclaration, "type %q already has a request builder", typ)
	}
	builders.m[typ] = fn
	return nil
}

func builderOf(typ string) (BuilderFunc, bool) {
	builders.RLock()
	defer builders.RUnlock()
	fn, ok := builders.m[typ]
	return fn, ok
}

// HasBuilder reports whether typ declares a request builder.
func HasBuilder(typ string) bool {
	_, ok := builderOf(typ)
	return ok
}

// BuildFrom is the auxiliary request described by rec, the last record folded
// into s (s.Len()-1 == rec.Seq).
func BuildFrom(s *State, session string, rec event.Record) (b Built, err error) {
	if rec.Seq+1 != s.len {
		return Built{}, fmt.Errorf("surface: the builder event %d is not the last folded event %d", rec.Seq, s.len-1)
	}
	fn, ok := builderOf(rec.Type)
	if !ok {
		return Built{}, errorf(CodeNoBuilder, "type %q at seq %d declares no request builder", rec.Type, rec.Seq)
	}
	a := &AuxBuilder{b: Built{meta: Meta{Session: session, Event: rec.Seq, EventType: rec.Type}, asOf: rec.Seq}}
	if h, ok := s.RequestHeader(); ok {
		a.b.route, a.b.controls = h.Route, h.Controls
	}
	defer func() {
		if r := recover(); r != nil {
			b, err = Built{}, fmt.Errorf("surface: the request builder of %q panicked: %v", rec.Type, r)
		}
	}()
	if err := fn(event.Of(rec), s.Clone(), a); err != nil {
		return Built{}, err
	}
	if a.err != nil {
		return Built{}, a.err
	}
	if err := a.b.meta.Labels().Validate(); err != nil {
		return Built{}, err
	}
	if len(a.b.messages) == 0 {
		return Built{}, fmt.Errorf("surface: the request builder of %q put no message", rec.Type)
	}
	if err := a.b.seal(); err != nil {
		return Built{}, err
	}
	return a.b, nil
}

// ReadFunc reads up to limit records from position from, in seq order.
type ReadFunc func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error)

// rebuildPage is how many records Rebuild reads per call.
const rebuildPage = 1024

// ReplayPrefix folds the log prefix 0..through read by read.
func ReplayPrefix(ctx context.Context, reg *event.Registry, read ReadFunc, through event.Seq) (*State, event.Record, error) {
	s := NewState(reg)
	var last event.Record
	for s.len <= through {
		want := min(rebuildPage, int(through-s.len)+1)
		recs, err := read(ctx, s.len, want)
		if err != nil {
			return nil, event.Record{}, err
		}
		if len(recs) == 0 {
			return nil, event.Record{}, errorf(CodeBrokenStore, "the log ends at %d, before position %d", s.len, through)
		}
		for _, r := range recs {
			if s.len > through {
				break
			}
			if err := s.Fold(r, true); err != nil {
				return nil, event.Record{}, err
			}
			last = r
		}
	}
	return s, last, nil
}

// Rebuild recomputes the loop request whose requestAsOf is asOf from the log
// prefix ending there. Its canonical bytes and digest equal the original's.
func Rebuild(ctx context.Context, reg *event.Registry, read ReadFunc, session string, asOf event.Seq) (Built, error) {
	s, _, err := ReplayPrefix(ctx, reg, read, asOf)
	if err != nil {
		return Built{}, err
	}
	return Build(s, session)
}

// RebuildFrom recomputes the auxiliary request described by the event at seq.
func RebuildFrom(ctx context.Context, reg *event.Registry, read ReadFunc, session string, seq event.Seq) (Built, error) {
	s, rec, err := ReplayPrefix(ctx, reg, read, seq)
	if err != nil {
		return Built{}, err
	}
	return BuildFrom(s, session, rec)
}
