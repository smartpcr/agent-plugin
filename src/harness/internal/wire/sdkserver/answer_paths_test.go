package sdkserver_test

import (
	"context"
	"errors"
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

// reported is the next failure the server reported to OnAnswerError.
func (ar *ansRig) reported(t *testing.T) error {
	t.Helper()
	select {
	case err := <-ar.errs:
		return err
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("no answer failure was reported")
	}
	return nil
}

// Payloads given by pointer are asked like values; a cancelled approval is
// denied as cancelled; a payload that is neither kind is refused and reported.
func TestAnswerPayloadForms(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnect(t, ar.srv, bothCaps, "")
	ref := ar.ref(t, cl.ansCreate(protocol.SessionCreateParams{}))

	done := ar.ask(human.Item{Kind: human.KindApproval, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: &approval.Prompt{Tool: "edit", Reason: "edits a file"}})
	r := cl.hostRequest(sdkserver.ApprovalRequestMethod)
	if p := params[protocol.ApprovalRequest](t, r); p.ToolName != "edit" || p.Origin != "tool" {
		t.Fatalf("approval/request from a pointer = %+v, want tool edit of origin tool", p)
	}
	cl.peer.Reply(r.ID, protocol.ApprovalResult{Outcome: "cancelled"})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied || a.ans.Text != "cancelled" {
		t.Fatalf("a cancelled approval settled %+v, want denied: cancelled", a.ans)
	}

	done = ar.ask(human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: &question.Prompt{Question: "Which?", Choices: []string{"x", "y"}}})
	q := cl.hostRequest(sdkserver.QuestionAskMethod)
	if p := params[protocol.QuestionRequest](t, q); len(p.Questions) != 1 || p.Questions[0].Text != "Which?" {
		t.Fatalf("question/ask from a pointer = %+v", p)
	}
	cl.peer.Reply(q.ID, protocol.QuestionResult{Answers: []protocol.Answer{{ID: "q1", Selected: []string{"o1"}}}})
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeAnswered || a.ans.Text != "x" {
		t.Fatalf("a pointer question settled %+v, want answered x", a.ans)
	}

	done = ar.ask(human.Item{Kind: human.KindApproval, To: human.Audience{Agent: ref}, Session: ref.Session, Payload: "not a prompt"})
	if err := ar.reported(t); !strings.Contains(err.Error(), "is not an approval or a question") {
		t.Fatalf("reported %v, want the payload refused", err)
	}
	if a := settledWith(t, done); a.ans.Outcome != human.OutcomeDenied || !strings.Contains(a.ans.Text, "refused") {
		t.Fatalf("an unknown payload settled %+v, want denied: refused", a.ans)
	}
}

// A question to a client that advertised only approvals is never sent: it
// settles unavailable.
func TestUnadvertisedQuestionIsNotAsked(t *testing.T) {
	ar := newAnsRig(t)
	_, cl := ansConnect(t, ar.srv, protocol.ClientCapabilities{Approvals: true}, "")
	ref := ar.ref(t, cl.ansCreate(protocol.SessionCreateParams{}))
	a := settledWith(t, ar.ask(human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: question.Prompt{Question: "Which?"}}))
	if a.ans.Outcome != human.OutcomeUnavailable || !strings.Contains(a.ans.Detail, "questions") {
		t.Fatalf("settled %+v, want unavailable: questions not advertised", a.ans)
	}
}

// An agent bound to the sdk channel that no connection owns, live or not,
// settles unavailable: no connection can answer it.
func TestSDKBoundAgentWithoutOwnerIsUnavailable(t *testing.T) {
	ar := newAnsRig(t)
	loner := must(ar.reg.Create(ar.o.Owner, agent.CreateOptions{Session: "ses_unowned", Model: agent.ModelSelection{Route: route, Model: fakemodel.ModelName}}))
	t.Cleanup(func() { _ = loner.Dispose(context.Background()) })
	ref := loner.Agent().Ref()
	must(ar.hreg.Bind(loner.Agent().Scope(), ref, human.Binding{Channel: sdkserver.ChannelSDK}))
	if a := settledWith(t, ar.ask(approvalFor(ref))); a.ans.Outcome != human.OutcomeUnavailable || !strings.Contains(a.ans.Detail, "no sdk connection") {
		t.Fatalf("an unowned live agent settled %+v, want unavailable: no sdk connection", a.ans)
	}

	ghost := agent.Ref{Session: session.ID("ses_ghost")}
	must(ar.hreg.Bind(ar.o.Owner, ghost, human.Binding{Channel: sdkserver.ChannelSDK}))
	if a := settledWith(t, ar.ask(approvalFor(ghost))); a.ans.Outcome != human.OutcomeUnavailable || !strings.Contains(a.ans.Detail, "no sdk connection") {
		t.Fatalf("an agent not live settled %+v, want unavailable: no sdk connection", a.ans)
	}
}

// OnFault ignores faults other than an unknown response; one it cannot refuse,
// for a transport that is no Faulter or one whose rejection fails, is
// reported with an empty interaction id.
func TestOnFaultReportsUnrefusedLateAnswers(t *testing.T) {
	ar := newAnsRig(t)
	late := &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Reason: jsonrpc.ReasonUnknownResponse, Message: "no pending request 9"}

	bare := must(ar.srv.Connect())
	t.Cleanup(func() { _ = bare.Close(context.Background()) })
	bare.OnFault(errors.New("some other fault"))
	select {
	case err := <-ar.errs:
		t.Fatalf("a fault other than an unknown response was reported: %v", err)
	default:
	}
	bare.OnFault(late)
	if err := ar.reported(t); !strings.Contains(err.Error(), "cannot refuse it") {
		t.Fatalf("reported %v, want the transport named as unable to refuse", err)
	}

	broken := must(ar.srv.Connect())
	t.Cleanup(func() { _ = broken.Close(context.Background()) })
	conn, _ := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: broken.Methods()})
	if err := broken.Attach(brokenFaults{conn}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	broken.OnFault(late)
	if err := ar.reported(t); !strings.Contains(err.Error(), "the rejection could not be sent: the pipe is gone") {
		t.Fatalf("reported %v, want the failed rejection", err)
	}

	// Under Serve, a rejection that cannot fit the frame bound even without its
	// message is not written, and is reported. The late reply itself fits.
	tiny := must(ar.srv.Connect())
	t.Cleanup(func() { _ = tiny.Close(context.Background()) })
	servePeerWith(t, tiny, jsonrpc.Options{MaxFrameBytes: 64}, nil).Reply(jsonrpc.Int64ID(9), struct{}{})
	// The reported chain leads with the late reply's own reason, so the bound's
	// is matched in the text.
	if err := ar.reported(t); !strings.Contains(err.Error(), "could not be sent: "+jsonrpc.ReasonLineTooLong) {
		t.Fatalf("reported %v, want %s", err, jsonrpc.ReasonLineTooLong)
	}
}

// Serve keeps the caller's OnError: a late reply reaches it and the client is
// still told sdk/late-answer.
func TestServeKeepsCallerOnError(t *testing.T) {
	ar := newAnsRig(t)
	seen := make(chan error, 4)
	_, cl := ansConnectWith(t, ar.srv, jsonrpc.Options{OnError: func(err error) { seen <- err }}, nil)
	cl.peer.Reply(jsonrpc.Int64ID(9999), protocol.ApprovalResult{Outcome: "allowed-once"})
	cl.rejected(sdkserver.ReasonLateAnswer, "")
	select {
	case err := <-seen:
		if !jsonrpc.HasReason(err, jsonrpc.ReasonUnknownResponse) {
			t.Fatalf("the caller's OnError got %v, want %s", err, jsonrpc.ReasonUnknownResponse)
		}
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("the caller's OnError never ran")
	}
}
