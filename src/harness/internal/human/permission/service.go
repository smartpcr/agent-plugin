package permission

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// The errors.
var (
	// ErrUnknownPreset is a switch to a name the table does not hold.
	ErrUnknownPreset = errors.New("permission/unknown-preset")
	// ErrNoSession is a switch or read of a session that is not live here.
	ErrNoSession = errors.New("permission/no-session")
)

// Sessions finds the live session permission/preset is appended to
// (session.Service is one).
type Sessions interface {
	Get(id session.ID) (session.Session, bool)
}

// Sandbox is the sandbox knob's owner (the sandbox resolver is one): Mode reads
// the mode in force for a session, SetMode writes it.
type Sandbox interface {
	Mode(ctx context.Context, s session.ID) (sandbox.Mode, error)
	SetMode(ctx context.Context, s session.ID, m sandbox.Mode) error
}

// Approval is the approval knob's owner (*approval.Service is one).
type Approval interface {
	Policy(id session.ID) approval.Policy
	SetPolicy(ctx context.Context, ag agent.Ref, p approval.Policy, by approval.By) error
}

// Deps are the service's collaborators. Sessions, Sandbox and Approval are
// required; Grants, when set, is the table a switch revokes even when only the
// mode changes (SetPolicy revokes it when the policy does).
type Deps struct {
	Sessions Sessions
	Sandbox  Sandbox
	Approval Approval
	Grants   *approval.Grants
}

// Service is permission.Service, the one Permissions choice. It is safe for
// concurrent use; switches are serialized.
type Service struct {
	deps Deps

	mu  sync.Mutex // guards cfg
	cfg Config
	// setMu serializes switches and pins, so a selection and its knob writes are
	// one step.
	setMu sync.Mutex
}

// Key is the service permission.presets provides (ctx.permissionPresets).
var Key = cordis.NewServiceKey[*Service]("permission.presets")

// New returns a service over cfg, which must validate, and deps.
func New(cfg Config, deps Deps) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Sessions == nil || deps.Sandbox == nil || deps.Approval == nil {
		return nil, errors.New("permission: the sessions, the sandbox resolver and the approval service are required")
	}
	return &Service{deps: deps, cfg: clone(cfg)}, nil
}

func clone(c Config) Config {
	c.Presets = slices.Clone(c.Presets)
	return c
}

// reconfigure swaps the table in place (the table is live configuration); the
// schema has validated cfg. No session's knobs change: a session whose
// selection left the table reads its knobs' first match, else custom.
func (s *Service) reconfigure(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = clone(cfg)
}

func (s *Service) config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Presets is a copy of the table, in its order.
func (s *Service) Presets() []Preset { return slices.Clone(s.config().Presets) }

// Default is the preset a fresh session is pinned to.
func (s *Service) Default() Preset {
	c := s.config()
	p, _ := c.lookup(c.DefaultPreset)
	return p
}

// Lookup is the entry named name.
func (s *Service) Lookup(name string) (Preset, bool) { return s.config().lookup(name) }

func (s *Service) live(id session.ID) (session.Session, error) {
	if h, ok := s.deps.Sessions.Get(id); ok {
		return h, nil
	}
	return nil, fmt.Errorf("%w: session %s is not live", ErrNoSession, id)
}

// knobs are the mode and the policy in force for session id, read from their
// owners.
func (s *Service) knobs(ctx context.Context, id session.ID) (sandbox.Mode, approval.Policy, error) {
	m, err := s.deps.Sandbox.Mode(ctx, id)
	if err != nil {
		return "", "", fmt.Errorf("permission: read the sandbox mode of %s: %w", id, err)
	}
	return m, s.deps.Approval.Policy(id), nil
}

// Current is the permission preset of session id: its last selection while the
// knobs in force are that entry's pair, else the first entry they match, else
// Custom.
func (s *Service) Current(ctx context.Context, id session.ID) (string, error) {
	h, err := s.live(id)
	if err != nil {
		return "", err
	}
	st, err := fold(ctx, h)
	if err != nil {
		return "", err
	}
	mode, policy, err := s.knobs(ctx, id)
	if err != nil {
		return "", err
	}
	c := s.config()
	if p, ok := c.lookup(st.Preset); ok && p.matches(mode, policy) {
		return p.Name, nil
	}
	for _, p := range c.Presets {
		if p.matches(mode, policy) {
			return p.Name, nil
		}
	}
	return Custom, nil
}

// Set switches ag's session to the preset named name, by by (an owner's action;
// /permission refuses anyone else). A switch to the selection in force whose
// knobs still hold writes nothing. Otherwise permission/preset is appended, then
// the sandbox mode is written through Sandbox.SetMode (attributed to by) and the
// approval policy through Approval.SetPolicy, each only if it changes, so the
// log gains at most one sandbox/mode and then at most one approval/policy; then
// every grant of ag ends policy-changed. A knob write that fails returns its
// error; Current then reads the knobs actually in force.
func (s *Service) Set(ctx context.Context, ag agent.Ref, name string, by approval.By) (Preset, error) {
	if ag.IsZero() || by.Principal == "" {
		return Preset{}, errors.New("permission: a switch needs its agent and its principal")
	}
	p, ok := s.Lookup(name)
	if !ok {
		return Preset{}, fmt.Errorf("%w: %q is not a permission preset", ErrUnknownPreset, name)
	}
	h, err := s.live(ag.Session)
	if err != nil {
		return Preset{}, err
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()
	st, err := fold(ctx, h)
	if err != nil {
		return Preset{}, err
	}
	mode, policy, err := s.knobs(ctx, ag.Session)
	if err != nil {
		return Preset{}, err
	}
	if st.Preset == p.Name && p.matches(mode, policy) {
		return p, nil
	}
	if err := s.write(ctx, h, ag, p, false, by); err != nil {
		return p, err
	}
	if s.deps.Grants != nil {
		if err := s.deps.Grants.EndAll(ctx, h, ag, approval.EndPolicyChanged); err != nil {
			return p, fmt.Errorf("permission: end the grants of %s: %w", ag, err)
		}
	}
	return p, nil
}

// write appends permission/preset for p and writes its knobs through their
// owners.
func (s *Service) write(ctx context.Context, h session.Session, ag agent.Ref, p Preset, pinned bool, by approval.By) error {
	d, err := PresetType.Draft(PresetSet{Preset: p.Name, Mode: p.Mode, Approval: p.Approval, Pinned: pinned, By: by})
	if err == nil {
		_, err = h.Append(ctx, d)
	}
	if err != nil {
		return fmt.Errorf("permission: append permission/preset: %w", err)
	}
	actx := sandbox.WithActor(ctx, sandbox.Actor{Principal: by.Principal, Channel: by.Channel, Peer: by.Peer})
	if err := s.deps.Sandbox.SetMode(actx, ag.Session, p.Mode); err != nil {
		return fmt.Errorf("permission: set the sandbox mode of %s to %s: %w", ag.Session, p.Mode, err)
	}
	if err := s.deps.Approval.SetPolicy(ctx, ag, p.Approval, by); err != nil {
		return fmt.Errorf("permission: set the approval policy of %s to %s: %w", ag.Session, p.Approval, err)
	}
	return nil
}

// Pin pins ag's session to the default preset when the session is fresh: its log
// holds no permission/preset, sandbox/mode or approval/policy. A resumed session
// keeps its values and nothing is written. The pin is by the harness and marked
// Pinned; the knobs are written as by Set.
func (s *Service) Pin(ctx context.Context, ag agent.Ref) error {
	h, err := s.live(ag.Session)
	if err != nil {
		return err
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()
	fresh, err := isFresh(ctx, h)
	if err != nil || !fresh {
		return err
	}
	return s.write(ctx, h, ag, s.Default(), true, approval.By{Principal: PrincipalHarness})
}

// Bind pins every fresh session when its agent is created under root (Pin); a
// failed pin fails the creation, so the agent never accepts input outside its
// preset. The returned handle unbinds.
func (s *Service) Bind(root *cordis.Context) (*cordis.EffectHandle, error) {
	return agent.Created.On(root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		ref := n.Agent.Ref()
		if _, ok := s.deps.Sessions.Get(ref.Session); !ok {
			return nil
		}
		return s.Pin(context.Background(), ref)
	})
}

// fold is the permissions fold of rs.
func fold(ctx context.Context, rs session.ReadSession) (State, error) {
	evs, err := rs.Read(ctx, 0, 0)
	if err != nil {
		return State{}, fmt.Errorf("permission: read the log of session %s: %w", rs.ID(), err)
	}
	var st State
	for _, ev := range evs {
		st, _ = apply(st, ev)
	}
	return st, nil
}

// knobTypes are the events that make a session not fresh.
var knobTypes = []string{PresetType.Name(), sandbox.ModeEventType, approval.PolicyType.Name()}

func isFresh(ctx context.Context, rs session.ReadSession) (bool, error) {
	evs, err := rs.Read(ctx, 0, 0)
	if err != nil {
		return false, fmt.Errorf("permission: read the log of session %s: %w", rs.ID(), err)
	}
	return !slices.ContainsFunc(evs, func(ev event.Event) bool { return slices.Contains(knobTypes, ev.Type()) }), nil
}

// CommandName is the name of the switch command.
const CommandName = "permission"

// Command is /permission [preset]: without an argument it lists the table and
// marks the current preset; with one it switches to that preset. It changes
// authority, so only the agent's owner may run it (DH-ADR-HUMAN-6, -8).
func (s *Service) Command() command.Command {
	return command.Command{
		Name:      CommandName,
		Summary:   "Show or switch the permission preset (sandbox mode and approval policy)",
		OwnerOnly: true,
		Run: func(ctx context.Context, c command.Call) (string, error) {
			name := strings.TrimSpace(c.Rest)
			if name == "" {
				return s.describe(ctx, c.Agent.Session)
			}
			p, err := s.Set(ctx, c.Agent, name, attributed(c.By))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("permission preset %s: sandbox %s, approval %s", p.Name, p.Mode, p.Approval), nil
		},
	}
}

func (s *Service) describe(ctx context.Context, id session.ID) (string, error) {
	cur, err := s.Current(ctx, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "permission preset: %s", cur)
	for _, p := range s.Presets() {
		mark := " "
		if p.Name == cur {
			mark = "*"
		}
		fmt.Fprintf(&b, "\n%s %s: sandbox %s, approval %s", mark, p.Name, p.Mode, p.Approval)
	}
	return b.String(), nil
}

// RegisterCommand registers Command in r as an effect of ctx's fiber.
func (s *Service) RegisterCommand(ctx *cordis.Context, r *command.Registry) (*cordis.EffectHandle, error) {
	return r.Register(ctx, s.Command())
}

// attributed is the approval attribution of a command's actor, named as the
// command registry names it.
func attributed(a human.Actor) approval.By {
	by := approval.By{Principal: a.Name, Channel: string(a.Channel), Peer: string(a.Peer)}
	if by.Principal == "" {
		by.Principal = by.Channel
	}
	if by.Principal == "" {
		by.Principal = "unknown"
	}
	return by
}
