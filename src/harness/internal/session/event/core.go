package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// StepEnd closes a step. Cause is the cancel cause of a step that was cancelled
// (user, parent, extension, disposed or budget), "" for one that ran to its end.
// Version 2 added Cause.
type StepEnd struct {
	Turn  int    `json:"turn"`
	Step  int    `json:"step"`
	Cause string `json:"cause,omitempty"`
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

// ClientInvocation is the client-invocation boundary of a signed-in client route
// (DH-ADR-LLM-10, M1-D-PIPE-4 A): what the harness started and handed the
// client for one attempt, as the unit a reader rebuilds from the log. Args is the
// argument list after the program with the attempt directory written as
// "{attempt}"; ArgsDigest is the SHA-256 of Args' JSON encoding, StdinDigest the
// SHA-256 of the bytes written to standard input and Files each attempt file's
// SHA-256. Env names the child's variables, never their values. SessionID is the
// client session id the harness chose, Model the model the client reported,
// Retries and Denials the client-internal retries and permission denials it
// reported. Every digest is "sha256:<hex>".
type ClientInvocation struct {
	Client      string       `json:"client"`
	Version     string       `json:"version"`
	Program     string       `json:"program"`
	Args        []string     `json:"args"`
	ArgsDigest  string       `json:"args_digest"`
	StdinDigest string       `json:"stdin_digest"`
	Files       []FileDigest `json:"files,omitempty"`
	Env         []string     `json:"env,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
	Model       string       `json:"model,omitempty"`
	Retries     int          `json:"retries,omitempty"`
	Denials     int          `json:"denials,omitempty"`
}

// FileDigest is one file the adapter wrote for an attempt: its name inside the
// attempt directory and the SHA-256 of its bytes.
type FileDigest struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
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
	// ServedBy names who served a substituted stream (llm.StreamAround); empty
	// when the route's provider did.
	ServedBy string `json:"served_by,omitempty"`
	// Estimate is the token meter's heuristic price of the request sent, so a
	// fold can calibrate against that same request (DH-ADR-LLM-9); 0 when
	// unknown.
	Estimate int `json:"estimate,omitempty"`
	// Client is the client invocation of a signed-in client route; nil on every
	// other route.
	Client *ClientInvocation `json:"client,omitempty"`
}

// AssistantAttemptPayload is the log-only record of a model call that did not
// produce the assistant/message: its partial message, if any, and the failure.
type AssistantAttemptPayload struct {
	Message       *vocab.Message    `json:"message,omitempty"`
	RequestAsOf   *Seq              `json:"request_as_of,omitempty"`
	RequestDigest string            `json:"request_digest,omitempty"`
	Usage         *vocab.Usage      `json:"usage,omitempty"`
	Timing        *Timing           `json:"timing,omitempty"`
	Failure       *vocab.Failure    `json:"failure,omitempty"`
	Stream        json.RawMessage   `json:"stream,omitempty"`
	ServedBy      string            `json:"served_by,omitempty"`
	Estimate      int               `json:"estimate,omitempty"`
	Client        *ClientInvocation `json:"client,omitempty"`
}

// ToolCallPayload is a tool call the model made; Arguments stay the raw string the
// model produced.
type ToolCallPayload struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Spill names where an over-large tool output was moved (area A07): Path is the
// spill store's id of the file.
type Spill struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// BlobRefs cites the spill file, "spill:<id>", for the session store's blob_refs
// rows (storage.md §6.2), so the collector keeps it while the event is logged.
func (p ToolResultPayload) BlobRefs() []string {
	if p.Spill == nil || p.Spill.Path == "" {
		return nil
	}
	return []string{"spill:" + p.Spill.Path}
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
	// Version 2 of turn/end writes a failure's retry_after as text ("2s").
	TurnEndType = mustCore(Spec[TurnEnd]{Name: "turn/end", Version: 2, NoSources: true,
		Upgrades: map[int]Upgrade{1: DurationsAsText("reason.failure.retry_after")},
		Validate: func(p TurnEnd) error {
			if p.Reason.Kind == "" {
				return errors.New("reason kind is required")
			}
			return nil
		}})
	StepStartType = mustCore(Spec[StepStart]{Name: "step/start", Version: 1, NoSources: true})
	StepEndType   = mustCore(Spec[StepEnd]{Name: "step/end", Version: 2, NoSources: true,
		Upgrades: map[int]Upgrade{1: upgradeAddedStepCause}})

	SystemMessage = mustCoreSurface(Spec[MessagePayload]{Name: "system/message", Version: 1,
		Validate: func(p MessagePayload) error { return needRole(p.Message, vocab.RoleSystem) }})
	DeveloperMessage = mustCoreSurface(Spec[MessagePayload]{Name: "developer/message", Version: 1,
		Validate: func(p MessagePayload) error { return needRole(p.Message, vocab.RoleDeveloper) }})
	UserMessage = mustCoreSurface(Spec[UserMessagePayload]{Name: "user/message", Version: 1,
		Validate: func(p UserMessagePayload) error { return needRole(p.Message, vocab.RoleUser) }})
	// Version 2 of both attempt events adds the optional served_by
	// (llm.StreamAround); version 3 adds the optional estimate (the token meter);
	// version 4 of assistant/attempt writes its failure's retry_after as text;
	// version 4 of assistant/message and 5 of assistant/attempt add the optional
	// client (the client invocation of a signed-in client route, DH-D-LLM-12).
	AssistantMessage = mustCoreSurface(Spec[AssistantMessagePayload]{Name: "assistant/message", Version: 4,
		Upgrades: map[int]Upgrade{1: upgradeAddedServedBy, 2: upgradeAddedEstimate, 3: upgradeAddedClient}, Cold: "stream", NoSources: true,
		Validate: func(p AssistantMessagePayload) error { return needRole(p.Message, vocab.RoleAssistant) }})
	AssistantAttempt = mustCore(Spec[AssistantAttemptPayload]{Name: "assistant/attempt", Version: 5, Cold: "stream",
		Upgrades: map[int]Upgrade{1: upgradeAddedServedBy, 2: upgradeAddedEstimate, 3: DurationsAsText("failure.retry_after"), 4: upgradeAddedClient}})

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

// upgradeAddedServedBy is the identity: version 2 only adds the optional
// served_by, and a version-1 payload, which has none, was served by the provider.
func upgradeAddedServedBy(raw json.RawMessage) (json.RawMessage, error) { return raw, nil }

// upgradeAddedEstimate is the identity: version 3 only adds the optional
// estimate, and a version-2 payload, which has none, left it unknown.
func upgradeAddedEstimate(raw json.RawMessage) (json.RawMessage, error) { return raw, nil }

// upgradeAddedClient is the identity: the new versions only add the optional
// client, and an older payload, which has none, was not on a client route.
func upgradeAddedClient(raw json.RawMessage) (json.RawMessage, error) { return raw, nil }

// upgradeAddedStepCause is the identity: version 2 of step/end only adds the
// optional cause, and a version-1 step, which has none, was not cancelled.
func upgradeAddedStepCause(raw json.RawMessage) (json.RawMessage, error) { return raw, nil }

// DurationsAsText is the upgrade of the persisted-format change that made a
// duration its text (plan 14.4.1, Rule 6): at each dotted path ("failure.
// retry_after", "policy.base_delay"), an integer count of nanoseconds becomes
// the text schema.Marshal writes for it ("2s"). Every other field is kept as it
// was; a path that is absent, crosses a value that is not an object (null, say),
// or already holds text is left alone, for the decode to judge. A number that is
// not whole nanoseconds fails the upgrade. Any declaring package whose payload
// holds a duration uses it for the version that changed the form.
func DurationsAsText(paths ...string) Upgrade {
	steps := make([][]string, len(paths))
	for i, p := range paths {
		steps[i] = strings.Split(p, ".")
	}
	return func(raw json.RawMessage) (json.RawMessage, error) {
		for i, s := range steps {
			var err error
			if raw, err = durationAsText(raw, s); err != nil {
				return nil, fmt.Errorf("%s: %w", paths[i], err)
			}
		}
		return raw, nil
	}
}

func durationAsText(raw json.RawMessage, steps []string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return raw, nil // not an object: nothing below it to convert
	}
	field, ok := m[steps[0]]
	if !ok {
		return raw, nil
	}
	var out json.RawMessage
	if len(steps) > 1 {
		sub, err := durationAsText(field, steps[1:])
		if err != nil {
			return nil, err
		}
		out = sub
	} else {
		var ns json.Number
		if len(field) > 0 && field[0] == '"' || json.Unmarshal(field, &ns) != nil {
			return raw, nil // already text, or not a number: the decode judges it
		}
		n, err := ns.Int64()
		if err != nil {
			return nil, fmt.Errorf("%s is not whole nanoseconds", ns)
		}
		if out, err = json.Marshal(time.Duration(n).String()); err != nil {
			return nil, err
		}
	}
	m[steps[0]] = out
	return json.Marshal(m)
}

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
