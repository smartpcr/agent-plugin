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

// SystemPrice is one retained system prompt's seq, its order key and its
// estimated tokens.
type SystemPrice struct {
	Seq    event.Seq `json:"seq"`
	Key    event.Seq `json:"key"`
	Tokens int       `json:"tokens"`
}

// Anchor is the order key of a retained node that replaced others (a
// compaction's summary, a spilled tool result): the key of the first node it
// replaced, which is the position it took.
type Anchor struct {
	Seq event.Seq `json:"seq"`
	Key event.Seq `json:"key"`
}

// Breakdown is the contextBreakdown fold's state: one price per system prompt
// the surface retains, in surface order, and their running total. It holds no
// price per message.
//
// Surface order is an order key per retained node: an appended node's key is
// its seq; a replacement's node takes the key of the first node it replaces.
// Keys rise along the surface, so a replacement of Start..End removes exactly
// the nodes whose key lies between Start's and End's keys. A source it cites
// outside that range (provenance) stays. Only a node that replaced others has
// a key other than its seq, so only those are kept (Anchors), and an anchor
// goes when its node is replaced.
//
// The state is bounded: at most MaxAnchors anchors are kept, the newest. An
// evicted anchor's node is then ordered by its seq, which may misplace a
// later range's ends, so the fold says so (Approximate, sticky) and the
// Meter's Breakdown and Pressure read the system prompts from the session's
// surface, their one owner, instead (token-meter.2, .6). Exact positions for
// every rewritten node would need one entry per node, which DH-ADR-LLM-9
// rules out.
type Breakdown struct {
	System      []SystemPrice `json:"system,omitempty"`
	SystemTotal int           `json:"system_total"`
	Anchors     []Anchor      `json:"anchors,omitempty"`
	Approximate bool          `json:"approximate,omitempty"`
}

// MaxAnchors bounds the anchors a Breakdown keeps.
const MaxAnchors = 16

// key is the order key of the retained node at seq.
func (s Breakdown) key(seq event.Seq) event.Seq {
	for _, p := range s.System {
		if p.Seq == seq {
			return p.Key
		}
	}
	for _, a := range s.Anchors {
		if a.Seq == seq {
			return a.Key
		}
	}
	return seq
}

// isSystem reports whether the retained node at seq is a priced system prompt.
func (s Breakdown) isSystem(seq event.Seq) bool {
	return slices.ContainsFunc(s.System, func(p SystemPrice) bool { return p.Seq == seq })
}

// surfaceTypes are the five surface event types, the ones that change history.
var surfaceTypes = []string{
	event.SystemMessage.Name(), event.DeveloperMessage.Name(), event.UserMessage.Name(),
	event.AssistantMessage.Name(), event.ToolResult.Name(),
}

// BreakdownView is the contextBreakdown fold over the surface events.
func BreakdownView() views.Definition[Breakdown] {
	return views.Definition[Breakdown]{
		Key: BreakdownKey, Version: 2, Stored: true, Types: surfaceTypes,
		Init: func(session.Header) Breakdown { return Breakdown{} },
		Apply: func(s Breakdown, ev event.Event) (Breakdown, bool) {
			m, ok := surfaceMessage(ev)
			if !ok {
				return s, false
			}
			next := s.step(ev, m)
			return next, !slices.Equal(next.System, s.System) || !slices.Equal(next.Anchors, s.Anchors) || next.Approximate != s.Approximate
		},
	}
}

// step is s after the surface event ev carrying m, applied as the surface does
// (surface/history.go): a replacement removes the prices of the system prompts
// in its range, whatever its own type (a compaction may cover a later system
// prompt), and keeps those it only cites; a system prompt is priced only when
// it leaves a node, so an empty system replacement removes the price and adds
// none. The lists are rebuilt, never written in place, so an earlier state
// shares nothing with it.
func (s Breakdown) step(ev event.Event, m vocab.Message) Breakdown {
	op := ev.Op()
	in := func(event.Seq) bool { return false }
	key := ev.Seq()
	if op.Kind == event.OpReplace {
		from, to := s.key(op.Start), s.key(op.End)
		in = func(k event.Seq) bool { return k >= from && k <= to }
		key = from
	}
	kept := make([]SystemPrice, 0, len(s.System)+1)
	for _, p := range s.System {
		if !in(p.Key) {
			kept = append(kept, p)
		}
	}
	anchors := make([]Anchor, 0, len(s.Anchors)+1)
	for _, a := range s.Anchors {
		if !in(a.Key) {
			anchors = append(anchors, a)
		}
	}
	if retained(ev.Type(), m) {
		switch {
		case ev.Type() == event.SystemMessage.Name():
			at := len(kept)
			for i, p := range kept {
				if p.Key > key {
					at = i
					break
				}
			}
			kept = slices.Insert(kept, at, SystemPrice{Seq: ev.Seq(), Key: key, Tokens: EstimateMessage(m)})
		case key != ev.Seq():
			anchors = append(anchors, Anchor{Seq: ev.Seq(), Key: key})
		}
	}
	n := 0
	for _, p := range kept {
		n += p.Tokens
	}
	approx := s.Approximate
	if len(anchors) > MaxAnchors {
		anchors, approx = anchors[len(anchors)-MaxAnchors:], true
	}
	if len(anchors) == 0 {
		anchors = nil
	}
	return Breakdown{System: kept, SystemTotal: n, Anchors: anchors, Approximate: approx}
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
// happened since it (Rewritten) and whether one of them removed a node other
// than a system prompt (Compacted).
//
// It also carries the session's calibration factor. Surface is the
// heuristic's running price of the retained non-system nodes and Header that of
// the request header's tools; both are exact until a replacement removes a
// node whose price the bounded state does not keep (Inexact). At every sample
// Factor is the sample's reported prompt over the heuristic's price of that
// same request: the estimate the runtime recorded on the settlement when it
// sent it (llm.Attempt.Estimate), so a request after any number of
// compactions recalibrates. A settlement without one (written before the
// field existed) calibrates on the running price instead, and only when that
// is exact and its request was the fold's latest state (nothing on the surface
// or the header changed after its requestAsOf). Otherwise Factor keeps the
// last ratio, so a compaction or a header change scales the estimate by it
// rather than dropping back to the raw heuristic (llm.md, token-meter.4).
// Bounded: it grows with the log only as Breakdown does.
type Pressure struct {
	Sample         *Sample   `json:"sample,omitempty"`
	System         Breakdown `json:"system"`
	SystemAtSample int       `json:"system_at_sample,omitempty"`
	AppendedSince  int       `json:"appended_since,omitempty"`
	Rewritten      bool      `json:"rewritten,omitempty"`
	Compacted      bool      `json:"compacted,omitempty"`
	Surface        int       `json:"surface,omitempty"`
	Header         int       `json:"header,omitempty"`
	Inexact        bool      `json:"inexact,omitempty"`
	Changed        event.Seq `json:"changed,omitempty"`
	Factor         float64   `json:"factor,omitempty"`
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

// PressureView is the contextPressure fold over the surface events and the
// request header.
func PressureView() views.Definition[Pressure] {
	return views.Definition[Pressure]{
		Key: PressureKey, Version: 2, Stored: true, Types: append(slices.Clone(surfaceTypes), event.RequestHeader.Name()),
		Init: func(session.Header) Pressure { return Pressure{} },
		Apply: func(s Pressure, ev event.Event) (Pressure, bool) {
			if ev.Type() == event.RequestHeader.Name() {
				p, err := event.RequestHeader.As(ev)
				if err != nil {
					return s, false
				}
				s.Header, s.Changed = EstimateTools(p.Tools), ev.Seq()
				return s, true
			}
			m, ok := surfaceMessage(ev)
			if !ok {
				return s, false
			}
			op := ev.Op()
			if op.Kind == event.OpReplace {
				// Only a system prompt replacing exactly one system prompt
				// keeps the running price exact: the breakdown reprices it.
				if !(ev.Type() == event.SystemMessage.Name() && op.Start == op.End && s.System.isSystem(op.Start)) {
					s.Inexact, s.Compacted = true, true
				}
				s.Rewritten = true
			}
			s.System = s.System.step(ev, m)
			if ev.Type() == event.AssistantMessage.Name() && op.Kind == event.OpAppend {
				if p, err := event.AssistantMessage.As(ev); err == nil && p.Usage != nil {
					asOf := ev.Seq()
					if p.RequestAsOf != nil {
						asOf = *p.RequestAsOf
					}
					switch {
					case p.Estimate > 0:
						// The runtime priced the request it sent: the same
						// request's ratio, whatever was rewritten before it.
						s.Factor = Calibration(Prompt(*p.Usage), p.Estimate)
					case !s.Inexact && s.Changed <= asOf:
						s.Factor = Calibration(Prompt(*p.Usage), s.Surface+s.System.SystemTotal+s.Header)
					}
					s.Sample = &Sample{Seq: ev.Seq(), RequestAsOf: asOf, RequestDigest: p.RequestDigest,
						Prompt: Prompt(*p.Usage), Output: p.Usage.Output}
					s.SystemAtSample, s.AppendedSince = s.System.SystemTotal, 0
					s.Rewritten, s.Compacted = false, false
					s.Surface += EstimateMessage(m)
					s.Changed = ev.Seq()
					return s, true
				}
			}
			if ev.Type() != event.SystemMessage.Name() && retained(ev.Type(), m) {
				s.AppendedSince += EstimateMessage(m)
				s.Surface += EstimateMessage(m)
			}
			s.Changed = ev.Seq()
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
