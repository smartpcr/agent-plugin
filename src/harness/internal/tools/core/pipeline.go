package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/internal/body"
)

// DefaultMaxArgumentBytes bounds a call's argument bytes when
// Config.MaxArgumentBytes is zero (areas/tools.md §6.2, maxArgumentBytes).
const DefaultMaxArgumentBytes = 1 << 20

// The defaults of the deadline and result budget (areas/tools.md §6.2).
const (
	DefaultTimeout        = 5 * time.Minute
	DefaultGrace          = 5 * time.Second
	DefaultMaxResultBytes = 256 << 10
)

// Clock is the time the deadlines are measured on; tests pass a manual clock.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Config configures the pipeline of a registry.
type Config struct {
	// MaxArgumentBytes bounds a call's raw argument bytes; zero is
	// DefaultMaxArgumentBytes.
	MaxArgumentBytes int
	// MaxResultBytes is the ceiling on a result's model-facing text, applied
	// after the shapers and the spill seat; zero is DefaultMaxResultBytes.
	MaxResultBytes int
	// DefaultTimeout is the budget of a tool that declares none; zero is
	// DefaultTimeout.
	DefaultTimeout time.Duration
	// Timeouts are the operator's per-tool overrides, by tool name: they win
	// over the declaration's Timeout (tools.NoDeadline among them).
	Timeouts map[string]time.Duration
	// Grace is how long a cancelled body is waited for before it is detached;
	// zero is DefaultGrace. A declaration's longer Grace wins.
	Grace time.Duration
	// Clock measures budgets and grace; nil is the wall clock.
	Clock Clock
	// Approval is the service an Ask is put to; nil is the one mounted under
	// approval.Key on the registry's context, or, when none is, a service with no
	// registry, which settles every request unavailable (SP-7).
	Approval *approval.Service
	// Checkpoint is the tool-body checkpoint; nil is the policy mounted on the
	// registry's context (checkpoint.Mounted).
	Checkpoint checkpoint.Policy
	// MaxParallel caps the bodies of one batch that overlap; zero is
	// DefaultMaxParallel, and one runs every batch serially.
	MaxParallel int
	// ForceExclusive names tools the operator runs exclusive whatever their
	// declared Overlap.
	ForceExclusive []string
}

// ByApproval names the approval service in a Result's By.
const ByApproval = "human.approval"

// producer names the registry as the producer of a tool/result message.
const producer = Name

// named is a listener and the id results name it by.
type named[F any] struct {
	id string
	f  F
}

func (r *registry) Decide(ctx *cordis.Context, id string, p tools.Policy) (*cordis.EffectHandle, error) {
	if p == nil || id == "" {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a policy listener needs its id and function"}
	}
	return addListener(r, ctx, "tools policy "+id, func(l *layer) map[uint64]named[tools.Policy] { return l.policies }, named[tools.Policy]{id, p})
}

func (r *registry) Guard(ctx *cordis.Context, id string, g tools.Guard) (*cordis.EffectHandle, error) {
	if g == nil || id == "" {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a guard needs its id and function"}
	}
	return addListener(r, ctx, "tools guard "+id, func(l *layer) map[uint64]named[tools.Guard] { return l.guards }, named[tools.Guard]{id, g})
}

func (r *registry) Shape(ctx *cordis.Context, id string, s tools.Shaper) (*cordis.EffectHandle, error) {
	if s == nil || id == "" {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a shaper needs its id and function"}
	}
	return addListener(r, ctx, "tools shaper "+id, func(l *layer) map[uint64]named[tools.Shaper] { return l.shapers }, named[tools.Shaper]{id, s})
}

// ShapeFinal files final shaper s, named id (tools.FinalShaper).
func (r *registry) ShapeFinal(ctx *cordis.Context, id string, s tools.Shaper) (*cordis.EffectHandle, error) {
	if s == nil || id == "" {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a final shaper needs its id and function"}
	}
	return addListener(r, ctx, "tools final shaper "+id, func(l *layer) map[uint64]named[tools.Shaper] { return l.finals }, named[tools.Shaper]{id, s})
}

// addListener files v in the layer of ctx's scope as an effect of ctx's fiber.
func addListener[F any](r *registry, ctx *cordis.Context, label string, of func(*layer) map[uint64]named[F], v named[F]) (*cordis.EffectHandle, error) {
	return r.layers.Add(ctx, label, func(l *layer) func() {
		l.mu.Lock()
		l.next++
		id := l.next
		of(l)[id] = v
		l.mu.Unlock()
		return func() {
			l.mu.Lock()
			delete(of(l), id)
			l.mu.Unlock()
		}
	})
}

// listeners is what the scope chain of one batch holds, nearest layer first and
// in registration order within a layer. It is taken once per batch, so every
// call of the batch is judged by the same set.
type listeners struct {
	policies []named[tools.Policy]
	guards   []named[tools.Guard]
	shapers  []named[tools.Shaper]
	finals   []named[tools.Shaper]
	spill    *named[tools.SpillShaper] // the nearest layer's seat
	// at is the scope of the batch's agent, which tools.Settled is about.
	at scope.Key
}

func collect[F any](m map[uint64]named[F]) []named[F] {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]named[F], 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}

func (r *registry) listenersAt(t tools.Target) listeners {
	ls := listeners{at: t.Scope}
	for _, l := range r.layers.Chain(t.Scope) {
		l.mu.RLock()
		ls.policies = append(ls.policies, collect(l.policies)...)
		ls.guards = append(ls.guards, collect(l.guards)...)
		ls.shapers = append(ls.shapers, collect(l.shapers)...)
		ls.finals = append(ls.finals, collect(l.finals)...)
		if ls.spill == nil && l.spill != nil {
			ls.spill = l.spill
		}
		l.mu.RUnlock()
	}
	return ls
}

func (r *registry) approvalService() *approval.Service {
	if r.cfg.Approval != nil {
		return r.cfg.Approval
	}
	if r.ctx != nil {
		if s, ok := approval.Key.Lookup(r.ctx); ok && s != nil {
			return s
		}
	}
	return approval.New(approval.Config{})
}

func (r *registry) checkpointPolicy() checkpoint.Policy {
	if r.cfg.Checkpoint != nil {
		return r.cfg.Checkpoint
	}
	return checkpoint.Mounted(r.ctx)
}

func (r *registry) ExecuteBatch(ctx context.Context, b tools.Batch) (tools.BatchResult, error) {
	if b.View == nil || b.Log == nil {
		return tools.BatchResult{}, &tools.Error{Code: tools.CodeInvalidBatch, Msg: "a batch needs its step view and log"}
	}
	seen := make(map[string]struct{}, len(b.Calls))
	for _, c := range b.Calls {
		if c.ID == "" || c.Name == "" {
			return tools.BatchResult{}, &tools.Error{Code: tools.CodeInvalidBatch, Msg: "every call needs its id and name"}
		}
		if _, dup := seen[c.ID]; dup {
			return tools.BatchResult{}, &tools.Error{Code: tools.CodeInvalidBatch, Msg: "call id " + c.ID + " repeats in the batch"}
		}
		seen[c.ID] = struct{}{}
	}
	if b.Origin == "" {
		b.Origin = tools.OriginModel
	}
	target := b.View.Target()
	ls := r.listenersAt(target)
	return r.schedule(ctx, b, target.Agent, ls)
}

// ended is how a call ended before Shape, or the body's outcome.
type ended struct {
	stage    tools.Stage
	code     tools.Code
	by       string
	msg      string
	value    any
	approval string
	grant    string
	cause    tools.Cause
}

func stop(stage tools.Stage, code tools.Code, by, msg string) ended {
	return ended{stage: stage, code: code, by: by, msg: msg}
}

// prepared is one call taken through the stages before its body: either how it
// ended there (run is nil) or the body still to run. Every prepared call is
// settled exactly once, which appends its one tool/result.
type prepared struct {
	call tools.Call
	e    ended
	run  func() ended
	// err is a log failure of the stages, joined with the settle's.
	err error
}

func early(call tools.Call, e ended) prepared { return prepared{call: call, e: e} }

// prepare takes one call through the stages before its body, in their fixed
// order. It runs on the batch's coordinator, so a batch's appends stay serial.
// ov is the call's declared classification, forceExclusive aside; a detached
// body keeps it.
func (r *registry) prepare(ctx context.Context, b tools.Batch, ag agent.Ref, ls listeners, req tools.CallRequest, ov tools.Overlap) prepared {
	args := []byte(req.Arguments)
	call := tools.NewCall(req.ID, req.Name, args, b.Origin, b.Parent, ag, nil, nil)

	// 1. Resolve: only the step's fixed view is searched. A miss never starts the
	// call, so no tool/call is appended and nothing later runs.
	d, ok := b.View.Lookup(req.Name)
	if !ok {
		why := "is not a tool of this step"
		if !r.registered(req.Name) {
			why = "is not a registered tool"
		}
		return early(call, stop(tools.StageResolve, tools.CodeNotStarted, "", fmt.Sprintf("%q %s", req.Name, why)))
	}
	if !d.Reaches(b.Origin) {
		return early(call, stop(tools.StageResolve, tools.CodeNotStarted, "",
			fmt.Sprintf("%q cannot be called from origin %s (reach %s)", req.Name, b.Origin, d.Reach())))
	}
	bd, ok := body.Of(d)
	if !ok {
		return early(call, stop(tools.StageResolve, tools.CodeNotStarted, "", fmt.Sprintf("%q has no body", req.Name)))
	}
	draft, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: req.ID, Name: req.Name, Arguments: req.Arguments})
	if err == nil {
		_, err = b.Log.Append(ctx, draft)
	}
	if err != nil {
		a := early(call, stop(tools.StageResolve, tools.CodeNotStarted, "", "the call could not be logged: "+err.Error()))
		a.err = fmt.Errorf("tools: append tool/call %s: %w", req.ID, err)
		return a
	}
	call = tools.NewCall(req.ID, req.Name, args, b.Origin, b.Parent, ag, d, nil)

	// 2. Bound, decode and validate the arguments.
	if len(args) > r.cfg.MaxArgumentBytes {
		return early(call, stop(tools.StageValidate, tools.CodeArgumentsTooLarge, "",
			fmt.Sprintf("%d argument bytes exceed the bound of %d", len(args), r.cfg.MaxArgumentBytes)))
	}
	in, err := decode(bd, args)
	if err != nil {
		code := tools.CodeInvalidArguments
		var te *tools.Error
		if errors.As(err, &te) && te.Code != "" {
			code = te.Code
		}
		return early(call, stop(tools.StageValidate, code, "", trimCode(err.Error(), code)))
	}
	// Listeners see deep copies of in, never in itself, and nothing is decoded
	// again: what one of them mutates reaches neither the others nor the body.
	isolated, err := tools.NewIsolatedCall(req.ID, req.Name, args, b.Origin, b.Parent, ag, d, in)
	if err != nil {
		return early(call, stop(tools.StageValidate, tools.CodeInvalidArguments, "",
			"the decoded arguments cannot be isolated from the listeners: "+err.Error()))
	}
	call = isolated
	if ctx.Err() != nil {
		return early(call, stop(tools.StagePolicy, tools.CodeAbortedBeforeStart, "", "cancelled: "+context.Cause(ctx).Error()))
	}

	// 3. Policy listeners: the strongest verdict wins, whatever the order.
	dec, by := decide(ctx, ls.policies, call)
	switch dec.Verdict {
	case tools.VerdictDeny:
		return early(call, stop(tools.StagePolicy, tools.CodeDenied, by, denyText(dec)))
	case tools.VerdictCancel:
		return early(call, stop(tools.StagePolicy, tools.CodeAbortedBeforeStart, by, dec.Reason))
	}

	// 4. Deny-only guards.
	for _, g := range ls.guards {
		if den := guard(ctx, g, call); den != nil {
			return early(call, stop(tools.StageGuards, tools.CodeDenied, g.id, denialText(den)))
		}
	}

	// 5. Approval, only for an Ask. Nobody to ask means no: a call attributed to
	// no agent has nobody to ask on its behalf, so it is unavailable without a
	// request. The policy's subject, if any, is what a session grant can match;
	// the approval service, not the pipeline, matches and creates grants.
	outcome, grant := "", ""
	if dec.Verdict == tools.VerdictAsk {
		if ag.IsZero() {
			e := stop(tools.StageApproval, tools.CodeApprovalUnavailable, ByApproval, "the call is attributed to no agent, so nobody can be asked to approve it")
			e.approval = string(approval.OutcomeUnavailable)
			return early(call, e)
		}
		display := dec.Display
		if display == "" {
			display = req.Arguments
		}
		ad, err := requestApproval(ctx, r.approvalService(), b.Log, approval.Request{Agent: ag, Tool: d.Name(), Call: req.ID,
			Parent: b.Parent, Origin: string(b.Origin), Reason: dec.Reason, Display: display, Subject: approvalSubject(dec.Subject)})
		outcome, grant = string(ad.Outcome), ad.Grant
		// A wait ended by the turn's context is an abort, settled below, not the
		// approver's refusal.
		if ctx.Err() == nil && (err != nil || !ad.Approved()) {
			e := stop(tools.StageApproval, approvalCode(ad.Outcome), ByApproval, approvalText(ad, err))
			e.approval = outcome
			return early(call, e)
		}
	}
	// asked is e with the approval it passed, for every later end of the call.
	asked := func(e ended) ended {
		e.approval, e.grant = outcome, grant
		return e
	}
	if ctx.Err() != nil {
		return early(call, asked(stop(tools.StageApproval, tools.CodeAbortedBeforeStart, "", "cancelled: "+context.Cause(ctx).Error())))
	}

	// 6. Checkpoint: everything logged so far, the approval audit included, is
	// durable before the body's first instruction.
	if err := checkpoint.Before(ctx, r.checkpointPolicy(), b.Log, checkpoint.ToolBody); err != nil {
		code := tools.CodeNotStarted
		var ce *checkpoint.Error
		if errors.As(err, &ce) {
			code = tools.Code(ce.Code)
		}
		return early(call, asked(stop(tools.StageFlush, code, "", err.Error())))
	}

	// 7. The body, under its budget: a body that does not return within grace
	// after its deadline or a cancellation is detached, and the call settles. A
	// tool whose detached body still runs for this agent is busy, and so is a
	// call whose resource keys conflict with such a body's.
	if why := r.busy(ag, d.Name(), ov); why != "" {
		return early(call, asked(stop(tools.StageBody, tools.CodeBusy, "", why)))
	}
	return prepared{call: call, run: func() ended {
		return asked(r.execute(ctx, b, d, bd, in, tools.Run{CallID: req.ID, Agent: ag}, ov))
	}}
}

// approvalSubject maps a policy's subject onto the approval service's (stage 4
// of DH-D-TOOLS-2); nil for none or one that is not Valid, so the request offers
// no session choice and matches no grant. The label goes with it, so the
// answerer shows the scope in words; nothing matches on it.
func approvalSubject(s *tools.Subject) *approval.Subject {
	if s == nil || !s.Valid() {
		return nil
	}
	if s.Kind == tools.SubjectApplication {
		p, a, _ := strings.Cut(s.Name, ":")
		return &approval.Subject{Kind: approval.ScopeApplication, Provider: p, App: a, Label: s.Label}
	}
	return &approval.Subject{Kind: approval.ScopeDirectory, Path: s.Name, Label: s.Label}
}

// registered reports whether any layer holds a tool of that name.
func (r *registry) registered(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for d := range r.live {
		if d.Name() == name {
			return true
		}
	}
	return false
}

func decode(bd body.Body, args []byte) (in any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &tools.Error{Code: tools.CodeInvalidArguments, Msg: fmt.Sprintf("validation panicked: %v", p)}
		}
	}()
	return bd.Decode(json.RawMessage(args))
}

func runBody(ctx context.Context, bd body.Body, in any, run tools.Run) (v any, err error) {
	defer func() {
		if p := recover(); p != nil {
			v, err = nil, &tools.Error{Code: tools.CodeFailed, Msg: fmt.Sprintf("the body panicked: %v", p)}
		}
	}()
	return bd.Run(ctx, in, run)
}

// strength orders the verdicts; an unknown one is a Deny.
func strength(v tools.Verdict) int {
	switch v {
	case tools.VerdictAllow:
		return 0
	case tools.VerdictAsk:
		return 1
	case tools.VerdictCancel:
		return 2
	}
	return 3
}

// decide asks every policy and keeps the strongest verdict, and the first
// listener to give it; no listener is an Allow. A panicking listener denies.
func decide(ctx context.Context, ps []named[tools.Policy], c tools.Call) (tools.Decision, string) {
	best, by := tools.Allow(), ""
	for _, p := range ps {
		d := policy(ctx, p, c)
		if strength(d.Verdict) > 3 || (d.Verdict != tools.VerdictAllow && d.Verdict != tools.VerdictAsk &&
			d.Verdict != tools.VerdictCancel && d.Verdict != tools.VerdictDeny) {
			d = tools.Deny(tools.CodeDenied, fmt.Sprintf("unknown verdict %q", d.Verdict))
		}
		if strength(d.Verdict) > strength(best.Verdict) {
			best, by = d, p.id
		}
	}
	return best, by
}

func policy(ctx context.Context, p named[tools.Policy], c tools.Call) (d tools.Decision) {
	defer func() {
		if x := recover(); x != nil {
			d = tools.Deny(tools.CodeDenied, fmt.Sprintf("the policy listener panicked: %v", x))
		}
	}()
	return p.f(ctx, c)
}

func guard(ctx context.Context, g named[tools.Guard], c tools.Call) (den *tools.Denial) {
	defer func() {
		if x := recover(); x != nil {
			den = &tools.Denial{Code: tools.CodeDenied, Reason: fmt.Sprintf("the guard panicked: %v", x)}
		}
	}()
	return g.f(ctx, c)
}

func requestApproval(ctx context.Context, s *approval.Service, log approval.Log, r approval.Request) (d approval.Decision, err error) {
	defer func() {
		if x := recover(); x != nil {
			d = approval.Decision{Outcome: approval.OutcomeUnavailable, Detail: fmt.Sprintf("the approval service panicked: %v", x)}
			err = fmt.Errorf("approval: %v", x)
		}
	}()
	return s.Request(ctx, log, r)
}

// approvalCode is the tool code of a refusing outcome (tools.md stage 4); an
// outcome outside the closed set, or an allowed one returned with an error, is
// unavailable.
func approvalCode(o approval.Outcome) tools.Code {
	switch o {
	case approval.OutcomeRejected:
		return tools.CodeApprovalRejected
	case approval.OutcomeCancelled:
		return tools.CodeApprovalCancelled
	}
	return tools.CodeApprovalUnavailable
}

func approvalText(d approval.Decision, err error) string {
	switch {
	case err != nil:
		return "the approval could not be recorded: " + err.Error()
	case d.Outcome == approval.OutcomeUnavailable && d.Detail == approval.DetailTimeout:
		return "nobody approved the call within its timeout"
	case d.Outcome == approval.OutcomeUnavailable:
		return "nobody could be asked to approve the call (" + d.Detail + ")"
	case d.Outcome == approval.OutcomeCancelled:
		return "the approval was cancelled before anyone answered (" + d.Detail + ")"
	case d.Outcome == approval.OutcomeRejected && d.Detail == approval.DetailPolicyNever:
		return "the session's approval policy is never: nobody is asked and the call is rejected"
	}
	s := "the call was not approved by " + d.By.Principal
	if d.Detail != "" {
		s += ": " + d.Detail
	}
	return s
}

func denyText(d tools.Decision) string {
	s := d.Reason
	if d.Code != "" && d.Code != tools.CodeDenied {
		s = string(d.Code) + ": " + s
	}
	return s
}

func denialText(d *tools.Denial) string {
	s := d.Reason
	if d.Code != "" && d.Code != tools.CodeDenied {
		s = string(d.Code) + ": " + s
	}
	return s
}

// trimCode drops a leading "<code>: " from msg; the result text adds it back once.
func trimCode(msg string, code tools.Code) string {
	return strings.TrimPrefix(msg, string(code)+": ")
}

// resultMeta is the structured part of a tool/result, carried in its message's
// source.
type resultMeta struct {
	Code         tools.Code   `json:"code,omitempty"`
	Stage        tools.Stage  `json:"stage"`
	By           string       `json:"by,omitempty"`
	Approval     string       `json:"approval,omitempty"`
	Grant        string       `json:"grant,omitempty"`
	Cause        tools.Cause  `json:"cause,omitempty"`
	Truncated    bool         `json:"truncated,omitempty"`
	Spill        *event.Spill `json:"spill,omitempty"`
	SpillFailure string       `json:"spillFailure,omitempty"`
}

// maxSpillFailure bounds the reason a spill failure notice quotes.
const maxSpillFailure = 200

// spillShape runs the spill seat; a panic is reported as failure, and the text
// is kept for the ceiling to bound.
func spillShape(ctx context.Context, s named[tools.SpillShaper], c tools.Call, o tools.Outcome) (sr tools.SpillResult, failure string) {
	defer func() {
		if x := recover(); x != nil {
			sr, failure = tools.SpillResult{}, fmt.Sprintf("the spill seat %s panicked: %v", s.id, x)
		}
	}()
	return s.f(ctx, c, o), ""
}

// boundedSpillShape runs the spill seat detached from the turn's cancellation
// but bounded by the registry's grace: a seat whose store stalls (a full disk,
// a hung mount) is cancelled and abandoned at grace, and reported as a spill
// failure so the ceiling and the not-saved notice still hold the turn to
// budget plus grace (DH-ADR-TOOLS-4).
func (r *registry) boundedSpillShape(ctx context.Context, s named[tools.SpillShaper], c tools.Call, o tools.Outcome) (tools.SpillResult, string) {
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	type out struct {
		sr      tools.SpillResult
		failure string
	}
	done := make(chan out, 1)
	go func() {
		sr, failure := spillShape(sctx, s, c, o)
		done <- out{sr, failure}
	}()
	select {
	case v := <-done:
		cancel()
		return v.sr, v.failure
	case <-r.cfg.Clock.After(r.cfg.Grace):
		cancel()
		return tools.SpillResult{}, fmt.Sprintf("the spill seat %s did not return within %s", s.id, r.cfg.Grace)
	}
}

// spillFailureNotice is the model-facing line saying the spill seat failed; it
// does not claim the full result was saved, since a seat that panicked may have
// saved it and lost the reference.
func spillFailureNotice(failure string) string {
	failure = strings.ReplaceAll(failure, "\n", " ")
	if len(failure) > maxSpillFailure {
		cut := maxSpillFailure
		for cut > 0 && !utf8.RuneStart(failure[cut]) {
			cut--
		}
		failure = failure[:cut] + "..."
	}
	return "\n[the full result was not saved for later reading: " + failure + "]"
}

// withNotice appends notice to text within limit bytes, cutting text (with the
// ceiling's marker) rather than the notice; ok reports whether text was cut.
func withNotice(text, notice string, limit int) (string, bool) {
	if len(text)+len(notice) <= limit {
		return text + notice, false
	}
	if len(notice) >= limit {
		out, _ := ceiling(notice, limit)
		return out, true
	}
	cut, _ := ceiling(text, limit-len(notice))
	return cut + notice, true
}

// ceiling cuts text to at most limit bytes, at a rune boundary, with a marker
// saying how much was dropped; ok reports whether it cut.
func ceiling(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	marker := fmt.Sprintf("\n[truncated: %d of %d bytes omitted]", len(text), len(text))
	keep := max(limit-len(marker), 0)
	for keep > 0 && !utf8.RuneStart(text[keep]) {
		keep--
	}
	marker = fmt.Sprintf("\n[truncated: %d of %d bytes omitted]", len(text)-keep, len(text))
	out := text[:keep] + marker
	if len(out) > limit {
		out = out[:limit]
	}
	return out, true
}

// settle is stage 8: validate and render a body's value, run the shapers (a
// refused call included), and append the one tool/result of the call.
func (r *registry) settle(ctx context.Context, b tools.Batch, ls listeners, c tools.Call, e ended) (tools.Result, error) {
	res := tools.Result{CallID: c.ID(), Name: c.Name(), Stage: e.stage, Code: e.code, By: e.by, Approval: e.approval, Grant: e.grant}
	if e.code == "" {
		text, parts, msg := output(c.Tool(), e.value)
		if msg != "" {
			res.Code, res.Stage = tools.CodeInvalidOutput, tools.StageShape
			res.Text = string(tools.CodeInvalidOutput) + ": " + msg
		} else {
			res.Value, res.Text, res.Parts = e.value, text, parts
			res.Spill = bodySpill(e.value)
		}
	} else {
		res.Text = string(e.code) + ": " + e.msg
	}
	res.IsError = res.Code != ""

	// A call refused at resolve never started: nothing after resolve, shapers
	// included, sees it.
	shapers := ls.shapers
	if e.stage == tools.StageResolve {
		shapers = nil
	}
	// Shapers see deep copies of the value, as policies and guards see copies of
	// the input: what one mutates reaches neither a later shaper nor the
	// returned Value, which stays the one Text was rendered from. A value that
	// cannot be copied is invalid output, before any shaper sees it.
	var value tools.Call
	if res.Value != nil && len(shapers) > 0 {
		snap, err := tools.NewIsolatedCall(c.ID(), c.Name(), c.Arguments(), c.Origin(), c.Parent(), c.Agent(), c.Tool(), res.Value)
		if err != nil {
			res.IsError, res.Code, res.Stage, res.Value, res.Parts = true, tools.CodeInvalidOutput, tools.StageShape, nil, nil
			res.Text = string(tools.CodeInvalidOutput) + ": the value cannot be isolated from the shapers: " + err.Error()
		} else {
			value = snap
		}
	}
	// fail ends the call as code by component id: its content is the text alone.
	fail := func(code tools.Code, id, text string) {
		res.IsError, res.Code, res.By, res.Stage, res.Value, res.Parts = true, code, id, tools.StageShape, nil, nil
		res.Text = text
	}
	// replace is a shaper's ReplaceContent: a changed text replaces the content
	// whole, images included, since the shaper saw only the text.
	replace := func(text string) {
		if text != res.Text {
			res.Text, res.Parts = text, nil
		}
	}
	for _, s := range shapers {
		o := tools.Outcome{IsError: res.IsError, Code: res.Code, Text: res.Text, Parts: tools.CloneParts(res.Parts)}
		if res.Value != nil {
			o.Value = value.Input()
		}
		pd := shape(s, c, o)
		switch pd.Kind {
		case tools.PostAccept:
		case tools.PostReplaceContent:
			replace(pd.Text)
		case tools.PostBlock:
			// Block turns a success into a failure; a failure stays the failure it was.
			if !res.IsError {
				fail(tools.CodeBlocked, s.id, string(tools.CodeBlocked)+": "+pd.Reason)
			}
		default:
			// A failing shaper fails the call; nothing turns a failure into a success.
			if !res.IsError || res.Code != tools.CodeFailed {
				fail(tools.CodeFailed, s.id, string(tools.CodeFailed)+": "+pd.Reason)
			}
		}
	}

	// Final shapers have the last word on the text: they see what the ordinary
	// shapers left, a Block's text included, and only replace it. They run on a
	// call refused at resolve too, and never see the value.
	for _, s := range ls.finals {
		pd := shape(s, c, tools.Outcome{IsError: res.IsError, Code: res.Code, Text: res.Text, Parts: tools.CloneParts(res.Parts)})
		switch pd.Kind {
		case tools.PostAccept:
		case tools.PostReplaceContent:
			replace(pd.Text)
		default:
			// A final shaper that cannot answer fails the call, and its text is
			// replaced whole: what it would have changed is not shown.
			fail(tools.CodeFailed, s.id, string(tools.CodeFailed)+": the final shaper "+s.id+" did not answer")
		}
	}

	meta := resultMeta{Code: res.Code, Stage: res.Stage, By: res.By, Approval: res.Approval, Grant: res.Grant}
	res.Cause = e.cause
	// The spill seat bounds a large result and saves the whole of it; the ceiling
	// then holds whatever it left (DH-D-STORAGE-6, DH-ADR-TOOLS-4). A result with
	// images reaches the seat as its parts, so the images are priced and kept or
	// left out whole with the text.
	if ls.spill != nil && c.Tool() != nil && !c.Tool().BoundsOwnOutput() {
		sr, failure := r.boundedSpillShape(ctx, *ls.spill, c,
			tools.Outcome{IsError: res.IsError, Code: res.Code, Text: res.Text, Parts: tools.CloneParts(res.Parts)})
		if failure == "" && sr.Replace {
			failure = r.applySpill(&res, ls.spill.id, sr)
		}
		if failure != "" {
			res.SpillFailure = failure
			res.Truncated = r.addNotice(&res, spillFailureNotice(failure))
		}
	}
	if !res.Truncated {
		res.Truncated = r.bound(&res)
	}
	if !hasImage(res.Parts) {
		res.Parts = nil
	}
	meta.Cause, meta.Truncated, meta.Spill, meta.SpillFailure = res.Cause, res.Truncated, res.Spill, res.SpillFailure
	data, err := json.Marshal(meta)
	if err != nil {
		return res, err
	}
	content := []vocab.Part{vocab.TextPart(res.Text)}
	if res.Parts != nil {
		content = tools.CloneParts(res.Parts)
	}
	msg, err := vocab.NewMessage(vocab.RoleTool, vocab.ToolResultBlock(c.ID(), res.IsError, content...))
	if err == nil {
		msg, err = msg.WithSource(vocab.Source{Kind: "tool-result", Producers: []string{producer}, Data: data})
	}
	if err != nil {
		return res, fmt.Errorf("tools: result of %s: %w", c.ID(), err)
	}
	d, err := event.ToolResult.Draft(event.Append(), event.ToolResultPayload{Message: msg, CallID: c.ID(), IsError: res.IsError, Spill: res.Spill})
	if err != nil {
		return res, fmt.Errorf("tools: result of %s: %w", c.ID(), err)
	}
	// A cancelled turn still closes the call it started.
	rc, err := b.Log.Append(context.WithoutCancel(ctx), d)
	if err != nil {
		return res, fmt.Errorf("tools: append tool/result %s: %w", c.ID(), err)
	}
	res.Seq = rc.Seq
	// Stage 7: observers learn of the settled call only once its tool/result is
	// appended, and see the durable form encoded above. Notify contains each
	// observer's failure, so nothing they do changes the result or the batch.
	if r.ctx != nil {
		tools.Settled.Notify(r.ctx, ls.at, tools.NewSettlement(c, res, d.Raw()))
	}
	return res, nil
}

// shape runs one shaper; a panic is an unknown answer, which fails the call.
func shape(s named[tools.Shaper], c tools.Call, o tools.Outcome) (pd tools.PostDecision) {
	defer func() {
		if x := recover(); x != nil {
			pd = tools.PostDecision{Kind: "panic", Reason: fmt.Sprintf("the shaper panicked: %v", x)}
		}
	}()
	pd = s.f(c, o)
	if pd.Kind == "" {
		pd.Kind = tools.PostAccept
	}
	return pd
}

// output validates a body's value against the declared output schema and
// renders it, a tools.Content as its parts; msg says why it could not be, a
// panic in a value's own encoding included, so the call still settles with its
// one tool/result.
func output(d *tools.Declaration, v any) (text string, parts []vocab.Part, msg string) {
	defer func() {
		if x := recover(); x != nil {
			text, parts, msg = "", nil, fmt.Sprintf("encoding the output panicked: %v", x)
		}
	}()
	if d != nil && d.Output() != nil {
		if is := d.Output().Validate(v); len(is) > 0 {
			return "", nil, is.Error()
		}
	}
	if text, parts, msg, ok := content(v); ok {
		return text, parts, msg
	}
	text, err := render(v)
	if err != nil {
		return "", nil, err.Error()
	}
	return text, nil, ""
}

// render is the model-facing text of a body's value: a string as it is, raw JSON
// as its bytes, anything else as JSON.
func render(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case json.RawMessage:
		return string(x), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
