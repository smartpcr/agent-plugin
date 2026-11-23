package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// liveSession is a live in-memory session of id under root and the service that
// holds it.
func liveSession(t *testing.T, root *cordis.Context, id session.ID) (session.Service, session.Handle) {
	t.Helper()
	sessions := session.NewService(root, memstore.New(), session.Config{})
	h, err := sessions.Create(context.Background(), nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return sessions, h
}

func logTypes(t *testing.T, h session.ReadSession, from int) []string {
	t.Helper()
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs[from:] {
		out = append(out, e.Type())
	}
	return out
}

// DH-T-HUMAN-3 (never), DH-T-HUMAN-6 (a policy switch stops a grant): under ask a
// request with a subject consults the grant table and asks; switching to never
// appends approval/policy and ends the grant policy-changed; then a request is
// rejected by the harness at once, with no delivery and no grant lookup. A switch
// to the policy in force writes nothing, and the fold reads the policy back.
//
//dsh:test DH-T-HUMAN-3, DH-T-HUMAN-6
func TestNeverRejectsWithoutAsking(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	sessions, h := liveSession(t, root, me.Session)
	reg := human.New()
	ans := newPeerAnswerer(t, root, reg, human.Actor{Channel: "ui", Name: "alice", Peer: "p1"}, human.Answer{Outcome: human.OutcomeAllowedSession})
	if _, err := reg.Bind(root, me, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	grants := approval.NewGrants()
	svc := approval.New(approval.Config{Registry: reg, Grants: grants, Sessions: sessions})
	if p := svc.Policy(me.Session); p != approval.PolicyAsk {
		t.Fatalf("a fresh session's policy is %q, want ask", p)
	}
	w := t.TempDir()
	r := dirReq(me, "c1", w)
	d, err := svc.Request(ctx, h, r)
	if err != nil || d.Outcome != approval.OutcomeAllowedOnce || d.Grant == "" || ans.count.Load() != 1 || grants.Lookups() != 1 {
		t.Fatalf("under ask: %+v, %v, delivered %d, lookups %d", d, err, ans.count.Load(), grants.Lookups())
	}

	by := approval.By{Principal: "alice", Channel: "ui", Peer: "p1"}
	if err := svc.SetPolicy(ctx, me, approval.PolicyNever, by); err != nil {
		t.Fatal(err)
	}
	if n := len(grants.List(me)); n != 0 {
		t.Fatalf("%d grants after the switch", n)
	}
	before := len(logTypes(t, h, 0))
	d, err = svc.Request(ctx, h, dirReq(me, "c2", w))
	if err != nil || d.Outcome != approval.OutcomeRejected || d.Detail != approval.DetailPolicyNever || d.By != (approval.By{Principal: approval.PrincipalHarness}) {
		t.Fatalf("under never: %+v, %v", d, err)
	}
	if n, l := ans.count.Load(), grants.Lookups(); n != 1 || l != 1 {
		t.Fatalf("under never the answerer saw %d items (want 1, the earlier one) and the grants %d lookups (want 1)", n, l)
	}
	if got := strings.Join(logTypes(t, h, before), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("under never the log gained %s", got)
	}
	if got := strings.Join(logTypes(t, h, 0), ","); !strings.Contains(got, "approval/policy,grant/ended,approval/asked") {
		t.Fatalf("log %s: want approval/policy then grant/ended before the next request", got)
	}
	evs, _ := h.Read(ctx, 0, 0)
	for _, ev := range evs {
		if ev.Type() == approval.GrantEndedType.Name() {
			if e, _ := approval.GrantEndedType.As(ev); e.Reason != approval.EndPolicyChanged {
				t.Fatalf("grant/ended %+v, want policy-changed", e)
			}
		}
		if ev.Type() == approval.PolicyType.Name() {
			if p, _ := approval.PolicyType.As(ev); p.Policy != approval.PolicyNever || p.By != by {
				t.Fatalf("approval/policy %+v", p)
			}
		}
	}

	n := len(logTypes(t, h, 0))
	if err := svc.SetPolicy(ctx, me, approval.PolicyNever, by); err != nil || len(logTypes(t, h, 0)) != n {
		t.Fatalf("a switch to the policy in force: %v, the log grew", err)
	}
	fold := views.New()
	handle, err := approval.RegisterView(fold)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := fold.Read(ctx, h, approval.PolicyKey)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := handle.Get(snap); err != nil || st.Policy != approval.PolicyNever || st.By != by {
		t.Fatalf("the approval fold %+v, %v", st, err)
	}
}

// SetPolicy refuses an unknown policy, a missing principal and a session that is
// not live, and changes nothing when its approval/policy cannot be written.
func TestSetPolicyRefusals(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	sessions, _ := liveSession(t, root, me.Session)
	svc := approval.New(approval.Config{Sessions: sessions})
	alice := approval.By{Principal: "alice"}
	if err := svc.SetPolicy(ctx, me, "sometimes", alice); err == nil {
		t.Fatal("an unknown policy was set")
	}
	if err := svc.SetPolicy(ctx, me, approval.PolicyNever, approval.By{}); err == nil {
		t.Fatal("a switch without a principal was set")
	}
	if err := svc.SetPolicy(ctx, agent.Ref{Session: "s-gone", Incarnation: 1}, approval.PolicyNever, alice); !errors.Is(err, approval.ErrNoSession) {
		t.Fatalf("a session that is not live: %v", err)
	}
	if err := approval.New(approval.Config{}).SetPolicy(ctx, me, approval.PolicyNever, alice); !errors.Is(err, approval.ErrNoSession) {
		t.Fatalf("no sessions: %v", err)
	}
	failing := approval.New(approval.Config{Sessions: failingPolicy{sessions}})
	if err := failing.SetPolicy(ctx, me, approval.PolicyNever, alice); err == nil || failing.Policy(me.Session) != approval.PolicyAsk {
		t.Fatalf("a failed append: %v, policy %q", err, failing.Policy(me.Session))
	}
	if _, err := approval.PolicyType.Draft(approval.PolicySet{Policy: approval.PolicyAsk}); err == nil {
		t.Fatal("an unattributed approval/policy was drafted")
	}
	if _, err := approval.PolicyType.Draft(approval.PolicySet{Policy: "x", By: alice}); err == nil {
		t.Fatal("an approval/policy outside the closed set was drafted")
	}
	if c := approval.New(approval.Config{Policy: approval.PolicyNever}); c.Policy("any") != approval.PolicyNever {
		t.Fatal("the configured default policy is not used")
	}
}

// failingPolicy finds sessions whose approval/policy appends fail.
type failingPolicy struct{ session.Service }

func (s failingPolicy) Get(id session.ID) (session.Session, bool) {
	h, ok := s.Service.Get(id)
	if !ok {
		return nil, false
	}
	return policyFails{h}, true
}

type policyFails struct{ session.Session }

func (s policyFails) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if d.Type() == approval.PolicyType.Name() {
		return event.Receipt{}, errors.New("injected approval/policy failure")
	}
	return s.Session.Append(ctx, d)
}

// executorRig is the guarded pipeline over svc with one tool, add, whose body
// counts its runs, and a policy that asks for every call.
type executorRig struct {
	root  *cordis.Context
	tools tools.Service
	runs  int
	turn  int
}

type addIn struct {
	A int `json:"a"`
	B int `json:"b"`
}

func newExecutor(t *testing.T, svc *approval.Service) *executorRig {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	var tab scope.Table
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) (err error) {
		tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	x := &executorRig{root: root, tools: core.NewWith(root, tab, core.Config{Approval: svc})}
	if _, err := x.tools.Register(root, tools.Define(tools.Spec[addIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(_ context.Context, in addIn, _ tools.Run) (int, error) { x.runs++; return in.A + in.B, nil },
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := x.tools.Decide(root, "ask", func(context.Context, tools.Call) tools.Decision { return tools.Ask("adds", "") }); err != nil {
		t.Fatal(err)
	}
	return x
}

// call opens a turn whose step asks for add with id, runs it through the
// executor, and closes the turn.
func (x *executorRig) call(t *testing.T, h session.Handle, id string) tools.Result {
	t.Helper()
	ctx := context.Background()
	x.turn++
	turn := x.turn
	user, err := vocab.RestoreMessage("u-"+id, vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a-"+id, vocab.RoleAssistant, nil, vocab.ToolUseBlock(id, "add", json.RawMessage(`{"a":1,"b":2}`)))
	if err != nil {
		t.Fatal(err)
	}
	appendAll(t, h,
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: turn}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: turn, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		})
	defer appendAll(t, h,
		func() (event.Draft, error) { return event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: 1}) },
		func() (event.Draft, error) {
			return event.TurnEndType.Draft(event.TurnEnd{Turn: turn, Reason: event.Reason{Kind: event.ReasonCompleted}})
		})
	v, err := x.tools.Resolve(ctx, tools.Target{Agent: me})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	res, err := x.tools.ExecuteBatch(ctx, tools.Batch{View: v, Log: h, Calls: []tools.CallRequest{{ID: id, Name: "add", Arguments: `{"a":1,"b":2}`}}})
	if err != nil || len(res.Results) != 1 {
		t.Fatalf("execute %+v, %v", res, err)
	}
	return res.Results[0]
}

// Scenario "never refuses without asking" (16.1): given policy never and a fake
// answerer that counts deliveries, when a tool that asks is called through the
// executor, the outcome is rejected, the answerer saw 0 items, the grant table
// was not consulted, the body counter is 0, and the audit pair names principal
// harness. A control call under ask first shows the same rig asks and consults.
//
//dsh:test DH-T-HUMAN-3
func TestNeverRefusesWithoutAskingThroughTheExecutor(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	reg := human.New()
	ans := newPeerAnswerer(t, root, reg, human.Actor{Channel: "ui", Name: "alice", Peer: "p1"}, human.Answer{Outcome: human.OutcomeAllowedOnce})
	if _, err := reg.Bind(root, me, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	grants := approval.NewGrants()
	sessions, h := liveSession(t, root, me.Session)
	svc := approval.New(approval.Config{Registry: reg, Grants: grants, Sessions: sessions})
	x := newExecutor(t, svc)

	// Control: under ask the call is asked, the table consulted, the body run.
	if res := x.call(t, h, "c1"); res.IsError || res.Approval != string(approval.OutcomeAllowedOnce) || ans.count.Load() != 1 || grants.Lookups() != 1 || x.runs != 1 {
		t.Fatalf("under ask: %+v, delivered %d, lookups %d, runs %d", res, ans.count.Load(), grants.Lookups(), x.runs)
	}
	if err := svc.SetPolicy(ctx, me, approval.PolicyNever, approval.By{Principal: "owner", Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	ans.count.Store(0)
	x.runs = 0
	lookups := grants.Lookups()
	before := len(logTypes(t, h, 0))

	res := x.call(t, h, "c2")
	if !res.IsError || res.Approval != string(approval.OutcomeRejected) || res.Code != tools.CodeApprovalRejected || res.Stage != tools.StageApproval {
		t.Fatalf("under never: %+v, want approval/rejected at the approval stage", res)
	}
	if n := ans.count.Load(); n != 0 {
		t.Fatalf("the answerer saw %d items", n)
	}
	if l := grants.Lookups(); l != lookups {
		t.Fatalf("the grant table was consulted %d times", l-lookups)
	}
	if x.runs != 0 {
		t.Fatalf("the body ran %d times", x.runs)
	}
	evs, err := h.Read(ctx, event.Offset(before), 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range evs {
		kinds = append(kinds, ev.Type())
		if ev.Type() == approval.DecidedType.Name() {
			d, err := approval.DecidedType.As(ev)
			if err != nil || d.Outcome != approval.OutcomeRejected || d.By.Principal != approval.PrincipalHarness || d.Call != "c2" {
				t.Fatalf("approval/decided %+v, %v; want rejected by harness", d, err)
			}
		}
	}
	if got := strings.Join(kinds, ","); !strings.Contains(got, "tool/call,approval/asked,approval/decided,tool/result") {
		t.Fatalf("the call logged %s", got)
	}
	if !strings.Contains(res.Text, "never") {
		t.Fatalf("the model is told %q", res.Text)
	}
}

// appendAll appends the drafts mks make to h, in order.
func appendAll(t *testing.T, h session.Handle, mks ...func() (event.Draft, error)) {
	t.Helper()
	for _, mk := range mks {
		d, err := mk()
		if err == nil {
			_, err = h.Append(context.Background(), d)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// fixedView is a step view with no tools for one agent.
type fixedView struct{ target tools.Target }

func (v fixedView) Target() tools.Target                   { return v.target }
func (fixedView) Tools() []*tools.Declaration              { return nil }
func (fixedView) Lookup(string) (*tools.Declaration, bool) { return nil, false }
func (fixedView) Release()                                 {}

// DH-T-TOOLS-14 (the approval-policy section): the section the service registers
// renders to fixed bytes at its fixed place in the manifest, owned by the
// registering component, and follows a switch at the next assembly.
//
//dsh:test DH-T-TOOLS-14
func TestPolicySectionGolden(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	sessions, _ := liveSession(t, root, me.Session)
	svc := approval.New(approval.Config{Sessions: sessions})
	var octx *cordis.Context
	f, err := root.Apply("human.approval.prompt", func(c *cordis.Context) error { octx = c; return nil })
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	ps, err := prompt.New(root, scope.NewTable(root), prompt.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterPromptSection(octx, ps); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterPromptSection(octx, ps); !errors.Is(err, prompt.ErrDuplicate) {
		t.Fatalf("a second registration in one scope: %v", err)
	}
	var b strings.Builder
	render := func(label string) {
		a, err := ps.Assemble(ctx, prompt.Input{View: fixedView{tools.Target{Agent: me}}})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "== %s\n", label)
		for _, m := range a.Manifest() {
			fmt.Fprintf(&b, "manifest %s order=%d owner=%s\n", m.Name, m.Order, m.Owner)
		}
		text, err := ps.Render(a)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(text + "\n")
	}
	render("policy ask")
	if err := svc.SetPolicy(ctx, me, approval.PolicyNever, approval.By{Principal: "owner"}); err != nil {
		t.Fatal(err)
	}
	render("policy never")
	golden.Assert(t, filepath.ToSlash("policy_section"), b.String())
}
