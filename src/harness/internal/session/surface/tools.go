package surface

import (
	"encoding/json"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// RequestHeader is the request/header snapshot in force: the non-history part of
// a request. Returned by value with fresh slices.
type RequestHeader struct {
	Seq      event.Seq
	Route    vocab.RouteID
	Controls vocab.Controls
	Tools    []vocab.ToolSchema
}

func (h RequestHeader) clone() RequestHeader {
	h.Controls = h.Controls.Clone()
	h.Tools = cloneTools(h.Tools)
	return h
}

func cloneTools(ts []vocab.ToolSchema) []vocab.ToolSchema {
	if ts == nil {
		return nil
	}
	out := make([]vocab.ToolSchema, len(ts))
	for i, t := range ts {
		t.Parameters = slices.Clone(t.Parameters)
		out[i] = t
	}
	return out
}

// ToolUpdateKind is the source kind of a developer/message that announces tool
// additions and removals within a request series. Its source data is a
// ToolUpdate; every added name must have exactly one schema in the request/header
// the update cites.
const ToolUpdateKind = "tool-update"

// ToolUpdate is the source data of a tool-update developer message.
type ToolUpdate struct {
	Header  event.Seq `json:"header"`
	Added   []string  `json:"added,omitempty"`
	Removed []string  `json:"removed,omitempty"`
}

// ToolChange is one tool update, resolved through the header it cites.
type ToolChange struct {
	Seq     event.Seq // the developer message
	Header  event.Seq // the request/header holding the added schemas
	Added   []vocab.ToolSchema
	Removed []string
}

// ToolHistory is the tool state of the current request series: the tools
// declared by the header that started it and the ordered changes since. It is
// capability-independent; projecting it for one provider is area A04's.
type ToolHistory struct {
	// Base is the request/header that started the series; HasBase is false
	// before the first header.
	Base    event.Seq
	HasBase bool
	// Declared are the base header's tools, which a request sends as its tools.
	Declared []vocab.ToolSchema
	Changes  []ToolChange
}

// Active is the tools in force after every change, in declaration then addition
// order.
func (t ToolHistory) Active() []vocab.ToolSchema {
	out := cloneTools(t.Declared)
	for _, c := range t.Changes {
		out = slices.DeleteFunc(out, func(s vocab.ToolSchema) bool { return slices.Contains(c.Removed, s.Name) })
		out = append(out, cloneTools(c.Added)...)
	}
	return out
}

type toolRef struct {
	name   string
	digest string
}

type change struct {
	seq, header event.Seq
	added       []toolRef
	removed     []string
}

// toolFold folds headers and tool updates (DH-T-SESSION-11). A request/header
// starts a new series when it is the first, when the surface was replaced since
// the previous header (a compaction or a prompt rewrite), or when it changes the
// definition of a name the series knows; otherwise it continues the series and is
// citable by a later tool update. A name removed and later offered again with the
// same definition continues the series.
type toolFold struct {
	hasBase  bool
	base     event.Seq
	declared []toolRef
	changes  []change
	active   []toolRef
	known    map[string]string       // name -> digest, every definition the series has seen
	headers  map[event.Seq][]toolRef // headers of the current series
	order    []event.Seq             // their seqs, in order
	replaced bool                    // a surface replacement since the last header
	schemas  map[string]vocab.ToolSchema
}

func newToolFold() toolFold {
	return toolFold{known: map[string]string{}, headers: map[event.Seq][]toolRef{}, schemas: map[string]vocab.ToolSchema{}}
}

func (f toolFold) clone() toolFold {
	c := f
	c.declared = slices.Clone(f.declared)
	c.active = slices.Clone(f.active)
	c.changes = make([]change, len(f.changes))
	for i, ch := range f.changes {
		c.changes[i] = change{ch.seq, ch.header, slices.Clone(ch.added), slices.Clone(ch.removed)}
	}
	c.known = make(map[string]string, len(f.known))
	for k, v := range f.known {
		c.known[k] = v
	}
	c.headers = make(map[event.Seq][]toolRef, len(f.headers))
	for k, v := range f.headers {
		c.headers[k] = slices.Clone(v)
	}
	c.order = slices.Clone(f.order)
	// schemas are interned by digest and never change: sharing the map's values is
	// safe, but the map itself is copied so that a discarded plan adds nothing.
	c.schemas = make(map[string]vocab.ToolSchema, len(f.schemas))
	for k, v := range f.schemas {
		c.schemas[k] = v
	}
	return c
}

func (f *toolFold) intern(ts []vocab.ToolSchema) ([]toolRef, error) {
	refs := make([]toolRef, 0, len(ts))
	for _, t := range ts {
		d, err := vocab.Digest(t)
		if err != nil {
			return nil, err
		}
		if _, ok := f.schemas[d]; !ok {
			t.Parameters = slices.Clone(t.Parameters)
			f.schemas[d] = t
		}
		refs = append(refs, toolRef{t.Name, d})
	}
	return refs, nil
}

func (f *toolFold) applyHeader(seq event.Seq, tools []vocab.ToolSchema) error {
	refs, err := f.intern(tools)
	if err != nil {
		return err
	}
	restart := !f.hasBase || f.replaced
	for _, r := range refs {
		if d, ok := f.known[r.name]; ok && d != r.digest {
			restart = true
		}
	}
	if restart {
		f.hasBase, f.base, f.declared = true, seq, refs
		f.changes, f.active = nil, slices.Clone(refs)
		f.known = map[string]string{}
		f.headers, f.order = map[event.Seq][]toolRef{}, nil
	}
	for _, r := range refs {
		f.known[r.name] = r.digest
	}
	f.headers[seq] = refs
	f.order = append(f.order, seq)
	f.replaced = false
	return nil
}

// toolUpdateOf extracts a tool update from a developer message; ok is false when
// the message is not one.
func toolUpdateOf(m vocab.Message) (ToolUpdate, bool, error) {
	src, ok := m.Source()
	if !ok || src.Kind != ToolUpdateKind {
		return ToolUpdate{}, false, nil
	}
	var u ToolUpdate
	if err := json.Unmarshal(src.Data, &u); err != nil {
		return ToolUpdate{}, true, err
	}
	return u, true, nil
}

// planUpdate resolves u against the series; it changes nothing.
func (f toolFold) planUpdate(seq event.Seq, u ToolUpdate) (change, error) {
	refs, ok := f.headers[u.Header]
	if !ok {
		return change{}, errorf(event.CodeInvalidSurface, "tool update at seq %d cites seq %d, which is not a request/header of the current series", seq, u.Header)
	}
	if len(u.Added) == 0 && len(u.Removed) == 0 {
		return change{}, errorf(event.CodeInvalidSurface, "tool update at seq %d adds and removes nothing", seq)
	}
	ch := change{seq: seq, header: u.Header, removed: slices.Clone(u.Removed)}
	isActive := func(name string) bool {
		return slices.ContainsFunc(f.active, func(r toolRef) bool { return r.name == name })
	}
	for _, name := range u.Removed {
		if !isActive(name) || slices.Contains(u.Added, name) {
			return change{}, errorf(event.CodeInvalidSurface, "tool update at seq %d removes %q, which is not an active tool", seq, name)
		}
	}
	for _, name := range u.Added {
		var found []toolRef
		for _, r := range refs {
			if r.name == name {
				found = append(found, r)
			}
		}
		if len(found) != 1 {
			return change{}, errorf(event.CodeInvalidSurface, "tool update at seq %d adds %q, which header %d does not define exactly once", seq, name, u.Header)
		}
		if isActive(name) {
			return change{}, errorf(event.CodeInvalidSurface, "tool update at seq %d adds %q, which is already active", seq, name)
		}
		ch.added = append(ch.added, found[0])
	}
	return ch, nil
}

func (f *toolFold) applyUpdate(ch change) {
	f.changes = append(f.changes, ch)
	f.active = slices.DeleteFunc(f.active, func(r toolRef) bool { return slices.Contains(ch.removed, r.name) })
	f.active = append(f.active, ch.added...)
}

func (f toolFold) resolve(refs []toolRef) []vocab.ToolSchema {
	if refs == nil {
		return nil
	}
	out := make([]vocab.ToolSchema, len(refs))
	for i, r := range refs {
		t := f.schemas[r.digest]
		t.Parameters = slices.Clone(t.Parameters)
		out[i] = t
	}
	return out
}

func (f toolFold) history() ToolHistory {
	t := ToolHistory{Base: f.base, HasBase: f.hasBase, Declared: f.resolve(f.declared)}
	for _, ch := range f.changes {
		t.Changes = append(t.Changes, ToolChange{Seq: ch.seq, Header: ch.header, Added: f.resolve(ch.added), Removed: slices.Clone(ch.removed)})
	}
	return t
}
