package sdkserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

const route vocab.RouteID = "r/a"

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// stubPrompt assembles a fixed system prompt and the view's tools.
type stubPrompt struct{}

func (stubPrompt) Assemble(_ context.Context, in prompt.Input) (prompt.Assembly, error) {
	a := prompt.Assembly{Sections: []prompt.RenderedSection{{Name: "test", Order: prompt.OrderHarness, Owner: "sdkserver-test", Text: "You are the test agent."}}}
	for _, d := range in.View.Tools() {
		a.Tools = append(a.Tools, d.Schema())
	}
	fp, err := vocab.Digest(a.Tools)
	a.Fingerprint = fp
	return a, err
}

func (stubPrompt) Render(a prompt.Assembly) (string, error) { return a.Sections[0].Text, nil }

// newServer is a server over a driven registry, a memstore and the fake model
// replying turns in order.
func newServer(t *testing.T, turns ...fakemodel.Turn) *sdkserver.Server {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		must(0, must(root.Use(c, cordis.None{})).Wait())
	}
	var tsvc tools.Service
	must(0, must(root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		tsvc, err = tools.Key.Get(c)
		return err
	})).Wait())
	svc := session.NewService(root, memstore.New(), session.Config{})
	reg := agent.NewRegistry(root)
	rt := llm.New()
	fakemodel.Register(t, rt, route, fakemodel.NewTransport(t, turns...))
	var drvCtx *cordis.Context
	must(0, must(root.Apply("driver", func(c *cordis.Context) error { drvCtx = c; return nil })).Wait())
	f := must(driver.NewFactory(driver.FactoryOptions{
		Context: drvCtx, Sessions: svc, Scopes: scope.NewTable(root), Models: rt, Tools: tsvc, Prompt: stubPrompt{},
	}))
	must(reg.SetFactory(root, f))
	return must(sdkserver.New(sdkserver.Options{
		Owner: root, Agents: reg, Sessions: svc, Routes: rt,
		Info: protocol.Implementation{Name: "dsh", Version: "test"}, Limits: protocol.Limits{MaxFrameBytes: 1 << 20},
	}))
}

// client is one initialized connection to the server.
type client struct {
	t    *testing.T
	peer *jsonrpctest.Peer
	next int64
}

func connect(t *testing.T, srv *sdkserver.Server) *client {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	cl := &client{t: t, peer: peer}
	var init protocol.InitializeResult
	cl.call("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"}}, &init)
	if init.ProtocolVersion != protocol.Version || init.ServerInfo.Name != "dsh" {
		t.Fatalf("initialize = %+v", init)
	}
	return cl
}

func (c *client) id() jsonrpc.ID { c.next++; return jsonrpc.Int64ID(c.next) }

func (c *client) call(method string, params, result any) {
	c.t.Helper()
	id := c.id()
	c.peer.Request(id, method, params)
	r := c.peer.ExpectResponse(id)
	if r.Error != nil {
		c.t.Fatalf("%s: %v", method, r.Error)
	}
	if err := json.Unmarshal(r.Result, result); err != nil {
		c.t.Fatalf("%s: decode %s: %v", method, r.Result, err)
	}
}

func (c *client) refused(method string, params any, reason string) {
	c.t.Helper()
	id := c.id()
	c.peer.Request(id, method, params)
	c.peer.ExpectError(id, reason)
}

func (c *client) create() string {
	c.t.Helper()
	var r protocol.SessionCreateResult
	c.call("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}, &r)
	if r.SessionID == "" {
		c.t.Fatalf("session/create = %+v", r)
	}
	return r.SessionID
}

func text(s string) []protocol.ContentBlock { return []protocol.ContentBlock{{Type: "text", Text: s}} }

// TC-12a: create, prompt and history.
func TestCreatePromptHistory(t *testing.T) {
	cl := connect(t, newServer(t, fakemodel.Reply("hi there")))
	id := cl.create()

	var r protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("hello"), MessageID: "m1"}, &r)
	if r.MessageID != "m1" {
		t.Fatalf("receipt = %+v", r)
	}
	var again protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("hello"), MessageID: "m1"}, &again)
	if again != r {
		t.Fatalf("resent receipt = %+v, want the first %+v", again, r)
	}
	var w protocol.LastSeqResult
	cl.call("session/wait", protocol.SessionRef{SessionID: id}, &w)

	var page protocol.SessionHistoryResult
	cl.call("session/history", protocol.SessionHistoryParams{SessionID: id, Types: []string{"user/message", "assistant/message"}}, &page)
	if len(page.Events) != 2 || page.More || page.LastSeq != w.LastSeq {
		t.Fatalf("history = %+v, want two messages, lastSeq %d", page, w.LastSeq)
	}
	user, asst := page.Events[0], page.Events[1]
	if user.Type != "user/message" || !strings.Contains(string(user.Data), "hello") ||
		asst.Type != "assistant/message" || !strings.Contains(string(asst.Data), "hi there") || user.Seq >= asst.Seq {
		t.Fatalf("history = %s %s / %s %s, want the user then the assistant message", user.Type, user.Data, asst.Type, asst.Data)
	}

	var all protocol.SessionHistoryResult
	cl.call("session/history", protocol.SessionHistoryParams{SessionID: id, Limit: 2}, &all)
	if len(all.Events) != 2 || all.Events[0].Seq != 0 || !all.More {
		t.Fatalf("first page = %+v, want seq 0 and more", all)
	}
	var rest protocol.SessionHistoryResult
	cl.call("session/history", protocol.SessionHistoryParams{SessionID: id, AfterSeq: all.Events[1].Seq, Limit: sdkserver.MaxHistoryLimit}, &rest)
	if len(rest.Events) == 0 || rest.More || rest.Events[0].Seq != all.Events[1].Seq+1 || rest.Events[len(rest.Events)-1].Seq != rest.LastSeq {
		t.Fatalf("next page = %+v, want every later event once", rest)
	}
}

// A session owned by a live writer is session/owned to a second connection,
// which drives nothing it does not own; once closed it resumes.
func TestResumeOfALiveSessionIsOwned(t *testing.T) {
	srv := newServer(t)
	first, second := connect(t, srv), connect(t, srv)
	id := first.create()

	second.refused("session/resume", protocol.SessionResumeParams{SessionID: id}, "session/owned")
	second.refused("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("x")}, protocol.ReasonSessionNotOwned)
	first.refused("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: []protocol.ContentBlock{{Type: "image", Data: "AA==", MimeType: "image/png"}}}, protocol.ReasonInvalidContent)

	var again protocol.SessionResumeResult
	first.call("session/resume", protocol.SessionResumeParams{SessionID: id}, &again)
	if again.SessionID != id {
		t.Fatalf("idempotent resume = %+v", again)
	}
	var closed protocol.LastSeqResult
	first.call("session/close", protocol.SessionRef{SessionID: id}, &closed)
	var resumed protocol.SessionResumeResult
	second.call("session/resume", protocol.SessionResumeParams{SessionID: id}, &resumed)
	if resumed.SessionID != id || resumed.LastSeq < closed.LastSeq || resumed.ClosedInterruptedTurn {
		t.Fatalf("resume after close = %+v, closed at %d", resumed, closed.LastSeq)
	}
	first.refused("session/wait", protocol.SessionRef{SessionID: id}, protocol.ReasonSessionNotOwned)
}
