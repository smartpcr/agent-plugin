package sdkserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
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

// pipePeer is the client end of Conn.Serve over two pipes.
type pipePeer struct {
	t      *testing.T
	w      io.Writer
	frames chan jsonrpc.Frame
	served chan struct{} // closed once Serve returned serveErr
	// serveErr is what Serve returned; read it only after served is closed.
	serveErr error
}

// servePeerWith serves c with o, its output passing through wrap when set. The
// peer reads with o's frame bound, as a conforming bounded client does.
func servePeerWith(t *testing.T, c *sdkserver.Conn, o jsonrpc.Options, wrap func(io.Writer) io.Writer) *pipePeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var out io.Writer = outW
	if wrap != nil {
		out = wrap(outW)
	}
	p := &pipePeer{t: t, w: inW, frames: make(chan jsonrpc.Frame, 256), served: make(chan struct{})}
	go func() {
		p.serveErr = c.Serve(context.Background(), inR, out, o)
		_ = outW.Close()
		close(p.served)
	}()
	bound := o.MaxFrameBytes
	if bound <= 0 {
		bound = 1 << 20
	}
	go func() {
		defer close(p.frames)
		rd := jsonrpc.NewReader(outR, bound)
		for {
			f, err := rd.Read()
			if err != nil {
				return
			}
			p.frames <- f
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		_ = outR.Close()
		select {
		case <-p.served:
		case <-time.After(jsonrpctest.Timeout):
			t.Errorf("Serve did not return within %s", jsonrpctest.Timeout)
		}
		_ = inR.Close()
	})
	return p
}

func (p *pipePeer) send(f jsonrpc.Frame) {
	p.t.Helper()
	line, err := jsonrpc.Encode(f)
	if err != nil {
		p.t.Fatalf("encode %T: %v", f, err)
	}
	sent := make(chan error, 1)
	go func() { _, err := p.w.Write(append(line, '\n')); sent <- err }()
	select {
	case err := <-sent:
		if err != nil {
			p.t.Fatalf("send: %v", err)
		}
	case <-time.After(jsonrpctest.Timeout):
		p.t.Fatalf("send: the server read nothing within %s", jsonrpctest.Timeout)
	}
}

func (p *pipePeer) raw(v any) json.RawMessage {
	p.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		p.t.Fatalf("marshal %T: %v", v, err)
	}
	return raw
}

func (p *pipePeer) Request(id jsonrpc.ID, method string, params any) {
	p.t.Helper()
	p.send(&jsonrpc.Request{ID: id, Method: method, Params: p.raw(params)})
}

func (p *pipePeer) Reply(id jsonrpc.ID, result any) {
	p.t.Helper()
	p.send(&jsonrpc.Response{ID: id, Result: p.raw(result)})
}

func (p *pipePeer) ReplyError(id jsonrpc.ID, e *jsonrpc.Error) {
	p.t.Helper()
	p.send(&jsonrpc.Response{ID: id, Error: e})
}

func (p *pipePeer) Expect() jsonrpc.Frame {
	p.t.Helper()
	select {
	case f, ok := <-p.frames:
		if !ok {
			p.t.Fatal("the server's output ended")
		}
		return f
	case <-time.After(jsonrpctest.Timeout):
		p.t.Fatalf("no frame within %s", jsonrpctest.Timeout)
	}
	return nil
}

func (p *pipePeer) ExpectResponse(id jsonrpc.ID) *jsonrpc.Response {
	p.t.Helper()
	r, ok := p.Expect().(*jsonrpc.Response)
	if !ok || r.ID != id {
		p.t.Fatalf("want the response to %s, got %+v", id, r)
	}
	return r
}

func (p *pipePeer) ExpectError(id jsonrpc.ID, reason string) *jsonrpc.Error {
	p.t.Helper()
	r := p.ExpectResponse(id)
	if r.Error == nil || r.Error.Reason != reason {
		p.t.Fatalf("response to %s = %+v, want error %s", id, r, reason)
	}
	return r.Error
}

// ansConnect is an initialized connection advertising caps, served by
// Conn.Serve over pipes: the transport production uses, which carries host
// requests and their null-id rejections.
func ansConnect(t *testing.T, srv *sdkserver.Server, caps protocol.ClientCapabilities, zone string) (*sdkserver.Conn, *client) {
	t.Helper()
	return ansConnectOver(t, srv, caps, zone, jsonrpc.Options{}, nil)
}

// ansConnectWith is ansConnect, both kinds advertised, served with o and wrap.
func ansConnectWith(t *testing.T, srv *sdkserver.Server, o jsonrpc.Options, wrap func(io.Writer) io.Writer) (*sdkserver.Conn, *client) {
	t.Helper()
	return ansConnectOver(t, srv, bothCaps, "", o, wrap)
}

func ansConnectOver(t *testing.T, srv *sdkserver.Server, caps protocol.ClientCapabilities, zone string, o jsonrpc.Options, wrap func(io.Writer) io.Writer) (*sdkserver.Conn, *client) {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	cl := &client{t: t, peer: servePeerWith(t, c, o, wrap)}
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

// rejected is the next error the server sent to the null id, skipping session
// notifications: its refusal of a reply. It names interaction when not "".
func (c *client) rejected(reason, interaction string) *jsonrpc.Error {
	c.t.Helper()
	for {
		switch f := c.peer.Expect().(type) {
		case *jsonrpc.Notification:
		case *jsonrpc.Response:
			if !f.ID.IsNull() || f.Error == nil || f.Error.Reason != reason {
				c.t.Fatalf("got %+v, want an error %s to the null id", f, reason)
			}
			var d struct {
				InteractionID string `json:"interactionId"`
			}
			_ = json.Unmarshal(f.Error.Details, &d)
			if d.InteractionID != interaction {
				c.t.Fatalf("%s names interaction %q, want %q", reason, d.InteractionID, interaction)
			}
			return f.Error
		default:
			c.t.Fatalf("waiting for %s: unexpected frame %T", reason, f)
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
	if e := cl.rejected(sdkserver.ReasonInvalidAnswer, req.InteractionID); !strings.Contains(e.Message, "allowed-forever") {
		t.Fatalf("the client was told %q, want the refused outcome named", e.Message)
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
		r := cl.hostRequest(sdkserver.ApprovalRequestMethod)
		cl.peer.Reply(r.ID, map[string]string{"outcome": "allowed-session"})
		cl.rejected(sdkserver.ReasonInvalidAnswer, params[protocol.ApprovalRequest](t, r).InteractionID)
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

// brokenFaults carries host requests but cannot send a rejection.
type brokenFaults struct{ *jsonrpc.Conn }

func (brokenFaults) Fault(*jsonrpc.Error) error { return errors.New("the pipe is gone") }

// attachConnect is ansConnect over a jsonrpctest peer whose server end is
// attached as t: the transport is whatever Attach is given.
func attachConnect(t *testing.T, srv *sdkserver.Server, wrap func(*jsonrpc.Conn) sdkserver.Notifier) *client {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	conn, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods(), OnError: c.OnFault})
	if err := c.Attach(wrap(conn)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	cl := &client{t: t, peer: peer}
	var init protocol.InitializeResult
	cl.call("initialize", protocol.InitializeParams{
		ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"},
		Capabilities: bothCaps,
	}, &init)
	return cl
}

// A client must hear its answer was refused: a transport that cannot refuse is
// never asked, and a rejection that cannot be sent refuses the interaction at
// once (reported) instead of asking again.
func TestRejectionDeliveryFailureIsExplicit(t *testing.T) {
	ar := newAnsRig(t)
	plain := attachConnect(t, ar.srv, func(c *jsonrpc.Conn) sdkserver.Notifier { return c })
	ref := ar.ref(t, plain.ansCreate(protocol.SessionCreateParams{}))
	if a := settledWith(t, ar.ask(approvalFor(ref))); a.ans.Outcome != human.OutcomeUnavailable || !strings.Contains(a.ans.Detail, "host requests") {
		t.Fatalf("settled %+v over a transport that cannot refuse, want unavailable", a.ans)
	}

	broken := attachConnect(t, ar.srv, func(c *jsonrpc.Conn) sdkserver.Notifier { return brokenFaults{c} })
	ref = ar.ref(t, broken.ansCreate(protocol.SessionCreateParams{}))
	done := ar.ask(approvalFor(ref))
	broken.peer.Reply(broken.hostRequest(sdkserver.ApprovalRequestMethod).ID, map[string]string{"outcome": "allowed-forever"})
	a := settledWith(t, done)
	if a.ans.Outcome != human.OutcomeDenied || !strings.Contains(a.ans.Text, "the rejection could not be sent: the pipe is gone") {
		t.Fatalf("settled %+v, want denied naming the undelivered rejection", a.ans)
	}
	var sawUndelivered bool
	for len(ar.errs) > 0 {
		if err := <-ar.errs; strings.Contains(err.Error(), "could not be sent") {
			sawUndelivered = true
		}
	}
	if !sawUndelivered {
		t.Fatal("the undelivered rejection was not reported to OnAnswerError")
	}
	if p := ar.hreg.Pending(ref); len(p) != 0 {
		t.Fatalf("pending after an undelivered rejection = %+v, want none", p)
	}
}

// A rejection keeps the codec's bounds (sdk-acp-typert.md §6). One over
// MaxFrameBytes, because it quotes a near-limit reply, drops the quote and still
// reaches a client reading with that bound; the interaction is asked again.
func TestRejectionKeepsTheFrameBound(t *testing.T) {
	const bound = 4096
	ar := newAnsRig(t)
	_, cl := ansConnectWith(t, ar.srv, jsonrpc.Options{MaxFrameBytes: bound}, nil)
	ref := ar.ref(t, cl.ansCreate(protocol.SessionCreateParams{}))
	done := ar.ask(approvalFor(ref))

	first := cl.hostRequest(sdkserver.ApprovalRequestMethod)
	outcome := strings.Repeat("x", bound-100) // the reply fits; its rejection would not
	cl.peer.Reply(first.ID, map[string]string{"outcome": outcome})
	e := cl.rejected(sdkserver.ReasonInvalidAnswer, params[protocol.ApprovalRequest](t, first).InteractionID)
	if strings.Contains(e.Message, outcome) || !strings.Contains(e.Message, "frame bound") {
		t.Fatalf("the client was told %d bytes, want the quote dropped for the frame bound", len(e.Message))
	}
	cl.peer.Reply(cl.hostRequest(sdkserver.ApprovalRequestMethod).ID, protocol.ApprovalResult{Outcome: "allowed-once"})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeAllowedOnce {
		t.Fatalf("settled %+v, want allowed-once after the bounded rejection", a.ans)
	}
}

// stallFaults passes every line but the null-id rejections, which it holds
// until release closes: a client reading frames but never the rejection.
type stallFaults struct {
	w       io.Writer
	release chan struct{}
}

func (s stallFaults) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte(`"id":null`)) {
		<-s.release
		return 0, io.ErrClosedPipe
	}
	return s.w.Write(b)
}

// A rejection the client does not accept within WriteStall treats the client as
// gone: the interaction is refused, and the transport closes reporting the stall.
func TestStalledRejectionClosesTheTransport(t *testing.T) {
	ar := newAnsRig(t)
	release := make(chan struct{})
	_, cl := ansConnectWith(t, ar.srv, jsonrpc.Options{WriteStall: 200 * time.Millisecond},
		func(w io.Writer) io.Writer { return stallFaults{w: w, release: release} })
	t.Cleanup(func() { close(release) }) // runs before the peer's cleanup
	ref := ar.ref(t, cl.ansCreate(protocol.SessionCreateParams{}))
	done := ar.ask(approvalFor(ref))

	cl.peer.Reply(cl.hostRequest(sdkserver.ApprovalRequestMethod).ID, map[string]string{"outcome": "allowed-forever"})
	a := settledWith(t, done)
	if a.ans.Outcome != human.OutcomeDenied || !strings.Contains(a.ans.Text, "could not be sent") {
		t.Fatalf("settled %+v, want denied naming the undelivered rejection", a.ans)
	}
	p := cl.peer.(*pipePeer)
	select {
	case <-p.served:
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("the transport stayed open behind a stalled rejection")
	}
	if !jsonrpc.HasReason(p.serveErr, jsonrpc.ReasonWriteStalled) {
		t.Fatalf("Serve = %v, want %s", p.serveErr, jsonrpc.ReasonWriteStalled)
	}
}

// failFaults passes every line but the null-id rejections, which fail at once:
// a pipe that breaks under the rejection the codec never sees.
type failFaults struct{ w io.Writer }

func (f failFaults) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte(`"id":null`)) {
		return 0, errors.New("the pipe broke")
	}
	return f.w.Write(b)
}

// A rejection write that fails is a failed transport even though it bypassed the
// codec: Serve returns naming it, and the connection's sessions are released
// for another connection to resume.
func TestFailedRejectionWriteClosesTheTransport(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnectWith(t, ar.srv, jsonrpc.Options{}, func(w io.Writer) io.Writer { return failFaults{w: w} })
	id := cl.ansCreate(protocol.SessionCreateParams{})
	done := ar.ask(approvalFor(ar.ref(t, id)))

	cl.peer.Reply(cl.hostRequest(sdkserver.ApprovalRequestMethod).ID, map[string]string{"outcome": "allowed-forever"})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied || !strings.Contains(a.ans.Text, "the pipe broke") {
		t.Fatalf("settled %+v, want denied naming the failed write", a.ans)
	}
	p := cl.peer.(*pipePeer)
	select {
	case <-p.served:
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("the transport stayed open behind a failed rejection write")
	}
	if p.serveErr == nil || !strings.Contains(p.serveErr.Error(), "writing the rejection failed: the pipe broke") {
		t.Fatalf("Serve = %v, want the failed rejection write", p.serveErr)
	}
	_, cl2 := ansConnect(t, ar.srv, bothCaps, "")
	if r := cl2.roundTrip("session/resume", protocol.SessionResumeParams{SessionID: id}); r.Error != nil {
		t.Fatalf("session/resume after the transport failed: %v", r.Error)
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
	// The late answer names no pending call: the client is told, and nothing
	// settles twice.
	cl.peer.Reply(q.ID, protocol.QuestionResult{Answers: []protocol.Answer{{ID: "q1", Selected: []string{"o1"}}}})
	cl.rejected(sdkserver.ReasonLateAnswer, "")
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
	first := cl1.hostRequest(sdkserver.ApprovalRequestMethod)
	cl1.peer.Reply(first.ID, map[string]string{"outcome": "allowed-session"})
	if err := <-ar.errs; !jsonrpc.HasReason(err, sdkserver.ReasonInvalidAnswer) {
		t.Fatalf("allow-for-session reported %v, want %s", err, sdkserver.ReasonInvalidAnswer)
	}
	cl1.rejected(sdkserver.ReasonInvalidAnswer, params[protocol.ApprovalRequest](t, first).InteractionID)
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
	ar := newAnsRig(t, hang("a"), hang("b"))
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
	// The zone travels in the client record of every message queued for the session.
	for id, want := range map[string]string{plain: "Asia/Tokyo", own: "Europe/Berlin"} {
		var pr protocol.SessionPromptResult
		cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("tomorrow at nine")}, &pr)
		m := inserted(t, ar.rig, id, pr.MessageID)
		if m.Client.TimeZone != want {
			t.Fatalf("message %s of %s carries client %+v, want zone %s", pr.MessageID, id, m.Client, want)
		}
		msg := must(m.Message())
		if src, ok := msg.Source(); !ok || !strings.Contains(string(src.Data), `"timeZone":"`+want+`"`) {
			t.Fatalf("the claimed message's source data = %s, want the zone %s", src.Data, want)
		}
	}
	select {
	case <-ar.tr.PrefixServed(2):
	case <-time.After(10 * time.Second):
		t.Fatal("the prompts never reached the model")
	}
}

// inserted is message msgID as session id's inbox admitted it.
func inserted(t *testing.T, r *rig, id, msgID string) agent.InboxMessage {
	t.Helper()
	a, ok := r.reg.Get(session.ID(id))
	if !ok {
		t.Fatalf("session %s is not live", id)
	}
	for _, ev := range must(a.Session().Read(context.Background(), 0, 0)) {
		if ev.Type() != agent.SplicedType.Name() {
			continue
		}
		for _, m := range must(agent.SplicedType.As(ev)).Inserted {
			if m.ID == msgID {
				return m
			}
		}
	}
	t.Fatalf("session %s admitted no message %s", id, msgID)
	return agent.InboxMessage{}
}

// DH-T-SDK-14: a question reply settles only when it answers the one question
// asked with an offered option or, where allowed, custom text; every malformed
// reply is sdk/invalid-answer to the client and leaves it pending; a dismissal
// alone settles it declined.
func TestQuestionAnswersAreValidated(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnect(t, ar.srv, bothCaps, "")
	ref := ar.ref(t, cl.ansCreate(protocol.SessionCreateParams{}))
	ask := func(choices ...string) (<-chan asked, *jsonrpc.Request, string) {
		done := ar.ask(human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: ref}, Session: ref.Session,
			Payload: question.Prompt{Question: "Which colour?", Choices: choices}})
		r := cl.hostRequest(sdkserver.QuestionAskMethod)
		return done, r, params[protocol.QuestionRequest](t, r).InteractionID
	}
	pick := func(id string, opts ...string) protocol.Answer { return protocol.Answer{ID: id, Selected: opts} }
	bad := []struct {
		name  string
		reply protocol.QuestionResult
	}{
		{"unknown option", protocol.QuestionResult{Answers: []protocol.Answer{pick("q1", "o9")}}},
		{"duplicate id", protocol.QuestionResult{Answers: []protocol.Answer{pick("q1", "o1"), pick("q1", "o2")}}},
		{"missing id", protocol.QuestionResult{Answers: []protocol.Answer{pick("q2", "o1")}}},
		{"two options", protocol.QuestionResult{Answers: []protocol.Answer{pick("q1", "o1", "o2")}}},
		{"custom not offered", protocol.QuestionResult{Answers: []protocol.Answer{{ID: "q1", Custom: "green"}}}},
		{"dismissal with answers", protocol.QuestionResult{Dismissed: true, Answers: []protocol.Answer{pick("q1", "o1")}}},
	}
	for _, b := range bad {
		done, r, iid := ask("red", "blue")
		cl.peer.Reply(r.ID, b.reply)
		if err := <-ar.errs; !jsonrpc.HasReason(err, sdkserver.ReasonInvalidAnswer) {
			t.Fatalf("%s: reported %v, want %s", b.name, err, sdkserver.ReasonInvalidAnswer)
		}
		cl.rejected(sdkserver.ReasonInvalidAnswer, iid)
		again := cl.hostRequest(sdkserver.QuestionAskMethod)
		if p := ar.hreg.Pending(ref); len(p) != 1 || string(p[0].ID) != iid {
			t.Fatalf("%s: pending = %+v, want %s still pending", b.name, p, iid)
		}
		// The re-asked question then settles on a valid option.
		cl.peer.Reply(again.ID, protocol.QuestionResult{Answers: []protocol.Answer{pick("q1", "o2")}})
		if a := settledWith(t, done); a.ans.Outcome != human.OutcomeAnswered || a.ans.Text != "blue" || a.by.Channel != sdkserver.ChannelSDK {
			t.Fatalf("%s: settled %+v by %+v, want answered blue on sdk", b.name, a.ans, a.by)
		}
	}

	done, r, _ := ask()
	if q := params[protocol.QuestionRequest](t, r).Questions[0]; !q.AllowCustom || len(q.Options) != 0 {
		t.Fatalf("a question with no choices = %+v, want custom text allowed", q)
	}
	cl.peer.Reply(r.ID, protocol.QuestionResult{Answers: []protocol.Answer{{ID: "q1", Custom: "teal"}}})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeAnswered || a.ans.Text != "teal" {
		t.Fatalf("custom answer settled %+v, want answered teal", a.ans)
	}

	done, r, _ = ask("red")
	cl.peer.Reply(r.ID, protocol.QuestionResult{Dismissed: true})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied || a.ans.Text != "dismissed" {
		t.Fatalf("dismissal settled %+v, want denied: dismissed", a.ans)
	}
}

// DH-ADR-SDK-6: a subagent of an owned session is bound via its creator while it
// is created, so a child asking the moment it is live reaches the owning client.
func TestSubagentAsksTheOwningClientAtOnce(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnect(t, ar.srv, bothCaps, "")
	root := cl.ansCreate(protocol.SessionCreateParams{})
	parent := ar.ref(t, root)
	for i := range 20 {
		kid := session.ID("kid-" + strconv.Itoa(i))
		h := must(ar.reg.Create(ar.o.Owner, agent.CreateOptions{Session: kid, Parent: parent,
			Model: agent.ModelSelection{Route: route, Model: fakemodel.ModelName}}))
		done := ar.ask(approvalFor(h.Agent().Ref()))
		r := cl.hostRequest(sdkserver.ApprovalRequestMethod)
		if p := params[protocol.ApprovalRequest](t, r); p.SessionID != string(kid) || p.RootSessionID != root {
			t.Fatalf("approval/request = %+v, want session %s under root %s", p, kid, root)
		}
		cl.peer.Reply(r.ID, protocol.ApprovalResult{Outcome: "allowed-once"})
		if a := settledWith(t, done); a.ans.Outcome != human.OutcomeAllowedOnce {
			t.Fatalf("child %s settled %+v, want allowed-once", kid, a.ans)
		}
		must(0, h.Dispose(context.Background()))
	}

	// A child whose creator no sdk connection owns is not bound here.
	other := must(ar.reg.Create(ar.o.Owner, agent.CreateOptions{Session: "loner", Model: agent.ModelSelection{Route: route, Model: fakemodel.ModelName}}))
	kid := must(ar.reg.Create(ar.o.Owner, agent.CreateOptions{Session: "loner-kid", Parent: other.Agent().Ref(),
		Model: agent.ModelSelection{Route: route, Model: fakemodel.ModelName}}))
	if a := settledWith(t, ar.ask(approvalFor(kid.Agent().Ref()))); a.ans.Outcome != human.OutcomeUnavailable || a.ans.Detail != "unbound" {
		t.Fatalf("an unowned child settled %+v, want unavailable: unbound", a.ans)
	}
}
