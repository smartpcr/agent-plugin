package sdkserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// told is an initialized connection attached to its peer: it reads every frame
// in order, keeping the notifications while it waits for a response.
type told struct {
	*client
	notes []*jsonrpc.Notification
}

func connectTold(t *testing.T, srv *sdkserver.Server) *told {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	conn, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	if err := c.Attach(conn); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	tl := &told{client: &client{t: t, peer: peer}}
	var init protocol.InitializeResult
	tl.call("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"}}, &init)
	return tl
}

func (c *told) call(method string, params, result any) {
	c.t.Helper()
	id := c.id()
	c.peer.Request(id, method, params)
	for {
		switch f := c.peer.Expect().(type) {
		case *jsonrpc.Notification:
			c.notes = append(c.notes, f)
		case *jsonrpc.Response:
			if f.ID != id {
				c.t.Fatalf("%s: got the response to %s", method, f.ID)
			}
			if f.Error != nil {
				c.t.Fatalf("%s: %v", method, f.Error)
			}
			if err := json.Unmarshal(f.Result, result); err != nil {
				c.t.Fatalf("%s: decode %s: %v", method, f.Result, err)
			}
			return
		default:
			c.t.Fatalf("%s: unexpected frame %T", method, f)
		}
	}
}

func (c *told) create() (string, int64) {
	c.t.Helper()
	var r protocol.SessionCreateResult
	c.call("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}, &r)
	return r.SessionID, r.LastSeq
}

// next reads frames until a notification of method arrives and returns it.
func (c *told) await(method string) *jsonrpc.Notification {
	c.t.Helper()
	for {
		switch f := c.peer.Expect().(type) {
		case *jsonrpc.Notification:
			c.notes = append(c.notes, f)
			if f.Method == method {
				return f
			}
		default:
			c.t.Fatalf("waiting for %s: unexpected frame %T", method, f)
		}
	}
}

func decode[T any](t *testing.T, n *jsonrpc.Notification) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(n.Params, &v); err != nil {
		t.Fatalf("decode %s %s: %v", n.Method, n.Params, err)
	}
	return v
}

// TC-12a: two sessions on one connection run; each session.event names its own
// session as its root, and per session the seqs follow on from create's lastSeq
// with no gap, end at wait's lastSeq and carry that session's own log.
func TestScopedOrderedEvents(t *testing.T) {
	cl := connectTold(t, newServer(t, fakemodel.Reply("one"), fakemodel.Reply("two")))
	a, aFrom := cl.create()
	b, bFrom := cl.create()
	var r protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: a, Content: text("to a"), MessageID: "ma"}, &r)
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: b, Content: text("to b"), MessageID: "mb"}, &r)
	var wa, wb protocol.LastSeqResult
	cl.call("session/wait", protocol.SessionRef{SessionID: a}, &wa)
	cl.call("session/wait", protocol.SessionRef{SessionID: b}, &wb)

	from := map[string]int64{a: aFrom, b: bFrom}
	got := map[string][]protocol.Event{}
	runs := map[string][]protocol.RunCompletedNotification{}
	for _, n := range cl.notes {
		switch n.Method {
		case sdkserver.EventNotification:
			e := decode[protocol.SessionEventNotification](t, n)
			if _, ok := from[e.SessionID]; !ok || e.RootSessionID != e.SessionID || e.ParentSessionID != "" {
				t.Fatalf("session.event scope = %+v, want one of %s, %s as its own root", e.Scope, a, b)
			}
			got[e.SessionID] = append(got[e.SessionID], e.Event)
		case sdkserver.RunCompletedNotification:
			rc := decode[protocol.RunCompletedNotification](t, n)
			runs[rc.SessionID] = append(runs[rc.SessionID], rc)
		}
	}
	for id, last := range map[string]int64{a: wa.LastSeq, b: wb.LastSeq} {
		evs := got[id]
		if len(evs) == 0 || evs[0].Seq != from[id]+1 || evs[len(evs)-1].Seq != last {
			t.Fatalf("session %s: events %d..%d, want %d..%d", id, first(evs), lastOf(evs), from[id]+1, last)
		}
		for i := 1; i < len(evs); i++ {
			if evs[i].Seq != evs[i-1].Seq+1 {
				t.Fatalf("session %s: seq %d follows %d", id, evs[i].Seq, evs[i-1].Seq)
			}
		}
		var page protocol.SessionHistoryResult
		cl.call("session/history", protocol.SessionHistoryParams{SessionID: id, AfterSeq: from[id], Limit: sdkserver.MaxHistoryLimit}, &page)
		if len(page.Events) != len(evs) {
			t.Fatalf("session %s: %d events told, history has %d", id, len(evs), len(page.Events))
		}
		for i, e := range page.Events {
			if e.Seq != evs[i].Seq || e.Type != evs[i].Type || string(e.Data) != string(evs[i].Data) {
				t.Fatalf("session %s: told %d %s, history has %d %s", id, evs[i].Seq, evs[i].Type, e.Seq, e.Type)
			}
		}
		if len(runs[id]) != 1 {
			t.Fatalf("session %s: runs %+v, want one", id, runs[id])
		}
	}
	if runs[a][0].Run.MessageID != "ma" || runs[b][0].Run.MessageID != "mb" {
		t.Fatalf("runs = %+v / %+v, want ma on %s and mb on %s", runs[a], runs[b], a, b)
	}
}

func first(evs []protocol.Event) int64 {
	if len(evs) == 0 {
		return -1
	}
	return evs[0].Seq
}

func lastOf(evs []protocol.Event) int64 {
	if len(evs) == 0 {
		return -1
	}
	return evs[len(evs)-1].Seq
}

// TC-12a: a run whose model replies A, calls a tool, then replies B completes
// with B, the last assistant message inside the turn, written after the
// turn/end that ends its bracket.
func TestRunResultIsLastAssistantText(t *testing.T) {
	cl := connectTold(t, newServer(t,
		fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("A"), fakemodel.Tool("c1", "missing", ""),
			fakemodel.Usage(vocab.Usage{Input: 1, Output: 1}), fakemodel.Finish(vocab.FinishToolCalls)}},
		fakemodel.Reply("B")))
	id, _ := cl.create()
	var r protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go"), MessageID: "m1"}, &r)

	rc := decode[protocol.RunCompletedNotification](t, cl.await(sdkserver.RunCompletedNotification))
	if rc.SessionID != id || rc.RootSessionID != id || rc.Run.Kind != "prompt" || rc.Run.MessageID != "m1" || rc.Turn <= 0 {
		t.Fatalf("run.completed = %+v, want prompt m1 of %s with its turn", rc, id)
	}
	if rc.FinalResponse != "B" {
		t.Fatalf("finalResponse = %q, want B", rc.FinalResponse)
	}
	var asst []string
	var end protocol.Event
	for _, n := range cl.notes {
		if n.Method != sdkserver.EventNotification {
			continue
		}
		e := decode[protocol.SessionEventNotification](t, n).Event
		if e.Seq < rc.FirstSeq || e.Seq > rc.LastSeq {
			continue
		}
		switch e.Type {
		case "assistant/message":
			asst = append(asst, string(e.Data))
		case "turn/end":
			end = e
		}
	}
	if len(asst) != 2 || !strings.Contains(asst[0], `"A"`) || !strings.Contains(asst[1], `"B"`) {
		t.Fatalf("assistant messages in the bracket = %v, want A then B", asst)
	}
	if end.Seq != rc.LastSeq || !strings.Contains(string(end.Data), rc.FinishReason) {
		t.Fatalf("turn/end before the result = %+v, want seq %d with reason %q", end, rc.LastSeq, rc.FinishReason)
	}
}

// Attach refuses a nil notifier, a second attach and a closing connection; a
// session owned before the attach is forwarded from its end then.
func TestAttachFollowsOwnedSessionsOnce(t *testing.T) {
	srv := newServer(t, fakemodel.Reply("late"))
	c := must(srv.Connect())
	conn, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	tl := &told{client: &client{t: t, peer: peer}}
	var init protocol.InitializeResult
	tl.call("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"}}, &init)
	id, from := tl.create()
	if err := c.Attach(nil); err == nil {
		t.Fatal("Attach(nil) succeeded")
	}
	if err := c.Attach(conn); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.Attach(conn); err == nil {
		t.Fatal("a second Attach succeeded")
	}
	var r protocol.SessionPromptResult
	tl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("hi"), MessageID: "m1"}, &r)
	rc := decode[protocol.RunCompletedNotification](t, tl.await(sdkserver.RunCompletedNotification))
	if rc.FinalResponse != "late" || rc.FirstSeq <= from {
		t.Fatalf("run.completed = %+v, want late after seq %d", rc, from)
	}

	closed := must(srv.Connect())
	if err := closed.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := closed.Attach(conn); err == nil {
		t.Fatal("Attach after Close succeeded")
	}
}

// A resumed session folds its history into the run tracker but tells nothing of
// it: the new connection sees only the runs it prompted, from after resume's
// lastSeq; close sends the events it wrote before its response.
func TestResumeReportsOnlyNewRuns(t *testing.T) {
	srv := newServer(t, fakemodel.Reply("one"), fakemodel.Reply("two"))
	first := connectTold(t, srv)
	id, _ := first.create()
	var r protocol.SessionPromptResult
	first.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("a"), MessageID: "m1"}, &r)
	var w protocol.LastSeqResult
	first.call("session/wait", protocol.SessionRef{SessionID: id}, &w)
	var closed protocol.LastSeqResult
	first.call("session/close", protocol.SessionRef{SessionID: id}, &closed)
	if seqs := toldSeqs(t, first, id); seqs[len(seqs)-1] != closed.LastSeq {
		t.Fatalf("close told up to %d, closed at %d", seqs[len(seqs)-1], closed.LastSeq)
	}

	second := connectTold(t, srv)
	var resumed protocol.SessionResumeResult
	second.call("session/resume", protocol.SessionResumeParams{SessionID: id}, &resumed)
	second.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("b"), MessageID: "m2"}, &r)
	rc := decode[protocol.RunCompletedNotification](t, second.await(sdkserver.RunCompletedNotification))
	if rc.Run.MessageID != "m2" || rc.FinalResponse != "two" || rc.FirstSeq <= resumed.LastSeq {
		t.Fatalf("run.completed = %+v, want m2 → two after seq %d", rc, resumed.LastSeq)
	}
	if seqs := toldSeqs(t, second, id); seqs[0] != resumed.LastSeq+1 {
		t.Fatalf("resumed connection told from %d, want %d", seqs[0], resumed.LastSeq+1)
	}
}

// A queued prompt discarded by cancel completes at its splice as discarded; the
// cancelled turn's prompt completes at its turn/end.
func TestDiscardedPromptCompletes(t *testing.T) {
	r := newRig(t, hang("working"))
	cl := connectTold(t, r.server())
	id, _ := cl.create()
	var pr protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("a"), MessageID: "m0"}, &pr)
	running(t, r, id)
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("b"), MessageID: "m1"}, &pr)
	var cancelled protocol.SessionCancelResult
	cl.call("session/cancel", protocol.SessionCancelParams{SessionID: id, Scope: "turn", Inbox: "discard"}, &cancelled)
	got := map[string]protocol.RunCompletedNotification{}
	for _, n := range cl.notes {
		if n.Method == sdkserver.RunCompletedNotification {
			rc := decode[protocol.RunCompletedNotification](t, n)
			got[rc.Run.MessageID] = rc
		}
	}
	for len(got) < 2 {
		rc := decode[protocol.RunCompletedNotification](t, cl.await(sdkserver.RunCompletedNotification))
		got[rc.Run.MessageID] = rc
	}
	if d := got["m1"]; d.FinishReason != sdkserver.FinishDiscarded || d.FirstSeq != d.LastSeq || d.Turn != 0 {
		t.Fatalf("discarded run = %+v", d)
	}
	if c := got["m0"]; c.Turn <= 0 || c.FinishReason == sdkserver.FinishDiscarded || c.FinishReason == "" {
		t.Fatalf("cancelled run = %+v", c)
	}
}

// toldSeqs is the seqs of the session.events c was told for id, in order.
func toldSeqs(t *testing.T, c *told, id string) []int64 {
	t.Helper()
	var seqs []int64
	for _, n := range c.notes {
		if n.Method == sdkserver.EventNotification {
			if e := decode[protocol.SessionEventNotification](t, n); e.SessionID == id {
				seqs = append(seqs, e.Event.Seq)
			}
		}
	}
	if len(seqs) == 0 {
		t.Fatalf("no session.event told for %s", id)
	}
	return seqs
}
