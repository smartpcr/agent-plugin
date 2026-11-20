package llm

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// part is one content block being assembled from its deltas.
type part struct {
	kind   vocab.BlockKind
	text   strings.Builder
	callID string
	name   string
	args   strings.Builder
	ended  bool
	block  vocab.Block // authoritative once ended
}

// assembler builds the assistant message from an attempt's chunks. A BlockEnd's
// block is authoritative and later deltas for its index are ignored.
type assembler struct {
	parts map[int]*part
	usage *vocab.Usage
}

func newAssembler() *assembler { return &assembler{parts: map[int]*part{}} }

func (a *assembler) at(i int, kind vocab.BlockKind) *part {
	p, ok := a.parts[i]
	if !ok {
		p = &part{kind: kind}
		a.parts[i] = p
	}
	if p.kind == "" {
		p.kind = kind
	}
	return p
}

// add folds one non-finish chunk in; content reports whether it carried model output.
func (a *assembler) add(c vocab.Chunk) (content bool) {
	switch c := c.(type) {
	case vocab.BlockStart:
		a.at(c.Index, c.Kind)
	case vocab.TextDelta:
		if p := a.at(c.Index, vocab.KindText); !p.ended && p.kind == vocab.KindText {
			p.text.WriteString(c.Text)
		}
		return c.Text != ""
	case vocab.ReasoningDelta:
		if p := a.at(c.Index, vocab.KindReasoning); !p.ended && p.kind == vocab.KindReasoning {
			p.text.WriteString(c.Text)
		}
		return c.Text != ""
	case vocab.ToolCallDelta:
		if p := a.at(c.Index, vocab.KindToolUse); !p.ended && p.kind == vocab.KindToolUse {
			if c.ID != "" {
				p.callID = c.ID
			}
			if c.Name != "" {
				p.name = c.Name
			}
			p.args.WriteString(c.ArgsDelta)
		}
		return true
	case vocab.BlockEnd:
		p := a.at(c.Index, c.Block.Kind)
		if !p.ended {
			p.ended, p.kind, p.block = true, c.Block.Kind, c.Block.Clone()
		}
		return true
	case vocab.UsageChunk:
		u := c.Usage
		a.usage = &u
	}
	return false
}

// blocks is the message content in index order. complete is true for a finish
// that ended the turn normally: unended text and reasoning are closed and an
// unended tool call is kept when its arguments parse. Otherwise (interrupted)
// text and reasoning are kept as a prefix and every tool call, ended or not, is
// dropped. dropCalls drops every tool call, ended or not (max_tokens: the call
// list may be cut short). ok is false when a kept tool call is malformed.
func (a *assembler) blocks(complete, dropCalls bool) (out []vocab.Block, ok bool) {
	idx := make([]int, 0, len(a.parts))
	for i := range a.parts {
		idx = append(idx, i)
	}
	slices.Sort(idx)
	for _, i := range idx {
		p := a.parts[i]
		if p.ended {
			if (dropCalls || !complete) && p.block.Kind == vocab.KindToolUse {
				continue
			}
			out = append(out, p.block)
			continue
		}
		switch p.kind {
		case vocab.KindText:
			if p.text.Len() > 0 {
				out = append(out, vocab.TextBlock(p.text.String()))
			}
		case vocab.KindReasoning:
			if p.text.Len() > 0 {
				out = append(out, vocab.ReasoningBlock(p.text.String(), "", false))
			}
		case vocab.KindToolUse:
			if !complete || dropCalls {
				continue
			}
			args := json.RawMessage(p.args.String())
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			var obj map[string]json.RawMessage
			if p.callID == "" || p.name == "" || json.Unmarshal(args, &obj) != nil || obj == nil {
				return nil, false
			}
			out = append(out, vocab.ToolUseBlock(p.callID, p.name, args))
		}
	}
	return out, true
}
