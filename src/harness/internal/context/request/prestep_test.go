package request_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// memLog is a request.Log that records each append and can refuse them.
type memLog struct {
	mu       sync.Mutex
	appended []string
	fail     error
}

func (l *memLog) History() surface.History { return surface.History{} }

func (l *memLog) Append(_ context.Context, d event.Draft) (event.Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return event.Receipt{}, l.fail
	}
	l.appended = append(l.appended, d.Type())
	return event.Receipt{}, nil
}

func (l *memLog) types() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.appended)
}

var refX = agent.Ref{Session: "s-x", Incarnation: 1}

// bare is a component tree with a scope table and no driver: the pipeline is
// asked directly through agent.AskPreStep.
func bare(t *testing.T) (*cordis.Context, scope.Table) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	t.Cleanup(root.Dispose)
	return root, scope.NewTable(root)
}

func ask(t *testing.T, root *cordis.Context, ctx context.Context) (agent.PreStepDecision, error) {
	t.Helper()
	return agent.AskPreStep(root, scope.Key{}, agent.PreStepRequest{Ctx: ctx, Agent: refX, Turn: 1, Step: 1})
}

// Order is the table of context-compaction-skill.md §6.2, and a fresh copy.
func TestOrderIsTheTableAndACopy(t *testing.T) {
	want := []string{request.KindInstructions, request.KindSkillCatalog, request.KindClock, request.KindTmux, request.KindSkillInvocation}
	got := request.Order()
	if !slices.Equal(got, want) {
		t.Fatalf("Order() = %q, want %q", got, want)
	}
	got[0] = "changed"
	if again := request.Order(); !slices.Equal(again, want) {
		t.Fatalf("changing a returned Order changed the table: %q", again)
	}
}

// New and Register refuse missing parts.
func TestNewAndRegisterRefuseMissingParts(t *testing.T) {
	root, tab := bare(t)
	sessions := func(agent.Ref) (request.Log, error) { return &memLog{}, nil }
	for name, o := range map[string]request.Options{
		"no table":   {Sessions: sessions},
		"no session": {Table: tab},
	} {
		if _, _, err := request.New(root, o); err == nil {
			t.Errorf("New with %s succeeded", name)
		}
	}
	if _, _, err := request.New(nil, request.Options{Table: tab, Sessions: sessions}); err == nil {
		t.Error("New with no owner succeeded")
	}
	p, h := must2(request.New(root, request.Options{Table: tab, Sessions: sessions}))
	defer h.Dispose()
	if _, err := p.Register(nil, &fake{name: request.KindClock}); err == nil {
		t.Error("Register with no owner succeeded")
	}
	if _, err := p.Register(root, nil); err == nil {
		t.Error("Register with no contributor succeeded")
	}
}

// context/skipped names a contributor of the order table and carries the error.
func TestSkippedRecordIsValidated(t *testing.T) {
	for name, s := range map[string]request.Skipped{
		"unknown contributor": {Contributor: "weather", Turn: 1, Step: 1, Error: "x"},
		"no error":            {Contributor: request.KindClock, Turn: 1, Step: 1},
	} {
		if _, err := request.SkippedType.Draft(s); err == nil {
			t.Errorf("a skip record with %s was accepted", name)
		}
	}
	if _, err := request.SkippedType.Draft(request.Skipped{Contributor: request.KindClock, Turn: 1, Step: 1, Error: "x"}); err != nil {
		t.Fatalf("a valid skip record: %v", err)
	}
}

// Live and LiveScopes resolve through the registry: an agent it does not know
// is an error, and a step for it fails with the lookup's error named.
func TestLiveLookupsFailForAnUnknownAgent(t *testing.T) {
	root, _ := bare(t)
	reg := agent.NewRegistry(root)
	if _, err := request.Live(reg)(refX); err == nil {
		t.Error("Live resolved an unknown agent")
	}
	if _, err := request.LiveScopes(reg)(refX); err == nil {
		t.Error("LiveScopes resolved an unknown agent")
	}

	for _, tc := range []struct {
		name string
		o    request.Options
		want string
	}{
		{"session", request.Options{Sessions: request.Live(reg)}, "request: the session of"},
		{"scope", request.Options{Sessions: request.Live(reg), Scopes: request.LiveScopes(reg)}, "request: the scope of"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := bare(t)
			o := tc.o
			o.Table = scope.NewTable(root)
			p, _ := must2(request.New(root, o))
			must(p.Register(root, &fake{name: request.KindClock, t: t}))
			if _, err := ask(t, root, bg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("a step for an unknown agent: %v, want %q", err, tc.want)
			}
		})
	}
}

// A downstream listener's error is the step's error; the contributors do not run.
func TestDownstreamErrorIsReturned(t *testing.T) {
	root, tab := bare(t)
	p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{}, nil }}))
	clock := &fake{name: request.KindClock, t: t}
	must(p.Register(root, clock))
	errDown := errors.New("downstream broke")
	must(agent.PreStep.On(root, func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		return nil, errDown
	}))
	if _, err := ask(t, root, bg); !errors.Is(err, errDown) {
		t.Fatalf("step: %v, want the downstream error", err)
	}
	if clock.calls.Load() != 0 {
		t.Fatal("a contributor ran for a step whose chain failed")
	}
}

// With no Scopes lookup every agent is at the global scope, and a request with
// no context gets one: the global contributor observes the step.
func TestGlobalScopeAndBackgroundContext(t *testing.T) {
	root, tab := bare(t)
	p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{}, nil }}))
	var gotCtx context.Context
	must(p.Register(root, &fake{name: request.KindClock, t: t, run: func(in request.Input) (request.Output, error) {
		m, err := request.Message(request.KindClock, "test.clock", "noon", nil)
		return request.Output{Messages: []vocab.Message{m}}, err
	}}))
	must(p.Register(root, ctxProbe{&gotCtx}))
	d := must(ask(t, root, nil))
	enter, ok := d.(agent.Enter)
	if !ok || len(enter.Messages) != 1 {
		t.Fatalf("decision %+v, want the clock's one message entered", d)
	}
	if src, _ := enter.Messages[0].Source(); src.Kind != request.KindClock || len(src.Data) != 0 {
		t.Fatalf("entered source %+v, want the clock's, with no data", src)
	}
	if gotCtx == nil {
		t.Fatal("a contributor of a request with no context got none")
	}
}

// ctxProbe records the context it is called with and adds nothing.
type ctxProbe struct{ got *context.Context }

func (ctxProbe) Name() string   { return request.KindTmux }
func (ctxProbe) Required() bool { return false }
func (c ctxProbe) Contribute(ctx context.Context, _ request.Input) (request.Output, error) {
	*c.got = ctx
	return request.Output{}, nil
}

// A cancelled turn enters the step as proposed: no contributor runs after the
// cancel, and a failure the cancel caused is neither a skip nor a reject.
func TestCancelledTurnEntersAsProposed(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		root, tab := bare(t)
		log := &memLog{}
		p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return log, nil }}))
		clock := &fake{name: request.KindClock, t: t}
		must(p.Register(root, clock))
		ctx, cancel := context.WithCancel(bg)
		cancel()
		d := must(ask(t, root, ctx))
		if e, ok := d.(agent.Enter); !ok || len(e.Messages) != 0 {
			t.Fatalf("decision %+v, want the proposal entered", d)
		}
		if clock.calls.Load() != 0 || len(log.types()) != 0 {
			t.Fatalf("a cancelled step ran %d contributors and logged %q", clock.calls.Load(), log.types())
		}
	})
	t.Run("during", func(t *testing.T) {
		root, tab := bare(t)
		log := &memLog{}
		p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return log, nil }}))
		ctx, cancel := context.WithCancel(bg)
		defer cancel()
		must(p.Register(root, &fake{name: request.KindInstructions, required: true, t: t, run: func(request.Input) (request.Output, error) {
			cancel()
			return request.Output{}, context.Canceled
		}}))
		clock := &fake{name: request.KindClock, t: t}
		must(p.Register(root, clock))
		d := must(ask(t, root, ctx))
		if e, ok := d.(agent.Enter); !ok || len(e.Messages) != 0 {
			t.Fatalf("decision %+v, want the proposal entered, not a reject", d)
		}
		if clock.calls.Load() != 0 || len(log.types()) != 0 {
			t.Fatalf("after the cancel %d contributors ran and %q was logged", clock.calls.Load(), log.types())
		}
	})
}

// A skip that cannot be recorded fails the step with the log's error.
func TestUnrecordableSkipFailsTheStep(t *testing.T) {
	root, tab := bare(t)
	errDisk := errors.New("disk full")
	p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{fail: errDisk}, nil }}))
	must(p.Register(root, &fake{name: request.KindClock, t: t, run: func(request.Input) (request.Output, error) {
		return request.Output{}, errors.New("no clock")
	}}))
	if _, err := ask(t, root, bg); !errors.Is(err, errDisk) || !strings.Contains(err.Error(), `recording the skip of "clock"`) {
		t.Fatalf("step: %v, want the failed skip record", err)
	}
}

// A required contributor's malformed message rejects the step with the reason:
// an empty message, a message of another role, one that fails validation.
func TestMalformedContributionsAreRejected(t *testing.T) {
	assistant := must(vocab.NewMessage(vocab.RoleAssistant, vocab.TextBlock("x")))
	assistant = must(assistant.WithSource(vocab.Source{Kind: request.KindInstructions, Producers: []string{"p"}}))
	for _, tc := range []struct {
		name string
		msg  vocab.Message
		want string
	}{
		{"empty", vocab.Message{}, "returned an empty message"},
		{"assistant", assistant, "message, want user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, tab := bare(t)
			p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{}, nil }}))
			must(p.Register(root, &fake{name: request.KindInstructions, required: true, t: t, run: func(request.Input) (request.Output, error) {
				return request.Output{Messages: []vocab.Message{tc.msg}}, nil
			}}))
			d := must(ask(t, root, bg))
			rej, ok := d.(agent.Reject)
			if !ok || rej.By != request.KindInstructions || !strings.Contains(rej.Reason, tc.want) {
				t.Fatalf("decision %+v, want a reject by instructions naming %q", d, tc.want)
			}
		})
	}
}

// Message needs a producer and data that encodes as JSON.
func TestMessageRefusesNoProducerAndBadData(t *testing.T) {
	if _, err := request.Message(request.KindClock, " ", "noon", nil); err == nil {
		t.Error("a message with no producer was made")
	}
	if _, err := request.Message(request.KindClock, "test.clock", "noon", func() {}); err == nil {
		t.Error("a message with data that is not JSON was made")
	}
	m := must(request.Message(request.KindClock, "test.clock", "noon", map[string]string{"at": "12:00"}))
	if src, ok := m.Source(); !ok || src.Kind != request.KindClock || string(src.Data) != `{"at":"12:00"}` || m.Role() != vocab.RoleUser {
		t.Fatalf("message %+v source %+v", m, src)
	}
}
