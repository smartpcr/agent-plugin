package tools

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// The guarded pipeline (DH-D-TOOLS-2, architecture.md sec. 5.5): every call runs
// the stages below in this fixed order, and a call stopped at one stage runs none
// of the later ones except Shape, which every call reaches exactly once so that its
// tool/result is appended. Denial is enforced here, in the executor: a tool that
// needs approval stays in the step's tool list, and its call is refused when
// nobody approves it (SP-6, SP-7).

// Stage names one stage of the pipeline.
type Stage string

// The stages, in their fixed order.
const (
	// StageResolve looks the name up in the step's fixed view and checks reach.
	// It appends tool/call, with the raw argument bytes, for a resolved call.
	StageResolve Stage = "resolve"
	// StageValidate bounds the argument bytes and strictly decodes and validates them.
	StageValidate Stage = "validate"
	// StagePolicy asks the policy listeners on the scope chain.
	StagePolicy Stage = "policy"
	// StageGuards runs the deny-only guards on the scope chain.
	StageGuards Stage = "guards"
	// StageApproval asks the approval service, only for a call a policy asked about.
	StageApproval Stage = "approval"
	// StageFlush takes the tool-body checkpoint: the log so far is durable.
	StageFlush Stage = "flush"
	// StageBody runs the tool's body.
	StageBody Stage = "body"
	// StageShape validates and renders the result, runs the shapers and appends
	// tool/result.
	StageShape Stage = "shape"
)

// Stages is every stage in pipeline order.
var Stages = []Stage{StageResolve, StageValidate, StagePolicy, StageGuards, StageApproval, StageFlush, StageBody, StageShape}

// Origin is who asked for a call.
type Origin string

// The origins.
const (
	OriginModel  Origin = "model"
	OriginUser   Origin = "user"
	OriginPlugin Origin = "plugin"
	// OriginScript is a call a running body made through the tool endpoint.
	OriginScript Origin = "script"
)

// reaches reports whether a tool of reach r may be called from origin o: a
// script reaches only script tools, everyone else only direct ones.
func reaches(r Reach, o Origin) bool {
	if o == OriginScript {
		return r == ReachScript || r == ReachBoth
	}
	return r == ReachDirect || r == ReachBoth
}

// Reaches reports whether d may be called from origin o.
func (d *Declaration) Reaches(o Origin) bool { return reaches(d.reach, o) }

// Log is the session a batch writes its tool/call and tool/result events to, and
// the checkpoint flushes; session.Session satisfies it.
type Log interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
	Flush(ctx context.Context) (event.Offset, error)
}

// CallRequest is one call block, as the model wrote it.
type CallRequest struct {
	ID   string
	Name string
	// Arguments are the raw argument bytes. They are logged exactly as given and
	// never rewritten.
	Arguments string
}

// Batch is the calls of one step, run in the model's order against the step's
// fixed view.
type Batch struct {
	// View is the step's view: the only tools the batch can reach, attributed to
	// View.Target().Agent.
	View StepView
	// Log is the session of the step.
	Log Log
	// Origin is who made the calls; "" is OriginModel.
	Origin Origin
	// Parent is the outer call of nested calls, "" at top level.
	Parent string
	Calls  []CallRequest
}

// Result is the settled outcome of one call: exactly one per call, appended as
// its tool/result.
type Result struct {
	CallID string
	Name   string
	// Stage is where the call ended: the stage that stopped it, or StageShape
	// for a call whose body ran.
	Stage   Stage
	IsError bool
	// Code is the failure code, "" on success.
	Code Code
	// By names the component that ended the call: a policy, a guard, the
	// approval service or a shaper; "" when the pipeline itself did.
	By string
	// Text is the model-facing content.
	Text string
	// Value is the body's typed value on success.
	Value any
	// Approval is the approval outcome when the call was asked about.
	Approval string
	// Seq is the seq of the tool/result event.
	Seq event.Seq
}

// BatchResult holds one Result per call, in the batch's order.
type BatchResult struct {
	Results []Result
}

// Call is what a policy, guard or shaper sees of a call: read-only accessors that
// return copies, so a listener cannot change what was logged or what the body
// receives.
type Call struct {
	id, name, parent string
	origin           Origin
	agent            agent.Ref
	args             []byte
	decl             *Declaration
	input            any
}

// NewCall is a call as the pipeline hands it to its listeners; tests of a
// listener build theirs with it.
func NewCall(id, name string, args []byte, origin Origin, parent string, ag agent.Ref, d *Declaration, input any) Call {
	return Call{id: id, name: name, args: bytes.Clone(args), origin: origin, parent: parent, agent: ag, decl: d, input: input}
}

// ID is the call id.
func (c Call) ID() string { return c.id }

// Name is the tool's name.
func (c Call) Name() string { return c.name }

// Arguments is a copy of the raw argument bytes.
func (c Call) Arguments() json.RawMessage { return bytes.Clone(c.args) }

// Origin is who asked for the call.
func (c Call) Origin() Origin { return c.origin }

// Parent is the outer call id, "" at top level.
func (c Call) Parent() string { return c.parent }

// Agent is the attributed agent.
func (c Call) Agent() agent.Ref { return c.agent }

// Tool is the resolved declaration; nil for a call refused at resolve.
func (c Call) Tool() *Declaration { return c.decl }

// Input is the decoded argument value; nil before validation.
func (c Call) Input() any { return c.input }

// Verdict is a policy listener's closed answer.
type Verdict string

// The verdicts, from the weakest to the strongest when several listeners answer:
// Allow < Ask < Cancel < Deny.
const (
	VerdictAllow  Verdict = "allow"
	VerdictAsk    Verdict = "ask"
	VerdictCancel Verdict = "cancel"
	VerdictDeny   Verdict = "deny"
)

// Decision is a policy listener's answer; build it with Allow, Deny, Ask or
// Cancel.
type Decision struct {
	Verdict Verdict
	Reason  string
	// Code is a Deny's own code, carried in the refusal's text.
	Code Code
	// Display is what an Ask shows the answerer instead of the raw arguments.
	Display string
}

// Allow lets the call go on to the guards.
func Allow() Decision { return Decision{Verdict: VerdictAllow} }

// Deny refuses the call.
func Deny(code Code, reason string) Decision {
	return Decision{Verdict: VerdictDeny, Code: code, Reason: reason}
}

// Ask has the call approved before it runs.
func Ask(reason, display string) Decision {
	return Decision{Verdict: VerdictAsk, Reason: reason, Display: display}
}

// Cancel ends the call as aborted before start.
func Cancel(reason string) Decision { return Decision{Verdict: VerdictCancel, Reason: reason} }

// Policy is a policy listener. It sees the call read-only and returns a Decision;
// a panic or an unknown verdict means Deny.
type Policy func(ctx context.Context, c Call) Decision

// Denial is a guard's refusal.
type Denial struct {
	Code   Code
	Reason string
}

// Guard is a deny-only check: a non-nil Denial refuses the call, and nil lets it
// pass. A guard cannot allow what a policy denied. A panic means a denial.
type Guard func(ctx context.Context, c Call) *Denial

// Outcome is what a shaper sees of a settled call.
type Outcome struct {
	IsError bool
	Code    Code
	Text    string
	Value   any
}

// PostKind is a shaper's closed answer.
type PostKind string

// The shaper answers.
const (
	PostAccept         PostKind = "accept"
	PostReplaceContent PostKind = "replace-content"
	PostBlock          PostKind = "block"
)

// PostDecision is a shaper's answer; build it with Accept, ReplaceContent or
// Block.
type PostDecision struct {
	Kind   PostKind
	Text   string
	Reason string
}

// Accept keeps the outcome.
func Accept() PostDecision { return PostDecision{Kind: PostAccept} }

// ReplaceContent replaces the model-facing text; success stays success and
// failure stays failure.
func ReplaceContent(text string) PostDecision {
	return PostDecision{Kind: PostReplaceContent, Text: text}
}

// Block turns a success into a tools/blocked failure. It never turns a failure
// into a success.
func Block(reason string) PostDecision { return PostDecision{Kind: PostBlock, Reason: reason} }

// Shaper is a shaping listener over every settled call, refused ones included.
// A panic fails the call.
type Shaper func(c Call, o Outcome) PostDecision
