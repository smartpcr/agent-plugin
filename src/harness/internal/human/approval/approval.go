// Package approval is the approval service of the interaction area
// (DH-D-HUMAN-2). One request for permission to run one tool call returns exactly
// one closed Outcome: approved, denied, timeout or no-answerer. It also leaves an
// attributed audit pair in the session log: approval/asked before anyone is
// asked, and approval/decided once the request settles. Nobody to ask means no
// (SP-7). An unbound agent, a channel with no answerer, a stopping host, a failed
// or panicking answerer and a missing registry all settle no-answerer, which every
// caller treats as a denial.
//
// The request is asked through the interaction registry (human.Registry), so the
// answer comes from whichever channel the agent is bound to. This package only
// bounds the wait, maps the registry's outcomes onto the closed set and writes the
// audit events. A decision covers one call only and never grants the session
// anything.
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Outcome is the closed result of one approval request.
type Outcome string

// The outcomes. Only OutcomeApproved lets the call run.
const (
	OutcomeApproved   Outcome = "approved"
	OutcomeDenied     Outcome = "denied"
	OutcomeTimeout    Outcome = "timeout"
	OutcomeNoAnswerer Outcome = "no-answerer"
)

// Valid reports whether o is one of the closed outcomes.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeApproved, OutcomeDenied, OutcomeTimeout, OutcomeNoAnswerer:
		return true
	}
	return false
}

// DefaultTimeout bounds the wait for an answer when Config.Timeout is zero.
const DefaultTimeout = 5 * time.Minute

// PrincipalHarness is the principal of a decision that nobody answered (a
// timeout, a missing answerer, a cancellation): the harness settled it, not a
// person.
const PrincipalHarness = "harness"

// Request asks permission for one tool call.
type Request struct {
	// Agent is the agent whose call it is; required.
	Agent agent.Ref
	// Tool is the tool's name; required.
	Tool string
	// Call is the tool call's id; "" for an approval that belongs to no call.
	Call string
	// Parent is the tool call that started this one, "" at top level.
	Parent string
	// Origin is who asked for the call: model, user, plugin or nested.
	Origin string
	// Reason is the policy's reason for asking.
	Reason string
	// Display is what the answerer shows: the call's arguments or a summary of
	// them. It is never written back into the call.
	Display string
	// Timeout overrides Config.Timeout for this request when positive.
	Timeout time.Duration
}

// Prompt is the payload of the human.Item an answerer receives.
type Prompt struct {
	Tool    string `json:"tool"`
	Call    string `json:"call,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Display string `json:"display,omitempty"`
}

// By attributes a decision to the principal who made it and the channel it came
// through. A decision nobody answered is by PrincipalHarness on no channel.
type By struct {
	Principal string `json:"principal"`
	Channel   string `json:"channel,omitempty"`
}

// Decision is one settled request.
type Decision struct {
	// ID is the interaction id that its approval/asked and approval/decided share.
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
	// Detail explains any outcome other than approved.
	Detail string `json:"detail,omitempty"`
	By     By     `json:"by"`
}

// Approved reports whether the call may run.
func (d Decision) Approved() bool { return d.Outcome == OutcomeApproved }

// Asked is approval/asked: a request is about to be put to the agent's channel.
type Asked struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Tool    string `json:"tool"`
	Call    string `json:"call,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Timeout int64  `json:"timeout_ms"`
}

// Decided is approval/decided: the request settled with one closed outcome.
type Decided struct {
	ID      string  `json:"id"`
	Tool    string  `json:"tool"`
	Call    string  `json:"call,omitempty"`
	Outcome Outcome `json:"outcome"`
	Detail  string  `json:"detail,omitempty"`
	By      By      `json:"by"`
}

func validAsked(a Asked) error {
	if a.ID == "" || a.Agent == "" || a.Tool == "" {
		return errors.New("approval/asked: id, agent and tool are required")
	}
	return nil
}

func validDecided(d Decided) error {
	if d.ID == "" || d.Tool == "" {
		return errors.New("approval/decided: id and tool are required")
	}
	if !d.Outcome.Valid() {
		return fmt.Errorf("approval/decided: outcome %q is not in the closed set", d.Outcome)
	}
	if d.By.Principal == "" {
		return errors.New("approval/decided: a decision must name its principal")
	}
	return nil
}

// The durable, log-only audit events of the service.
var (
	AskedType   = event.MustDefine(event.Default(), event.Spec[Asked]{Name: "approval/asked", Version: 1, Validate: validAsked})
	DecidedType = event.MustDefine(event.Default(), event.Spec[Decided]{Name: "approval/decided", Version: 1, Validate: validDecided})
)

// Log is where the audit events are appended: the session of the call.
type Log interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
}

// Config configures a Service.
type Config struct {
	// Registry is where requests are asked; nil settles every request
	// no-answerer.
	Registry human.Registry
	// Timeout bounds the wait for an answer; zero is DefaultTimeout.
	Timeout time.Duration
	// After is the timer; nil is time.After. Tests pass a manual clock's.
	After func(time.Duration) <-chan time.Time
}

// Service asks for approvals.
type Service struct{ cfg Config }

// Key is the service a composition mounts its approval service under; the tool
// pipeline asks through it, and fails closed when none is mounted.
var Key = cordis.NewServiceKey[*Service]("human.approval")

// New returns a Service over cfg.
func New(cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	return &Service{cfg: cfg}
}

// errTimedOut is the cause the service cancels an unanswered request with.
var errTimedOut = errors.New("approval: no answer within the timeout")

// Request asks for r and returns its decision. The approval/asked event is
// appended to log before anyone is asked, and approval/decided after the request
// settles; both are attributed. An error means the audit could not be written or
// r is malformed. The call must then be refused, exactly as for a denial, and the
// returned Decision says so (its outcome is never approved).
func (s *Service) Request(ctx context.Context, log Log, r Request) (Decision, error) {
	if log == nil {
		return refuse("", "no-log"), errors.New("approval: a request needs the log its audit is written to")
	}
	if r.Agent.IsZero() || r.Tool == "" {
		return refuse("", "invalid"), errors.New("approval: a request needs its agent and tool")
	}
	timeout := s.cfg.Timeout
	if r.Timeout > 0 {
		timeout = r.Timeout
	}
	id := mintID()
	asked, err := AskedType.Draft(Asked{ID: id, Agent: r.Agent.String(), Tool: r.Tool, Call: r.Call, Parent: r.Parent,
		Origin: r.Origin, Reason: r.Reason, Timeout: timeout.Milliseconds()})
	if err == nil {
		_, err = log.Append(ctx, asked)
	}
	if err != nil {
		return refuse(id, "audit-failed"), fmt.Errorf("approval: append approval/asked: %w", err)
	}

	d := s.ask(ctx, id, r, timeout)
	decided, err := DecidedType.Draft(Decided{ID: d.ID, Tool: r.Tool, Call: r.Call, Outcome: d.Outcome, Detail: d.Detail, By: d.By})
	if err == nil {
		// Write the decision even when ctx has ended, so the caller's
		// cancellation never leaves an audit pair open.
		_, err = log.Append(context.WithoutCancel(ctx), decided)
	}
	if err != nil {
		return refuse(id, "audit-failed"), fmt.Errorf("approval: append approval/decided: %w", err)
	}
	return d, nil
}

// refuse is the decision returned with an error: never approved.
func refuse(id, detail string) Decision {
	return Decision{ID: id, Outcome: OutcomeDenied, Detail: detail, By: By{Principal: PrincipalHarness}}
}

// ask puts r to the registry and maps its answer onto the closed outcomes.
func (s *Service) ask(ctx context.Context, id string, r Request, timeout time.Duration) (d Decision) {
	d = Decision{ID: id, By: By{Principal: PrincipalHarness}}
	if s.cfg.Registry == nil {
		d.Outcome, d.Detail = OutcomeNoAnswerer, "no-registry"
		return d
	}
	defer func() {
		if p := recover(); p != nil {
			d = Decision{ID: id, Outcome: OutcomeNoAnswerer, Detail: fmt.Sprintf("answerer-failed: %v", p), By: By{Principal: PrincipalHarness}}
		}
	}()

	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	defer close(stop)
	timer := s.cfg.After(timeout)
	go func() {
		select {
		case <-timer:
			cancel(errTimedOut)
		case <-stop:
		}
	}()

	ans, by, err := s.cfg.Registry.Ask(actx, human.Item{
		ID:      human.ID(id),
		Kind:    human.KindApproval,
		To:      human.Audience{Agent: r.Agent},
		Session: r.Agent.Session,
		Call:    r.Call,
		Payload: Prompt{Tool: r.Tool, Call: r.Call, Parent: r.Parent, Origin: r.Origin, Reason: r.Reason, Display: r.Display},
	})
	if err != nil {
		d.Outcome, d.Detail = OutcomeNoAnswerer, err.Error()
		return d
	}
	switch ans.Outcome {
	case human.OutcomeAllowedOnce, human.OutcomeAllowedSession:
		// Granting the session is not this package's job, so the call is
		// approved once.
		d.Outcome = OutcomeApproved
		d.By = attributed(by)
	case human.OutcomeDenied:
		d.Outcome, d.Detail = OutcomeDenied, ans.Text
		d.By = attributed(by)
	case human.OutcomeCancelled:
		if errors.Is(context.Cause(actx), errTimedOut) {
			d.Outcome, d.Detail = OutcomeTimeout, timeout.String()
		} else {
			d.Outcome, d.Detail = OutcomeDenied, "cancelled: "+ans.Detail
		}
	case human.OutcomeUnavailable:
		d.Outcome, d.Detail = OutcomeNoAnswerer, ans.Detail
	default:
		d.Outcome, d.Detail = OutcomeDenied, "unexpected answer "+string(ans.Outcome)
	}
	return d
}

// attributed names the person who answered: the actor's name, or the channel
// itself when the actor gave no name.
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
	return "apr-" + hex.EncodeToString(b[:])
}
