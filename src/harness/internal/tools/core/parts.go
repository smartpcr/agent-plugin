package core

import (
	"fmt"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// A result with images (a tools.Content value) is carried as its parts from the
// body to the log: the shapers and the spill seat see them, the seat prices and
// bounds the images with the text, and the tool/result message holds them in
// order. Result.Text is always the text of the parts. A result whose parts hold
// no image is text-only, logged exactly as a string result is.

// truncMarker is what the ceiling appends to a cut result.
const truncMarker = "\n[truncated: %d of %d bytes omitted]"

// textOf is the text of parts in order, images left out.
func textOf(parts []vocab.Part) string {
	n := 0
	for _, p := range parts {
		if p.Text != nil {
			n += len(p.Text.Text)
		}
	}
	b := make([]byte, 0, n)
	for _, p := range parts {
		if p.Text != nil {
			b = append(b, p.Text.Text...)
		}
	}
	return string(b)
}

// imageCount is how many image parts parts holds.
func imageCount(parts []vocab.Part) int {
	n := 0
	for _, p := range parts {
		if p.Image != nil {
			n++
		}
	}
	return n
}

func hasImage(parts []vocab.Part) bool { return imageCount(parts) > 0 }

// appendText adds s to parts, joined to a text part that ends them.
func appendText(parts []vocab.Part, s string) []vocab.Part {
	if s == "" {
		return parts
	}
	if n := len(parts); n > 0 && parts[n-1].Text != nil {
		parts[n-1] = vocab.TextPart(parts[n-1].Text.Text + s)
		return parts
	}
	return append(parts, vocab.TextPart(s))
}

// content is a body's value as model-facing content: a tools.Content's text and,
// when it holds an image, its parts; msg says why a part is not valid.
func content(v any) (text string, parts []vocab.Part, msg string, ok bool) {
	var c tools.Content
	switch x := v.(type) {
	case tools.Content:
		c = x
	case *tools.Content:
		if x == nil {
			return "", nil, "", false
		}
		c = *x
	default:
		return "", nil, "", false
	}
	for i, p := range c.Parts {
		if err := p.Validate(); err != nil {
			return "", nil, fmt.Sprintf("part %d: %v", i, err), true
		}
	}
	parts = tools.CloneParts(c.Parts)
	text = textOf(parts)
	if !hasImage(parts) {
		parts = nil
	}
	return text, parts, "", true
}

// applySpill takes the spill seat's Replace answer sr, from seat id, into res;
// it returns the failure when the answer cannot be taken: a part that is not
// valid, or a result the seat grew (more text, or more images).
func (r *registry) applySpill(res *tools.Result, id string, sr tools.SpillResult) string {
	if res.Parts != nil && sr.Parts != nil {
		for i, p := range sr.Parts {
			if err := p.Validate(); err != nil {
				return fmt.Sprintf("the spill seat %s returned an invalid part %d: %v", id, i, err)
			}
		}
		text := textOf(sr.Parts)
		if len(text) > len(res.Text) || imageCount(sr.Parts) > imageCount(res.Parts) {
			return fmt.Sprintf("the spill seat %s grew the result from %d bytes and %d images to %d bytes and %d images",
				id, len(res.Text), imageCount(res.Parts), len(text), imageCount(sr.Parts))
		}
		res.Text, res.Parts = text, tools.CloneParts(sr.Parts)
	} else {
		if len(sr.Text) > len(res.Text) {
			return fmt.Sprintf("the spill seat %s grew the text from %d to %d bytes", id, len(res.Text), len(sr.Text))
		}
		res.Text, res.Parts = sr.Text, nil
	}
	if sr.Ref != nil {
		ref := *sr.Ref
		res.Spill = &ref
	}
	return ""
}

// addNotice appends notice to res within the ceiling, cutting the content rather
// than the notice; it reports whether the content was cut.
func (r *registry) addNotice(res *tools.Result, notice string) bool {
	limit := r.cfg.MaxResultBytes
	if res.Parts == nil || len(notice) >= limit {
		var cut bool
		res.Text, cut = withNotice(res.Text, notice, limit)
		res.Parts = nil
		return cut
	}
	parts, cut := ceilingParts(res.Parts, limit-len(notice))
	parts = appendText(parts, notice)
	res.Parts, res.Text = parts, textOf(parts)
	return cut
}

// bound applies the registry's ceiling to res; it reports whether it cut.
func (r *registry) bound(res *tools.Result) bool {
	if res.Parts == nil {
		var cut bool
		res.Text, cut = ceiling(res.Text, r.cfg.MaxResultBytes)
		return cut
	}
	parts, cut := ceilingParts(res.Parts, r.cfg.MaxResultBytes)
	res.Parts, res.Text = parts, textOf(parts)
	return cut
}

// ceilingParts cuts parts so their text holds at most limit bytes with the
// ceiling's marker: the parts are kept in order up to the text that overflows,
// which is cut at a rune boundary, and every later part, an image included, is
// left out. An image is never split. A limit too small for the marker keeps the
// cut text alone, as ceiling does.
func ceilingParts(parts []vocab.Part, limit int) ([]vocab.Part, bool) {
	total := len(textOf(parts))
	if total <= limit {
		return parts, false
	}
	room := limit - len(fmt.Sprintf(truncMarker, total, total))
	if room < 0 {
		text, _ := ceiling(textOf(parts), limit)
		return []vocab.Part{vocab.TextPart(text)}, true
	}
	out := make([]vocab.Part, 0, len(parts)+1)
	kept := 0
	for _, p := range parts {
		if p.Image != nil {
			out = append(out, vocab.ImagePart(p.Image.MediaType, p.Image.Ref))
			continue
		}
		if p.Text == nil {
			continue
		}
		t := p.Text.Text
		if kept+len(t) <= room {
			out = appendText(out, t)
			kept += len(t)
			continue
		}
		keep := room - kept
		for keep > 0 && !utf8.RuneStart(t[keep]) {
			keep--
		}
		out = appendText(out, t[:keep])
		kept += keep
		break
	}
	return appendText(out, fmt.Sprintf(truncMarker, total-kept, total)), true
}
