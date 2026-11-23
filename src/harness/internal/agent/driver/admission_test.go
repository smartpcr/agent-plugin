package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// groups records every append group the driver and the inbox write.
type groups struct {
	mu  sync.Mutex
	all [][]string
}

func (g *groups) record(_ context.Context, ds []event.Draft) error {
	var types []string
	for _, d := range ds {
		types = append(types, d.Type())
	}
	g.mu.Lock()
	g.all = append(g.all, types)
	g.mu.Unlock()
	return nil
}

func (g *groups) list() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.all)
}

// between are the event types after the n-th step/start, up to its first
// assistant settlement, without the step's model/selection capture.
func between(evs []event.Event, n int) []string {
	var out []string
	seen := 0
	for _, e := range evs {
		if e.Type() == "step/start" {
			seen++
			continue
		}
		if seen != n || e.Type() == "model/selection" {
			continue
		}
		if e.Type() == "assistant/message" || e.Type() == "assistant/attempt" {
			break
		}
		out = append(out, e.Type())
	}
	return out
}

func headers(t *testing.T, evs []event.Event) []event.RequestHeaderPayload {
	var out []event.RequestHeaderPayload
	for _, e := range ofType(evs, "request/header") {
		out = append(out, decode[event.RequestHeaderPayload](t, e))
	}
	return out
}

func toolNames(ts []vocab.ToolSchema) []string {
	var out []string
	for _, s := range ts {
		out = append(out, s.Name)
	}
	return out
}

// DH-T-AGENT-27: a composition without the prompt assembly is refused, by Start
// and by NewFactory; the factory injects the service the tools.prompt component
// provides under prompt.Key.
//
//dsh:test DH-T-AGENT-27
func TestCompositionWithoutAPrompterIsRefused(t *testing.T) {
	root, svc := setup(t)
	sess := must(session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s-np"}}))
	t.Cleanup(func() { _ = sess.Close(bg) })
	opts := driver.Options{
		Context: root, Agent: stubAgent{ref: agent.Ref{Session: "s-np", Incarnation: 1}}, Session: sess,
		Inbox: must(agent.OpenInbox(bg, sess)), Model: must(agent.OpenModel(bg, sess, agent.ModelOptions{})),
		Models: llm.New(), Tools: svc,
	}
	if d, err := driver.Start(opts); err == nil || d != nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("Start without a prompter: %v, %v", d, err)
	}
	fo := driver.FactoryOptions{Context: root, Sessions: session.NewService(root, memstore.New(), session.Config{}),
		Scopes: scope.NewTable(root), Models: llm.New(), Tools: svc}
	if f, err := driver.NewFactory(fo); err == nil || f != nil || !strings.Contains(err.Error(), prompt.Key.Name()) {
		t.Fatalf("NewFactory without a prompter: %v, %v", f, err)
	}
	must(0, must(root.Use(prompt.Component, prompt.Defaults())).Wait())
	if _, err := driver.NewFactory(fo); err != nil {
		t.Fatalf("NewFactory with %s provided: %v", prompt.Key.Name(), err)
	}
}

// 17.1.2: a failed assembly ends the turn error{by: driver/prompt} and opens no
// step; nothing of the claimed input is admitted.
func TestAssemblyFailureOpensNoStep(t *testing.T) {
	p := newTestPrompt(defaultPrompt)
	p.fail = errors.New("a section failed")
	r := newRig(t, rigOptions{prompt: p})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	if n := len(ofType(evs, "step/start")); n != 0 {
		t.Fatalf("%d steps opened", n)
	}
	got := turnEnds(t, evs)
	if len(got) != 1 || got[0].Kind != event.ReasonError || got[0].By != "driver/prompt" || !strings.Contains(got[0].Detail, "a section failed") {
		t.Fatalf("turn/end %+v, want error{by: driver/prompt}", got)
	}
	if n := len(ofType(evs, "user/message")) + len(ofType(evs, "system/message")); n != 0 {
		t.Fatalf("%d messages admitted for a step that never opened", n)
	}
	if n := p.assembled(); n != 1 {
		t.Fatalf("assembled %d times, want once", n)
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-12, 17.1.4, 17.1.8: the claim writes only its splice; the step's
// admission is one group of the system prompt, the claimed messages (the turn's
// item, then the next-step input) and the header; the system prompt is node 0
// and the first request's leading message. A retry reuses the assembly and
// appends no second header.
//
//dsh:test DH-T-AGENT-12
func TestAdmissionIsOneBatch(t *testing.T) {
	g := &groups{}
	f := &faulty{}
	f.set(g.record, nil)
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	var retries atomic.Int32
	must(agent.AttemptFailed.On(root, func(_ *cordis.Context, _ agent.AttemptFailure, _ func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
		retries.Add(1)
		return agent.Retry{}, nil
	}))
	r := newRig(t, rigOptions{root: root, svc: svc, wrap: f.wrap, turns: []fakemodel.Turn{fakemodel.Status(500), fakemodel.Reply("done")}})
	r.send(agent.ListNextStep, "steer")
	r.send(agent.ListNextTurn, "go")
	r.idle()

	var admit []string
	for _, gr := range g.list() {
		switch {
		case slices.Contains(gr, "turn/start"):
			if strings.Join(gr, ",") != "turn/start,agent/inbox/spliced" {
				t.Fatalf("the turn's claim group is %v, want turn/start and the splice only", gr)
			}
		case slices.Contains(gr, "user/message"):
			if admit != nil {
				t.Fatalf("a second admission group %v", gr)
			}
			admit = gr
		}
	}
	if strings.Join(admit, ",") != "system/message,user/message,user/message,request/header" {
		t.Fatalf("admission group %v", admit)
	}
	evs := r.events()
	var users []string
	for _, e := range ofType(evs, "user/message") {
		m := decode[event.UserMessagePayload](t, e).Message
		users = append(users, m.Blocks()[0].Text.Text)
	}
	if !slices.Equal(users, []string{"go", "steer"}) {
		t.Fatalf("admitted %v, want the turn's item, then the steering", users)
	}
	if h := r.sess.History(); h.Len() < 3 || h.At(0).Type() != "system/message" {
		t.Fatalf("node 0 is not the system prompt: %s", must(json.Marshal(h)))
	}
	sent := r.sent.requests()
	if retries.Load() != 1 || len(sent) != 2 {
		t.Fatalf("%d retries, %d requests", retries.Load(), len(sent))
	}
	for i, req := range sent {
		msgs := must(req.Messages())
		if msgs[0].Role() != vocab.RoleSystem || msgs[0].Blocks()[0].Text.Text != defaultPrompt {
			t.Fatalf("request %d leads with %s", i+1, msgs[0].Role())
		}
	}
	if n := len(ofType(evs, "request/header")); n != 1 {
		t.Fatalf("%d headers, want one: a retry appends only a changed header", n)
	}
	if n := r.prompt.assembled(); n != 1 {
		t.Fatalf("assembled %d times for one step and its retry", n)
	}
	hp := headers(t, evs)[0]
	if hp.Fingerprint == "" || len(hp.Manifest) != 1 || hp.Manifest[0].Owner != "driver-test" {
		t.Fatalf("header %+v lacks the assembly's fingerprint and manifest", hp)
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-12, M1-D-LOOP-1 A with the context pipeline (17.3): the entered
// batch is logged as PreStep returns it: a claimed message the pipeline rewords
// keeps its id and is logged reworded, and each message is logged in the
// returned order (the claimed message, its After messages, then a
// contributor's), not the claimed messages first.
//
//dsh:test DH-T-AGENT-12
func TestEnteredMessagesAreAdmittedInTheirOrder(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	must(agent.PreStep.On(root, func(_ *cordis.Context, req agent.PreStepRequest, next func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		if req.Step != 1 {
			return next(req)
		}
		var out []vocab.Message
		for _, m := range req.Messages {
			src, _ := m.Source()
			reworded := must(vocab.RestoreMessage(m.ID(), m.Role(), &src, vocab.TextBlock("reworded: "+m.Blocks()[0].Text.Text)))
			after := must(vocab.NewMessage(vocab.RoleUser, vocab.TextBlock("after "+m.Blocks()[0].Text.Text)))
			out = append(out, reworded, after)
		}
		out = append(out, must(vocab.NewMessage(vocab.RoleDeveloper, vocab.TextBlock("contributed"))))
		return agent.Enter{Messages: out}, nil
	}))
	r := newRig(t, rigOptions{root: root, svc: svc, turns: []fakemodel.Turn{fakemodel.Reply("done")}})
	r.send(agent.ListNextStep, "steer")
	r.send(agent.ListNextTurn, "go")
	r.idle()
	var got []string
	ids := map[string]bool{}
	for _, e := range r.events() {
		var m vocab.Message
		switch e.Type() {
		case "user/message":
			m = decode[event.UserMessagePayload](t, e).Message
		case "developer/message":
			m = decode[event.MessagePayload](t, e).Message
		default:
			continue
		}
		got = append(got, m.Blocks()[0].Text.Text)
		ids[m.ID()] = true
	}
	want := []string{"reworded: go", "after go", "reworded: steer", "after steer", "contributed"}
	if !slices.Equal(got, want) {
		t.Fatalf("admitted %q, want %q", got, want)
	}
	for _, d := range must(agent.OpenInbox(bg, r.sess)).Discarded() {
		t.Fatalf("a reworded claimed message was reported discarded: %+v", d)
	}
	if len(ids) != len(want) {
		t.Fatalf("ids %v", ids)
	}
	checkClosed(t, r.sess)
}

// 17.1.7, M1-G-LOOP-4, scenario "An unchanged prompt adds nothing": two steps
// with an identical assembly append one system/message and one request/header;
// step 2 appends neither, although the header in force holds the schemas as the
// log stored them (keys sorted) and the step's view as they were derived.
func TestUnchangedHeaderIsNotRepeated(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	if got := between(evs, 2); len(got) != 0 {
		t.Fatalf("step 2 admitted %v, want nothing", got)
	}
	if h, s := len(ofType(evs, "request/header")), len(ofType(evs, "system/message")); h != 1 || s != 1 {
		t.Fatalf("%d headers and %d system messages, want one each", h, s)
	}
	// The stored and the derived parameters differ as bytes but not as schemas.
	cur, _ := r.sess.RequestHeader()
	derived := tool("echo").Schema()
	if string(cur.Tools[0].Parameters) == string(derived.Parameters) {
		t.Fatal("the stored and derived parameters are byte-equal: the canonical comparison is not exercised")
	}
	checkClosed(t, r.sess)
}

// 17.1.4: a changed prompt rewrites node 0 with a system/message that replaces
// exactly that node (event.Replace(seq0, seq0)), which starts a new request
// series: the header follows it, and the history holds one system prompt.
func TestChangedPromptReplacesNodeZero(t *testing.T) {
	var r *rig
	hooks := driver.Hooks{ContinueOrStop: func(_, step int) {
		if step == 1 {
			r.prompt.set(func(p *testPrompt) { p.text = "You are the edited test agent." })
		}
	}}
	r = newRig(t, rigOptions{hooks: hooks, turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	if got := strings.Join(between(evs, 2), ","); got != "system/message,request/header" {
		t.Fatalf("step 2 admitted %s", got)
	}
	sys := ofType(evs, "system/message")
	if len(sys) != 2 || sys[1].Op().Kind != event.OpReplace || sys[1].Op().Start != sys[0].Seq() || sys[1].Op().End != sys[0].Seq() {
		t.Fatalf("system messages %v: the second does not replace node 0", sys)
	}
	h := r.sess.History()
	if h.At(0).Type() != "system/message" || h.At(0).Seq() != sys[1].Seq() || h.At(0).Revision() != 1 {
		t.Fatalf("node 0 is seq %d rev %d", h.At(0).Seq(), h.At(0).Revision())
	}
	if th := r.sess.ToolHistory(); th.Base != ofType(evs, "request/header")[1].Seq() {
		t.Fatalf("the replace did not start a series: base %d", th.Base)
	}
	if last := r.sent.requests()[1]; must(last.Messages())[0].Blocks()[0].Text.Text != "You are the edited test agent." {
		t.Fatal("step 2's request does not lead with the edited prompt")
	}
	checkClosed(t, r.sess)
}

// DH-T-AGENT-26, 17.1.5, 17.1.6, scenario "Tool-change notice": a tool
// registered between step 1 and step 2 gives exactly one developer/message
// naming it, a tool update citing step 2's header, whose tools follow the
// assembly's order (toolOrder), not the view's name order.
//
//dsh:test DH-T-AGENT-26
func TestToolChangeNoticeNamesTheAddedTool(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	p := newTestPrompt(defaultPrompt)
	p.order = []string{"echo", "add"}
	var r *rig
	hooks := driver.Hooks{ContinueOrStop: func(_, step int) {
		if step == 1 {
			must(svc.Register(root, tool("add")))
		}
	}}
	r = newRig(t, rigOptions{root: root, svc: svc, prompt: p, hooks: hooks,
		turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Calls(fakemodel.Tool("c2", "add", `{"text":"y"}`)), fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	if got := strings.Join(between(evs, 2), ","); got != "request/header,developer/message" {
		t.Fatalf("step 2 admitted %s", got)
	}
	if got := between(evs, 3); len(got) != 0 {
		t.Fatalf("step 3 admitted %v", got)
	}
	devs := ofType(evs, "developer/message")
	if len(devs) != 1 {
		t.Fatalf("%d notices, want exactly one", len(devs))
	}
	m := decode[event.MessagePayload](t, devs[0]).Message
	src, _ := m.Source()
	var u surface.ToolUpdate
	must(0, json.Unmarshal(src.Data, &u))
	hs := ofType(evs, "request/header")
	if src.Kind != surface.ToolUpdateKind || u.Header != hs[1].Seq() || !slices.Equal(u.Added, []string{"add"}) || len(u.Removed) != 0 ||
		!strings.Contains(m.Blocks()[0].Text.Text, "Added: add.") {
		t.Fatalf("notice %s", devs[0].Raw())
	}
	if got := toolNames(headers(t, evs)[1].Tools); !slices.Equal(got, []string{"echo", "add"}) {
		t.Fatalf("step 2's header lists %v, want the assembly's order", got)
	}
	if got := toolNames(r.sess.ToolHistory().Active()); !slices.Equal(got, []string{"echo", "add"}) {
		t.Fatalf("active tools %v", got)
	}
	if res := ofType(evs, "tool/result"); len(res) != 2 || decode[event.ToolResultPayload](t, res[1]).IsError {
		t.Fatalf("add did not run in step 2: %v", res)
	}
	checkClosed(t, r.sess)
}

// 17.1.4: a runtime-context snapshot is a user/message with source
// prompt-context after the claimed messages; an unchanged snapshot is not
// repeated, a changed one is appended again.
func TestRuntimeContextIsASnapshotWhenItChanges(t *testing.T) {
	p := newTestPrompt(defaultPrompt)
	p.ctxs = []prompt.Snapshot{{Name: "clock", Owner: "driver-test", Text: "t1"}}
	var r *rig
	hooks := driver.Hooks{ContinueOrStop: func(_, step int) {
		if step == 2 {
			r.prompt.set(func(p *testPrompt) { p.ctxs = []prompt.Snapshot{{Name: "clock", Owner: "driver-test", Text: "t2"}} })
		}
	}}
	r = newRig(t, rigOptions{prompt: p, hooks: hooks, turns: []fakemodel.Turn{fakemodel.Calls(echo("c1")), fakemodel.Calls(echo("c2")), fakemodel.Reply("done")}})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	if got := strings.Join(between(evs, 1), ","); got != "system/message,user/message,user/message,request/header" {
		t.Fatalf("step 1 admitted %s", got)
	}
	var snaps []string
	for _, e := range ofType(evs, "user/message") {
		m := decode[event.UserMessagePayload](t, e).Message
		if src, ok := m.Source(); ok && src.Kind == driver.SourcePromptContext {
			snaps = append(snaps, m.Blocks()[0].Text.Text)
		}
	}
	if !slices.Equal(snaps, []string{"t1", "t2"}) {
		t.Fatalf("snapshots %v, want t1 once and t2 after the change", snaps)
	}
	checkClosed(t, r.sess)
}

// DH-T-TOOLS-14 through the driver: with the tools.prompt component's service as
// the prompter, the first request leads with prompt.IdentityLine, and the
// header carries the assembly's fingerprint, its manifest with each section's
// owner, and the tools in toolOrder.
//
//dsh:test DH-T-TOOLS-14
func TestPromptServiceReachesTheHeader(t *testing.T) {
	root, svc := setup(t)
	for _, n := range []string{"alpha", "zeta"} {
		must(svc.Register(root, tool(n)))
	}
	cfg := prompt.Defaults()
	cfg.ToolOrder = []string{"zeta", prompt.Unlisted}
	cfg.PersonaSuffix = "Be brief."
	must(0, must(root.Use(prompt.Component, cfg)).Wait())
	var ps *prompt.Service
	must(0, must(root.Inject([]string{prompt.Key.Name()}, func(c *cordis.Context) (err error) {
		ps, err = prompt.Key.Get(c)
		return err
	})).Wait())
	r := newRig(t, rigOptions{root: root, svc: svc, turns: []fakemodel.Turn{fakemodel.Reply("done")},
		options: func(o *driver.Options) { o.Prompt = ps }})
	r.send(agent.ListNextTurn, "go")
	r.idle()
	evs := r.events()
	msgs := must(r.sent.requests()[0].Messages())
	if msgs[0].Role() != vocab.RoleSystem || !strings.HasPrefix(msgs[0].Blocks()[0].Text.Text, prompt.IdentityLine) ||
		!strings.HasSuffix(msgs[0].Blocks()[0].Text.Text, "Be brief.") {
		t.Fatalf("the first request leads with %+v", msgs[0])
	}
	hp := headers(t, evs)[0]
	if got := toolNames(hp.Tools); !slices.Equal(got, []string{"zeta", "alpha"}) {
		t.Fatalf("header tools %v, want toolOrder's", got)
	}
	st := must(driver.ResolveStep(bg, svc, stubAgent{ref: agent.Ref{Session: r.sess.ID(), Incarnation: 1}}, agent.ModelSelection{Route: route}))
	defer st.Release()
	want := must(ps.Assemble(bg, prompt.Input{View: st.View()}))
	if hp.Fingerprint != want.Fingerprint {
		t.Fatalf("header fingerprint %s, want the assembly's %s", hp.Fingerprint, want.Fingerprint)
	}
	var names []string
	for _, m := range hp.Manifest {
		if m.Owner == "" {
			t.Fatalf("manifest entry %+v names no owner", m)
		}
		names = append(names, m.Name)
	}
	if !slices.Equal(names, []string{prompt.SectionIdentity, prompt.SectionPersonaSuffix}) {
		t.Fatalf("manifest %v", hp.Manifest)
	}
	if got := toolNames(st.Schemas()); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("the step tool set (the admission list) is %v, by name", got)
	}
	checkClosed(t, r.sess)
}

// M1-D-LOOP-1 A, scenario "Crash between claim and admit" (SQLite): a claim made
// durable, then a store that fails the admission; reopened, the turn is closed
// interrupted, the message is reported discarded, and it never runs: a later
// turn runs only its own input.
func TestCrashBetweenClaimAndAdmit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	st := must(sqlite.Open(bg, path))
	t.Cleanup(func() { _ = st.Close(bg) })
	f := &faulty{}
	f.set(once(func(ds []event.Draft) bool { return has(ds, "user/message") }, func(context.Context) error { return errors.New("disk gone") }), nil)
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	r := newRig(t, rigOptions{root: root, svc: svc, store: st, wrap: f.wrap, failOK: true})
	r.skipReplay = true
	r.send(agent.ListNextTurn, "go")
	within(t, r.drv.Done(), "the driver's stop on the failed admission")
	if err := r.drv.Err(); err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("driver error %v", err)
	}
	if n := len(r.sent.requests()); n != 0 {
		t.Fatalf("%d requests sent without an admission", n)
	}
	must(0, r.sess.Close(bg))

	svc2 := session.NewService(root, st, session.Config{})
	h, rep, err := svc2.Open(bg, nil, r.sess.ID(), session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(bg) })
	if !rep.Interrupted {
		t.Fatal("the reopen did not close the open turn")
	}
	evs := must(h.Read(bg, 0, 0))
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonInterrupted {
		t.Fatalf("turn/end %+v, want interrupted", got)
	}
	in := must(agent.OpenInbox(bg, h))
	d := in.Discarded()
	if len(d) != 1 || d[0].Message.Text != "go" || d[0].Turn != 1 || d[0].Cause != agent.CauseUnadmitted {
		t.Fatalf("discarded %+v, want the claimed message", d)
	}
	if p := in.Pending(agent.ListNextTurn); len(p) != 0 {
		t.Fatalf("pending after the reopen %+v", p)
	}

	// The resumed agent runs new input only.
	rt := llm.New()
	tr := fakemodel.NewTransport(t, fakemodel.Reply("ok"))
	fakemodel.Register(t, rt, route, tr)
	drv := must(driver.Start(driver.Options{
		Context: root, Agent: stubAgent{ref: agent.Ref{Session: h.ID(), Incarnation: 2}}, Session: h, Inbox: in,
		Model:  must(agent.OpenModel(bg, h, agent.ModelOptions{Initial: agent.ModelSelection{Route: route}})),
		Models: rt, Tools: svc, Prompt: newTestPrompt(defaultPrompt),
	}))
	t.Cleanup(func() { _ = drv.Close(bg) })
	must(in.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "next"}))
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	must(0, drv.WaitIdle(ctx))
	var users []string
	for _, e := range ofType(must(h.Read(bg, 0, 0)), "user/message") {
		users = append(users, decode[event.UserMessagePayload](t, e).Message.Blocks()[0].Text.Text)
	}
	if !slices.Equal(users, []string{"next"}) {
		t.Fatalf("user messages %v: the discarded item ran", users)
	}
}
