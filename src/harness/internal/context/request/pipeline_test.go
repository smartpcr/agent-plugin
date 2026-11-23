package request_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// DH-T-CONTEXT-1, -2 (the Stage 11.4 scenario "Context pipeline is logged and
// ordered"): three contributors registered out of table order enter in table
// order after the claimed message; each contribution is a logged user/message
// at or before the request's requestAsOf; the request sent equals, byte for
// byte, surface.Rebuild over the log prefix ending at requestAsOf, and that
// rebuild observes nothing again.
func TestContributionsAreLoggedInTableOrderAndRebuildTheRequest(t *testing.T) {
	r := newRig(t, fakemodel.Reply("done"))
	clock := &fake{name: request.KindClock}
	catalog := &fake{name: request.KindSkillCatalog}
	instr := &fake{name: request.KindInstructions, required: true}
	r.register(clock, catalog, instr)

	r.turn("hi")
	want := []string{"user", request.KindInstructions, request.KindSkillCatalog, request.KindClock}
	if got := r.kinds(); !slices.Equal(got, want) {
		t.Fatalf("user/message source kinds %q, want %q", got, want)
	}
	if got := strings.Join(r.types(), ","); !strings.HasPrefix(got[strings.Index(got, "step/start"):], "step/start,model/selection,user/message,user/message,user/message,user/message,request/header") {
		// The claimed message is admitted in the same batch, before the
		// contributions (M1-D-LOOP-1 A, plan 17.1.8).
		t.Fatalf("the claimed message and the contributions are not admitted between step/start and the request header: %s", got)
	}

	sent := r.sent.requests()
	if len(sent) != 1 {
		t.Fatalf("%d requests sent, want 1", len(sent))
	}
	req := sent[0]
	msgs := must(req.Messages())
	var reqKinds []string
	for _, m := range msgs {
		src, _ := m.Source()
		reqKinds = append(reqKinds, src.Kind)
	}
	if !slices.Equal(reqKinds[len(reqKinds)-3:], want[1:]) {
		t.Fatalf("request message kinds %q, want the contributions %q last", reqKinds, want[1:])
	}
	// Each contribution is backed by a logged event the request reflects.
	for _, m := range msgs[len(msgs)-3:] {
		found := false
		for _, e := range r.events() {
			if e.Type() == event.UserMessage.Name() && strings.Contains(e.Raw(), m.ID()) {
				found = e.Seq() <= req.AsOf()
			}
		}
		if !found {
			t.Fatalf("contribution %s is not logged at or before requestAsOf %d", m.ID(), req.AsOf())
		}
	}

	for _, c := range []*fake{clock, catalog, instr} {
		c.forbid.Store(true)
	}
	b := r.rebuild(req.AsOf())
	if b.Digest() != req.Digest() || b.Canonical() != req.Canonical() {
		t.Fatalf("the rebuilt request differs\nsent:    %s\nrebuilt: %s", req.Canonical(), b.Canonical())
	}
	if got := r.lastTurnEnd().Reason.Kind; got != event.ReasonCompleted {
		t.Fatalf("turn ended %s", got)
	}
}

// DH-T-CONTEXT-2: an unchanged observation adds nothing. A contributor that
// reads its visible state from the surface adds its message once.
func TestUnchangedObservationAddsNothing(t *testing.T) {
	r := newRig(t, fakemodel.Reply("one"), fakemodel.Reply("two"))
	var seen []int
	clock := &fake{name: request.KindClock, run: func(in request.Input) (request.Output, error) {
		msgs := must(in.Visible.Messages())
		seen = append(seen, len(in.Claimed))
		for _, m := range msgs {
			if src, ok := m.Source(); ok && src.Kind == request.KindClock {
				return request.Output{}, nil
			}
		}
		m := must(request.Message(request.KindClock, "test.clock", "it is noon", map[string]string{"at": "12:00"}))
		return request.Output{Messages: []vocab.Message{m}}, nil
	}}
	r.register(clock)
	r.turn("first")
	r.turn("second")
	if got := r.kinds(); !slices.Equal(got, []string{"user", request.KindClock, "user"}) {
		t.Fatalf("source kinds %q: the unchanged clock was entered again", got)
	}
	if clock.calls.Load() != 2 || !slices.Equal(seen, []int{1, 1}) {
		t.Fatalf("calls %d, claimed %v", clock.calls.Load(), seen)
	}
}

// DH-T-CONTEXT-3: an optional contributor's error or panic is skipped and
// logged as context/skipped while the others run; a required one rejects the
// step; a downstream reject enters nothing.
func TestFailuresAreContainedPerContributor(t *testing.T) {
	t.Run("optional", func(t *testing.T) {
		r := newRig(t, fakemodel.Reply("done"))
		r.register(
			&fake{name: request.KindClock, run: func(request.Input) (request.Output, error) { return request.Output{}, errors.New("no clock") }},
			&fake{name: request.KindTmux, run: func(request.Input) (request.Output, error) { panic("tmux bug") }},
			&fake{name: request.KindSkillCatalog, run: func(request.Input) (request.Output, error) {
				// A message without its source is a failure too.
				return request.Output{Messages: []vocab.Message{must(vocab.NewMessage(vocab.RoleUser, vocab.TextBlock("x")))}}, nil
			}},
			&fake{name: request.KindInstructions},
		)
		r.turn("hi")
		sk := r.skipped()
		var names []string
		for _, s := range sk {
			names = append(names, s.Contributor)
			if s.Turn != 1 || s.Step != 1 || s.Error == "" {
				t.Fatalf("skip record %+v", s)
			}
		}
		if !slices.Equal(names, []string{request.KindSkillCatalog, request.KindClock, request.KindTmux}) {
			t.Fatalf("skipped %q, want the catalogue, the clock and tmux in table order", names)
		}
		if !strings.Contains(sk[2].Error, "tmux bug") {
			t.Fatalf("the panic is not recorded: %+v", sk[2])
		}
		if got := r.kinds(); !slices.Equal(got, []string{"user", request.KindInstructions}) {
			t.Fatalf("kinds %q: the healthy contributor did not run", got)
		}
		if got := r.lastTurnEnd().Reason.Kind; got != event.ReasonCompleted {
			t.Fatalf("turn ended %s", got)
		}
	})

	t.Run("required", func(t *testing.T) {
		r := newRig(t)
		r.register(&fake{name: request.KindInstructions, required: true, run: func(request.Input) (request.Output, error) {
			return request.Output{}, errors.New("instructions unreadable")
		}})
		r.turn("hi")
		te := r.lastTurnEnd()
		if te.Reason.Kind != event.ReasonBlocked || te.Reason.By != request.KindInstructions || !strings.Contains(te.Reason.Detail, "unreadable") {
			t.Fatalf("turn/end %+v, want blocked by instructions", te)
		}
		if slices.Contains(r.types(), "step/start") || len(r.sent.requests()) != 0 {
			t.Fatalf("a rejected step opened or called the model: %v", r.types())
		}
	})

	t.Run("downstream reject", func(t *testing.T) {
		r := newRig(t)
		clock := &fake{name: request.KindClock}
		r.register(clock)
		must(agent.PreStep.On(r.root, func(_ *cordis.Context, _ agent.PreStepRequest, _ func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
			return agent.Reject{By: "quota", Reason: "over"}, nil
		}))
		r.turn("hi")
		if te := r.lastTurnEnd(); te.Reason.Kind != event.ReasonBlocked || te.Reason.By != "quota" {
			t.Fatalf("turn/end %+v", te)
		}
		// A rejected step admits nothing, the claimed message neither
		// (M1-D-LOOP-1 A, plan 17.1.8).
		if clock.calls.Load() != 0 || len(r.kinds()) != 0 || len(r.skipped()) != 0 {
			t.Fatalf("a rejected step ran contributors: %d calls, kinds %q", clock.calls.Load(), r.kinds())
		}
	})
}

// Register refuses a name outside the order table and a second contributor
// of one kind; disposing a registration frees its slot.
func TestRegisterKeepsTheTable(t *testing.T) {
	r := newRig(t)
	if _, err := r.pipe.Register(r.root, &fake{name: "weather"}); err == nil {
		t.Fatal("a contributor outside the order table was registered")
	}
	h := must(r.pipe.Register(r.root, &fake{name: request.KindClock}))
	if _, err := r.pipe.Register(r.root, &fake{name: request.KindClock}); err == nil {
		t.Fatal("a second clock contributor was registered")
	}
	h.Dispose()
	must(r.pipe.Register(r.root, &fake{name: request.KindClock}))
}

// DH-ADR-CONTEXT-1: a contributor mounted in a preset serves only that
// preset's agents. Two sibling presets each mount their own clock (the same
// kind, both accepted) under a global clock and a global catalogue: each
// preset's agent sees its own clock and the catalogue, a sibling's agent never
// observes the other preset's clock, an agent outside both sees the global
// clock, and closing a preset leaves its agent the global one.
func TestContributorServesOnlyItsScopesAgents(t *testing.T) {
	r := newRig(t)
	presetA := must(r.tab.New(r.root, "preset-a"))
	presetB := must(r.tab.New(r.root, "preset-b"))
	agentA := must(r.tab.New(r.root, "agent-a", presetA.Key()))
	agentB := must(r.tab.New(r.root, "agent-b", presetB.Key()))
	refA := agent.Ref{Session: "s-a", Incarnation: 1}
	refB := agent.Ref{Session: "s-b", Incarnation: 1}
	refC := agent.Ref{Session: "s-c", Incarnation: 1}
	r.mu.Lock()
	r.scopes[refA], r.scopes[refB] = agentA.Key(), agentB.Key()
	r.mu.Unlock()

	global := &fake{name: request.KindClock, t: t}
	catalog := &fake{name: request.KindSkillCatalog, t: t}
	clockA := &fake{name: request.KindClock, t: t}
	clockB := &fake{name: request.KindClock, t: t}
	must(r.pipe.Register(r.root, global))
	must(r.pipe.Register(r.root, catalog))
	must(r.pipe.Register(presetA.Ctx(), clockA))
	must(r.pipe.Register(presetB.Ctx(), clockB))
	if _, err := r.pipe.Register(presetA.Ctx(), &fake{name: request.KindClock, t: t}); err == nil {
		t.Fatal("a second clock contributor was registered in preset A")
	}

	step := func(ref agent.Ref) []string {
		t.Helper()
		r.mu.Lock()
		at := r.scopes[ref]
		r.mu.Unlock()
		d := must(agent.AskPreStep(r.root, at, agent.PreStepRequest{
			Ctx: bg, Agent: ref, Turn: 1, Step: 1, Selection: agent.ModelSelection{Route: route},
		}))
		enter, ok := d.(agent.Enter)
		if !ok {
			t.Fatalf("%s: decision %+v, want enter", ref, d)
		}
		var kinds []string
		for _, m := range enter.Messages {
			src, _ := m.Source()
			kinds = append(kinds, src.Kind)
		}
		return kinds
	}
	calls := func() [4]int32 {
		return [4]int32{global.calls.Load(), catalog.calls.Load(), clockA.calls.Load(), clockB.calls.Load()}
	}
	want := []string{request.KindSkillCatalog, request.KindClock}

	for _, tc := range []struct {
		name string
		ref  agent.Ref
		want [4]int32 // global, catalog, clock A, clock B, cumulative
	}{
		{"preset A's agent", refA, [4]int32{0, 1, 1, 0}},
		{"preset B's agent", refB, [4]int32{0, 2, 1, 1}},
		{"an agent outside both presets", refC, [4]int32{1, 3, 1, 1}},
	} {
		if got := step(tc.ref); !slices.Equal(got, want) {
			t.Fatalf("%s entered %q, want %q", tc.name, got, want)
		}
		if got := calls(); got != tc.want {
			t.Fatalf("after %s the contributors (global, catalog, A, B) ran %v times, want %v", tc.name, got, tc.want)
		}
	}

	must(0, presetA.Close())
	step(refA)
	if got := calls(); got != [4]int32{2, 4, 1, 1} {
		t.Fatalf("after preset A closed its agent ran (global, catalog, A, B) %v, want the global clock", got)
	}
}
