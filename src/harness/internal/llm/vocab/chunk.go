package vocab

import "encoding/json"

// Chunk is one element of a model stream; the set is sealed to the seven
// types below (docs/harness/areas/llm.md section 6.2).
type Chunk interface{ chunk() }

// BlockStart opens block Index; blocks are numbered in first-seen order.
type BlockStart struct {
	Index int
	Kind  BlockKind
}

// TextDelta appends text to block Index.
type TextDelta struct {
	Index int
	Text  string
}

// ReasoningDelta appends reasoning to block Index.
type ReasoningDelta struct {
	Index int
	Text  string
}

// ToolCallDelta appends to a tool call. ArgsDelta is raw text exactly as
// received; it is checked as JSON once, at BlockEnd.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	ArgsDelta string
}

// BlockEnd closes block Index with the complete block.
type BlockEnd struct {
	Index int
	Block Block
}

// UsageChunk reports usage; it comes before Finish.
type UsageChunk struct {
	Usage Usage
}

// FinishReason is why a stream ended; the set is closed.
type FinishReason string

// The finish reasons.
const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool_calls"
	FinishMaxTokens FinishReason = "max_tokens"
	FinishAborted   FinishReason = "aborted"
	FinishError     FinishReason = "error"
)

// Finish ends a stream; nothing follows it. Failure is set for Aborted and
// Error. Replay is the provider state the next request must send back.
type Finish struct {
	Reason  FinishReason
	Failure *Failure
	Replay  json.RawMessage
}

func (BlockStart) chunk()     {}
func (TextDelta) chunk()      {}
func (ReasoningDelta) chunk() {}
func (ToolCallDelta) chunk()  {}
func (BlockEnd) chunk()       {}
func (UsageChunk) chunk()     {}
func (Finish) chunk()         {}
