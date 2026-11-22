package request_test

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

var bg = context.Background()

const route vocab.RouteID = "r/ctx"

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// fake is a contributor with a scripted observation.
type fake struct {
	name     string
	required bool
	// run is the observation; nil adds one message naming the step.
	run   func(request.Input) (request.Output, error)
	calls atomic.Int32
	// forbid fails the test on any call: replay must not observe again.
	forbid atomic.Bool
	t      *testing.T
}

func (f *fake) Name() string   { return f.name }
func (f *fake) Required() bool { return f.required }
func (f *fake) Contribute(_ context.Context, in request.Input) (request.Output, error) {
	if f.forbid.Load() {
		f.t.Errorf("contributor %s observed again during replay", f.name)
	}
	f.calls.Add(1)
	if f.run != nil {
		return f.run(in)
	}
	m, err := request.Message(f.name, "test."+f.name, f.name+" reading", map[string]int{"step": in.Step})
	return request.Output{Messages: []vocab.Message{m}}, err
}

// recorder keeps every request the driver streamed.
type recorder struct {
	driver.Models
	mu   sync.Mutex
	sent []session.Request
}

func (r *recorder) Prepare(ctx context.Context, rt vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error) {
	p, err := r.Models.Prepare(ctx, rt, model, c)
	if err != nil {
		return nil, err
	}
	return recorded{Prepared: p, r: r}, nil
}

func (r *recorder) requests() []session.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.sent)
}

type recorded struct {
	llm.Prepared
	r *recorder
}

func (p recorded) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	p.r.mu.Lock()
	p.r.sent = append(p.r.sent, req)
	p.r.mu.Unlock()
	return p.Prepared.Stream(ctx, req, sink)
}

type stubAgent struct {
	agent.Agent
	ref agent.Ref
}

func (a stubAgent) Ref() agent.Ref      { return a.ref }
func (a stubAgent) ScopeKey() scope.Key { return scope.Key{} }

// rig is one driven agent on the scripted model with the pipeline listening.
type rig struct {
	t    *testing.T
	root *cordis.Context
	tab  scope.Table
	st   *memstore.Store
	// scopes is each agent's scope key for the pipeline; absent is global.
	mu     sync.Mutex
	scopes map[agent.Ref]scope.Key
	sess   session.Handle
	inbox  *agent.Inbox
	drv    *driver.Driver
	sent   *recorder
	pipe   *request.Pipeline
}

func newRig(t *testing.T, turns ...fakemodel.Turn) *rig {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		must(0, must(root.Use(c, cordis.None{})).Wait())
	}
	var (
		svc tools.Service
		tab scope.Table
	)
	g := must(root.Inject([]string{tools.Key.Name(), scope.TableKey.Name()}, func(c *cordis.Context) (err error) {
		if svc, err = tools.Key.Get(c); err != nil {
			return err
		}
		tab, err = scope.TableKey.Get(c)
		return err
	}))
	must(0, g.Wait())

	st := memstore.New()
	id := "s-ctx"
	sess := must(session.NewService(root, st, session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
	t.Cleanup(func() { _ = sess.Close(bg) })
	r := &rig{t: t, root: root, tab: tab, st: st, sess: sess, scopes: map[agent.Ref]scope.Key{}}
	r.pipe, _ = must2(request.New(root, request.Options{
		Table:    tab,
		Sessions: func(agent.Ref) (request.Log, error) { return sess, nil },
		Scopes: func(a agent.Ref) (scope.Key, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.scopes[a], nil
		},
	}))

	rt := llm.New()
	fakemodel.Register(t, rt, route, fakemodel.NewTransport(t, turns...))
	r.sent = &recorder{Models: rt}
	r.inbox = must(agent.OpenInbox(bg, sess))
	mod := must(agent.OpenModel(bg, sess, agent.ModelOptions{Initial: agent.ModelSelection{Route: route}}))
	r.drv = must(driver.Start(driver.Options{
		Context: root, Agent: stubAgent{ref: agent.Ref{Session: session.ID(id), Incarnation: 1}},
		Session: sess, Inbox: r.inbox, Model: mod, Models: r.sent, Tools: svc,
	}))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 10*time.Second)
		defer cancel()
		if err := r.drv.Close(ctx); err != nil {
			t.Errorf("close the driver: %v", err)
		}
	})
	return r
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

func (r *rig) register(cs ...*fake) {
	for _, c := range cs {
		c.t = r.t
		must(r.pipe.Register(r.root, c))
	}
}

// turn sends text and waits for the agent to go idle.
func (r *rig) turn(text string) {
	r.t.Helper()
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: text}))
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		r.t.Fatalf("wait idle: %v", err)
	}
}

func (r *rig) events() []event.Event { return must(r.sess.Read(bg, 0, 0)) }

func (r *rig) types() []string {
	var out []string
	for _, e := range r.events() {
		out = append(out, e.Type())
	}
	return out
}

// kinds is the source kind of each user/message in the log ("" for none).
func (r *rig) kinds() []string {
	var out []string
	for _, e := range r.events() {
		if e.Type() != event.UserMessage.Name() {
			continue
		}
		var p event.UserMessagePayload
		must(0, json.Unmarshal([]byte(e.Raw()), &p))
		src, _ := p.Message.Source()
		out = append(out, src.Kind)
	}
	return out
}

func (r *rig) skipped() []request.Skipped {
	var out []request.Skipped
	for _, e := range r.events() {
		if e.Type() == request.SkippedType.Name() {
			var s request.Skipped
			must(0, json.Unmarshal([]byte(e.Raw()), &s))
			out = append(out, s)
		}
	}
	return out
}

func (r *rig) lastTurnEnd() event.TurnEnd {
	evs := r.events()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type() == event.TurnEndType.Name() {
			var te event.TurnEnd
			must(0, json.Unmarshal([]byte(evs[i].Raw()), &te))
			return te
		}
	}
	r.t.Fatal("no turn/end")
	return event.TurnEnd{}
}

// rebuild is surface.Rebuild over the stored log prefix ending at asOf.
func (r *rig) rebuild(asOf event.Seq) surface.Built {
	r.t.Helper()
	rd := must(r.st.OpenRead(bg, string(r.sess.ID())))
	defer func() { _ = rd.Close(bg) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	return must(surface.Rebuild(bg, event.Default(), read, string(r.sess.ID()), asOf))
}
