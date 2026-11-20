package surface

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Node is one entry of derived model history: the surface event's position, a
// revision counter and the message as canonical JSON. A Node holds scalars and
// strings only, so a copy shares nothing that can change.
type Node struct {
	seq  event.Seq
	rev  int
	typ  string
	role vocab.Role
	call string // the call a tool/result node answers
	msg  string
}

// Seq is the position of the surface event that produced the node.
func (n Node) Seq() event.Seq { return n.seq }

// Revision counts the rewrites of the node's content; 0 until one happens.
func (n Node) Revision() int { return n.rev }

// Type is the surface event type.
func (n Node) Type() string { return n.typ }

// Role is the message's role.
func (n Node) Role() vocab.Role { return n.role }

// Raw is the message as canonical JSON.
func (n Node) Raw() string { return n.msg }

// Message decodes a fresh message.
func (n Node) Message() (vocab.Message, error) {
	var m vocab.Message
	if err := json.Unmarshal([]byte(n.msg), &m); err != nil {
		return vocab.Message{}, err
	}
	return m, nil
}

// History is an immutable snapshot of derived model history. Later appends and
// replacements never change a snapshot: an append writes past its length and a
// replacement builds a new array.
type History struct{ nodes []Node }

// Len is the number of nodes.
func (h History) Len() int { return len(h.nodes) }

// At is node i, by value.
func (h History) At(i int) Node { return h.nodes[i] }

// Seqs is a fresh list of the nodes' positions, in history order.
func (h History) Seqs() []event.Seq {
	out := make([]event.Seq, len(h.nodes))
	for i, n := range h.nodes {
		out[i] = n.seq
	}
	return out
}

// Messages decodes every node's message, in order.
func (h History) Messages() ([]vocab.Message, error) {
	out := make([]vocab.Message, 0, len(h.nodes))
	for _, n := range h.nodes {
		m, err := n.Message()
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// Raw is a fresh list of the nodes' canonical message JSON.
func (h History) Raw() []json.RawMessage {
	out := make([]json.RawMessage, len(h.nodes))
	for i, n := range h.nodes {
		out[i] = json.RawMessage(n.msg)
	}
	return out
}

// MarshalJSON is the history as an array of {seq, rev, type, message}.
func (h History) MarshalJSON() ([]byte, error) {
	type node struct {
		Seq     event.Seq       `json:"seq"`
		Rev     int             `json:"rev"`
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	out := make([]node, len(h.nodes))
	for i, n := range h.nodes {
		out[i] = node{n.seq, n.rev, n.typ, json.RawMessage(n.msg)}
	}
	return vocab.Encode(out)
}

// Class is where an event stands relative to model history.
type Class int

// The classes.
const (
	LogOnly  Class = iota // never on the surface
	Current               // a node of current history
	Shadowed              // a surface event that a replacement removed, or that produced no node
)

func (c Class) String() string {
	switch c {
	case LogOnly:
		return "log-only"
	case Current:
		return "current"
	case Shadowed:
		return "shadowed"
	}
	return "class(?)"
}

// isEmpty reports whether m produces no node: no blocks, or only empty text.
func isEmpty(m vocab.Message) bool {
	for _, b := range m.Blocks() {
		if b.Kind != vocab.KindText || b.Text == nil || strings.TrimSpace(b.Text.Text) != "" {
			return false
		}
	}
	return true
}

// historyFold is the surface: the ordered nodes and an index by seq.
type historyFold struct {
	nodes []Node
	index map[event.Seq]int
}

func newHistoryFold() historyFold { return historyFold{index: map[event.Seq]int{}} }

func (h historyFold) clone() historyFold {
	c := historyFold{nodes: slices.Clone(h.nodes), index: make(map[event.Seq]int, len(h.index))}
	for k, v := range h.index {
		c.index[k] = v
	}
	return c
}

func (h historyFold) snapshot() History { return History{nodes: h.nodes[:len(h.nodes):len(h.nodes)]} }

// surfacePlan is the history part of a plan: the node to add (nil for none) and,
// for a replacement, the positions it replaces.
type surfacePlan struct {
	op       event.OpKind
	node     *Node
	from, to int // replaced positions, inclusive
}

// planSurface judges a surface record whose message and call id were decoded.
func (h historyFold) planSurface(rec event.Record, role vocab.Role, call string, msg vocab.Message) (surfacePlan, error) {
	p := surfacePlan{op: rec.Op}
	if !(rec.Type == event.ToolResult.Name() || rec.Type == event.UserMessage.Name()) && isEmpty(msg) {
		p.node = nil
	} else {
		raw, err := vocab.Encode(msg)
		if err != nil {
			return surfacePlan{}, errorf(event.CodeInvalidEvent, "type %q at seq %d: encoding the message: %v", rec.Type, rec.Seq, err)
		}
		p.node = &Node{seq: rec.Seq, typ: rec.Type, role: role, call: call, msg: string(raw)}
	}
	if rec.Op != event.OpReplace {
		return p, nil
	}
	from, ok1 := h.index[rec.Start]
	to, ok2 := h.index[rec.End]
	switch {
	case !ok1 || !ok2:
		return surfacePlan{}, errorf(event.CodeInvalidSurface, "type %q at seq %d replaces %d..%d, which are not both current nodes", rec.Type, rec.Seq, rec.Start, rec.End)
	case from > to:
		return surfacePlan{}, errorf(event.CodeInvalidSurface, "type %q at seq %d replaces %d..%d, which is not an ordered range of current nodes", rec.Type, rec.Seq, rec.Start, rec.End)
	}
	cited := make(map[event.Seq]bool, len(rec.Sources))
	for _, s := range rec.Sources {
		cited[s] = true
	}
	for i := from; i <= to; i++ {
		if !cited[h.nodes[i].seq] {
			return surfacePlan{}, errorf(event.CodeInvalidSurface, "type %q at seq %d replaces node %d without citing it as a source", rec.Type, rec.Seq, h.nodes[i].seq)
		}
	}
	if rec.Type == event.ToolResult.Name() {
		old := h.nodes[from]
		if from != to || old.typ != event.ToolResult.Name() {
			return surfacePlan{}, errorf(event.CodeInvalidSurface, "tool/result at seq %d must replace exactly one current tool result", rec.Seq)
		}
		if old.call != call {
			return surfacePlan{}, errorf(event.CodeInvalidSurface, "tool/result at seq %d answers %q, but the result it replaces answers %q: only content may change", rec.Seq, call, old.call)
		}
	}
	if from == 0 && h.nodes[0].typ == event.SystemMessage.Name() {
		if rec.Type != event.SystemMessage.Name() || from != to {
			return surfacePlan{}, errorf(event.CodeInvalidSurface, "type %q at seq %d: the system prompt at node 0 may only be replaced by a system/message over exactly that node", rec.Type, rec.Seq)
		}
	}
	p.from, p.to = from, to
	return p, nil
}

func (h *historyFold) apply(p surfacePlan) {
	switch p.op {
	case event.OpAppend:
		if p.node != nil {
			h.index[p.node.seq] = len(h.nodes)
			h.nodes = append(h.nodes, *p.node)
		}
	case event.OpReplace:
		next := make([]Node, 0, len(h.nodes)-(p.to-p.from+1)+1)
		next = append(next, h.nodes[:p.from]...)
		if p.node != nil {
			next = append(next, *p.node)
		}
		next = append(next, h.nodes[p.to+1:]...)
		h.nodes = next
		h.index = make(map[event.Seq]int, len(next))
		for i, n := range next {
			h.index[n.seq] = i
		}
	}
}
