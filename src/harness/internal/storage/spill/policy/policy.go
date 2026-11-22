// Package policy is the spill policy, storage.spill.policy (DH-D-STORAGE-6,
// DH-ADR-STORAGE-4, docs/harness/areas/storage.md): the tool registry's spill
// seat, which keeps every tool result within the budget. A result over the budget
// is saved whole through the spill store and the model gets its head and tail
// and a notice naming the saved file; a failed save still bounds the result and
// the notice says it was not saved. The budget is MaxInlineTokens priced by the
// shared token estimate (DH-D-LLM-6), which a load-time check keeps within the
// registry's maxResultBytes, so no result over either reaches a model.
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
	// MaxReasonBytes bounds the failure reason a notice quotes.
	MaxReasonBytes = 200
)

// separator joins the head and the tail of a preview.
const separator = "\n[...]\n"

// Config configures the policy. It has no "off" value.
type Config struct {
	// MaxInlineTokens is the most a result may cost inline; zero is
	// DefaultMaxInlineTokens, and less than MinInlineTokens is refused.
	MaxInlineTokens int
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
	if c.Keep < 0 {
		return fmt.Errorf("spill policy: Keep %s is negative", c.Keep)
	}
	return nil
}

func (c Config) normalized() Config {
	if c.MaxInlineTokens == 0 {
		c.MaxInlineTokens = DefaultMaxInlineTokens
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
	if need := p.worstNotice(p.path(probe.ID), int64(1)<<40) + len(separator) + 2*utf8.UTFMax; need > p.budget {
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

// Shape is the tools.SpillShaper: a text within the budget is kept; a longer one
// is saved whole for the call's session and replaced by its head, its tail and
// the notice. A rune is at least one byte and a quarter token, so a text within
// the byte budget is within MaxInlineTokens too.
func (p *Policy) Shape(ctx context.Context, c tools.Call, o tools.Outcome) tools.SpillResult {
	text := o.Text
	if len(text) <= p.budget && meter.EstimateText(text) <= p.cfg.MaxInlineTokens {
		return tools.SpillResult{}
	}
	total := int64(len(text))
	owner := string(c.Agent().Session)
	var ref spill.Ref
	path := ""
	if owner != "" {
		ref = p.store.Reserve(owner, c.Name()+".txt")
		path = p.path(ref.ID)
	}
	// The notice is sized for its worst case before anything is saved, so a
	// saved file is never orphaned by a notice that does not fit.
	room := p.budget - p.worstNotice(path, total) - len(separator)
	head := string(spill.Head([]byte(text), room/2))
	tail := tailOf(text, room-len(head))
	omitted := total - int64(len(head)) - int64(len(tail))
	preview := head + separator + tail

	if owner == "" {
		return tools.SpillResult{Replace: true, Text: preview + failNotice(omitted, total, "the call has no session to own the saved file")}
	}
	if err := p.save(ctx, ref, text); err != nil {
		return tools.SpillResult{Replace: true, Text: preview + failNotice(omitted, total, err.Error())}
	}
	return tools.SpillResult{Replace: true, Text: preview + p.savedNotice(omitted, total, path),
		Ref: &event.Spill{Path: ref.ID, Bytes: total}}
}

func (p *Policy) save(ctx context.Context, ref spill.Ref, text string) error {
	w, err := p.store.Create(ctx, ref, int64(len(text)))
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(text)); err != nil {
		_ = w.Abort()
		return err
	}
	if err := w.Close(); err != nil {
		_ = w.Abort()
		return err
	}
	return nil
}

// path is what the notice cites: the host path the file tools can read, or the
// id when the store cannot map it.
func (p *Policy) path(id string) string {
	if hp, ok := p.store.HostPath(id); ok {
		return hp
	}
	return id
}

func (p *Policy) savedNotice(omitted, total int64, path string) string {
	until := p.cfg.Now().Add(p.cfg.Keep).UTC().Format("2006-01-02")
	return fmt.Sprintf("\n(Omitted %d of %d bytes. Full result saved to %s, kept until %s. Use read with offset and limit, or grep this path.)",
		omitted, total, path, until)
}

func failNotice(omitted, total int64, reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > MaxReasonBytes {
		reason = string(spill.Head([]byte(reason), MaxReasonBytes-3)) + "..."
	}
	return fmt.Sprintf("\n(Omitted %d of %d bytes. The full result was not saved: %s.)", omitted, total, reason)
}

// worstNotice is the longest notice a result of total bytes can get.
func (p *Policy) worstNotice(path string, total int64) int {
	return max(len(p.savedNotice(total, total, path)), len(failNotice(total, total, strings.Repeat("x", MaxReasonBytes))))
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
