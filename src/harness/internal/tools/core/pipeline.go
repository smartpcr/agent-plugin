package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/internal/body"
)

// DefaultMaxArgumentBytes bounds a call's argument bytes when
// Config.MaxArgumentBytes is zero (areas/tools.md §6.2, maxArgumentBytes).
const DefaultMaxArgumentBytes = 1 << 20

// Config configures the pipeline of a registry.
type Config struct {
	// MaxArgumentBytes bounds a call's raw argument bytes; zero is
	// DefaultMaxArgumentBytes.
	MaxArgumentBytes int
	// Approval is the service an Ask is put to; nil is the one mounted under
	// approval.Key on the registry's context, or, when none is, a service with no
	// registry, which settles every request no-answerer (SP-7).
	Approval *approval.Service
	// Checkpoint is the tool-body checkpoint; nil is the policy mounted on the
	// registry's context (checkpoint.Mounted).
	Checkpoint checkpoint.Policy
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
	var ls listeners
	for _, l := range r.layers.Chain(t.Scope) {
		l.mu.RLock()
		ls.policies = append(ls.policies, collect(l.policies)...)
		ls.guards = append(ls.guards, collect(l.guards)...)
		ls.shapers = append(ls.shapers, collect(l.shapers)...)
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
	for _, c := range b.Calls {
		if c.ID == "" || c.Name == "" {
			return tools.BatchResult{}, &tools.Error{Code: tools.CodeInvalidBatch, Msg: "every call needs its id and name"}
		}
	}
	if b.Origin == "" {
		b.Origin = tools.OriginModel
	}
	target := b.View.Target()
	ls := r.listenersAt(target)
	out := tools.BatchResult{Results: make([]tools.Result, 0, len(b.Calls))}
	for _, c := range b.Calls {
		res, err := r.run(ctx, b, target.Agent, ls, c)
		out.Results = append(out.Results, res)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// ended is how a call ended before Shape, or the body's outcome.
type ended struct {
	stage    tools.Stage
	code     tools.Code
	by       string
	msg      string
	value    any
	approval string
}

func stop(stage tools.Stage, code tools.Code, by, msg string) ended {
	return ended{stage: stage, code: code, by: by, msg: msg}
}

// run takes one call through the stages, in their fixed order; every path ends in
// settle, which appends its one tool/result.
func (r *registry) run(ctx context.Context, b tools.Batch, ag agent.Ref, ls listeners, req tools.CallRequest) (tools.Result, error) {
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
		return r.settle(ctx, b, ls, call, stop(tools.StageResolve, tools.CodeNotStarted, "", fmt.Sprintf("%q %s", req.Name, why)))
	}
	if !d.Reaches(b.Origin) {
		return r.settle(ctx, b, ls, call, stop(tools.StageResolve, tools.CodeNotStarted, "",
			fmt.Sprintf("%q cannot be called from origin %s (reach %s)", req.Name, b.Origin, d.Reach())))
	}
	bd, ok := body.Of(d)
	if !ok {
		return r.settle(ctx, b, ls, call, stop(tools.StageResolve, tools.CodeNotStarted, "", fmt.Sprintf("%q has no body", req.Name)))
	}
	draft, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: req.ID, Name: req.Name, Arguments: req.Arguments})
	if err == nil {
		_, err = b.Log.Append(ctx, draft)
	}
	if err != nil {
		res, serr := r.settle(ctx, b, ls, call, stop(tools.StageResolve, tools.CodeNotStarted, "", "the call could not be logged: "+err.Error()))
		return res, errors.Join(fmt.Errorf("tools: append tool/call %s: %w", req.ID, err), serr)
	}
	call = tools.NewCall(req.ID, req.Name, args, b.Origin, b.Parent, ag, d, nil)

	// 2. Bound, decode and validate the arguments.
	if len(args) > r.cfg.MaxArgumentBytes {
		return r.settle(ctx, b, ls, call, stop(tools.StageValidate, tools.CodeArgumentsTooLarge, "",
			fmt.Sprintf("%d argument bytes exceed the bound of %d", len(args), r.cfg.MaxArgumentBytes)))
	}
	in, err := decode(bd, args)
	if err != nil {
		code := tools.CodeInvalidArguments
		var te *tools.Error
		if errors.As(err, &te) && te.Code != "" {
			code = te.Code
		}
		return r.settle(ctx, b, ls, call, stop(tools.StageValidate, code, "", trimCode(err.Error(), code)))
	}
	// Listeners see deep copies of in, never in itself, and nothing is decoded
	// again: what one of them mutates reaches neither the others nor the body.
	isolated, err := tools.NewIsolatedCall(req.ID, req.Name, args, b.Origin, b.Parent, ag, d, in)
	if err != nil {
		return r.settle(ctx, b, ls, call, stop(tools.StageValidate, tools.CodeInvalidArguments, "",
			"the decoded arguments cannot be isolated from the listeners: "+err.Error()))
	}
	call = isolated
	if ctx.Err() != nil {
		return r.settle(ctx, b, ls, call, stop(tools.StagePolicy, tools.CodeAbortedBeforeStart, "", "cancelled: "+context.Cause(ctx).Error()))
	}

	// 3. Policy listeners: the strongest verdict wins, whatever the order.
	dec, by := decide(ctx, ls.policies, call)
	switch dec.Verdict {
	case tools.VerdictDeny:
		return r.settle(ctx, b, ls, call, stop(tools.StagePolicy, tools.CodeDenied, by, denyText(dec)))
	case tools.VerdictCancel:
		return r.settle(ctx, b, ls, call, stop(tools.StagePolicy, tools.CodeAbortedBeforeStart, by, dec.Reason))
	}

	// 4. Deny-only guards.
	for _, g := range ls.guards {
		if den := guard(ctx, g, call); den != nil {
			return r.settle(ctx, b, ls, call, stop(tools.StageGuards, tools.CodeDenied, g.id, denialText(den)))
		}
	}

	// 5. Approval, only for an Ask. Nobody to ask means no.
	outcome := ""
	if dec.Verdict == tools.VerdictAsk {
		display := dec.Display
		if display == "" {
			display = req.Arguments
		}
		ad, err := requestApproval(ctx, r.approvalService(), b.Log, approval.Request{Agent: ag, Tool: d.Name(), Call: req.ID,
			Parent: b.Parent, Origin: string(b.Origin), Reason: dec.Reason, Display: display})
		outcome = string(ad.Outcome)
		if err != nil || !ad.Approved() {
			e := stop(tools.StageApproval, approvalCode(ad.Outcome), ByApproval, approvalText(ad, err))
			e.approval = outcome
			return r.settle(ctx, b, ls, call, e)
		}
	}

	// 6. Checkpoint: everything logged so far, the approval audit included, is
	// durable before the body's first instruction.
	if err := checkpoint.Before(ctx, r.checkpointPolicy(), b.Log, checkpoint.ToolBody); err != nil {
		code := tools.CodeNotStarted
		var ce *checkpoint.Error
		if errors.As(err, &ce) {
			code = tools.Code(ce.Code)
		}
		e := stop(tools.StageFlush, code, "", err.Error())
		e.approval = outcome
		return r.settle(ctx, b, ls, call, e)
	}

	// 7. The body.
	v, err := runBody(ctx, bd, in, tools.Run{CallID: req.ID, Agent: ag})
	if err != nil {
		code := tools.CodeFailed
		var te *tools.Error
		if errors.As(err, &te) && te.Code != "" {
			code = te.Code
		}
		e := stop(tools.StageBody, code, "", trimCode(err.Error(), code))
		e.approval = outcome
		return r.settle(ctx, b, ls, call, e)
	}
	return r.settle(ctx, b, ls, call, ended{stage: tools.StageShape, value: v, approval: outcome})
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
			d = approval.Decision{Outcome: approval.OutcomeNoAnswerer, Detail: fmt.Sprintf("the approval service panicked: %v", x)}
			err = fmt.Errorf("approval: %v", x)
		}
	}()
	return s.Request(ctx, log, r)
}

func approvalCode(o approval.Outcome) tools.Code {
	switch o {
	case approval.OutcomeTimeout:
		return tools.CodeApprovalTimeout
	case approval.OutcomeNoAnswerer:
		return tools.CodeApprovalNoAnswerer
	}
	return tools.CodeApprovalDenied
}

func approvalText(d approval.Decision, err error) string {
	switch {
	case err != nil:
		return "the approval could not be recorded: " + err.Error()
	case d.Outcome == approval.OutcomeNoAnswerer:
		return "nobody could be asked to approve the call (" + d.Detail + ")"
	case d.Outcome == approval.OutcomeTimeout:
		return "nobody approved the call within " + d.Detail
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
	Code     tools.Code  `json:"code,omitempty"`
	Stage    tools.Stage `json:"stage"`
	By       string      `json:"by,omitempty"`
	Approval string      `json:"approval,omitempty"`
}

// settle is stage 8: validate and render a body's value, run the shapers (a
// refused call included), and append the one tool/result of the call.
func (r *registry) settle(ctx context.Context, b tools.Batch, ls listeners, c tools.Call, e ended) (tools.Result, error) {
	res := tools.Result{CallID: c.ID(), Name: c.Name(), Stage: e.stage, Code: e.code, By: e.by, Approval: e.approval}
	if e.code == "" {
		text, msg := output(c.Tool(), e.value)
		if msg != "" {
			res.Code, res.Stage = tools.CodeInvalidOutput, tools.StageShape
			res.Text = string(tools.CodeInvalidOutput) + ": " + msg
		} else {
			res.Value, res.Text = e.value, text
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
			res.IsError, res.Code, res.Stage, res.Value = true, tools.CodeInvalidOutput, tools.StageShape, nil
			res.Text = string(tools.CodeInvalidOutput) + ": the value cannot be isolated from the shapers: " + err.Error()
		} else {
			value = snap
		}
	}
	for _, s := range shapers {
		o := tools.Outcome{IsError: res.IsError, Code: res.Code, Text: res.Text}
		if res.Value != nil {
			o.Value = value.Input()
		}
		pd := shape(s, c, o)
		switch pd.Kind {
		case tools.PostAccept:
		case tools.PostReplaceContent:
			res.Text = pd.Text
		case tools.PostBlock:
			if !res.IsError {
				res.IsError, res.Code, res.By, res.Stage, res.Value = true, tools.CodeBlocked, s.id, tools.StageShape, nil
				res.Text = string(tools.CodeBlocked) + ": " + pd.Reason
			}
		default:
			// A failing shaper fails the call; nothing turns a failure into a success.
			if !res.IsError || res.Code != tools.CodeFailed {
				res.IsError, res.Code, res.By, res.Stage, res.Value = true, tools.CodeFailed, s.id, tools.StageShape, nil
				res.Text = string(tools.CodeFailed) + ": " + pd.Reason
			}
		}
	}

	meta := resultMeta{Code: res.Code, Stage: res.Stage, By: res.By, Approval: res.Approval}
	data, err := json.Marshal(meta)
	if err != nil {
		return res, err
	}
	msg, err := vocab.NewMessage(vocab.RoleTool, vocab.ToolResultBlock(c.ID(), res.IsError, vocab.TextPart(res.Text)))
	if err == nil {
		msg, err = msg.WithSource(vocab.Source{Kind: "tool-result", Producers: []string{producer}, Data: data})
	}
	if err != nil {
		return res, fmt.Errorf("tools: result of %s: %w", c.ID(), err)
	}
	d, err := event.ToolResult.Draft(event.Append(), event.ToolResultPayload{Message: msg, CallID: c.ID(), IsError: res.IsError})
	if err != nil {
		return res, fmt.Errorf("tools: result of %s: %w", c.ID(), err)
	}
	// A cancelled turn still closes the call it started.
	rc, err := b.Log.Append(context.WithoutCancel(ctx), d)
	if err != nil {
		return res, fmt.Errorf("tools: append tool/result %s: %w", c.ID(), err)
	}
	res.Seq = rc.Seq
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
// renders it; msg says why it could not be, a panic in a value's own encoding
// included, so the call still settles with its one tool/result.
func output(d *tools.Declaration, v any) (text, msg string) {
	defer func() {
		if x := recover(); x != nil {
			text, msg = "", fmt.Sprintf("encoding the output panicked: %v", x)
		}
	}()
	if d != nil && d.Output() != nil {
		if is := d.Output().Validate(v); len(is) > 0 {
			return "", is.Error()
		}
	}
	text, err := render(v)
	if err != nil {
		return "", err.Error()
	}
	return text, ""
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
