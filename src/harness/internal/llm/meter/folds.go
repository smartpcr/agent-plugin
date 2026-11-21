package meter

import (
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// The three folds' keys: the tokenUsage, contextBreakdown and contextPressure
// projections of llm.md section 6.2.
const (
	UsageKey     = "llm/token-usage"
	BreakdownKey = "llm/context-breakdown"
	PressureKey  = "llm/context-pressure"
)

// Usage is the tokenUsage fold's state: what the session was billed.
type Usage struct {
	// Billed sums every attempt's provider-reported usage, by bucket.
	Billed vocab.Usage `json:"billed"`
	// Attempts counts the settlements folded; Reported those that carried usage.
	Attempts int `json:"attempts"`
	Reported int `json:"reported"`
	// Last is the usage of the last attempt that reported any; LastSeq its seq.
	Last    vocab.Usage `json:"last"`
	LastSeq event.Seq   `json:"last_seq,omitempty"`
}

// Prompt is the tokens of a usage's request: uncached input, cache reads and
// cache writes, the buckets being disjoint.
func Prompt(u vocab.Usage) int { return u.Input + u.CacheRead + u.CacheWrite }

// Total is the sum of the four disjoint buckets.
func Total(u vocab.Usage) int { return Prompt(u) + u.Output }

// UsageView is the tokenUsage fold over the settlement events.
func UsageView() views.Definition[Usage] {
	return views.Definition[Usage]{
		Key: UsageKey, Version: 3, Stored: true,
		Types: []string{event.AssistantMessage.Name(), event.AssistantAttempt.Name()},
		Init:  func(session.Header) Usage { return Usage{} },
		Apply: func(s Usage, ev event.Event) (Usage, bool) {
			var u *vocab.Usage
			switch ev.Type() {
			case event.AssistantMessage.Name():
				p, err := event.AssistantMessage.As(ev)
				if err != nil {
					return s, false
				}
				u = p.Usage
			case event.AssistantAttempt.Name():
				p, err := event.AssistantAttempt.As(ev)
				if err != nil {
					return s, false
				}
				u = p.Usage
			default:
				return s, false
			}
			s.Attempts++
			if u != nil {
				s.Billed = add(s.Billed, *u)
				s.Reported++
				s.Last, s.LastSeq = *u, ev.Seq()
			}
			return s, true
		},
	}
}

// SystemPrice is one retained system prompt's seq and estimated tokens.
type SystemPrice struct {
	Seq    event.Seq `json:"seq"`
	Tokens int       `json:"tokens"`
}

// Breakdown is the contextBreakdown fold's state: one price per system prompt
// the surface retains, in surface order, and their running total. It holds
// nothing per message.
type Breakdown struct {
	System      []SystemPrice `json:"system,omitempty"`
	SystemTotal int           `json:"system_total"`
}

// surfaceTypes are the five surface event types, the ones that change history.
var surfaceTypes = []string{
	event.SystemMessage.Name(), event.DeveloperMessage.Name(), event.UserMessage.Name(),
	event.AssistantMessage.Name(), event.ToolResult.Name(),
}

// BreakdownView is the contextBreakdown fold over the surface events.
func BreakdownView() views.Definition[Breakdown] {
	return views.Definition[Breakdown]{
		Key: BreakdownKey, Version: 1, Stored: true, Types: surfaceTypes,
		Init: func(session.Header) Breakdown { return Breakdown{} },
		Apply: func(s Breakdown, ev event.Event) (Breakdown, bool) {
			m, ok := surfaceMessage(ev)
			if !ok {
				return s, false
			}
			next := s.step(ev, m)
			return next, !slices.Equal(next.System, s.System)
		},
	}
}

// step is s after the surface event ev carrying m, applied as the surface does
// (surface/history.go): a replacement removes the prices of every system prompt
// it replaces, whatever its own type (a compaction may cover a later system
// prompt), and a system prompt is priced only when it leaves a node, so an
// empty system replacement removes the price and adds none. The list is
// rebuilt, never written in place, so an earlier state shares nothing with it.
func (s Breakdown) step(ev event.Event, m vocab.Message) Breakdown {
	op := ev.Op()
	replaced := map[event.Seq]bool{}
	if op.Kind == event.OpReplace {
		replaced[op.Start], replaced[op.End] = true, true
		for _, q := range ev.Sources() {
			replaced[q] = true
		}
	}
	kept := make([]SystemPrice, 0, len(s.System)+1)
	at := -1
	for _, p := range s.System {
		if replaced[p.Seq] {
			if at < 0 {
				at = len(kept)
			}
			continue
		}
		kept = append(kept, p)
	}
	if ev.Type() == event.SystemMessage.Name() && retained(ev.Type(), m) {
		p := SystemPrice{Seq: ev.Seq(), Tokens: EstimateMessage(m)}
		if at < 0 {
			kept = append(kept, p)
		} else {
			kept = slices.Insert(kept, at, p)
		}
	}
	n := 0
	for _, p := range kept {
		n += p.Tokens
	}
	return Breakdown{System: kept, SystemTotal: n}
}

// Sample is the last provider-reported context: the usage of a settled step
// (assistant/message) and the request it answered.
type Sample struct {
	Seq           event.Seq `json:"seq"`
	RequestAsOf   event.Seq `json:"request_as_of"`
	RequestDigest string    `json:"request_digest,omitempty"`
	Prompt        int       `json:"prompt"`
	Output        int       `json:"output"`
}

// Pressure is the contextPressure fold's state: the last sample, the system
// prompts' prices, their total when the sample was taken, the heuristic price
// of the nodes appended since the sample, and whether any surface replacement
// happened since it (Rewritten) and whether one of them was not a system
// prompt's (Compacted). Bounded: it does not grow with the log.
type Pressure struct {
	Sample         *Sample   `json:"sample,omitempty"`
	System         Breakdown `json:"system"`
	SystemAtSample int       `json:"system_at_sample,omitempty"`
	AppendedSince  int       `json:"appended_since,omitempty"`
	Rewritten      bool      `json:"rewritten,omitempty"`
	Compacted      bool      `json:"compacted,omitempty"`
}

// Projected is the context the next request is projected to occupy: the
// sample's prompt and answer, plus every node appended since, plus the system
// prompts' price change since (a system-prompt replacement is repriced at
// once). Before any sample it is the heuristic price of the surface. After a
// compaction it is an upper bound, the replaced nodes' price being unknown to
// a bounded fold; the Meter's Pressure measures the surface instead.
func (p Pressure) Projected() int {
	n := p.AppendedSince + p.System.SystemTotal
	if p.Sample != nil {
		n += p.Sample.Prompt + p.Sample.Output - p.SystemAtSample
	}
	return max(n, 0)
}

// Fraction is Projected over a context window of window tokens; 0 for a window
// of 0 or less.
func (p Pressure) Fraction(window int) float64 { return fraction(p.Projected(), window) }

// PressureView is the contextPressure fold over the surface events.
func PressureView() views.Definition[Pressure] {
	return views.Definition[Pressure]{
		Key: PressureKey, Version: 1, Stored: true, Types: surfaceTypes,
		Init: func(session.Header) Pressure { return Pressure{} },
		Apply: func(s Pressure, ev event.Event) (Pressure, bool) {
			m, ok := surfaceMessage(ev)
			if !ok {
				return s, false
			}
			s.System = s.System.step(ev, m)
			if ev.Type() == event.AssistantMessage.Name() && ev.Op().Kind == event.OpAppend {
				if p, err := event.AssistantMessage.As(ev); err == nil && p.Usage != nil {
					asOf := ev.Seq()
					if p.RequestAsOf != nil {
						asOf = *p.RequestAsOf
					}
					s.Sample = &Sample{Seq: ev.Seq(), RequestAsOf: asOf, RequestDigest: p.RequestDigest,
						Prompt: Prompt(*p.Usage), Output: p.Usage.Output}
					s.SystemAtSample, s.AppendedSince = s.System.SystemTotal, 0
					s.Rewritten, s.Compacted = false, false
					return s, true
				}
			}
			if ev.Op().Kind == event.OpReplace {
				s.Rewritten = true
				if ev.Type() != event.SystemMessage.Name() {
					s.Compacted = true
				}
			}
			if ev.Type() != event.SystemMessage.Name() && retained(ev.Type(), m) {
				s.AppendedSince += EstimateMessage(m)
			}
			return s, true
		},
	}
}

// surfaceMessage is the message a surface event carries.
func surfaceMessage(ev event.Event) (vocab.Message, bool) {
	switch ev.Type() {
	case event.SystemMessage.Name():
		p, err := event.SystemMessage.As(ev)
		return p.Message, err == nil
	case event.DeveloperMessage.Name():
		p, err := event.DeveloperMessage.As(ev)
		return p.Message, err == nil
	case event.UserMessage.Name():
		p, err := event.UserMessage.As(ev)
		return p.Message, err == nil
	case event.AssistantMessage.Name():
		p, err := event.AssistantMessage.As(ev)
		return p.Message, err == nil
	case event.ToolResult.Name():
		p, err := event.ToolResult.As(ev)
		return p.Message, err == nil
	}
	return vocab.Message{}, false
}

// retained mirrors the surface's rule: a user message or tool result always
// leaves a node; any other message leaves none when every block is blank text.
func retained(typ string, m vocab.Message) bool {
	if typ == event.UserMessage.Name() || typ == event.ToolResult.Name() {
		return true
	}
	for _, b := range m.Blocks() {
		if b.Kind != vocab.KindText || b.Text == nil || strings.TrimSpace(b.Text.Text) != "" {
			return true
		}
	}
	return false
}

func fraction(n, window int) float64 {
	if window <= 0 {
		return 0
	}
	return float64(n) / float64(window)
}

func add(a, b vocab.Usage) vocab.Usage {
	return vocab.Usage{Input: a.Input + b.Input, CacheRead: a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite, Output: a.Output + b.Output, Total: a.Total + b.Total}
}
