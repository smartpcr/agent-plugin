package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/internal/body"
)

// Reach says who may call a tool: the model directly, a script through the tool
// endpoint, or both (DH-ADR-TOOLS-1).
type Reach string

// The reaches; the zero Reach of a Spec means ReachBoth.
const (
	ReachDirect Reach = "direct"
	ReachScript Reach = "script"
	ReachBoth   Reach = "both"
)

// Disclosure says how much of a tool the model is told up front (DH-D-TOOLS-6).
type Disclosure string

// The disclosures; the zero Disclosure of a Spec means Full.
const (
	// Full: the schema is in every request of a step that sees the tool.
	Full Disclosure = "full"
	// Deferred: the tool is absent from the request header until disclosed.
	Deferred Disclosure = "deferred"
)

// Effect is one kind of side effect a tool may have; a guard can read them.
type Effect string

// The closed set of effects.
const (
	EffectNone    Effect = "none"
	EffectFiles   Effect = "files"
	EffectProcess Effect = "process"
	EffectNetwork Effect = "network"
	EffectSession Effect = "session"
	EffectConfig  Effect = "config"
)

var effects = []Effect{EffectNone, EffectFiles, EffectProcess, EffectNetwork, EffectSession, EffectConfig}

// Target is what a step view is resolved for: the agent, its scope and the
// step's model selection. A When condition receives it.
type Target struct {
	Agent agent.Ref
	Scope scope.Key
	Model agent.ModelSelection
}

// Clone is a deep copy: the model controls' pointers and stop list are not
// shared, so a step view's target cannot be changed through a caller's copy.
func (t Target) Clone() Target {
	t.Model.Controls = t.Model.Controls.Clone()
	return t
}

// Run is what the pipeline hands a body besides its arguments.
type Run struct {
	// CallID is the id of the call being run.
	CallID string
	// Agent is the agent the call is attributed to.
	Agent agent.Ref
}

// Overlap classifies one call for the batch scheduler (DH-ADR-AGENT-11): an
// exclusive barrier, free to overlap with anything, or the resource keys it reads
// and writes. The zero Overlap is Exclusive.
type Overlap struct {
	free          bool
	reads, writes []string
}

// Exclusive runs alone; it is also the classification of a call whose Overlap
// function is nil, fails or panics.
var Exclusive = Overlap{}

// Free overlaps with any call.
var Free = Overlap{free: true}

// Reads is a call that reads the given resource keys.
func Reads(keys ...string) Overlap { return Overlap{reads: normKeys(keys)} }

// Writes is a call that writes the given resource keys.
func Writes(keys ...string) Overlap { return Overlap{writes: normKeys(keys)} }

// With is o plus the keys of p; Exclusive absorbs, Free is the identity.
func (o Overlap) With(p Overlap) Overlap {
	switch {
	case o.IsExclusive() || p.IsExclusive():
		return Exclusive
	case o.free:
		return p
	case p.free:
		return o
	}
	return Overlap{reads: normKeys(append(slices.Clone(o.reads), p.reads...)),
		writes: normKeys(append(slices.Clone(o.writes), p.writes...))}
}

// IsExclusive reports whether the call runs alone.
func (o Overlap) IsExclusive() bool { return !o.free && len(o.reads) == 0 && len(o.writes) == 0 }

// IsFree reports whether the call overlaps with anything.
func (o Overlap) IsFree() bool { return o.free }

// ReadKeys is the sorted resource keys the call reads; a fresh slice.
func (o Overlap) ReadKeys() []string { return slices.Clone(o.reads) }

// WriteKeys is the sorted resource keys the call writes; a fresh slice.
func (o Overlap) WriteKeys() []string { return slices.Clone(o.writes) }

// Conflicts reports whether o and p may not overlap: either is exclusive, or one
// writes a key the other reads or writes.
func (o Overlap) Conflicts(p Overlap) bool {
	if o.IsExclusive() || p.IsExclusive() {
		return true
	}
	hit := func(ws, ks []string) bool {
		for _, w := range ws {
			if _, ok := slices.BinarySearch(ks, w); ok {
				return true
			}
		}
		return false
	}
	return hit(o.writes, p.reads) || hit(o.writes, p.writes) || hit(p.writes, o.reads)
}

func normKeys(keys []string) []string {
	out := slices.Clone(keys)
	slices.Sort(out)
	return slices.Compact(out)
}

// Spec is the one Go value that declares a tool with typed input and output.
type Spec[In, Out any] struct {
	// Name is the model-facing name: 1 to 64 of [A-Za-z0-9_-].
	Name string
	// Description is the model-facing description; required.
	Description string
	// Run is the body; required.
	Run func(ctx context.Context, in In, run Run) (Out, error)
	// Overlap classifies one call from its validated arguments; nil means Exclusive.
	Overlap func(in In) Overlap
	// When is a step-time condition; nil means always. An error fails Resolve.
	When func(t Target) (bool, error)
	// Timeout is the call budget; 0 means the registry's default and NoDeadline
	// opts the tool out.
	Timeout time.Duration
	// Grace is how long the pipeline waits for the body to return after its
	// budget ends or the turn is cancelled, when it is longer than the
	// registry's grace (a tool that must stop a process tree); 0 is the
	// registry's.
	Grace time.Duration
	// BoundsOwnOutput says the body bounds its own result (a reader of spilled
	// text), so the spill policy skips it; the registry's ceiling still applies.
	BoundsOwnOutput bool
	// Reach is who may call the tool; "" means ReachBoth.
	Reach Reach
	// Disclosure is how much the model is told up front; "" means Full.
	Disclosure Disclosure
	// Effects is the closed set of side effects; empty means unknown.
	Effects []Effect
}

// NoDeadline, as a declaration's Timeout, opts the tool out of the call budget
// (DH-ADR-TOOLS-5); an operator override still applies. Opting out is explicit.
const NoDeadline time.Duration = -1

// DynamicSpec declares a tool whose schema arrives at run time (an MCP server, a
// plugin): the input schema is a JSON Schema document and the body receives the
// raw validated arguments.
type DynamicSpec struct {
	Name        string
	Description string
	// Origin names where the tool came from; required, recorded in the catalogue.
	Origin string
	// Schema is the input JSON Schema document; its root must be an object.
	Schema  json.RawMessage
	Run     func(ctx context.Context, args json.RawMessage, run Run) (json.RawMessage, error)
	Overlap func(args json.RawMessage) Overlap
	When    func(t Target) (bool, error)
	Timeout time.Duration
	Grace   time.Duration
	// BoundsOwnOutput: as Spec's.
	BoundsOwnOutput bool
	Reach           Reach
	// Disclosure defaults to Full.
	Disclosure Disclosure
	Effects    []Effect
}

// Declaration is the derived, immutable form of one tool. Its zero value is not
// usable; build one with Define or Dynamic.
type Declaration struct {
	name        string
	description string
	input       *schema.Type
	output      *schema.Type
	params      json.RawMessage
	reach       Reach
	disclosure  Disclosure
	effects     []Effect
	timeout     time.Duration
	grace       time.Duration
	boundsOwn   bool
	origin      string

	when     func(Target) (bool, error)
	classify func(json.RawMessage) Overlap
	decode   func(json.RawMessage) (any, error)
	run      func(ctx context.Context, in any, run Run) (any, error)

	err error
}

// Name is the model-facing name.
func (d *Declaration) Name() string { return d.name }

// Description is the model-facing description.
func (d *Declaration) Description() string { return d.description }

// Input is the derived input shape; nil when the declaration failed.
func (d *Declaration) Input() *schema.Type { return d.input }

// Output is the derived output shape; nil for a dynamic tool or a failed one.
func (d *Declaration) Output() *schema.Type { return d.output }

// Reach is who may call the tool.
func (d *Declaration) Reach() Reach { return d.reach }

// Disclosure is how much of the tool the model is told up front.
func (d *Declaration) Disclosure() Disclosure { return d.disclosure }

// Effects is the tool's declared side effects, sorted; a fresh slice.
func (d *Declaration) Effects() []Effect { return slices.Clone(d.effects) }

// Timeout is the declared budget, 0 for the registry's default, NoDeadline for
// none.
func (d *Declaration) Timeout() time.Duration { return d.timeout }

// Grace is the declared grace, 0 for the registry's.
func (d *Declaration) Grace() time.Duration { return d.grace }

// BoundsOwnOutput reports whether the body bounds its own result, so the spill
// policy skips it.
func (d *Declaration) BoundsOwnOutput() bool { return d.boundsOwn }

// Origin is "" for a compiled-in tool and the DynamicSpec's origin otherwise.
func (d *Declaration) Origin() string { return d.origin }

// Err is why the declaration cannot be registered, nil when it can. A declaration
// not built by Define or Dynamic (a zero value) has no schema and no body, and is
// refused here rather than by whatever reads it later.
func (d *Declaration) Err() error {
	if d.err != nil {
		return d.err
	}
	if d.input == nil || d.run == nil {
		return &Error{Code: CodeInvalidDeclaration, Tool: d.name, Msg: "not built by tools.Define or tools.Dynamic"}
	}
	return nil
}

// Schema is the tool as the model sees it: the canonical input document of
// package schema, byte-identical on every call.
func (d *Declaration) Schema() vocab.ToolSchema {
	return vocab.ToolSchema{Name: d.name, Description: d.description, Parameters: bytes.Clone(d.params)}
}

// Active evaluates the When condition for t; a panic is an error.
func (d *Declaration) Active(t Target) (ok bool, err error) {
	if d.when == nil {
		return true, nil
	}
	defer func() {
		if r := recover(); r != nil {
			ok, err = false, fmt.Errorf("when panicked: %v", r)
		}
	}()
	return d.when(t)
}

// Classify is the Overlap of one call with the given raw arguments: Exclusive when
// they do not decode, when the declaration has no Overlap function, or when it
// panics.
func (d *Declaration) Classify(args json.RawMessage) (o Overlap) {
	if d.classify == nil {
		return Exclusive
	}
	defer func() {
		if recover() != nil {
			o = Exclusive
		}
	}()
	return d.classify(args)
}

func init() {
	body.Of = func(decl any) (body.Body, bool) {
		d, ok := decl.(*Declaration)
		if !ok || d == nil || d.err != nil {
			return body.Body{}, false
		}
		return body.Body{
			Decode: d.decode,
			Run: func(ctx context.Context, in any, run any) (any, error) {
				r, _ := run.(Run)
				return d.run(ctx, in, r)
			},
		}, true
	}
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Define derives a declaration from spec. It never panics: a declaration that
// cannot be registered carries the reason, with every offending schema path, in
// Err, and Register returns it. A good declaration's name joins the compiled-in
// names Known reports.
func Define[In, Out any](spec Spec[In, Out]) *Declaration {
	d := base(spec.Name, spec.Description, spec.Reach, spec.Disclosure, spec.Effects, spec.Timeout, spec.When)
	d.grace, d.boundsOwn = spec.Grace, spec.BoundsOwnOutput
	var issues []string
	if spec.Run == nil {
		issues = append(issues, "the declaration has no body")
	}
	in, err := schema.ForType(reflect.TypeFor[In]())
	if err != nil {
		issues = append(issues, "input: "+err.Error())
	} else {
		d.input = in
		d.params = in.JSONSchema()
		issues = append(issues, d.checkParams()...)
	}
	out, err := schema.ForType(reflect.TypeFor[Out]())
	if err != nil {
		issues = append(issues, "output: "+err.Error())
	}
	d.output = out
	issues = append(d.checkBase(), issues...)
	if len(issues) > 0 {
		d.err = &Error{Code: CodeInvalidDeclaration, Tool: spec.Name, Issues: issues}
		return d
	}
	d.decode = func(args json.RawMessage) (any, error) {
		var v In
		if is := d.input.Decode(args, &v); len(is) > 0 {
			return nil, &Error{Code: CodeInvalidArguments, Tool: d.name, Issues: issueStrings(is)}
		}
		return v, nil
	}
	d.run = func(ctx context.Context, v any, run Run) (any, error) {
		typed, ok := v.(In)
		if !ok {
			return nil, &Error{Code: CodeInvalidArguments, Tool: d.name, Msg: fmt.Sprintf("arguments of type %T", v)}
		}
		return spec.Run(ctx, typed, run)
	}
	if spec.Overlap != nil {
		d.classify = func(args json.RawMessage) Overlap {
			var v In
			if is := d.input.Decode(args, &v); len(is) > 0 {
				return Exclusive
			}
			return spec.Overlap(v)
		}
	}
	compiled.Store(d.name, true)
	return d
}

// Dynamic derives a declaration for a tool whose schema arrives at run time. Its
// name is not compiled in; a toolOrder may name it only through a prefix declared
// with DeclarePrefix.
func Dynamic(spec DynamicSpec) *Declaration {
	d := base(spec.Name, spec.Description, spec.Reach, spec.Disclosure, spec.Effects, spec.Timeout, spec.When)
	d.grace, d.boundsOwn = spec.Grace, spec.BoundsOwnOutput
	d.origin = spec.Origin
	var issues []string
	if strings.TrimSpace(spec.Origin) == "" {
		issues = append(issues, "a dynamic tool needs an origin")
	}
	if spec.Run == nil {
		issues = append(issues, "the declaration has no body")
	}
	in, err := schema.FromJSON(spec.Schema)
	if err != nil {
		issues = append(issues, "input: "+err.Error())
	} else {
		d.input = in
		d.params = in.JSONSchema()
		issues = append(issues, d.checkParams()...)
	}
	issues = append(d.checkBase(), issues...)
	if len(issues) > 0 {
		d.err = &Error{Code: CodeInvalidDeclaration, Tool: spec.Name, Issues: issues}
		return d
	}
	d.decode = func(args json.RawMessage) (any, error) {
		if is := d.input.Validate(args); len(is) > 0 {
			return nil, &Error{Code: CodeInvalidArguments, Tool: d.name, Issues: issueStrings(is)}
		}
		return json.RawMessage(bytes.Clone(args)), nil
	}
	d.run = func(ctx context.Context, v any, run Run) (any, error) {
		raw, ok := v.(json.RawMessage)
		if !ok {
			return nil, &Error{Code: CodeInvalidArguments, Tool: d.name, Msg: fmt.Sprintf("arguments of type %T", v)}
		}
		return spec.Run(ctx, raw, run)
	}
	if spec.Overlap != nil {
		d.classify = func(args json.RawMessage) Overlap {
			if len(d.input.Validate(args)) > 0 {
				return Exclusive
			}
			return spec.Overlap(args)
		}
	}
	return d
}

func base(name, desc string, reach Reach, disc Disclosure, eff []Effect, timeout time.Duration, when func(Target) (bool, error)) *Declaration {
	if reach == "" {
		reach = ReachBoth
	}
	if disc == "" {
		disc = Full
	}
	e := slices.Clone(eff)
	slices.Sort(e)
	return &Declaration{name: name, description: desc, reach: reach, disclosure: disc,
		effects: slices.Compact(e), timeout: timeout, when: when}
}

// checkBase checks the fields every declaration has.
func (d *Declaration) checkBase() []string {
	var issues []string
	if !namePattern.MatchString(d.name) {
		issues = append(issues, fmt.Sprintf("name %q is not 1 to 64 of [A-Za-z0-9_-]", d.name))
	}
	if strings.TrimSpace(d.description) == "" {
		issues = append(issues, "the description is blank")
	}
	switch d.reach {
	case ReachDirect, ReachScript, ReachBoth:
	default:
		issues = append(issues, fmt.Sprintf("reach %q is not direct, script or both", d.reach))
	}
	switch d.disclosure {
	case Full, Deferred:
	default:
		issues = append(issues, fmt.Sprintf("disclosure %q is not full or deferred", d.disclosure))
	}
	for _, e := range d.effects {
		if !slices.Contains(effects, e) {
			issues = append(issues, fmt.Sprintf("effect %q is not one of %v", e, effects))
		}
	}
	if d.timeout < 0 && d.timeout != NoDeadline {
		issues = append(issues, "the timeout is negative and not tools.NoDeadline")
	}
	if d.grace < 0 {
		issues = append(issues, "the grace is negative")
	}
	return issues
}

// subsetMarker is how vocab.ToolSchema.Validate introduces the offending paths.
const subsetMarker = ": the parameters leave the model-facing subset: "

// checkParams checks that the input document is an object schema inside the
// model-facing subset and returns every offending path.
func (d *Declaration) checkParams() []string {
	err := vocab.ToolSchema{Name: "x", Description: d.description, Parameters: d.params}.Validate()
	if err == nil {
		return nil
	}
	msg := err.Error()
	if _, paths, ok := strings.Cut(msg, subsetMarker); ok {
		var out []string
		for _, p := range strings.Split(paths, "; ") {
			out = append(out, "input "+p)
		}
		return out
	}
	return []string{"input: " + strings.Replace(strings.TrimPrefix(msg, "vocab: "), "tool x", "the tool", 1)}
}

func issueStrings(is schema.Issues) []string {
	out := make([]string, 0, len(is))
	for _, i := range is {
		out = append(out, i.String())
	}
	return out
}

// compiled is the set of names declared by Define; prefixes the dynamic prefixes.
var (
	compiled sync.Map // string -> bool
	prefixMu sync.RWMutex
	prefixes []string
)

// DeclarePrefix records a name prefix under which dynamic tools appear (for
// example "mcp__"), so a toolOrder may name such a tool before it is registered.
func DeclarePrefix(prefix string) {
	if prefix == "" {
		return
	}
	prefixMu.Lock()
	defer prefixMu.Unlock()
	if !slices.Contains(prefixes, prefix) {
		prefixes = append(prefixes, prefix)
	}
}

// Known reports whether name is a compiled-in tool name or falls under a declared
// dynamic prefix. Config load checks a toolOrder with it (DH-D-TOOLS-5).
func Known(name string) bool {
	if _, ok := compiled.Load(name); ok {
		return true
	}
	prefixMu.RLock()
	defer prefixMu.RUnlock()
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return true
		}
	}
	return false
}
