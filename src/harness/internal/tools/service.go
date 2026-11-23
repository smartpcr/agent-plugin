package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// Service is the tool registry provided under Key by the tools.registry component.
// Registrations are effects of the caller's fiber, filed in the layer of the
// caller's scope (the global layer for an untagged context): they disappear when
// that fiber unloads, when the returned handle is disposed, or when the scope
// closes. ExecuteBatch is the guarded pipeline (DH-D-TOOLS-2): the one way to a
// tool body.
type Service interface {
	// Register files d in the layer of ctx's scope. A declaration with an Err, or
	// a second tool of d's name in that layer, is refused and the layer is left as
	// it was; a tool of the same name in an enclosing layer is shadowed.
	Register(ctx *cordis.Context, d *Declaration) (*cordis.EffectHandle, error)
	// RegisterSet files every declaration of ds in one step, or none of them.
	RegisterSet(ctx *cordis.Context, ds []*Declaration) (*cordis.EffectHandle, error)
	// Restrict filters the tools ctx's scope inherits from enclosing layers and
	// the global layer; the scope's own tools are spared. Restrictions on a scope
	// chain intersect. It needs a scoped context.
	Restrict(ctx *cordis.Context, f Filter) (*cordis.EffectHandle, error)
	// Resolve evaluates every visible tool's When once and returns the immutable
	// view of one step. A failing When fails it with tools/activation-failed and
	// no view is published.
	Resolve(ctx context.Context, t Target) (StepView, error)
	// Catalogue lists every registered tool once, sorted by name.
	Catalogue() []Entry

	// Decide files policy listener p, named id in results, in the layer of ctx's
	// scope; it answers for every call whose agent's scope chain holds the layer.
	Decide(ctx *cordis.Context, id string, p Policy) (*cordis.EffectHandle, error)
	// Guard files deny-only guard g, named id, like Decide.
	Guard(ctx *cordis.Context, id string, g Guard) (*cordis.EffectHandle, error)
	// Shape files shaper s, named id, like Decide.
	Shape(ctx *cordis.Context, id string, s Shaper) (*cordis.EffectHandle, error)
	// SetSpill files the spill seat s, named id, in the layer of ctx's scope:
	// one seat per layer, the nearest on the agent's scope chain answers, and a
	// second seat in the same layer is refused (DH-ADR-STORAGE-4).
	SetSpill(ctx *cordis.Context, id string, s SpillShaper) (*cordis.EffectHandle, error)
	// Detached lists the bodies that did not return within grace and may still
	// be running, oldest first (DH-ADR-TOOLS-4).
	Detached() []DetachedCall
	// Limits is the registry's configured budgets.
	Limits() Limits
	// ExecuteBatch runs b's calls through the guarded pipeline as one bounded
	// batch: calls start in the model's order, bodies overlap only when their
	// Overlap classifications do not conflict (at most Limits.MaxParallel at
	// once), and results commit in the model's order. It returns one Result per
	// call; each was appended as its tool/result. A refused call is a Result, not
	// an error: the error is for a malformed batch, or a log that refused a
	// tool/call or tool/result.
	ExecuteBatch(ctx context.Context, b Batch) (BatchResult, error)
}

// FinalShaper is the optional part of a Service that files a final shaper: one
// that runs after every ordinary shaper of the call's scope chain, on the text they
// left, a Block's text included, so no ordinary shaper registered later sees or
// rebuilds what it changed. Final shapers run nearest layer first and in
// registration order within a layer, also on a call refused at resolve. The secret
// mask (human.secret.redact) is one: its masking is the last word on the text.
type FinalShaper interface {
	ShapeFinal(ctx *cordis.Context, id string, s Shaper) (*cordis.EffectHandle, error)
}

// Key is the tool registry's service key.
var Key = cordis.NewServiceKey[Service]("tools")

// Limits is the registry's budgets (areas/tools.md sec. 6.2).
type Limits struct {
	// MaxResultBytes is the ceiling on the model-facing text of any result.
	MaxResultBytes int
	// MaxArgumentBytes bounds a call's raw argument bytes.
	MaxArgumentBytes int
	// DefaultTimeout is the budget of a tool that declares none.
	DefaultTimeout time.Duration
	// Grace is how long a cancelled body is waited for before it is detached.
	Grace time.Duration
	// MaxParallel caps the bodies of one batch that overlap.
	MaxParallel int
}

// DetachedCall is a body that did not return within grace: its outcome is
// unknown and it may still be running. It is listed until it returns.
type DetachedCall struct {
	CallID string
	Tool   string
	Agent  agent.Ref
	// Since is when it was detached.
	Since time.Time
}

// StepView is the immutable set of tools one agent may use in one step. The
// prompt, the pipeline and nested calls all read the same view.
type StepView interface {
	// Target is what the view was resolved for.
	Target() Target
	// Tools is the visible, active tools, sorted by name; a fresh slice.
	Tools() []*Declaration
	// Lookup is the visible, active tool of that name; false after Release.
	Lookup(name string) (*Declaration, bool)
	// Release ends the driver's hold at step end; it is idempotent.
	Release()
}

// Filter is a restriction over inherited tools: when Allow is non-nil only the
// names it lists pass, and no name Deny lists passes.
type Filter struct {
	Allow []string
	Deny  []string
}

// Admits reports whether the filter lets name through.
func (f Filter) Admits(name string) bool {
	for _, d := range f.Deny {
		if d == name {
			return false
		}
	}
	if f.Allow == nil {
		return true
	}
	for _, a := range f.Allow {
		if a == name {
			return true
		}
	}
	return false
}

// Entry is one catalogue row: what the generated tool catalogue
// (DH-ADR-ARCH-12) and diagnostics print.
type Entry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Origin      string          `json:"origin,omitempty"`
	Reach       Reach           `json:"reach"`
	Disclosure  Disclosure      `json:"disclosure"`
	Effects     []Effect        `json:"effects,omitempty"`
	Fingerprint string          `json:"fingerprint"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChangedArgs is the payload of Changed.
type ChangedArgs struct {
	// Scope is the scope whose layer changed; zero for the global layer.
	Scope scope.Key
}

// Changed is notified after a registration or restriction change commits or is
// undone; listeners see it when the changed scope is visible to them.
var Changed = scope.NewEvent[ChangedArgs]("tools/changed")

// Settled is notified once for every call that reached the pipeline, a refused
// call included, after its tool/result is appended (stage 7 of DH-D-TOOLS-2),
// in the batch's commit order. It is dispatched about the call's agent's scope.
// Observers see an immutable Settlement and cannot change the result: one that
// fails or panics is logged on its own fiber's logger, which names it, and
// neither the result, the batch nor a later observer is affected.
var Settled = scope.NewEvent[Settlement]("tools/settled")

// Settlement is one settled call as Settled carries it. Its accessors return
// copies, so nothing an observer does reaches the result, the log or another
// observer.
type Settlement struct {
	call    Call
	result  Result
	value   func() any
	durable string
}

// NewSettlement is the settlement of call c with result r, whose tool/result
// payload was appended as durable (its encoded JSON). The result's value is
// copied once here and again on each read; a value that cannot be copied is
// read as nil.
func NewSettlement(c Call, r Result, durable string) Settlement {
	s := Settlement{call: c, result: copyResult(r), durable: durable}
	s.result.Value = nil
	if snap, err := isolate(r.Value); err == nil && snap != nil {
		s.value = func() any {
			v, _ := isolate(snap)
			return v
		}
	}
	return s
}

// Call is the settled call.
func (s Settlement) Call() Call { return s.call }

// Result is a copy of the call's result as returned to the batch's caller, the
// Seq of its tool/result included.
func (s Settlement) Result() Result {
	r := copyResult(s.result)
	if s.value != nil {
		r.Value = s.value()
	}
	return r
}

// Durable is the tool/result payload exactly as appended: the result's encoded
// durable form, produced once before Settled.
func (s Settlement) Durable() json.RawMessage { return json.RawMessage(s.durable) }

// copyResult is r with its spill reference and parts not shared.
func copyResult(r Result) Result {
	if r.Spill != nil {
		sp := *r.Spill
		r.Spill = &sp
	}
	r.Parts = CloneParts(r.Parts)
	return r
}

// CloneParts is a deep copy of parts; nil stays nil.
func CloneParts(parts []vocab.Part) []vocab.Part {
	if parts == nil {
		return nil
	}
	out := make([]vocab.Part, 0, len(parts))
	for _, p := range parts {
		var c vocab.Part
		if p.Text != nil {
			t := *p.Text
			c.Text = &t
		}
		if p.Image != nil {
			i := *p.Image
			c.Image = &i
		}
		out = append(out, c)
	}
	return out
}
