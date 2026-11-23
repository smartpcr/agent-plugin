package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

var me = agent.Ref{Session: session.ID("s-pipeline"), Incarnation: 1}

// trace records the stages a call passed, in order.
type trace struct {
	mu    sync.Mutex
	steps []string
}

func (tr *trace) add(s string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.steps = append(tr.steps, s)
}

func (tr *trace) got() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.steps)
}

func (tr *trace) count(s string) int {
	n := 0
	for _, x := range tr.got() {
		if x == s {
			n++
		}
	}
	return n
}

// The current trace of the validate hook; tests in this file run serially.
var validating *trace

type addIn struct {
	A int `json:"a" dsh:"required"`
	B int `json:"b" dsh:"required"`
}

func (addIn) Validate() error {
	if validating != nil {
		validating.add("validate")
	}
	return nil
}

func addTool(tr *trace) *tools.Declaration {
	return tools.Define(tools.Spec[addIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(_ context.Context, in addIn, _ tools.Run) (int, error) {
			tr.add("body")
			return in.A + in.B, nil
		},
	})
}

// recView records the resolve stage.
type recView struct {
	tools.StepView
	tr *trace
}

func (v recView) Lookup(name string) (*tools.Declaration, bool) {
	v.tr.add("resolve")
	return v.StepView.Lookup(name)
}

// stubRegistry answers every approval with ans, recording "approval".
type stubRegistry struct {
	human.Registry
	tr  *trace
	ans human.Answer
}

func (s stubRegistry) Ask(_ context.Context, it human.Item) (human.Answer, human.Actor, error) {
	s.tr.add("approval")
	return s.ans, human.Actor{Channel: "tui", Name: "ana"}, nil
}

// recCheckpoint records the flush and flushes.
type recCheckpoint struct{ tr *trace }

func (c recCheckpoint) Before(ctx context.Context, s checkpoint.Session, _ checkpoint.Point) error {
	c.tr.add("flush")
	_, err := s.Flush(ctx)
	return err
}

// openSession is a session whose step's assistant message asks for calls, each
// a name and its arguments, with ids c1, c2, ...
func openSession(t *testing.T, st store.Store, calls ...[2]string) session.Handle {
	t.Helper()
	root := newEnv(t).root
	h, err := session.NewService(root, st, session.Config{}).Create(context.Background(), nil, session.CreateOptions{Header: session.Header{ID: "s-pipeline"}})
	if err != nil {
		t.Fatal(err)
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		t.Fatal(err)
	}
	var uses []vocab.Block
	for i, c := range calls {
		uses = append(uses, vocab.ToolUseBlock(callID(i), c[0], json.RawMessage(c[1])))
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, uses...)
	if err != nil {
		t.Fatal(err)
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
			t.Fatal(err)
		}
		if _, err := h.Append(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func callID(i int) string { return "c" + string(rune('1'+i)) }

func requests(calls ...[2]string) []tools.CallRequest {
	var out []tools.CallRequest
	for i, c := range calls {
		out = append(out, tools.CallRequest{ID: callID(i), Name: c[0], Arguments: c[1]})
	}
	return out
}

// types is the event types the session holds after its first four.
func types(t *testing.T, h session.Handle) []string {
	t.Helper()
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs[4:] {
		out = append(out, e.Type())
	}
	return out
}

// pipeline replaces the env's registry with one configured by cfg.
func (e *env) pipeline(t *testing.T, cfg core.Config) tools.Service {
	t.Helper()
	e.svc = core.NewWith(e.root, e.tab, cfg)
	return e.svc
}

func view(t *testing.T, svc tools.Service) tools.StepView {
	t.Helper()
	v, err := svc.Resolve(context.Background(), tools.Target{Agent: me})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Release)
	return v
}

func execute(t *testing.T, svc tools.Service, v tools.StepView, h session.Handle, calls ...[2]string) []tools.Result {
	t.Helper()
	res, err := svc.ExecuteBatch(context.Background(), tools.Batch{View: v, Log: h, Calls: requests(calls...)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != len(calls) {
		t.Fatalf("%d results for %d calls", len(res.Results), len(calls))
	}
	return res.Results
}

func ask(_ context.Context, c tools.Call) tools.Decision {
	return tools.Ask("adds", "")
}

// DH-T-TOOLS-4, TC-10a: arguments that break the schema never reach the body; the
// model gets a validation error.
//
//dsh:test DH-T-TOOLS-4
func TestSchemaBreakingArgumentsNeverReachTheBody(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, addTool(tr))
	call := [2]string{"add", `{"a":"x"}`}
	h := openSession(t, memstore.New(), call)
	res := execute(t, svc, view(t, svc), h, call)[0]
	if tr.count("body") != 0 {
		t.Fatalf("the body ran %d times", tr.count("body"))
	}
	if !res.IsError || res.Code != tools.CodeInvalidArguments || res.Stage != tools.StageValidate {
		t.Fatalf("result %+v, want an invalid-arguments error at validate", res)
	}
	if !strings.HasPrefix(res.Text, string(tools.CodeInvalidArguments)+": ") {
		t.Fatalf("text %q", res.Text)
	}
	if got := strings.Join(types(t, h), ","); got != "tool/call,tool/result" {
		t.Fatalf("events %s", got)
	}
}

// DH-T-TOOLS-3: the stages run in their fixed order.
//
//dsh:test DH-T-TOOLS-3
func TestStageOrderFixed(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	validating = tr
	t.Cleanup(func() { validating = nil })
	reg := stubRegistry{tr: tr, ans: human.Answer{Outcome: human.OutcomeAllowedOnce}}
	svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg}), Checkpoint: recCheckpoint{tr}})
	e.register(t, e.root, addTool(tr))
	// Every listener reads Input, twice: reading must not decode or validate again.
	var seen []any
	read := func(c tools.Call) { seen = append(seen, c.Input(), c.Input()) }
	if _, err := svc.Shape(e.root, "shape", func(c tools.Call, _ tools.Outcome) tools.PostDecision {
		tr.add("shape")
		read(c)
		return tools.Accept()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Guard(e.root, "guard", func(_ context.Context, c tools.Call) *tools.Denial {
		tr.add("guards")
		read(c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Decide(e.root, "policy", func(ctx context.Context, c tools.Call) tools.Decision {
		tr.add("policy")
		read(c)
		return ask(ctx, c)
	}); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"add", `{"a":1,"b":2}`}
	h := openSession(t, memstore.New(), call)
	res := execute(t, svc, recView{view(t, svc), tr}, h, call)[0]
	want := []string{"resolve", "validate", "policy", "guards", "approval", "flush", "body", "shape"}
	if got := tr.got(); !slices.Equal(got, want) {
		t.Fatalf("stages %v, want %v", got, want)
	}
	if len(seen) != 6 {
		t.Fatalf("listeners read %d inputs, want 6", len(seen))
	}
	for i, v := range seen {
		if v != (addIn{A: 1, B: 2}) {
			t.Fatalf("read %d of Input is %#v, want the validated addIn{1,2}", i, v)
		}
	}
	if res.IsError || res.Text != "3" || res.Approval != string(approval.OutcomeAllowedOnce) {
		t.Fatalf("result %+v", res)
	}
	if got := strings.Join(types(t, h), ","); got != "tool/call,approval/asked,approval/decided,tool/result" {
		t.Fatalf("events %s", got)
	}
}

// DH-T-TOOLS-3: a tool that is not registered, or registered but not admitted to
// the step, is refused at resolve; nothing later runs and one tool/result closes
// each call.
//
//dsh:test DH-T-TOOLS-3
func TestUnknownOrUnadmittedToolRefusedBeforeExecution(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	validating = tr
	t.Cleanup(func() { validating = nil })
	reg := stubRegistry{tr: tr, ans: human.Answer{Outcome: human.OutcomeAllowedOnce}}
	svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg}), Checkpoint: recCheckpoint{tr}})
	agentScope := e.scope(t, "agent")
	e.register(t, e.root, echo("Echoes."))
	e.register(t, e.root, named("grep"))
	if _, err := svc.Restrict(agentScope.Ctx(), tools.Filter{Allow: []string{"echo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Decide(e.root, "policy", func(ctx context.Context, c tools.Call) tools.Decision { tr.add("policy"); return ask(ctx, c) }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Guard(e.root, "guard", func(context.Context, tools.Call) *tools.Denial { tr.add("guards"); return nil }); err != nil {
		t.Fatal(err)
	}
	// A shaper that records and then panics: were it dispatched, the refusal
	// would turn into tools/failed at shape.
	if _, err := svc.Shape(e.root, "shape", func(tools.Call, tools.Outcome) tools.PostDecision {
		tr.add("shape")
		panic("a refused call reached the shapers")
	}); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Resolve(context.Background(), tools.Target{Agent: me, Scope: agentScope.Key()})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	if got := names(v); !slices.Equal(got, []string{"echo"}) {
		t.Fatalf("view %v", got)
	}
	calls := [][2]string{{"rm", `{"path":"/"}`}, {"grep", `{"text":"x"}`}}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, recView{v, tr}, h, calls...)
	for i, r := range res {
		if !r.IsError || r.Code != tools.CodeNotStarted || r.Stage != tools.StageResolve || r.Seq == 0 {
			t.Fatalf("call %d: %+v, want tools/not-started at resolve", i, r)
		}
	}
	if !strings.Contains(res[0].Text, "not a registered tool") || !strings.Contains(res[1].Text, "not a tool of this step") {
		t.Fatalf("texts %q, %q", res[0].Text, res[1].Text)
	}
	if got := tr.got(); !slices.Equal(got, []string{"resolve", "resolve"}) {
		t.Fatalf("stages %v, want only the two resolves", got)
	}
	if got := strings.Join(types(t, h), ","); got != "tool/result,tool/result" {
		t.Fatalf("events %s", got)
	}
}

// DH-S-10 exit evidence, SP-7: a tool needing approval, visible in the step, with
// nobody to ask: the outcome is unavailable, the audit pair is logged and the
// body never runs; the tool was not hidden.
//
//dsh:test DH-T-TOOLS-5
func TestNoAnswererMeansNoThroughTheExecutor(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	svc := e.pipeline(t, core.Config{}) // nothing mounted under approval.Key
	e.register(t, e.root, addTool(tr))
	if _, err := svc.Decide(e.root, "ask", ask); err != nil {
		t.Fatal(err)
	}
	v := view(t, svc)
	if got := names(v); !slices.Contains(got, "add") {
		t.Fatalf("add is not in the prompt's tools %v", got)
	}
	call := [2]string{"add", `{"a":1,"b":2}`}
	h := openSession(t, memstore.New(), call)
	res := execute(t, svc, v, h, call)[0]
	if tr.count("body") != 0 {
		t.Fatal("the body ran")
	}
	if res.Approval != string(approval.OutcomeUnavailable) || res.Code != tools.CodeApprovalUnavailable || res.Stage != tools.StageApproval || res.By != core.ByApproval {
		t.Fatalf("result %+v", res)
	}
	if got := strings.Join(types(t, h), ","); got != "tool/call,approval/asked,approval/decided,tool/result" {
		t.Fatalf("events %s", got)
	}
	evs, _ := h.Read(context.Background(), 0, 0)
	var dec approval.Decided
	if err := json.Unmarshal([]byte(evs[6].Raw()), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Outcome != approval.OutcomeUnavailable || dec.By.Principal == "" {
		t.Fatalf("decided %+v", dec)
	}
}

// The other approval outcomes, through the executor: only an approval runs the
// body.
func TestApprovalOutcomesThroughTheExecutor(t *testing.T) {
	for _, tc := range []struct {
		ans  human.Outcome
		out  approval.Outcome
		code tools.Code
		runs int
	}{
		{human.OutcomeAllowedOnce, approval.OutcomeAllowedOnce, "", 1},
		{human.OutcomeDenied, approval.OutcomeRejected, tools.CodeApprovalRejected, 0},
		{human.OutcomeUnavailable, approval.OutcomeUnavailable, tools.CodeApprovalUnavailable, 0},
	} {
		t.Run(string(tc.ans), func(t *testing.T) {
			e := newEnv(t)
			tr := &trace{}
			reg := stubRegistry{tr: tr, ans: human.Answer{Outcome: tc.ans}}
			svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg})})
			e.register(t, e.root, addTool(tr))
			if _, err := svc.Decide(e.root, "ask", ask); err != nil {
				t.Fatal(err)
			}
			call := [2]string{"add", `{"a":1,"b":2}`}
			h := openSession(t, memstore.New(), call)
			res := execute(t, svc, view(t, svc), h, call)[0]
			if tr.count("body") != tc.runs || res.Code != tc.code || res.Approval != string(tc.out) {
				t.Fatalf("body ran %d times, result %+v", tr.count("body"), res)
			}
		})
	}
}

// Denial is enforced in the executor: the strongest verdict wins whatever the
// order, a guard denies on its own, and the tool stays in the view.
//
//dsh:test DH-T-TOOLS-5
func TestDenialEnforcedInTheExecutor(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, addTool(tr))
	if _, err := svc.Decide(e.root, "allow", func(context.Context, tools.Call) tools.Decision { return tools.Allow() }); err != nil {
		t.Fatal(err)
	}
	h1, err := svc.Decide(e.root, "deny", func(context.Context, tools.Call) tools.Decision {
		return tools.Deny("", "writes are off")
	})
	if err != nil {
		t.Fatal(err)
	}
	call := [2]string{"add", `{"a":1,"b":2}`}
	h := openSession(t, memstore.New(), call, call2)
	v := view(t, svc)
	res := execute(t, svc, v, h, call)[0]
	if res.Code != tools.CodeDenied || res.By != "deny" || res.Stage != tools.StagePolicy || tr.count("body") != 0 {
		t.Fatalf("result %+v", res)
	}
	h1.Dispose()
	if _, err := svc.Guard(e.root, "fence", func(context.Context, tools.Call) *tools.Denial {
		return &tools.Denial{Reason: "outside the workspace"}
	}); err != nil {
		t.Fatal(err)
	}
	res2, err := svc.ExecuteBatch(context.Background(), tools.Batch{View: v, Log: h, Calls: []tools.CallRequest{{ID: "c2", Name: "add", Arguments: call2[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	if r := res2.Results[0]; r.Code != tools.CodeDenied || r.By != "fence" || r.Stage != tools.StageGuards || tr.count("body") != 0 {
		t.Fatalf("result %+v", r)
	}
	if _, ok := v.Lookup("add"); !ok {
		t.Fatal("a denied tool was hidden")
	}
}

var call2 = [2]string{"add", `{"a":3,"b":4}`}

// The logged arguments are the model's bytes; nothing a listener does to the
// Call's arguments reaches the log or the body.
//
//dsh:test DH-T-TOOLS-4
func TestLoggedArgumentsNeverRewritten(t *testing.T) {
	e := newEnv(t)
	var got addIn
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, tools.Define(tools.Spec[addIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(_ context.Context, in addIn, _ tools.Run) (int, error) { got = in; return in.A + in.B, nil },
	}))
	if _, err := svc.Decide(e.root, "meddle", func(_ context.Context, c tools.Call) tools.Decision {
		a := c.Arguments()
		copy(a, []byte(`{"a":9`))
		return tools.Allow()
	}); err != nil {
		t.Fatal(err)
	}
	raw := `{ "a" : 1, "b":2 }`
	call := [2]string{"add", raw}
	h := openSession(t, memstore.New(), call)
	execute(t, svc, view(t, svc), h, call)
	evs, _ := h.Read(context.Background(), 0, 0)
	var p event.ToolCallPayload
	if err := json.Unmarshal([]byte(evs[4].Raw()), &p); err != nil {
		t.Fatal(err)
	}
	if p.Arguments != raw || got.A != 1 {
		t.Fatalf("logged %q, body got %+v", p.Arguments, got)
	}
}

// pair is a slice, so a decoded listIn shares backing storage with anything it
// is copied to; its wire shape is a fixed pair, which a strict schema admits.
type pair []string

func (pair) SchemaShape() reflect.Type { return reflect.TypeFor[[2]string]() }

type listIn struct {
	Items pair `json:"items" dsh:"required"`
}

// rawBytes is a dynamic tool's decoded input as bytes, whatever its byte type.
func rawBytes(t *testing.T, v any) []byte {
	t.Helper()
	switch b := v.(type) {
	case json.RawMessage:
		return b
	case []byte:
		return b
	}
	t.Fatalf("dynamic input is %T", v)
	return nil
}

// A decoded input a listener mutates, through a slice or raw JSON,
// changes neither what a later listener sees nor what the body receives.
func TestDecodedInputIsolatedFromListeners(t *testing.T) {
	const raw = `{"items":["a","b"]}`
	t.Run("typed", func(t *testing.T) {
		e := newEnv(t)
		var got listIn
		svc := e.pipeline(t, core.Config{})
		e.register(t, e.root, tools.Define(tools.Spec[listIn, string]{
			Name: "list", Description: "Lists.",
			Run: func(_ context.Context, in listIn, _ tools.Run) (string, error) { got = in; return "ok", nil },
		}))
		var seen listIn
		if _, err := svc.Decide(e.root, "meddle", func(_ context.Context, c tools.Call) tools.Decision {
			in := c.Input().(listIn)
			in.Items[0] = "x"
			return tools.Allow()
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Guard(e.root, "look", func(_ context.Context, c tools.Call) *tools.Denial {
			seen = c.Input().(listIn)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		call := [2]string{"list", raw}
		h := openSession(t, memstore.New(), call)
		if r := execute(t, svc, view(t, svc), h, call)[0]; r.IsError {
			t.Fatalf("result %+v", r)
		}
		for name, in := range map[string]listIn{"guard": seen, "body": got} {
			if len(in.Items) != 2 || in.Items[0] != "a" {
				t.Fatalf("the %s saw the policy's mutation: %+v", name, in)
			}
		}
	})
	t.Run("dynamic", func(t *testing.T) {
		e := newEnv(t)
		var got, seen string
		svc := e.pipeline(t, core.Config{})
		e.register(t, e.root, tools.Dynamic(tools.DynamicSpec{
			Name: "dyn", Description: "Dynamic.", Origin: "test",
			Schema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"string"}}}}`),
			Run: func(_ context.Context, args json.RawMessage, _ tools.Run) (json.RawMessage, error) {
				got = string(args)
				return json.RawMessage(`"ok"`), nil
			},
		}))
		if _, err := svc.Decide(e.root, "meddle", func(_ context.Context, c tools.Call) tools.Decision {
			copy(rawBytes(t, c.Input()), `{"items":["Z"`)
			return tools.Allow()
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Guard(e.root, "look", func(_ context.Context, c tools.Call) *tools.Denial {
			seen = string(rawBytes(t, c.Input()))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		dynRaw := `{"items":["a","b"]}`
		call := [2]string{"dyn", dynRaw}
		h := openSession(t, memstore.New(), call)
		if r := execute(t, svc, view(t, svc), h, call)[0]; r.IsError {
			t.Fatalf("result %+v", r)
		}
		if seen != dynRaw || got != dynRaw {
			t.Fatalf("guard saw %q, body got %q; want %q", seen, got, dynRaw)
		}
	})
}

// stamp decodes statefully: each decode takes the next number of decodes, so a
// second decode of the same bytes yields a different value.
type stamp struct{ N int }

var stamps int

func (stamp) SchemaShape() reflect.Type { return reflect.TypeFor[string]() }

func (s *stamp) UnmarshalJSON([]byte) error { stamps++; s.N = stamps; return nil }

type stampIn struct {
	S    stamp `json:"s" dsh:"required"`
	Tags pair  `json:"tags" dsh:"required"`
}

var stampValidations int

func (stampIn) Validate() error { stampValidations++; return nil }

// Listeners read copies of the value validated once: no listener read decodes or
// validates again, and what every listener sees equals what the body received.
func TestListenerInputIsTheValidatedValue(t *testing.T) {
	stamps, stampValidations = 0, 0
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	var body stampIn
	e.register(t, e.root, tools.Define(tools.Spec[stampIn, string]{
		Name: "stamp", Description: "Stamps.",
		Run: func(_ context.Context, in stampIn, _ tools.Run) (string, error) { body = in; return "ok", nil },
	}))
	var seen []stampIn
	read := func(c tools.Call) {
		for range 3 {
			seen = append(seen, c.Input().(stampIn))
		}
	}
	if _, err := svc.Decide(e.root, "policy", func(_ context.Context, c tools.Call) tools.Decision { read(c); return tools.Allow() }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Guard(e.root, "guard", func(_ context.Context, c tools.Call) *tools.Denial { read(c); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Shape(e.root, "shape", func(c tools.Call, _ tools.Outcome) tools.PostDecision { read(c); return tools.Accept() }); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"stamp", `{"s":"x","tags":["a","b"]}`}
	h := openSession(t, memstore.New(), call)
	if r := execute(t, svc, view(t, svc), h, call)[0]; r.IsError {
		t.Fatalf("result %+v", r)
	}
	if stamps != 1 || stampValidations != 1 {
		t.Fatalf("decoded %d times and validated %d times, want once each", stamps, stampValidations)
	}
	if len(seen) != 9 {
		t.Fatalf("listeners read %d inputs, want 9", len(seen))
	}
	for i, v := range seen {
		if !reflect.DeepEqual(v, body) {
			t.Fatalf("read %d is %+v, the body received %+v", i, v, body)
		}
	}
}

// bag is opaque: its state is a private slice, reachable only through methods,
// one of which mutates it in place.
type bag struct{ items []string }

func (bag) SchemaShape() reflect.Type { return reflect.TypeFor[[2]string]() }

func (b *bag) UnmarshalJSON(raw []byte) error { return json.Unmarshal(raw, &b.items) }

func (b bag) First() string { return b.items[0] }

func (b bag) Poison() { b.items[0] = "poisoned" }

type bagIn struct {
	B bag `json:"b" dsh:"required"`
}

// A listener calling an exported method that mutates an opaque input's private
// state changes neither what a later listener sees nor what the body receives.
func TestOpaqueInputPrivateStateIsolated(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	var body, guard, shaper string
	e.register(t, e.root, tools.Define(tools.Spec[bagIn, string]{
		Name: "bag", Description: "Bags.",
		Run: func(_ context.Context, in bagIn, _ tools.Run) (string, error) { body = in.B.First(); return "ok", nil },
	}))
	if _, err := svc.Decide(e.root, "poison", func(_ context.Context, c tools.Call) tools.Decision {
		c.Input().(bagIn).B.Poison()
		return tools.Allow()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Guard(e.root, "look", func(_ context.Context, c tools.Call) *tools.Denial {
		c.Input().(bagIn).B.Poison()
		guard = c.Input().(bagIn).B.First()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Shape(e.root, "look", func(c tools.Call, _ tools.Outcome) tools.PostDecision {
		shaper = c.Input().(bagIn).B.First()
		return tools.Accept()
	}); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"bag", `{"b":["a","b"]}`}
	h := openSession(t, memstore.New(), call)
	if r := execute(t, svc, view(t, svc), h, call)[0]; r.IsError {
		t.Fatalf("result %+v", r)
	}
	if body != "a" || guard != "a" || shaper != "a" {
		t.Fatalf("body saw %q, guard %q, shaper %q; want the validated %q everywhere", body, guard, shaper, "a")
	}
}

// hook decodes into a function, which no copy can isolate.
type hook struct{ Fn func() }

func (hook) SchemaShape() reflect.Type { return reflect.TypeFor[string]() }

func (h *hook) UnmarshalJSON([]byte) error { h.Fn = func() {}; return nil }

type hookIn struct {
	H hook `json:"h" dsh:"required"`
}

// An input that cannot be copied for the listeners is refused at validate, with
// the reason, before any listener or the body; it is never shared nor nil.
func TestUncopyableInputRefusedAtValidate(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, tools.Define(tools.Spec[hookIn, string]{
		Name: "hook", Description: "Hooks.",
		Run: func(context.Context, hookIn, tools.Run) (string, error) { tr.add("body"); return "ok", nil },
	}))
	if _, err := svc.Decide(e.root, "policy", func(context.Context, tools.Call) tools.Decision { tr.add("policy"); return tools.Allow() }); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"hook", `{"h":"x"}`}
	h := openSession(t, memstore.New(), call)
	r := execute(t, svc, view(t, svc), h, call)[0]
	if !r.IsError || r.Code != tools.CodeInvalidArguments || r.Stage != tools.StageValidate || !strings.Contains(r.Text, "cannot be isolated") || !strings.Contains(r.Text, ".H.Fn") {
		t.Fatalf("result %+v", r)
	}
	if got := tr.got(); len(got) != 0 {
		t.Fatalf("ran %v after the refusal", got)
	}
}

// boomOut's encoding panics.
type boomOut struct{ N int }

func (boomOut) MarshalJSON() ([]byte, error) { panic("cannot encode") }

// A value whose encoding panics after the body ran still settles its call with
// one error tool/result, and the batch goes on to the next call.
func TestOutputEncodingPanicSettlesTheCall(t *testing.T) {
	e := newEnv(t)
	tr := &trace{}
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, addTool(tr))
	e.register(t, e.root, tools.Define(tools.Spec[addIn, boomOut]{
		Name: "boom", Description: "Returns what cannot be encoded.",
		Run: func(context.Context, addIn, tools.Run) (boomOut, error) { tr.add("boom"); return boomOut{1}, nil },
	}))
	calls := [][2]string{{"boom", `{"a":1,"b":2}`}, {"add", `{"a":1,"b":2}`}}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	if r := res[0]; !r.IsError || r.Code != tools.CodeInvalidOutput || r.Stage != tools.StageShape || !strings.Contains(r.Text, "panicked") {
		t.Fatalf("boom result %+v", r)
	}
	if r := res[1]; r.IsError || r.Text != "3" {
		t.Fatalf("add result %+v", r)
	}
	if tr.count("boom") != 1 || tr.count("body") != 1 {
		t.Fatalf("bodies ran %v", tr.got())
	}
	if got := strings.Join(types(t, h), ","); got != "tool/call,tool/result,tool/call,tool/result" {
		t.Fatalf("events %s", got)
	}
}

// TC-10b: on the SQLite store, the approval audit is durable, visible from a
// second connection, before the body's first instruction.
func TestFlushBeforeBodyObservedInTheDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(ctx) })
	e := newEnv(t)
	var seen []string
	var bodyErr error
	tr := &trace{}
	reg := stubRegistry{tr: tr, ans: human.Answer{Outcome: human.OutcomeAllowedOnce}}
	svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg}), Checkpoint: recCheckpoint{tr}})
	e.register(t, e.root, tools.Define(tools.Spec[addIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(ctx context.Context, in addIn, _ tools.Run) (int, error) {
			tr.add("body")
			seen, bodyErr = durable(ctx, path)
			return in.A + in.B, nil
		},
	}))
	if _, err := svc.Decide(e.root, "ask", ask); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"add", `{"a":1,"b":2}`}
	h := openSession(t, st, call)
	res := execute(t, svc, view(t, svc), h, call)[0]
	if res.IsError || bodyErr != nil {
		t.Fatalf("result %+v, body: %v", res, bodyErr)
	}
	if !slices.Contains(seen, approval.DecidedType.Name()) || !slices.Contains(seen, event.ToolCall.Name()) {
		t.Fatalf("the second connection saw %v before the body", seen)
	}
	if got := tr.got(); !slices.Equal(got, []string{"approval", "flush", "body"}) {
		t.Fatalf("stages %v, want the flush between the approval and the body", got)
	}
}

// durable is the record types a second connection to the database at path sees.
func durable(ctx context.Context, path string) ([]string, error) {
	other, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer other.Close(ctx)
	r, err := other.OpenRead(ctx, "s-pipeline")
	if err != nil {
		return nil, err
	}
	defer r.Close(ctx)
	recs, err := r.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range recs {
		out = append(out, rec.Type)
	}
	return out, nil
}

// An empty batch view or log is refused before anything runs.
func TestInvalidBatchRefused(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	if _, err := svc.ExecuteBatch(context.Background(), tools.Batch{}); !errors.Is(err, tools.ErrInvalidBatch) {
		t.Fatalf("err %v, want tools/invalid-batch", err)
	}
}
