// Package meter is the token meter (DH-D-LLM-6, DH-ADR-LLM-9): three registered
// folds of the session log and the tokenMeter service over them, with the
// character heuristic that estimates a text's or a message's tokens with no
// model call (the result budget of DH-S-10 reads it).
//
//   - tokenUsage (UsageKey) bills every attempt: every assistant/message and
//     every assistant/attempt counts once, a failed or retried attempt included,
//     in four disjoint buckets (uncached input, cache read, cache write, output).
//   - contextBreakdown (BreakdownKey) keeps one price per retained system prompt
//     and their running total, nothing per message.
//   - contextPressure (PressureKey) is the projected context: the last provider
//     sample, the heuristic price of the nodes added since it and the system
//     prompts' price change since it, so a system-prompt rewrite is repriced in
//     the same step rather than counted as zero until the next sample.
//
// Every fold's state is bounded: it does not grow with the log. Occupancy is a
// reference figure, not an atomic observation. The Meter service (Key) measures
// a session's current surface, read from the session, its one owner, scaled by
// the session's calibration factor: the last provider-reported prompt size over
// the heuristic's estimate of that same request envelope.
package meter

import (
	"math"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

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
