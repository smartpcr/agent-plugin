package sdkserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/caltime"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// This file is the approval and question answerer (DH-D-SDK-6, DH-ADR-SDK-6):
// the server registers one answerer on the sdk channel and routes each
// interaction to the connection that owns its agent's root session. An
// interaction becomes one host request, approval/request or question/ask, sent
// only when the client advertised the capability; the reply settles it once.
// Settling it any other way (a turn cancel, an unbind, shutdown) ends the
// request's context, which sends $/cancel; a reply after that names no pending
// call and changes nothing.
//
// A reply that is not one of the answers offered does not settle the item: the
// client gets sdk/invalid-answer (an error response to the null id, since its
// reply was itself a response, with the interactionId in the details) and the
// same interaction is asked again, at most MaxAnswerAttempts times in all, after
// which it settles as refused. An error reply, a lost connection and shutdown
// settle as refused. A reply naming no pending request (a late answer) is
// answered sdk/late-answer the same way and changes nothing.
//
// A subagent of an owned agent is bound via its creator while it is created
// (agent.Created), before it is live, so no interaction of it can run unbound.
//
// No answer on this channel creates a session grant (I2 Q5, NG-8): the wire
// offers allowed-once, rejected and cancelled only, and nothing here maps to
// human.OutcomeAllowedSession.

// ChannelSDK is the interaction channel of the SDK carrier.
const ChannelSDK human.Channel = "sdk"

// ReasonInvalidAnswer is a reply to approval/request or question/ask that is not
// one of the answers offered.
const ReasonInvalidAnswer = "sdk/invalid-answer"

// ReasonLateAnswer is a reply that names no pending host request: the
// interaction was settled or withdrawn first.
const ReasonLateAnswer = "sdk/late-answer"

// MaxAnswerAttempts bounds how often one interaction is asked after invalid
// replies before it settles as refused.
const MaxAnswerAttempts = 3

// The host requests the answerer sends.
const (
	ApprovalRequestMethod = "approval/request"
	QuestionAskMethod     = "question/ask"
)

// questionID is the id of the one question an interaction asks.
const questionID = "q1"

// Caller sends one request to the client and waits for its reply, ending the
// wait (and sending $/cancel) when ctx ends; *jsonrpc.Conn is one. A Notifier
// given to Attach that is also a Caller carries the host requests.
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// faulter sends an error to the client's null id; *jsonrpc.Conn is one.
type faulter interface {
	Fault(err error) error
}

// errNoFault reports a codec without jsonrpc.Conn.Fault (an older SDK pin).
var errNoFault = errors.New("sdkserver: the codec cannot send a null-id error")

// answerer is the server's one registration on ChannelSDK.
type answerer struct{ s *Server }

// Deliver routes it to the connection owning its root and starts the request; it
// fails (settling it unavailable) when no connection can answer.
func (a answerer) Deliver(ctx context.Context, it human.Item) error {
	c, root := a.s.ownerOf(it.To.Agent.Session)
	if c == nil {
		return fmt.Errorf("no sdk connection owns the root of session %q", it.To.Agent.Session)
	}
	caps, call := c.answering()
	switch {
	case call == nil:
		return errors.New("the sdk connection carries no host requests")
	case it.Kind == human.KindApproval && !caps.Approvals:
		return errors.New("the sdk client did not advertise approvals")
	case it.Kind == human.KindQuestion && !caps.Questions:
		return errors.New("the sdk client did not advertise questions")
	}
	go c.ask(ctx, call, root, it)
	return nil
}

// Withdraw needs nothing more: the registry has already ended the request's
// context, which sent $/cancel.
func (answerer) Withdraw(human.ID, human.Reason) {}

// ownerOf is the connection owning id's root session and that root, following
// subagent headers to their creators; nil when none does.
func (s *Server) ownerOf(id session.ID) (*Conn, session.ID) {
	for range 16 {
		s.mu.Lock()
		c := s.held[id]
		s.mu.Unlock()
		if c != nil {
			if _, ok := c.agent(id); ok {
				return c, id
			}
			return nil, ""
		}
		a, ok := s.o.Agents.Get(id)
		if !ok {
			return nil, ""
		}
		h := a.Session().Header()
		if h.Origin != "subagent" || h.Creator == "" {
			return nil, ""
		}
		id = session.ID(h.Creator)
	}
	return nil, ""
}

// answering is what the client advertised and the transport host requests go
// over, nil before Attach or for a notifier that is no Caller.
func (c *Conn) answering() (protocol.ClientCapabilities, Caller) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.caps, nil
	}
	call, _ := c.out.(Caller)
	return c.caps, call
}

// actor is who an answer on this connection is by.
func (c *Conn) actor() human.Actor {
	return human.Actor{Channel: ChannelSDK, Name: c.by(), Peer: human.PeerRef(c.name)}
}

// ask asks it until it is answered validly, refused, or settled elsewhere.
func (c *Conn) ask(ctx context.Context, call Caller, root session.ID, it human.Item) {
	reg := c.s.o.Interactions
	for attempt := 1; ; attempt++ {
		ans, err := c.askOnce(ctx, call, root, it)
		if ctx.Err() != nil {
			return // settled by another path; a late reply changes nothing
		}
		if err == nil {
			_ = reg.Answer(ctx, c.actor(), it.ID, ans)
			return
		}
		c.s.reportAnswer(it.ID, err)
		if jsonrpc.HasReason(err, ReasonInvalidAnswer) {
			reject(call, it.ID, err)
			if attempt < MaxAnswerAttempts {
				continue
			}
		}
		_ = reg.Answer(ctx, c.actor(), it.ID, human.Answer{Outcome: human.OutcomeDenied, Text: "refused: " + err.Error()})
		return
	}
}

// reportAnswer hands a failed reply to Options.OnAnswerError.
func (s *Server) reportAnswer(id human.ID, err error) {
	if f := s.o.OnAnswerError; f != nil {
		f(id, err)
	}
}

// reject tells the client its reply to interaction id was refused with err's
// reason: an error response to the null id whose details name the interaction.
func reject(call Caller, id human.ID, err error) {
	f, ok := call.(faulter)
	if !ok {
		return
	}
	e := &jsonrpc.Error{Code: jsonrpc.CodeApplication, Reason: ReasonInvalidAnswer, Message: err.Error()}
	if je, ok := errors.AsType[*jsonrpc.Error](err); ok {
		e.Reason, e.Message = je.Reason, je.Message
	}
	e.Details, _ = json.Marshal(map[string]string{"interactionId": string(id)})
	_ = f.Fault(e)
}

// OnFault is the connection's jsonrpc.Options.OnError (Serve sets it): a reply
// naming no pending request is a late answer, which the client is told of with
// sdk/late-answer; it settles nothing.
func (c *Conn) OnFault(err error) {
	if !jsonrpc.HasReason(err, jsonrpc.ReasonUnknownResponse) {
		return
	}
	c.mu.Lock()
	f, ok := c.out.(faulter)
	c.mu.Unlock()
	if ok {
		_ = f.Fault(fail(ReasonLateAnswer, "the reply names no pending request; the interaction was settled or withdrawn"))
	}
}

func invalidAnswer(format string, args ...any) *jsonrpc.Error {
	return fail(ReasonInvalidAnswer, fmt.Sprintf(format, args...))
}

// askOnce sends it once and validates the reply.
func (c *Conn) askOnce(ctx context.Context, call Caller, root session.ID, it human.Item) (human.Answer, error) {
	switch p := it.Payload.(type) {
	case approval.Prompt:
		return askApproval(ctx, call, root, it, p)
	case *approval.Prompt:
		return askApproval(ctx, call, root, it, *p)
	case question.Prompt:
		return askQuestion(ctx, call, root, it, p)
	case *question.Prompt:
		return askQuestion(ctx, call, root, it, *p)
	}
	return human.Answer{}, fmt.Errorf("interaction %q: payload %T is not an approval or a question", it.ID, it.Payload)
}

func askApproval(ctx context.Context, call Caller, root session.ID, it human.Item, p approval.Prompt) (human.Answer, error) {
	var r protocol.ApprovalResult
	err := call.Call(ctx, ApprovalRequestMethod, protocol.ApprovalRequest{
		InteractionID: string(it.ID), SessionID: string(it.Session), RootSessionID: string(root),
		ToolName: p.Tool, CallID: orElse(p.Call, it.Call), Origin: orElse(p.Origin, "tool"), Reason: p.Reason,
	}, &r)
	if err != nil {
		return human.Answer{}, err
	}
	switch r.Outcome {
	case "allowed-once":
		return human.Answer{Outcome: human.OutcomeAllowedOnce}, nil
	case "rejected":
		return human.Answer{Outcome: human.OutcomeDenied}, nil
	case "cancelled":
		return human.Answer{Outcome: human.OutcomeDenied, Text: "cancelled"}, nil
	}
	return human.Answer{}, invalidAnswer("approval %q: outcome %q is not allowed-once, rejected or cancelled", it.ID, r.Outcome)
}

// optionID is the wire id of choice i.
func optionID(i int) string { return "o" + strconv.Itoa(i+1) }

func askQuestion(ctx context.Context, call Caller, root session.ID, it human.Item, p question.Prompt) (human.Answer, error) {
	q := protocol.Question{ID: questionID, Text: p.Question, AllowCustom: len(p.Choices) == 0}
	for i, label := range p.Choices {
		q.Options = append(q.Options, protocol.QuestionOption{ID: optionID(i), Label: label})
	}
	var r protocol.QuestionResult
	if err := call.Call(ctx, QuestionAskMethod, protocol.QuestionRequest{
		InteractionID: string(it.ID), SessionID: string(it.Session), RootSessionID: string(root),
		Questions: []protocol.Question{q},
	}, &r); err != nil {
		return human.Answer{}, err
	}
	return answerOf(it.ID, q, p.Choices, r)
}

// answerOf validates a reply to the one question q: a dismissal, or exactly one
// answer naming q with one offered option or, where allowed, custom text.
func answerOf(id human.ID, q protocol.Question, choices []string, r protocol.QuestionResult) (human.Answer, error) {
	if r.Dismissed {
		if len(r.Answers) > 0 {
			return human.Answer{}, invalidAnswer("question %q: a dismissal carries no answers", id)
		}
		return human.Answer{Outcome: human.OutcomeDenied, Text: "dismissed"}, nil
	}
	if len(r.Answers) != 1 || r.Answers[0].ID != q.ID {
		ids := make([]string, len(r.Answers))
		for i, a := range r.Answers {
			ids[i] = a.ID
		}
		return human.Answer{}, invalidAnswer("question %q: the answers name %q, want %q exactly once", id, ids, q.ID)
	}
	a := r.Answers[0]
	switch {
	case len(a.Selected) == 1 && a.Custom == "":
		i := slices.IndexFunc(q.Options, func(o protocol.QuestionOption) bool { return o.ID == a.Selected[0] })
		if i < 0 {
			return human.Answer{}, invalidAnswer("question %q: option %q was not offered", id, a.Selected[0])
		}
		return human.Answer{Outcome: human.OutcomeAnswered, Text: choices[i]}, nil
	case len(a.Selected) == 0 && a.Custom != "" && q.AllowCustom:
		return human.Answer{Outcome: human.OutcomeAnswered, Text: a.Custom}, nil
	case len(a.Selected) == 0 && a.Custom != "":
		return human.Answer{}, invalidAnswer("question %q: custom text was not offered", id)
	}
	return human.Answer{}, invalidAnswer("question %q: want one selected option or custom text", id)
}

// bind routes root a's interactions to the sdk channel for as long as a lives;
// a no-op on a host with no interaction registry.
func (c *Conn) bind(a agent.Agent) error {
	reg := c.s.o.Interactions
	if reg == nil {
		return nil
	}
	if _, err := reg.Bind(a.Scope(), a.Ref(), human.Binding{Channel: ChannelSDK}); err != nil {
		return fmt.Errorf("sdkserver: bind session %q to the sdk channel: %w", a.Ref().Session, err)
	}
	return nil
}

// bindSubagent is the agent.Created listener: a subagent whose creator's root an
// sdk connection owns is bound via its creator, before it goes live, for as
// long as it lives. Other agents are left alone; a binding another carrier
// already made stands. Any other failure fails the creation: an unbound child
// would ask nobody.
func (s *Server) bindSubagent(_ *cordis.Context, n agent.CreatedNotice) error {
	k := n.Agent
	h := k.Session().Header()
	if h.Origin != "subagent" || h.Creator == "" {
		return nil
	}
	if c, _ := s.ownerOf(session.ID(h.Creator)); c == nil {
		return nil
	}
	p, ok := s.o.Agents.Get(session.ID(h.Creator))
	if !ok {
		return fmt.Errorf("sdkserver: the creator %q of subagent %q is not live", h.Creator, k.Ref().Session)
	}
	if _, err := s.o.Interactions.Bind(k.Scope(), k.Ref(), human.Binding{Via: p.Ref()}); err != nil && !errors.Is(err, human.ErrTaken) {
		return fmt.Errorf("sdkserver: bind subagent %q via %q: %w", k.Ref().Session, h.Creator, err)
	}
	return nil
}

// withdrawQuestions settles the pending questions of root's tree as declined: a
// question outlives a turn cancel in the question service, so the carrier
// withdraws it; settling it ends its request, which sends $/cancel.
func (c *Conn) withdrawQuestions(ctx context.Context, root session.ID) {
	reg := c.s.o.Interactions
	if reg == nil {
		return
	}
	for _, x := range c.Tree(root) {
		for _, it := range reg.Pending(x.Ref()) {
			if it.Kind == human.KindQuestion {
				_ = reg.Answer(ctx, c.actor(), it.ID, human.Answer{Outcome: human.OutcomeDenied, Text: "withdrawn: the turn was cancelled"})
			}
		}
	}
}

// checkZone refuses a time zone name the host's zone data does not hold; ""
// is none.
func checkZone(field, name string) error {
	if name == "" {
		return nil
	}
	if _, r := caltime.LoadZone(name); r != caltime.OK {
		return invalidParams(fmt.Sprintf("%s: %q is not a known IANA time zone (%s)", field, name, r))
	}
	return nil
}

// TimeZone is the user's IANA zone for session id (U-K2-5): its
// session/create.timeZone, else the connection's initialize.timeZone; "" when
// the client gave none. The host process's zone is never presented as the
// user's.
func (c *Conn) TimeZone(id session.ID) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return orElse(c.zones[id], c.zone)
}

// clientRecord is the client record send puts on every message it queues for
// session id: the user's zone, zero when the client gave none.
func (c *Conn) clientRecord(id session.ID) agent.ClientRecord {
	return agent.ClientRecord{TimeZone: c.TimeZone(id)}
}
