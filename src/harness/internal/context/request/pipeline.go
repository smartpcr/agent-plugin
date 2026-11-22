package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Output is what a contributor adds: user-role messages, each with a source
// of the contributor's kind, entered after the claimed batch.
type Output struct {
	Messages []vocab.Message
}

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
	cs := p.contributors(at)
	if len(cs) == 0 {
		return enter, nil
	}
	log, err := p.o.Sessions(req.Agent)
	if err != nil {
		return nil, fmt.Errorf("request: the session of %s: %w", req.Agent, err)
	}
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	in := Input{
		Agent: req.Agent, Turn: req.Turn, Step: req.Step,
		Claimed: slices.Clone(enter.Messages), Selection: req.Selection, Previous: req.Previous,
		Visible: log.History(),
	}
	msgs := slices.Clone(enter.Messages)
	for _, c := range cs {
		if ctx.Err() != nil {
			// A cancelled turn: the driver's cancel path names the reason.
			return enter, nil
		}
		out, err := contribute(ctx, c, in)
		if err == nil {
			err = check(c.Name(), out.Messages)
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
		msgs = append(msgs, out.Messages...)
	}
	enter.Messages = msgs
	return enter, nil
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
