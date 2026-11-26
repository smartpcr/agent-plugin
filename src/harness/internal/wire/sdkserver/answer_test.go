package sdkserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// ansRig is a rig whose server answers on an interaction registry; errs gets
// every failed reply the server reports.
type ansRig struct {
	*rig
	hreg human.Registry
	errs chan error
	srv  *sdkserver.Server
}

func newAnsRig(t *testing.T, turns ...fakemodel.Turn) *ansRig {
	t.Helper()
	r := newRig(t, turns...)
	ar := &ansRig{rig: r, hreg: human.New(), errs: make(chan error, 16)}
	t.Cleanup(ar.hreg.Close)
	r.o.Interactions = ar.hreg
	r.o.OnAnswerError = func(_ human.ID, err error) { ar.errs <- err }
	ar.srv = r.server()
	return ar
}

var bothCaps = protocol.ClientCapabilities{Approvals: true, Questions: true}

// ansConnect is an attached, initialized connection advertising caps.
func ansConnect(t *testing.T, srv *sdkserver.Server, caps protocol.ClientCapabilities, zone string) (*sdkserver.Conn, *client) {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	conn, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	if err := c.Attach(conn); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	cl := &client{t: t, peer: peer}
	var init protocol.InitializeResult
	cl.call("initialize", protocol.InitializeParams{
		ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"},
		Capabilities: caps, TimeZone: zone,
	}, &init)
	return c, cl
}

// roundTrip sends one request and returns its response, skipping notifications.
func (c *client) roundTrip(method string, params any) *jsonrpc.Response {
	c.t.Helper()
	id := c.id()
	c.peer.Request(id, method, params)
	for {
		switch f := c.peer.Expect().(type) {
		case *jsonrpc.Notification:
		case *jsonrpc.Response:
			if f.ID == id {
				return f
			}
			c.t.Fatalf("%s: got the response to %s", method, f.ID)
		default:
			c.t.Fatalf("%s: unexpected frame %T", method, f)
		}
	}
}

func (c *client) ansCreate(p protocol.SessionCreateParams) string {
	c.t.Helper()
	p.Model = &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}
	r := c.roundTrip("session/create", p)
	var res protocol.SessionCreateResult
	if r.Error != nil || json.Unmarshal(r.Result, &res) != nil || res.SessionID == "" {
		c.t.Fatalf("session/create = %+v", r)
	}
	return res.SessionID
}

// hostRequest is the next host request, skipping session notifications.
func (c *client) hostRequest(method string) *jsonrpc.Request {
	c.t.Helper()
	for {
		switch f := c.peer.Expect().(type) {
		case *jsonrpc.Notification:
		case *jsonrpc.Request:
			if f.Method != method {
				c.t.Fatalf("got a request for %s, want %s", f.Method, method)
			}
			return f
		default:
			c.t.Fatalf("waiting for %s: unexpected frame %T", method, f)
		}
	}
}

type asked struct {
	ans human.Answer
	by  human.Actor
	err error
}

// ask asks it on the registry in the background, as a tool call would.
func (ar *ansRig) ask(it human.Item) <-chan asked {
	out := make(chan asked, 1)
	go func() {
		a, by, err := ar.hreg.Ask(context.Background(), it)
		out <- asked{a, by, err}
	}()
	return out
}

func settledWith(t *testing.T, ch <-chan asked) asked {
	t.Helper()
	select {
	case a := <-ch:
		if a.err != nil {
			t.Fatalf("Ask: %v", a.err)
		}
		return a
	case <-time.After(10 * time.Second):
		t.Fatal("the interaction never settled")
	}
	return asked{}
}

func approvalFor(ref agent.Ref) human.Item {
	return human.Item{Kind: human.KindApproval, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: approval.Prompt{Tool: "write", Call: "c1", Origin: "model", Reason: "writes a file"}}
}

func params[T any](t *testing.T, r *jsonrpc.Request) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.Params, &v); err != nil {
		t.Fatalf("decode %s: %v", r.Params, err)
	}
	return v
}

func (ar *ansRig) ref(t *testing.T, id string) agent.Ref {
	t.Helper()
	a, ok := ar.reg.Get(session.ID(id))
	if !ok {
		t.Fatalf("session %s is not live", id)
	}
	return a.Ref()
}

// TC-12a: an answer not offered is sdk/invalid-answer and settles nothing; the
// same interaction is asked again, and a valid answer then settles it.
func TestInvalidAnswerRejectedApprovalStaysPending(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnect(t, ar.srv, bothCaps, "")
	id := cl.ansCreate(protocol.SessionCreateParams{})
	ref := ar.ref(t, id)
	done := ar.ask(approvalFor(ref))

	first := cl.hostRequest(sdkserver.ApprovalRequestMethod)
	req := params[protocol.ApprovalRequest](t, first)
	if req.SessionID != id || req.RootSessionID != id || req.ToolName != "write" || req.CallID != "c1" || req.InteractionID == "" {
		t.Fatalf("approval/request = %+v", req)
	}
	cl.peer.Reply(first.ID, map[string]string{"outcome": "allowed-forever"})
	if err := <-ar.errs; !jsonrpc.HasReason(err, sdkserver.ReasonInvalidAnswer) {
		t.Fatalf("reported %v, want %s", err, sdkserver.ReasonInvalidAnswer)
	}
	again := cl.hostRequest(sdkserver.ApprovalRequestMethod)
	if p := ar.hreg.Pending(ref); len(p) != 1 || string(p[0].ID) != req.InteractionID {
		t.Fatalf("pending after an invalid answer = %+v, want %s", p, req.InteractionID)
	}
	if got := params[protocol.ApprovalRequest](t, again); got != req || again.ID == first.ID {
		t.Fatalf("re-asked %+v as %s, want %+v under a new request id", got, again.ID, req)
	}
	cl.peer.Reply(again.ID, protocol.ApprovalResult{Outcome: "allowed-once"})
	a := settledWith(t, done)
	if a.ans.Outcome != human.OutcomeAllowedOnce || a.by.Channel != sdkserver.ChannelSDK {
		t.Fatalf("settled %+v by %+v, want allowed-once on sdk", a.ans, a.by)
	}

	// Replies invalid every time settle as refused after MaxAnswerAttempts.
	done = ar.ask(approvalFor(ref))
	for range sdkserver.MaxAnswerAttempts {
		cl.peer.Reply(cl.hostRequest(sdkserver.ApprovalRequestMethod).ID, map[string]string{"outcome": "allowed-session"})
	}
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied {
		t.Fatalf("settled %+v, want denied after %d invalid answers", a.ans, sdkserver.MaxAnswerAttempts)
	}
	// An error reply refuses.
	done = ar.ask(approvalFor(ref))
	cl.peer.ReplyError(cl.hostRequest(sdkserver.ApprovalRequestMethod).ID, &jsonrpc.Error{Code: jsonrpc.CodeApplication, Reason: "client/boom", Message: "boom"})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied {
		t.Fatalf("settled %+v after an error reply, want denied", a.ans)
	}
}

// A kind the client did not advertise is never sent: it settles unavailable.
func TestUnadvertisedKindIsNotAsked(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnect(t, ar.srv, protocol.ClientCapabilities{Questions: true}, "")
	ref := ar.ref(t, cl.ansCreate(protocol.SessionCreateParams{}))
	if a := settledWith(t, ar.ask(approvalFor(ref))); a.ans.Outcome != human.OutcomeUnavailable || !strings.Contains(a.ans.Detail, "approvals") {
		t.Fatalf("settled %+v, want unavailable: approvals not advertised", a.ans)
	}
}

// TC-12a: cancelling the turn withdraws a pending question with $/cancel; the
// late answer changes nothing.
func TestWithdrawalCancelsPendingQuestion(t *testing.T) {
	ar := newAnsRig(t, hang("thinking"))
	_, cl := ansConnect(t, ar.srv, bothCaps, "")
	id := cl.ansCreate(protocol.SessionCreateParams{})
	if r := cl.roundTrip("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go")}); r.Error != nil {
		t.Fatalf("session/prompt: %v", r.Error)
	}
	ref := running(t, ar.rig, id).Ref()
	done := ar.ask(human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: question.Prompt{Question: "Which file?", Choices: []string{"a.go", "b.go"}}})
	q := cl.hostRequest(sdkserver.QuestionAskMethod)
	qp := params[protocol.QuestionRequest](t, q)
	if len(qp.Questions) != 1 || len(qp.Questions[0].Options) != 2 || qp.Questions[0].Options[1].Label != "b.go" {
		t.Fatalf("question/ask = %+v", qp)
	}

	cid := cl.id()
	cl.peer.Request(cid, "session/cancel", protocol.SessionCancelParams{SessionID: id, Scope: "turn", Inbox: "keep"})
	var withdrawn, replied bool
	for !withdrawn || !replied {
		switch f := cl.peer.Expect().(type) {
		case *jsonrpc.Notification:
			if f.Method == jsonrpc.CancelMethod {
				var c struct {
					ID json.RawMessage `json:"id"`
				}
				_ = json.Unmarshal(f.Params, &c)
				withdrawn = withdrawn || string(c.ID) == q.ID.String()
			}
		case *jsonrpc.Response:
			if f.ID != cid || f.Error != nil {
				t.Fatalf("session/cancel = %+v", f)
			}
			replied = true
		default:
			t.Fatalf("unexpected frame %T during the cancel", f)
		}
	}
	a := settledWith(t, done)
	if a.ans.Outcome != human.OutcomeDenied || !strings.Contains(a.ans.Text, "withdrawn") {
		t.Fatalf("settled %+v, want denied: withdrawn", a.ans)
	}
	// The late answer names no pending call: nothing settles twice.
	cl.peer.Reply(q.ID, protocol.QuestionResult{Answers: []protocol.Answer{{ID: "q1", Selected: []string{"o1"}}}})
	if p := ar.hreg.Pending(ref); len(p) != 0 {
		t.Fatalf("pending after the withdrawal = %+v", p)
	}
	if err := ar.hreg.Answer(context.Background(), human.Actor{Channel: sdkserver.ChannelSDK}, human.ID(qp.InteractionID), human.Answer{Outcome: human.OutcomeAnswered, Text: "a.go"}); err == nil {
		t.Fatal("a late answer to a withdrawn question was accepted")
	}
}

// TC-12a, NG-8: no answer on the sdk channel grants for the session, and nothing
// answered on one connection carries over a resume on another.
func TestNoSessionGrantSurvivesResume(t *testing.T) {
	ar := newAnsRig(t)
	c1, cl1 := ansConnect(t, ar.srv, bothCaps, "")
	id := cl1.ansCreate(protocol.SessionCreateParams{})
	done := ar.ask(approvalFor(ar.ref(t, id)))
	cl1.peer.Reply(cl1.hostRequest(sdkserver.ApprovalRequestMethod).ID, map[string]string{"outcome": "allowed-session"})
	if err := <-ar.errs; !jsonrpc.HasReason(err, sdkserver.ReasonInvalidAnswer) {
		t.Fatalf("allow-for-session reported %v, want %s", err, sdkserver.ReasonInvalidAnswer)
	}
	cl1.peer.Reply(cl1.hostRequest(sdkserver.ApprovalRequestMethod).ID, protocol.ApprovalResult{Outcome: "allowed-once"})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeAllowedOnce {
		t.Fatalf("settled %+v, want allowed-once and never allowed-session", a.ans)
	}
	old := ar.ref(t, id)
	if err := c1.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a := settledWith(t, ar.ask(approvalFor(old))); a.ans.Outcome != human.OutcomeUnavailable {
		t.Fatalf("asked after the connection closed: %+v, want unavailable", a.ans)
	}

	_, cl2 := ansConnect(t, ar.srv, bothCaps, "")
	if r := cl2.roundTrip("session/resume", protocol.SessionResumeParams{SessionID: id}); r.Error != nil {
		t.Fatalf("session/resume: %v", r.Error)
	}
	ref := ar.ref(t, id)
	done = ar.ask(approvalFor(ref))
	again := cl2.hostRequest(sdkserver.ApprovalRequestMethod)
	if p := params[protocol.ApprovalRequest](t, again); p.SessionID != id || p.ToolName != "write" {
		t.Fatalf("approval/request on resume = %+v", p)
	}
	cl2.peer.Reply(again.ID, protocol.ApprovalResult{Outcome: "rejected"})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied {
		t.Fatalf("settled %+v, want denied", a.ans)
	}
}

// U-K2-5: an unknown zone is jsonrpc/invalid-params naming the field and creates
// nothing; create's zone overrides initialize's for its session.
func TestTimeZone(t *testing.T) {
	ar := newAnsRig(t)
	c := must(ar.srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	cl := &client{t: t, peer: peer}
	init := protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test"}, TimeZone: "Mars/Olympus"}
	initID := cl.id()
	peer.Request(initID, "initialize", init)
	if err := peer.ExpectError(initID, jsonrpc.ReasonInvalidParams); err != nil && !strings.Contains(err.Message, "timeZone") {
		t.Fatalf("initialize refused with %q, want the field named", err.Message)
	}
	init.TimeZone = "Asia/Tokyo"
	var res protocol.InitializeResult
	cl.call("initialize", init, &res)

	cl.refused("session/create", protocol.SessionCreateParams{SessionID: "ses_bad", TimeZone: "Local"}, jsonrpc.ReasonInvalidParams)
	if _, ok := ar.reg.Get("ses_bad"); ok || len(c.Owned()) != 0 {
		t.Fatal("a refused zone created a session")
	}
	plain := cl.create()
	own := cl.createWith(protocol.SessionCreateParams{TimeZone: "Europe/Berlin"})
	if got := c.TimeZone(session.ID(plain)); got != "Asia/Tokyo" {
		t.Fatalf("zone of %s = %q, want the connection's", plain, got)
	}
	if got := c.TimeZone(session.ID(own)); got != "Europe/Berlin" {
		t.Fatalf("zone of %s = %q, want its create override", own, got)
	}
}
