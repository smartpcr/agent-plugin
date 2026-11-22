// Package question lets the model ask the user a question and wait for the
// answer or move on (DH-D-HUMAN-5). A question is asked through the interaction
// registry with a deadline. An answer always arrives through the agent's inbox
// as a user message naming the call: a waiting call returns the answer and the
// message is taken back out of the inbox (logged discarded, returned-to-call); a
// late answer, after the deadline or for a call that moved on, stays in the
// inbox and is claimed at the next boundary. Each question takes one answer; a
// second is refused with question/reply-queued. Both question/asked and
// question/answered are in the log, the answer attributed to its principal and
// channel.
package question

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// Outcome is how an ask settled for its caller.
type Outcome string

// The outcomes.
const (
	// OutcomeAnswered: the answer came before the deadline.
	OutcomeAnswered Outcome = "answered"
	// OutcomePending: the deadline passed, or the caller moved on; a late
	// answer arrives through the inbox.
	OutcomePending Outcome = "pending"
	// OutcomeDeclined: the person refused to answer; the question is closed.
	OutcomeDeclined Outcome = "declined"
	// OutcomeNoAnswerer: nobody can be asked; the question is closed.
	OutcomeNoAnswerer Outcome = "no-answerer"
)

// DefaultTimeout is the deadline of a question when neither the question nor
// the config sets one.
const DefaultTimeout = 10 * time.Minute

// Question is one question of the model.
type Question struct {
	// Agent is the asking agent; required.
	Agent agent.Ref
	// Call is the tool call asking, "" for none.
	Call string
	// Text is the question; required.
	Text string
	// Choices are offered answers, none for free text.
	Choices []string
	// Wait blocks the call until the answer or the deadline; false returns
	// pending at once and lets the answer arrive later.
	Wait bool
	// Timeout is the deadline; zero is Config.Timeout.
	Timeout time.Duration
}

// By attributes an answer.
type By struct {
	Principal string `json:"principal"`
	Channel   string `json:"channel,omitempty"`
}

// Asked is question/asked.
type Asked struct {
	ID       string   `json:"id"`
	Agent    string   `json:"agent"`
	Call     string   `json:"call,omitempty"`
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
	Wait     bool     `json:"wait,omitempty"`
	Timeout  int64    `json:"timeout_ms"`
}

// Answered is question/answered: the question's one answer, or why it closed
// with none.
type Answered struct {
	ID      string  `json:"id"`
	Call    string  `json:"call,omitempty"`
	Outcome Outcome `json:"outcome"`
	Text    string  `json:"text,omitempty"`
	By      By      `json:"by"`
	// Message is the inbox message the answer arrived as, "" for none.
	Message string `json:"message,omitempty"`
	// ToCall reports that the waiting call returned the answer.
	ToCall bool `json:"to_call,omitempty"`
}

// The question events, log-only.
var (
	AskedType = event.MustDefine(event.Default(), event.Spec[Asked]{Name: "question/asked", Version: 1,
		Validate: func(a Asked) error {
			if a.ID == "" || a.Agent == "" || strings.TrimSpace(a.Question) == "" {
				return errors.New("question/asked: id, agent and question are required")
			}
			return nil
		}})
	AnsweredType = event.MustDefine(event.Default(), event.Spec[Answered]{Name: "question/answered", Version: 1,
		Validate: func(a Answered) error {
			switch {
			case a.ID == "" || a.By.Principal == "":
				return errors.New("question/answered: id and principal are required")
			case a.Outcome != OutcomeAnswered && a.Outcome != OutcomeDeclined && a.Outcome != OutcomeNoAnswerer:
				return fmt.Errorf("question/answered: outcome %q closes no question", a.Outcome)
			case a.Outcome == OutcomeAnswered && a.Text == "":
				return errors.New("question/answered: an answer has text")
			}
			return nil
		}})
)

// The errors.
var (
	// ErrReplyQueued refuses a second answer: the first is already queued.
	ErrReplyQueued = errors.New("question/reply-queued")
	// ErrUnknown is a reply to no question this service knows.
	ErrUnknown = errors.New("question/unknown")
	// ErrInvalid is a malformed question or reply.
	ErrInvalid = errors.New("question/invalid")
)

// PrincipalHarness closes a question nobody could answer.
const PrincipalHarness = "harness"

// CauseReturnedToCall is the discard cause of an answer the waiting call took.
const CauseReturnedToCall = "returned-to-call"

// Log is the asking agent's session.
type Log interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
}

// Inbox is the asking agent's inbox (*agent.Inbox). An answer's admission and
// its question/answered are one group (SendWith, Hand), so no crash separates
// them.
type Inbox interface {
	SendWith(ctx context.Context, list agent.List, m agent.InboxMessage, also ...event.Draft) (agent.Receipt, error)
	Hand(ctx context.Context, m agent.InboxMessage, cause string, also ...event.Draft) (agent.Receipt, error)
	Seen(id string) (agent.Receipt, bool)
}

// Prompt is the payload of the human.Item an answerer receives.
type Prompt struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
	Call     string   `json:"call,omitempty"`
}

// Result is what the asking call gets.
type Result struct {
	ID      string
	Outcome Outcome
	Text    string
	By      By
}

// Config configures a Service.
type Config struct {
	// Registry is where questions are asked; nil settles every ask no-answerer.
	Registry human.Registry
	// Timeout is the default deadline; zero is DefaultTimeout.
	Timeout time.Duration
	// After is the timer; nil is time.After.
	After func(time.Duration) <-chan time.Time
	// OnError hears a settlement that failed with no caller to return it to: an
	// answer or a close the registry produced for a call that moved on. The
	// question stays open; nil drops the report.
	OnError func(id string, err error)
}

// settlement is what a waiting call receives: its result, or why the
// settlement could not be written.
type settlement struct {
	r   Result
	err error
}

type open struct {
	q     Question
	log   Log
	inbox Inbox
	// cancel withdraws the registry interaction; nil for a restored question.
	cancel context.CancelFunc
	// waiting: a call blocks on result. Whoever clears it (a settlement, the
	// deadline, the caller's cancellation) sends result exactly one value.
	waiting bool
	// settling: a reply or a close is writing the settlement now.
	settling bool
	result   chan settlement
}

// Service asks questions and takes their answers.
type Service struct {
	cfg Config

	mu   sync.Mutex
	open map[string]*open
	done map[string]bool
}

// New is a Service over cfg.
func New(cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	return &Service{cfg: cfg, open: map[string]*open{}, done: map[string]bool{}}
}

// Ask appends question/asked and puts q to the agent's channel. With Wait it
// returns the answer, or pending at the deadline or when ctx ends; without, it
// returns pending at once. Either way a later answer arrives through inbox. An
// error with a result means the settlement could not be written: the question is
// still open.
func (s *Service) Ask(ctx context.Context, log Log, inbox Inbox, q Question) (Result, error) {
	if log == nil || inbox == nil || q.Agent.IsZero() || strings.TrimSpace(q.Text) == "" {
		return Result{}, fmt.Errorf("%w: a question needs its log, inbox, agent and text", ErrInvalid)
	}
	timeout := s.cfg.Timeout
	if q.Timeout > 0 {
		timeout = q.Timeout
	}
	id := mintID()
	d, err := AskedType.Draft(Asked{ID: id, Agent: q.Agent.String(), Call: q.Call, Question: q.Text, Choices: q.Choices, Wait: q.Wait, Timeout: timeout.Milliseconds()})
	if err == nil {
		_, err = log.Append(ctx, d)
	}
	if err != nil {
		return Result{}, fmt.Errorf("question: append question/asked: %w", err)
	}
	// The question outlives the asking call: the interaction ends at its
	// deadline or its settlement, never at the caller's cancellation.
	actx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	o := &open{q: q, log: log, inbox: inbox, cancel: cancel, waiting: q.Wait, result: make(chan settlement, 1)}
	s.mu.Lock()
	s.open[id] = o
	s.mu.Unlock()
	go s.interact(actx, id, o, timeout)
	if !q.Wait {
		return Result{ID: id, Outcome: OutcomePending}, nil
	}
	select {
	case st := <-o.result:
		return st.r, st.err
	case <-ctx.Done():
	}
	s.expire(id, o)
	st := <-o.result
	return st.r, st.err
}

// interact puts the question to the registry until the deadline or a
// settlement. An answer is settled through Reply; a refusal or nobody to ask
// closes the question; the deadline leaves it open for a late answer.
func (s *Service) interact(ctx context.Context, id string, o *open, timeout time.Duration) {
	defer o.cancel()
	defer s.expire(id, o)
	wctx := context.WithoutCancel(ctx)
	if s.cfg.Registry == nil {
		s.report(id, s.closeWith(wctx, id, OutcomeNoAnswerer, "no-registry", By{Principal: PrincipalHarness}))
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	timer := s.cfg.After(timeout)
	go func() {
		select {
		case <-timer:
			o.cancel()
		case <-stop:
		}
	}()
	ans, by, err := s.cfg.Registry.Ask(ctx, human.Item{ID: human.ID(id), Kind: human.KindQuestion, To: human.Audience{Agent: o.q.Agent},
		Session: o.q.Agent.Session, Call: o.q.Call, Payload: Prompt{Question: o.q.Text, Choices: o.q.Choices, Call: o.q.Call}})
	switch {
	case err != nil || ans.Outcome == human.OutcomeUnavailable:
		detail := ans.Detail
		if err != nil {
			detail = err.Error()
		}
		err = s.closeWith(wctx, id, OutcomeNoAnswerer, detail, By{Principal: PrincipalHarness})
	case ans.Outcome == human.OutcomeAnswered:
		if _, err = s.Reply(wctx, id, by, ans.Text); errors.Is(err, ErrReplyQueued) || errors.Is(err, ErrUnknown) {
			err = nil // another reply settled it first
		}
	case ans.Outcome == human.OutcomeDenied:
		err = s.closeWith(wctx, id, OutcomeDeclined, ans.Text, attributed(by))
	}
	s.report(id, err)
}

// expire returns pending to o's waiting call unless a settlement took it first
// (that settlement sends the call its result).
func (s *Service) expire(id string, o *open) {
	s.mu.Lock()
	release := o.waiting
	o.waiting = false
	s.mu.Unlock()
	if release {
		o.result <- settlement{r: Result{ID: id, Outcome: OutcomePending}}
	}
}

func (s *Service) report(id string, err error) {
	if err != nil && s.cfg.OnError != nil {
		s.cfg.OnError(id, err)
	}
}

// take starts settling id: one settlement at a time, and none after one was
// written. toCall reports that a call is waiting for this settlement.
func (s *Service) take(id string) (o *open, toCall bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.open[id]
	switch {
	case (ok && o.settling) || (!ok && s.done[id]):
		return nil, false, fmt.Errorf("%w: %s", ErrReplyQueued, id)
	case !ok:
		return nil, false, fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	o.settling = true
	toCall = o.waiting
	o.waiting = false
	return o, toCall, nil
}

// finish ends a settlement. written (the settlement is in the log) closes the
// question and withdraws the interaction; otherwise the question is open again.
// A waiting call gets r with err, or pending with err when nothing was written.
func (s *Service) finish(id string, o *open, toCall, written bool, r Result, err error) {
	s.mu.Lock()
	o.settling = false
	if written {
		delete(s.open, id)
		s.done[id] = true
	}
	s.mu.Unlock()
	if written && o.cancel != nil {
		o.cancel()
	}
	if toCall {
		if !written {
			r = Result{ID: id, Outcome: OutcomePending}
		}
		o.result <- settlement{r: r, err: err}
	}
}

// closeWith settles id with no answer, appending question/answered. A question
// settled already is left alone (nil).
func (s *Service) closeWith(ctx context.Context, id string, out Outcome, detail string, by By) error {
	o, toCall, err := s.take(id)
	if err != nil {
		return nil
	}
	d, err := AnsweredType.Draft(Answered{ID: id, Call: o.q.Call, Outcome: out, Text: detail, By: by})
	if err == nil {
		_, err = o.log.Append(ctx, d)
	}
	if err != nil {
		err = fmt.Errorf("question: append question/answered: %w", err)
	}
	s.finish(id, o, toCall, err == nil, Result{ID: id, Outcome: out, Text: detail, By: by}, err)
	return err
}

// answerID is the inbox id of question id's answer; deterministic, so the
// inbox refuses a second admission of it.
func answerID(id string) string { return "ans-" + id }

// redeliveredID is the inbox id of an answer delivered again after a crash took
// it from its call.
func redeliveredID(id string) string { return answerID(id) + "-again" }

// bracketed is a log that knows whether a turn is open (a session).
type bracketed interface{ Open() surface.Brackets }

// lateList is where an answer no call waits for goes: into the open turn at its
// next step, or, when the agent is idle, next-turn, which wakes the agent and
// starts a turn of its own.
func lateList(log Log) agent.List {
	if b, ok := log.(bracketed); ok && b.Open().TurnOpen {
		return agent.ListNextStep
	}
	return agent.ListNextTurn
}

// Reply answers the open question id with text, by an actor. The answer's inbox
// admission and its attributed question/answered are one durable group: a
// waiting call is handed the answer (admitted and discarded returned-to-call,
// and the interaction withdrawn); otherwise it stays queued, next-step into an
// open turn or next-turn to wake an idle agent. A question takes one answer: a
// second is ErrReplyQueued. On an error nothing was written unless the error says
// the flush failed, and the question is open for another answer.
func (s *Service) Reply(ctx context.Context, id string, by human.Actor, text string) (agent.Receipt, error) {
	if strings.TrimSpace(text) == "" {
		return agent.Receipt{}, fmt.Errorf("%w: an answer has text", ErrInvalid)
	}
	o, toCall, err := s.take(id)
	if err != nil {
		return agent.Receipt{}, err
	}
	who := attributed(by)
	msg := agent.InboxMessage{ID: answerID(id), Source: agent.SourceQuestion, By: who.Principal, Call: o.q.Call, Text: text}
	var rec agent.Receipt
	d, err := AnsweredType.Draft(Answered{ID: id, Call: o.q.Call, Outcome: OutcomeAnswered, Text: text, By: who, Message: msg.ID, ToCall: toCall})
	if err == nil {
		if toCall {
			rec, err = o.inbox.Hand(ctx, msg, CauseReturnedToCall, d)
		} else {
			rec, err = o.inbox.SendWith(ctx, lateList(o.log), msg, d)
		}
	}
	_, written := o.inbox.Seen(msg.ID)
	switch {
	case errors.Is(err, agent.ErrDuplicateMessage):
		err = fmt.Errorf("%w: %s", ErrReplyQueued, id)
	case err != nil:
		err = fmt.Errorf("question: settle the answer: %w", err)
	}
	s.finish(id, o, toCall, written, Result{ID: id, Outcome: OutcomeAnswered, Text: text, By: who}, err)
	if err != nil {
		return rec, err
	}
	rec.MessageID = msg.ID
	return rec, nil
}

// RestoreLog is the session Restore folds and later appends to.
type RestoreLog interface {
	Log
	Len() event.Offset
	Read(ctx context.Context, from event.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error)
}

// Restore reopens the questions of a resumed session: each question/asked with
// no question/answered is open again for a late Reply, answered into inbox, and
// each answered one refuses a second answer. An answer handed to a call the
// crash ended before its result (the call's only result is a closer, or none) is
// delivered again, next-turn, so the model still sees it; once, by id.
func (s *Service) Restore(ctx context.Context, log RestoreLog, inbox Inbox) ([]string, error) {
	asked := map[string]Asked{}
	var order []string
	var handed []Answered
	results := map[string]bool{} // call id -> a result the tool wrote
	const page = 256
	for from, n := event.Offset(0), log.Len(); from < n; from += page {
		evs, err := log.Read(ctx, from, page)
		if err != nil {
			return nil, err
		}
		for _, ev := range evs {
			switch ev.Type() {
			case AskedType.Name():
				if a, err := AskedType.As(ev); err == nil {
					asked[a.ID] = a
					order = append(order, a.ID)
				}
			case AnsweredType.Name():
				if a, err := AnsweredType.As(ev); err == nil {
					delete(asked, a.ID)
					s.mu.Lock()
					s.done[a.ID] = true
					s.mu.Unlock()
					if a.ToCall && a.Outcome == OutcomeAnswered && a.Call != "" {
						handed = append(handed, a)
					}
				}
			case event.ToolResult.Name():
				if p, err := event.ToolResult.As(ev); err == nil {
					if src, ok := p.Message.Source(); !ok || src.Kind != closerKind {
						results[p.CallID] = true
					}
				}
			}
		}
	}
	var reopened []string
	s.mu.Lock()
	for _, id := range order {
		a, ok := asked[id]
		if !ok || s.open[id] != nil {
			continue
		}
		if _, admitted := inbox.Seen(answerID(id)); admitted {
			s.done[id] = true // settled; its question/answered is in the same group
			continue
		}
		s.open[id] = &open{q: Question{Agent: parseRef(a.Agent), Call: a.Call, Text: a.Question, Choices: a.Choices}, log: log, inbox: inbox, result: make(chan settlement, 1)}
		reopened = append(reopened, id)
	}
	s.mu.Unlock()
	for _, a := range handed {
		if results[a.Call] {
			continue
		}
		m := agent.InboxMessage{ID: redeliveredID(a.ID), Source: agent.SourceQuestion, By: a.By.Principal, Call: a.Call, Text: a.Text}
		if _, sent := inbox.Seen(m.ID); sent {
			continue
		}
		if _, err := inbox.SendWith(ctx, agent.ListNextTurn, m); err != nil && !errors.Is(err, agent.ErrDuplicateMessage) {
			return reopened, fmt.Errorf("question: deliver %s again: %w", a.ID, err)
		}
	}
	return reopened, nil
}

// closerKind is the source kind of a result the session wrote for a call the
// crash left open (surface.Closers).
const closerKind = "closer"

// Open is the ids of the questions still waiting for an answer, sorted.
func (s *Service) Open() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.open))
	for id := range s.open {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// parseRef reads agent.Ref.String's "session#incarnation".
func parseRef(s string) agent.Ref {
	id, inc, _ := strings.Cut(s, "#")
	n, _ := strconv.ParseUint(inc, 10, 64)
	return agent.Ref{Session: session.ID(id), Incarnation: n}
}

func attributed(a human.Actor) By {
	p := a.Name
	if p == "" {
		p = string(a.Channel)
	}
	if p == "" {
		p = "unknown"
	}
	return By{Principal: p, Channel: string(a.Channel)}
}

func mintID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "qst-" + hex.EncodeToString(b[:])
}
