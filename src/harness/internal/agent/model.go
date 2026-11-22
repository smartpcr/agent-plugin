package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// CodeNoModel ends a turn as error when no source names a model (DH-D-AGENT-7).
const CodeNoModel Code = "agent/no-model"

// SelectionSource is where a captured selection came from, in precedence order.
type SelectionSource string

const (
	// SourceSelect is a recorded Select not yet used, and the record Select appends.
	SourceSelect SelectionSource = "select"
	// SourceHeader is the selection of the latest request header.
	SourceHeader SelectionSource = "header"
	// SourceOptions is the agent's creation options.
	SourceOptions SelectionSource = "options"
	// SourceDefault is the default registered nearest on the agent's scope chain.
	SourceDefault SelectionSource = "default"
)

func (s SelectionSource) valid() bool {
	switch s {
	case SourceSelect, SourceHeader, SourceOptions, SourceDefault:
		return true
	}
	return false
}

// SelectionRecord is the model/selection payload. A Select appends one with no
// step: it is pending until a capture uses it. The driver's capture appends one
// right after the step's step/start, naming the step and the selection the step
// runs with; Consumed is the seq of the Select record it used, if any.
type SelectionRecord struct {
	Selection ModelSelection  `json:"selection"`
	Source    SelectionSource `json:"source"`
	Turn      int             `json:"turn,omitempty"`
	Step      int             `json:"step,omitempty"`
	Consumed  *event.Seq      `json:"consumed,omitempty"`
}

// Captured reports whether r is a step's capture rather than a Select.
func (r SelectionRecord) Captured() bool { return r.Step > 0 }

func validSelection(r SelectionRecord) error {
	if err := r.Selection.Route.Validate(); err != nil {
		return err
	}
	if !r.Source.valid() {
		return fmt.Errorf("agent: unknown selection source %q", r.Source)
	}
	if !r.Captured() {
		if r.Source != SourceSelect || r.Turn != 0 || r.Consumed != nil {
			return fmt.Errorf("agent: a select record names no turn, step or consumed select")
		}
		return nil
	}
	if r.Turn < 1 {
		return fmt.Errorf("agent: a captured selection names its turn")
	}
	if (r.Consumed != nil) != (r.Source == SourceSelect) {
		return fmt.Errorf("agent: a capture names the select it consumed exactly when its source is select")
	}
	return nil
}

// SelectionType is the log-only model/selection event (the modelSelection fold).
var SelectionType = event.MustDefine(event.Default(), event.Spec[SelectionRecord]{Name: "model/selection", Version: 1, Validate: validSelection})

// DefaultModels is the default-model service as the agent reads it: the default
// registered nearest on a scope chain (DH-ADR-AGENT-10).
type DefaultModels interface {
	Current(at scope.Key) (ModelSelection, bool)
}

// DefaultModelsFunc adapts a function to DefaultModels.
type DefaultModelsFunc func(at scope.Key) (ModelSelection, bool)

// Current calls f.
func (f DefaultModelsFunc) Current(at scope.Key) (ModelSelection, bool) { return f(at) }

// ModelLog is the session Model reads and Select appends to.
type ModelLog interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
	Len() event.Offset
	Read(ctx context.Context, from event.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error)
}

// ModelOptions configures an agent's Model.
type ModelOptions struct {
	// Initial is the creation options' selection; a blank Route is none.
	Initial ModelSelection
	// Defaults is the default-model service, nil for none; Scope is the agent's
	// scope key it is read with.
	Defaults DefaultModels
	Scope    scope.Key
}

// Model is an agent's model selection (DH-D-AGENT-7). Its state is the
// modelSelection fold of the log, brought up to date on every read: the pending
// Select, the latest request header and the last step's capture.
type Model struct {
	log  ModelLog
	opts ModelOptions

	mu     sync.Mutex
	folded event.Offset
	// pending is the latest Select record no capture has used, and its seq.
	pending    *ModelSelection
	pendingSeq event.Seq
	header     *ModelSelection
	last       *SelectionRecord
}

// OpenModel folds log's selections and request headers into a Model.
func OpenModel(ctx context.Context, log ModelLog, opts ModelOptions) (*Model, error) {
	if log == nil {
		return nil, errorf(CodeInvalid, "a model selection needs its session")
	}
	opts.Initial = cloneSelection(opts.Initial)
	m := &Model{log: log, opts: opts}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.foldLocked(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func cloneSelection(s ModelSelection) ModelSelection {
	s.Controls = s.Controls.Clone()
	return s
}

func (m *Model) foldLocked(ctx context.Context) error {
	const page = 256
	for n := m.log.Len(); m.folded < n; {
		evs, err := m.log.Read(ctx, m.folded, page)
		if err != nil {
			return fmt.Errorf("agent: fold the model selection: %w", err)
		}
		if len(evs) == 0 {
			break
		}
		for _, ev := range evs {
			if err := m.apply(ev); err != nil {
				return err
			}
		}
		m.folded = evs[len(evs)-1].Seq() + 1
	}
	return nil
}

func (m *Model) apply(ev event.Event) error {
	switch ev.Type() {
	case SelectionType.Name():
		r, err := SelectionType.As(ev)
		if err != nil {
			return fmt.Errorf("agent: fold the model selection at seq %d: %w", ev.Seq(), err)
		}
		if !r.Captured() {
			sel := cloneSelection(r.Selection)
			m.pending, m.pendingSeq = &sel, ev.Seq()
			return nil
		}
		if r.Consumed != nil && m.pending != nil && m.pendingSeq <= *r.Consumed {
			m.pending = nil
		}
		m.last = &r
	case event.RequestHeader.Name():
		h, err := event.RequestHeader.As(ev)
		if err != nil {
			return fmt.Errorf("agent: fold the request header at seq %d: %w", ev.Seq(), err)
		}
		sel := ModelSelection{Route: h.Route}
		if h.Controls != nil {
			sel.Controls = h.Controls.Clone()
		}
		m.header = &sel
	}
	return nil
}

// Select records sel as the agent's next selection: a log-only model/selection
// event. It applies from the next step the driver captures, never to a step
// already captured.
func (m *Model) Select(ctx context.Context, sel ModelSelection) (event.Receipt, error) {
	d, err := SelectionType.Draft(SelectionRecord{Selection: cloneSelection(sel), Source: SourceSelect})
	if err != nil {
		return event.Receipt{}, errorf(CodeInvalid, "%v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.log.Append(ctx, d)
	if err != nil {
		return r, fmt.Errorf("agent: select a model: %w", err)
	}
	return r, m.foldLocked(ctx)
}

// Current is the selection the next step would capture, and its source: a
// recorded Select not yet used, else the latest request header's, else the
// creation options', else the default nearest on the agent's scope chain. With
// none it is an agent/no-model error.
func (m *Model) Current(ctx context.Context) (ModelSelection, SelectionSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.foldLocked(ctx); err != nil {
		return ModelSelection{}, "", err
	}
	sel, src, _, err := m.currentLocked()
	return sel, src, err
}

func (m *Model) currentLocked() (ModelSelection, SelectionSource, *event.Seq, error) {
	if m.pending != nil {
		seq := m.pendingSeq
		return cloneSelection(*m.pending), SourceSelect, &seq, nil
	}
	if m.header != nil && m.header.Route != "" {
		sel := cloneSelection(*m.header)
		// A header names no model; the step that wrote it does.
		if m.last != nil && m.last.Selection.Route == sel.Route {
			sel.Model = m.last.Selection.Model
		}
		return sel, SourceHeader, nil, nil
	}
	if m.opts.Initial.Route != "" {
		return cloneSelection(m.opts.Initial), SourceOptions, nil, nil
	}
	if m.opts.Defaults != nil {
		if sel, ok := m.opts.Defaults.Current(m.opts.Scope); ok && sel.Route != "" {
			return cloneSelection(sel), SourceDefault, nil, nil
		}
	}
	return ModelSelection{}, "", nil, errorf(CodeNoModel, "no selection, request header, creation option or default names a model")
}

// Capture is a step's selection taken at point 4.
type Capture struct {
	Selection ModelSelection
	Source    SelectionSource
	// Previous is the last step's selection, nil before the first capture.
	Previous *ModelSelection
	// Record is the model/selection event to append right after the step's
	// step/start (ProposeStep's also); appending it consumes the Select it used.
	Record event.Draft
}

// Capture takes the selection for turn's step. It changes nothing until its
// Record is appended, so a step that is not entered uses no Select; a Select
// made after the capture stays pending for the next step.
func (m *Model) Capture(ctx context.Context, turn, step int) (Capture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.foldLocked(ctx); err != nil {
		return Capture{}, err
	}
	sel, src, consumed, err := m.currentLocked()
	if err != nil {
		return Capture{}, err
	}
	d, err := SelectionType.Draft(SelectionRecord{Selection: sel, Source: src, Turn: turn, Step: step, Consumed: consumed})
	if err != nil {
		return Capture{}, errorf(CodeInvalid, "%v", err)
	}
	c := Capture{Selection: cloneSelection(sel), Source: src, Record: d}
	if m.last != nil {
		prev := cloneSelection(m.last.Selection)
		c.Previous = &prev
	}
	return c, nil
}

// Last is the most recent step's captured selection record, false before any.
func (m *Model) Last(ctx context.Context) (SelectionRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.foldLocked(ctx); err != nil {
		return SelectionRecord{}, false, err
	}
	if m.last == nil {
		return SelectionRecord{}, false, nil
	}
	r := *m.last
	r.Selection = cloneSelection(r.Selection)
	return r, true, nil
}
