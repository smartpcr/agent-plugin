package request_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// compactor is a fake compaction seam: it records each trigger and runs run
// (nil reduces nothing).
type compactor struct {
	mu       sync.Mutex
	triggers []request.Trigger
	run      func(ctx context.Context, a agent.Ref, t request.Trigger) (*request.Result, error)
	// forbid fails the test on any call: replay must not compact again.
	forbid atomic.Bool
	t      *testing.T
}

func (c *compactor) Consider(ctx context.Context, a agent.Ref, t request.Trigger) (*request.Result, error) {
	if c.forbid.Load() {
		c.t.Errorf("the compactor ran again during replay")
	}
	c.mu.Lock()
	c.triggers = append(c.triggers, t)
	c.mu.Unlock()
	if c.run != nil {
		return c.run(ctx, a, t)
	}
	return nil, nil
}

func (c *compactor) seen() []request.Trigger {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.triggers)
}

// tracedLog is a memLog that notes each History read in a shared trace.
type tracedLog struct {
	memLog
	note func(string)
}

func (l *tracedLog) History() surface.History {
	l.note("history")
	return l.memLog.History()
}

// askStep asks the pipeline on root about req for refX.
func askStep(t *testing.T, root *cordis.Context, req agent.PreStepRequest) agent.PreStepDecision {
	t.Helper()
	req.Agent = refX
	if req.Ctx == nil {
		req.Ctx = bg
	}
	return must(agent.AskPreStep(root, scope.Key{}, req))
}

// DH-T-CONTEXT-2 (17.3.1, M1-D-LOOP-4): the compaction check of an entered
// step runs before the contributors, which read the surface only after it; it
// runs with no contributor registered too. A step whose route equals the last
// one's is a pressure trigger, one on another route a model-switch trigger,
// and both carry the last selection; the first step has none.
//
//dsh:test DH-T-CONTEXT-2
func TestCompactionIsCheckedBeforeTheContributors(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		root, tab := bare(t)
		var trace []string
		note := func(s string) { trace = append(trace, s) }
		comp := &compactor{t: t, run: func(context.Context, agent.Ref, request.Trigger) (*request.Result, error) {
			note("consider")
			return &request.Result{ID: "c-1", Outcome: "reduced"}, nil
		}}
		p, _ := must2(request.New(root, request.Options{Table: tab, Compact: comp,
			Sessions: func(agent.Ref) (request.Log, error) { return &tracedLog{note: note}, nil }}))
		must(p.Register(root, &fake{name: request.KindClock, t: t, run: func(in request.Input) (request.Output, error) {
			note("contribute")
			return request.Output{}, nil
		}}))
		d := askStep(t, root, agent.PreStepRequest{Turn: 1, Step: 1, Selection: agent.ModelSelection{Route: route}})
		if _, ok := d.(agent.Enter); !ok {
			t.Fatalf("decision %+v, want enter", d)
		}
		if want := []string{"consider", "history", "contribute"}; !slices.Equal(trace, want) {
			t.Fatalf("trace %q, want %q: the surface must be read after the compaction check", trace, want)
		}
		got := comp.seen()
		if len(got) != 1 || got[0].Kind != request.TriggerPressure || got[0].Previous != nil || got[0].Turn != 1 || got[0].Step != 1 || got[0].Selection.Route != route {
			t.Fatalf("triggers %+v, want one first-step pressure trigger", got)
		}
	})

	t.Run("triggers", func(t *testing.T) {
		root, tab := bare(t)
		comp := &compactor{t: t}
		must2(request.New(root, request.Options{Table: tab, Compact: comp, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{}, nil }}))
		same := agent.ModelSelection{Route: route}
		other := agent.ModelSelection{Route: "r/other"}
		for i, prev := range []*agent.ModelSelection{&same, &other} {
			d := askStep(t, root, agent.PreStepRequest{Turn: 1, Step: i + 2, Selection: agent.ModelSelection{Route: route}, Previous: prev})
			if e, ok := d.(agent.Enter); !ok || len(e.Messages) != 0 {
				t.Fatalf("step %d: decision %+v, want the proposal entered", i+2, d)
			}
		}
		got := comp.seen()
		if len(got) != 2 {
			t.Fatalf("%d triggers, want 2: the check runs with no contributor too", len(got))
		}
		if got[0].Kind != request.TriggerPressure || got[0].Previous == nil || got[0].Previous.Route != route {
			t.Fatalf("same route: trigger %+v, want pressure with the last selection", got[0])
		}
		if got[1].Kind != request.TriggerModelSwitch || got[1].Previous == nil || got[1].Previous.Route != "r/other" || got[1].Selection.Route != route {
			t.Fatalf("another route: trigger %+v, want model-switch from r/other", got[1])
		}
	})
}

// DH-T-CONTEXT-2 (17.3.1), the failure table of §6.2: a failed or panicking
// model-switch compaction rejects the step by compaction and runs no
// contributor; a failed pressure compaction lets the step proceed with full
// history; a failure the turn's cancel caused enters the step as proposed; a
// cancelled turn is not compacted; a downstream reject is never compacted.
//
//dsh:test DH-T-CONTEXT-2, DH-T-CONTEXT-3
func TestCompactionFailuresFollowTheTrigger(t *testing.T) {
	switchReq := agent.PreStepRequest{Turn: 1, Step: 2, Selection: agent.ModelSelection{Route: route}, Previous: &agent.ModelSelection{Route: "r/other"}}
	pressureReq := agent.PreStepRequest{Turn: 1, Step: 2, Selection: agent.ModelSelection{Route: route}, Previous: &agent.ModelSelection{Route: route}}
	setup := func(t *testing.T, run func(context.Context, agent.Ref, request.Trigger) (*request.Result, error)) (*cordis.Context, *compactor, *fake) {
		root, tab := bare(t)
		comp := &compactor{t: t, run: run}
		p, _ := must2(request.New(root, request.Options{Table: tab, Compact: comp, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{}, nil }}))
		clock := &fake{name: request.KindClock, t: t}
		must(p.Register(root, clock))
		return root, comp, clock
	}
	errSummary := errors.New("summariser unavailable")

	for _, tc := range []struct {
		name string
		run  func(context.Context, agent.Ref, request.Trigger) (*request.Result, error)
		want string
	}{
		{"error", func(context.Context, agent.Ref, request.Trigger) (*request.Result, error) { return nil, errSummary }, "summariser unavailable"},
		{"panic", func(context.Context, agent.Ref, request.Trigger) (*request.Result, error) { panic("engine bug") }, "panicked: engine bug"},
	} {
		t.Run("model switch "+tc.name, func(t *testing.T) {
			root, _, clock := setup(t, tc.run)
			d := askStep(t, root, switchReq)
			rej, ok := d.(agent.Reject)
			if !ok || rej.By != request.ByCompaction || !strings.Contains(rej.Reason, tc.want) {
				t.Fatalf("decision %+v, want a reject by compaction naming %q", d, tc.want)
			}
			if clock.calls.Load() != 0 {
				t.Fatal("a contributor ran for a step whose model-switch compaction failed")
			}
		})
	}

	t.Run("pressure error", func(t *testing.T) {
		root, _, clock := setup(t, func(context.Context, agent.Ref, request.Trigger) (*request.Result, error) { return nil, errSummary })
		d := askStep(t, root, pressureReq)
		if e, ok := d.(agent.Enter); !ok || len(e.Messages) != 1 || clock.calls.Load() != 1 {
			t.Fatalf("decision %+v after %d clock calls, want the step entered with the clock's reading", d, clock.calls.Load())
		}
	})

	t.Run("cancelled during", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		defer cancel()
		root, _, clock := setup(t, func(context.Context, agent.Ref, request.Trigger) (*request.Result, error) {
			cancel()
			return nil, context.Canceled
		})
		req := switchReq
		req.Ctx = ctx
		d := askStep(t, root, req)
		if e, ok := d.(agent.Enter); !ok || len(e.Messages) != 0 || clock.calls.Load() != 0 {
			t.Fatalf("decision %+v after %d clock calls, want the proposal entered, not a reject", d, clock.calls.Load())
		}
	})

	t.Run("cancelled before", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		cancel()
		root, comp, _ := setup(t, nil)
		req := pressureReq
		req.Ctx = ctx
		if e, ok := askStep(t, root, req).(agent.Enter); !ok || len(e.Messages) != 0 || len(comp.seen()) != 0 {
			t.Fatalf("a cancelled step was compacted (%d triggers) or not entered as proposed", len(comp.seen()))
		}
	})

	t.Run("downstream reject", func(t *testing.T) {
		root, comp, _ := setup(t, nil)
		must(agent.PreStep.On(root, func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
			return agent.Reject{By: "quota", Reason: "over"}, nil
		}))
		if rej, ok := askStep(t, root, pressureReq).(agent.Reject); !ok || rej.By != "quota" || len(comp.seen()) != 0 {
			t.Fatalf("a rejected step was compacted: %d triggers", len(comp.seen()))
		}
	})
}

// checkpointKind is the source kind of the fake compactor's checkpoint.
const checkpointKind = "compaction-checkpoint"

// shadowBaseline is a fake compaction for the agent's step at turn: it
// replaces the instruction baseline on the current surface with a checkpoint
// message that cites it (a user/message with event.Replace).
func shadowBaseline(t *testing.T, sessions func(agent.Ref) (request.Log, error), turn int) func(context.Context, agent.Ref, request.Trigger) (*request.Result, error) {
	return func(ctx context.Context, a agent.Ref, tr request.Trigger) (*request.Result, error) {
		if tr.Turn != turn || tr.Step != 1 {
			return nil, nil
		}
		log, err := sessions(a)
		if err != nil {
			return nil, err
		}
		h := log.History()
		for i := range h.Len() {
			n := h.At(i)
			m, err := n.Message()
			if err != nil {
				return nil, err
			}
			if src, ok := m.Source(); !ok || src.Kind != request.KindInstructions {
				continue
			}
			cp := must(vocab.NewMessage(vocab.RoleUser, vocab.TextBlock("Summary: the workspace instructions were read.")))
			cp = must(cp.WithSource(vocab.Source{Kind: checkpointKind, Producers: []string{"test.compact"}, Data: json.RawMessage(`{"id":"c-1"}`)}))
			d := must(event.UserMessage.Draft(event.Replace(n.Seq(), n.Seq()), event.UserMessagePayload{Message: cp}, n.Seq()))
			rc, err := log.Append(ctx, d)
			if err != nil {
				return nil, err
			}
			return &request.Result{ID: "c-1", Checkpoint: rc.Seq, Shadowed: []event.Seq{n.Seq()}, Outcome: "summarized"}, nil
		}
		t.Errorf("turn %d: no instruction baseline on the surface to shadow", turn)
		return nil, nil
	}
}

// baseline is an instructions contributor whose visible state is a fold over
// its own messages on the current surface: it adds the baseline when none is
// visible.
func baseline() *fake {
	return &fake{name: request.KindInstructions, required: true, run: func(in request.Input) (request.Output, error) {
		msgs, err := in.Visible.Messages()
		if err != nil {
			return request.Output{}, err
		}
		for _, m := range msgs {
			if src, ok := m.Source(); ok && src.Kind == request.KindInstructions {
				return request.Output{}, nil
			}
		}
		m, err := request.Message(request.KindInstructions, "test.instructions", "Build with make; test with make test.", map[string]string{"form": "baseline"})
		return request.Output{Messages: []vocab.Message{m}}, err
	}}
}

// admission is the source kind of each user/message the last opened step
// admitted: those logged after its step/start, before its assistant message.
// It reads the log as the driver committed it at this build (17.1 changes how
// the claimed messages are admitted, not that contributions follow
// step/start).
func (r *rig) admission() []string {
	evs := r.events()
	start := -1
	for i, e := range evs {
		if e.Type() == event.StepStartType.Name() {
			start = i
		}
	}
	var kinds []string
	for _, e := range evs[start+1:] {
		if e.Type() == event.AssistantMessage.Name() {
			break
		}
		if e.Type() != event.UserMessage.Name() {
			continue
		}
		var p event.UserMessagePayload
		must(0, json.Unmarshal([]byte(e.Raw()), &p))
		src, _ := p.Message.Source()
		kinds = append(kinds, src.Kind)
	}
	return kinds
}

// DH-T-CONTEXT-2 (17.3.2, the Stage 17.3 scenario "A compaction-shadowed
// baseline is re-entered"): given the instruction baseline on the surface and
// a fake compactor that replaces it with a checkpoint at the next step, the
// contributor, reading the surface after the compaction check, enters the
// baseline again in that same step's admission; the request sent holds the
// checkpoint and the new baseline, not the shadowed one, and rebuilds from the
// log to the sent digest with nothing observed again.
//
//dsh:test DH-T-CONTEXT-2
func TestBaselineShadowedByCompactionIsReentered(t *testing.T) {
	comp := &compactor{t: t}
	r := newRigWith(t, func(o *request.Options) {
		comp.run = shadowBaseline(t, o.Sessions, 2)
		o.Compact = comp
	}, fakemodel.Reply("one"), fakemodel.Reply("two"))
	instr := baseline()
	r.register(instr)

	r.turn("first")
	first := r.sent.requests()
	if len(first) != 1 || !slices.Equal(r.admission(), []string{request.KindInstructions}) {
		t.Fatalf("turn 1 admitted %q, want the baseline", r.admission())
	}
	shadowedID := baselineIDs(t, must(first[0].Messages()))
	if len(shadowedID) != 1 {
		t.Fatalf("turn 1's request holds %d baselines, want 1", len(shadowedID))
	}

	r.turn("second")
	if got := comp.seen(); len(got) != 2 || got[1].Kind != request.TriggerPressure || got[1].Turn != 2 {
		t.Fatalf("triggers %+v, want a pressure check at each turn", got)
	}
	if got := r.admission(); !slices.Contains(got, request.KindInstructions) {
		t.Fatalf("turn 2 admitted %q: the shadowed baseline was not re-entered in the same step", got)
	}
	if instr.calls.Load() != 2 {
		t.Fatalf("the instructions contributor ran %d times, want once per step", instr.calls.Load())
	}
	// The checkpoint was logged before the step's admission, as a replacement.
	var cpSeq event.Seq
	for _, e := range r.events() {
		if e.Type() == event.UserMessage.Name() && strings.Contains(e.Raw(), checkpointKind) {
			cpSeq = e.Seq()
		}
	}
	if cpSeq == 0 {
		t.Fatal("no checkpoint was logged")
	}

	sent := r.sent.requests()
	if len(sent) != 2 {
		t.Fatalf("%d requests sent, want 2", len(sent))
	}
	req := sent[1]
	msgs := must(req.Messages())
	ids := baselineIDs(t, msgs)
	if len(ids) != 1 || ids[0] == shadowedID[0] {
		t.Fatalf("turn 2's request holds baselines %q (shadowed %q), want exactly the re-entered one", ids, shadowedID)
	}
	var kinds []string
	for _, m := range msgs {
		src, _ := m.Source()
		kinds = append(kinds, src.Kind)
	}
	cp, again := slices.Index(kinds, checkpointKind), slices.Index(kinds, request.KindInstructions)
	if cp < 0 || again < cp {
		t.Fatalf("turn 2's request kinds %q: want the checkpoint, then the re-entered baseline", kinds)
	}
	if cpSeq > req.AsOf() {
		t.Fatalf("the checkpoint at %d is after requestAsOf %d", cpSeq, req.AsOf())
	}

	instr.forbid.Store(true)
	comp.forbid.Store(true)
	b := r.rebuild(req.AsOf())
	if b.Digest() != req.Digest() || b.Canonical() != req.Canonical() {
		t.Fatalf("the rebuilt request differs\nsent:    %s\nrebuilt: %s", req.Canonical(), b.Canonical())
	}
}

// baselineIDs is the id of each instructions message of msgs.
func baselineIDs(t *testing.T, msgs []vocab.Message) []string {
	t.Helper()
	var ids []string
	for _, m := range msgs {
		if src, ok := m.Source(); ok && src.Kind == request.KindInstructions {
			ids = append(ids, m.ID())
		}
	}
	return ids
}
