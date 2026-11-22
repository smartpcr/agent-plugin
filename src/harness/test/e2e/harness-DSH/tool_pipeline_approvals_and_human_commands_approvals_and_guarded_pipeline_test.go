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

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// The session every scenario's step runs in, and the agent its calls are
// attributed to.
const agpSessionID = "s-agp"

var agpAgent = agent.Ref{Session: session.ID(agpSessionID), Incarnation: 1}

// agpOuterT is the test running the suite; scenarios take temp homes from it.
var agpOuterT *testing.T

// agpTrace records the stages a call passed, in order.
type agpTrace struct {
	mu    sync.Mutex
	steps []string
}

func (tr *agpTrace) add(s string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.steps = append(tr.steps, s)
}

func (tr *agpTrace) got() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.steps)
}

func (tr *agpTrace) count(s string) int {
	n := 0
	for _, x := range tr.got() {
		if x == s {
			n++
		}
	}
	return n
}

// agpValidating is the trace the inputs' Validate hooks record into; godog runs
// the scenarios one after another, so one at a time owns it.
var agpValidating *agpTrace

type agpAddIn struct {
	A int `json:"a" dsh:"required"`
	B int `json:"b" dsh:"required"`
}

func (agpAddIn) Validate() error { agpValidating.add("validate"); return nil }

type agpTextIn struct {
	Text string `json:"text" dsh:"required"`
}

func (agpTextIn) Validate() error { agpValidating.add("validate"); return nil }

type agpTextOut struct {
	Text string `json:"text"`
}

// agpRecView records the resolve stage of the step's fixed view.
type agpRecView struct {
	tools.StepView
	tr *agpTrace
}

func (v agpRecView) Lookup(name string) (*tools.Declaration, bool) {
	v.tr.add("resolve")
	return v.StepView.Lookup(name)
}

// agpAnswerer is a fake interaction registry that approves every request.
type agpAnswerer struct {
	human.Registry
	tr *agpTrace
}

func (a agpAnswerer) Ask(context.Context, human.Item) (human.Answer, human.Actor, error) {
	a.tr.add("approval")
	return human.Answer{Outcome: human.OutcomeAllowedOnce}, human.Actor{Channel: "tui", Name: "ana"}, nil
}

// agpCheckpoint records the flush stage and delegates to the shipped Default
// policy, so the flush the scenarios observe is the production one. It refuses
// any point other than ToolBody, proving the pipeline asked for the tool-body
// checkpoint (a refusal surfaces as tools/not-started and fails the scenario).
type agpCheckpoint struct{ tr *agpTrace }

func (c agpCheckpoint) Before(ctx context.Context, s checkpoint.Session, p checkpoint.Point) error {
	if p != checkpoint.ToolBody {
		return fmt.Errorf("checkpoint at %q, want %q", p, checkpoint.ToolBody)
	}
	c.tr.add("flush")
	return checkpoint.Default{}.Before(ctx, s, p)
}

type agpState struct {
	root *cordis.Context
	tab  scope.Table
	ps   *prompt.Service
	svc  tools.Service
	tr   *agpTrace

	// st is the store the step's session lives in: memstore unless the
	// scenario provisioned the SQLite store.
	st     store.Store
	home   home.Layout
	sqlite bool

	stepScope *scope.Scope
	sess      session.Handle

	results []tools.Result

	// What the second connection saw from inside the body.
	seen    []string
	seenErr error

	cleanups []func()
}

func (s *agpState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *agpState) dispose() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
	agpValidating = nil
}

// boot composes the scope table, the tool registry and the prompt service, then
// replaces the registry with a pipeline configured by cfg.
func (s *agpState) boot(cfg core.Config) error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	for _, c := range []struct {
		comp *cordis.Component
		cfg  any
	}{{scope.Component, cordis.None{}}, {core.Component, cordis.None{}}, {prompt.Component, prompt.Defaults()}} {
		f, err := root.Use(c.comp, c.cfg)
		if err != nil {
			return err
		}
		if err := f.Wait(); err != nil {
			return fmt.Errorf("%s: %w", c.comp.Name(), err)
		}
	}
	g, err := root.Inject([]string{scope.TableKey.Name(), prompt.Key.Name()}, func(c *cordis.Context) error {
		var err error
		if s.tab, err = scope.TableKey.Get(c); err != nil {
			return err
		}
		s.ps, err = prompt.Key.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return err
	}
	s.root = root
	s.svc = core.NewWith(root, s.tab, cfg)
	return nil
}

// --- Given ---

func (s *agpState) pipelineNoApproval() error {
	s.tr = &agpTrace{}
	agpValidating = s.tr
	// Nothing is mounted under approval.Key and the config names no service: the
	// pipeline falls back to a service with no registry.
	return s.boot(core.Config{})
}

func (s *agpState) pipelineRecordingApproves() error {
	s.tr = &agpTrace{}
	agpValidating = s.tr
	cfg := core.Config{
		Approval:   approval.New(approval.Config{Registry: agpAnswerer{tr: s.tr}}),
		Checkpoint: agpCheckpoint{tr: s.tr},
	}
	if err := s.boot(cfg); err != nil {
		return err
	}
	tr := s.tr
	if _, err := s.svc.Decide(s.root, "policy", func(context.Context, tools.Call) tools.Decision {
		tr.add("policy")
		return tools.Ask("needs approval", "")
	}); err != nil {
		return err
	}
	if _, err := s.svc.Guard(s.root, "guard", func(context.Context, tools.Call) *tools.Denial {
		tr.add("guards")
		return nil
	}); err != nil {
		return err
	}
	_, err := s.svc.Shape(s.root, "shape", func(tools.Call, tools.Outcome) tools.PostDecision {
		tr.add("shape")
		return tools.Accept()
	})
	return err
}

func (s *agpState) askPolicy() error {
	_, err := s.svc.Decide(s.root, "ask", func(context.Context, tools.Call) tools.Decision {
		return tools.Ask("adds", "")
	})
	return err
}

func (s *agpState) registerAdd(sig string) error {
	if sig != "add(a:int,b:int)" {
		return fmt.Errorf("only add(a:int,b:int) is declared, got %q", sig)
	}
	tr := s.tr
	_, err := s.svc.Register(s.root, tools.Define(tools.Spec[agpAddIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(_ context.Context, in agpAddIn, _ tools.Run) (int, error) {
			tr.add("body")
			return in.A + in.B, nil
		},
	}))
	return err
}

func (s *agpState) registerDurableAdd(sig string) error {
	if sig != "add(a:int,b:int)" {
		return fmt.Errorf("only add(a:int,b:int) is declared, got %q", sig)
	}
	if !s.sqlite {
		return errors.New("the SQLite store was not provisioned")
	}
	tr, db := s.tr, s.home.DB()
	_, err := s.svc.Register(s.root, tools.Define(tools.Spec[agpAddIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(ctx context.Context, in agpAddIn, _ tools.Run) (int, error) {
			// The body's first instruction: look at the database from outside.
			s.seen, s.seenErr = agpDurable(ctx, db)
			tr.add("body")
			return in.A + in.B, nil
		},
	}))
	return err
}

func (s *agpState) registerTextTools(a, b string) error {
	tr := s.tr
	for _, name := range []string{a, b} {
		_, err := s.svc.Register(s.root, tools.Define(tools.Spec[agpTextIn, agpTextOut]{
			Name: name, Description: "Tool " + name + ".",
			Run: func(_ context.Context, in agpTextIn, _ tools.Run) (agpTextOut, error) {
				tr.add("body")
				return agpTextOut(in), nil
			},
		}))
		if err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
	}
	return nil
}

func (s *agpState) admitsOnly(name string) error {
	sc, err := s.tab.New(s.root, "step")
	if err != nil {
		return err
	}
	if _, err := s.svc.Restrict(sc.Ctx(), tools.Filter{Allow: []string{name}}); err != nil {
		return err
	}
	s.stepScope = sc
	v, err := s.view()
	if err != nil {
		return err
	}
	defer v.Release()
	if got := agpNames(v); !slices.Equal(got, []string{name}) {
		return fmt.Errorf("the step's ToolSet is %v, want only %s", got, name)
	}
	return nil
}

func (s *agpState) promptLists(name string) error {
	v, err := s.view()
	if err != nil {
		return err
	}
	defer v.Release()
	if got := agpNames(v); !slices.Contains(got, name) {
		return fmt.Errorf("%s is not in the step's tool list %v", name, got)
	}
	a, err := s.ps.Assemble(context.Background(), prompt.Input{View: v})
	if err != nil {
		return err
	}
	for _, ts := range a.Tools {
		if ts.Name == name {
			return nil
		}
	}
	return fmt.Errorf("%s is not in the assembled prompt's tools", name)
}

// sqliteHome provisions the SQLite store: in a fresh directory under
// DSH_E2E_STORE_HOME when the compose run mounts one, else in a test-kit temp
// home (the gate path). Nothing skips.
func (s *agpState) sqliteHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-agp-")
		if err != nil {
			return err
		}
		s.cleanup(func() { _ = os.RemoveAll(dir) })
		base = dir
	} else if agpOuterT != nil {
		base = agpOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_tool_pipeline_approvals_and_human_commands_approvals_and_guarded_pipeline")
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

// --- When ---

func (s *agpState) view() (tools.StepView, error) {
	t := tools.Target{Agent: agpAgent}
	if s.stepScope != nil {
		t.Scope = s.stepScope.Key()
	}
	return s.svc.Resolve(context.Background(), t)
}

// openStep opens the session of the step whose assistant message asks for calls.
func (s *agpState) openStep(calls []tools.CallRequest) error {
	if s.st == nil {
		s.st = memstore.New()
	}
	ctx := context.Background()
	h, err := session.NewService(s.root, s.st, session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: agpSessionID}})
	if err != nil {
		return fmt.Errorf("create the session: %w", err)
	}
	s.cleanup(func() { _ = h.Close(context.Background()) })
	s.sess = h
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return err
	}
	var uses []vocab.Block
	for _, c := range calls {
		uses = append(uses, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
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
	return nil
}

func (s *agpState) execute(calls ...tools.CallRequest) error {
	if err := s.openStep(calls); err != nil {
		return err
	}
	v, err := s.view()
	if err != nil {
		return err
	}
	defer v.Release()
	res, err := s.svc.ExecuteBatch(context.Background(), tools.Batch{View: agpRecView{v, s.tr}, Log: s.sess, Calls: calls})
	if err != nil {
		return fmt.Errorf("execute the batch: %w", err)
	}
	if len(res.Results) != len(calls) {
		return fmt.Errorf("%d results for %d calls", len(res.Results), len(calls))
	}
	s.results = res.Results
	return nil
}

func (s *agpState) modelCalls(name, args string) error {
	return s.execute(tools.CallRequest{ID: "c1", Name: name, Arguments: args})
}

func (s *agpState) modelCallsTwo(n1, a1, n2, a2 string) error {
	return s.execute(tools.CallRequest{ID: "c1", Name: n1, Arguments: a1}, tools.CallRequest{ID: "c2", Name: n2, Arguments: a2})
}

// --- Then ---

func (s *agpState) one() (tools.Result, error) {
	if len(s.results) != 1 {
		return tools.Result{}, fmt.Errorf("%d results, want one", len(s.results))
	}
	return s.results[0], nil
}

func (s *agpState) bodyCounter(n int) error {
	if got := s.tr.count("body"); got != n {
		return fmt.Errorf("the body ran %d times, want %d", got, n)
	}
	return nil
}

func (s *agpState) callEnds(code, stage string) error {
	r, err := s.one()
	if err != nil {
		return err
	}
	if !r.IsError || string(r.Code) != code || string(r.Stage) != stage || r.Seq == 0 {
		return fmt.Errorf("result %+v, want %s at %s with its tool/result appended", r, code, stage)
	}
	return nil
}

func (s *agpState) events() ([]event.Event, error) {
	evs, err := s.sess.Read(context.Background(), 0, 0)
	if err != nil {
		return nil, err
	}
	if len(evs) < 4 {
		return nil, fmt.Errorf("the session holds %d events, want the step's four first", len(evs))
	}
	return evs[4:], nil
}

func (s *agpState) logHolds(csv string) error {
	evs, err := s.events()
	if err != nil {
		return err
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.Type())
	}
	if strings.Join(got, ",") != csv {
		return fmt.Errorf("the step logged %v, want %s", got, csv)
	}
	return nil
}

func (s *agpState) resultToModel(prefix string) error {
	r, err := s.one()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(r.Text, prefix) {
		return fmt.Errorf("the result text %q does not start with %q", r.Text, prefix)
	}
	evs, err := s.events()
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.Type() != event.ToolResult.Name() {
			continue
		}
		var p struct {
			CallID  string          `json:"call_id"`
			IsError bool            `json:"is_error"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal([]byte(e.Raw()), &p); err != nil {
			return err
		}
		if p.CallID != r.CallID || !p.IsError || !strings.Contains(string(p.Message), prefix) {
			return fmt.Errorf("the logged tool/result %s is not the model-facing error %q", e.Raw(), prefix)
		}
		return nil
	}
	return errors.New("no tool/result was logged for the model")
}

func (s *agpState) approvalOutcome(want string) error {
	r, err := s.one()
	if err != nil {
		return err
	}
	if r.Approval != want || r.By != core.ByApproval {
		return fmt.Errorf("result %+v, want approval %s by %s", r, want, core.ByApproval)
	}
	return nil
}

func (s *agpState) decidedOutcome(want string) error {
	evs, err := s.events()
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.Type() != approval.DecidedType.Name() {
			continue
		}
		var d approval.Decided
		if err := json.Unmarshal([]byte(e.Raw()), &d); err != nil {
			return err
		}
		if string(d.Outcome) != want || d.By.Principal == "" || d.Tool != "add" || d.Call != "c1" {
			return fmt.Errorf("approval/decided %+v, want %s for add/c1 attributed to a principal", d, want)
		}
		return nil
	}
	return errors.New("no approval/decided was logged")
}

func (s *agpState) recordedOrder(csv string) error {
	if got := strings.Join(s.tr.got(), ","); got != csv {
		return fmt.Errorf("recorded order %s, want %s", got, csv)
	}
	return nil
}

func (s *agpState) succeeds(text, outcome string) error {
	r, err := s.one()
	if err != nil {
		return err
	}
	if r.IsError || r.Text != text || r.Approval != outcome || r.Stage != tools.StageShape {
		return fmt.Errorf("result %+v, want success %q approved %s", r, text, outcome)
	}
	return nil
}

func (s *agpState) eachCallEnds(code, stage string) error {
	if len(s.results) == 0 {
		return errors.New("no results")
	}
	for i, r := range s.results {
		if !r.IsError || string(r.Code) != code || string(r.Stage) != stage || r.Seq == 0 {
			return fmt.Errorf("call %d: %+v, want %s at %s with its tool/result", i, r, code, stage)
		}
	}
	return nil
}

func (s *agpState) refusalSays(name, text string) error {
	for _, r := range s.results {
		if r.Name == name {
			if !strings.Contains(r.Text, text) {
				return fmt.Errorf("the refusal of %s is %q, want it to say %q", name, r.Text, text)
			}
			return nil
		}
	}
	return fmt.Errorf("no result for %s", name)
}

func (s *agpState) laterStubsZero() error {
	for _, st := range []string{"validate", "policy", "guards", "approval", "flush", "body", "shape"} {
		if n := s.tr.count(st); n != 0 {
			return fmt.Errorf("the %s stub recorded %d invocations (trace %v)", st, n, s.tr.got())
		}
	}
	if n := s.tr.count("resolve"); n != len(s.results) {
		return fmt.Errorf("resolve ran %d times for %d calls", n, len(s.results))
	}
	return nil
}

func (s *agpState) secondConnSaw(csv string) error {
	if s.seenErr != nil {
		return fmt.Errorf("the second connection failed: %w", s.seenErr)
	}
	if s.seen == nil {
		return errors.New("the body never looked at the database")
	}
	if len(s.seen) < 4 {
		return fmt.Errorf("the second connection saw %v before the body, want the step's four first", s.seen)
	}
	if got, want := s.seen[4:], strings.Split(csv, ","); !slices.Equal(got, want) {
		return fmt.Errorf("the second connection saw %v after the step's four before the body, want exactly %v", got, want)
	}
	return nil
}

func (s *agpState) secondConnNotSaw(typ string) error {
	if slices.Contains(s.seen, typ) {
		return fmt.Errorf("the second connection saw %s before the body ran: %v", typ, s.seen)
	}
	return nil
}

// agpDurable is the record types of the step's session that a second
// connection to the database at path sees.
func agpDurable(ctx context.Context, path string) ([]string, error) {
	other, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer other.Close(ctx)
	r, err := other.OpenRead(ctx, agpSessionID)
	if err != nil {
		return nil, err
	}
	defer r.Close(ctx)
	recs, err := r.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, rec := range recs {
		out = append(out, rec.Type)
	}
	return out, nil
}

func agpNames(v tools.StepView) []string {
	var out []string
	for _, d := range v.Tools() {
		out = append(out, d.Name())
	}
	return out
}

func InitializeScenario_tool_pipeline_approvals_and_human_commands_approvals_and_guarded_pipeline(ctx *godog.ScenarioContext) {
	s := &agpState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = agpState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.dispose()
		return c, nil
	})

	ctx.Step(`^a booted guarded pipeline with no approval service mounted$`, s.pipelineNoApproval)
	ctx.Step(`^a booted guarded pipeline with recording stubs at every stage and an answerer that approves$`, s.pipelineRecordingApproves)
	ctx.Step(`^tool "([^"]+)" is registered with a body counter$`, s.registerAdd)
	ctx.Step(`^tool "([^"]+)" whose body reads the database from a second connection$`, s.registerDurableAdd)
	ctx.Step(`^tools "([^"]+)" and "([^"]+)" are registered$`, s.registerTextTools)
	ctx.Step(`^the step's fixed ToolSet admits only "([^"]+)"$`, s.admitsOnly)
	ctx.Step(`^a policy that asks for approval of every call$`, s.askPolicy)
	ctx.Step(`^the step's assembled prompt lists tool "([^"]+)"$`, s.promptLists)
	ctx.Step(`^the SQLite store in a temp home$`, s.sqliteHome)

	ctx.Step(`^the model calls "([^"]+)" with arguments '([^']*)'$`, s.modelCalls)
	ctx.Step(`^the model calls "([^"]+)" with arguments '([^']*)' and then "([^"]+)" with arguments '([^']*)'$`, s.modelCallsTwo)

	ctx.Step(`^the body counter is (\d+)$`, s.bodyCounter)
	ctx.Step(`^the call ends "([^"]+)" at the "([^"]+)" stage$`, s.callEnds)
	ctx.Step(`^the session log after the step holds "([^"]+)"$`, s.logHolds)
	ctx.Step(`^the tool/result returned to the model is an error starting with "([^"]+)"$`, s.resultToModel)
	ctx.Step(`^the approval outcome is "([^"]+)"$`, s.approvalOutcome)
	ctx.Step(`^the logged approval/decided has outcome "([^"]+)" attributed to a principal$`, s.decidedOutcome)
	ctx.Step(`^the recorded order is "([^"]+)"$`, s.recordedOrder)
	ctx.Step(`^the call succeeds with text "([^"]*)" and approval outcome "([^"]+)"$`, s.succeeds)
	ctx.Step(`^each call ends "([^"]+)" at the "([^"]+)" stage$`, s.eachCallEnds)
	ctx.Step(`^the refusal of "([^"]+)" says "([^"]+)"$`, s.refusalSays)
	ctx.Step(`^the validate, policy, guards, approval, flush, body and shape stubs record zero invocations$`, s.laterStubsZero)
	ctx.Step(`^the second connection saw "([^"]+)" before the body's first instruction$`, s.secondConnSaw)
	ctx.Step(`^the second connection saw no "([^"]+)" before the body's first instruction$`, s.secondConnNotSaw)
}

func TestE2E_tool_pipeline_approvals_and_human_commands_approvals_and_guarded_pipeline(t *testing.T) {
	agpOuterT = t
	t.Cleanup(func() { agpOuterT = nil })
	suite := godog.TestSuite{
		Name:                "tool_pipeline_approvals_and_human_commands_approvals_and_guarded_pipeline",
		ScenarioInitializer: InitializeScenario_tool_pipeline_approvals_and_human_commands_approvals_and_guarded_pipeline,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"tool_pipeline_approvals_and_human_commands_approvals_and_guarded_pipeline.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
