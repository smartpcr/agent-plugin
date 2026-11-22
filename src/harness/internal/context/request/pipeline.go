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
	// Sessions is the live session of an agent. Required.
	Sessions func(agent.Ref) (Log, error)
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
	o  Options
	mu sync.Mutex
	// by is the registered contributor of each kind.
	by map[string]Contributor
}

// New builds the pipeline and registers its PreStep listener as an effect of
// owner: the listener goes when owner's fiber unloads or the handle is
// disposed.
func New(owner *cordis.Context, o Options) (*Pipeline, *cordis.EffectHandle, error) {
	if owner == nil || o.Sessions == nil {
		return nil, nil, errors.New("request: New needs an owner and a session lookup")
	}
	p := &Pipeline{o: o, by: map[string]Contributor{}}
	h, err := agent.PreStep.On(owner, p.preStep)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// Register adds c as an effect of owner: it contributes until owner's fiber
// unloads or the handle is disposed. A name outside the order table, or one
// already registered, is refused.
func (p *Pipeline) Register(owner *cordis.Context, c Contributor) (*cordis.EffectHandle, error) {
	if owner == nil || c == nil {
		return nil, errors.New("request: Register needs an owner and a contributor")
	}
	name := c.Name()
	if !slices.Contains(order, name) {
		return nil, fmt.Errorf("request: contributor %q is not in the order table %v", name, order)
	}
	return owner.Effect("context.request:"+name, func() (cordis.Disposer, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if _, taken := p.by[name]; taken {
			return nil, fmt.Errorf("request: a %q contributor is already registered", name)
		}
		p.by[name] = c
		return func() error {
			p.mu.Lock()
			if p.by[name] == c {
				delete(p.by, name)
			}
			p.mu.Unlock()
			return nil
		}, nil
	})
}

// contributors is the registered contributors in table order.
func (p *Pipeline) contributors() []Contributor {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Contributor
	for _, k := range order {
		if c, ok := p.by[k]; ok {
			out = append(out, c)
		}
	}
	return out
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
	cs := p.contributors()
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
		out, err := contribute(ctx, c, in)
		if err == nil {
			err = check(c.Name(), out.Messages)
		}
		if err != nil {
			if c.Required() {
				return agent.Reject{By: c.Name(), Reason: err.Error()}, nil
			}
			dr, derr := SkippedType.Draft(Skipped{Contributor: c.Name(), Turn: req.Turn, Step: req.Step, Error: err.Error()})
			if derr == nil {
				_, derr = log.Append(ctx, dr)
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
