package vocab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// BlockKind names a block's variant; the set is closed.
type BlockKind string

// The block kinds. Audio and video have no block by decision (DH-ADR-LLM-1).
const (
	KindText       BlockKind = "text"
	KindReasoning  BlockKind = "reasoning"
	KindToolUse    BlockKind = "tool_use"
	KindToolResult BlockKind = "tool_result"
	KindImage      BlockKind = "image"
)

// Block is one piece of a message: Kind names the variant and exactly that
// variant's field is set. A struct rather than an interface so that the shape
// has a schema and a fingerprint.
type Block struct {
	Kind       BlockKind   `json:"kind" dsh:"enum=text|reasoning|tool_use|tool_result|image"`
	Text       *Text       `json:"text,omitempty"`
	Reasoning  *Reasoning  `json:"reasoning,omitempty"`
	ToolUse    *ToolUse    `json:"tool_use,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
	Image      *Image      `json:"image,omitempty"`
}

// Text is plain text.
type Text struct {
	Text string `json:"text"`
}

// Reasoning is the model's reasoning. Signature is the provider's opaque proof
// that lets it be sent back; Redacted reasoning carries only that proof.
type Reasoning struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
}

// ToolUse is a tool call the model made. Args is a JSON object, checked once
// when the block is complete.
type ToolUse struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolResult answers the tool call CallID.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Parts   []Part `json:"parts"`
	IsError bool   `json:"is_error,omitempty"`
}

// Part is one piece of a tool result: text or an image.
type Part struct {
	Text  *Text  `json:"text,omitempty"`
	Image *Image `json:"image,omitempty"`
}

// Image is a stored image, named by its attachment reference; the bytes live
// in the attachment store (area A07), never in the vocabulary.
type Image struct {
	MediaType string `json:"media_type" dsh:"enum=image/png|image/jpeg|image/gif|image/webp"`
	Ref       string `json:"ref"`
}

var mediaTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// TextBlock is a text block.
func TextBlock(s string) Block { return Block{Kind: KindText, Text: &Text{Text: s}} }

// ReasoningBlock is a reasoning block.
func ReasoningBlock(text, signature string, redacted bool) Block {
	return Block{Kind: KindReasoning, Reasoning: &Reasoning{Text: text, Signature: signature, Redacted: redacted}}
}

// ToolUseBlock is a tool call; args is copied.
func ToolUseBlock(id, name string, args json.RawMessage) Block {
	return Block{Kind: KindToolUse, ToolUse: &ToolUse{ID: id, Name: name, Args: bytes.Clone(args)}}
}

// ToolResultBlock answers a tool call; parts are copied, and no parts is an
// empty list, never a missing one.
func ToolResultBlock(callID string, isError bool, parts ...Part) Block {
	if parts == nil {
		parts = []Part{}
	}
	b := Block{Kind: KindToolResult, ToolResult: &ToolResult{CallID: callID, Parts: parts, IsError: isError}}
	return b.Clone()
}

// ImageBlock is an image block.
func ImageBlock(mediaType, ref string) Block {
	return Block{Kind: KindImage, Image: &Image{MediaType: mediaType, Ref: ref}}
}

// TextPart and ImagePart are tool-result parts.
func TextPart(s string) Part { return Part{Text: &Text{Text: s}} }

// ImagePart is an image tool-result part.
func ImagePart(mediaType, ref string) Part {
	return Part{Image: &Image{MediaType: mediaType, Ref: ref}}
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (p Part) clone() Part { return Part{Text: clonePtr(p.Text), Image: clonePtr(p.Image)} }

// Clone is a deep copy: nothing in the copy aliases b.
func (b Block) Clone() Block {
	out := Block{Kind: b.Kind, Text: clonePtr(b.Text), Reasoning: clonePtr(b.Reasoning), Image: clonePtr(b.Image)}
	if b.ToolUse != nil {
		u := *b.ToolUse
		u.Args = bytes.Clone(u.Args)
		out.ToolUse = &u
	}
	if b.ToolResult != nil {
		r := *b.ToolResult
		// Keep nil distinct from empty so Validate can refuse a missing list.
		if b.ToolResult.Parts != nil {
			r.Parts = make([]Part, len(b.ToolResult.Parts))
			for i, p := range b.ToolResult.Parts {
				r.Parts[i] = p.clone()
			}
		}
		out.ToolResult = &r
	}
	return out
}

// Validate checks that exactly the field Kind names is set and that the
// variant is well formed.
func (b Block) Validate() error {
	set := 0
	for _, p := range []bool{b.Text != nil, b.Reasoning != nil, b.ToolUse != nil, b.ToolResult != nil, b.Image != nil} {
		if p {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("vocab: a %q block must set exactly one variant, found %d", b.Kind, set)
	}
	switch b.Kind {
	case KindText:
		if b.Text == nil {
			return errors.New("vocab: a text block has no text")
		}
	case KindReasoning:
		if b.Reasoning == nil {
			return errors.New("vocab: a reasoning block has no reasoning")
		}
		if b.Reasoning.Redacted && b.Reasoning.Signature == "" {
			return errors.New("vocab: redacted reasoning needs its signature")
		}
	case KindToolUse:
		u := b.ToolUse
		if u == nil || u.ID == "" || u.Name == "" {
			return errors.New("vocab: a tool call needs an id and a name")
		}
		if !isObject(u.Args) {
			return fmt.Errorf("vocab: the arguments of tool call %s are not a JSON object", u.ID)
		}
	case KindToolResult:
		r := b.ToolResult
		if r == nil || r.CallID == "" {
			return errors.New("vocab: a tool result needs the id of its call")
		}
		if r.Parts == nil {
			return fmt.Errorf("vocab: tool result %s needs its parts list", r.CallID)
		}
		for i, p := range r.Parts {
			if err := p.Validate(); err != nil {
				return fmt.Errorf("vocab: tool result %s part %d: %w", r.CallID, i, err)
			}
		}
	case KindImage:
		if b.Image == nil {
			return errors.New("vocab: an image block has no image")
		}
		return b.Image.Validate()
	default:
		return fmt.Errorf("vocab: block kind %q is not one of the closed set", b.Kind)
	}
	return nil
}

// Validate checks that exactly one of text or image is set.
func (p Part) Validate() error {
	switch {
	case (p.Text == nil) == (p.Image == nil):
		return errors.New("a part sets exactly one of text and image")
	case p.Image != nil:
		return p.Image.Validate()
	}
	return nil
}

// Validate checks the media type and the reference.
func (i Image) Validate() error {
	if !slices.Contains(mediaTypes, i.MediaType) {
		return fmt.Errorf("vocab: image media type %q is not one of %s", i.MediaType, strings.Join(mediaTypes, ", "))
	}
	if i.Ref == "" {
		return errors.New("vocab: an image needs its attachment reference")
	}
	return nil
}

func isObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m != nil
}
