package title

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// TitlePayload is session/title: the whole new value of the session's title.
type TitlePayload struct {
	Title       string      `json:"title"`
	Source      Source      `json:"source"`
	MessageSeqs []event.Seq `json:"message_seqs,omitempty"`
	Pinned      bool        `json:"pinned"`
	// RequestSeq is the session/title-request a generated title answers.
	RequestSeq *event.Seq `json:"request_seq,omitempty"`
}

// RequestPayload is session/title-request: everything the title model is sent.
// The request is rebuilt from it (RequestFrom), the only way to make one.
type RequestPayload struct {
	Provider        string        `json:"provider"`
	Route           vocab.RouteID `json:"route"`
	System          string        `json:"system,omitempty"`
	Messages        []string      `json:"messages"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Effort          string        `json:"effort,omitempty"`
	MessageSeqs     []event.Seq   `json:"message_seqs"`
	Purpose         string        `json:"purpose"`
}

// Outcome is how a title attempt ended.
type Outcome string

// The outcomes (session-views.md sec. 6.2).
const (
	OutcomeAccepted    Outcome = "accepted"
	OutcomeRejected    Outcome = "rejected"
	OutcomeFailed      Outcome = "failed"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeSuperseded  Outcome = "superseded"
	OutcomeInterrupted Outcome = "interrupted"
)

// AttemptPayload is session/title-attempt: the one settlement of one request.
type AttemptPayload struct {
	RequestSeq    event.Seq          `json:"request_seq"`
	RequestAsOf   *event.Seq         `json:"request_as_of,omitempty"`
	RequestDigest string             `json:"request_digest,omitempty"`
	Outcome       Outcome            `json:"outcome"`
	Finish        vocab.FinishReason `json:"finish,omitempty"`
	Usage         *vocab.Usage       `json:"usage,omitempty"`
	DurationMs    int64              `json:"duration_ms"`
	Text          string             `json:"text,omitempty"`
	ServedBy      string             `json:"served_by,omitempty"`
	ErrorCode     string             `json:"error_code,omitempty"`
	ErrorClass    string             `json:"error_class,omitempty"`
}

func validTitle(p TitlePayload) error {
	if p.Title == "" || Normalize(p.Title, 0) != p.Title {
		return fmt.Errorf("session/title: title %q is empty or not normalized", p.Title)
	}
	if p.Source != SourceGenerated && p.Source != SourceUser {
		return fmt.Errorf("session/title: source %q is not generated or user", p.Source)
	}
	if p.Source == SourceGenerated && (p.RequestSeq == nil || p.Pinned) {
		return errors.New("session/title: a generated title cites its request and is never pinned")
	}
	return nil
}

func validRequest(p RequestPayload) error {
	switch {
	case p.Provider == "" || p.Purpose == "":
		return errors.New("session/title-request: provider and purpose are required")
	case p.Route == "":
		return errors.New("session/title-request: route is required")
	case len(p.Messages) == 0 || len(p.Messages) != len(p.MessageSeqs):
		return errors.New("session/title-request: one message seq per message, at least one")
	}
	return nil
}

func validAttempt(p AttemptPayload) error {
	switch p.Outcome {
	case OutcomeAccepted, OutcomeRejected, OutcomeFailed, OutcomeCancelled, OutcomeSuperseded, OutcomeInterrupted:
		return nil
	}
	return fmt.Errorf("session/title-attempt: outcome %q is not one of the closed set", p.Outcome)
}

func mustLent[T any](s event.Spec[T]) *event.Type[T] {
	t, err := event.DefineLent(event.Default(), s)
	if err != nil {
		panic(err)
	}
	return t
}

// The three durable, log-only title events. The title never enters the model's
// history: none is a surface type. session/title-request declares its request
// builder in its own declaration.
var (
	TitleType   = mustLent(event.Spec[TitlePayload]{Name: "session/title", Version: 1, Detached: true, NoSources: true, Validate: validTitle})
	RequestType = func() *event.Type[RequestPayload] {
		t := mustLent(event.Spec[RequestPayload]{Name: "session/title-request", Version: 1, Detached: true, NoSources: true, Validate: validRequest})
		if err := surface.DeclareBuilder(event.Default(), t.Name(), buildRequest); err != nil {
			panic(err)
		}
		return t
	}()
	AttemptType = mustLent(event.Spec[AttemptPayload]{Name: "session/title-attempt", Version: 1, Detached: true, NoSources: true, Validate: validAttempt})
)

// buildRequest is session/title-request's request builder: the system text and
// the prompts the event names, on its route and controls, with no tools and no
// history.
func buildRequest(ev event.Event, _ surface.View, b *surface.AuxBuilder) error {
	var p RequestPayload
	if err := json.Unmarshal([]byte(ev.Raw()), &p); err != nil {
		return err
	}
	b.Purpose(p.Purpose)
	b.Route(p.Route)
	b.Controls(vocab.Controls{Effort: p.Effort, MaxOutputTokens: p.MaxOutputTokens})
	if p.System != "" {
		m, err := vocab.RestoreMessage("title-system", vocab.RoleSystem, nil, vocab.TextBlock(p.System))
		if err != nil {
			return err
		}
		b.Message(m)
	}
	for i, text := range p.Messages {
		m, err := vocab.RestoreMessage(fmt.Sprintf("title-prompt-%d", i+1), vocab.RoleUser, nil, vocab.TextBlock(text))
		if err != nil {
			return err
		}
		b.Message(m)
	}
	return nil
}
