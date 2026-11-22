//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

const msbBatchSession = "s-msb-batch"

// msbWait bounds every real-time wait on a body's channel.
const msbWait = 10 * time.Second

// msbOuterT is the test running the suite; scenarios take temp homes and the
// fake model's transport from it.
var msbOuterT *testing.T

// msbAgent is the agent a step runs for: a real session and scope, no loop.
// The scenarios play the driver's part (point 4 to 13) around it.
type msbAgent struct {
	ref agent.Ref
	h   session.Handle
	sc  *scope.Scope
}

func (a *msbAgent) Ref() agent.Ref               { return a.ref }
func (a *msbAgent) Session() session.ReadSession { return a.h }
func (a *msbAgent) Scope() *cordis.Context       { return a.sc.Ctx() }
func (a *msbAgent) ScopeKey() scope.Key          { return a.sc.Key() }
func (a *msbAgent) Status() agent.Status         { return agent.StatusIdle }
func (a *msbAgent) State() agent.State           { return agent.StateLive }

// msbKeyIn is the "touch" tool's input: the resource key it writes and the
// call's ordinal, so a body knows which call it is.
type msbKeyIn struct {
	Key string `json:"key" dsh:"required"`
	ID  string `json:"id" dsh:"required"`
}

// msbActivity records, under one lock, the bodies running and what each saw.
type msbActivity struct {
	mu      sync.Mutex
	running map[string]bool
	max     int
	ended   []string
	overlap map[[2]string]bool
}

func (a *msbActivity) start(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for other := range a.running {
		a.overlap[[2]string{other, id}] = true
	}
	a.running[id] = true
	a.max = max(a.max, len(a.running))
}

func (a *msbActivity) end(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.running, id)
	a.ended = append(a.ended, id)
}

func (a *msbActivity) overlapped(x, y string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.overlap[[2]string{x, y}] || a.overlap[[2]string{y, x}]
}

type msbState struct {
	root *cordis.Context
	tab  scope.Table

	// Model selection.
	ag       *msbAgent
	model    *agent.Model
	def      agent.ModelSelection
	rt       *llm.Runtime
	tr       *fakemodel.Transport
	captures map[int]agent.Capture

	// Step tool batch.
	st      store.Store
	home    home.Layout
	sqlite  bool
	svc     tools.Service
	step    *driver.StepTools
	act     *msbActivity
	started chan struct{}
	gate    chan struct{}
	results []tools.Result

	cleanups []func()
}

func (s *msbState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *msbState) dispose() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

// boot composes the scope table on a fresh root.
func (s *msbState) boot() error {
	if s.root != nil {
		return nil
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err != nil {
		return err
	}
	if err := f.Wait(); err != nil {
		return err
	}
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		s.tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

func msbMust(d event.Draft, err error) event.Draft {
	if err != nil {
		panic(err)
	}
	return d
}

func msbSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// ------------------------------------------------------- model selection

// agentWithDefault opens an agent on the memory store whose default-model
// service answers the current default, read at the agent's scope.
func (s *msbState) agentWithDefault(route string) error {
	if err := s.boot(); err != nil {
		return err
	}
	ctx := context.Background()
	h, err := session.NewService(s.root, memstore.New(), session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: "s-msb-model"}})
	if err != nil {
		return fmt.Errorf("create the session: %w", err)
	}
	s.cleanup(func() { _ = h.Close(context.Background()) })
	sc, err := s.tab.New(s.root, "s-msb-model")
	if err != nil {
		return err
	}
	s.cleanup(func() { _ = sc.Close() })
	s.ag = &msbAgent{ref: agent.Ref{Session: "s-msb-model", Incarnation: 1}, h: h, sc: sc}
	s.def = agent.ModelSelection{Route: vocab.RouteID(route), Model: fakemodel.ModelName}
	key := sc.Key()
	s.model, err = agent.OpenModel(ctx, h, agent.ModelOptions{Scope: key,
		Defaults: agent.DefaultModelsFunc(func(at scope.Key) (agent.ModelSelection, bool) {
			if at != key {
				return agent.ModelSelection{}, false
			}
			return s.def, true
		})})
	if err != nil {
		return fmt.Errorf("open the model: %w", err)
	}
	s.captures = map[int]agent.Capture{}
	_, err = h.Append(ctx, msbMust(event.TurnStartType.Draft(event.TurnStart{Turn: 1})))
	return err
}

// routesServed registers both routes on one runtime, each served by the kit's
// adapter over one scripted transport.
func (s *msbState) routesServed(a, b string) error {
	if msbOuterT == nil {
		return errors.New("no outer *testing.T; run through TestE2E_agent_loop_milestone_m1_model_selection_and_step_tool_batch")
	}
	s.rt = llm.New()
	s.tr = fakemodel.NewTransport(msbOuterT)
	fakemodel.Register(msbOuterT, s.rt, vocab.RouteID(a), s.tr)
	fakemodel.Register(msbOuterT, s.rt, vocab.RouteID(b), s.tr)
	return nil
}

func (s *msbState) changeDefault(route string) error {
	s.def = agent.ModelSelection{Route: vocab.RouteID(route), Model: fakemodel.ModelName}
	return nil
}

func (s *msbState) selectRoute(route string) error {
	_, err := s.model.Select(context.Background(), agent.ModelSelection{Route: vocab.RouteID(route), Model: fakemodel.ModelName})
	return err
}

// msbSink settles each attempt into the session.
type msbSink struct{ h session.Handle }

func (k msbSink) Settle(a llm.Attempt) error {
	d, err := a.Draft()
	if err != nil {
		return err
	}
	_, err = k.h.Append(context.Background(), d)
	return err
}

// runStep plays the driver: capture (point 4), PreStep and step/start with the
// capture as one group, the request header the attempt is bound to, the
// step's user message, one attempt on the captured route through the scripted
// fake model, and step/end.
func (s *msbState) runStep(n int) error {
	ctx := context.Background()
	h := s.ag.h
	cp, err := s.model.Capture(ctx, 1, n)
	if err != nil {
		return fmt.Errorf("capture step %d: %w", n, err)
	}
	user, err := vocab.RestoreMessage(fmt.Sprintf("u%d", n), vocab.RoleUser, nil, vocab.TextBlock(fmt.Sprintf("step %d", n)))
	if err != nil {
		return err
	}
	out, err := agent.ProposeStep(s.root, s.ag, h, agent.PreStepRequest{Ctx: ctx, Agent: s.ag.ref, Turn: 1, Step: n,
		Messages: []vocab.Message{user}, Selection: cp.Selection, Previous: cp.Previous}, cp.Record)
	if err != nil {
		return fmt.Errorf("propose step %d: %w", n, err)
	}
	if !out.Entered {
		return fmt.Errorf("step %d was not entered: %+v", n, out)
	}
	s.captures[n] = cp
	ctrl := cp.Selection.Controls
	for _, d := range []func() (event.Draft, error){
		func() (event.Draft, error) {
			return event.RequestHeader.Draft(event.RequestHeaderPayload{Route: cp.Selection.Route, Controls: &ctrl})
		},
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
	} {
		dr, err := d()
		if err != nil {
			return err
		}
		if _, err := h.Append(ctx, dr); err != nil {
			return err
		}
	}
	s.tr.Script(fakemodel.Reply(fmt.Sprintf("reply %d", n)))
	p, err := s.rt.Prepare(ctx, cp.Selection.Route, cp.Selection.Model, cp.Selection.Controls)
	if err != nil {
		return fmt.Errorf("prepare step %d on %s: %w", n, cp.Selection.Route, err)
	}
	req, err := h.NextRequest()
	if err != nil {
		return err
	}
	for range p.Stream(ctx, req, msbSink{h}) {
	}
	_, err = h.Append(ctx, msbMust(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: n})))
	return err
}

type msbStepLog struct {
	start    event.Seq
	selected agent.SelectionRecord
	settled  vocab.RouteID
	settles  int
}

// msbSteps reads the log: for every step/start, the event right after it, and
// the route of the latest request header the step's settlement reflects.
func (s *msbState) msbSteps() ([]msbStepLog, error) {
	evs, err := s.ag.h.Read(context.Background(), 0, 0)
	if err != nil {
		return nil, err
	}
	type hdr struct {
		seq   event.Seq
		route vocab.RouteID
	}
	var headers []hdr
	var out []msbStepLog
	for i, ev := range evs {
		switch ev.Type() {
		case event.StepStartType.Name():
			if i+1 >= len(evs) || evs[i+1].Type() != agent.SelectionType.Name() {
				next := "nothing"
				if i+1 < len(evs) {
					next = evs[i+1].Type()
				}
				return nil, fmt.Errorf("step/start at seq %d is followed by %s, not model/selection", ev.Seq(), next)
			}
			r, err := agent.SelectionType.As(evs[i+1])
			if err != nil {
				return nil, err
			}
			out = append(out, msbStepLog{start: ev.Seq(), selected: r})
		case event.RequestHeader.Name():
			p, err := event.RequestHeader.As(ev)
			if err != nil {
				return nil, err
			}
			headers = append(headers, hdr{ev.Seq(), p.Route})
		case event.AssistantMessage.Name():
			p, err := event.AssistantMessage.As(ev)
			if err != nil {
				return nil, err
			}
			if len(out) == 0 {
				return nil, fmt.Errorf("assistant/message at seq %d outside a step", ev.Seq())
			}
			if p.RequestAsOf == nil {
				return nil, fmt.Errorf("assistant/message at seq %d names no request", ev.Seq())
			}
			var route vocab.RouteID
			for _, h := range headers {
				if h.seq <= *p.RequestAsOf {
					route = h.route
				}
			}
			cur := &out[len(out)-1]
			cur.settled, cur.settles = route, cur.settles+1
		}
	}
	return out, nil
}

func (s *msbState) eachStartFollowedBySelection() error {
	got, err := s.msbSteps()
	if err != nil {
		return err
	}
	if len(got) != len(s.captures) || len(got) == 0 {
		return fmt.Errorf("%d step/start events logged, %d steps ran", len(got), len(s.captures))
	}
	reqs := s.tr.Requests()
	if len(reqs) != len(got) {
		return fmt.Errorf("the fake model saw %d requests for %d steps", len(reqs), len(got))
	}
	for i, st := range got {
		n := i + 1
		r := st.selected
		if r.Turn != 1 || r.Step != n {
			return fmt.Errorf("the model/selection after step %d's step/start names turn %d step %d", n, r.Turn, r.Step)
		}
		cp := s.captures[n]
		if r.Selection.Route != cp.Selection.Route || r.Selection.Model != cp.Selection.Model || r.Source != cp.Source {
			return fmt.Errorf("step %d logged %+v from %s, captured %+v from %s", n, r.Selection, r.Source, cp.Selection, cp.Source)
		}
		var body fakemodel.Body
		if err := json.Unmarshal(reqs[i].Body, &body); err != nil {
			return err
		}
		if body.Route != r.Selection.Route || body.Model != r.Selection.Model {
			return fmt.Errorf("step %d's model call went to %s/%s, its model/selection names %s/%s",
				n, body.Route, body.Model, r.Selection.Route, r.Selection.Model)
		}
	}
	return nil
}

func (s *msbState) settlementMatchesSelection() error {
	got, err := s.msbSteps()
	if err != nil {
		return err
	}
	for i, st := range got {
		if st.settles != 1 {
			return fmt.Errorf("step %d settled %d assistant/messages, want 1", i+1, st.settles)
		}
		if st.settled != st.selected.Selection.Route {
			return fmt.Errorf("step %d's settlement was requested on %q, its model/selection names %q", i+1, st.settled, st.selected.Selection.Route)
		}
	}
	return nil
}

func (s *msbState) calledOnRoutes(list string) error {
	var got []string
	for _, r := range s.tr.Requests() {
		var body fakemodel.Body
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return err
		}
		got = append(got, string(body.Route))
	}
	if want := msbSplit(list); !slices.Equal(got, want) {
		return fmt.Errorf("the fake model was called on %v, want %v", got, want)
	}
	return nil
}

func (s *msbState) capturedOn(n int, route, source string) error {
	cp, ok := s.captures[n]
	if !ok {
		return fmt.Errorf("step %d did not run", n)
	}
	if string(cp.Selection.Route) != route || string(cp.Source) != source {
		return fmt.Errorf("step %d was captured on %q from %q, want %q from %q", n, cp.Selection.Route, cp.Source, route, source)
	}
	return nil
}

// ------------------------------------------------------- step tool batch

// sqliteHome provisions the SQLite store in a home: a fresh directory under
// DSH_E2E_STORE_HOME when the compose run mounts one, else a test-kit temp
// home (the gate path). Nothing skips.
func (s *msbState) sqliteHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-msb-")
		if err != nil {
			return err
		}
		s.cleanup(func() { _ = os.RemoveAll(dir) })
		base = dir
	} else if msbOuterT != nil {
		base = msbOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_agent_loop_milestone_m1_model_selection_and_step_tool_batch")
	}
	s.home = home.New(filepath.Join(base, "home"))
	if err := home.Ensure(s.home); err != nil {
		return err
	}
	st, err := sqlite.OpenHome(context.Background(), s.home)
	if err != nil {
		return fmt.Errorf("open the SQLite store: %w", err)
	}
	s.cleanup(func() { _ = st.Close(context.Background()) })
	s.st, s.sqlite = st, true
	return nil
}

func (s *msbState) pipeline(n int) error {
	if err := s.boot(); err != nil {
		return err
	}
	s.svc = core.NewWith(s.root, s.tab, core.Config{MaxParallel: n, Checkpoint: checkpoint.Default{}})
	if got := s.svc.Limits().MaxParallel; got != n {
		return fmt.Errorf("the registry's maxParallel is %d, want %d", got, n)
	}
	return nil
}

// registerTouch declares a tool whose Overlap writes the key argument. Call 1
// holds until call 2 has finished; call 2 runs only once call 1 has started.
func (s *msbState) registerTouch(name, arg string) error {
	if arg != "key" {
		return fmt.Errorf("only the key argument is declared, got %q", arg)
	}
	s.act = &msbActivity{running: map[string]bool{}, overlap: map[[2]string]bool{}}
	s.started, s.gate = make(chan struct{}), make(chan struct{})
	act, started, gate := s.act, s.started, s.gate
	_, err := s.svc.Register(s.root, tools.Define(tools.Spec[msbKeyIn, string]{
		Name: name, Description: "Writes a keyed resource.",
		Overlap: func(in msbKeyIn) tools.Overlap { return tools.Writes(in.Key) },
		Run: func(ctx context.Context, in msbKeyIn, _ tools.Run) (string, error) {
			act.start(in.ID)
			defer act.end(in.ID)
			switch in.ID {
			case "1":
				close(started)
				select {
				case <-gate:
				case <-time.After(msbWait):
					return "", errors.New("call 2 never finished")
				}
			case "2":
				defer close(gate)
				select {
				case <-started:
				case <-time.After(msbWait):
					return "", errors.New("call 1 never started")
				}
			}
			return in.ID, nil
		},
	}))
	return err
}

func (s *msbState) resolveStep() error {
	a := &msbAgent{ref: agent.Ref{Session: msbBatchSession, Incarnation: 1}, sc: nil}
	st, err := driver.ResolveStep(context.Background(), s.svc, msbRootAgent{a}, agent.ModelSelection{Route: "r/a"})
	if err != nil {
		return err
	}
	s.cleanup(st.Release)
	s.step = st
	return nil
}

// msbRootAgent is an agent at the root scope, for the step tool set.
type msbRootAgent struct{ *msbAgent }

func (a msbRootAgent) ScopeKey() scope.Key    { return scope.Key{} }
func (a msbRootAgent) Scope() *cordis.Context { return nil }

// callsThree opens the step in the SQLite session, its assistant message
// asking for the three calls, and runs them as the step's batch.
func (s *msbState) callsThree(name, keys string) error {
	ks := msbSplit(keys)
	if len(ks) != 3 {
		return fmt.Errorf("want three keys, got %v", ks)
	}
	ctx := context.Background()
	h, err := session.NewService(s.root, s.st, session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: msbBatchSession}})
	if err != nil {
		return fmt.Errorf("create the session: %w", err)
	}
	s.cleanup(func() { _ = h.Close(context.Background()) })
	var calls []tools.CallRequest
	var uses []vocab.Block
	for i, k := range ks {
		c := tools.CallRequest{ID: fmt.Sprintf("c%d", i+1), Name: name, Arguments: fmt.Sprintf(`{"key":%q,"id":"%d"}`, k, i+1)}
		calls = append(calls, c)
		uses = append(uses, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return err
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, uses...)
	if err != nil {
		return err
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			return err
		}
		if _, err := h.Append(ctx, d); err != nil {
			return err
		}
	}
	type outcome struct {
		results []tools.Result
		err     error
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*msbWait)
	defer cancel()
	done := make(chan outcome, 1)
	go func() {
		res, err := s.step.Execute(runCtx, h, calls)
		done <- outcome{results: res.Results, err: err}
	}()
	select {
	case out := <-done:
		if out.err != nil {
			return fmt.Errorf("the batch failed: %w", out.err)
		}
		s.results = out.results
	case <-runCtx.Done():
		cancel()
		// Give the cancelled batch a bounded chance to stop before cleanup closes the session.
		select {
		case <-done:
		case <-time.After(msbWait):
		}
		return errors.New("the batch did not finish")
	}
	if _, err := h.Flush(ctx); err != nil {
		return fmt.Errorf("flush the session: %w", err)
	}
	for _, r := range s.results {
		if r.IsError {
			return fmt.Errorf("call %s failed: %s %s", r.CallID, r.Code, r.Text)
		}
	}
	return nil
}

func (s *msbState) bodiesEnded(order string) error {
	s.act.mu.Lock()
	got := slices.Clone(s.act.ended)
	s.act.mu.Unlock()
	if want := msbSplit(order); !slices.Equal(got, want) {
		return fmt.Errorf("the bodies finished %v, want %v", got, want)
	}
	return nil
}

func (s *msbState) neverOverlapped(x, y string) error {
	if s.act.overlapped(x, y) {
		return fmt.Errorf("calls %s and %s share a resource key but overlapped", x, y)
	}
	return nil
}

func (s *msbState) overlapped(x, y string) error {
	if !s.act.overlapped(x, y) {
		return fmt.Errorf("calls %s and %s do not conflict but did not overlap", x, y)
	}
	return nil
}

func (s *msbState) atMost(n int) error {
	s.act.mu.Lock()
	defer s.act.mu.Unlock()
	if s.act.max > n || s.act.max < 1 {
		return fmt.Errorf("%d bodies ran at once, want at most %d", s.act.max, n)
	}
	return nil
}

func (s *msbState) resultsInOrder(order string) error {
	var got []string
	for _, r := range s.results {
		got = append(got, r.CallID)
	}
	if want := msbSplit(order); !slices.Equal(got, want) {
		return fmt.Errorf("the batch returned %v, want %v", got, want)
	}
	return nil
}

// secondConnRows reads the session's rows from a second connection to the
// database, in seq order, and checks the committed order of calls and results.
func (s *msbState) secondConnRows(calls, results string) error {
	if !s.sqlite {
		return errors.New("the SQLite store was not provisioned")
	}
	ctx := context.Background()
	other, err := sqlite.Open(ctx, s.home.DB())
	if err != nil {
		return err
	}
	defer other.Close(ctx)
	rd, err := other.OpenRead(ctx, msbBatchSession)
	if err != nil {
		return err
	}
	defer rd.Close(ctx)
	recs, err := rd.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		return err
	}
	var lc, lr []string
	var last store.Seq
	for i, rec := range recs {
		if i > 0 && rec.Seq <= last {
			return fmt.Errorf("rows are not in seq order: %d after %d", rec.Seq, last)
		}
		last = rec.Seq
		switch rec.Type {
		case event.ToolCall.Name():
			var p event.ToolCallPayload
			if err := json.Unmarshal([]byte(rec.Payload), &p); err != nil {
				return err
			}
			lc = append(lc, p.CallID)
		case event.ToolResult.Name():
			var p event.ToolResultPayload
			if err := json.Unmarshal([]byte(rec.Payload), &p); err != nil {
				return err
			}
			if p.IsError {
				return fmt.Errorf("the logged tool/result for %s is an error", p.CallID)
			}
			lr = append(lr, p.CallID)
		}
	}
	if want := msbSplit(calls); !slices.Equal(lc, want) {
		return fmt.Errorf("a second connection reads tool/call rows %v, want %v", lc, want)
	}
	if want := msbSplit(results); !slices.Equal(lr, want) {
		return fmt.Errorf("a second connection reads tool/result rows %v, want %v", lr, want)
	}
	return nil
}

func InitializeScenario_agent_loop_milestone_m1_model_selection_and_step_tool_batch(ctx *godog.ScenarioContext) {
	s := &msbState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = msbState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.dispose()
		return c, nil
	})

	ctx.Step(`^an agent on the memory store whose default model is route "([^"]+)"$`, s.agentWithDefault)
	ctx.Step(`^routes "([^"]+)" and "([^"]+)" are served by the scripted fake model$`, s.routesServed)
	ctx.Step(`^step (\d+) runs$`, s.runStep)
	ctx.Step(`^the agent's default model changes to route "([^"]+)"$`, s.changeDefault)
	ctx.Step(`^the agent selects route "([^"]+)"$`, s.selectRoute)
	ctx.Step(`^each step/start is followed by a model/selection naming the model the step used$`, s.eachStartFollowedBySelection)
	ctx.Step(`^each step's settlement was requested on the route its model/selection names$`, s.settlementMatchesSelection)
	ctx.Step(`^the scripted fake model was called on routes "([^"]+)"$`, s.calledOnRoutes)
	ctx.Step(`^step (\d+) was captured on route "([^"]+)" from source "([^"]+)"$`, s.capturedOn)

	ctx.Step(`^the SQLite store in a temp home$`, s.sqliteHome)
	ctx.Step(`^a guarded pipeline with maxParallel (\d+)$`, s.pipeline)
	ctx.Step(`^tool "([^"]+)" that writes the resource named by its "([^"]+)" argument$`, s.registerTouch)
	ctx.Step(`^the step's tool set is resolved for the agent$`, s.resolveStep)
	ctx.Step(`^the model calls "([^"]+)" three times with keys "([^"]+)" and call 2 finishes first$`, s.callsThree)
	ctx.Step(`^the bodies finished in order "([^"]+)"$`, s.bodiesEnded)
	ctx.Step(`^calls (\d+) and (\d+) never overlapped$`, s.neverOverlapped)
	ctx.Step(`^calls (\d+) and (\d+) overlapped$`, s.overlapped)
	ctx.Step(`^no more than (\d+) bodies ran at once$`, s.atMost)
	ctx.Step(`^the batch returned results in order "([^"]+)"$`, s.resultsInOrder)
	ctx.Step(`^a second connection to the database reads tool/call rows in order "([^"]+)" and tool/result rows in order "([^"]+)"$`, s.secondConnRows)
}

func TestE2E_agent_loop_milestone_m1_model_selection_and_step_tool_batch(t *testing.T) {
	msbOuterT = t
	t.Cleanup(func() { msbOuterT = nil })
	suite := godog.TestSuite{
		Name:                "agent_loop_milestone_m1_model_selection_and_step_tool_batch",
		ScenarioInitializer: InitializeScenario_agent_loop_milestone_m1_model_selection_and_step_tool_batch,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"agent_loop_milestone_m1_model_selection_and_step_tool_batch.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
