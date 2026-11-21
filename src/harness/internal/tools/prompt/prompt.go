// Package prompt is the tools.prompt component (DH-D-TOOLS-5, DH-ADR-TOOLS-8):
// scoped registries of system-prompt sections, {{name}} variables and runtime
// contexts, and the deterministic assembly of one step's system prompt and tool
// list.
//
// Assemble is a pure function of the registries, the config and the step view: the
// same inputs give the same bytes, so the provider's prefix cache holds and the log
// can reproduce the request. A component changes the result in three ways only: a
// scoped section or variable shadows the global one of the same name; Suppress
// removes an optional runtime context for a scope; one Complete section replaces
// every section. There is no assembly event and no change event.
package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Name is the component's catalogue name.
const Name = "tools.prompt"

// Key is the prompt service's key.
var Key = cordis.NewServiceKey[*Service]("tools.prompt")

// Component is tools.prompt: it injects the scope table, provides a *Service under
// Key built from its Config, and applies a live config change in place so the next
// assembly uses it.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	tab, err := scope.TableKey.Get(ctx)
	if err != nil {
		return err
	}
	tab.Declare(Key.Name(), scope.Layered)
	s, err := New(ctx, tab, cfg)
	if err != nil {
		return err
	}
	if _, err := cordis.AcceptUpdate(ctx, func(next Config) { s.store(next) }); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, s)
	return err
}, cordis.WithInject(scope.TableKey.Name()), cordis.WithDefaults(Defaults))

// Code is a "<domain>/<reason>" error code.
type Code string

// The codes assembly and registration raise.
const (
	CodeUnknownVariable   Code = "prompt/unknown-variable"
	CodeValuelessVariable Code = "prompt/valueless-variable"
	CodeMalformed         Code = "prompt/malformed"
	CodeTwoComplete       Code = "prompt/two-complete"
	CodeDuplicate         Code = "prompt/duplicate"
	CodeInvalid           Code = "prompt/invalid"
	CodeNotAssembled      Code = "prompt/not-assembled"
)

// Error carries a Code and what it is about; errors.Is matches a sentinel.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Msg == "" && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrUnknownVariable   = &Error{Code: CodeUnknownVariable}
	ErrValuelessVariable = &Error{Code: CodeValuelessVariable}
	ErrMalformed         = &Error{Code: CodeMalformed}
	ErrTwoComplete       = &Error{Code: CodeTwoComplete}
	ErrDuplicate         = &Error{Code: CodeDuplicate}
	ErrInvalid           = &Error{Code: CodeInvalid}
	ErrNotAssembled      = &Error{Code: CodeNotAssembled}
)

// Input is what one assembly is for: the step view, which carries the agent, its
// scope and the model selection.
type Input struct {
	View tools.StepView
}

// Target is the view's target, zero without a view.
func (in Input) Target() tools.Target {
	if in.View == nil {
		return tools.Target{}
	}
	return in.View.Target()
}

// Section is a named, ordered piece of system-prompt text.
type Section struct {
	Name  string
	Order int
	// Text is the section's text; Func, when set, computes it per assembly
	// instead. Text that comes out empty leaves the section out.
	Text string
	Func func(in Input) (string, error)
	// Literal sections are not interpolated.
	Literal bool
	// Complete replaces every section of the assembly; two visible complete
	// sections fail it.
	Complete bool
}

// Variable is the value of one {{name}}; ok false means it has none, which fails
// an assembly that references it.
type Variable func(in Input) (value string, ok bool, err error)

// RuntimeContext is a changing fact delivered to the model as a user-role
// snapshot, outside the cached system prompt.
type RuntimeContext struct {
	Name  string
	Order int
	// Optional contexts may be removed for a scope with Suppress.
	Optional bool
	// Func computes the snapshot; ok false leaves it out of this assembly.
	Func func(in Input) (text string, ok bool, err error)
}

// Assembly is one step's system prompt and tool list.
type Assembly struct {
	// Sections is the rendered sections in order.
	Sections []RenderedSection `json:"sections"`
	// Variables is every variable the sections referenced, sorted by name.
	Variables []ResolvedVariable `json:"variables,omitempty"`
	// Contexts is the runtime-context snapshots in order.
	Contexts []Snapshot `json:"contexts,omitempty"`
	// Tools is the model-facing declarations in order.
	Tools []vocab.ToolSchema `json:"tools"`
	// Fingerprint is "sha256:<hex>" of the rendered sections and the tools.
	Fingerprint string `json:"fingerprint"`
}

// RenderedSection is one section after interpolation.
type RenderedSection struct {
	Name     string `json:"name"`
	Order    int    `json:"order"`
	Owner    string `json:"owner"`
	Complete bool   `json:"complete,omitempty"`
	Text     string `json:"text"`
}

// ResolvedVariable is one variable's value in an assembly.
type ResolvedVariable struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	Value string `json:"value"`
}

// Snapshot is one runtime context's text in an assembly.
type Snapshot struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	Text  string `json:"text"`
}

// ManifestEntry names one section of an assembly and its owning component.
type ManifestEntry struct {
	Name     string `json:"name"`
	Order    int    `json:"order"`
	Owner    string `json:"owner"`
	Complete bool   `json:"complete,omitempty"`
}

// Manifest is the section names, orders and owners, in order.
func (a Assembly) Manifest() []ManifestEntry {
	out := make([]ManifestEntry, len(a.Sections))
	for i, s := range a.Sections {
		out[i] = ManifestEntry{Name: s.Name, Order: s.Order, Owner: s.Owner, Complete: s.Complete}
	}
	return out
}

// owned is a registered value and the component that registered it.
type owned[V any] struct {
	owner string
	v     V
}

type layer struct {
	mu         sync.RWMutex
	sections   map[string]owned[Section]
	vars       map[string]owned[Variable]
	contexts   map[string]owned[RuntimeContext]
	suppressed map[string]int
}

func newLayer() *layer {
	return &layer{sections: map[string]owned[Section]{}, vars: map[string]owned[Variable]{},
		contexts: map[string]owned[RuntimeContext]{}, suppressed: map[string]int{}}
}

// Service is the prompt registries and assembly.
type Service struct {
	layers *scope.Layers[*layer]
	cfg    atomic.Pointer[Config]
}

// New is a service over the scope table tab with config cfg. The identity line
// and the persona prefix and suffix are registered as global sections of owner,
// computed from the config current at each assembly.
func New(owner *cordis.Context, tab scope.Table, cfg Config) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Service{layers: scope.NewLayers(tab, newLayer)}
	s.store(cfg)
	builtins := []Section{
		{Name: SectionIdentity, Order: OrderIdentity, Literal: true, Func: func(Input) (string, error) {
			if s.Config().IncludeHarnessIdentity {
				return IdentityLine, nil
			}
			return "", nil
		}},
		{Name: SectionPersonaPrefix, Order: OrderPersonaPrefix, Literal: true, Func: func(Input) (string, error) {
			return s.Config().PersonaPrefix, nil
		}},
		{Name: SectionPersonaSuffix, Order: OrderPersonaSuffix, Literal: true, Func: func(Input) (string, error) {
			return s.Config().PersonaSuffix, nil
		}},
	}
	for _, b := range builtins {
		if _, err := s.Section(owner, b); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Config is the config the next assembly uses.
// The caller owns the returned copy: changing its ToolOrder changes nothing here.
func (s *Service) Config() Config { return s.cfg.Load().clone() }

// Configure validates cfg and makes the next assembly use it.
func (s *Service) Configure(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.store(cfg)
	return nil
}

// store keeps a private copy of cfg, so the caller's slice cannot change the
// validated ordering behind the service's back.
func (s *Service) store(cfg Config) {
	c := cfg.clone()
	s.cfg.Store(&c)
}

func ownerOf(ctx *cordis.Context) string { return ctx.Fiber().Name() }

// add files one named value in the layer of ctx's scope, refusing a second of the
// same name in that layer.
func add[V any](s *Service, ctx *cordis.Context, kind, name string, pick func(*layer) map[string]owned[V], v V) (*cordis.EffectHandle, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalid, Msg: "a context is required"}
	}
	o := owned[V]{owner: ownerOf(ctx), v: v}
	k := scope.Of(ctx)
	var refused error
	h, err := s.layers.Add(ctx, "prompt "+kind+" "+name, func(l *layer) func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		m := pick(l)
		if _, taken := m[name]; taken {
			refused = &Error{Code: CodeDuplicate, Msg: fmt.Sprintf("%s %q is already registered in %s", kind, name, k)}
			return nil
		}
		m[name] = o
		return func() {
			l.mu.Lock()
			delete(pick(l), name)
			l.mu.Unlock()
		}
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		h.Dispose()
		return nil, refused
	}
	return h, nil
}

// Section registers sec in the layer of ctx's scope; it shadows a section of the
// same name in an enclosing layer.
func (s *Service) Section(ctx *cordis.Context, sec Section) (*cordis.EffectHandle, error) {
	if strings.TrimSpace(sec.Name) == "" || strings.TrimSpace(sec.Name) != sec.Name {
		return nil, &Error{Code: CodeInvalid, Msg: fmt.Sprintf("section name %q is blank or padded", sec.Name)}
	}
	return add(s, ctx, "section", sec.Name, func(l *layer) map[string]owned[Section] { return l.sections }, sec)
}

// Variable registers {{name}} in the layer of ctx's scope.
func (s *Service) Variable(ctx *cordis.Context, name string, fn Variable) (*cordis.EffectHandle, error) {
	if !varName.MatchString(name) || fn == nil {
		return nil, &Error{Code: CodeInvalid, Msg: fmt.Sprintf("variable %q has a malformed name or no function", name)}
	}
	return add(s, ctx, "variable", name, func(l *layer) map[string]owned[Variable] { return l.vars }, fn)
}

// Context registers a runtime context in the layer of ctx's scope.
func (s *Service) Context(ctx *cordis.Context, c RuntimeContext) (*cordis.EffectHandle, error) {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Name) != c.Name || c.Func == nil {
		return nil, &Error{Code: CodeInvalid, Msg: fmt.Sprintf("runtime context %q has a blank or padded name or no function", c.Name)}
	}
	return add(s, ctx, "context", c.Name, func(l *layer) map[string]owned[RuntimeContext] { return l.contexts }, c)
}

// Suppress removes the optional runtime context name from every assembly at ctx's
// scope and the scopes that enclose it; a non-optional context is kept.
func (s *Service) Suppress(ctx *cordis.Context, name string) (*cordis.EffectHandle, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalid, Msg: "a context is required"}
	}
	return s.layers.Add(ctx, "prompt suppress "+name, func(l *layer) func() {
		l.mu.Lock()
		l.suppressed[name]++
		l.mu.Unlock()
		return func() {
			l.mu.Lock()
			if l.suppressed[name]--; l.suppressed[name] <= 0 {
				delete(l.suppressed, name)
			}
			l.mu.Unlock()
		}
	})
}

// visible is the nearest binding of every name of one kind along the chain.
func visible[V any](chain []*layer, pick func(*layer) map[string]owned[V]) map[string]owned[V] {
	out := map[string]owned[V]{}
	for _, l := range chain {
		l.mu.RLock()
		for name, o := range pick(l) {
			if _, ok := out[name]; !ok {
				out[name] = o
			}
		}
		l.mu.RUnlock()
	}
	return out
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Assemble computes one step's assembly from the registries visible at the view's
// scope, the config and the view's tools.
func (s *Service) Assemble(ctx context.Context, in Input) (Assembly, error) {
	if err := ctx.Err(); err != nil {
		return Assembly{}, err
	}
	if in.View == nil {
		return Assembly{}, &Error{Code: CodeInvalid, Msg: "a step view is required"}
	}
	cfg := s.Config()
	chain := s.layers.Chain(in.Target().Scope)

	secs := visible(chain, func(l *layer) map[string]owned[Section] { return l.sections })
	vars := visible(chain, func(l *layer) map[string]owned[Variable] { return l.vars })

	var order []owned[Section]
	var complete []string
	for _, name := range sortedNames(secs) {
		o := secs[name]
		if o.v.Complete {
			complete = append(complete, name)
		}
		order = append(order, o)
	}
	switch {
	case len(complete) > 1:
		return Assembly{}, &Error{Code: CodeTwoComplete, Msg: "complete sections " + strings.Join(complete, ", ")}
	case len(complete) == 1:
		order = []owned[Section]{secs[complete[0]]}
	}
	slices.SortStableFunc(order, func(a, b owned[Section]) int {
		if a.v.Order != b.v.Order {
			return a.v.Order - b.v.Order
		}
		return strings.Compare(a.v.Name, b.v.Name)
	})

	resolved := map[string]ResolvedVariable{}
	lookup := func(name string) (string, error) {
		if r, ok := resolved[name]; ok {
			return r.Value, nil
		}
		o, ok := vars[name]
		if !ok {
			return "", &Error{Code: CodeUnknownVariable, Msg: "{{" + name + "}}"}
		}
		v, has, err := o.v(in)
		if err != nil {
			return "", fmt.Errorf("prompt: variable %s (%s): %w", name, o.owner, err)
		}
		if !has {
			return "", &Error{Code: CodeValuelessVariable, Msg: "{{" + name + "}} (" + o.owner + ")"}
		}
		resolved[name] = ResolvedVariable{Name: name, Owner: o.owner, Value: v}
		return v, nil
	}

	var a Assembly
	for _, o := range order {
		text := o.v.Text
		if o.v.Func != nil {
			var err error
			if text, err = o.v.Func(in); err != nil {
				return Assembly{}, fmt.Errorf("prompt: section %s (%s): %w", o.v.Name, o.owner, err)
			}
		}
		if !o.v.Literal {
			var err error
			if text, err = interpolate(text, lookup); err != nil {
				if pe, ok := err.(*Error); ok {
					return Assembly{}, &Error{Code: pe.Code, Msg: "section " + o.v.Name + ": " + pe.Msg}
				}
				return Assembly{}, fmt.Errorf("prompt: section %s: %w", o.v.Name, err)
			}
		}
		if text == "" {
			continue
		}
		a.Sections = append(a.Sections, RenderedSection{Name: o.v.Name, Order: o.v.Order, Owner: o.owner, Complete: o.v.Complete, Text: text})
	}
	for _, name := range sortedNames(resolved) {
		a.Variables = append(a.Variables, resolved[name])
	}

	if cfg.IncludeRuntimeContext {
		ctxs := visible(chain, func(l *layer) map[string]owned[RuntimeContext] { return l.contexts })
		suppressed := map[string]bool{}
		for _, l := range chain {
			l.mu.RLock()
			for name := range l.suppressed {
				suppressed[name] = true
			}
			l.mu.RUnlock()
		}
		var list []owned[RuntimeContext]
		for _, name := range sortedNames(ctxs) {
			if o := ctxs[name]; !(o.v.Optional && suppressed[name]) {
				list = append(list, o)
			}
		}
		slices.SortStableFunc(list, func(a, b owned[RuntimeContext]) int { return a.v.Order - b.v.Order })
		for _, o := range list {
			text, ok, err := o.v.Func(in)
			if err != nil {
				return Assembly{}, fmt.Errorf("prompt: runtime context %s (%s): %w", o.v.Name, o.owner, err)
			}
			if ok && text != "" {
				a.Contexts = append(a.Contexts, Snapshot{Name: o.v.Name, Owner: o.owner, Text: text})
			}
		}
	}

	for _, d := range orderTools(in.View.Tools(), cfg.ToolOrder) {
		a.Tools = append(a.Tools, d.Schema())
	}
	fp, err := fingerprint(a)
	if err != nil {
		return Assembly{}, err
	}
	a.Fingerprint = fp
	return a, nil
}

// orderTools drops deferred tools and orders the rest by toolOrder, or by name
// when it is unset. A named tool that is not in the view is skipped.
func orderTools(ds []*tools.Declaration, order []string) []*tools.Declaration {
	var full []*tools.Declaration
	for _, d := range ds {
		if d.Disclosure() == tools.Full {
			full = append(full, d)
		}
	}
	slices.SortFunc(full, func(a, b *tools.Declaration) int { return strings.Compare(a.Name(), b.Name()) })
	if len(order) == 0 {
		return full
	}
	byName := map[string]*tools.Declaration{}
	for _, d := range full {
		byName[d.Name()] = d
	}
	listed := map[string]bool{}
	for _, n := range order {
		listed[n] = true
	}
	out := make([]*tools.Declaration, 0, len(full))
	for _, n := range order {
		if n == Unlisted {
			for _, d := range full {
				if !listed[d.Name()] {
					out = append(out, d)
				}
			}
			continue
		}
		if d, ok := byName[n]; ok {
			out = append(out, d)
		}
	}
	return out
}

// Render is the system prompt text of a: its sections joined by a blank line. An
// assembly that Assemble did not produce, or that was changed since, is refused.
func (s *Service) Render(a Assembly) (string, error) {
	fp, err := fingerprint(a)
	if err != nil {
		return "", err
	}
	if a.Fingerprint == "" || fp != a.Fingerprint {
		return "", &Error{Code: CodeNotAssembled, Msg: "the assembly's fingerprint does not match its content"}
	}
	texts := make([]string, len(a.Sections))
	for i, sec := range a.Sections {
		texts[i] = sec.Text
	}
	return strings.Join(texts, "\n\n"), nil
}

// fingerprint hashes what the model's cached prefix holds: the sections' names and
// texts and the tools in order. Runtime contexts are outside the prefix.
func fingerprint(a Assembly) (string, error) {
	type sec struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	body := struct {
		Sections []sec              `json:"sections"`
		Tools    []vocab.ToolSchema `json:"tools"`
	}{Tools: a.Tools}
	for _, s := range a.Sections {
		body.Sections = append(body.Sections, sec{s.Name, s.Text})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("prompt: fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
