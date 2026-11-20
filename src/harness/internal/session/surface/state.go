package surface

import (
	"errors"
	"fmt"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// State is the core folds of one session: the surface, the header in force, the
// tool history, the brackets, and the next position and least time. It holds no
// events. A State is not safe for concurrent use; the live session guards it with
// its append lock.
type State struct {
	reg      *event.Registry
	len      event.Seq
	lastTime int64
	hist     historyFold
	header   RequestHeader
	hasHead  bool
	tools    toolFold
	br       Brackets
}

// NewState is the folds of an empty log, judged against reg (nil is
// event.Default()).
func NewState(reg *event.Registry) *State {
	if reg == nil {
		reg = event.Default()
	}
	return &State{reg: reg, hist: newHistoryFold(), tools: newToolFold()}
}

// Clone is an independent copy; a group is planned and applied on a clone that
// replaces the original only when the whole group committed.
func (s *State) Clone() *State {
	c := *s
	c.hist = s.hist.clone()
	c.header = s.header.clone()
	c.tools = s.tools.clone()
	c.br = s.br.clone()
	return &c
}

// Registry is the registry the state judges against.
func (s *State) Registry() *event.Registry { return s.reg }

// Len is the next position.
func (s *State) Len() event.Seq { return s.len }

// LastTime is the time of the last event folded, the least time of the next.
func (s *State) LastTime() int64 { return s.lastTime }

// History is a snapshot of the derived model history.
func (s *State) History() History { return s.hist.snapshot() }

// RequestHeader is the request/header in force.
func (s *State) RequestHeader() (RequestHeader, bool) { return s.header.clone(), s.hasHead }

// ToolHistory is the tool state of the current request series.
func (s *State) ToolHistory() ToolHistory { return s.tools.history() }

// Open is the brackets.
func (s *State) Open() Brackets { return s.br.clone() }

// Classify is where rec stands relative to the current history: log-only for a
// log-only, plugin or unknown type, current for a node, shadowed otherwise.
func (s *State) Classify(rec event.Record) Class {
	info, ok := s.reg.Lookup(rec.Type)
	if !ok || !info.Surface || rec.Seq >= s.len {
		return LogOnly
	}
	if _, ok := s.hist.index[rec.Seq]; ok {
		return Current
	}
	return Shadowed
}

// Plan is the judged effect of one record on a State. A Plan applies only to the
// state it was planned against, unchanged.
type Plan struct {
	seq      event.Seq
	time     int64
	surface  *surfacePlan
	header   *RequestHeader
	tools    []vocab.ToolSchema
	update   *change
	brackets *Brackets
}

// Plan judges rec as the next record. byCore admits reserved types and the
// interrupted turn end (SP-3). It changes nothing; on error the record must not
// be committed.
func (s *State) Plan(rec event.Record, byCore bool) (Plan, error) {
	if rec.Seq != s.len {
		return Plan{}, errorf(event.CodeInvalidEvent, "type %q has seq %d, but the next position is %d", rec.Type, rec.Seq, s.len)
	}
	if rec.Time < s.lastTime {
		return Plan{}, errorf(event.CodeInvalidEvent, "type %q at seq %d has time %d, before the previous event's %d", rec.Type, rec.Seq, rec.Time, s.lastTime)
	}
	if err := s.reg.Validate(rec); err != nil {
		return Plan{}, err
	}
	if err := s.reg.Classify(rec); err != nil {
		return Plan{}, err
	}
	p := Plan{seq: rec.Seq, time: rec.Time}
	info, ok := s.reg.Lookup(rec.Type)
	if !ok || rec.Version > info.Version {
		return p, nil // a plugin record, or an ignorable record this build skips
	}
	if info.Reserved && !byCore {
		return Plan{}, errorf(event.CodeReservedType, "type %q at seq %d is reserved to the session core", rec.Type, rec.Seq)
	}
	var err error
	switch rec.Type {
	case event.TurnStartType.Name():
		err = planBracket(&p, rec, event.TurnStartType, func(v event.TurnStart) (Brackets, error) { return s.br.planTurnStart(rec, v) })
	case event.TurnEndType.Name():
		err = planBracket(&p, rec, event.TurnEndType, func(v event.TurnEnd) (Brackets, error) { return s.br.planTurnEnd(rec, v, byCore) })
	case event.StepStartType.Name():
		err = planBracket(&p, rec, event.StepStartType, func(v event.StepStart) (Brackets, error) { return s.br.planStepStart(rec, v) })
	case event.StepEndType.Name():
		err = planBracket(&p, rec, event.StepEndType, func(v event.StepEnd) (Brackets, error) { return s.br.planStepEnd(rec, v) })
	case event.ToolCall.Name():
		err = planBracket(&p, rec, event.ToolCall, func(v event.ToolCallPayload) (Brackets, error) { return s.br.planToolCall(rec, v) })
	case event.RequestHeader.Name():
		err = s.planHeader(&p, rec)
	default:
		if info.Surface {
			err = s.planSurfaceRecord(&p, rec)
		}
	}
	if err != nil {
		return Plan{}, err
	}
	return p, nil
}

type decoder[T any] interface{ Decode(event.Record) (T, error) }

func planBracket[T any](p *Plan, rec event.Record, t decoder[T], next func(T) (Brackets, error)) error {
	v, err := t.Decode(rec)
	if err != nil {
		return err
	}
	b, err := next(v)
	if err != nil {
		return err
	}
	p.brackets = &b
	return nil
}

func (s *State) planHeader(p *Plan, rec event.Record) error {
	v, err := event.RequestHeader.Decode(rec)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, t := range v.Tools {
		if err := t.Validate(); err != nil {
			return errorf(event.CodeInvalidEvent, "request/header at seq %d: %v", rec.Seq, err)
		}
		if seen[t.Name] {
			return errorf(event.CodeInvalidEvent, "request/header at seq %d declares tool %q twice", rec.Seq, t.Name)
		}
		seen[t.Name] = true
	}
	if v.Route != "" {
		if err := v.Route.Validate(); err != nil {
			return errorf(event.CodeInvalidEvent, "request/header at seq %d: %v", rec.Seq, err)
		}
	}
	h := RequestHeader{Seq: rec.Seq, Route: v.Route, Tools: cloneTools(v.Tools)}
	if v.Controls != nil {
		if err := v.Controls.Validate(); err != nil {
			return errorf(event.CodeInvalidEvent, "request/header at seq %d: %v", rec.Seq, err)
		}
		h.Controls = v.Controls.Clone()
	}
	p.header, p.tools = &h, cloneTools(v.Tools)
	return nil
}

// surfaceMessage decodes a core surface record's message and, for a tool result,
// the call it answers.
func surfaceMessage(rec event.Record) (vocab.Message, string, error) {
	switch rec.Type {
	case event.SystemMessage.Name():
		v, err := event.SystemMessage.Decode(rec)
		return v.Message, "", err
	case event.DeveloperMessage.Name():
		v, err := event.DeveloperMessage.Decode(rec)
		return v.Message, "", err
	case event.UserMessage.Name():
		v, err := event.UserMessage.Decode(rec)
		return v.Message, "", err
	case event.AssistantMessage.Name():
		v, err := event.AssistantMessage.Decode(rec)
		return v.Message, "", err
	case event.ToolResult.Name():
		v, err := event.ToolResult.Decode(rec)
		if err != nil {
			return vocab.Message{}, "", err
		}
		if err := answersCall(v.Message, v.CallID); err != nil {
			return vocab.Message{}, "", errorf(event.CodeInvalidSurface, "tool/result at seq %d: %v", rec.Seq, err)
		}
		return v.Message, v.CallID, nil
	}
	return vocab.Message{}, "", errorf(event.CodeInvalidSurface, "surface type %q at seq %d has no message this build can fold", rec.Type, rec.Seq)
}

// answersCall requires m to hold exactly one tool_result block, and that block
// to answer call: the payload's call id is the bracket's, the block's is the
// model's, and the two may not disagree.
func answersCall(m vocab.Message, call string) error {
	var ids []string
	for _, b := range m.Blocks() {
		if b.Kind == vocab.KindToolResult {
			if b.ToolResult == nil {
				return errors.New("a tool_result block has no body")
			}
			ids = append(ids, b.ToolResult.CallID)
		}
	}
	switch {
	case len(ids) != 1:
		return fmt.Errorf("the message holds %d tool_result blocks, want exactly 1", len(ids))
	case ids[0] != call:
		return fmt.Errorf("the payload answers %q but its tool_result block answers %q", call, ids[0])
	}
	return nil
}

func (s *State) planSurfaceRecord(p *Plan, rec event.Record) error {
	msg, call, err := surfaceMessage(rec)
	if err != nil {
		return err
	}
	// A replacement while calls are unanswered could drop the tool_use of an open
	// call, or the answer to an earlier one, and leave the history unpaired.
	if rec.Op == event.OpReplace && len(s.br.Calls) > 0 {
		return unbalanced(rec, "a replacement while %d tool calls are unanswered", len(s.br.Calls))
	}
	sp, err := s.hist.planSurface(rec, msg.Role(), call, msg)
	if err != nil {
		return err
	}
	p.surface = &sp
	switch rec.Type {
	case event.AssistantMessage.Name():
		if rec.Op == event.OpReplace {
			if slices.ContainsFunc(msg.Blocks(), func(b vocab.Block) bool { return b.Kind == vocab.KindToolUse }) {
				return errorf(event.CodeInvalidSurface, "assistant/message at seq %d is a replacement that makes tool calls", rec.Seq)
			}
			return nil
		}
		b, err := s.br.planCalls(rec, msg)
		if err != nil {
			return err
		}
		p.brackets = &b
	case event.ToolResult.Name():
		if rec.Op == event.OpReplace {
			return nil
		}
		b, err := s.br.planResult(rec, call)
		if err != nil {
			return err
		}
		p.brackets = &b
	case event.DeveloperMessage.Name():
		u, ok, err := toolUpdateOf(msg)
		if err != nil {
			return errorf(event.CodeInvalidSurface, "developer/message at seq %d: tool update data: %v", rec.Seq, err)
		}
		if !ok {
			return nil
		}
		if rec.Op != event.OpAppend {
			return errorf(event.CodeInvalidSurface, "developer/message at seq %d: a tool update is appended, not a replacement", rec.Seq)
		}
		ch, err := s.tools.planUpdate(rec.Seq, u)
		if err != nil {
			return err
		}
		p.update = &ch
	}
	return nil
}

// Apply advances the folds by a plan made against this state, after its record
// committed.
func (s *State) Apply(p Plan) {
	if p.seq != s.len {
		panic("surface: a plan applied out of order")
	}
	if p.surface != nil {
		s.hist.apply(*p.surface)
		if p.surface.op == event.OpReplace {
			s.tools.replaced = true
		}
	}
	if p.header != nil {
		s.header, s.hasHead = *p.header, true
		// interning only fails on an unencodable schema, which planHeader's
		// Validate already refused.
		_ = s.tools.applyHeader(p.seq, p.tools)
	}
	if p.update != nil {
		s.tools.applyUpdate(*p.update)
	}
	if p.brackets != nil {
		s.br = *p.brackets
	}
	s.len, s.lastTime = p.seq+1, p.time
}

// Fold plans and applies rec; on error s is unchanged.
func (s *State) Fold(rec event.Record, byCore bool) error {
	p, err := s.Plan(rec, byCore)
	if err != nil {
		return err
	}
	s.Apply(p)
	return nil
}

// Replay is the folds of recs, a log from position 0, read back from storage:
// every record is admitted as the core's.
func Replay(reg *event.Registry, recs []event.Record) (*State, error) {
	s := NewState(reg)
	for _, r := range recs {
		if err := s.Fold(r, true); err != nil {
			return nil, err
		}
	}
	return s, nil
}
