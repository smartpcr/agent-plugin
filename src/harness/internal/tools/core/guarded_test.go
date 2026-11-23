package core_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// Stage 16.2.1 (M1-G-PIPE-7): the parts of the guarded pipeline that had no
// test. DH-T-TOOLS-5 is the listener-order, panic and no-agent clauses;
// DH-T-TOOLS-7 is the output schema, Block and tools.Settled clauses.

// countingRegistry is an answerer that counts its deliveries and answers each
// with answer(n), n counting from 1; a nil answer allows once.
type countingRegistry struct {
	human.Registry
	mu     sync.Mutex
	items  []human.Item
	answer func(n int) human.Answer
}

func (c *countingRegistry) Ask(_ context.Context, it human.Item) (human.Answer, human.Actor, error) {
	c.mu.Lock()
	c.items = append(c.items, it)
	n := len(c.items)
	c.mu.Unlock()
	if c.answer == nil {
		return human.Answer{Outcome: human.OutcomeAllowedOnce}, human.Actor{Channel: "tui", Name: "ana"}, nil
	}
	return c.answer(n), human.Actor{Channel: "tui", Name: "ana"}, nil
}

func (c *countingRegistry) delivered() []human.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.items)
}

// permutations is every order of the indexes 0..n-1.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			q := append(append(append([]int{}, p[:i]...), n-1), p[i:]...)
			out = append(out, q)
		}
	}
	return out
}

// A guard's denial holds whatever order the policy listeners and guards were
// registered in, and the answerer is never asked: the guards run before the
// approval, so an Ask that a guard refuses reaches nobody.
//
//dsh:test DH-T-TOOLS-5
func TestGuardDenialHoldsUnderEveryListenerOrder(t *testing.T) {
	type listener struct {
		name string
		add  func(svc tools.Service, ctx *cordis.Context) error
	}
	policy := func(id string, d tools.Decision) listener {
		return listener{id, func(svc tools.Service, ctx *cordis.Context) error {
			_, err := svc.Decide(ctx, id, func(context.Context, tools.Call) tools.Decision { return d })
			return err
		}}
	}
	guard := func(id string, den *tools.Denial) listener {
		return listener{id, func(svc tools.Service, ctx *cordis.Context) error {
			_, err := svc.Guard(ctx, id, func(context.Context, tools.Call) *tools.Denial { return den })
			return err
		}}
	}
	all := []listener{
		policy("allow", tools.Allow()),
		policy("ask", tools.AskFor("writes", "", tools.Subject{Kind: tools.SubjectDirectory, Name: "/w"})),
		guard("pass", nil),
		guard("fence", &tools.Denial{Reason: "outside the workspace"}),
	}
	perms := permutations(len(all))
	if len(perms) != 24 {
		t.Fatalf("%d permutations of 4", len(perms))
	}
	for _, p := range perms {
		var order []string
		for _, i := range p {
			order = append(order, all[i].name)
		}
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			e := newEnv(t)
			tr := &trace{}
			reg := &countingRegistry{}
			svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg, Grants: approval.NewGrants()})})
			e.register(t, e.root, addTool(tr))
			for _, i := range p {
				if err := all[i].add(svc, e.root); err != nil {
					t.Fatal(err)
				}
			}
			call := [2]string{"add", `{"a":1,"b":2}`}
			h := openSession(t, memstore.New(), call)
			res := execute(t, svc, view(t, svc), h, call)[0]
			if res.Code != tools.CodeDenied || res.Stage != tools.StageGuards || res.By != "fence" || res.Approval != "" {
				t.Fatalf("result %+v, want denied at guards by fence", res)
			}
			if n := len(reg.delivered()); n != 0 || tr.count("body") != 0 {
				t.Fatalf("the answerer was asked %d times and the body ran %d times", n, tr.count("body"))
			}
			if got := strings.Join(types(t, h), ","); got != "tool/call,tool/result" {
				t.Fatalf("events %s, want no approval audit", got)
			}
		})
	}
}

// A panicking policy listener, guard or answerer refuses its call with a
// failure result naming it, and the batch goes on: the next call of the same
// batch runs.
//
//dsh:test DH-T-TOOLS-5
func TestPanickingListenerRefusesTheCallAndTheTurnGoesOn(t *testing.T) {
	first := func(c tools.Call) bool { return c.ID() == "c1" }
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, svc tools.Service, ctx *cordis.Context)
		reg   *countingRegistry
		check func(t *testing.T, r tools.Result)
	}{
		{name: "policy", setup: func(t *testing.T, svc tools.Service, ctx *cordis.Context) {
			if _, err := svc.Decide(ctx, "boom", func(_ context.Context, c tools.Call) tools.Decision {
				if first(c) {
					panic("policy on fire")
				}
				return tools.Allow()
			}); err != nil {
				t.Fatal(err)
			}
		}, check: func(t *testing.T, r tools.Result) {
			if r.Code != tools.CodeDenied || r.Stage != tools.StagePolicy || r.By != "boom" || !strings.Contains(r.Text, "panicked: policy on fire") {
				t.Fatalf("result %+v", r)
			}
		}},
		{name: "guard", setup: func(t *testing.T, svc tools.Service, ctx *cordis.Context) {
			if _, err := svc.Guard(ctx, "boom", func(_ context.Context, c tools.Call) *tools.Denial {
				if first(c) {
					panic("guard on fire")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}, check: func(t *testing.T, r tools.Result) {
			if r.Code != tools.CodeDenied || r.Stage != tools.StageGuards || r.By != "boom" || !strings.Contains(r.Text, "panicked: guard on fire") {
				t.Fatalf("result %+v", r)
			}
		}},
		{name: "answerer", reg: &countingRegistry{answer: func(n int) human.Answer {
			if n == 1 {
				panic("answerer on fire")
			}
			return human.Answer{Outcome: human.OutcomeAllowedOnce}
		}}, setup: func(t *testing.T, svc tools.Service, ctx *cordis.Context) {
			if _, err := svc.Decide(ctx, "ask", ask); err != nil {
				t.Fatal(err)
			}
		}, check: func(t *testing.T, r tools.Result) {
			if r.Code != tools.CodeApprovalUnavailable || r.Stage != tools.StageApproval || r.By != core.ByApproval ||
				r.Approval != string(approval.OutcomeUnavailable) || !strings.Contains(r.Text, "answerer on fire") {
				t.Fatalf("result %+v", r)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tr := &trace{}
			reg := tc.reg
			if reg == nil {
				reg = &countingRegistry{}
			}
			svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg})})
			e.register(t, e.root, addTool(tr))
			tc.setup(t, svc, e.root)
			calls := [][2]string{{"add", `{"a":1,"b":2}`}, call2}
			h := openSession(t, memstore.New(), calls...)
			res := execute(t, svc, view(t, svc), h, calls...)
			if !res[0].IsError {
				t.Fatalf("the call whose %s panicked succeeded: %+v", tc.name, res[0])
			}
			tc.check(t, res[0])
			if r := res[1]; r.IsError || r.Text != "7" || tr.count("body") != 1 {
				t.Fatalf("the next call: %+v, body ran %d times; want it run", r, tr.count("body"))
			}
		})
	}
}

// An Ask for a call attributed to no agent is unavailable: nobody can be asked
// on its behalf, no request is made and no audit pair is written.
//
//dsh:test DH-T-TOOLS-5
func TestAskWithNoAgentIsUnavailable(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	reg := &countingRegistry{}
	svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg})})
	e.register(t, e.root, addTool(tr))
	if _, err := svc.Decide(e.root, "ask", ask); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Resolve(context.Background(), tools.Target{})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	call := [2]string{"add", `{"a":1,"b":2}`}
	h := openSession(t, memstore.New(), call)
	res := execute(t, svc, v, h, call)[0]
	if res.Code != tools.CodeApprovalUnavailable || res.Stage != tools.StageApproval || res.By != core.ByApproval ||
		res.Approval != string(approval.OutcomeUnavailable) || !strings.Contains(res.Text, "no agent") {
		t.Fatalf("result %+v", res)
	}
	if len(reg.delivered()) != 0 || tr.count("body") != 0 {
		t.Fatal("someone was asked, or the body ran")
	}
	if got := strings.Join(types(t, h), ","); got != "tool/call,tool/result" {
		t.Fatalf("events %s", got)
	}
}

type kindOut struct {
	Kind string `json:"kind" dsh:"enum=a|b"`
}

// A value that breaks the declared output schema is tools/invalid-output at
// shape, with the offending path; the shapers see it as a failure and no value.
//
//dsh:test DH-T-TOOLS-7
func TestOutputSchemaViolationIsInvalidOutput(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, tools.Define(tools.Spec[noIn, kindOut]{
		Name: "kind", Description: "Returns a kind.",
		Run: func(context.Context, noIn, tools.Run) (kindOut, error) { return kindOut{Kind: "c"}, nil },
	}))
	var seen tools.Outcome
	if _, err := svc.Shape(e.root, "look", func(_ tools.Call, o tools.Outcome) tools.PostDecision { seen = o; return tools.Accept() }); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"kind", `{}`}
	res := execute(t, svc, view(t, svc), openSession(t, memstore.New(), call), call)[0]
	if !res.IsError || res.Code != tools.CodeInvalidOutput || res.Stage != tools.StageShape || res.Value != nil ||
		!strings.HasPrefix(res.Text, string(tools.CodeInvalidOutput)+": ") || !strings.Contains(res.Text, "kind") {
		t.Fatalf("result %+v", res)
	}
	if !seen.IsError || seen.Code != tools.CodeInvalidOutput || seen.Value != nil {
		t.Fatalf("the shaper saw %+v", seen)
	}
}

// Block turns a success into tools/blocked by the shaper; on a failure it
// changes nothing, and no shaper answer turns a failure into a success.
//
//dsh:test DH-T-TOOLS-7
func TestBlockTurnsSuccessIntoFailureAndNeverTheReverse(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, textTool("ok", "fine", false))
	e.register(t, e.root, tools.Define(tools.Spec[noIn, string]{
		Name: "bad", Description: "Fails.",
		Run: func(context.Context, noIn, tools.Run) (string, error) { return "", errors.New("disk full") },
	}))
	if _, err := svc.Shape(e.root, "block", func(tools.Call, tools.Outcome) tools.PostDecision { return tools.Block("not today") }); err != nil {
		t.Fatal(err)
	}
	// A later shaper that rewrites the text cannot clear the error.
	if _, err := svc.Shape(e.root, "rewrite", func(_ tools.Call, o tools.Outcome) tools.PostDecision {
		return tools.ReplaceContent("all good: " + o.Text)
	}); err != nil {
		t.Fatal(err)
	}
	calls := [][2]string{{"ok", `{}`}, {"bad", `{}`}}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	if r := res[0]; !r.IsError || r.Code != tools.CodeBlocked || r.By != "block" || r.Stage != tools.StageShape || r.Value != nil ||
		r.Text != "all good: "+string(tools.CodeBlocked)+": not today" {
		t.Fatalf("blocked success %+v", r)
	}
	if r := res[1]; !r.IsError || r.Code != tools.CodeFailed || r.By != "" || r.Stage != tools.StageBody ||
		r.Text != "all good: "+string(tools.CodeFailed)+": disk full" {
		t.Fatalf("blocked failure %+v, want the body's failure unchanged", r)
	}
	logged, _ := resultsOf(t, h)
	for _, id := range []string{"c1", "c2"} {
		if !logged[id].IsError {
			t.Fatalf("%s was logged as a success", id)
		}
	}
}

// settledObserver is a component named name whose fiber observes tools.Settled
// with fn.
func settledObserver(t *testing.T, root *cordis.Context, name string, fn func(*cordis.Context, tools.Settlement) error) {
	t.Helper()
	f, err := root.Use(cordis.Define(name, func(ctx *cordis.Context, _ cordis.None) error {
		_, err := tools.Settled.On(ctx, fn)
		return err
	}), cordis.None{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
}

// Scenario "A Settled observer cannot change a result" (stage 16.2): with an
// observer on tools.Settled that panics, one that fails and one that records,
// an approved call and a denied call settle exactly as they would without them:
// each tool/result is the durable form the recorder saw, the recorder saw both
// settlements in commit order and could not change them, and the panic and the
// failure are logged naming their observers.
//
//dsh:test DH-T-TOOLS-7
func TestSettledObserverCannotChangeAResult(t *testing.T) {
	e := newEnv(t)
	logs := &cordis.MemoryExporter{}
	e.root.Loggers().AddExporter(logs)
	tr := &trace{}
	reg := &countingRegistry{}
	svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg})})
	e.register(t, e.root, addTool(tr))
	if _, err := svc.Decide(e.root, "gate", func(_ context.Context, c tools.Call) tools.Decision {
		if c.ID() == "c1" {
			return tools.Ask("adds", "")
		}
		return tools.Deny("", "no second sums")
	}); err != nil {
		t.Fatal(err)
	}
	settledObserver(t, e.root, "settled.panicker", func(*cordis.Context, tools.Settlement) error { panic("observer on fire") })
	settledObserver(t, e.root, "settled.failer", func(*cordis.Context, tools.Settlement) error { return errors.New("observer refuses") })
	var seen []tools.Settlement
	settledObserver(t, e.root, "settled.recorder", func(_ *cordis.Context, s tools.Settlement) error {
		seen = append(seen, s)
		// Whatever it does to its copies reaches nothing.
		r := s.Result()
		r.Text, r.IsError, r.Code = "tampered", false, ""
		s.Durable()[0] = 'X'
		return nil
	})

	calls := [][2]string{{"add", `{"a":1,"b":2}`}, call2}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	if r := res[0]; r.IsError || r.Text != "3" || r.Approval != string(approval.OutcomeAllowedOnce) {
		t.Fatalf("approved call %+v", r)
	}
	if r := res[1]; r.Code != tools.CodeDenied || r.By != "gate" {
		t.Fatalf("denied call %+v", r)
	}
	if len(seen) != 2 {
		t.Fatalf("the recorder saw %d settlements, want 2", len(seen))
	}
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	var seqs []uint64
	for _, ev := range evs {
		if ev.Type() == "tool/result" {
			logged = append(logged, ev.Raw())
			seqs = append(seqs, uint64(ev.Seq()))
		}
	}
	for i, s := range seen {
		r := s.Result()
		if s.Call().ID() != res[i].CallID || r.CallID != res[i].CallID || r.Text != res[i].Text || r.IsError != res[i].IsError ||
			r.Code != res[i].Code || r.Seq != res[i].Seq || uint64(r.Seq) != seqs[i] {
			t.Fatalf("settlement %d: call %s, result %+v; returned %+v at seq %d", i, s.Call().ID(), r, res[i], seqs[i])
		}
		if string(s.Durable()) != logged[i] {
			t.Fatalf("settlement %d durable %s, logged %s", i, s.Durable(), logged[i])
		}
	}
	if seen[0].Result().Value != 3 {
		t.Fatalf("the approved settlement's value is %v", seen[0].Result().Value)
	}
	for _, want := range []struct{ name, text string }{{"settled.panicker", "observer on fire"}, {"settled.failer", "observer refuses"}} {
		n := 0
		for _, m := range logs.Messages() {
			if m.Name == want.name && m.Level == cordis.LevelError && strings.Contains(m.Text, tools.Settled.Name()) && strings.Contains(m.Text, want.text) {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("%s logged %d failures naming it, want 2 (one per call): %s", want.name, n, dump(logs))
		}
	}
}

func dump(logs *cordis.MemoryExporter) string {
	var b strings.Builder
	for _, m := range logs.Messages() {
		fmt.Fprintf(&b, "\n  %s [%s] %s", m.Name, m.Level, m.Text)
	}
	return b.String()
}
