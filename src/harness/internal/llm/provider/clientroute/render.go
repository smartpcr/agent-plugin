package clientroute

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// Surface is what Render reads of a request: session.Request, or a request
// rebuilt from the log (surface.Built).
type Surface interface {
	Messages() ([]vocab.Message, error)
	Tools() []vocab.ToolSchema
}

// ImagePlaceholder is the text an image becomes: both client routes take text
// only (DH-D-LLM-12, Capabilities).
const ImagePlaceholder = "[image omitted: this route accepts text only]"

// RenderOptions shape the rendering per client.
type RenderOptions struct {
	// SystemInPrompt puts the system text at the head of the prompt instead of
	// System, for a client that cannot replace its own system prompt (Copilot).
	SystemInPrompt bool
}

// Rendered is a request as a client route hands it to the client.
type Rendered struct {
	// System is the system prompt file: every system and developer text block,
	// in order, separated by a blank line. Empty under SystemInPrompt.
	System []byte
	// Prompt is standard input.
	Prompt []byte
	// Tools is the tool schema file the listing server serves, a JSON array of
	// MCP tool entries ({name, description, inputSchema}) holding the canonical
	// schemas (DH-ADR-LLM-8: every keyword of the subset is sent to an Anthropic
	// or MCP consumer). Nil when the request declares no tool.
	Tools []byte
	// ToolNames are the declared tools' names, in request order.
	ToolNames []string
}

// transcriptLead opens a rendered conversation.
const transcriptLead = "The conversation so far is below, oldest first. Continue it: reply to its last turn as the assistant.\n\n"

// Render is a pure function of src: the same request renders to the same bytes,
// so a request rebuilt from the log renders to what was sent. A conversation
// that is one user message is that message's text; anything longer, with
// earlier assistant turns or tool results, is a transcript, because neither
// client can be given earlier assistant turns. Reasoning is not rendered (its
// signature cannot be replayed through a client). A system message carrying
// anything but text is llm/unsupported-content.
func Render(src Surface, o RenderOptions) (Rendered, error) {
	msgs, err := src.Messages()
	if err != nil {
		return Rendered{}, err
	}
	var system []string
	var turns []vocab.Message
	for _, m := range msgs {
		switch m.Role() {
		case vocab.RoleSystem, vocab.RoleDeveloper:
			for _, b := range m.Blocks() {
				if b.Kind != vocab.KindText {
					return Rendered{}, Fail(vocab.CodeUnsupportedContent, "clientroute: a %s message carries a %s block", m.Role(), b.Kind)
				}
				if b.Text.Text != "" {
					system = append(system, b.Text.Text)
				}
			}
		default:
			turns = append(turns, m)
		}
	}
	var out Rendered
	var prompt strings.Builder
	sys := strings.Join(system, "\n\n")
	if o.SystemInPrompt {
		if sys != "" {
			prompt.WriteString("<system>\n" + sys + "\n</system>\n\n")
		}
	} else {
		out.System = []byte(sys)
	}
	if len(turns) == 1 && turns[0].Role() == vocab.RoleUser && plain(turns[0]) {
		prompt.WriteString(blocksText(turns[0].Blocks()))
	} else {
		prompt.WriteString(transcriptLead)
		prompt.WriteString("<conversation>\n")
		for _, m := range turns {
			writeTurn(&prompt, m)
		}
		prompt.WriteString("</conversation>\n")
	}
	out.Prompt = []byte(prompt.String())
	tools := src.Tools()
	if len(tools) == 0 {
		return out, nil
	}
	type mcpTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	list := make([]mcpTool, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters
		if len(bytes.TrimSpace(params)) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		list = append(list, mcpTool{Name: t.Name, Description: t.Description, InputSchema: params})
		out.ToolNames = append(out.ToolNames, t.Name)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(list); err != nil {
		return Rendered{}, Fail(vocab.CodeInvalidRequest, "clientroute: the tool schemas do not encode: %v", err)
	}
	out.Tools = b.Bytes()
	return out, nil
}

// plain reports whether m holds only text and images.
func plain(m vocab.Message) bool {
	for _, b := range m.Blocks() {
		if b.Kind != vocab.KindText && b.Kind != vocab.KindImage {
			return false
		}
	}
	return true
}

// blocksText is the text and image placeholders of blocks, a blank line apart.
func blocksText(blocks []vocab.Block) string {
	var parts []string
	for _, b := range blocks {
		switch b.Kind {
		case vocab.KindText:
			if b.Text.Text != "" {
				parts = append(parts, b.Text.Text)
			}
		case vocab.KindImage:
			parts = append(parts, ImagePlaceholder)
		}
	}
	return strings.Join(parts, "\n\n")
}

// writeTurn renders one message of the conversation.
func writeTurn(w *strings.Builder, m vocab.Message) {
	role := string(m.Role())
	opened := false
	open := func() {
		if !opened {
			w.WriteString("<" + role + ">\n")
			opened = true
		}
	}
	closeTag := func() {
		if opened {
			w.WriteString("</" + role + ">\n")
			opened = false
		}
	}
	for _, b := range m.Blocks() {
		switch b.Kind {
		case vocab.KindText:
			if b.Text.Text != "" {
				open()
				w.WriteString(b.Text.Text + "\n")
			}
		case vocab.KindImage:
			open()
			w.WriteString(ImagePlaceholder + "\n")
		case vocab.KindToolUse:
			open()
			args := strings.TrimSpace(string(b.ToolUse.Args))
			if args == "" {
				args = "{}"
			}
			fmt.Fprintf(w, "<tool_call id=%q name=%q>%s</tool_call>\n", b.ToolUse.ID, b.ToolUse.Name, args)
		case vocab.KindToolResult:
			closeTag()
			r := b.ToolResult
			errAttr := ""
			if r.IsError {
				errAttr = ` error="true"`
			}
			fmt.Fprintf(w, "<tool_result id=%q%s>\n", r.CallID, errAttr)
			for _, p := range r.Parts {
				switch {
				case p.Text != nil:
					w.WriteString(p.Text.Text + "\n")
				case p.Image != nil:
					w.WriteString(ImagePlaceholder + "\n")
				}
			}
			w.WriteString("</tool_result>\n")
		}
	}
	closeTag()
}
