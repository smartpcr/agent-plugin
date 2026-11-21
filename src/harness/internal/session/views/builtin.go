package views

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// The built-in views of docs/harness/areas/session-views.md DH-D-VIEW-3: four
// stored fold families registered by RegisterBuiltins. Each Apply returns a new
// state and never writes to its input's visible values; an append-only slice may
// grow into spare capacity past the input's length, which the input does not see
// (the same rule as the surface history).
const (
	ListSummaryKey = "view/list-summary"
	TurnIndexKey   = "view/turn-index"
	StatsKey       = "view/stats"
	EventIndexKey  = "view/event-index"
)

// PreviewRunes bounds ListSummary.Preview.
const PreviewRunes = 80

// Builtins are the handles of the built-in views in one registry.
type Builtins struct {
	ListSummary Handle[ListSummary]
	TurnIndex   Handle[TurnIndex]
	Stats       Handle[Stats]
	EventIndex  Handle[EventIndex]
}

// RegisterBuiltins registers the four built-in views in r (the session.views
// component's registrations); a preset that does not want one registers the
// others by their definitions.
func RegisterBuiltins(r *Registry) (Builtins, error) {
	var (
		b   Builtins
		err error
	)
	if b.ListSummary, err = Register(r, ListSummaryView()); err != nil {
		return Builtins{}, err
	}
	if b.TurnIndex, err = Register(r, TurnIndexView()); err != nil {
		return Builtins{}, err
	}
	if b.Stats, err = Register(r, StatsView()); err != nil {
		return Builtins{}, err
	}
	if b.EventIndex, err = Register(r, EventIndexView()); err != nil {
		return Builtins{}, err
	}
	return b, nil
}

// Refold is d's state over evs from Init, as a registry folds them; the
// reference a stored state is checked against.
func Refold[S any](d Definition[S], h session.Header, evs []event.Event) S {
	return FoldFrom(d, d.Init(h), evs)
}

// FoldFrom is d's state s continued over evs: a stored row taken up by the log
// after it.
func FoldFrom[S any](d Definition[S], s S, evs []event.Event) S {
	for _, ev := range evs {
		if d.Types != nil && !slices.Contains(d.Types, ev.Type()) {
			continue
		}
		if next, changed := d.Apply(s, ev); changed {
			s = next
		}
	}
	return s
}

// ------------------------------------------------------------ list summary

// ListSummary is what the session list shows of a session: whether it is blank
// (no prompt yet, hidden by the list), the last human prompt time it orders by,
// and a preview of the first prompt.
type ListSummary struct {
	Blank        bool      `json:"blank"`
	Prompts      int       `json:"prompts"`
	Turns        int       `json:"turns"`
	FirstPrompt  event.Seq `json:"first_prompt"`
	LastPromptAt int64     `json:"last_prompt_at"` // Unix ms; 0 when blank
	Preview      string    `json:"preview"`
}

// ListSummaryView is the list summary fold.
func ListSummaryView() Definition[ListSummary] {
	return Definition[ListSummary]{
		Key: ListSummaryKey, Version: 1, Stored: true,
		Types: []string{event.UserMessage.Name(), event.TurnStartType.Name()},
		Init:  func(session.Header) ListSummary { return ListSummary{Blank: true} },
		Apply: func(s ListSummary, ev event.Event) (ListSummary, bool) {
			switch ev.Type() {
			case event.TurnStartType.Name():
				s.Turns++
				return s, true
			case event.UserMessage.Name():
				p, err := event.UserMessage.As(ev)
				if err != nil {
					return s, false
				}
				if s.Prompts == 0 {
					s.FirstPrompt = ev.Seq()
					s.Preview = preview(p.Message, p.Display)
				}
				s.Prompts++
				s.Blank = false
				s.LastPromptAt = ev.Time()
				return s, true
			}
			return s, false
		},
	}
}

// preview is the message's text (or display), whitespace collapsed, cut to
// PreviewRunes runes.
func preview(m vocab.Message, display string) string {
	text := display
	if text == "" {
		var parts []string
		for _, b := range m.Blocks() {
			if b.Kind == vocab.KindText && b.Text != nil {
				parts = append(parts, b.Text.Text)
			}
		}
		text = strings.Join(parts, " ")
	}
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= PreviewRunes {
		return text
	}
	r := []rune(text)
	return string(r[:PreviewRunes-1]) + "…"
}

// ------------------------------------------------------------ turn index

// TurnEntry is one turn of the outline. End and Reason are set once the turn
// settles (turn/end); an open turn is unsettled.
type TurnEntry struct {
	Turn      int       `json:"turn"`
	Agent     string    `json:"agent,omitempty"`
	Start     event.Seq `json:"start"`
	StartAt   int64     `json:"start_at"`
	Settled   bool      `json:"settled"`
	End       event.Seq `json:"end,omitempty"`
	EndAt     int64     `json:"end_at,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Steps     int       `json:"steps"`
	HasPrompt bool      `json:"has_prompt,omitempty"`
	Prompt    event.Seq `json:"prompt,omitempty"` // the turn's first user/message
}

// TurnIndex is the navigable outline of a session: its turns in log order.
type TurnIndex struct {
	Turns []TurnEntry `json:"turns"`
}

// Open is the open (unsettled) last turn, or false.
func (t TurnIndex) Open() (TurnEntry, bool) {
	if n := len(t.Turns); n > 0 && !t.Turns[n-1].Settled {
		return t.Turns[n-1], true
	}
	return TurnEntry{}, false
}

// TurnIndexView is the turn index fold.
func TurnIndexView() Definition[TurnIndex] {
	return Definition[TurnIndex]{
		Key: TurnIndexKey, Version: 1, Stored: true,
		Types: []string{event.TurnStartType.Name(), event.TurnEndType.Name(), event.StepStartType.Name(), event.UserMessage.Name()},
		Init:  func(session.Header) TurnIndex { return TurnIndex{} },
		Apply: func(s TurnIndex, ev event.Event) (TurnIndex, bool) {
			if ev.Type() == event.TurnStartType.Name() {
				p, err := event.TurnStartType.As(ev)
				if err != nil {
					return s, false
				}
				s.Turns = append(slices.Clone(s.Turns), TurnEntry{Turn: p.Turn, Agent: p.Agent, Start: ev.Seq(), StartAt: ev.Time()})
				return s, true
			}
			open, ok := s.Open()
			if !ok {
				return s, false
			}
			switch ev.Type() {
			case event.TurnEndType.Name():
				p, err := event.TurnEndType.As(ev)
				if err != nil || p.Turn != open.Turn {
					return s, false
				}
				open.Settled, open.End, open.EndAt, open.Reason = true, ev.Seq(), ev.Time(), p.Reason.Kind
			case event.StepStartType.Name():
				open.Steps++
			case event.UserMessage.Name():
				if open.HasPrompt {
					return s, false
				}
				open.HasPrompt, open.Prompt = true, ev.Seq()
			default:
				return s, false
			}
			s.Turns = slices.Clone(s.Turns)
			s.Turns[len(s.Turns)-1] = open
			return s, true
		},
	}
}

// ------------------------------------------------------------ statistics

// Stats counts the whole session for the statistics strip. An attempt is a model
// call: an assistant/message or an assistant/attempt; Timed counts those that
// carry timing, ModelMillis sums their durations. Steps counts closed steps
// (a step/end), VisibleSteps the closed ones that produced visible output (an
// assistant/message with non-blank text), and OpenStep is true while a step has
// started and not yet ended, its output so far in OpenVisible: an entered but
// unsettled step is not counted as closed.
type Stats struct {
	Events            int            `json:"events"`
	Turns             int            `json:"turns"`
	Steps             int            `json:"steps"`
	VisibleSteps      int            `json:"visible_steps"`
	OpenStep          bool           `json:"open_step,omitempty"`
	OpenVisible       bool           `json:"open_visible,omitempty"`
	UserMessages      int            `json:"user_messages"`
	AssistantMessages int            `json:"assistant_messages"`
	Attempts          int            `json:"attempts"`
	FailedAttempts    int            `json:"failed_attempts"`
	Timed             int            `json:"timed"`
	ModelMillis       int64          `json:"model_ms"`
	FirstTokenMillis  int64          `json:"first_token_ms"`
	ToolCalls         int            `json:"tool_calls"`
	ToolResults       int            `json:"tool_results"`
	ToolErrors        int            `json:"tool_errors"`
	Spilled           int            `json:"spilled"`
	Replacements      int            `json:"replacements"`
	Usage             vocab.Usage    `json:"usage"`
	Outcomes          map[string]int `json:"outcomes"` // turn/end reason kind -> turns
}

// StatsView is the statistics fold.
func StatsView() Definition[Stats] {
	return Definition[Stats]{
		Key: StatsKey, Version: 2, Stored: true,
		Init: func(session.Header) Stats { return Stats{} },
		Apply: func(s Stats, ev event.Event) (Stats, bool) {
			s.Events++
			if ev.Op().Kind == event.OpReplace {
				s.Replacements++
			}
			timed := func(u *vocab.Usage, t *event.Timing) {
				s.Attempts++
				if u != nil {
					s.Usage = addUsage(s.Usage, *u)
				}
				if t != nil {
					s.Timed++
					if t.End > t.Start {
						s.ModelMillis += t.End - t.Start
					}
					if t.FirstToken > t.Start {
						s.FirstTokenMillis += t.FirstToken - t.Start
					}
				}
			}
			switch ev.Type() {
			case event.TurnStartType.Name():
				s.Turns++
			case event.TurnEndType.Name():
				if p, err := event.TurnEndType.As(ev); err == nil {
					s.Outcomes = maps.Clone(s.Outcomes)
					if s.Outcomes == nil {
						s.Outcomes = map[string]int{}
					}
					s.Outcomes[p.Reason.Kind]++
				}
			case event.StepStartType.Name():
				s.OpenStep, s.OpenVisible = true, false
			case event.StepEndType.Name():
				if s.OpenStep {
					s.Steps++
					if s.OpenVisible {
						s.VisibleSteps++
					}
				}
				s.OpenStep, s.OpenVisible = false, false
			case event.UserMessage.Name():
				s.UserMessages++
			case event.AssistantMessage.Name():
				s.AssistantMessages++
				if p, err := event.AssistantMessage.As(ev); err == nil {
					timed(p.Usage, p.Timing)
					s.OpenVisible = s.OpenVisible || (s.OpenStep && hasText(p.Message))
				} else {
					s.Attempts++
				}
			case event.AssistantAttempt.Name():
				s.FailedAttempts++
				if p, err := event.AssistantAttempt.As(ev); err == nil {
					timed(p.Usage, p.Timing)
				} else {
					s.Attempts++
				}
			case event.ToolCall.Name():
				s.ToolCalls++
			case event.ToolResult.Name():
				s.ToolResults++
				if p, err := event.ToolResult.As(ev); err == nil {
					if p.IsError {
						s.ToolErrors++
					}
					if p.Spill != nil {
						s.Spilled++
					}
				}
			}
			return s, true
		},
	}
}

func addUsage(a, b vocab.Usage) vocab.Usage {
	return vocab.Usage{Input: a.Input + b.Input, CacheRead: a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite, Output: a.Output + b.Output, Total: a.Total + b.Total}
}

// ------------------------------------------------------------ event index

// The event classes: where an event stands relative to model history, as the
// surface fold classifies it (surface.Class).
const (
	ClassLogOnly  = "log-only"
	ClassCurrent  = "current"
	ClassShadowed = "shadowed"
)

// EventEntry is one event's derived row: its class, the replacement that
// shadowed it, the events it cites, and the tool call it belongs to.
type EventEntry struct {
	Seq        event.Seq   `json:"seq"`
	Type       string      `json:"type"`
	Class      string      `json:"class"`
	Replaced   bool        `json:"replaced,omitempty"`
	ReplacedBy event.Seq   `json:"replaced_by,omitempty"`
	Cites      []event.Seq `json:"cites,omitempty"`
	Call       string      `json:"call,omitempty"`
}

// EventIndex is the derived row of every event, by seq, and the seqs of current
// model history in history order (which a replacement reorders).
type EventIndex struct {
	Events  []EventEntry `json:"events"`
	History []event.Seq  `json:"history"`
}

// Entry is the row of seq, or false.
func (x EventIndex) Entry(seq event.Seq) (EventEntry, bool) {
	if i := int(seq); i < len(x.Events) && x.Events[i].Seq == seq {
		return x.Events[i], true
	}
	for _, e := range x.Events {
		if e.Seq == seq {
			return e, true
		}
	}
	return EventEntry{}, false
}

// ByCall is the seqs of the events that belong to tool call id, in log order.
func (x EventIndex) ByCall(id string) []event.Seq {
	var out []event.Seq
	for _, e := range x.Events {
		if e.Call == id {
			out = append(out, e.Seq)
		}
	}
	return out
}

// EventIndexView is the event index fold.
func EventIndexView() Definition[EventIndex] {
	return Definition[EventIndex]{
		Key: EventIndexKey, Version: 2, Stored: true,
		Init: func(session.Header) EventIndex { return EventIndex{} },
		Apply: func(s EventIndex, ev event.Event) (EventIndex, bool) {
			e := EventEntry{Seq: ev.Seq(), Type: ev.Type(), Class: ClassLogOnly, Call: callOf(ev)}
			if c := ev.Sources(); len(c) > 0 {
				e.Cites = c
			}
			info, ok := event.Default().Lookup(ev.Type())
			op := ev.Op()
			if !ok || !info.Surface || op.Kind == event.OpNone {
				s.Events = append(s.Events, e)
				return s, true
			}
			if ev.Version() > info.Version {
				// A newer ignorable surface record: the core skips it (no node, no
				// replacement) and classifies it shadowed (surface.State.Plan).
				e.Class = ClassShadowed
				s.Events = append(s.Events, e)
				return s, true
			}
			node := producesNode(ev)
			if node {
				e.Class = ClassCurrent
			} else {
				e.Class = ClassShadowed
			}
			if op.Kind != event.OpReplace {
				s.Events = append(s.Events, e)
				if node {
					s.History = append(s.History, e.Seq)
				}
				return s, true
			}
			from, to := slices.Index(s.History, op.Start), slices.Index(s.History, op.End)
			if from < 0 || to < from {
				s.Events = append(s.Events, e)
				return s, true
			}
			events := append(slices.Clone(s.Events), e)
			for _, seq := range s.History[from : to+1] {
				for i := range events {
					if events[i].Seq == seq {
						events[i].Class, events[i].Replaced, events[i].ReplacedBy = ClassShadowed, true, e.Seq
						break
					}
				}
			}
			hist := slices.Clone(s.History[:from])
			if node {
				hist = append(hist, e.Seq)
			}
			s.Events, s.History = events, append(hist, s.History[to+1:]...)
			return s, true
		},
	}
}

// producesNode reports whether a surface event becomes a history node: a tool
// result or a user message always does; any other message only when it has
// content (the surface fold's rule).
func producesNode(ev event.Event) bool {
	var m vocab.Message
	switch ev.Type() {
	case event.ToolResult.Name(), event.UserMessage.Name():
		return true
	case event.AssistantMessage.Name():
		p, err := event.AssistantMessage.As(ev)
		if err != nil {
			return true
		}
		m = p.Message
	case event.SystemMessage.Name():
		p, err := event.SystemMessage.As(ev)
		if err != nil {
			return true
		}
		m = p.Message
	case event.DeveloperMessage.Name():
		p, err := event.DeveloperMessage.As(ev)
		if err != nil {
			return true
		}
		m = p.Message
	default:
		return true
	}
	for _, b := range m.Blocks() {
		if b.Kind != vocab.KindText || b.Text == nil || strings.TrimSpace(b.Text.Text) != "" {
			return true
		}
	}
	return false
}

// hasText reports whether m has a text block with non-blank text.
func hasText(m vocab.Message) bool {
	return slices.ContainsFunc(m.Blocks(), func(b vocab.Block) bool {
		return b.Kind == vocab.KindText && b.Text != nil && strings.TrimSpace(b.Text.Text) != ""
	})
}

// callOf is the tool call id an event belongs to: a tool call's own, a tool
// result's answered call.
func callOf(ev event.Event) string {
	switch ev.Type() {
	case event.ToolCall.Name():
		if p, err := event.ToolCall.As(ev); err == nil {
			return p.CallID
		}
	case event.ToolResult.Name():
		if p, err := event.ToolResult.As(ev); err == nil {
			return p.CallID
		}
	}
	return ""
}
