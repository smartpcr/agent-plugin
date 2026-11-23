package approval

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// Policy is a session's approval policy (DH-D-HUMAN-2, M1-G-PIPE-3): whether a
// request that needs approval is put to a person at all.
type Policy string

// The policies.
const (
	// PolicyAsk puts each request to the agent's bound answerer, after grants.
	PolicyAsk Policy = "ask"
	// PolicyNever rejects each request at once: nobody is asked and no grant is
	// consulted. An unattended run uses it to be told "never ask".
	PolicyNever Policy = "never"
)

// Valid reports whether p is ask or never.
func (p Policy) Valid() bool { return p == PolicyAsk || p == PolicyNever }

// PolicySet is approval/policy: the session's policy became Policy, set by By.
type PolicySet struct {
	Policy Policy `json:"policy"`
	By     By     `json:"by"`
}

// PolicyType is the durable, log-only approval/policy event.
var PolicyType = event.MustDefine(event.Default(), event.Spec[PolicySet]{Name: "approval/policy", Version: 1,
	Validate: func(p PolicySet) error {
		if !p.Policy.Valid() {
			return fmt.Errorf("approval/policy: policy %q is not ask or never", p.Policy)
		}
		if p.By.Principal == "" {
			return errors.New("approval/policy: a switch must name its principal")
		}
		return nil
	}})

// PolicyKey is the key of the approval fold.
const PolicyKey = "approval"

// PolicyState is the approval fold of one session: the policy its last
// approval/policy set and who set it. An empty Policy means the session never
// set one, so the configured default applies.
type PolicyState struct {
	Policy Policy `json:"policy,omitempty"`
	By     By     `json:"by,omitzero"`
}

// PolicyView is the approval fold (session-views registration, DH-D-VIEW-1): the
// last approval/policy of the log. The service's Policy reads the same fold,
// applied to the log at resume and to each switch it makes.
func PolicyView() views.Definition[PolicyState] {
	return views.Definition[PolicyState]{
		Key: PolicyKey, Version: 1, Stored: true, Types: []string{PolicyType.Name()},
		Init: func(session.Header) PolicyState { return PolicyState{} },
		Apply: func(s PolicyState, ev event.Event) (PolicyState, bool) {
			if ev.Type() != PolicyType.Name() {
				return s, false
			}
			p, err := PolicyType.As(ev)
			if err != nil {
				return s, false
			}
			return PolicyState{Policy: p.Policy, By: p.By}, true
		},
	}
}

// RegisterView registers the approval fold in r.
func RegisterView(r *views.Registry) (views.Handle[PolicyState], error) {
	return views.Register(r, PolicyView())
}

// Policy is the approval policy of session id: the one its last approval/policy
// set (this service's own switch, or the log's, read back at resume), else
// Config.Policy.
func (s *Service) Policy(id session.ID) Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.policies[id]; ok {
		return p
	}
	return s.cfg.Policy
}

// restorePolicy takes the fold's state for session id, as read from its log.
func (s *Service) restorePolicy(id session.ID, st PolicyState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.Policy.Valid() {
		s.policies[id] = st.Policy
	} else {
		delete(s.policies, id)
	}
}

func (s *Service) forgetPolicy(id session.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.policies, id)
}

// ErrNoSession is SetPolicy's error when the agent's session is not live here.
var ErrNoSession = errors.New("approval/no-session")

// SetPolicy switches the approval policy of ag's session to p, by by. A switch
// to the policy in force writes nothing. Otherwise approval/policy is appended to
// the session first (an append that fails changes nothing), the policy is in
// force for the next request, and every grant of ag ends with policy-changed, so
// a tightened session keeps no standing yes (DH-ADR-HUMAN-6); a grant whose end
// cannot be written is still forgotten and the error returned.
func (s *Service) SetPolicy(ctx context.Context, ag agent.Ref, p Policy, by By) error {
	if !p.Valid() {
		return fmt.Errorf("approval: policy %q is not ask or never", p)
	}
	if ag.IsZero() || by.Principal == "" {
		return errors.New("approval: a policy switch needs its agent and its principal")
	}
	var log Log
	if ss := s.sessions.Load(); ss != nil {
		if h, ok := (*ss).Get(ag.Session); ok {
			log = h
		}
	}
	if log == nil {
		return fmt.Errorf("%w: session %s is not live", ErrNoSession, ag.Session)
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()
	if s.Policy(ag.Session) == p {
		return nil
	}
	d, err := PolicyType.Draft(PolicySet{Policy: p, By: by})
	if err == nil {
		_, err = log.Append(ctx, d)
	}
	if err != nil {
		return fmt.Errorf("approval: append approval/policy: %w", err)
	}
	s.restorePolicy(ag.Session, PolicyState{Policy: p, By: by})
	if s.cfg.Grants != nil {
		return s.cfg.Grants.EndAll(ctx, log, ag, EndPolicyChanged)
	}
	return nil
}

// The model-facing text of the approval-policy section, one per policy.
const (
	PolicyTextAsk = "Approval policy: ask. A tool call that needs approval waits until a person allows or rejects it. " +
		"A call whose approval is rejected, cancelled or unavailable did not run."
	PolicyTextNever = "Approval policy: never. Nobody is asked to approve tool calls in this session: " +
		"a call that needs approval is rejected without running, so prefer actions that need no approval."
)

// PolicyText is the approval-policy section's text for p.
func PolicyText(p Policy) string {
	if p == PolicyNever {
		return PolicyTextNever
	}
	return PolicyTextAsk
}

// PromptSection is the system-prompt section that tells the model its session's
// approval policy (DH-D-HUMAN-2, as upstream). It is computed per assembly from
// the step's agent, so a switch reaches the next step.
func (s *Service) PromptSection() prompt.Section {
	return prompt.Section{Name: prompt.SectionApprovalPolicy, Order: prompt.OrderApproval, Literal: true,
		Func: func(in prompt.Input) (string, error) { return PolicyText(s.Policy(in.Target().Agent.Session)), nil }}
}

// RegisterPromptSection registers PromptSection in ps, owned by owner.
func (s *Service) RegisterPromptSection(owner *cordis.Context, ps *prompt.Service) (*cordis.EffectHandle, error) {
	return ps.Section(owner, s.PromptSection())
}
