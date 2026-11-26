//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"

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

const rrsnRoute vocab.RouteID = "r/rrsn"

// rrsnT is the suite's *testing.T: the fake model, the peer and the cleanups of
// the server need a testing.TB.
var rrsnT *testing.T

// rrsnPrompt assembles a fixed system prompt and the view's tools.
type rrsnPrompt struct{}

func (rrsnPrompt) Assemble(_ context.Context, in prompt.Input) (prompt.Assembly, error) {
	a := prompt.Assembly{Sections: []prompt.RenderedSection{{Name: "e2e", Order: prompt.OrderHarness, Owner: "run-results-e2e", Text: "You are the e2e agent."}}}
	for _, d := range in.View.Tools() {
		a.Tools = append(a.Tools, d.Schema())
	}
	fp, err := vocab.Digest(a.Tools)
	a.Fingerprint = fp
	return a, err
}

func (rrsnPrompt) Render(a prompt.Assembly) (string, error) { return a.Sections[0].Text, nil }

type rrsnState struct {
	peer  *jsonrpctest.Peer
	next  int64
	notes []*jsonrpc.Notification // every notification read, in arrival order

	sessions []string
	from     map[string]int64 // create's lastSeq per session
	until    map[string]int64 // wait's lastSeq per session
	msgIDs   map[string]string

	run    protocol.RunCompletedNotification
	runIdx int // the index of run in notes
}

func rrsnWait(f *cordis.Fiber, err error) error {
	if err != nil {
		return err
	}
	return f.Wait()
}

// newServer builds a driven agent registry over a memstore and the fake model
// answering turns in order, and the SDK server over them, as cmd/dsh wires it.
func (s *rrsnState) newServer(turns ...fakemodel.Turn) (*sdkserver.Server, error) {
	t := rrsnT
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		if err := rrsnWait(root.Use(c, cordis.None{})); err != nil {
			return nil, fmt.Errorf("use %v: %w", c, err)
		}
	}
	var tsvc tools.Service
	if err := rrsnWait(root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		tsvc, err = tools.Key.Get(c)
		return err
	})); err != nil {
		return nil, err
	}
	svc := session.NewService(root, memstore.New(), session.Config{})
	reg := agent.NewRegistry(root)
	rt := llm.New()
	fakemodel.Register(t, rt, rrsnRoute, fakemodel.NewTransport(t, turns...))
	var drvCtx *cordis.Context
	if err := rrsnWait(root.Apply("driver", func(c *cordis.Context) error { drvCtx = c; return nil })); err != nil {
		return nil, err
	}
	f, err := driver.NewFactory(driver.FactoryOptions{
		Context: drvCtx, Sessions: svc, Scopes: scope.NewTable(root), Models: rt, Tools: tsvc, Prompt: rrsnPrompt{},
	})
	if err != nil {
		return nil, err
	}
	if _, err := reg.SetFactory(root, f); err != nil {
		return nil, err
	}
	return sdkserver.New(sdkserver.Options{
		Owner: root, Agents: reg, Sessions: svc, Routes: rt,
		Info: protocol.Implementation{Name: "dsh", Version: "e2e"}, Limits: protocol.Limits{MaxFrameBytes: 1 << 20},
	})
}

// attach connects to a new server, attaches the connection's notifier to the
// transport (as Serve does) and initializes.
func (s *rrsnState) attach(turns ...fakemodel.Turn) error {
	srv, err := s.newServer(turns...)
	if err != nil {
		return err
	}
	c, err := srv.Connect()
	if err != nil {
		return err
	}
	rrsnT.Cleanup(func() { _ = c.Close(context.Background()) })
	conn, peer := jsonrpctest.NewPeer(rrsnT, jsonrpc.Options{Methods: c.Methods()})
	if err := c.Attach(conn); err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	s.peer = peer
	var init protocol.InitializeResult
	if err := s.call("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "e2e", Version: "0"}}, &init); err != nil {
		return err
	}
	if init.ProtocolVersion != protocol.Version || init.ServerInfo.Name != "dsh" {
		return fmt.Errorf("initialize = %+v", init)
	}
	return nil
}

// call sends one request and reads frames until its response, keeping every
// notification written before it.
func (s *rrsnState) call(method string, params, result any) error {
	s.next++
	id := jsonrpc.Int64ID(s.next)
	s.peer.Request(id, method, params)
	for {
		switch f := s.peer.Expect().(type) {
		case *jsonrpc.Notification:
			s.notes = append(s.notes, f)
		case *jsonrpc.Response:
			if f.ID != id {
				return fmt.Errorf("%s: got the response to %v", method, f.ID)
			}
			if f.Error != nil {
				return fmt.Errorf("%s: %v", method, f.Error)
			}
			if err := json.Unmarshal(f.Result, result); err != nil {
				return fmt.Errorf("%s: decode %s: %w", method, f.Result, err)
			}
			return nil
		default:
			return fmt.Errorf("%s: unexpected frame %T", method, f)
		}
	}
}

func rrsnDecode[T any](n *jsonrpc.Notification) (T, error) {
	var v T
	if err := json.Unmarshal(n.Params, &v); err != nil {
		return v, fmt.Errorf("decode %s %s: %w", n.Method, n.Params, err)
	}
	return v, nil
}

func (s *rrsnState) givenTwoReplies(a, b string) error {
	return s.attach(fakemodel.Reply(a), fakemodel.Reply(b))
}

// givenToolThenReply is a run of two model calls: text a with a tool call the
// turn cannot resolve, then the reply b.
func (s *rrsnState) givenToolThenReply(a, b string) error {
	return s.attach(
		fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text(a), fakemodel.Tool("c1", "missing", ""),
			fakemodel.Usage(vocab.Usage{Input: 1, Output: 1}), fakemodel.Finish(vocab.FinishToolCalls)}},
		fakemodel.Reply(b))
}

func (s *rrsnState) create(n int) error {
	s.from, s.until, s.msgIDs = map[string]int64{}, map[string]int64{}, map[string]string{}
	for range n {
		var r protocol.SessionCreateResult
		if err := s.call("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(rrsnRoute), Model: fakemodel.ModelName}}, &r); err != nil {
			return err
		}
		if r.SessionID == "" {
			return fmt.Errorf("session/create = %+v, want a session id", r)
		}
		if _, dup := s.from[r.SessionID]; dup {
			return fmt.Errorf("session/create returned %s twice", r.SessionID)
		}
		s.sessions = append(s.sessions, r.SessionID)
		s.from[r.SessionID] = r.LastSeq
	}
	return nil
}

func (s *rrsnState) prompt(id, msgID string) error {
	var r protocol.SessionPromptResult
	if err := s.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: []protocol.ContentBlock{{Type: "text", Text: "go " + msgID}}, MessageID: msgID}, &r); err != nil {
		return err
	}
	s.msgIDs[id] = msgID
	return nil
}

func (s *rrsnState) wait(id string) error {
	var w protocol.LastSeqResult
	if err := s.call("session/wait", protocol.SessionRef{SessionID: id}, &w); err != nil {
		return err
	}
	s.until[id] = w.LastSeq
	return nil
}

// promptBothAndWait starts both runs before waiting on either, so they run
// concurrently on the one connection.
func (s *rrsnState) promptBothAndWait() error {
	for i, id := range s.sessions {
		if err := s.prompt(id, fmt.Sprintf("m%d", i+1)); err != nil {
			return err
		}
	}
	for _, id := range s.sessions {
		if err := s.wait(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *rrsnState) promptOneAndWait(msgID string) error {
	if len(s.sessions) != 1 {
		return fmt.Errorf("want one session, have %d", len(s.sessions))
	}
	if err := s.prompt(s.sessions[0], msgID); err != nil {
		return err
	}
	return s.wait(s.sessions[0])
}

// told groups the session.event notifications by session, in arrival order.
func (s *rrsnState) told() (map[string][]protocol.SessionEventNotification, error) {
	out := map[string][]protocol.SessionEventNotification{}
	for _, n := range s.notes {
		if n.Method != sdkserver.EventNotification {
			continue
		}
		e, err := rrsnDecode[protocol.SessionEventNotification](n)
		if err != nil {
			return nil, err
		}
		out[e.SessionID] = append(out[e.SessionID], e)
	}
	return out, nil
}

func (s *rrsnState) eventsAreScoped() error {
	got, err := s.told()
	if err != nil {
		return err
	}
	if len(got) == 0 {
		return fmt.Errorf("no session.event was told")
	}
	for id, evs := range got {
		if _, ok := s.from[id]; !ok {
			return fmt.Errorf("session.event for %s, which this connection did not create (%v)", id, s.sessions)
		}
		for _, e := range evs {
			if e.RootSessionID != e.SessionID || e.ParentSessionID != "" {
				return fmt.Errorf("session.event scope = %+v, want %s as its own root with no parent", e.Scope, id)
			}
		}
	}
	return nil
}

func (s *rrsnState) seqsStrictlyIncrease() error {
	got, err := s.told()
	if err != nil {
		return err
	}
	for _, id := range s.sessions {
		evs := got[id]
		if len(evs) == 0 {
			return fmt.Errorf("session %s: no session.event told", id)
		}
		if f := evs[0].Event.Seq; f != s.from[id]+1 {
			return fmt.Errorf("session %s: first told seq %d, want %d (create's lastSeq + 1)", id, f, s.from[id]+1)
		}
		if l := evs[len(evs)-1].Event.Seq; l != s.until[id] {
			return fmt.Errorf("session %s: last told seq %d, want wait's lastSeq %d", id, l, s.until[id])
		}
		for i := 1; i < len(evs); i++ {
			if evs[i].Event.Seq != evs[i-1].Event.Seq+1 {
				return fmt.Errorf("session %s: seq %d follows %d", id, evs[i].Event.Seq, evs[i-1].Event.Seq)
			}
		}
	}
	return nil
}

// toldIsOwnHistory proves no cross-session leakage: each session's told events
// are, one for one, the events its own log holds.
func (s *rrsnState) toldIsOwnHistory() error {
	got, err := s.told()
	if err != nil {
		return err
	}
	for _, id := range s.sessions {
		var page protocol.SessionHistoryResult
		if err := s.call("session/history", protocol.SessionHistoryParams{SessionID: id, AfterSeq: s.from[id], Limit: sdkserver.MaxHistoryLimit}, &page); err != nil {
			return err
		}
		evs := got[id]
		if len(page.Events) != len(evs) {
			return fmt.Errorf("session %s: %d events told, history has %d", id, len(evs), len(page.Events))
		}
		for i, h := range page.Events {
			e := evs[i].Event
			if h.Seq != e.Seq || h.Type != e.Type || string(h.Data) != string(e.Data) {
				return fmt.Errorf("session %s: told %d %s, history has %d %s", id, e.Seq, e.Type, h.Seq, h.Type)
			}
		}
	}
	return nil
}

func (s *rrsnState) runs() ([]protocol.RunCompletedNotification, []int, error) {
	var out []protocol.RunCompletedNotification
	var idx []int
	for i, n := range s.notes {
		if n.Method != sdkserver.RunCompletedNotification {
			continue
		}
		rc, err := rrsnDecode[protocol.RunCompletedNotification](n)
		if err != nil {
			return nil, nil, err
		}
		out, idx = append(out, rc), append(idx, i)
	}
	return out, idx, nil
}

func (s *rrsnState) oneRunEach() error {
	rs, _, err := s.runs()
	if err != nil {
		return err
	}
	per := map[string][]protocol.RunCompletedNotification{}
	for _, r := range rs {
		per[r.SessionID] = append(per[r.SessionID], r)
	}
	for _, id := range s.sessions {
		got := per[id]
		if len(got) != 1 {
			return fmt.Errorf("session %s: runs %+v, want one", id, got)
		}
		if got[0].RootSessionID != id || got[0].Run.MessageID != s.msgIDs[id] {
			return fmt.Errorf("session %s: run %+v, want message %s rooted at %s", id, got[0], s.msgIDs[id], id)
		}
	}
	if len(per) != len(s.sessions) {
		return fmt.Errorf("runs for %d sessions, want %d", len(per), len(s.sessions))
	}
	return nil
}

func (s *rrsnState) runArrives(msgID string) error {
	rs, idx, err := s.runs()
	if err != nil {
		return err
	}
	if len(rs) != 1 {
		return fmt.Errorf("session.run.completed = %+v, want one", rs)
	}
	id, r := s.sessions[0], rs[0]
	if r.SessionID != id || r.RootSessionID != id || r.Run.Kind != "prompt" || r.Run.MessageID != msgID || r.Turn <= 0 {
		return fmt.Errorf("run.completed = %+v, want prompt %s of %s with its turn", r, msgID, id)
	}
	s.run, s.runIdx = r, idx[0]
	return nil
}

func (s *rrsnState) resultIs(want string) error {
	if s.run.FinalResponse != want {
		return fmt.Errorf("finalResponse = %q, want %q", s.run.FinalResponse, want)
	}
	return nil
}

// bracketHolds checks the run's [firstSeq, lastSeq] bracket: its assistant
// messages are a then b, and its last event is the turn/end that completed it,
// told before the session.run.completed.
func (s *rrsnState) bracketHolds(a, b string) error {
	var asst []string
	var end *protocol.Event
	endIdx := -1
	for i, n := range s.notes {
		if n.Method != sdkserver.EventNotification {
			continue
		}
		en, err := rrsnDecode[protocol.SessionEventNotification](n)
		if err != nil {
			return err
		}
		e := en.Event
		if e.Seq < s.run.FirstSeq || e.Seq > s.run.LastSeq {
			continue
		}
		switch e.Type {
		case "assistant/message":
			asst = append(asst, string(e.Data))
		case "turn/end":
			end, endIdx = &e, i
		}
	}
	if len(asst) != 2 || !strings.Contains(asst[0], `"`+a+`"`) || !strings.Contains(asst[1], `"`+b+`"`) {
		return fmt.Errorf("assistant messages in the bracket = %v, want %s then %s", asst, a, b)
	}
	if end == nil || end.Seq != s.run.LastSeq || !strings.Contains(string(end.Data), s.run.FinishReason) {
		return fmt.Errorf("turn/end of the bracket = %+v, want seq %d with reason %q", end, s.run.LastSeq, s.run.FinishReason)
	}
	if endIdx > s.runIdx {
		return fmt.Errorf("session.run.completed was told before the turn/end that completes it")
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_run_results_and_scoped_notifications(ctx *godog.ScenarioContext) {
	var s *rrsnState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &rrsnState{}
		return c, nil
	})
	ctx.Step(`^an attached jsonrpctest peer initialized against an SDK server over a memstore and a fake model replying "([^"]*)" then "([^"]*)"$`, func(a, b string) error { return s.givenTwoReplies(a, b) })
	ctx.Step(`^an attached jsonrpctest peer initialized against an SDK server over a memstore and a fake model replying "([^"]*)" with a tool call then "([^"]*)"$`, func(a, b string) error { return s.givenToolThenReply(a, b) })
	ctx.Step(`^the client creates two sessions on the connection$`, func() error { return s.create(2) })
	ctx.Step(`^the client creates one session on the connection$`, func() error { return s.create(1) })
	ctx.Step(`^the client prompts both sessions and waits for each to settle$`, func() error { return s.promptBothAndWait() })
	ctx.Step(`^the client prompts the session with message id "([^"]*)" and waits for it to settle$`, func(id string) error { return s.promptOneAndWait(id) })
	ctx.Step(`^every session\.event names one of the two sessions as its own root$`, func() error { return s.eventsAreScoped() })
	ctx.Step(`^per session the session\.event seqs strictly increase from create's lastSeq to wait's lastSeq with no gap$`, func() error { return s.seqsStrictlyIncrease() })
	ctx.Step(`^per session the told events are exactly that session's own history$`, func() error { return s.toldIsOwnHistory() })
	ctx.Step(`^each session gets one session\.run\.completed for its own message id$`, func() error { return s.oneRunEach() })
	ctx.Step(`^a session\.run\.completed arrives for message id "([^"]*)" of that session$`, func(id string) error { return s.runArrives(id) })
	ctx.Step(`^the run's result text is "([^"]*)"$`, func(w string) error { return s.resultIs(w) })
	ctx.Step(`^the run's bracket holds the assistant messages "([^"]*)" then "([^"]*)" and ends with its turn/end$`, func(a, b string) error { return s.bracketHolds(a, b) })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_run_results_and_scoped_notifications(t *testing.T) {
	rrsnT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_run_results_and_scoped_notifications,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_run_results_and_scoped_notifications.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the run results and scoped notifications e2e scenarios failed")
	}
}
