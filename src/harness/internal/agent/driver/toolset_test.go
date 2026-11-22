package driver_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type textIn struct {
	Text string `json:"text" dsh:"required"`
}

func tool(name string) *tools.Declaration {
	return tools.Define(tools.Spec[textIn, string]{
		Name: name, Description: "Tool " + name + ".",
		Run:     func(_ context.Context, in textIn, _ tools.Run) (string, error) { return in.Text, nil },
		Overlap: func(textIn) tools.Overlap { return tools.Free },
	})
}

// stubAgent is the agent the driver runs for; only Ref and ScopeKey are read.
type stubAgent struct {
	agent.Agent
	ref agent.Ref
}

func (a stubAgent) Ref() agent.Ref               { return a.ref }
func (a stubAgent) ScopeKey() scope.Key          { return scope.Key{} }
func (a stubAgent) Scope() *cordis.Context       { return nil }
func (a stubAgent) Session() session.ReadSession { return nil }

func setup(t *testing.T) (*cordis.Context, tools.Service) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		must(0, must(root.Use(c, cordis.None{})).Wait())
	}
	var svc tools.Service
	g := must(root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		svc, err = tools.Key.Get(c)
		return err
	}))
	must(0, g.Wait())
	return root, svc
}

// openStep is a session whose open step's assistant message asks for calls.
func openStep(t *testing.T, root *cordis.Context, step int, h session.Handle, calls ...tools.CallRequest) session.Handle {
	t.Helper()
	if h == nil {
		h = must(session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s-driver"}}))
		must(h.Append(bg, must(event.TurnStartType.Draft(event.TurnStart{Turn: 1}))))
	}
	must(h.Append(bg, must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: step}))))
	var uses []vocab.Block
	for _, c := range calls {
		uses = append(uses, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	}
	msg := must(vocab.NewMessage(vocab.RoleAssistant, uses...))
	must(h.Append(bg, must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: msg}))))
	return h
}

func schemaNames(s *driver.StepTools) []string {
	var out []string
	for _, sc := range s.Schemas() {
		out = append(out, sc.Name)
	}
	return out
}

// DH-D-AGENT-10, TC-11a: a step's tool set is fixed at step start. A tool
// registered mid-step is neither shown nor run; its call ends tools/not-started,
// and it is available from the next step.
func TestStepToolSetFixedAtStepStart(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	a := stubAgent{ref: agent.Ref{Session: "s-driver", Incarnation: 1}}

	st := must(driver.ResolveStep(bg, svc, a, agent.ModelSelection{Route: "r/a"}))
	if got := schemaNames(st); len(got) != 1 || got[0] != "echo" {
		t.Fatalf("step 1 schemas %v", got)
	}
	if tgt := st.View().Target(); tgt.Agent != a.ref || tgt.Model.Route != "r/a" {
		t.Fatalf("target %+v", tgt)
	}
	must(svc.Register(root, tool("add")))
	if got := schemaNames(st); len(got) != 1 {
		t.Fatalf("the step's schemas moved to %v", got)
	}
	calls := []tools.CallRequest{{ID: "c1", Name: "echo", Arguments: `{"text":"hi"}`}, {ID: "c2", Name: "add", Arguments: `{"text":"x"}`}}
	h := openStep(t, root, 1, nil, calls...)
	res := must(st.Execute(bg, h, calls)).Results
	if res[0].IsError || res[0].Text != "hi" {
		t.Fatalf("echo %+v", res[0])
	}
	if !res[1].IsError || res[1].Code != tools.CodeNotStarted {
		t.Fatalf("add registered mid-step: %+v", res[1])
	}
	st.Release()
	st.Release()
	if _, ok := st.View().Lookup("echo"); ok {
		t.Fatal("a released step still looks up tools")
	}
	must(h.Append(bg, must(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 1}))))

	next := must(driver.ResolveStep(bg, svc, a, agent.ModelSelection{Route: "r/a"}))
	defer next.Release()
	if got := schemaNames(next); len(got) != 2 || got[0] != "add" {
		t.Fatalf("step 2 schemas %v", got)
	}
	call := []tools.CallRequest{{ID: "c3", Name: "add", Arguments: `{"text":"y"}`}}
	openStep(t, root, 2, h, call...)
	if r := must(next.Execute(bg, h, call)).Results[0]; r.IsError {
		t.Fatalf("add in step 2: %+v", r)
	}
}
