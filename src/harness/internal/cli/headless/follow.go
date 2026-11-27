package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// exportLine is one committed event in the export line format of A05
// (session/store/sqlite's export, DH-D-STORE-7): the stored strings, never a
// re-encoded payload.
type exportLine struct {
	Seq       event.Seq    `json:"seq"`
	Time      int64        `json:"time"`
	Type      string       `json:"type"`
	PV        int          `json:"pv"`
	Ignorable bool         `json:"ignorable,omitempty"`
	Op        event.OpKind `json:"op,omitempty"`
	Start     event.Seq    `json:"start,omitempty"`
	End       event.Seq    `json:"end,omitempty"`
	Sources   []event.Seq  `json:"sources,omitempty"`
	Data      string       `json:"data"`
	Aux       string       `json:"aux,omitempty"`
}

// follower reads the run's session from the interval's start as commits wake
// it: under --json it writes each event as an export line; it writes a progress
// line at each step start and the reasoning --reasoning allows to the diagnostic
// stream, and keeps the last assistant text and turn ending. Only its goroutine
// touches its fields until loop returns.
type follower struct {
	r         *runner
	rs        session.ReadSession
	next      session.Offset
	json      bool
	reasoning bool
	poke      chan struct{}
	route     vocab.RouteID

	text   string
	turn   int
	reason string
	err    error
}

func (f *follower) wake() {
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

// loop drains on each wake, and once more when final closes.
func (f *follower) loop(ctx context.Context, final <-chan struct{}) {
	for {
		select {
		case <-f.poke:
			f.drain(ctx)
		case <-final:
			f.drain(ctx)
			return
		case <-ctx.Done():
			return
		}
	}
}

func (f *follower) drain(ctx context.Context) {
	for {
		evs, err := f.rs.Read(ctx, f.next, 0, session.WithCold())
		if err != nil {
			if f.err == nil && ctx.Err() == nil {
				f.err = fmt.Errorf("read the session: %w", err)
			}
			return
		}
		if len(evs) == 0 {
			return
		}
		for _, e := range evs {
			f.next = e.Seq() + 1
			f.handle(e)
		}
	}
}

func (f *follower) handle(e event.Event) {
	if f.json {
		f.export(e)
	}
	switch e.Type() {
	case event.RequestHeader.Name():
		if p, err := event.RequestHeader.As(e); err == nil && p.Route != "" {
			f.route = p.Route
		}
	case event.StepStartType.Name():
		if p, err := event.StepStartType.As(e); err == nil {
			route := f.route
			if route == "" {
				route = "default"
			}
			fmt.Fprintf(f.r.diag, "dsh headless: turn %d step %d on route %s\n", p.Turn, p.Step, route)
		}
	case event.AssistantMessage.Name():
		p, err := event.AssistantMessage.As(e)
		if err != nil {
			return
		}
		var text, thought strings.Builder
		for _, b := range p.Message.Blocks() {
			switch {
			case b.Kind == vocab.KindText && b.Text != nil:
				text.WriteString(b.Text.Text)
			case b.Kind == vocab.KindReasoning && b.Reasoning != nil && !b.Reasoning.Redacted:
				thought.WriteString(b.Reasoning.Text)
			}
		}
		if f.reasoning && thought.Len() > 0 {
			fmt.Fprintf(f.r.diag, "%s\n", strings.TrimRight(thought.String(), "\n"))
		}
		if text.Len() > 0 {
			f.text = text.String()
		}
	case event.TurnEndType.Name():
		if p, err := event.TurnEndType.As(e); err == nil {
			f.turn, f.reason = p.Turn, p.Reason.Kind
		}
	}
}

func (f *follower) export(e event.Event) {
	if f.err != nil {
		return
	}
	op := e.Op()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(exportLine{
		Seq: e.Seq(), Time: e.Time(), Type: e.Type(), PV: e.Version(), Ignorable: e.Ignorable(),
		Op: op.Kind, Start: op.Start, End: op.End, Sources: e.Sources(), Data: e.Raw(), Aux: e.ColdRaw(),
	}); err != nil {
		f.err = err
		return
	}
	if _, err := f.r.out.Write(b.Bytes()); err != nil {
		f.err = fmt.Errorf("%s: write an export line: %w", output.ReasonClosed, err)
	}
}
