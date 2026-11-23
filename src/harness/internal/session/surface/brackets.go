package surface

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Call is one tool call the model made that no tool/result has answered yet.
type Call struct {
	ID   string
	Name string
	// Seq is the assistant/message that made the call.
	Seq event.Seq
	// Started is set by the tool/call that began running it.
	Started bool
	// Held is the id of the approval that holds the call's body back: set by an
	// approval/asked for the call, cleared by an approval/decided that allows it.
	// A held call's body never started, so its closer says it did not run.
	Held string `json:"held,omitempty"`
}

// Brackets is the open turn, the open step and the unanswered calls, as values
// (DH-D-SESSION-5). Calls is a fresh slice.
type Brackets struct {
	TurnOpen bool
	Turn     int
	Agent    string
	StepOpen bool
	Step     int
	Calls    []Call
	// LastTurn is the highest turn number started, and LastStep the highest step
	// number started in the open (or last) turn.
	LastTurn int
	LastStep int
}

// IsOpen reports whether anything needs a closer.
func (b Brackets) IsOpen() bool { return b.TurnOpen || b.StepOpen || len(b.Calls) > 0 }

func (b Brackets) clone() Brackets {
	b.Calls = slices.Clone(b.Calls)
	return b
}

func (b Brackets) call(id string) int {
	return slices.IndexFunc(b.Calls, func(c Call) bool { return c.ID == id })
}

func unbalanced(rec event.Record, format string, args ...any) error {
	return errorf(CodeUnbalanced, "type %q at seq %d: %s", rec.Type, rec.Seq, fmt.Sprintf(format, args...))
}

// planBrackets is the next bracket value for a bracket event, or an error; it
// does not change b.
func (b Brackets) planTurnStart(rec event.Record, p event.TurnStart) (Brackets, error) {
	switch {
	case b.TurnOpen:
		return b, unbalanced(rec, "turn %d is still open", b.Turn)
	case len(b.Calls) > 0:
		return b, unbalanced(rec, "%d tool calls are unanswered", len(b.Calls))
	case p.Turn <= b.LastTurn:
		return b, unbalanced(rec, "turn %d does not follow turn %d", p.Turn, b.LastTurn)
	}
	n := b.clone()
	n.TurnOpen, n.Turn, n.Agent, n.LastTurn, n.LastStep = true, p.Turn, p.Agent, p.Turn, 0
	return n, nil
}

func (b Brackets) planTurnEnd(rec event.Record, p event.TurnEnd, byCore bool) (Brackets, error) {
	switch {
	case p.Reason.Kind == event.ReasonInterrupted && !byCore:
		return b, unbalanced(rec, "only the session core writes an interrupted turn end (SP-3)")
	case !b.TurnOpen:
		return b, unbalanced(rec, "no turn is open")
	case p.Turn != b.Turn:
		return b, unbalanced(rec, "turn %d is open, not turn %d", b.Turn, p.Turn)
	case b.StepOpen:
		return b, unbalanced(rec, "step %d is still open", b.Step)
	case len(b.Calls) > 0:
		return b, unbalanced(rec, "%d tool calls are unanswered", len(b.Calls))
	}
	n := b.clone()
	n.TurnOpen, n.Agent = false, ""
	return n, nil
}

func (b Brackets) planStepStart(rec event.Record, p event.StepStart) (Brackets, error) {
	switch {
	case !b.TurnOpen:
		return b, unbalanced(rec, "no turn is open")
	case p.Turn != b.Turn:
		return b, unbalanced(rec, "turn %d is open, not turn %d", b.Turn, p.Turn)
	case b.StepOpen:
		return b, unbalanced(rec, "step %d is still open", b.Step)
	case len(b.Calls) > 0:
		return b, unbalanced(rec, "%d tool calls are unanswered", len(b.Calls))
	case p.Step <= b.LastStep:
		return b, unbalanced(rec, "step %d does not follow step %d", p.Step, b.LastStep)
	}
	n := b.clone()
	n.StepOpen, n.Step, n.LastStep = true, p.Step, p.Step
	return n, nil
}

func (b Brackets) planStepEnd(rec event.Record, p event.StepEnd) (Brackets, error) {
	switch {
	case !b.StepOpen:
		return b, unbalanced(rec, "no step is open")
	case p.Turn != b.Turn || p.Step != b.Step:
		return b, unbalanced(rec, "step %d of turn %d is open, not step %d of turn %d", b.Step, b.Turn, p.Step, p.Turn)
	case len(b.Calls) > 0:
		return b, unbalanced(rec, "%d tool calls are unanswered", len(b.Calls))
	}
	n := b.clone()
	n.StepOpen = false
	return n, nil
}

// planCalls records the calls an appended assistant message makes.
func (b Brackets) planCalls(rec event.Record, m vocab.Message) (Brackets, error) {
	var uses []Call
	for _, blk := range m.Blocks() {
		if blk.Kind == vocab.KindToolUse && blk.ToolUse != nil {
			uses = append(uses, Call{ID: blk.ToolUse.ID, Name: blk.ToolUse.Name, Seq: rec.Seq})
		}
	}
	if len(uses) == 0 {
		return b, nil
	}
	n := b.clone()
	for _, c := range uses {
		if c.ID == "" || n.call(c.ID) >= 0 {
			return b, unbalanced(rec, "tool call id %q is empty or already unanswered", c.ID)
		}
		n.Calls = append(n.Calls, c)
	}
	return n, nil
}

func (b Brackets) planToolCall(rec event.Record, p event.ToolCallPayload) (Brackets, error) {
	i := b.call(p.CallID)
	switch {
	case i < 0:
		return b, unbalanced(rec, "call %q is not an unanswered call", p.CallID)
	case b.Calls[i].Started:
		return b, unbalanced(rec, "call %q was already started", p.CallID)
	case b.Calls[i].Name != p.Name:
		return b, unbalanced(rec, "call %q is to %q, not %q", p.CallID, b.Calls[i].Name, p.Name)
	}
	n := b.clone()
	n.Calls[i].Started = true
	return n, nil
}

func (b Brackets) planResult(rec event.Record, callID string) (Brackets, error) {
	i := b.call(callID)
	if i < 0 {
		return b, unbalanced(rec, "call %q is not an unanswered call", callID)
	}
	n := b.clone()
	n.Calls = slices.Delete(n.Calls, i, i+1)
	return n, nil
}

// The approval audit types (human/approval owns them; surface cannot import it,
// so it matches their names, and a test there checks they are equal). Only the
// two fields every version of them carries are read: the request id and the call.
const (
	ApprovalAskedType   = "approval/asked"
	ApprovalDecidedType = "approval/decided"
)

// approvalRef is the part of an approval/asked or approval/decided the brackets
// read: which request, for which call, and how it settled.
type approvalRef struct {
	ID      string `json:"id"`
	Call    string `json:"call"`
	Outcome string `json:"outcome"`
}

// allowedOutcomes are the approval/decided outcomes that let the body start,
// across versions: version 2's two allowed outcomes and version 1's approved.
var allowedOutcomes = []string{"allowed-once", "allowed-by-grant", "approved"}

// planApproval holds or releases an unanswered, started call on an approval
// record for it: approval/asked holds it, an approval/decided that allows it
// releases it, one that refuses keeps it held (its body never runs). A record
// that does not name an unanswered call, or whose payload does not decode,
// changes nothing: the approval types are not the core's to judge, so they never
// fail a fold.
func (b Brackets) planApproval(rec event.Record) (Brackets, bool) {
	var a approvalRef
	if json.Unmarshal([]byte(rec.Payload), &a) != nil || a.ID == "" || a.Call == "" {
		return b, false
	}
	i := b.call(a.Call)
	if i < 0 || !b.Calls[i].Started {
		return b, false
	}
	n := b.clone()
	switch rec.Type {
	case ApprovalAskedType:
		n.Calls[i].Held = a.ID
	case ApprovalDecidedType:
		if n.Calls[i].Held != a.ID || !slices.Contains(allowedOutcomes, a.Outcome) {
			return b, false
		}
		n.Calls[i].Held = ""
	}
	return n, true
}

// Cause is why the core closes an open turn.
type Cause string

// The causes.
const (
	CauseInterrupted Cause = "interrupted" // the previous holder stopped mid-turn
	CauseForked      Cause = "forked"      // a fork boundary fell inside a turn
	CauseFailed      Cause = "failed"      // the turn's driver failed
)

// Closers are the events that close b, in order: an error tool/result for each
// unanswered call (tools/not-started if no tool/call began it; approval/
// unavailable if its approval was asked and not allowed, so its body never ran;
// otherwise tools/outcome-unknown), then step/end for an open step, then
// turn/end with reason interrupted and cause for an open turn. at is the seq the first closer
// will take; message ids derive from it, so the closers of one position are the
// same bytes on every run. A caller appends them as one group, by the core.
func Closers(b Brackets, cause Cause, at event.Seq) ([]event.Draft, error) {
	if cause == "" {
		return nil, fmt.Errorf("surface: a closer needs a cause")
	}
	var out []event.Draft
	for i, c := range b.Calls {
		code, text := CodeToolNotStarted, fmt.Sprintf("the tool call %q was not started before the turn was closed (%s)", c.Name, cause)
		switch {
		case c.Started && c.Held != "":
			code, text = CodeApprovalUnavailable, fmt.Sprintf("the tool call %q did not run: its approval was asked and not given before the turn was closed (%s)", c.Name, cause)
		case c.Started:
			code, text = CodeToolOutcomeUnknown, fmt.Sprintf("the outcome of the tool call %q is unknown: the turn was closed while it ran (%s)", c.Name, cause)
		}
		msg, err := vocab.RestoreMessage(fmt.Sprintf("msg_closer_%d_%d", at, i), vocab.RoleTool,
			&vocab.Source{Kind: "closer", Producers: []string{"session"}, Data: []byte(fmt.Sprintf(`{"code":%q,"cause":%q}`, code, cause))},
			vocab.ToolResultBlock(c.ID, true, vocab.TextPart(code+": "+text)))
		if err != nil {
			return nil, err
		}
		d, err := event.ToolResult.Draft(event.Append(), event.ToolResultPayload{Message: msg, CallID: c.ID, IsError: true})
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if b.StepOpen {
		d, err := event.StepEndType.Draft(event.StepEnd{Turn: b.Turn, Step: b.Step})
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if b.TurnOpen {
		d, err := event.TurnEndType.Draft(event.TurnEnd{Turn: b.Turn, Reason: event.Reason{Kind: event.ReasonInterrupted, Cause: string(cause)}})
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
