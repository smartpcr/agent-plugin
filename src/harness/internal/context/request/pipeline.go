package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// The source kinds of the order table (context-compaction-skill.md §6.2), each
// owned by one contributor. The instructions contributor returns the baseline
// before its changes.
const (
	KindInstructions    = "instructions"
	KindSkillCatalog    = "skill-catalog"
	KindClock           = "clock"
	KindTmux            = "tmux"
	KindSkillInvocation = "skill-invocation"
)

// order is the one contributor order: background first, the text to act on
// last. It is not load order.
var order = []string{KindInstructions, KindSkillCatalog, KindClock, KindTmux, KindSkillInvocation}

// Order is a fresh copy of the contributor order table.
func Order() []string { return slices.Clone(order) }

// Contributor observes the world once per step and returns the messages it
// adds; it never appends.
type Contributor interface {
	// Name is the source kind of every message it returns, one of Order.
	Name() string
	// Required says a failure rejects the step; an optional contributor's
	// failure is skipped and recorded as context/skipped.
	Required() bool
	Contribute(ctx context.Context, in Input) (Output, error)
}

// Input is what a contributor sees: values and a read-only view. Visible is
// the current surface (shadowed nodes are not in it), before the step's batch.
type Input struct {
	Agent      agent.Ref
	Turn, Step int
	// Claimed is the step's batch, copies, in order.
	Claimed   []vocab.Message
	Selection agent.ModelSelection
	Previous  *agent.ModelSelection
	Visible   surface.History
}

// Output is what a contributor adds. Every message is user-role with a source
// of the contributor's kind and an id new to the step; every key of After and
// Reword is the id of a message of the claimed batch (Input.Claimed).
//
// The step's batch is merged in one order: each claimed message (reworded if
// a contributor reworded it), directly followed by the After messages placed
// behind it, contributor by contributor in table order; then every
// contributor's Messages, in table order. An output that breaks a rule fails
// its contributor as a whole: none of it enters, and the contributor is
// skipped (optional) or rejects the step (required).
type Output struct {
	// Messages follow the claimed batch.
	Messages []vocab.Message
	// After holds messages entered directly after one claimed message, as a
	// session snapshot follows the message that cited it.
	After map[string][]vocab.Message
	// Reword is the readable text of one claimed message, as a mention made
	// readable: it replaces that message's text blocks with one text block at
	// the first one's place, keeping its id, role, source and other blocks. One
	// contributor at most rewords a message, and only one that has text.
	Reword map[string]string
}

// TriggerKind is why the pipeline asks the compaction seam at a step.
type TriggerKind string

// The triggers of the pipeline's compaction check (context-compaction-skill.md
// §6.2): overflow and manual belong to other callers of the engine.
const (
	// TriggerPressure is every entered step: the engine measures the estimate
	// against the route's threshold and reduces only if it is crossed.
	TriggerPressure TriggerKind = "pressure"
	// TriggerModelSwitch is a step whose route differs from the last step's:
	// history is measured against the new route.
	TriggerModelSwitch TriggerKind = "model-switch"
)

// Trigger is what the pipeline asks a Considerer about. Previous is the last
// step's selection, nil for an agent's first step; a model-switch trigger
// always has it set, naming another route than Selection.
type Trigger struct {
	Kind       TriggerKind
	Turn, Step int
	Selection  agent.ModelSelection
	Previous   *agent.ModelSelection
}

// Result is what a compaction committed (context-compaction-skill.md §6.1):
// its id, the sequence numbers of its summary record and checkpoint message,
// the nodes it shadowed, the estimated tokens freed, and its outcome. The
// pipeline does not read it: the contributors see the reduction through the
// surface.
type Result struct {
	ID                  string
	Summary, Checkpoint event.Seq
	Shadowed            []event.Seq
	Freed               int
	Outcome             string
}

// Considerer is the part of the compaction seam (the spec's compact.Engine)
// the pipeline calls: Consider qualifies, plans and runs one compaction of the
// agent's session for t, appending it to the log, and returns nil with no
// error when nothing could be reduced and nothing was logged. An error is a
// failed compaction the engine has recorded.
type Considerer interface {
	Consider(ctx context.Context, a agent.Ref, t Trigger) (*Result, error)
}

// ByCompaction is the Reject.By of a step whose model-switch compaction
// failed: nothing was sent to the new route.
const ByCompaction = "compaction"

// Log is the part of an agent's live session the pipeline uses: the current
// surface, and the append of context/skipped. A session.Session is one.
type Log interface {
	History() surface.History
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
}

// Options are New's options.
type Options struct {
	// Table is the scope table contributors are layered over. Required.
	Table scope.Table
	// Sessions is the live session of an agent. Required.
	Sessions func(agent.Ref) (Log, error)
	// Scopes is an agent's scope: its step sees the contributors registered in
	// that scope, in the scopes enclosing it (its preset) and globally. Nil
	// puts every agent at the global scope, which sees only global ones.
	Scopes func(agent.Ref) (scope.Key, error)
	// Compact is the compaction check, run after downstream entered the step
	// and before the contributors, which then see the surface it left. Nil (as
	// at M1, decision M1-D-LOOP-4) runs no compaction. A failed pressure
	// compaction lets the step proceed with full history; a failed
	// model-switch compaction rejects the step (ByCompaction).
	Compact Considerer
}

// Live is the Sessions lookup over the agent registry: the live session of the
// agent r names, which the driver's instance holds open for writing.
func Live(reg agent.Registry) func(agent.Ref) (Log, error) {
	return func(r agent.Ref) (Log, error) {
		a, err := reg.Resolve(r)
		if err != nil {
			return nil, err
		}
		l, ok := a.Session().(Log)
		if !ok {
			return nil, fmt.Errorf("request: the session of %s is not live", r)
		}
		return l, nil
	}
}

// LiveScopes is the Scopes lookup over the agent registry: the scope key of
// the agent r names.
func LiveScopes(reg agent.Registry) func(agent.Ref) (scope.Key, error) {
	return func(r agent.Ref) (scope.Key, error) {
		a, err := reg.Resolve(r)
		if err != nil {
			return scope.Key{}, err
		}
		return a.ScopeKey(), nil
	}
}

// Skipped is the payload of context/skipped: an optional contributor that
// failed for one step.
type Skipped struct {
	Contributor string `json:"contributor"`
	Turn        int    `json:"turn"`
	Step        int    `json:"step"`
	Error       string `json:"error"`
}

// SkippedType is the ignorable, log-only context/skipped.
var SkippedType = event.MustDefine(event.Default(), event.Spec[Skipped]{
	Name: "context/skipped", Version: 1, Ignorable: true, NoSources: true,
	Validate: func(s Skipped) error {
		if !slices.Contains(order, s.Contributor) {
			return fmt.Errorf("context/skipped: %q is not a contributor of the order table", s.Contributor)
		}
		if s.Error == "" {
			return errors.New("context/skipped: the error is required")
		}
		return nil
	},
})

// Pipeline is the context area's one PreStep listener and its contributors.
type Pipeline struct {
	o Options
	// mu orders a registration's duplicate check with its binding.
	mu sync.Mutex
	// layers holds the contributors of each scope by kind: a contributor
	// mounted in a preset serves only that preset's agents
	// (DH-ADR-CONTEXT-1), and the nearest scope's contributor of a kind wins.
	layers *scope.Layers[*scope.Named[Contributor]]
}

// New builds the pipeline and registers its PreStep listener as an effect of
// owner: the listener goes when owner's fiber unloads or the handle is
// disposed.
func New(owner *cordis.Context, o Options) (*Pipeline, *cordis.EffectHandle, error) {
	if owner == nil || o.Table == nil || o.Sessions == nil {
		return nil, nil, errors.New("request: New needs an owner, a scope table and a session lookup")
	}
	p := &Pipeline{o: o, layers: scope.NewLayers(o.Table, scope.NewNamed[Contributor])}
	h, err := agent.PreStep.On(owner, p.preStep)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// Register adds c in owner's scope (global for an untagged owner) as an
// effect of owner: it contributes to the agents that scope encloses until
// owner's fiber unloads, the handle is disposed or the scope closes. A name
// outside the order table, or one already registered in that scope, is
// refused; the same kind in another scope is not.
func (p *Pipeline) Register(owner *cordis.Context, c Contributor) (*cordis.EffectHandle, error) {
	if owner == nil || c == nil {
		return nil, errors.New("request: Register needs an owner and a contributor")
	}
	name := c.Name()
	if !slices.Contains(order, name) {
		return nil, fmt.Errorf("request: contributor %q is not in the order table %v", name, order)
	}
	k := scope.Of(owner)
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.layers.Peek(k); ok {
		if _, taken := l.Get(name); taken {
			return nil, fmt.Errorf("request: a %q contributor is already registered in %s", name, k)
		}
	}
	return p.layers.Add(owner, "context.request:"+name, func(l *scope.Named[Contributor]) func() {
		return l.Set(name, c)
	})
}

// contributors is, in table order, the contributor of each kind visible at
// k: its own scope's, else the nearest enclosing scope's, else the global one.
func (p *Pipeline) contributors(k scope.Key) []Contributor {
	by := map[string]Contributor{}
	for _, e := range scope.Merged(p.layers, k, func(l *scope.Named[Contributor]) *scope.Named[Contributor] { return l }) {
		by[e.Name] = e.Value
	}
	var out []Contributor
	for _, kind := range order {
		if c, ok := by[kind]; ok {
			out = append(out, c)
		}
	}
	return out
}

// scopeOf is the scope the agent r's step runs at.
func (p *Pipeline) scopeOf(r agent.Ref) (scope.Key, error) {
	if p.o.Scopes == nil {
		return scope.Key{}, nil
	}
	return p.o.Scopes(r)
}

func (p *Pipeline) preStep(_ *cordis.Context, req agent.PreStepRequest, next func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
	d, err := next(req)
	if err != nil {
		return d, err
	}
	enter, ok := d.(agent.Enter)
	if !ok {
		return d, nil
	}
	at, err := p.scopeOf(req.Agent)
	if err != nil {
		return nil, fmt.Errorf("request: the scope of %s: %w", req.Agent, err)
	}
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if p.o.Compact != nil {
		if ctx.Err() != nil {
			return enter, nil
		}
		if rej, ok := p.compact(ctx, req); ok {
			return rej, nil
		}
	}
	cs := p.contributors(at)
	if len(cs) == 0 {
		return enter, nil
	}
	log, err := p.o.Sessions(req.Agent)
	if err != nil {
		return nil, fmt.Errorf("request: the session of %s: %w", req.Agent, err)
	}
	// Visible is read after the compaction check: a baseline it shadowed is
	// absent, so its contributor enters it again in this step.
	in := Input{
		Agent: req.Agent, Turn: req.Turn, Step: req.Step,
		Claimed: slices.Clone(enter.Messages), Selection: req.Selection, Previous: req.Previous,
		Visible: log.History(),
	}
	m := newMerge(enter.Messages)
	for _, c := range cs {
		if ctx.Err() != nil {
			// A cancelled turn: the driver's cancel path names the reason.
			return enter, nil
		}
		out, err := contribute(ctx, c, in)
		if err == nil {
			err = m.check(c.Name(), out)
		}
		if err != nil {
			if ctx.Err() != nil {
				// The failure is the cancel's, not the contributor's: enter
				// the step as the chain proposed it and let the driver end
				// the turn aborted.
				return enter, nil
			}
			if c.Required() {
				return agent.Reject{By: c.Name(), Reason: err.Error()}, nil
			}
			dr, derr := SkippedType.Draft(Skipped{Contributor: c.Name(), Turn: req.Turn, Step: req.Step, Error: err.Error()})
			if derr == nil {
				_, derr = log.Append(context.WithoutCancel(ctx), dr)
			}
			if derr != nil {
				return nil, fmt.Errorf("request: recording the skip of %q: %w", c.Name(), derr)
			}
			continue
		}
		m.add(c.Name(), out)
	}
	enter.Messages = m.messages()
	return enter, nil
}

// compact runs the compaction check of an entered step. It returns a Reject
// and true when a model-switch compaction failed; a failed pressure
// compaction, or a failure the turn's cancel caused, lets the step proceed.
func (p *Pipeline) compact(ctx context.Context, req agent.PreStepRequest) (agent.PreStepDecision, bool) {
	t := Trigger{Kind: TriggerPressure, Turn: req.Turn, Step: req.Step, Selection: cloneSelection(req.Selection)}
	if req.Previous != nil {
		prev := cloneSelection(*req.Previous)
		t.Previous = &prev
		if prev.Route != req.Selection.Route {
			t.Kind = TriggerModelSwitch
		}
	}
	err := consider(ctx, p.o.Compact, req.Agent, t)
	if err == nil || ctx.Err() != nil || t.Kind != TriggerModelSwitch {
		// Pressure: the step proceeds with full history; the engine recorded
		// its failure and backs off (§6.2).
		return nil, false
	}
	return agent.Reject{By: ByCompaction, Reason: err.Error()}, true
}

// consider runs the compaction seam with its panic contained.
func consider(ctx context.Context, c Considerer, a agent.Ref, t Trigger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("compaction (%s) panicked: %v", t.Kind, r)
		}
	}()
	_, err = c.Consider(ctx, a, t)
	return err
}

// cloneSelection is a copy of s that shares no control with it.
func cloneSelection(s agent.ModelSelection) agent.ModelSelection {
	s.Controls = s.Controls.Clone()
	return s
}

// merge builds a step's batch from the claimed messages and the contributors'
// outputs, by the rules of Output.
type merge struct {
	claimed []vocab.Message
	// at is the position of each claimed message by id.
	at map[string]int
	// ids is every id in the batch so far.
	ids      map[string]bool
	after    [][]vocab.Message
	reworded map[string]vocab.Message
	// rewordBy names the contributor that reworded each claimed message.
	rewordBy map[string]string
	tail     []vocab.Message
}

func newMerge(claimed []vocab.Message) *merge {
	m := &merge{
		claimed: slices.Clone(claimed), at: make(map[string]int, len(claimed)), ids: make(map[string]bool, len(claimed)),
		after: make([][]vocab.Message, len(claimed)), reworded: map[string]vocab.Message{}, rewordBy: map[string]string{},
	}
	for i, c := range claimed {
		m.at[c.ID()], m.ids[c.ID()] = i, true
	}
	return m
}

// check accepts out from the contributor name, or says which rule it breaks;
// it changes nothing, so a failed output enters no part of itself.
func (m *merge) check(name string, out Output) error {
	seen := map[string]bool{}
	fresh := func(msgs []vocab.Message) error {
		if err := check(name, msgs); err != nil {
			return err
		}
		for _, x := range msgs {
			if m.ids[x.ID()] || seen[x.ID()] {
				return fmt.Errorf("contributor %q returned message %s, whose id is already in the step's batch", name, x.ID())
			}
			seen[x.ID()] = true
		}
		return nil
	}
	if err := fresh(out.Messages); err != nil {
		return err
	}
	for _, id := range slices.Sorted(maps.Keys(out.After)) {
		if _, ok := m.at[id]; !ok {
			return fmt.Errorf("contributor %q placed messages after %s, which is not a claimed message of the step", name, id)
		}
		if err := fresh(out.After[id]); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(out.Reword)) {
		i, ok := m.at[id]
		switch {
		case !ok:
			return fmt.Errorf("contributor %q rewords %s, which is not a claimed message of the step", name, id)
		case strings.TrimSpace(out.Reword[id]) == "":
			return fmt.Errorf("contributor %q rewords %s with no text", name, id)
		case m.rewordBy[id] != "":
			return fmt.Errorf("contributor %q rewords %s, which %q already reworded", name, id, m.rewordBy[id])
		}
		if _, err := reword(m.claimed[i], out.Reword[id]); err != nil {
			return fmt.Errorf("contributor %q: %w", name, err)
		}
	}
	return nil
}

// add merges an output check accepted.
func (m *merge) add(name string, out Output) {
	for _, x := range out.Messages {
		m.ids[x.ID()] = true
	}
	m.tail = append(m.tail, out.Messages...)
	for id, msgs := range out.After {
		for _, x := range msgs {
			m.ids[x.ID()] = true
		}
		m.after[m.at[id]] = append(m.after[m.at[id]], msgs...)
	}
	for id, text := range out.Reword {
		r, _ := reword(m.claimed[m.at[id]], text) // check built it once already
		m.reworded[id], m.rewordBy[id] = r, name
	}
}

// messages is the merged batch.
func (m *merge) messages() []vocab.Message {
	out := make([]vocab.Message, 0, len(m.ids))
	for i, c := range m.claimed {
		if r, ok := m.reworded[c.ID()]; ok {
			c = r
		}
		out = append(out, c)
		out = append(out, m.after[i]...)
	}
	return append(out, m.tail...)
}

// reword is msg with its text blocks replaced by one block of text, at the
// first one's place.
func reword(msg vocab.Message, text string) (vocab.Message, error) {
	var blocks []vocab.Block
	placed := false
	for _, b := range msg.Blocks() {
		switch {
		case b.Kind != vocab.KindText:
			blocks = append(blocks, b)
		case !placed:
			blocks, placed = append(blocks, vocab.TextBlock(text)), true
		}
	}
	if !placed {
		return vocab.Message{}, fmt.Errorf("message %s has no text to reword", msg.ID())
	}
	var src *vocab.Source
	if s, ok := msg.Source(); ok {
		src = &s
	}
	return vocab.RestoreMessage(msg.ID(), msg.Role(), src, blocks...)
}

// contribute runs one contributor with its panic contained.
func contribute(ctx context.Context, c Contributor, in Input) (out Output, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = Output{}, fmt.Errorf("contributor %q panicked: %v", c.Name(), r)
		}
	}()
	in.Claimed = slices.Clone(in.Claimed)
	return c.Contribute(ctx, in)
}

// check accepts only user-role messages whose source is the contributor's
// kind and whose data, if any, is JSON.
func check(name string, msgs []vocab.Message) error {
	for _, m := range msgs {
		if m.IsZero() {
			return fmt.Errorf("contributor %q returned an empty message", name)
		}
		if m.Role() != vocab.RoleUser {
			return fmt.Errorf("contributor %q returned a %s message, want user", name, m.Role())
		}
		src, ok := m.Source()
		if !ok || src.Kind != name {
			return fmt.Errorf("contributor %q returned message %s without a %q source", name, m.ID(), name)
		}
		if err := m.Validate(); err != nil {
			return fmt.Errorf("contributor %q: %w", name, err)
		}
	}
	return nil
}

// Message is a helper for contributors: a user message of kind with text and
// the observation data, produced by producer.
func Message(kind, producer, text string, data any) (vocab.Message, error) {
	m, err := vocab.NewMessage(vocab.RoleUser, vocab.TextBlock(text))
	if err != nil {
		return vocab.Message{}, err
	}
	src := vocab.Source{Kind: kind, Producers: []string{producer}}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return vocab.Message{}, err
		}
		src.Data = raw
	}
	if strings.TrimSpace(producer) == "" {
		return vocab.Message{}, errors.New("request: a message needs its producer")
	}
	return m.WithSource(src)
}
