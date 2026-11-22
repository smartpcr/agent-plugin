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
	if got := strings.Join(r.types(), ","); !strings.HasPrefix(got[strings.Index(got, "step/start"):], "step/start,model/selection,user/message,user/message,user/message,request/header") {
		t.Fatalf("the contributions are not admitted between step/start and the request header: %s", got)
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
		if clock.calls.Load() != 0 || len(r.kinds()) != 1 || len(r.skipped()) != 0 {
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
