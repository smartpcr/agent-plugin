// Package approval is the approval service of the interaction area
// (DH-D-HUMAN-2). One request for permission to run one tool call returns exactly
// one closed Outcome (M1-D-PIPE-1 A): allowed-once, allowed-by-grant, rejected,
// cancelled or unavailable. It also leaves an attributed audit pair in the
// session log: approval/asked before anything else is decided, and
// approval/decided once the request settles, naming the principal, the channel
// and the peer that decided, and the grant used or created. Nobody to ask means
// no (SP-7): an unbound agent, a channel with no answerer, a stopping host, a
// failed or panicking answerer, a set timeout that ran out and a missing registry
// all settle unavailable, which every caller treats as a refusal.
//
// The order of one request is fixed: append approval/asked, check the session's
// approval policy (policy.go; under never the request is rejected at once and
// nobody is asked), match the session's grants (grants.go), ask the interaction
// registry (human.Registry), append approval/decided. This package bounds the
// wait, maps the registry's answers onto the closed set and writes the audit
// events. A decision covers one call; a session grant is created only from an
// allowed-session answer to a request naming a subject. A host that dies with a
// request open leaves approval/asked without its decision; the resume closer
// (resume.go) writes the missing decision before the session accepts work.
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Outcome is the closed result of one approval request.
type Outcome string

// The outcomes (interaction-credentials.md section 6.2). Only the two allowed
// outcomes let the call run.
const (
	// OutcomeAllowedOnce: a person or an automation client allowed this call.
	OutcomeAllowedOnce Outcome = "allowed-once"
	// OutcomeAllowedByGrant: a session grant in force covers the request's
	// subject; nobody was asked.
	OutcomeAllowedByGrant Outcome = "allowed-by-grant"
	// OutcomeRejected: a person said no, or the session's policy is never.
	OutcomeRejected Outcome = "rejected"
	// OutcomeCancelled: the request's context ended before it settled.
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeUnavailable: nobody could answer: no binding, no answerer, a failed
	// or panicking answerer, a set timeout, a stopping host, or an audit failure.
	// Detail says which.
	OutcomeUnavailable Outcome = "unavailable"
)

// Valid reports whether o is one of the closed outcomes.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeAllowedOnce, OutcomeAllowedByGrant, OutcomeRejected, OutcomeCancelled, OutcomeUnavailable:
		return true
	}
	return false
}

// Allowed reports whether o lets the call run.
func (o Outcome) Allowed() bool { return o == OutcomeAllowedOnce || o == OutcomeAllowedByGrant }

// DefaultTimeout is the wait for an answer when neither Config.Timeout nor
// Request.Timeout is set: zero, which waits until the request is answered or its
// context ends (M1-D-PIPE-1 A). A set timeout settles unavailable with detail
// timeout.
const DefaultTimeout time.Duration = 0

// PrincipalHarness is the principal of a decision that nobody answered (a
// timeout, a missing answerer, a cancellation, the policy never, a resume
// closer): the harness settled it, not a person.
const PrincipalHarness = "harness"

// Kind is what a request asks for.
type Kind string

// The request kinds: an approval of a tool call, or consent to a change to the
// user's own machine. A consent is never granted for the session.
const (
	KindApproval Kind = "approval"
	KindConsent  Kind = "consent"
)

// Request asks permission for one tool call.
type Request struct {
	// Agent is the agent whose call it is; required.
	Agent agent.Ref
	// Kind is approval (the default) or consent.
	Kind Kind
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
	// Subject is what the call needs (one directory, one application), nil
	// for none. A live session grant covering it approves the call without
	// asking; an allowed-session answer grants it. No subject never matches.
	Subject *Subject
	// Mode is the sandbox mode an escalation asks for, "" for none.
	Mode string
}

// Prompt is the payload of the human.Item an answerer receives.
type Prompt struct {
	Kind    Kind   `json:"kind,omitempty"`
	Tool    string `json:"tool"`
	Call    string `json:"call,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Display string `json:"display,omitempty"`
	Mode    string `json:"mode,omitempty"`
	// Subject is what an allowed-session answer would grant, nil for none.
	Subject *Subject `json:"subject,omitempty"`
}

// By attributes a decision to the principal who made it, the channel it came
// through and the peer (connection) on that channel. A decision nobody answered
// is by PrincipalHarness on no channel and no peer.
type By struct {
	Principal string `json:"principal"`
	Channel   string `json:"channel,omitempty"`
	Peer      string `json:"peer,omitempty"`
}

// Decision is one settled request.
type Decision struct {
	// ID is the interaction id that its approval/asked and approval/decided share.
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
	// Detail explains the outcome: why it was refused or unavailable, or which
	// grant was used, created or refused.
	Detail string `json:"detail,omitempty"`
	// Grant is the id of the grant this decision used (allowed-by-grant) or
	// created (an allowed-session answer), "" for none.
	Grant string `json:"grant,omitempty"`
	By    By     `json:"by"`
	// session is an allowed-session answer, for the grant path.
	session bool
}

// The details the service writes itself.
const (
	// DetailAllowedByGrant prefixes the detail of a decision a session grant made.
	DetailAllowedByGrant = "allowed-by-grant"
	// DetailGranted prefixes the detail of an allowed-session decision that
	// created a grant.
	DetailGranted = "granted"
	// DetailGrantRefused prefixes the detail of an allowed-session decision whose
	// grant a rule refused (channel, subject, cap): the call is approved once.
	DetailGrantRefused = "grant-refused"
	// DetailTimeout is the detail of a request a set timeout ended.
	DetailTimeout = "timeout"
	// DetailPolicyNever is the detail of a request the policy never rejected.
	DetailPolicyNever = "policy-never"
	// DetailHostStopped is the detail of the decision the resume closer writes
	// for a request its host died with.
	DetailHostStopped = "host-stopped"
)

// Approved reports whether the call may run.
func (d Decision) Approved() bool { return d.Outcome.Allowed() }

// Asked is approval/asked: a request is about to be decided. Version 2 adds the
// kind, the subject and the sandbox mode; a version-1 record is an approval with
// neither.
type Asked struct {
	ID      string   `json:"id"`
	Kind    Kind     `json:"kind"`
	Agent   string   `json:"agent"`
	Tool    string   `json:"tool"`
	Call    string   `json:"call,omitempty"`
	Parent  string   `json:"parent,omitempty"`
	Origin  string   `json:"origin,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Subject *Subject `json:"subject,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	// Timeout is the wait in milliseconds; 0 waits until answered or cancelled.
	Timeout int64 `json:"timeout_ms"`
}

// Decided is approval/decided: the request settled with one closed outcome.
// Version 2 uses the M1-D-PIPE-1 outcomes, names the peer in by, and the grant
// used or created.
type Decided struct {
	ID      string  `json:"id"`
	Tool    string  `json:"tool"`
	Call    string  `json:"call,omitempty"`
	Outcome Outcome `json:"outcome"`
	Detail  string  `json:"detail,omitempty"`
	Grant   string  `json:"grant,omitempty"`
	By      By      `json:"by"`
}

func validAsked(a Asked) error {
	if a.ID == "" || a.Agent == "" || a.Tool == "" {
		return errors.New("approval/asked: id, agent and tool are required")
	}
	if a.Kind != KindApproval && a.Kind != KindConsent {
		return fmt.Errorf("approval/asked: kind %q is not approval or consent", a.Kind)
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

// The durable, log-only audit events of the service. Version 2 of both is the
// M1-D-PIPE-1 format (plan 16.1.2, Rule 6); the upgrades read a version-1 record.
var (
	AskedType = event.MustDefine(event.Default(), event.Spec[Asked]{Name: "approval/asked", Version: 2,
		Upgrades: map[int]event.Upgrade{1: upgradeAskedV1}, Validate: validAsked})
	DecidedType = event.MustDefine(event.Default(), event.Spec[Decided]{Name: "approval/decided", Version: 2,
		Upgrades: map[int]event.Upgrade{1: upgradeDecidedV1}, Validate: validDecided})
)

// upgradeAskedV1 reads a version-1 approval/asked: it had no kind (every request
// was an approval), no subject and no mode.
func upgradeAskedV1(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if _, ok := m["kind"]; !ok {
		m["kind"] = json.RawMessage(`"` + string(KindApproval) + `"`)
	}
	return json.Marshal(m)
}

// upgradeDecidedV1 reads a version-1 approval/decided into the M1-D-PIPE-1
// vocabulary: approved is allowed-by-grant when a grant decided it (detail
// "allowed-by-grant:<id>") and allowed-once otherwise; denied is cancelled when
// the caller's context ended (detail "cancelled: ...") and rejected otherwise,
// except the refusals the harness made itself (an audit failure, an unexpected
// answer), which are unavailable; timeout is unavailable with detail timeout;
// no-answerer is unavailable. A grant id in the detail becomes the grant field.
func upgradeDecidedV1(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	var outcome, detail string
	if v, ok := m["outcome"]; ok {
		if err := json.Unmarshal(v, &outcome); err != nil {
			return nil, fmt.Errorf("outcome: %w", err)
		}
	}
	if v, ok := m["detail"]; ok {
		if err := json.Unmarshal(v, &detail); err != nil {
			return nil, fmt.Errorf("detail: %w", err)
		}
	}
	next, grant := outcome, ""
	switch outcome {
	case "approved":
		next = string(OutcomeAllowedOnce)
		if id, ok := strings.CutPrefix(detail, DetailAllowedByGrant+":"); ok {
			next, grant = string(OutcomeAllowedByGrant), id
		} else if id, ok := strings.CutPrefix(detail, DetailGranted+":"); ok {
			grant = id
		}
	case "denied":
		switch {
		case strings.HasPrefix(detail, "cancelled"):
			next = string(OutcomeCancelled)
		case detail == "grant-audit-failed" || detail == "audit-failed" || strings.HasPrefix(detail, "unexpected answer"):
			next = string(OutcomeUnavailable)
		default:
			next = string(OutcomeRejected)
		}
	case "timeout":
		next, detail = string(OutcomeUnavailable), DetailTimeout
	case "no-answerer":
		next = string(OutcomeUnavailable)
	}
	enc := func(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
	m["outcome"] = enc(next)
	if detail != "" {
		m["detail"] = enc(detail)
	}
	if grant != "" {
		m["grant"] = enc(grant)
	}
	return json.Marshal(m)
}

// Log is where the audit events are appended: the session of the call.
type Log interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
}

// Config configures a Service.
type Config struct {
	// Registry is where requests are asked; nil settles every request
	// unavailable.
	Registry human.Registry
	// Timeout bounds the wait for an answer; zero (DefaultTimeout) waits.
	Timeout time.Duration
	// After is the timer; nil is time.After. Tests pass a manual clock's.
	After func(time.Duration) <-chan time.Time
	// Grants is the session grant table; nil disables grants, so an
	// allowed-session answer approves once.
	Grants *Grants
	// Policy is the approval policy of a session that never set one; zero is
	// PolicyAsk.
	Policy Policy
	// Sessions finds the live session SetPolicy writes approval/policy to; nil
	// uses the sessions Bind was given.
	Sessions Sessions
}

// Service asks for approvals.
type Service struct {
	cfg Config

	// sessions is Config.Sessions, or the ones Bind was given.
	sessions atomic.Pointer[Sessions]
	// setMu serialises SetPolicy, so an append and its table change are one step.
	setMu sync.Mutex
	// mu guards policies: each session's policy, set by SetPolicy or restored on
	// resume from the log's last approval/policy. A session absent from it has
	// Config.Policy.
	mu       sync.Mutex
	policies map[session.ID]Policy
}

// Key is the service a composition mounts its approval service under; the tool
// pipeline asks through it, and fails closed when none is mounted.
var Key = cordis.NewServiceKey[*Service]("human.approval")

// New returns a Service over cfg.
func New(cfg Config) *Service {
	if cfg.Timeout < 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.Policy == "" {
		cfg.Policy = PolicyAsk
	}
	s := &Service{cfg: cfg, policies: map[session.ID]Policy{}}
	if cfg.Sessions != nil {
		s.sessions.Store(&cfg.Sessions)
	}
	return s
}

// errTimedOut is the cause the service cancels an unanswered request with.
var errTimedOut = errors.New("approval: no answer within the timeout")

// Request asks for r and returns its decision. The approval/asked event is
// appended to log first; then the session's policy is checked (never rejects at
// once, with no delivery and no grant lookup), then the session's grants, then
// the agent's bound answerer is asked; approval/decided is appended once the
// request settles. Both are attributed. An error means the audit could not be
// written or r is malformed. The call must then be refused, exactly as for a
// rejection, and the returned Decision says so (its outcome is never allowed).
func (s *Service) Request(ctx context.Context, log Log, r Request) (Decision, error) {
	if log == nil {
		return refuse("", "no-log"), errors.New("approval: a request needs the log its audit is written to")
	}
	if r.Agent.IsZero() || r.Tool == "" {
		return refuse("", "invalid"), errors.New("approval: a request needs its agent and tool")
	}
	if r.Kind == "" {
		r.Kind = KindApproval
	}
	if r.Kind != KindApproval && r.Kind != KindConsent {
		return refuse("", "invalid"), fmt.Errorf("approval: kind %q is not approval or consent", r.Kind)
	}
	timeout := s.cfg.Timeout
	if r.Timeout > 0 {
		timeout = r.Timeout
	}
	id := mintID()
	asked, err := AskedType.Draft(Asked{ID: id, Kind: r.Kind, Agent: r.Agent.String(), Tool: r.Tool, Call: r.Call, Parent: r.Parent,
		Origin: r.Origin, Reason: r.Reason, Subject: r.Subject, Mode: r.Mode, Timeout: timeout.Milliseconds()})
	if err == nil {
		_, err = log.Append(ctx, asked)
	}
	if err != nil {
		return refuse(id, "audit-failed"), fmt.Errorf("approval: append approval/asked: %w", err)
	}

	d, grantErr := s.decide(ctx, log, id, r, timeout)
	decided, err := DecidedType.Draft(Decided{ID: d.ID, Tool: r.Tool, Call: r.Call, Outcome: d.Outcome, Detail: d.Detail, Grant: d.Grant, By: d.By})
	if err == nil {
		// Write the decision even when ctx has ended, so the caller's
		// cancellation never leaves an audit pair open.
		_, err = log.Append(context.WithoutCancel(ctx), decided)
	}
	if err != nil {
		return refuse(id, "audit-failed"), errors.Join(grantErr, fmt.Errorf("approval: append approval/decided: %w", err))
	}
	if grantErr != nil {
		return d, grantErr
	}
	return d, nil
}

// decide is the policy, grant and answer steps of one request whose
// approval/asked is logged. A panic in any of them is unavailable.
func (s *Service) decide(ctx context.Context, log Log, id string, r Request, timeout time.Duration) (d Decision, grantErr error) {
	defer func() {
		if p := recover(); p != nil {
			d, grantErr = Decision{ID: id, Outcome: OutcomeUnavailable, Detail: fmt.Sprintf("approval-failed: %v", p), By: By{Principal: PrincipalHarness}}, nil
		}
	}()
	// 1. The policy, before any grant or person: never asks nobody.
	if s.Policy(r.Agent.Session) == PolicyNever {
		return Decision{ID: id, Outcome: OutcomeRejected, Detail: DetailPolicyNever, By: By{Principal: PrincipalHarness}}, nil
	}
	// 2. A grant in force covers the subject: nobody is asked. A consent is never
	// granted; a request with no subject matches no grant.
	consult := r.Kind == KindApproval && s.cfg.Grants != nil
	grantable := consult && r.Subject != nil
	if consult {
		if gr, ok := s.cfg.Grants.Match(r.Agent, r.Subject); ok {
			return Decision{ID: id, Outcome: OutcomeAllowedByGrant, Detail: DetailAllowedByGrant + ":" + gr.ID, Grant: gr.ID, By: gr.By}, nil
		}
	}
	// 3. The agent's bound answerer.
	d = s.ask(ctx, id, r, timeout)
	if d.session && grantable {
		gr, err := s.cfg.Grants.create(context.WithoutCancel(ctx), log, r.Agent, *r.Subject, d.By, r.Tool, r.Call)
		switch {
		case err == nil:
			d.Detail, d.Grant = DetailGranted+":"+gr.ID, gr.ID
		case errors.Is(err, ErrGrantRefused) || errors.Is(err, ErrGrantCap):
			// The grant is refused by rule (channel, subject, cap); the
			// person still allowed this call, once.
			d.Detail = DetailGrantRefused + ":" + err.Error()
		default:
			// The grant could not be recorded: the call is refused, as for
			// any audit failure, and the decision says why.
			return refuse(id, "grant-audit-failed"), err
		}
	}
	return d, nil
}

// refuse is the decision returned with an error: never allowed.
func refuse(id, detail string) Decision {
	return Decision{ID: id, Outcome: OutcomeUnavailable, Detail: detail, By: By{Principal: PrincipalHarness}}
}

// ask puts r to the registry and maps its answer onto the closed outcomes.
func (s *Service) ask(ctx context.Context, id string, r Request, timeout time.Duration) (d Decision) {
	d = Decision{ID: id, By: By{Principal: PrincipalHarness}}
	if s.cfg.Registry == nil {
		d.Outcome, d.Detail = OutcomeUnavailable, "no-registry"
		return d
	}
	defer func() {
		if p := recover(); p != nil {
			d = Decision{ID: id, Outcome: OutcomeUnavailable, Detail: fmt.Sprintf("answerer-failed: %v", p), By: By{Principal: PrincipalHarness}}
		}
	}()

	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if timeout > 0 {
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
	}

	ans, by, err := s.cfg.Registry.Ask(actx, human.Item{
		ID:      human.ID(id),
		Kind:    human.KindApproval,
		To:      human.Audience{Agent: r.Agent},
		Session: r.Agent.Session,
		Call:    r.Call,
		Payload: Prompt{Kind: r.Kind, Tool: r.Tool, Call: r.Call, Parent: r.Parent, Origin: r.Origin, Reason: r.Reason, Display: r.Display,
			Mode: r.Mode, Subject: r.Subject},
	})
	if err != nil {
		d.Outcome, d.Detail = OutcomeUnavailable, err.Error()
		return d
	}
	switch ans.Outcome {
	case human.OutcomeAllowedOnce, human.OutcomeAllowedSession:
		// An allowed-session answer allows this call; Request turns it into a
		// session grant when the request names a subject.
		d.Outcome = OutcomeAllowedOnce
		d.By = attributed(by)
		d.session = ans.Outcome == human.OutcomeAllowedSession
	case human.OutcomeDenied:
		d.Outcome, d.Detail = OutcomeRejected, ans.Text
		d.By = attributed(by)
	case human.OutcomeCancelled:
		if errors.Is(context.Cause(actx), errTimedOut) {
			d.Outcome, d.Detail = OutcomeUnavailable, DetailTimeout
		} else {
			d.Outcome, d.Detail = OutcomeCancelled, ans.Detail
		}
	case human.OutcomeUnavailable:
		d.Outcome, d.Detail = OutcomeUnavailable, ans.Detail
	default:
		d.Outcome, d.Detail = OutcomeUnavailable, "unexpected answer "+string(ans.Outcome)
	}
	return d
}

// attributed names the person who answered: the actor's name, or the channel
// itself when the actor gave no name, with the channel and the peer. An actor
// with no peer answered in process.
func attributed(a human.Actor) By {
	p := a.Name
	if p == "" {
		p = string(a.Channel)
	}
	if p == "" {
		p = "unknown"
	}
	peer := a.Peer
	if peer == "" {
		peer = human.PeerInProcess
	}
	return By{Principal: p, Channel: string(a.Channel), Peer: string(peer)}
}

func mintID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "apr-" + hex.EncodeToString(b[:])
}
