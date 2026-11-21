package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// Version is the anthropic-version header value the driver speaks.
const Version = "2023-06-01"

// Surface is what Encode reads: a session.Request, or the surface.Built that
// surface.Rebuild reconstructs at the same position.
type Surface interface {
	Messages() ([]vocab.Message, error)
	Tools() []vocab.ToolSchema
}

// ThinkingBudget is the budget_tokens sent for an effort.
var ThinkingBudget = map[string]int{"low": 1024, "medium": 4096, "high": 16384}

// Encoded is a request body and what the encoder noticed rendering it.
type Encoded struct {
	Body []byte
	// Diagnostics name lossy renderings, e.g. a system message after the first
	// non-system message, which the dialect can only carry in the leading system.
	Diagnostics []string
}

type request struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        []content       `json:"system,omitempty"`
	Messages      []message       `json:"messages"`
	Tools         []tool          `json:"tools,omitempty"`
	ToolChoice    *toolChoice     `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Thinking      *thinkingConfig `json:"thinking,omitempty"`
	Stream        bool            `json:"stream"`
}

type message struct {
	Role    string    `json:"role"`
	Content []content `json:"content"`
}

// content is one Messages content block; Type selects which fields are set.
type content struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   []content       `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

func str(s string) *string { return &s }

// Encode renders src for model under cfg. The rendering is deterministic: the
// same surface, model and config give the same bytes, so a request rebuilt from
// the log re-encodes to the bytes that were sent. System and developer messages
// become the leading system field; tool messages become user tool_result blocks;
// consecutive messages of one role merge. An image, which this route does not
// accept, is llm/unsupported-content; a zero max_tokens is llm/invalid-request.
func Encode(src Surface, model string, cfg vocab.CallConfig) (Encoded, error) {
	var out Encoded
	c := cfg.Controls
	if c.MaxOutputTokens <= 0 {
		return out, failf(vocab.CodeInvalidRequest, "anthropic: max_tokens is required and must be positive")
	}
	msgs, err := src.Messages()
	if err != nil {
		return out, err
	}
	req := request{Model: model, MaxTokens: c.MaxOutputTokens, Messages: []message{}, Stream: true,
		Temperature: c.Temperature, TopP: c.TopP, StopSequences: c.Stop}
	seenTurn := false
	for i, m := range msgs {
		switch m.Role() {
		case vocab.RoleSystem, vocab.RoleDeveloper:
			if seenTurn {
				out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("llm-deepseek.4: %s message %d follows the conversation and is sent in the leading system prompt", m.Role(), i))
			}
			for _, b := range m.Blocks() {
				if b.Kind != vocab.KindText {
					return out, failf(vocab.CodeUnsupportedContent, "anthropic: a %s message carries a %s block", m.Role(), b.Kind)
				}
				if b.Text.Text != "" {
					req.System = append(req.System, content{Type: "text", Text: str(b.Text.Text)})
				}
			}
			continue
		}
		seenTurn = true
		role := "user"
		if m.Role() == vocab.RoleAssistant {
			role = "assistant"
		}
		var blocks []content
		for _, b := range m.Blocks() {
			cb, keep, err := block(b)
			if err != nil {
				return out, err
			}
			if keep {
				blocks = append(blocks, cb)
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == role {
			req.Messages[n-1].Content = append(req.Messages[n-1].Content, blocks...)
			continue
		}
		req.Messages = append(req.Messages, message{Role: role, Content: blocks})
	}
	for _, t := range src.Tools() {
		req.Tools = append(req.Tools, tool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters})
	}
	switch c.ToolChoice.Mode {
	case vocab.ToolChoiceAuto:
		req.ToolChoice = &toolChoice{Type: "auto"}
	case vocab.ToolChoiceRequired:
		req.ToolChoice = &toolChoice{Type: "any"}
	case vocab.ToolChoiceTool:
		req.ToolChoice = &toolChoice{Type: "tool", Name: c.ToolChoice.Name}
	case vocab.ToolChoiceNone:
		req.ToolChoice = &toolChoice{Type: "none"}
	}
	if c.Effort != "" {
		budget, ok := ThinkingBudget[c.Effort]
		if !ok {
			return out, failf(vocab.CodeUnsupportedReasoningEffort, "anthropic: effort %q has no thinking budget", c.Effort)
		}
		req.Thinking = &thinkingConfig{Type: "enabled", BudgetTokens: budget}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return out, failf(vocab.CodeInvalidRequest, "anthropic: the request does not encode: %v", err)
	}
	out.Body = bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return out, nil
}

func block(b vocab.Block) (content, bool, error) {
	switch b.Kind {
	case vocab.KindText:
		if b.Text.Text == "" {
			return content{}, false, nil
		}
		return content{Type: "text", Text: str(b.Text.Text)}, true, nil
	case vocab.KindReasoning:
		r := b.Reasoning
		if r.Redacted {
			return content{Type: "redacted_thinking", Data: r.Signature}, true, nil
		}
		if r.Text == "" && r.Signature == "" {
			return content{}, false, nil
		}
		return content{Type: "thinking", Thinking: str(r.Text), Signature: r.Signature}, true, nil
	case vocab.KindToolUse:
		// The Messages API requires tool_use.input, so a call without arguments sends {}.
		in := b.ToolUse.Args
		if len(bytes.TrimSpace(in)) == 0 {
			in = json.RawMessage("{}")
		}
		return content{Type: "tool_use", ID: b.ToolUse.ID, Name: b.ToolUse.Name, Input: in}, true, nil
	case vocab.KindToolResult:
		r := b.ToolResult
		parts := []content{}
		var text []string
		for _, p := range r.Parts {
			if p.Image != nil {
				return content{}, false, failf(vocab.CodeUnsupportedContent, "anthropic: tool result %s carries an image", r.CallID)
			}
			if p.Text != nil {
				text = append(text, p.Text.Text)
			}
		}
		if s := strings.Join(text, "\n"); s != "" {
			parts = append(parts, content{Type: "text", Text: str(s)})
		}
		return content{Type: "tool_result", ToolUseID: r.CallID, Content: parts, IsError: r.IsError}, true, nil
	case vocab.KindImage:
		return content{}, false, failf(vocab.CodeUnsupportedContent, "anthropic: this route does not accept images")
	}
	return content{}, false, failf(vocab.CodeUnsupportedContent, "anthropic: block kind %q", b.Kind)
}

func failf(code vocab.Code, format string, args ...any) *vocab.Failure {
	f, err := vocab.NewFailure(code, fmt.Sprintf(format, args...))
	if err != nil {
		panic(err)
	}
	return f
}
