package title

import (
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// The keys of the two folds registered with session/views.
const (
	FoldKey  = "title"
	InputKey = "titleInput"
)

// Message is one eligible own human prompt: its seq and its text.
type Message struct {
	Seq  event.Seq `json:"seq"`
	Text string    `json:"text"`
}

// TitleInput is the titleInput fold: the first and the latest eligible own
// prompt (each text at most MaxInputBytes) and how many there are.
type TitleInput struct {
	First  *Message  `json:"first,omitempty"`
	Latest *Message  `json:"latest,omitempty"`
	Count  int       `json:"count"`
	Own    event.Seq `json:"own"`
}

// own is the first seq of h's own events: past the cut of a fork.
func own(h session.Header) event.Seq {
	if h.ForkSource != "" {
		return h.ForkThrough + 1
	}
	return 0
}

// promptText is the text of an eligible human prompt: a user/message with text.
func promptText(ev event.Event) (string, bool) {
	p, err := event.UserMessage.As(ev)
	if err != nil {
		return "", false
	}
	var parts []string
	for _, b := range p.Message.Blocks() {
		if b.Text != nil && strings.TrimSpace(b.Text.Text) != "" {
			parts = append(parts, b.Text.Text)
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// TitleFold is the title fold: the fallback from the first eligible prompt
// while there is no title, each session/title the whole new value, and the
// request and attempt bookkeeping that the revision check and the restart rule
// read.
func TitleFold(cfg Config) views.Definition[Title] {
	cfg = cfg.withDefaults()
	return views.Definition[Title]{
		Key: FoldKey, Version: 1, Stored: true,
		Types: []string{event.UserMessage.Name(), TitleType.Name(), RequestType.Name(), AttemptType.Name()},
		Init:  func(h session.Header) Title { return Title{Own: own(h)} },
		Apply: func(t Title, ev event.Event) (Title, bool) {
			switch ev.Type() {
			case event.UserMessage.Name():
				text, ok := promptText(ev)
				if !ok || t.Source != "" {
					return t, false
				}
				if f := Fallback(text, cfg.FallbackMaxWords, cfg.FallbackMaxBytes); f != "" {
					t.Text, t.Source, t.MessageSeqs = f, SourceFallback, []event.Seq{ev.Seq()}
					return t, true
				}
			case TitleType.Name():
				p, err := TitleType.As(ev)
				if err != nil {
					return t, false
				}
				t.Text, t.Source, t.Pinned = p.Title, p.Source, p.Pinned
				t.MessageSeqs, t.RequestSeq = slices.Clone(p.MessageSeqs), p.RequestSeq
				t.Revision++
				return t, true
			case RequestType.Name():
				p, err := RequestType.As(ev)
				if err != nil {
					return t, false
				}
				seq := ev.Seq()
				t.Pending = &seq
				t.Revision++
				if seq >= t.Own {
					t.Requests++
				}
				for _, s := range p.MessageSeqs {
					t.Covered = max(t.Covered, s)
				}
				return t, true
			case AttemptType.Name():
				p, err := AttemptType.As(ev)
				if err != nil || t.Pending == nil || *t.Pending != p.RequestSeq {
					return t, false
				}
				t.Pending = nil
				return t, true
			}
			return t, false
		},
	}
}

// InputFold is the titleInput fold over the eligible own prompts.
func InputFold(g GeneratorConfig) views.Definition[TitleInput] {
	g = g.withDefaults()
	return views.Definition[TitleInput]{
		Key: InputKey, Version: 1, Stored: true,
		Types: []string{event.UserMessage.Name()},
		Init:  func(h session.Header) TitleInput { return TitleInput{Own: own(h)} },
		Apply: func(in TitleInput, ev event.Event) (TitleInput, bool) {
			text, ok := promptText(ev)
			if !ok || ev.Seq() < in.Own {
				return in, false
			}
			m := &Message{Seq: ev.Seq(), Text: clip(text, g.MaxInputBytes)}
			if in.First == nil {
				in.First = m
			}
			in.Latest = m
			in.Count++
			return in, true
		},
	}
}
