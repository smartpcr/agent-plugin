//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
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

const aqaRoute vocab.RouteID = "r/aqa"

// aqaT is the suite's *testing.T. Scenarios never register cleanups or report
// failures on it directly; they use their own aqaTB.
var aqaT *testing.T

const aqaSettle = 10 * time.Second

// aqaTB is one scenario's testing.TB. Cleanups are collected and run LIFO by
// the scenario's After hook, and failures are returned from that hook, so
// servers, connections and hanging model turns never outlive their scenario
// and a leak is blamed on the scenario that caused it. Once the scenario is
// torn down, late reports fall through to the suite.
type aqaTB struct {
	testing.TB // the suite's T, for Helper, Name and logging

	mu       sync.Mutex
	cleanups []func()
	errs     []error
	done     bool
}

func newAqaTB() *aqaTB { return &aqaTB{TB: aqaT} }

func (t *aqaTB) Cleanup(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, f)
}

func (t *aqaTB) report(msg string) {
	t.mu.Lock()
	done := t.done
	if !done {
		t.errs = append(t.errs, errors.New(msg))
	}
	t.mu.Unlock()
	if done {
		t.TB.Errorf("after scenario teardown: %s", msg)
	}
}

func (t *aqaTB) Error(args ...any)                 { t.report(fmt.Sprint(args...)) }
func (t *aqaTB) Errorf(format string, args ...any) { t.report(fmt.Sprintf(format, args...)) }
func (t *aqaTB) Fail()                             { t.report("failed") }

// FailNow panics instead of calling runtime.Goexit: godog recovers a step's
// panic into that step's error.
func (t *aqaTB) FailNow()          { t.report("failed"); panic("aqaTB.FailNow") }
func (t *aqaTB) Fatal(args ...any) { t.Error(args...); panic(fmt.Sprint(args...)) }
func (t *aqaTB) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	panic(fmt.Sprintf(format, args...))
}

// teardown runs the scenario's cleanups in LIFO order and returns every
// failure reported during the scenario or its cleanups.
func (t *aqaTB) teardown() error {
	for {
		t.mu.Lock()
		n := len(t.cleanups)
		if n == 0 {
			t.done = true
			errs := t.errs
			t.mu.Unlock()
			return errors.Join(errs...)
		}
		f := t.cleanups[n-1]
		t.cleanups = t.cleanups[:n-1]
		t.mu.Unlock()
		f()
	}
}

type aqaPrompt struct{}

func (aqaPrompt) Assemble(_ context.Context, in prompt.Input) (prompt.Assembly, error) {
	a := prompt.Assembly{Sections: []prompt.RenderedSection{{Name: "e2e", Order: prompt.OrderHarness, Owner: "answerer-e2e", Text: "You are the e2e agent."}}}
	for _, d := range in.View.Tools() {
		a.Tools = append(a.Tools, d.Schema())
	}
	fp, err := vocab.Digest(a.Tools)
	a.Fingerprint = fp
	return a, err
}

func (aqaPrompt) Render(a prompt.Assembly) (string, error) { return a.Sections[0].Text, nil }

func aqaWait(f *cordis.Fiber, err error) error {
	if err != nil {
		return err
	}
	return f.Wait()
}

// aqaPeer is the client end of Conn.Serve over two pipes: the transport
// production uses, which carries host requests and their null-id rejections.
type aqaPeer struct {
	w      io.Writer
	frames chan jsonrpc.Frame
	next   int64
}

func aqaServe(t testing.TB, c *sdkserver.Conn) *aqaPeer {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &aqaPeer{w: inW, frames: make(chan jsonrpc.Frame, 256)}
	served := make(chan struct{})
	go func() {
		_ = c.Serve(context.Background(), inR, outW, jsonrpc.Options{})
		_ = outW.Close()
		close(served)
	}()
	go func() {
		defer close(p.frames)
		rd := jsonrpc.NewReader(outR, 1<<20)
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
		case <-served:
		case <-time.After(jsonrpctest.Timeout):
			t.Errorf("Serve did not return within %s", jsonrpctest.Timeout)
		}
		_ = inR.Close()
	})
	return p
}

func (p *aqaPeer) send(f jsonrpc.Frame) error {
	line, err := jsonrpc.Encode(f)
	if err != nil {
		return fmt.Errorf("encode %T: %w", f, err)
	}
	sent := make(chan error, 1)
	go func() { _, err := p.w.Write(append(line, '\n')); sent <- err }()
	select {
	case err := <-sent:
		return err
	case <-time.After(jsonrpctest.Timeout):
		return fmt.Errorf("the server read nothing within %s", jsonrpctest.Timeout)
	}
}

func (p *aqaPeer) expect() (jsonrpc.Frame, error) {
	select {
	case f, ok := <-p.frames:
		if !ok {
			return nil, fmt.Errorf("the server's output ended")
		}
		return f, nil
	case <-time.After(jsonrpctest.Timeout):
		return nil, fmt.Errorf("no frame within %s", jsonrpctest.Timeout)
	}
}

func (p *aqaPeer) request(method string, params any) (jsonrpc.ID, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return jsonrpc.ID{}, err
	}
	p.next++
	id := jsonrpc.Int64ID(p.next)
	return id, p.send(&jsonrpc.Request{ID: id, Method: method, Params: raw})
}

func (p *aqaPeer) reply(id jsonrpc.ID, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return p.send(&jsonrpc.Response{ID: id, Result: raw})
}

// call sends one request and decodes its response, skipping notifications.
func (p *aqaPeer) call(method string, params, result any) error {
	id, err := p.request(method, params)
	if err != nil {
		return err
	}
	for {
		f, err := p.expect()
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		switch f := f.(type) {
		case *jsonrpc.Notification:
		case *jsonrpc.Response:
			if f.ID != id {
				return fmt.Errorf("%s: got the response to %s", method, f.ID)
			}
			if f.Error != nil {
				return fmt.Errorf("%s: %v", method, f.Error)
			}
			if result == nil {
				return nil
			}
			return json.Unmarshal(f.Result, result)
		default:
			return fmt.Errorf("%s: unexpected frame %T", method, f)
		}
	}
}

// hostRequest is the next host request, skipping session notifications.
func (p *aqaPeer) hostRequest(method string) (*jsonrpc.Request, error) {
	for {
		f, err := p.expect()
		if err != nil {
			return nil, fmt.Errorf("waiting for %s: %w", method, err)
		}
		switch f := f.(type) {
		case *jsonrpc.Notification:
		case *jsonrpc.Request:
			if f.Method != method {
				return nil, fmt.Errorf("got a request for %s, want %s", f.Method, method)
			}
			return f, nil
		default:
			return nil, fmt.Errorf("waiting for %s: unexpected frame %T", method, f)
		}
	}
}

// rejected is the next error the server sent to the null id, skipping session
// notifications; it names interaction when not "".
func (p *aqaPeer) rejected(reason, interaction string) (*jsonrpc.Error, error) {
	for {
		f, err := p.expect()
		if err != nil {
			return nil, fmt.Errorf("waiting for %s: %w", reason, err)
		}
		switch f := f.(type) {
		case *jsonrpc.Notification:
		case *jsonrpc.Response:
			if !f.ID.IsNull() || f.Error == nil || f.Error.Reason != reason {
				return nil, fmt.Errorf("got %+v, want an error %s to the null id", f, reason)
			}
			var d struct {
				InteractionID string `json:"interactionId"`
			}
			_ = json.Unmarshal(f.Error.Details, &d)
			if d.InteractionID != interaction {
				return nil, fmt.Errorf("%s names interaction %q, want %q", reason, d.InteractionID, interaction)
			}
			return f.Error, nil
		default:
			return nil, fmt.Errorf("waiting for %s: unexpected frame %T", reason, f)
		}
	}
}

type aqaAsked struct {
	ans human.Answer
	by  human.Actor
	err error
}

type aqaState struct {
	t     *aqaTB
	reg   agent.Registry
	hreg  human.Registry
	tr    *fakemodel.Transport
	srv   *sdkserver.Server
	errs  chan error
	conn1 *sdkserver.Conn
	peer  *aqaPeer // the client being asked now
	sid   string

	done     <-chan aqaAsked
	req      *jsonrpc.Request
	approval protocol.ApprovalRequest
	question protocol.QuestionRequest

	cancelledIDs []json.RawMessage // ids of every $/cancel seen during cancelTurn
}

func (s *aqaState) newServer(turns ...fakemodel.Turn) error {
	t := s.t
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		if err := aqaWait(root.Use(c, cordis.None{})); err != nil {
			return fmt.Errorf("use %v: %w", c, err)
		}
	}
	var tsvc tools.Service
	if err := aqaWait(root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		tsvc, err = tools.Key.Get(c)
		return err
	})); err != nil {
		return err
	}
	svc := session.NewService(root, memstore.New(), session.Config{})
	s.reg = agent.NewRegistry(root)
	rt := llm.New()
	s.tr = fakemodel.NewTransport(t, turns...)
	fakemodel.Register(t, rt, aqaRoute, s.tr)
	var drvCtx *cordis.Context
	if err := aqaWait(root.Apply("driver", func(c *cordis.Context) error { drvCtx = c; return nil })); err != nil {
		return err
	}
	f, err := driver.NewFactory(driver.FactoryOptions{
		Context: drvCtx, Sessions: svc, Scopes: scope.NewTable(root), Models: rt, Tools: tsvc, Prompt: aqaPrompt{},
	})
	if err != nil {
		return err
	}
	if _, err := s.reg.SetFactory(root, f); err != nil {
		return err
	}
	s.hreg = human.New()
	t.Cleanup(s.hreg.Close)
	s.errs = make(chan error, 32)
	s.srv, err = sdkserver.New(sdkserver.Options{
		Owner: root, Agents: s.reg, Sessions: svc, Routes: rt,
		Info: protocol.Implementation{Name: "dsh", Version: "e2e"}, Limits: protocol.Limits{MaxFrameBytes: 1 << 20},
		Interactions: s.hreg, OnAnswerError: func(_ human.ID, err error) { s.errs <- err },
	})
	return err
}

// connect serves a new connection over pipes and initializes it advertising
// approvals and questions.
func (s *aqaState) connect() (*sdkserver.Conn, *aqaPeer, error) {
	c, err := s.srv.Connect()
	if err != nil {
		return nil, nil, err
	}
	s.t.Cleanup(func() { _ = c.Close(context.Background()) })
	p := aqaServe(s.t, c)
	var init protocol.InitializeResult
	if err := p.call("initialize", protocol.InitializeParams{
		ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "e2e", Version: "0"},
		Capabilities: protocol.ClientCapabilities{Approvals: true, Questions: true},
	}, &init); err != nil {
		return nil, nil, err
	}
	if init.ProtocolVersion != protocol.Version {
		return nil, nil, fmt.Errorf("initialize = %+v", init)
	}
	return c, p, nil
}

func (s *aqaState) given(turns ...fakemodel.Turn) error {
	if err := s.newServer(turns...); err != nil {
		return err
	}
	c, p, err := s.connect()
	s.conn1, s.peer = c, p
	return err
}

func (s *aqaState) givenPlain() error { return s.given() }

func (s *aqaState) givenHolding() error {
	return s.given(fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("thinking")}, Hang: true})
}

func (s *aqaState) create() error {
	var r protocol.SessionCreateResult
	if err := s.peer.call("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(aqaRoute), Model: fakemodel.ModelName}}, &r); err != nil {
		return err
	}
	if r.SessionID == "" {
		return fmt.Errorf("session/create = %+v, want a session id", r)
	}
	s.sid = r.SessionID
	return nil
}

func (s *aqaState) ref() (agent.Ref, error) {
	a, ok := s.reg.Get(session.ID(s.sid))
	if !ok {
		return agent.Ref{}, fmt.Errorf("session %s is not live", s.sid)
	}
	return a.Ref(), nil
}

// ask raises it on the registry in the background, as a tool call would.
func (s *aqaState) ask(it human.Item) {
	out := make(chan aqaAsked, 1)
	go func() {
		a, by, err := s.hreg.Ask(context.Background(), it)
		out <- aqaAsked{a, by, err}
	}()
	s.done = out
}

func (s *aqaState) settled() (aqaAsked, error) {
	select {
	case a := <-s.done:
		if a.err != nil {
			return a, fmt.Errorf("Ask: %w", a.err)
		}
		return a, nil
	case <-time.After(aqaSettle):
		return aqaAsked{}, fmt.Errorf("the interaction never settled")
	}
}

func (s *aqaState) readApproval() error {
	r, err := s.peer.hostRequest(sdkserver.ApprovalRequestMethod)
	if err != nil {
		return err
	}
	var p protocol.ApprovalRequest
	if err := json.Unmarshal(r.Params, &p); err != nil {
		return fmt.Errorf("decode %s: %w", r.Params, err)
	}
	s.req, s.approval = r, p
	return nil
}

func (s *aqaState) approvalPending(tool string) error {
	ref, err := s.ref()
	if err != nil {
		return err
	}
	s.ask(human.Item{Kind: human.KindApproval, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: approval.Prompt{Tool: tool, Call: "c1", Origin: "model", Reason: "writes a file"}})
	if err := s.readApproval(); err != nil {
		return err
	}
	p := s.approval
	if p.InteractionID == "" || p.SessionID != s.sid || p.RootSessionID != s.sid || p.ToolName != tool || p.CallID != "c1" {
		return fmt.Errorf("approval/request = %+v, want tool %s for session %s", p, tool, s.sid)
	}
	return nil
}

func (s *aqaState) answerApproval(outcome string) error {
	return s.peer.reply(s.req.ID, map[string]string{"outcome": outcome})
}

func (s *aqaState) invalidAnswerSent() error {
	select {
	case err := <-s.errs:
		if !jsonrpc.HasReason(err, sdkserver.ReasonInvalidAnswer) {
			return fmt.Errorf("the server reported %v, want %s", err, sdkserver.ReasonInvalidAnswer)
		}
	case <-time.After(aqaSettle):
		return fmt.Errorf("the server reported no failed answer")
	}
	e, err := s.peer.rejected(sdkserver.ReasonInvalidAnswer, s.approval.InteractionID)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(e.Reason, "sdk/") {
		return fmt.Errorf("rejection reason %q is not an sdk/* error", e.Reason)
	}
	return nil
}

func (s *aqaState) stillPending() error {
	ref, err := s.ref()
	if err != nil {
		return err
	}
	if p := s.hreg.Pending(ref); len(p) != 1 || string(p[0].ID) != s.approval.InteractionID {
		return fmt.Errorf("pending after an invalid answer = %+v, want %s", p, s.approval.InteractionID)
	}
	select {
	case a := <-s.done:
		return fmt.Errorf("the approval settled %+v after an invalid answer", a)
	default:
	}
	return nil
}

func (s *aqaState) askedAgain() error {
	prev, prevReq := s.approval, s.req
	if err := s.readApproval(); err != nil {
		return err
	}
	if s.approval != prev || s.req.ID == prevReq.ID {
		return fmt.Errorf("re-asked %+v as %s, want %+v under a new request id", s.approval, s.req.ID, prev)
	}
	return nil
}

func (s *aqaState) settledAllowedOnce() error {
	a, err := s.settled()
	if err != nil {
		return err
	}
	if a.ans.Outcome != human.OutcomeAllowedOnce || a.by.Channel != sdkserver.ChannelSDK {
		return fmt.Errorf("settled %+v by %+v, want allowed-once on sdk", a.ans, a.by)
	}
	return nil
}

func (s *aqaState) settledDenied() error {
	a, err := s.settled()
	if err != nil {
		return err
	}
	if a.ans.Outcome != human.OutcomeDenied {
		return fmt.Errorf("settled %+v, want denied", a.ans)
	}
	return nil
}

func (s *aqaState) promptRunning() error {
	var r protocol.SessionPromptResult
	if err := s.peer.call("session/prompt", protocol.SessionPromptParams{SessionID: s.sid, Content: []protocol.ContentBlock{{Type: "text", Text: "go"}}}, &r); err != nil {
		return err
	}
	select {
	case <-s.tr.PrefixServed(1):
	case <-time.After(aqaSettle):
		return fmt.Errorf("session %s never reached the model", s.sid)
	}
	a, ok := s.reg.Get(session.ID(s.sid))
	if !ok || a.Status() != agent.StatusRunning {
		return fmt.Errorf("session %s is not running", s.sid)
	}
	return nil
}

func (s *aqaState) questionPending(text, a, b string) error {
	ref, err := s.ref()
	if err != nil {
		return err
	}
	s.ask(human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: question.Prompt{Question: text, Choices: []string{a, b}}})
	r, err := s.peer.hostRequest(sdkserver.QuestionAskMethod)
	if err != nil {
		return err
	}
	var q protocol.QuestionRequest
	if err := json.Unmarshal(r.Params, &q); err != nil {
		return err
	}
	if q.InteractionID == "" || len(q.Questions) != 1 || q.Questions[0].Text != text || len(q.Questions[0].Options) != 2 || q.Questions[0].Options[1].Label != b {
		return fmt.Errorf("question/ask = %+v", q)
	}
	s.req, s.question = r, q
	return nil
}

// cancelTurn cancels the turn and reads until both the $/cancel for the
// question and the cancel's response arrive, in either order.
func (s *aqaState) cancelTurn() error {
	cid, err := s.peer.request("session/cancel", protocol.SessionCancelParams{SessionID: s.sid, Scope: "turn", Inbox: "keep"})
	if err != nil {
		return err
	}
	var withdrawn, replied bool
	for !withdrawn || !replied {
		f, err := s.peer.expect()
		if err != nil {
			return fmt.Errorf("during the cancel (withdrawn %v, replied %v): %w", withdrawn, replied, err)
		}
		switch f := f.(type) {
		case *jsonrpc.Notification:
			if f.Method == jsonrpc.CancelMethod {
				var c protocol.CancelParams
				if err := json.Unmarshal(f.Params, &c); err != nil {
					return fmt.Errorf("$/cancel params %s: %w", f.Params, err)
				}
				s.cancelledIDs = append(s.cancelledIDs, c.ID)
				withdrawn = withdrawn || aqaSameID(c.ID, s.req.ID)
			}
		case *jsonrpc.Response:
			if f.ID != cid || f.Error != nil {
				return fmt.Errorf("session/cancel = %+v", f)
			}
			replied = true
		default:
			return fmt.Errorf("unexpected frame %T during the cancel", f)
		}
	}
	return nil
}

func (s *aqaState) withdrawalReceived() error {
	if s.req == nil {
		return fmt.Errorf("no question/ask was sent")
	}
	for _, id := range s.cancelledIDs {
		if aqaSameID(id, s.req.ID) {
			return nil
		}
	}
	return fmt.Errorf("no $/cancel named question/ask id %v (saw %q)", s.req.ID, s.cancelledIDs)
}

// aqaSameID reports whether the wire id raw decodes to the same JSON value
// as id: a number equals a number, a string equals a string, never across.
func aqaSameID(raw json.RawMessage, id jsonrpc.ID) bool {
	decode := func(b []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil || d.More() {
			return nil, false
		}
		return v, true
	}
	want, err := id.MarshalJSON()
	if err != nil {
		return false
	}
	a, ok1 := decode(raw)
	b, ok2 := decode(want)
	return ok1 && ok2 && a == b
}

func (s *aqaState) settledWithdrawn() error {
	a, err := s.settled()
	if err != nil {
		return err
	}
	if a.ans.Outcome != human.OutcomeDenied || !strings.Contains(a.ans.Text, "withdrawn") {
		return fmt.Errorf("settled %+v, want denied: withdrawn", a.ans)
	}
	return nil
}

func (s *aqaState) lateAnswer() error {
	return s.peer.reply(s.req.ID, protocol.QuestionResult{Answers: []protocol.Answer{{ID: s.question.Questions[0].ID, Selected: []string{s.question.Questions[0].Options[0].ID}}}})
}

func (s *aqaState) lateAnswerRejected() error {
	_, err := s.peer.rejected(sdkserver.ReasonLateAnswer, "")
	return err
}

func (s *aqaState) nothingPending() error {
	ref, err := s.ref()
	if err != nil {
		return err
	}
	if p := s.hreg.Pending(ref); len(p) != 0 {
		return fmt.Errorf("pending after the withdrawal = %+v", p)
	}
	if err := s.hreg.Answer(context.Background(), human.Actor{Channel: sdkserver.ChannelSDK}, human.ID(s.question.InteractionID),
		human.Answer{Outcome: human.OutcomeAnswered, Text: "a.go"}); !errors.Is(err, human.ErrSettled) {
		return fmt.Errorf("a late answer to a withdrawn question = %v, want %v", err, human.ErrSettled)
	}
	return nil
}

func (s *aqaState) closeConn1() error {
	return s.conn1.Close(context.Background())
}

func (s *aqaState) secondResumes() error {
	_, p, err := s.connect()
	if err != nil {
		return err
	}
	s.peer = p
	var r json.RawMessage
	return p.call("session/resume", protocol.SessionResumeParams{SessionID: s.sid}, &r)
}

func (s *aqaState) askedAgainOnSecond(tool string) error {
	if err := s.readApproval(); err != nil {
		return err
	}
	if p := s.approval; p.SessionID != s.sid || p.ToolName != tool || p.InteractionID == "" {
		return fmt.Errorf("approval/request on resume = %+v, want tool %s of %s", p, tool, s.sid)
	}
	return nil
}

func (s *aqaState) raiseIdentical(tool string) error {
	ref, err := s.ref()
	if err != nil {
		return err
	}
	s.ask(human.Item{Kind: human.KindApproval, To: human.Audience{Agent: ref}, Session: ref.Session,
		Payload: approval.Prompt{Tool: tool, Call: "c1", Origin: "model", Reason: "writes a file"}})
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_approval_and_question_answerer(ctx *godog.ScenarioContext) {
	var s *aqaState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &aqaState{t: newAqaTB()}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		return c, s.t.teardown()
	})
	ctx.Step(`^a client served over stdio pipes advertising approvals and questions against an SDK server with an interaction registry$`, func() error { return s.givenPlain() })
	ctx.Step(`^a client served over stdio pipes advertising approvals and questions against an SDK server with an interaction registry and a model that holds the turn open$`, func() error { return s.givenHolding() })
	ctx.Step(`^the client creates a session$`, func() error { return s.create() })
	ctx.Step(`^an approval for tool "([^"]*)" is pending on that session$`, func(tool string) error { return s.approvalPending(tool) })
	ctx.Step(`^(?:the|the second) client answers the approval/request with outcome "([^"]*)"$`, func(o string) error { return s.answerApproval(o) })
	ctx.Step(`^the server sends sdk/invalid-answer to the null id naming the interaction$`, func() error { return s.invalidAnswerSent() })
	ctx.Step(`^the approval is still pending in the registry$`, func() error { return s.stillPending() })
	ctx.Step(`^the same approval is asked again under a new request id$`, func() error { return s.askedAgain() })
	ctx.Step(`^the approval settles allowed-once by the sdk channel$`, func() error { return s.settledAllowedOnce() })
	ctx.Step(`^the approval settles denied$`, func() error { return s.settledDenied() })
	ctx.Step(`^the client prompts the session and its turn is running$`, func() error { return s.promptRunning() })
	ctx.Step(`^a question "([^"]*)" with choices "([^"]*)" and "([^"]*)" is pending on that session$`, func(q, a, b string) error { return s.questionPending(q, a, b) })
	ctx.Step(`^the client cancels the session's turn$`, func() error { return s.cancelTurn() })
	ctx.Step(`^the client receives \$/cancel for the question/ask request$`, func() error { return s.withdrawalReceived() })
	ctx.Step(`^the question settles denied as withdrawn$`, func() error { return s.settledWithdrawn() })
	ctx.Step(`^the client sends a late answer selecting the first option$`, func() error { return s.lateAnswer() })
	ctx.Step(`^the server sends sdk/late-answer to the null id$`, func() error { return s.lateAnswerRejected() })
	ctx.Step(`^nothing is pending in the registry and a late registry answer is refused$`, func() error { return s.nothingPending() })
	ctx.Step(`^connection 1 closes$`, func() error { return s.closeConn1() })
	ctx.Step(`^a second client connects and resumes the session$`, func() error { return s.secondResumes() })
	ctx.Step(`^an identical approval for tool "([^"]*)" is raised on that session$`, func(tool string) error { return s.raiseIdentical(tool) })
	ctx.Step(`^the second client is asked again with approval/request for tool "([^"]*)"$`, func(tool string) error { return s.askedAgainOnSecond(tool) })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_approval_and_question_answerer(t *testing.T) {
	aqaT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_approval_and_question_answerer,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_approval_and_question_answerer.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the approval and question answerer e2e scenarios failed")
	}
}
