package event

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// The core vocabulary (core-session.md §6.2). Names are upstream's. The five
// surface types are SystemMessage, DeveloperMessage, UserMessage, AssistantMessage
// and ToolResult; only this package declares surface types.

// TurnStart opens a turn.
type TurnStart struct {
	Turn  int    `json:"turn"`
	Agent string `json:"agent,omitempty"`
}

// The turn/end reason kinds: upstream's seven plus budget and refusal.
const (
	ReasonCompleted   = "completed"
	ReasonMaxTokens   = "max-tokens"
	ReasonBlocked     = "blocked"
	ReasonAborted     = "aborted"
	ReasonError       = "error"
	ReasonInterrupted = "interrupted"
	ReasonStop        = "stop"
	ReasonBudget      = "budget"
	ReasonRefusal     = "refusal"
)

// Reason is why a turn ended. Kind is one of the Reason constants; an unknown kind
// written by a newer build is preserved as it was read.
type Reason struct {
	Kind    string         `json:"kind"`
	By      string         `json:"by,omitempty"`     // blocked: who blocked
	Detail  string         `json:"reason,omitempty"` // blocked: why
	Cause   string         `json:"cause,omitempty"`  // aborted: the cancel cause
	Failure *vocab.Failure `json:"failure,omitempty"`
	Limit   string         `json:"limit,omitempty"` // budget: which limit
	Value   int            `json:"value,omitempty"` // budget: its value
}

// TurnEnd closes a turn.
type TurnEnd struct {
	Turn   int    `json:"turn"`
	Reason Reason `json:"reason"`
}

// StepStart opens one model call within a turn.
type StepStart struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

// StepEnd closes a step.
type StepEnd struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

// MessagePayload is the payload of system/message and developer/message.
type MessagePayload struct {
	Message vocab.Message `json:"message"`
}

// UserMessagePayload is the payload of user/message; Display is what a client
// shows when it differs from what the model is sent.
type UserMessagePayload struct {
	Message vocab.Message `json:"message"`
	Display string        `json:"display,omitempty"`
}

// Timing is a model request's start, first token and end, in Unix milliseconds.
type Timing struct {
	Start      int64 `json:"start"`
	FirstToken int64 `json:"first_token,omitempty"`
	End        int64 `json:"end"`
}

// AssistantMessagePayload is the payload of assistant/message. The stream is the
// cold part; usage and timing are hot, so no fold parses a stream. A seeded message
// from an importer has no request fields and names its foreign source instead.
type AssistantMessagePayload struct {
	Message       vocab.Message   `json:"message"`
	RequestAsOf   *Seq            `json:"request_as_of,omitempty"`
	RequestDigest string          `json:"request_digest,omitempty"`
	Usage         *vocab.Usage    `json:"usage,omitempty"`
	Timing        *Timing         `json:"timing,omitempty"`
	Stream        json.RawMessage `json:"stream,omitempty"`
}

// AssistantAttemptPayload is the log-only record of a model call that did not
// produce the assistant/message: its partial message, if any, and the failure.
type AssistantAttemptPayload struct {
	Message       *vocab.Message  `json:"message,omitempty"`
	RequestAsOf   *Seq            `json:"request_as_of,omitempty"`
	RequestDigest string          `json:"request_digest,omitempty"`
	Usage         *vocab.Usage    `json:"usage,omitempty"`
	Timing        *Timing         `json:"timing,omitempty"`
	Failure       *vocab.Failure  `json:"failure,omitempty"`
	Stream        json.RawMessage `json:"stream,omitempty"`
}

// ToolCallPayload is a tool call the model made; Arguments stay the raw string the
// model produced.
type ToolCallPayload struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Spill names where an over-large tool output was moved (area A07).
type Spill struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// ToolResultPayload is the result of one tool call. Version 2 added Spill.
type ToolResultPayload struct {
	Message vocab.Message `json:"message"`
	CallID  string        `json:"call_id"`
	IsError bool          `json:"is_error,omitempty"`
	Spill   *Spill        `json:"spill,omitempty"`
}

// RequestHeaderPayload is the request header in force: model, tools and adapter
// defaults. Empty tool lists and defaults are omitted.
type RequestHeaderPayload struct {
	Route    vocab.RouteID      `json:"route,omitempty"`
	Tools    []vocab.ToolSchema `json:"tools,omitempty"`
	Controls *vocab.Controls    `json:"controls,omitempty"`
}

// RequestContextPayload is request context outside history (instructions, cwd).
type RequestContextPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// EndSeedPayload is session/end-seed: the core writes it at a fork cut and at each
// write-open, with the lease epoch.
type EndSeedPayload struct {
	Epoch uint64 `json:"epoch"`
	Cause string `json:"cause,omitempty"`
}

func needMessage(m vocab.Message) error {
	if m.IsZero() {
		return errors.New("message is required")
	}
	return nil
}

func needRole(m vocab.Message, role vocab.Role) error {
	if err := needMessage(m); err != nil {
		return err
	}
	if m.Role() != role {
		return fmt.Errorf("message role is %q, not %q", m.Role(), role)
	}
	return nil
}

// The core declarations, on Default.
var (
	TurnStartType = mustCore(Spec[TurnStart]{Name: "turn/start", Version: 1, NoSources: true})
	TurnEndType   = mustCore(Spec[TurnEnd]{Name: "turn/end", Version: 1, NoSources: true,
		Validate: func(p TurnEnd) error {
			if p.Reason.Kind == "" {
				return errors.New("reason kind is required")
			}
			return nil
		}})
	StepStartType = mustCore(Spec[StepStart]{Name: "step/start", Version: 1, NoSources: true})
	StepEndType   = mustCore(Spec[StepEnd]{Name: "step/end", Version: 1, NoSources: true})

	SystemMessage = mustCoreSurface(Spec[MessagePayload]{Name: "system/message", Version: 1,
		Validate: func(p MessagePayload) error { return needRole(p.Message, vocab.RoleSystem) }})
	DeveloperMessage = mustCoreSurface(Spec[MessagePayload]{Name: "developer/message", Version: 1,
		Validate: func(p MessagePayload) error { return needRole(p.Message, vocab.RoleDeveloper) }})
	UserMessage = mustCoreSurface(Spec[UserMessagePayload]{Name: "user/message", Version: 1,
		Validate: func(p UserMessagePayload) error { return needRole(p.Message, vocab.RoleUser) }})
	AssistantMessage = mustCoreSurface(Spec[AssistantMessagePayload]{Name: "assistant/message", Version: 1,
		Cold: "stream", NoSources: true,
		Validate: func(p AssistantMessagePayload) error { return needRole(p.Message, vocab.RoleAssistant) }})
	AssistantAttempt = mustCore(Spec[AssistantAttemptPayload]{Name: "assistant/attempt", Version: 1,
		Cold: "stream"})

	ToolCall = mustCore(Spec[ToolCallPayload]{Name: "tool/call", Version: 1,
		Validate: func(p ToolCallPayload) error {
			if p.CallID == "" || p.Name == "" {
				return errors.New("call_id and name are required")
			}
			return nil
		}})
	ToolResult = mustCoreSurface(Spec[ToolResultPayload]{Name: "tool/result", Version: 2,
		Upgrades: map[int]Upgrade{1: upgradeToolResultV1},
		Validate: func(p ToolResultPayload) error {
			if p.CallID == "" {
				return errors.New("call_id is required")
			}
			return needRole(p.Message, vocab.RoleTool)
		}})

	RequestHeader  = mustCore(Spec[RequestHeaderPayload]{Name: "request/header", Version: 1})
	RequestContext = mustCore(Spec[RequestContextPayload]{Name: "request/context", Version: 1,
		Validate: func(p RequestContextPayload) error {
			if p.Key == "" {
				return errors.New("key is required")
			}
			return nil
		}})

	EndSeed = mustCore(Spec[EndSeedPayload]{Name: "session/end-seed", Version: 1, NoSources: true})
)

// upgradeToolResultV1 converts tool/result version 1, which named the call "id"
// and had no spill reference, to version 2.
func upgradeToolResultV1(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if id, ok := m["id"]; ok {
		if _, has := m["call_id"]; !has {
			m["call_id"] = id
		}
		delete(m, "id")
	}
	return json.Marshal(m)
}
