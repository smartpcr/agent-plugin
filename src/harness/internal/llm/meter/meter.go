// Package meter is the token meter (DH-D-LLM-6, DH-ADR-LLM-9): token usage and
// context occupancy folded from the settlement events of a session, and the
// character heuristic that estimates a text's or a message's tokens with no
// model call (the result budget of DH-S-10 reads it).
//
// The fold is bounded: its state is totals and the last attempt's figures,
// never one entry per message, so it does not grow with the log. Usage is
// billed per attempt: every assistant/message and every assistant/attempt
// counts once, a failed or retried attempt included, in four disjoint buckets
// (uncached input, cache read, cache write, output). Occupancy is a reference
// figure, not an atomic observation: the context the last attempt that
// reported usage occupied, prompt and answer, which the next request carries.
package meter

import (
	"math"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// UsageKey is the usage fold's key (the tokenUsage projection).
const UsageKey = "llm/token-usage"

// Usage is the usage fold's state.
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

// Occupancy is the context the last reporting attempt occupied: its prompt and
// its answer; 0 before any attempt reported usage.
func (u Usage) Occupancy() int { return Total(u.Last) }

// Pressure is Occupancy as a fraction of a context window of window tokens; 0
// for a window of 0 or less.
func (u Usage) Pressure(window int) float64 {
	if window <= 0 {
		return 0
	}
	return float64(u.Occupancy()) / float64(window)
}

// UsageView is the usage fold over the settlement events.
func UsageView() views.Definition[Usage] {
	return views.Definition[Usage]{
		Key: UsageKey, Version: 1, Stored: true,
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

// Register registers the usage fold in r.
func Register(r *views.Registry) (views.Handle[Usage], error) { return views.Register(r, UsageView()) }

func add(a, b vocab.Usage) vocab.Usage {
	return vocab.Usage{Input: a.Input + b.Input, CacheRead: a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite, Output: a.Output + b.Output, Total: a.Total + b.Total}
}

// The heuristic's constants: about four characters per token, a fixed overhead
// per message and per block (role, separators, a tool call's framing), and a
// flat price per image until routes declare image pricing.
const (
	CharsPerToken = 4
	MessageTokens = 4
	BlockTokens   = 2
	ImageTokens   = 1600
)

// EstimateText is the heuristic's tokens of s: its characters over
// CharsPerToken, rounded up. Deterministic; an empty text is 0.
func EstimateText(s string) int {
	n := utf8.RuneCountInString(s)
	return (n + CharsPerToken - 1) / CharsPerToken
}

// EstimateBytes is EstimateText for n bytes of text not yet decoded (a tool
// result's size before it is read).
func EstimateBytes(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + CharsPerToken - 1) / CharsPerToken
}

// EstimateMessage is the heuristic's tokens of m: every block's text, reasoning,
// tool name and arguments, tool result and image, plus the framing overheads.
func EstimateMessage(m vocab.Message) int {
	n := MessageTokens
	for _, b := range m.Blocks() {
		n += BlockTokens + estimateBlock(b)
	}
	return n
}

func estimateBlock(b vocab.Block) int {
	switch {
	case b.Text != nil:
		return EstimateText(b.Text.Text)
	case b.Reasoning != nil:
		return EstimateText(b.Reasoning.Text) + EstimateText(b.Reasoning.Signature)
	case b.ToolUse != nil:
		return EstimateText(b.ToolUse.ID) + EstimateText(b.ToolUse.Name) + EstimateBytes(len(b.ToolUse.Args))
	case b.ToolResult != nil:
		n := EstimateText(b.ToolResult.CallID)
		for _, p := range b.ToolResult.Parts {
			switch {
			case p.Text != nil:
				n += EstimateText(p.Text.Text)
			case p.Image != nil:
				n += ImageTokens
			}
		}
		return n
	case b.Image != nil:
		return ImageTokens
	}
	return 0
}

// Calibration is the session's calibration factor: the provider-reported prompt
// size of a request over the heuristic's estimate of that same request; 1 when
// either is unknown (0 or less). Deterministic.
func Calibration(reported, estimated int) float64 {
	if reported <= 0 || estimated <= 0 {
		return 1
	}
	return float64(reported) / float64(estimated)
}

// Calibrated is estimate scaled by factor, rounded up; a factor of 0 or less
// leaves it as it is.
func Calibrated(estimate int, factor float64) int {
	if factor <= 0 || estimate <= 0 {
		return max(estimate, 0)
	}
	return int(math.Ceil(float64(estimate) * factor))
}
