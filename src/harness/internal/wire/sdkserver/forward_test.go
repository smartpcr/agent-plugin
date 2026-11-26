package sdkserver_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
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
	notes   []*jsonrpc.Notification
	awaited map[string]int // per method, the notes await has scanned
}

func connectTold(t *testing.T, srv *sdkserver.Server) *told {
	return connectWrapped(t, srv, nil, nil)
}

// connectWrapped is connectTold with the notifier wrapped by wrap and the
// methods changed by hook, either nil for none.
func connectWrapped(t *testing.T, srv *sdkserver.Server, wrap func(sdkserver.Notifier) sdkserver.Notifier, hook func(map[string]jsonrpc.Handler)) *told {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	methods := c.Methods()
	if hook != nil {
		hook(methods)
	}
	conn, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: methods})
	var n sdkserver.Notifier = conn
	if wrap != nil {
		n = wrap(n)
	}
	if err := c.Attach(n); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	tl := &told{client: &client{t: t, peer: peer}, awaited: map[string]int{}}
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

// await returns the next notification of method not yet awaited: first from
// those buffered while a call waited for its response, then from new frames.
// Each notification is returned at most once.
func (c *told) await(method string) *jsonrpc.Notification {
	c.t.Helper()
	for {
		i := c.awaited[method]
		for ; i < len(c.notes); i++ {
			if n := c.notes[i]; n.Method == method {
				c.awaited[method] = i + 1
				return n
			}
		}
		c.awaited[method] = i
		switch f := c.peer.Expect().(type) {
		case *jsonrpc.Notification:
			c.notes = append(c.notes, f)
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

// subagent creates a live subagent session whose creator is creator, closed
// with the test.
func subagent(t *testing.T, r *rig, creator string) session.Handle {
	t.Helper()
	h := must(r.svc.Create(t.Context(), nil, session.CreateOptions{Header: session.Header{Origin: "subagent", Creator: creator}}))
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func appendRecords(t *testing.T, h session.Handle, n int) {
	t.Helper()
	for i := range n {
		if _, err := h.AppendPluginRecord(t.Context(), "plugin:test/rec", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))); err != nil {
			t.Fatalf("append to %s: %v", h.ID(), err)
		}
	}
}

// TC-12a: a subagent of an owned root, and its own subagent, are forwarded from
// their first event, in order with no gap, each naming itself, the root and its
// direct creator; neither gets a session.run.completed. Closing the root stops
// them: what they commit afterwards is not told.
func TestSubagentEventsCarryRootAndParent(t *testing.T) {
	r := newRig(t)
	cl := connectTold(t, r.server())
	root, _ := cl.create()
	child := subagent(t, r, root)
	want := map[string]protocol.Scope{
		string(child.ID()): {SessionID: string(child.ID()), RootSessionID: root, ParentSessionID: root},
	}
	got := map[string][]int64{}
	// collect reads session.events until each of last's sessions has been told
	// its seq there, checking every subagent's scope.
	collect := func(last map[string]int64) {
		t.Helper()
		for done := 0; done < len(last); {
			e := decode[protocol.SessionEventNotification](t, cl.await(sdkserver.EventNotification))
			w, ok := want[e.SessionID]
			if !ok {
				continue // the root's own events
			}
			if e.Scope != w {
				t.Fatalf("session.event scope = %+v, want %+v", e.Scope, w)
			}
			got[e.SessionID] = append(got[e.SessionID], e.Event.Seq)
			if l, ok := last[e.SessionID]; ok && e.Event.Seq == l {
				done++
			}
		}
	}
	appendRecords(t, child, 3)
	// The grandchild is forwarded only once its creator is: wait for that.
	collect(map[string]int64{string(child.ID()): int64(child.Len() - 1)})
	grand := subagent(t, r, string(child.ID()))
	want[string(grand.ID())] = protocol.Scope{SessionID: string(grand.ID()), RootSessionID: root, ParentSessionID: string(child.ID())}
	appendRecords(t, grand, 2)
	appendRecords(t, child, 2)
	last := map[string]int64{string(child.ID()): int64(child.Len() - 1), string(grand.ID()): int64(grand.Len() - 1)}
	collect(last)
	for id, seqs := range got {
		for i, s := range seqs {
			if s != int64(i) {
				t.Fatalf("subagent %s told seqs %v, want 0..%d", id, seqs, last[id])
			}
		}
	}
	for _, n := range cl.notes {
		if n.Method == sdkserver.RunCompletedNotification {
			if rc := decode[protocol.RunCompletedNotification](t, n); want[rc.SessionID] != (protocol.Scope{}) {
				t.Fatalf("session.run.completed told for subagent %s: %s", rc.SessionID, n.Params)
			}
		}
	}

	var closed protocol.LastSeqResult
	cl.call("session/close", protocol.SessionRef{SessionID: root}, &closed)
	committed := make(chan struct{}, 1)
	afterClose := child.Len()
	must(session.Committed.On(r.o.Owner, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Session.ID() == child.ID() && a.Event.Seq() == afterClose {
			committed <- struct{}{}
		}
		return nil
	}))
	appendRecords(t, child, 1)
	select {
	case <-committed:
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("the subagent's commit after the root's close was never delivered")
	}
	time.Sleep(100 * time.Millisecond) // room for a forwarder that should not exist
	seen := len(cl.notes)
	cl.create() // a round trip: anything sent before its reply is in cl.notes
	for _, n := range cl.notes[seen:] {
		if n.Method == sdkserver.EventNotification {
			if e := decode[protocol.SessionEventNotification](t, n); want[e.SessionID] != (protocol.Scope{}) {
				t.Fatalf("subagent %s told seq %d after its root closed", e.SessionID, e.Event.Seq)
			}
		}
	}
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
	// wait answers after the run's completion is written, so the completion is
	// among the notifications buffered before wait's response.
	var w protocol.LastSeqResult
	cl.call("session/wait", protocol.SessionRef{SessionID: id}, &w)

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
	tl := &told{client: &client{t: t, peer: peer}, awaited: map[string]int{}}
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

// reply sends one request and returns its response, error or not, keeping the
// notifications that come before it.
func (c *told) reply(method string, params any) *jsonrpc.Response {
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
			return f
		default:
			c.t.Fatalf("%s: unexpected frame %T", method, f)
		}
	}
}

// failing fails every session.event once fail is set, as an encoder refusing
// an oversized frame does; everything else passes through.
type failing struct {
	sdkserver.Notifier
	fail atomic.Bool
}

func (f *failing) Notify(method string, params any) error {
	if method == sdkserver.EventNotification && f.fail.Load() {
		return errors.New("encode: frame too large")
	}
	return f.Notifier.Notify(method, params)
}

func refusedInternal(t *testing.T, method string, r *jsonrpc.Response) {
	t.Helper()
	if r.Error == nil || r.Error.Reason != jsonrpc.ReasonInternal || !strings.Contains(r.Error.Message, "frame too large") {
		t.Fatalf("%s = %+v, want %s naming the notify failure", method, r, jsonrpc.ReasonInternal)
	}
}

// A session.event the notifier fails to send fails the prompt that wrote it and
// every later watermark, rather than answering a seq the client never got.
func TestForwardFailureFailsWatermarks(t *testing.T) {
	var fn *failing
	cl := connectWrapped(t, newServer(t, fakemodel.Reply("lost")), func(n sdkserver.Notifier) sdkserver.Notifier {
		fn = &failing{Notifier: n}
		return fn
	}, nil)
	id, _ := cl.create()
	fn.fail.Store(true)
	refusedInternal(t, "session/prompt", cl.reply("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go"), MessageID: "m1"}))
	refusedInternal(t, "session/wait", cl.reply("session/wait", protocol.SessionRef{SessionID: id}))
	for _, n := range cl.notes {
		if n.Method == sdkserver.RunCompletedNotification {
			t.Fatalf("run.completed told after the forwarder failed: %s", n.Params)
		}
	}
}

// A close whose tail cannot be sent fails, yet the session is closed: another
// connection resumes it past the events the close wrote.
func TestCloseTailFailureFailsClose(t *testing.T) {
	var fn *failing
	r := newRig(t, hang("working"))
	srv := r.server()
	cl := connectWrapped(t, srv, func(n sdkserver.Notifier) sdkserver.Notifier {
		fn = &failing{Notifier: n}
		return fn
	}, nil)
	id, _ := cl.create()
	var pr protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go"), MessageID: "m1"}, &pr)
	running(t, r, id)
	fn.fail.Store(true)
	refusedInternal(t, "session/close", cl.reply("session/close", protocol.SessionRef{SessionID: id}))

	other := connectTold(t, srv)
	var resumed protocol.SessionResumeResult
	other.call("session/resume", protocol.SessionResumeParams{SessionID: id}, &resumed)
	if resumed.LastSeq <= pr.Seq {
		t.Fatalf("resumed at %d, want past the prompt's %d", resumed.LastSeq, pr.Seq)
	}
}

// heldEvents, once armed, holds every session.event until open is closed,
// signalling entered on the first.
type heldEvents struct {
	sdkserver.Notifier
	armed   atomic.Bool
	once    sync.Once
	entered chan struct{}
	open    chan struct{}
}

func (g *heldEvents) Notify(method string, params any) error {
	if method == sdkserver.EventNotification && g.armed.Load() {
		g.once.Do(func() { close(g.entered) })
		<-g.open
	}
	return g.Notifier.Notify(method, params)
}

// A retried message id racing its first send answers the first receipt only
// after that receipt's session.event is sent, like the first send does.
func TestDuplicatePromptWaitsForItsEvent(t *testing.T) {
	g := &heldEvents{entered: make(chan struct{}), open: make(chan struct{})}
	var opened atomic.Bool
	early := make(chan struct{}, 2)
	cl := connectWrapped(t, newServer(t, fakemodel.Reply("once")), func(n sdkserver.Notifier) sdkserver.Notifier {
		g.Notifier = n
		return g
	}, func(m map[string]jsonrpc.Handler) {
		inner := m["session/prompt"]
		m["session/prompt"] = func(ctx context.Context, p json.RawMessage) (any, error) {
			res, err := inner(ctx, p)
			if !opened.Load() {
				early <- struct{}{}
			}
			return res, err
		}
	})
	var release sync.Once
	unblock := func() { release.Do(func() { close(g.open) }) }
	t.Cleanup(unblock) // before the connection's Close, which a held event would block
	id, _ := cl.create()
	g.armed.Store(true)
	params := protocol.SessionPromptParams{SessionID: id, Content: text("go"), MessageID: "m1"}
	a, b := cl.id(), cl.id()
	cl.peer.Request(a, "session/prompt", params)
	<-g.entered
	cl.peer.Request(b, "session/prompt", params)
	select {
	case <-early:
		t.Fatal("a prompt answered while its session.event was held")
	case <-time.After(100 * time.Millisecond):
	}
	opened.Store(true)
	unblock()

	var seq int64 = -1
	sent := map[int64]bool{}
	for answered := 0; answered < 2; {
		switch f := cl.peer.Expect().(type) {
		case *jsonrpc.Notification:
			if f.Method == sdkserver.EventNotification {
				sent[decode[protocol.SessionEventNotification](t, f).Event.Seq] = true
			}
		case *jsonrpc.Response:
			if f.Error != nil {
				t.Fatalf("prompt %s: %v", f.ID, f.Error)
			}
			var r protocol.SessionPromptResult
			if err := json.Unmarshal(f.Result, &r); err != nil {
				t.Fatal(err)
			}
			if seq >= 0 && r.Seq != seq {
				t.Fatalf("receipts differ: %d and %d", seq, r.Seq)
			}
			seq = r.Seq
			if !sent[r.Seq] {
				t.Fatalf("prompt %s answered seq %d before its session.event", f.ID, r.Seq)
			}
			answered++
		}
	}
	if len(early) > 0 {
		t.Fatal("a prompt answered before the gate opened")
	}
}

// closingFailer holds the first notification of method until release is closed,
// signalling held, then fails it, like an encoder refusing an oversized frame.
// It is closable like the transport it wraps.
type closingFailer struct {
	*jsonrpc.Conn
	method  string
	once    sync.Once
	held    chan struct{}
	release chan struct{}
}

func (f *closingFailer) Notify(method string, params any) error {
	if method == f.method {
		f.once.Do(func() { close(f.held) })
		<-f.release
		return errors.New("encode: frame too large")
	}
	return f.Conn.Notify(method, params)
}

// A notification that fails after its prompt was answered ends the transport:
// a client awaiting the run's completion, sending nothing more, sees its input
// end rather than wait for ever. The failing notification is held until the
// receipt is in hand, so the order does not depend on scheduling.
func TestForwardFailureAfterReceiptEndsTransport(t *testing.T) {
	fn := &closingFailer{method: sdkserver.RunCompletedNotification, held: make(chan struct{}), release: make(chan struct{})}
	var released sync.Once
	release := func() { released.Do(func() { close(fn.release) }) }
	cl := connectWrapped(t, newServer(t, fakemodel.Reply("unsent")), func(n sdkserver.Notifier) sdkserver.Notifier {
		fn.Conn = n.(*jsonrpc.Conn)
		return fn
	}, nil)
	t.Cleanup(release) // before the connection's Close, which a held notification would block
	id, _ := cl.create()
	var r protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go"), MessageID: "m1"}, &r)
	if r.MessageID != "m1" {
		t.Fatalf("receipt = %+v", r)
	}
	select {
	case <-fn.held:
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("session.run.completed was never written")
	}
	select {
	case <-fn.Done():
		t.Fatal("the transport ended before the notification failed")
	default:
	}
	release()
	select {
	case <-fn.Done():
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("the transport stayed open after session.run.completed failed")
	}
	if !jsonrpc.HasReason(fn.Err(), jsonrpc.ReasonClosed) {
		t.Fatalf("transport ended with %v, want closed by this end", fn.Err())
	}
}

// Under Serve a forwarding failure closes the transport and Serve returns it:
// a prompt whose session.event exceeds the frame bound is never acknowledged.
func TestServeEndsOnForwardFailure(t *testing.T) {
	const frame = 4 << 10
	srv := newServer(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		err := srv.Serve(t.Context(), inR, outW, jsonrpc.Options{MaxFrameBytes: frame})
		_ = outW.Close()
		served <- err
	}()
	t.Cleanup(func() { _ = inW.Close() })
	send := func(id int, method string, params any) {
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		go func() { _, _ = inW.Write(append(line, '\n')) }()
	}
	send(1, "initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"}})
	lines := bufio.NewScanner(outR)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		var f struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *jsonrpc.Error  `json:"error"`
		}
		if err := json.Unmarshal(lines.Bytes(), &f); err != nil {
			t.Fatalf("frame %s: %v", lines.Bytes(), err)
		}
		switch {
		case f.ID == 3 && f.Error == nil:
			t.Fatalf("prompt acknowledged although its session.event was not sent: %s", f.Result)
		case f.ID == 3:
		case f.Error != nil:
			t.Fatalf("response %d: %v", f.ID, f.Error)
		case f.ID == 1:
			send(2, "session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}})
		case f.ID == 2:
			var c protocol.SessionCreateResult
			if err := json.Unmarshal(f.Result, &c); err != nil {
				t.Fatal(err)
			}
			// The request fits the bound; its event, wrapped in an envelope
			// and a scope, does not.
			send(3, "session/prompt", protocol.SessionPromptParams{SessionID: c.SessionID, Content: text(strings.Repeat("x", frame-200)), MessageID: "m1"})
		}
	}
	select {
	case err := <-served:
		if err == nil || !strings.Contains(err.Error(), "forwarding failed") {
			t.Fatalf("Serve = %v, want the forwarding failure", err)
		}
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("Serve did not return after the forwarding failure")
	}
}
