// Package sandbox is the contract half of the policy resolver (C33, DH-D-SANDBOX-1;
// cycle C-3 of impl.md sec. 10.2): the sandbox Mode, the opaque Policy a resolver
// issues for one call, the Resolver interface and its requests, the
// sandbox.ModeChanging event and the sandbox error codes. The resolver itself is
// world/sandbox/policy; the fake for tests is world/sandbox/sandboxtest.
//
// A Policy has unexported fields and no valid zero value: a `var p Policy` is
// refused with sandbox/unavailable by every consumer (Validate), never treated as
// read-only or as full access. Only a package under world/sandbox can issue one,
// because Issue takes the internal mint.Spec.
package sandbox

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
)

// The sandbox error codes this contract owns (architecture.md sec. 4.2 "Errors";
// sandbox-ssh.md sec. 6.1).
const (
	// CodeUnavailable: no usable policy (a zero Policy) or no backend can confine.
	CodeUnavailable herr.Reason = "sandbox/unavailable"
	// CodeInvalidRoot: a write root is not an absolute, clean path.
	CodeInvalidRoot herr.Reason = "sandbox/invalid-root"
	// CodeInvalidMode: a mode is none of the three.
	CodeInvalidMode herr.Reason = "sandbox/invalid-mode"
	// CodeEscalationRejected: a one-call widening was not approved.
	CodeEscalationRejected herr.Reason = "sandbox/escalation-rejected"
	// CodeLaunchFailed: the launch stage died, sent no valid ready, was refused,
	// or the host went away before go; the command never ran (INV-14).
	CodeLaunchFailed herr.Reason = "sandbox/launch-failed"
	// CodeExecFailed: the host sent go, but the stage could not start the command;
	// reported as the command's own failure to start.
	CodeExecFailed herr.Reason = "sandbox/exec-failed"
)

// Backend names the confinement a launch stage runs under (architecture.md sec.
// 4.2, Confinement.Backend).
type Backend string

// The backends. BackendNone is the only one of an unconfined plan.
const (
	BackendNone     Backend = "none"
	BackendBwrap    Backend = "bwrap"
	BackendLandlock Backend = "landlock"
)

// Backends lists the known backends.
var Backends = []Backend{BackendNone, BackendBwrap, BackendLandlock}

// Valid reports whether b is a known backend.
func (b Backend) Valid() bool { return slices.Contains(Backends, b) }

// Effect is one effect a policy promises to govern (a write outside the roots,
// outbound network, delegated-execution, ...). A launch stage reports the
// effects it governs; an effect it does not name counts as not governed
// (DH-ADR-SANDBOX-3).
type Effect string

// Mode is the sandbox mode of a session or one call.
type Mode string

// The three modes, narrowest first.
const (
	ReadOnly         Mode = "read-only"
	WorkspaceWrite   Mode = "workspace-write"
	DangerFullAccess Mode = "danger-full-access"
)

// Modes lists the modes, narrowest first: the strictly-wider ladder.
var Modes = []Mode{ReadOnly, WorkspaceWrite, DangerFullAccess}

// Valid reports whether m is one of the three modes.
func (m Mode) Valid() bool { return slices.Contains(Modes, m) }

// Wider reports whether m is strictly wider than than.
func (m Mode) Wider(than Mode) bool {
	return m.Valid() && than.Valid() && slices.Index(Modes, m) > slices.Index(Modes, than)
}

// ParseMode returns s as a Mode, or a sandbox/invalid-mode error.
func ParseMode(s string) (Mode, error) {
	if m := Mode(s); m.Valid() {
		return m, nil
	}
	return "", herr.Newf(CodeInvalidMode, "%q is not read-only, workspace-write or danger-full-access", s)
}

// RootSource says why a write root is writable.
type RootSource string

// The sources of a write root.
const (
	SourceWorkingDirectory RootSource = "working-directory"
	SourceWorkspace        RootSource = "workspace"
	SourceConfig           RootSource = "config"
	SourceGrant            RootSource = "grant"
	SourceEscalation       RootSource = "escalation"
	SourceScratch          RootSource = "scratch"
	SourceHelper           RootSource = "helper"
)

// WriteRoot is one directory a policy lets a call write below.
type WriteRoot struct {
	Path   string
	Source RootSource
}

// Require says whether a command may run when the backend cannot confine fully.
type Require string

// The two levels.
const (
	RequireFull Require = "full"
	RequireAny  Require = "any"
)

// Policy is the sandbox policy resolved for one call. It is opaque, immutable and
// comparable; its zero value is not a policy (see Validate).
type Policy struct{ v *value }

type value struct {
	mode        Mode
	roots       []WriteRoot
	network     bool
	require     Require
	textVersion string
}

// Issue validates s and returns the Policy it describes. Only packages under
// world/sandbox can name mint.Spec, so only a resolver issues a Policy.
func Issue(s mint.Spec) (Policy, error) {
	m, err := ParseMode(s.Mode)
	if err != nil {
		return Policy{}, err
	}
	req := Require(s.Require)
	switch req {
	case "":
		req = RequireAny
	case RequireFull, RequireAny:
	default:
		return Policy{}, herr.Newf(CodeUnavailable, "require %q is not full or any", s.Require)
	}
	if m != WorkspaceWrite && len(s.Roots) > 0 {
		return Policy{}, herr.Newf(CodeInvalidRoot, "a %s policy has no write roots", m)
	}
	roots := make([]WriteRoot, 0, len(s.Roots))
	for _, r := range s.Roots {
		if r.Path == "" || !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path {
			return Policy{}, herr.Newf(CodeInvalidRoot, "write root %q is not an absolute, clean path", r.Path)
		}
		if r.Source == "" {
			return Policy{}, herr.Newf(CodeInvalidRoot, "write root %q names no source", r.Path)
		}
		roots = append(roots, WriteRoot{Path: r.Path, Source: RootSource(r.Source)})
	}
	return Policy{v: &value{mode: m, roots: roots, network: s.Network, require: req, textVersion: s.TextVersion}}, nil
}

// IsZero reports whether p was never issued.
func (p Policy) IsZero() bool { return p.v == nil }

// Validate returns a sandbox/unavailable error for a Policy no resolver issued,
// and nil otherwise. Every consumer calls it before it reads p.
func (p Policy) Validate() error {
	if p.v == nil {
		return herr.New(CodeUnavailable, "no sandbox policy was resolved for this call")
	}
	return nil
}

// Mode is the policy's mode; "" for the zero Policy.
func (p Policy) Mode() Mode {
	if p.v == nil {
		return ""
	}
	return p.v.mode
}

// WriteRoots is a copy of the write roots with their sources. Only a
// workspace-write policy has any; danger-full-access writes anywhere.
func (p Policy) WriteRoots() []WriteRoot {
	if p.v == nil {
		return nil
	}
	return slices.Clone(p.v.roots)
}

// Network reports whether outbound network is an allowed effect.
func (p Policy) Network() bool { return p.v != nil && p.v.network }

// Require is full or any; "" for the zero Policy.
func (p Policy) Require() Require {
	if p.v == nil {
		return ""
	}
	return p.v.require
}

// TextVersion is the version of the model-visible policy text the call was
// resolved with, recorded by SANDBOX-7; "" when no text is shipped.
func (p Policy) TextVersion() string {
	if p.v == nil {
		return ""
	}
	return p.v.textVersion
}

// Request asks for the policy of one call of a session.
type Request struct {
	Session session.ID
	// Mode is an approved explicit mode for this call; it outranks the session's
	// last sandbox/mode, which outranks the configured default. "" means none.
	Mode Mode
}

// EscalationRequest is the model's request for one wider call: the two
// escalation fields of a tool call plus what the call needs.
type EscalationRequest struct {
	Session session.ID
	// Permissions is the tool's sandbox_permissions argument.
	Permissions string
	// Justification is the tool's justification argument, shown to the approver.
	Justification string
	// Mode is the requested mode; it must be strictly wider than the resolved one,
	// or equal with extra Paths.
	Mode Mode
	// Paths are extra directories the call needs to write.
	Paths []string
	// Tool and Call name the tool call the widening is for.
	Tool, Call string
}

// HelperRequest asks for the policy of a child that is not an agent's command
// (an MCP server, a document converter): read-only plus the named write roots.
type HelperRequest struct {
	Session    session.ID
	Kind       string
	WriteRoots []string
	Network    bool
	Require    Require
	// Unconfined is honoured only for a helper kind a compiled-in component
	// registered with a reason.
	Unconfined bool
}

// Resolver is the one place that turns mode, working directory, workspace and
// grants into a Policy (C33). Every enforcing capability asks it per call.
type Resolver interface {
	// Resolve returns the policy of one call; it works for a stored session no
	// agent is running.
	Resolve(ctx context.Context, req Request) (Policy, error)
	// Escalate asks for approval and returns a one-call, strictly wider policy.
	Escalate(ctx context.Context, req EscalationRequest) (Policy, error)
	// ForHelper returns the policy of a helper child.
	ForHelper(ctx context.Context, req HelperRequest) (Policy, error)
	// GrantRoot makes dir a write root of session s until release is called.
	GrantRoot(ctx context.Context, s session.ID, dir string) (release func(), err error)
	// SetMode dispatches ModeChanging and, if no listener refuses, writes
	// sandbox/mode to the session's log; the new mode applies to the next Resolve.
	// A mode the log already records writes nothing; recording the mode already
	// in force (the default) dispatches nothing and pins it.
	SetMode(ctx context.Context, s session.ID, m Mode) error
}

// Actor attributes a mode change to the principal who made it, the channel it
// came through and the peer on that channel (the shape of approval.By).
type Actor struct {
	Principal string `json:"principal"`
	Channel   string `json:"channel,omitempty"`
	Peer      string `json:"peer,omitempty"`
}

type actorKey struct{}

// WithActor returns ctx carrying by, the principal a resolver's SetMode records
// on the sandbox/mode it appends.
func WithActor(ctx context.Context, by Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, by)
}

// ActorFrom is the actor WithActor put on ctx; false when there is none or it
// names no principal.
func ActorFrom(ctx context.Context) (Actor, bool) {
	by, ok := ctx.Value(actorKey{}).(Actor)
	return by, ok && by.Principal != ""
}

// ResolverKey is the service the resolver component provides.
var ResolverKey = cordis.NewServiceKey[Resolver]("sandbox.policy")

// ModeEventType is the durable, log-only session event SetMode appends: the whole
// new value (mode and network setting) and the actor. The resolver registers it.
const ModeEventType = "sandbox/mode"

// ModeChange is the payload of ModeChanging.
type ModeChange struct {
	Session session.ID
	From    Mode
	To      Mode
}

// ModeChanging is dispatched by SetMode before sandbox/mode is appended, ordered:
// the first listener error refuses the change, so a running command can be
// refused under the old policy rather than mid-change.
var ModeChanging = scope.NewEvent[ModeChange]("sandbox.mode-changing")
