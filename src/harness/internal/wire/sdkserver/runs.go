package sdkserver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// FinishDiscarded is the finishReason of a run whose message left the inbox
// discarded, before any turn claimed it (DH-ADR-SDK-5).
const FinishDiscarded = "discarded"

// runTracker is the run half of a session's forwarder (DH-D-SDK-5, DH-ADR-SDK-5):
// it folds the committed log as the forwarder passes it and reports a run when
// it ends. A run is the turn that claimed the input: the claim splice names the
// turn that took message m, and that turn's turn/end completes m with the turn's
// reason, the text of the last assistant message inside the bracket and the
// bracket's first and last seq. A steer claimed at a step boundary belongs to
// the open turn; a message discarded before a claim completes as discarded.
//
// Commands complete from A17's command registry, which is not mounted yet
// (initialize advertises none), and a goal's stop signal (H-4) is not built:
// every run reported here is a prompt.
type runTracker struct {
	// pending mirrors the inbox: the ids waiting on each list, in order.
	pending map[agent.List][]string
	// turns are the open brackets by turn number.
	turns map[int]*bracket
}

// bracket is one open turn: where it started, the messages it claimed, in claim
// order, and the text of its last assistant message so far.
type bracket struct {
	first   event.Seq
	claimed []string
	last    string
}

func newRunTracker() *runTracker {
	return &runTracker{pending: map[agent.List][]string{}, turns: map[int]*bracket{}}
}

// observe folds e and returns the runs it completes, in order, without scope.
func (t *runTracker) observe(e event.Event) ([]protocol.RunCompletedNotification, error) {
	switch e.Type() {
	case agent.SplicedType.Name():
		s, err := agent.SplicedType.As(e)
		if err != nil {
			return nil, fmt.Errorf("sdkserver: read the splice at seq %d: %w", e.Seq(), err)
		}
		return t.splice(s, e.Seq()), nil
	case event.TurnStartType.Name():
		p, err := event.TurnStartType.As(e)
		if err != nil {
			return nil, fmt.Errorf("sdkserver: read the turn/start at seq %d: %w", e.Seq(), err)
		}
		t.open(p.Turn, e.Seq())
	case event.AssistantMessage.Name():
		p, err := event.AssistantMessage.As(e)
		if err != nil {
			return nil, fmt.Errorf("sdkserver: read the assistant/message at seq %d: %w", e.Seq(), err)
		}
		// The open turn is the one this message belongs to; turns do not nest.
		for _, b := range t.turns {
			b.last = textOfMessage(p.Message)
		}
	case event.TurnEndType.Name():
		p, err := event.TurnEndType.As(e)
		if err != nil {
			return nil, fmt.Errorf("sdkserver: read the turn/end at seq %d: %w", e.Seq(), err)
		}
		return t.end(p, e.Seq()), nil
	}
	return nil, nil
}

func (t *runTracker) open(turn int, at event.Seq) *bracket {
	b, ok := t.turns[turn]
	if !ok {
		b = &bracket{first: at}
		t.turns[turn] = b
	}
	return b
}

// splice applies s to the mirror; a claim files the removed ids under their
// turn, a discard completes each of them at once.
func (t *runTracker) splice(s agent.Splice, at event.Seq) []protocol.RunCompletedNotification {
	l := t.pending[s.List]
	pos := min(s.Position, len(l))
	end := min(pos+s.Removed, len(l))
	removed := slices.Clone(l[pos:end])
	ins := make([]string, 0, len(s.Inserted))
	for _, m := range s.Inserted {
		ins = append(ins, m.ID)
	}
	t.pending[s.List] = slices.Concat(l[:pos], ins, l[end:])
	if len(removed) == 0 || s.Outcome == nil {
		return nil
	}
	switch s.Outcome.Kind {
	case agent.OutcomeClaimed:
		b := t.open(s.Outcome.Turn, at)
		b.claimed = append(b.claimed, removed...)
	case agent.OutcomeDiscarded:
		out := make([]protocol.RunCompletedNotification, 0, len(removed))
		for _, id := range removed {
			out = append(out, protocol.RunCompletedNotification{
				Run:          protocol.RunRef{Kind: "prompt", MessageID: id},
				FinishReason: FinishDiscarded, FirstSeq: int64(at), LastSeq: int64(at),
			})
		}
		return out
	}
	return nil
}

// end closes turn p.Turn and completes every message it claimed.
func (t *runTracker) end(p event.TurnEnd, at event.Seq) []protocol.RunCompletedNotification {
	b, ok := t.turns[p.Turn]
	if !ok {
		return nil
	}
	delete(t.turns, p.Turn)
	out := make([]protocol.RunCompletedNotification, 0, len(b.claimed))
	for _, id := range b.claimed {
		out = append(out, protocol.RunCompletedNotification{
			Run: protocol.RunRef{Kind: "prompt", MessageID: id}, Turn: int64(p.Turn),
			FinishReason: p.Reason.Kind, FinalResponse: b.last, FirstSeq: int64(b.first), LastSeq: int64(at),
		})
	}
	return out
}

// textOfMessage is the text blocks of m, joined; reasoning and tool calls are
// not the answer.
func textOfMessage(m vocab.Message) string {
	var sb strings.Builder
	for _, b := range m.Blocks() {
		if b.Kind == vocab.KindText && b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	return sb.String()
}
