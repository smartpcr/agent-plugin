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

// Inbox is the asking agent's inbox (*agent.Inbox).
type Inbox interface {
	Send(ctx context.Context, list agent.List, m agent.InboxMessage) (agent.Receipt, error)
	Discard(ctx context.Context, list agent.List, id, cause string) (bool, error)
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
}

type open struct {
	q     Question
	log   Log
	inbox Inbox
	// waiting is where a waiting call takes its answer; nil once the call
	// has returned or never waited.
	waiting chan Result
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
// returns the answer, or pending at the deadline; without, it returns pending at
// once. Either way a later answer arrives through inbox.
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
	o := &open{q: q, log: log, inbox: inbox}
	if q.Wait {
		o.waiting = make(chan Result, 1)
	}
	s.mu.Lock()
	s.open[id] = o
	s.mu.Unlock()

	if !q.Wait {
		go s.ask(context.WithoutCancel(ctx), id, o, timeout)
		return Result{ID: id, Outcome: OutcomePending}, nil
	}
	ch := o.waiting
	if closed := s.ask(ctx, id, o, timeout); closed != nil {
		return *closed, nil
	}
	s.mu.Lock()
	_, still := s.open[id]
	if still {
		o.waiting = nil
	}
	s.mu.Unlock()
	if still {
		return Result{ID: id, Outcome: OutcomePending}, nil
	}
	// A reply took the question while the call still waited: it hands the
	// call its result.
	return <-ch, nil
}

// ask puts the question to the registry until the deadline. It returns the
// closing result when nobody can be asked or the person declined; an answer is
// taken through Reply; a deadline leaves the question open.
func (s *Service) ask(ctx context.Context, id string, o *open, timeout time.Duration) *Result {
	if s.cfg.Registry == nil {
		return s.close(ctx, id, OutcomeNoAnswerer, "no-registry", By{Principal: PrincipalHarness})
	}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	defer close(stop)
	timer := s.cfg.After(timeout)
	go func() {
		select {
		case <-timer:
			cancel()
		case <-stop:
		}
	}()
	ans, by, err := s.cfg.Registry.Ask(actx, human.Item{ID: human.ID(id), Kind: human.KindQuestion, To: human.Audience{Agent: o.q.Agent},
		Session: o.q.Agent.Session, Call: o.q.Call, Payload: Prompt{Question: o.q.Text, Choices: o.q.Choices, Call: o.q.Call}})
	switch {
	case err != nil || ans.Outcome == human.OutcomeUnavailable:
		detail := ans.Detail
		if err != nil {
			detail = err.Error()
		}
		return s.close(ctx, id, OutcomeNoAnswerer, detail, By{Principal: PrincipalHarness})
	case ans.Outcome == human.OutcomeAnswered:
		_, _ = s.Reply(ctx, id, by, ans.Text)
	case ans.Outcome == human.OutcomeDenied:
		return s.close(ctx, id, OutcomeDeclined, ans.Text, attributed(by))
	}
	return nil
}

// close settles id with no answer and appends question/answered; nil when id was
// settled already.
func (s *Service) close(ctx context.Context, id string, out Outcome, detail string, by By) *Result {
	s.mu.Lock()
	o, ok := s.open[id]
	if ok {
		delete(s.open, id)
		s.done[id] = true
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if d, err := AnsweredType.Draft(Answered{ID: id, Call: o.q.Call, Outcome: out, Text: detail, By: by}); err == nil {
		_, _ = o.log.Append(context.WithoutCancel(ctx), d)
	}
	return &Result{ID: id, Outcome: out, Text: detail, By: by}
}

// Reply answers the open question id with text, by an actor: the answer is sent
// into the asking agent's inbox as a user message naming the call, and
// question/answered is appended, attributed. A waiting call returns the answer
// and the message is discarded from the inbox (returned-to-call); otherwise it
// stays queued for the next boundary. A question takes one answer: a second is
// ErrReplyQueued.
func (s *Service) Reply(ctx context.Context, id string, by human.Actor, text string) (agent.Receipt, error) {
	if strings.TrimSpace(text) == "" {
		return agent.Receipt{}, fmt.Errorf("%w: an answer has text", ErrInvalid)
	}
	s.mu.Lock()
	o, ok := s.open[id]
	switch {
	case !ok && s.done[id]:
		s.mu.Unlock()
		return agent.Receipt{}, fmt.Errorf("%w: %s", ErrReplyQueued, id)
	case !ok:
		s.mu.Unlock()
		return agent.Receipt{}, fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	delete(s.open, id)
	s.done[id] = true
	waiting := o.waiting
	s.mu.Unlock()

	who := attributed(by)
	msg := agent.InboxMessage{ID: "ans-" + id, Source: agent.SourceQuestion, By: who.Principal, Call: o.q.Call, Text: text}
	rec, err := o.inbox.Send(ctx, agent.ListNextStep, msg)
	if err != nil {
		s.mu.Lock()
		o.waiting = nil
		s.open[id] = o
		delete(s.done, id)
		s.mu.Unlock()
		if waiting != nil {
			waiting <- Result{ID: id, Outcome: OutcomePending}
		}
		return agent.Receipt{}, fmt.Errorf("question: send the answer: %w", err)
	}
	if waiting != nil {
		_, _ = o.inbox.Discard(ctx, agent.ListNextStep, msg.ID, CauseReturnedToCall)
	}
	d, err := AnsweredType.Draft(Answered{ID: id, Call: o.q.Call, Outcome: OutcomeAnswered, Text: text, By: who, Message: msg.ID, ToCall: waiting != nil})
	if err == nil {
		_, err = o.log.Append(context.WithoutCancel(ctx), d)
	}
	if waiting != nil {
		waiting <- Result{ID: id, Outcome: OutcomeAnswered, Text: text, By: who}
	}
	if err != nil {
		return rec, fmt.Errorf("question: append question/answered: %w", err)
	}
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
// each answered one refuses a second answer.
func (s *Service) Restore(ctx context.Context, log RestoreLog, inbox Inbox) ([]string, error) {
	asked := map[string]Asked{}
	var order []string
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
				}
			}
		}
	}
	var reopened []string
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range order {
		a, ok := asked[id]
		if !ok || s.open[id] != nil {
			continue
		}
		s.open[id] = &open{q: Question{Agent: parseRef(a.Agent), Call: a.Call, Text: a.Question, Choices: a.Choices}, log: log, inbox: inbox}
		reopened = append(reopened, id)
	}
	return reopened, nil
}

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
