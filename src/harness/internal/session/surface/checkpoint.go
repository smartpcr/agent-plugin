package surface

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// CheckpointKey and CheckpointVersion name the core folds' checkpoint in the
// store's checkpoint table (DH-T-SESSION-14). A checkpoint holds structure, not
// message text or schemas: Restore reads the cited events back. Version 2 adds
// each unanswered call's Held approval (plan 16.1.4); a version-1 checkpoint is
// refused, so the session folds its log from the start and finds the holds.
const (
	CheckpointKey     = "core/surface"
	CheckpointVersion = 2
)

type cpNode struct {
	Seq event.Seq `json:"seq"`
	Rev int       `json:"rev,omitempty"`
}

type cpChange struct {
	Seq     event.Seq `json:"seq"`
	Header  event.Seq `json:"header"`
	Added   []string  `json:"added,omitempty"`
	Removed []string  `json:"removed,omitempty"`
}

type cpState struct {
	Len      event.Seq   `json:"len"`
	LastTime int64       `json:"last_time"`
	Nodes    []cpNode    `json:"nodes"`
	Header   *event.Seq  `json:"header,omitempty"`
	Series   []event.Seq `json:"series,omitempty"` // headers of the current tool series; the first is its base
	Changes  []cpChange  `json:"changes,omitempty"`
	Replaced bool        `json:"replaced,omitempty"`
	Brackets Brackets    `json:"brackets"`
}

// Checkpoint is the folds' structure as of Len()-1, as canonical JSON.
func (s *State) Checkpoint() (string, error) {
	cp := cpState{Len: s.len, LastTime: s.lastTime, Nodes: make([]cpNode, len(s.hist.nodes)),
		Series: slices.Clone(s.tools.order), Replaced: s.tools.replaced, Brackets: s.br.clone()}
	for i, n := range s.hist.nodes {
		cp.Nodes[i] = cpNode{n.seq, n.rev}
	}
	if s.hasHead {
		h := s.header.Seq
		cp.Header = &h
	}
	for _, ch := range s.tools.changes {
		c := cpChange{Seq: ch.seq, Header: ch.header, Removed: slices.Clone(ch.removed)}
		for _, r := range ch.added {
			c.Added = append(c.Added, r.name)
		}
		cp.Changes = append(cp.Changes, c)
	}
	b, err := json.Marshal(cp)
	return string(b), err
}

// ReadSeqsFunc reads the records at seqs, in the order given.
type ReadSeqsFunc func(ctx context.Context, seqs []event.Seq) ([]event.Record, error)

// Restore rebuilds the folds from a checkpoint of the given state version,
// reading the events it cites. Another version returns ErrCheckpoint: the caller
// discards the checkpoint and replays from position 0. Records after the
// checkpoint are then folded by Fold.
func Restore(ctx context.Context, reg *event.Registry, version int, state string, read ReadSeqsFunc) (*State, error) {
	if version != CheckpointVersion {
		return nil, errorf(CodeCheckpoint, "checkpoint state version %d is not %d", version, CheckpointVersion)
	}
	var cp cpState
	if err := json.Unmarshal([]byte(state), &cp); err != nil {
		return nil, errorf(CodeCheckpoint, "checkpoint is not readable: %v", err)
	}
	s := NewState(reg)
	s.len, s.lastTime, s.br = cp.Len, cp.LastTime, cp.Brackets.clone()

	seqs := make([]event.Seq, 0, len(cp.Nodes)+len(cp.Series)+1)
	for _, n := range cp.Nodes {
		seqs = append(seqs, n.Seq)
	}
	seqs = append(seqs, cp.Series...)
	if cp.Header != nil {
		seqs = append(seqs, *cp.Header)
	}
	recs, err := read(ctx, seqs)
	if err != nil {
		return nil, err
	}
	if len(recs) != len(seqs) {
		return nil, errorf(CodeBrokenStore, "checkpoint restore read %d of %d records", len(recs), len(seqs))
	}
	for i, r := range recs {
		if r.Seq != seqs[i] || r.Seq >= cp.Len {
			return nil, errorf(CodeBrokenStore, "checkpoint restore asked for seq %d and read seq %d", seqs[i], r.Seq)
		}
	}
	for i, n := range cp.Nodes {
		rec := recs[i]
		msg, call, err := surfaceMessage(rec)
		if err != nil {
			return nil, err
		}
		sp, err := s.hist.planSurface(event.Record{Seq: rec.Seq, Type: rec.Type, Op: event.OpAppend}, msg.Role(), call, msg)
		if err != nil {
			return nil, err
		}
		if sp.node == nil {
			return nil, errorf(CodeCheckpoint, "checkpoint names seq %d, which produces no node", rec.Seq)
		}
		sp.node.rev = n.Rev
		s.hist.apply(sp)
	}
	series := recs[len(cp.Nodes) : len(cp.Nodes)+len(cp.Series)]
	for _, r := range series {
		v, err := event.RequestHeader.Decode(r)
		if err != nil {
			return nil, err
		}
		if err := s.tools.applyHeader(r.Seq, v.Tools); err != nil {
			return nil, err
		}
	}
	for _, c := range cp.Changes {
		ch, err := s.tools.planUpdate(c.Seq, ToolUpdate{Header: c.Header, Added: c.Added, Removed: c.Removed})
		if err != nil {
			return nil, errorf(CodeCheckpoint, "checkpoint tool change at seq %d: %v", c.Seq, err)
		}
		s.tools.applyUpdate(ch)
	}
	s.tools.replaced = cp.Replaced
	if cp.Header != nil {
		r := recs[len(recs)-1]
		var p Plan
		if err := s.planHeader(&p, r); err != nil {
			return nil, err
		}
		s.header, s.hasHead = *p.header, true
	}
	return s, nil
}
