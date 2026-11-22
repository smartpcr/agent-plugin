package agent_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// runStep plays the driver's part of one step: capture at point 4, PreStep and
// step/start with the capture right after it, then the request header the
// attempt is bound to and its settlement, and step/end.
func runStep(t *testing.T, c *cordis.Context, a agent.Agent, h session.Handle, m *agent.Model, turn, step int, mid func()) agent.Capture {
	t.Helper()
	cp, err := m.Capture(bg, turn, step)
	if err != nil {
		t.Fatal(err)
	}
	if mid != nil {
		mid()
	}
	out := must(agent.ProposeStep(c, a, h, agent.PreStepRequest{Turn: turn, Step: step, Messages: []vocab.Message{userMsg("u1", "go")},
		Selection: cp.Selection, Previous: cp.Previous}, cp.Record))
	if !out.Entered {
		t.Fatalf("step %d not entered: %+v", step, out)
	}
	ctrl := cp.Selection.Controls
	hdr := must(h.Append(bg, must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: cp.Selection.Route, Controls: &ctrl}))))
	asOf := hdr.Seq
	reply := must(vocab.NewMessage(vocab.RoleAssistant, vocab.TextBlock("ok")))
	must(h.Append(bg, must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: reply, RequestAsOf: &asOf}))))
	must(h.Append(bg, must(event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step}))))
	return cp
}

type stepLog struct {
	step     int
	selected agent.SelectionRecord
	settled  vocab.RouteID // the route of the header the settlement names
}

// steps reads, for every step/start, the event right after it and the route
// its settlement was requested on.
func steps(t *testing.T, h session.Handle) []stepLog {
	t.Helper()
	evs := must(h.Read(bg, 0, 0))
	routes := map[event.Seq]vocab.RouteID{}
	var out []stepLog
	for i, ev := range evs {
		switch ev.Type() {
		case event.StepStartType.Name():
			if i+1 >= len(evs) || evs[i+1].Type() != agent.SelectionType.Name() {
				t.Fatalf("step/start at seq %d is not followed by model/selection", ev.Seq())
			}
			r := must(agent.SelectionType.As(evs[i+1]))
			out = append(out, stepLog{step: r.Step, selected: r})
		case event.RequestHeader.Name():
			routes[ev.Seq()] = must(event.RequestHeader.As(ev)).Route
		case event.AssistantMessage.Name():
			p := must(event.AssistantMessage.As(ev))
			out[len(out)-1].settled = routes[*p.RequestAsOf]
		}
	}
	return out
}

// DH-T-AGENT-23, TC-11a: each step/start is followed by the selection it runs
// with, and its settlement's route matches. The default changing between steps
// does not move a session whose request header names a route (DH-ADR-AGENT-10);
// a Select moves the next step.
func TestModelSelectionLoggedPerStep(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	h := openTurn(t, a, 1)
	def := agent.ModelSelection{Route: "r/a", Model: "m-a"}
	m := must(agent.OpenModel(bg, h, agent.ModelOptions{Scope: a.ScopeKey(),
		Defaults: agent.DefaultModelsFunc(func(scope.Key) (agent.ModelSelection, bool) { return def, true })}))

	first := runStep(t, e.root, a, h, m, 1, 1, nil)
	def = agent.ModelSelection{Route: "r/b", Model: "m-b"}
	second := runStep(t, e.root, a, h, m, 1, 2, nil)
	must(m.Select(bg, agent.ModelSelection{Route: "r/c", Model: "m-c"}))
	third := runStep(t, e.root, a, h, m, 1, 3, nil)

	if first.Source != agent.SourceDefault || first.Selection.Route != "r/a" || first.Previous != nil {
		t.Fatalf("first step %+v", first)
	}
	if second.Source != agent.SourceHeader || second.Selection.Route != "r/a" || second.Selection.Model != "m-a" {
		t.Fatalf("second step %+v: a changed default must not move a session with a header", second)
	}
	if third.Source != agent.SourceSelect || third.Selection.Route != "r/c" || third.Previous.Route != "r/a" {
		t.Fatalf("third step %+v", third)
	}
	got := steps(t, h)
	if len(got) != 3 {
		t.Fatalf("%d steps", len(got))
	}
	wantModel := []string{"m-a", "m-a", "m-c"}
	for i, s := range got {
		if s.step != i+1 || s.selected.Turn != 1 || s.selected.Selection.Model != wantModel[i] {
			t.Fatalf("step %d logged %+v", i+1, s.selected)
		}
		if s.settled != s.selected.Selection.Route {
			t.Fatalf("step %d settled on %s, selected %s", i+1, s.settled, s.selected.Selection.Route)
		}
	}
	// The Select was used by step 3 and is no longer pending: step 4 follows the header.
	if sel, src := must2(m.Current(bg)); src != agent.SourceHeader || sel.Route != "r/c" || sel.Model != "m-c" {
		t.Fatalf("after the select was used: %+v from %s", sel, src)
	}
	// A reopened Model folds the same state from the log.
	re := must(agent.OpenModel(bg, h, agent.ModelOptions{}))
	if last, ok, _ := re.Last(bg); !ok || last.Step != 3 || last.Selection.Route != "r/c" {
		t.Fatalf("reopened last %+v", last)
	}
}

// DH-T-AGENT-23: the four sources in precedence order, and agent/no-model.
func TestModelSelectionPrecedence(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	h := openTurn(t, a, 1)
	none := must(agent.OpenModel(bg, h, agent.ModelOptions{}))
	if _, _, err := none.Current(bg); !errors.Is(err, &agent.Error{Code: agent.CodeNoModel}) {
		t.Fatalf("no source: %v", err)
	}
	if _, err := none.Capture(bg, 1, 1); !errors.Is(err, &agent.Error{Code: agent.CodeNoModel}) {
		t.Fatalf("capture with no source: %v", err)
	}

	defaults := agent.DefaultModelsFunc(func(k scope.Key) (agent.ModelSelection, bool) {
		if k != a.ScopeKey() {
			t.Fatalf("default read with %v", k)
		}
		return agent.ModelSelection{Route: "r/default"}, true
	})
	m := must(agent.OpenModel(bg, h, agent.ModelOptions{Defaults: defaults, Scope: a.ScopeKey()}))
	check := func(route vocab.RouteID, src agent.SelectionSource) {
		t.Helper()
		if sel, got := must2(m.Current(bg)); sel.Route != route || got != src {
			t.Fatalf("current %s from %s, want %s from %s", sel.Route, got, route, src)
		}
	}
	check("r/default", agent.SourceDefault)
	m = must(agent.OpenModel(bg, h, agent.ModelOptions{Initial: agent.ModelSelection{Route: "r/options"}, Defaults: defaults, Scope: a.ScopeKey()}))
	check("r/options", agent.SourceOptions)
	must(h.Append(bg, must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/header"}))))
	check("r/header", agent.SourceHeader)
	must(m.Select(bg, agent.ModelSelection{Route: "r/select"}))
	check("r/select", agent.SourceSelect)
	if _, err := m.Select(bg, agent.ModelSelection{Route: " "}); !errors.Is(err, &agent.Error{Code: agent.CodeInvalid}) {
		t.Fatalf("a blank route: %v", err)
	}
}

// DH-T-AGENT-23: the capture is fixed for its step; a Select made after the
// capture, during the step, applies from the next step. A step that is not
// entered uses no Select.
func TestSelectMidStepAppliesNextStep(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	h := openTurn(t, a, 1)
	m := must(agent.OpenModel(bg, h, agent.ModelOptions{Initial: agent.ModelSelection{Route: "r/a"}}))
	must(m.Select(bg, agent.ModelSelection{Route: "r/b"}))

	// Not entered: the capture's record is never appended, so r/b stays pending.
	reject := must(agent.PreStep.On(a.Scope(), preStepFn(func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		return agent.Reject{By: "test", Reason: "not yet"}, nil
	})))
	cp := must(m.Capture(bg, 1, 1))
	must(agent.ProposeStep(e.root, a, h, agent.PreStepRequest{Turn: 1, Step: 1, Selection: cp.Selection}, cp.Record))
	reject.Dispose()
	if sel, src := must2(m.Current(bg)); sel.Route != "r/b" || src != agent.SourceSelect {
		t.Fatalf("a rejected step used the select: %s from %s", sel.Route, src)
	}

	openTurn(t, a, 2)
	s1 := runStep(t, e.root, a, h, m, 2, 1, func() {
		must(m.Select(bg, agent.ModelSelection{Route: "r/c"})) // after the capture, before step/start
	})
	s2 := runStep(t, e.root, a, h, m, 2, 2, nil)
	if s1.Selection.Route != "r/b" || s2.Selection.Route != "r/c" || s2.Source != agent.SourceSelect {
		t.Fatalf("steps ran on %s then %s (%s)", s1.Selection.Route, s2.Selection.Route, s2.Source)
	}
	got := steps(t, h)
	if len(got) != 2 || got[0].selected.Selection.Route != "r/b" || got[1].selected.Selection.Route != "r/c" {
		t.Fatalf("logged %+v", got)
	}
	// Reopened from the log, r/c was used, so nothing is pending.
	re := must(agent.OpenModel(bg, h, agent.ModelOptions{}))
	if sel, src := must2(re.Current(bg)); src != agent.SourceHeader || sel.Route != "r/c" {
		t.Fatalf("reopened current %s from %s", sel.Route, src)
	}
}

// DH-T-AGENT-23: the switch notice adds one user message when the route
// differs from the previous step's, and none otherwise.
func TestModelSwitchNotice(t *testing.T) {
	e := newEnv(t)
	e.seat(t)
	a := e.create(t, "a").Agent()
	must(agent.RegisterModelSwitchNotice(a.Scope()))
	h := openTurn(t, a, 1)
	ra, rb := agent.ModelSelection{Route: "r/a"}, agent.ModelSelection{Route: "r/b", Model: "m-b"}
	batch := []vocab.Message{userMsg("u1", "hi")}
	propose := func(step int, sel agent.ModelSelection, prev *agent.ModelSelection) []vocab.Message {
		out := must(agent.ProposeStep(e.root, a, h, agent.PreStepRequest{Turn: 1, Step: step, Messages: batch, Selection: sel, Previous: prev}))
		must(h.Append(bg, must(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: step}))))
		return out.Enter.Messages
	}
	if got := propose(1, ra, nil); len(got) != 1 {
		t.Fatalf("first step: %d messages", len(got))
	}
	if got := propose(2, ra, &ra); len(got) != 1 {
		t.Fatalf("same route: %d messages", len(got))
	}
	got := propose(3, rb, &ra)
	if len(got) != 2 || got[1].Role() != vocab.RoleUser || !strings.Contains(got[1].Blocks()[0].Text.Text, "r/b (m-b)") {
		t.Fatalf("switch: %+v", got)
	}
}
