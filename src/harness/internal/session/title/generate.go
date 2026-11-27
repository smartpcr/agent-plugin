package title

import (
	"fmt"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Cadence is when a provider titles a session automatically.
type Cadence int

const (
	// FirstPrompt titles a session once, from its first own prompt.
	FirstPrompt Cadence = iota
	// EveryPrompt titles it again after each new own prompt.
	EveryPrompt
)

// Input is what a provider builds a prompt from: every eligible own human
// prompt, the current title, and whether a person asked for it (Refresh).
type Input struct {
	Prompts []Message
	Current Title
	Refresh bool
}

// Prompt is a provider's part of a request: the system text and the prompts,
// each with its seq. The generator reduces it to MaxInputBytes and sends it.
type Prompt struct {
	System   string
	Messages []Message
}

// Provider builds title prompts. The generator, not the provider, makes the
// model call (session-views.md sec. 6.2, Titles).
type Provider interface {
	ID() string
	Cadence() Cadence
	Prompt(in Input) (Prompt, error)
}

// Providers is the provider registry; configuration selects one by id.
type Providers struct {
	mu sync.RWMutex
	m  map[string]Provider
}

// NewProviders is an empty registry.
func NewProviders() *Providers { return &Providers{m: map[string]Provider{}} }

// Register adds p; a second provider with p's id is refused.
func (r *Providers) Register(p Provider) error {
	if p == nil || p.ID() == "" {
		return herr.New(CodeInvalid, "title: a provider needs an id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.m[p.ID()]; dup {
		return herr.Newf(CodeInvalid, "title: provider %q is already registered", p.ID())
	}
	r.m[p.ID()] = p
	return nil
}

// Get is the provider id names, or title/unknown-provider.
func (r *Providers) Get(id string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.m[id]; ok {
		return p, nil
	}
	ids := make([]string, 0, len(r.m))
	for k := range r.m {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return nil, herr.Newf(CodeUnknownProvider, "title: no provider %q (registered: %v)", id, ids)
}

// FirstPromptID is the id of the default provider.
const FirstPromptID = "first-prompt"

// firstPrompt titles a session from its first own prompt; a Refresh summarises
// all of them.
type firstPrompt struct{ g GeneratorConfig }

// NewFirstPrompt is the first-prompt provider over g's targets.
func NewFirstPrompt(g GeneratorConfig) Provider { return firstPrompt{g: g.withDefaults()} }

func (firstPrompt) ID() string       { return FirstPromptID }
func (firstPrompt) Cadence() Cadence { return FirstPrompt }

func (p firstPrompt) Prompt(in Input) (Prompt, error) {
	if len(in.Prompts) == 0 {
		return Prompt{}, herr.New(CodeNoInput, "title: the session has no prompt to title")
	}
	what, msgs := "the user's first message below", in.Prompts[:1]
	if in.Refresh {
		what, msgs = "the user's messages below", in.Prompts
	}
	return Prompt{
		System: fmt.Sprintf("Write a title for a conversation from %s: at most %d words, or %d characters in Chinese, Japanese or Korean, in the language of the messages. Reply with the title only, on one line, with no quotes and no final full stop.",
			what, p.g.TargetWords, p.g.TargetCJKCharacters),
		Messages: msgs,
	}, nil
}

// elision marks a clipped message.
const elision = "…"

// clip cuts s to at most n bytes, ending at a character boundary with the
// elision mark; "" when not even the mark fits.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < len(elision)+1 {
		return ""
	}
	cut := n - len(elision)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + elision
}

// reduce bounds ms to budget bytes of text: when they do not fit it keeps the
// first message and the newest that fit whole, clips the next one into what is
// left, and keeps log order.
func reduce(ms []Message, budget int) []Message {
	total := 0
	for _, m := range ms {
		total += len(m.Text)
	}
	if total <= budget || len(ms) == 0 {
		return ms
	}
	first := Message{Seq: ms[0].Seq, Text: clip(ms[0].Text, budget)}
	left := budget - len(first.Text)
	var tail []Message
	for i := len(ms) - 1; i > 0 && left > 0; i-- {
		m := ms[i]
		if len(m.Text) > left {
			if c := clip(m.Text, left); c != "" {
				tail = append(tail, Message{Seq: m.Seq, Text: c})
			}
			break
		}
		tail = append(tail, m)
		left -= len(m.Text)
	}
	out := []Message{first}
	for i := len(tail) - 1; i >= 0; i-- {
		out = append(out, tail[i])
	}
	return out
}
