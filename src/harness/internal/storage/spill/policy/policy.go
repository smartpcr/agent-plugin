// Package policy is the spill policy, storage.spill.policy (DH-D-STORAGE-6,
// DH-ADR-STORAGE-4, docs/harness/areas/storage.md): the tool registry's spill
// seat, which keeps every tool result within the budget. A result over the budget
// is saved whole through the spill store and the model gets its head and tail
// and a notice naming the saved file; a failed save still bounds the result and
// the notice says it was not saved. The budget is MaxInlineTokens priced by the
// shared token estimate (DH-D-LLM-6), which a load-time check keeps within the
// registry's maxResultBytes, so no result over either reaches a model.
//
// A result is a sequence of parts, text and images (ShapeParts). An image is
// priced by the route's calculator (Config.PriceImage) or, without one, by
// Config.ImageTokenFallback, and is kept or omitted whole; the saved text holds
// an image line (ImageLine) at each image's position.
package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// SeatID names the policy in the registry's spill seat.
const SeatID = "storage.spill.policy"

// Defaults and bounds of Config (storage.md sec. 6.2, spill.PolicyConfig).
const (
	DefaultMaxInlineTokens = 12500
	MinInlineTokens        = 512
	DefaultKeep            = 720 * time.Hour
	// DefaultImageTokenFallback is what an image costs when no route calculator
	// prices it (storage.md sec. 6.2, spill.PolicyConfig).
	DefaultImageTokenFallback = 1500
	// MaxReasonBytes bounds the failure reason a notice quotes.
	MaxReasonBytes = 200
)

// separator joins the head and the tail of a preview.
const separator = "\n[...]\n"

// probeCount is the largest byte or image count a notice is sized for at load.
const probeCount = int64(1) << 40

// ImagePricer is a model route's image calculator (DH-D-LLM-6): the tokens img
// costs on the route the call's agent uses. ok false (the route does not price
// images) falls back to Config.ImageTokenFallback, as do a negative price and a
// panic.
type ImagePricer func(ctx context.Context, c tools.Call, img vocab.Image) (tokens int, ok bool)

// Config configures the policy. It has no "off" value.
type Config struct {
	// MaxInlineTokens is the most a result may cost inline; zero is
	// DefaultMaxInlineTokens, and less than MinInlineTokens is refused.
	MaxInlineTokens int
	// ImageTokenFallback is an image's price when PriceImage is nil or gives
	// none; zero is DefaultImageTokenFallback, and a negative value is refused.
	ImageTokenFallback int
	// PriceImage is the route's image calculator; nil prices every image at
	// ImageTokenFallback.
	PriceImage ImagePricer
	// ReadTool names the tool that reads a saved file back (spill_read) when it
	// is mounted: the notice then cites the file's id and names the tool, rather
	// than a path the file tools would read. "" cites the path.
	ReadTool string
	// Keep is how long the notice says the saved file is kept; zero is
	// DefaultKeep (the spill store's retention).
	Keep time.Duration
	// Now is the clock of the notice's date; nil is time.Now.
	Now func() time.Time
}

// Validate refuses a config that cannot bound a result.
func (c Config) Validate() error {
	if c.MaxInlineTokens != 0 && c.MaxInlineTokens < MinInlineTokens {
		return fmt.Errorf("spill policy: MaxInlineTokens %d is below the minimum of %d", c.MaxInlineTokens, MinInlineTokens)
	}
	if c.ImageTokenFallback < 0 {
		return fmt.Errorf("spill policy: ImageTokenFallback %d is negative", c.ImageTokenFallback)
	}
	if c.Keep < 0 {
		return fmt.Errorf("spill policy: Keep %s is negative", c.Keep)
	}
	return nil
}

func (c Config) normalized() Config {
	if c.MaxInlineTokens == 0 {
		c.MaxInlineTokens = DefaultMaxInlineTokens
	}
	if c.ImageTokenFallback == 0 {
		c.ImageTokenFallback = DefaultImageTokenFallback
	}
	if c.Keep == 0 {
		c.Keep = DefaultKeep
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Policy is the spill seat over one spill store.
type Policy struct {
	store  spill.Store
	cfg    Config
	budget int
}

// New is the policy over store for a registry whose ceiling is maxResultBytes
// (zero for no ceiling to check). It refuses a config below the minimum, a
// budget the ceiling would cut, and a budget too small for its worst-case notice
// and a preview.
func New(store spill.Store, cfg Config, maxResultBytes int) (*Policy, error) {
	if store == nil {
		return nil, errors.New("spill policy: a spill store is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.normalized()
	budget := cfg.MaxInlineTokens * meter.CharsPerToken
	if maxResultBytes > 0 && budget > maxResultBytes {
		return nil, fmt.Errorf("spill policy: MaxInlineTokens %d (%d bytes) exceeds the registry's maxResultBytes %d",
			cfg.MaxInlineTokens, budget, maxResultBytes)
	}
	p := &Policy{store: store, cfg: cfg, budget: budget}
	probe := store.Reserve("spill-policy-probe", strings.Repeat("x", spill.MaxNameLen))
	if need := p.worstNotice(p.path(probe.ID), probeCount, probeCount) + len(separator) + 2*utf8.UTFMax; need > p.budget {
		return nil, fmt.Errorf("spill policy: a budget of %d bytes cannot hold its notice of up to %d bytes and a preview", p.budget, need)
	}
	return p, nil
}

// Budget is the most bytes a result the policy leaves may hold.
func (p *Policy) Budget() int { return p.budget }

// Mount files the policy in svc's spill seat for ctx's scope.
func Mount(ctx *cordis.Context, svc tools.Service, store spill.Store, cfg Config) (*Policy, *cordis.EffectHandle, error) {
	p, err := New(store, cfg, svc.Limits().MaxResultBytes)
	if err != nil {
		return nil, nil, err
	}
	h, err := svc.SetSpill(ctx, SeatID, p.Shape)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// Shape is the tools.SpillShaper: the result's text is one text part shaped by
// ShapeParts. A text within the budget is kept; a longer one is saved whole for
// the call's session and replaced by its head, its tail and the notice. (The
// pipeline's Outcome carries text only; a result with images goes through
// ShapeParts.)
func (p *Policy) Shape(ctx context.Context, c tools.Call, o tools.Outcome) tools.SpillResult {
	r := p.ShapeParts(ctx, c, []vocab.Part{vocab.TextPart(o.Text)})
	if !r.Replace {
		return tools.SpillResult{}
	}
	return tools.SpillResult{Replace: true, Text: TextOf(r.Parts), Ref: r.Ref}
}

// Shaped is ShapeParts' answer. With Replace unset the parts are kept.
type Shaped struct {
	Replace bool
	// Parts is the bounded result in the original order: the head's parts, the
	// "[...]" separator, the tail's parts and the notice, adjacent texts joined.
	Parts []vocab.Part
	// Ref is the saved full result; nil when the save failed (the notice says
	// so).
	Ref *event.Spill
	// ImagesOmitted is how many of the result's images the parts leave out.
	ImagesOmitted int
}

// item is one part of a result with its cost in budget bytes: a text's length,
// or an image's price times meter.CharsPerToken.
type item struct {
	text string
	img  *vocab.Image
	cost int
}

// ShapeParts bounds a result of text and image parts. Each image is priced by
// the route's calculator, or by the fallback, and costs its tokens times
// meter.CharsPerToken budget bytes; a text costs its length. Within the budget
// the parts are kept. Over it, the head takes up to half of what the notice
// leaves and the tail the rest, in order: a text may be cut at a rune boundary,
// an image is kept only whole, and the head stops at the first part it cannot
// take whole. The full text is saved with an image line at each image's
// position, and the notice says how many bytes and images were left out. A kept
// result's text bytes plus its images' budget bytes never exceed Budget, so its
// estimated tokens never exceed MaxInlineTokens.
func (p *Policy) ShapeParts(ctx context.Context, c tools.Call, parts []vocab.Part) Shaped {
	items, total, images, cost, tokens := p.price(ctx, c, parts)
	if cost <= p.budget && tokens <= p.cfg.MaxInlineTokens {
		return Shaped{}
	}
	owner := string(c.Agent().Session)
	var ref spill.Ref
	path := ""
	if owner != "" {
		ref = p.store.Reserve(owner, c.Name()+".txt")
		path = p.path(ref.ID)
	}
	// The notice is sized for its worst case before anything is saved, so a
	// saved file is never orphaned by a notice that does not fit.
	room := p.budget - p.worstNotice(path, total, int64(images)) - len(separator)
	head, tail := cut(items, room)
	var kept, keptImages int64
	out := make([]vocab.Part, 0, len(head)+len(tail)+2)
	add := func(it item) {
		if it.img != nil {
			keptImages++
			out = append(out, vocab.Part{Image: &vocab.Image{MediaType: it.img.MediaType, Ref: it.img.Ref}})
			return
		}
		kept += int64(len(it.text))
		out = appendText(out, it.text)
	}
	for _, it := range head {
		add(it)
	}
	out = appendText(out, separator)
	for _, it := range tail {
		add(it)
	}
	omitted, omittedImages := total-kept, int64(images)-keptImages
	r := Shaped{Replace: true, ImagesOmitted: int(omittedImages)}

	var notice string
	if owner == "" {
		notice = failNotice(omitted, total, omittedImages, int64(images), "the call has no session to own the saved file")
	} else if saved, err := p.save(ctx, ref, savedText(parts)); err != nil {
		notice = failNotice(omitted, total, omittedImages, int64(images), err.Error())
	} else {
		notice = p.savedNotice(omitted, total, omittedImages, int64(images), path)
		r.Ref = &event.Spill{Path: ref.ID, Bytes: saved}
	}
	r.Parts = appendText(out, notice)
	return r
}

// price is the result's items, its text bytes, its image count, its cost in
// budget bytes and its estimated tokens.
func (p *Policy) price(ctx context.Context, c tools.Call, parts []vocab.Part) (items []item, total int64, images, cost, tokens int) {
	items = make([]item, 0, len(parts))
	runes := 0
	for _, pt := range parts {
		switch {
		case pt.Image != nil:
			n := p.imageTokens(ctx, c, *pt.Image)
			items = append(items, item{img: pt.Image, cost: n * meter.CharsPerToken})
			images++
			tokens += n
			cost += n * meter.CharsPerToken
		case pt.Text != nil && pt.Text.Text != "":
			items = append(items, item{text: pt.Text.Text, cost: len(pt.Text.Text)})
			total += int64(len(pt.Text.Text))
			runes += utf8.RuneCountInString(pt.Text.Text)
			cost += len(pt.Text.Text)
		}
	}
	// The texts are priced as one, as the model will see them.
	tokens += (runes + meter.CharsPerToken - 1) / meter.CharsPerToken
	return items, total, images, cost, tokens
}

// imageTokens is img's price: the route calculator's, or the fallback when there
// is none, it gives none, it is negative or the calculator panics.
func (p *Policy) imageTokens(ctx context.Context, c tools.Call, img vocab.Image) (n int) {
	if p.cfg.PriceImage == nil {
		return p.cfg.ImageTokenFallback
	}
	defer func() {
		if recover() != nil {
			n = p.cfg.ImageTokenFallback
		}
	}()
	if t, ok := p.cfg.PriceImage(ctx, c, img); ok && t >= 0 {
		return t
	}
	return p.cfg.ImageTokenFallback
}

// cut splits room between the head and the tail of items: the head takes up to
// room/2 from the front and stops at the first item it cannot take whole (a text
// is cut at a rune boundary, an image left out); the tail takes the rest of room
// from the back, down to what the head left of that item. The tail is returned in
// order.
func cut(items []item, room int) (head, tail []item) {
	headRoom, used := room/2, 0
	i, taken := 0, 0
	for ; i < len(items); i++ {
		it := items[i]
		if used+it.cost <= headRoom {
			head = append(head, it)
			used += it.cost
			continue
		}
		if it.img == nil {
			if h := string(spill.Head([]byte(it.text), headRoom-used)); h != "" {
				head = append(head, item{text: h, cost: len(h)})
				used += len(h)
				taken = len(h)
			}
		}
		break
	}
	tailRoom, tused := room-used, 0
	for j := len(items) - 1; j >= i; j-- {
		it := items[j]
		if j == i && it.img == nil {
			it.text = it.text[taken:]
			it.cost = len(it.text)
		}
		if tused+it.cost <= tailRoom {
			tail = append(tail, it)
			tused += it.cost
			continue
		}
		if it.img == nil {
			if t := tailOf(it.text, tailRoom-tused); t != "" {
				tail = append(tail, item{text: t, cost: len(t)})
			}
		}
		break
	}
	for l, r := 0, len(tail)-1; l < r; l, r = l+1, r-1 {
		tail[l], tail[r] = tail[r], tail[l]
	}
	return head, tail
}

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

// TextOf is the text of parts, images left out.
func TextOf(parts []vocab.Part) string {
	if len(parts) == 1 && parts[0].Text != nil {
		return parts[0].Text.Text
	}
	var b strings.Builder
	for _, pt := range parts {
		if pt.Text != nil {
			b.WriteString(pt.Text.Text)
		}
	}
	return b.String()
}

// ImageLine is the line the saved text holds at an image's position: the
// image's media type and attachment reference, on a line of its own.
func ImageLine(img vocab.Image) string {
	return "[image " + oneLine(img.MediaType) + " " + oneLine(img.Ref) + "]\n"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), "_") }

// savedText is the full result as saved: the texts in order, with ImageLine at
// each image, which starts on a line of its own.
func savedText(parts []vocab.Part) string {
	if len(parts) == 1 && parts[0].Text != nil {
		return parts[0].Text.Text
	}
	var b strings.Builder
	for _, pt := range parts {
		switch {
		case pt.Image != nil:
			if s := b.String(); s != "" && s[len(s)-1] != '\n' {
				b.WriteByte('\n')
			}
			b.WriteString(ImageLine(*pt.Image))
		case pt.Text != nil:
			b.WriteString(pt.Text.Text)
		}
	}
	return b.String()
}

// save writes text to ref's file and returns its size.
func (p *Policy) save(ctx context.Context, ref spill.Ref, text string) (int64, error) {
	w, err := p.store.Create(ctx, ref, int64(len(text)))
	if err != nil {
		return 0, err
	}
	if _, err := w.Write([]byte(text)); err != nil {
		_ = w.Abort()
		return 0, err
	}
	if err := w.Close(); err != nil {
		_ = w.Abort()
		return 0, err
	}
	return int64(len(text)), nil
}

// path is what the notice cites: the id when the read tool is named, else the
// host path the file tools can read, or the id when the store cannot map it.
func (p *Policy) path(id string) string {
	if p.cfg.ReadTool != "" {
		return id
	}
	if hp, ok := p.store.HostPath(id); ok {
		return hp
	}
	return id
}

// omittedPhrase is what a notice says was left out: bytes, and images when the
// result had any.
func omittedPhrase(omitted, total, omittedImages, images int64) string {
	s := fmt.Sprintf("Omitted %d of %d bytes", omitted, total)
	if images > 0 {
		s += fmt.Sprintf(" and %d of %d images", omittedImages, images)
	}
	return s
}

func (p *Policy) savedNotice(omitted, total, omittedImages, images int64, path string) string {
	until := p.cfg.Now().Add(p.cfg.Keep).UTC().Format("2006-01-02")
	hint := "Use read with offset and limit, or grep this path."
	if p.cfg.ReadTool != "" {
		hint = "Use " + p.cfg.ReadTool + " with this ref and an offset and limit, or a pattern."
	}
	return fmt.Sprintf("\n(%s. Full result saved to %s, kept until %s. %s)",
		omittedPhrase(omitted, total, omittedImages, images), path, until, hint)
}

func failNotice(omitted, total, omittedImages, images int64, reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > MaxReasonBytes {
		reason = string(spill.Head([]byte(reason), MaxReasonBytes-3)) + "..."
	}
	return fmt.Sprintf("\n(%s. The full result was not saved: %s.)", omittedPhrase(omitted, total, omittedImages, images), reason)
}

// worstNotice is the longest notice a result of total bytes and images images
// can get.
func (p *Policy) worstNotice(path string, total, images int64) int {
	return max(len(p.savedNotice(total, total, images, images, path)),
		len(failNotice(total, total, images, images, strings.Repeat("x", MaxReasonBytes))))
}

// tailOf is text's longest suffix of at most n bytes that does not split a rune.
func tailOf(text string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(text) <= n {
		return text
	}
	start := len(text) - n
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
