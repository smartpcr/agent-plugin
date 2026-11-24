// Package policy is the sandbox policy resolver (C33, DH-D-SANDBOX-1; impl.md
// 12.3): the one place that turns a session's mode, working directory, workspace
// root, configured roots and directory grants into the sandbox.Policy of one
// call. Shells, file tools, terminals and plugins all ask it, so they cannot
// drift.
//
// Precedence (sandbox-ssh.md sec. 3.2): an approved explicit Request.Mode
// outranks the session's last sandbox/mode event, which outranks
// Config.DefaultMode (read-only). Under workspace-write the write roots are the
// working directory's project root (the workspace), the configured extra roots,
// the session's grants and its scratch directory, in that order, each once; the
// configured roots and the grants are re-checked on every call. Read-only and
// danger-full-access carry no write roots.
//
// SetMode dispatches sandbox.ModeChanging (ordered: the first listener error
// refuses the change) and only then appends sandbox/mode to the session's log;
// the new mode applies to the next Resolve. The model-visible policy section is
// rendered by Render from the per-platform text (render.go); no text ships yet,
// so Text returns ErrNoText and the section is left out (M2-C-6).
//
// Only world/fs/ops imports this package (archtest): every tool reaches the fence
// through ops.
package policy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
)

// Network is a session's or the deployment's outbound network setting.
type Network string

// The two settings.
const (
	NetworkAllowed Network = "allowed"
	NetworkDenied  Network = "denied"
)

// Valid reports whether n is allowed or denied.
func (n Network) Valid() bool { return n == NetworkAllowed || n == NetworkDenied }

// Config is policy.Config (sandbox-ssh.md sec. 6.2), the config of the
// sandbox.policy entry.
type Config struct {
	// DefaultMode is the mode of a session that never wrote sandbox/mode; ""
	// is read-only.
	DefaultMode sandbox.Mode `json:"defaultMode,omitempty"`
	// ExtraWriteRoots are absolute, clean directories every workspace-write call
	// may write below (source config); each is re-checked on every call.
	ExtraWriteRoots []string `json:"extraWriteRoots,omitempty"`
	// Network is the default network setting; "" is allowed.
	Network Network `json:"network,omitempty"`
	// Require is the confinement level a command needs; "" is any.
	Require sandbox.Require `json:"require,omitempty"`
	// UnconfinedHelpers maps each helper kind a compiled-in component allows to
	// run unconfined to its reason; any other kind asking is refused. Compiled
	// in, so no configuration layer can set it.
	UnconfinedHelpers map[string]string `json:"-"`
	// Platform names the policy text a resolved policy is versioned with; "" is
	// runtime.GOOS.
	Platform string `json:"platform,omitempty"`
}

// Validate reports a malformed Config.
func (c Config) Validate() error {
	if c.DefaultMode != "" && !c.DefaultMode.Valid() {
		return herr.Newf(sandbox.CodeInvalidMode, "default mode %q is not read-only, workspace-write or danger-full-access", c.DefaultMode)
	}
	for _, r := range c.ExtraWriteRoots {
		if !cleanAbs(r) {
			return herr.Newf(sandbox.CodeInvalidRoot, "configured write root %q is not an absolute, clean path", r)
		}
	}
	if c.Network != "" && !c.Network.Valid() {
		return herr.Newf(sandbox.CodeUnavailable, "network %q is not allowed or denied", c.Network)
	}
	switch c.Require {
	case "", sandbox.RequireFull, sandbox.RequireAny:
	default:
		return herr.Newf(sandbox.CodeUnavailable, "require %q is not full or any", c.Require)
	}
	for kind, reason := range c.UnconfinedHelpers {
		if kind == "" || strings.TrimSpace(reason) == "" {
			return herr.Newf(sandbox.CodeUnavailable, "unconfined helper kind %q needs a kind and a reason", kind)
		}
	}
	if c.Platform != "" && !slices.Contains(Platforms, c.Platform) {
		return fmt.Errorf("policy: %w: %q", ErrUnknownPlatform, c.Platform)
	}
	return nil
}

func (c Config) defaultMode() sandbox.Mode {
	if c.DefaultMode == "" {
		return sandbox.ReadOnly
	}
	return c.DefaultMode
}

func (c Config) network() Network {
	if c.Network == "" {
		return NetworkAllowed
	}
	return c.Network
}

func (c Config) platform() string {
	if c.Platform == "" {
		return runtime.GOOS
	}
	return c.Platform
}

// Sessions is what the resolver reads a session's log through: a live session
// to append sandbox/mode to, or a stored one to fold. session.Service is one.
type Sessions interface {
	Get(id session.ID) (session.Session, bool)
	OpenRead(ctx context.Context, id session.ID) (session.ReadSession, func(), error)
}

// Escalation is one widening put to the Approver: the model's request and what
// the resolver decided it asks for.
type Escalation struct {
	Request sandbox.EscalationRequest
	// From is the mode the call would run under without the escalation.
	From sandbox.Mode
	// To is the requested mode; Paths are the extra write roots, cleaned.
	To    sandbox.Mode
	Paths []string
}

// Approver asks a person for a one-call widening (A17). A nil error approves
// that one call; any error refuses it.
type Approver interface {
	ApproveEscalation(ctx context.Context, e Escalation) error
}

// ApproverFunc adapts a function to Approver.
type ApproverFunc func(ctx context.Context, e Escalation) error

// ApproveEscalation implements Approver.
func (f ApproverFunc) ApproveEscalation(ctx context.Context, e Escalation) error { return f(ctx, e) }

// Deps are the resolver's collaborators. Owner is required; the others are
// optional and, when nil, contribute nothing (no session log, no root checks,
// no scratch, no escalation answerer).
type Deps struct {
	// Owner dispatches sandbox.ModeChanging, scoped to the session.
	Owner *cordis.Context
	// Sessions holds the logs sandbox/mode is read from and appended to.
	Sessions Sessions
	// Workdir supplies each session's working directory and project root.
	Workdir workdir.Lookup
	// FS re-checks configured roots and grants on every call: each must exist,
	// be a directory and not be a symbolic link.
	FS fs.FS
	// Scratch is the private scratch directory of a session, a write root of
	// every workspace-write policy; "" means none.
	Scratch func(s session.ID) (string, error)
	// Approver decides an escalation; nil rejects every escalation.
	Approver Approver
}

// PrincipalHarness is the principal of a mode change no person made.
const PrincipalHarness = "harness"

// Actor attributes a mode change to the principal who made it, the channel it
// came through and the peer on that channel (the shape of approval.By).
type Actor struct {
	Principal string `json:"principal"`
	Channel   string `json:"channel,omitempty"`
	Peer      string `json:"peer,omitempty"`
}

// ModeSet is the payload of sandbox/mode: the whole new value (mode and network
// setting), the mode it replaced and who set it (architecture.md sec. 4.1).
type ModeSet struct {
	Mode    sandbox.Mode `json:"mode"`
	From    sandbox.Mode `json:"from,omitempty"`
	Network Network      `json:"network"`
	By      Actor        `json:"by"`
}

// ModeType is the durable, log-only sandbox/mode event (sandbox.ModeEventType);
// only SetMode writes it. The name is a literal for the inventory scan.
var ModeType = event.MustDefine(event.Default(), event.Spec[ModeSet]{Name: "sandbox/mode", Version: 1,
	Validate: func(m ModeSet) error {
		if !m.Mode.Valid() {
			return fmt.Errorf("sandbox/mode: mode %q is not a sandbox mode", m.Mode)
		}
		if m.From != "" && !m.From.Valid() {
			return fmt.Errorf("sandbox/mode: previous mode %q is not a sandbox mode", m.From)
		}
		if !m.Network.Valid() {
			return fmt.Errorf("sandbox/mode: network %q is not allowed or denied", m.Network)
		}
		if m.By.Principal == "" {
			return errors.New("sandbox/mode: a change must name its principal")
		}
		return nil
	}})

// ModeKey is the key of the sandbox-mode fold.
const ModeKey = "sandboxMode"

// ModeState is the sandbox-mode fold of one session: its last sandbox/mode. An
// empty Mode means the session never set one, so the configured default applies.
type ModeState struct {
	Mode    sandbox.Mode `json:"mode,omitempty"`
	Network Network      `json:"network,omitempty"`
	By      Actor        `json:"by,omitzero"`
}

func apply(s ModeState, ev event.Event) (ModeState, bool) {
	if ev.Type() != ModeType.Name() {
		return s, false
	}
	m, err := ModeType.As(ev)
	if err != nil {
		return s, false
	}
	return ModeState{Mode: m.Mode, Network: m.Network, By: m.By}, true
}

// ModeView is the sandbox-mode fold (session-views registration, DH-D-VIEW-1):
// the last sandbox/mode of the log. The resolver folds the same way when it
// reads a session it has not seen.
func ModeView() views.Definition[ModeState] {
	return views.Definition[ModeState]{
		Key: ModeKey, Version: 1, Stored: true, Types: []string{ModeType.Name()},
		Init:  func(session.Header) ModeState { return ModeState{} },
		Apply: apply,
	}
}

// RegisterView registers the sandbox-mode fold in r.
func RegisterView(r *views.Registry) (views.Handle[ModeState], error) {
	return views.Register(r, ModeView())
}

type actorKey struct{}

// WithActor returns ctx carrying by, the principal SetMode records on the
// sandbox/mode it appends; without it the principal is the harness.
func WithActor(ctx context.Context, by Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, by)
}

func actorOf(ctx context.Context) Actor {
	if by, ok := ctx.Value(actorKey{}).(Actor); ok && by.Principal != "" {
		return by
	}
	return Actor{Principal: PrincipalHarness}
}

// Resolver is the policy resolver. It is safe for concurrent use.
type Resolver struct {
	cfg  Config
	deps Deps

	mu sync.Mutex
	// modes and setting hold live sessions only: an entry whose session
	// Sessions.Get no longer returns is evicted when next read, and by a sweep
	// each time modes doubles past its size after the previous sweep, so both
	// stay within twice the live sessions the resolver has seen.
	modes     map[session.ID]ModeState // a live session's fold, or SetMode's
	grants    map[session.ID][]*string
	setting   map[session.ID]chan struct{}
	sweptSize int
}

var _ sandbox.Resolver = (*Resolver)(nil)

// New returns a resolver over cfg and deps; cfg must validate and deps.Owner be
// set.
func New(cfg Config, deps Deps) (*Resolver, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Owner == nil {
		return nil, errors.New("policy: an owner context is required to dispatch sandbox.ModeChanging")
	}
	cfg.ExtraWriteRoots = slices.Clone(cfg.ExtraWriteRoots)
	helpers := make(map[string]string, len(cfg.UnconfinedHelpers))
	for k, v := range cfg.UnconfinedHelpers {
		helpers[k] = v
	}
	cfg.UnconfinedHelpers = helpers
	return &Resolver{cfg: cfg, deps: deps, modes: map[session.ID]ModeState{}, grants: map[session.ID][]*string{},
		setting: map[session.ID]chan struct{}{}}, nil
}

func cleanAbs(p string) bool { return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p }

// Resolve implements sandbox.Resolver.
func (r *Resolver) Resolve(ctx context.Context, req sandbox.Request) (sandbox.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeUnavailable, err, "resolve")
	}
	if req.Mode != "" && !req.Mode.Valid() {
		return sandbox.Policy{}, herr.Newf(sandbox.CodeInvalidMode, "%q is not a sandbox mode", req.Mode)
	}
	st, err := r.state(ctx, req.Session)
	if err != nil {
		return sandbox.Policy{}, err
	}
	m := req.Mode
	if m == "" {
		m = r.modeOf(st)
	}
	return r.issue(ctx, req.Session, m, r.networkOf(st), nil)
}

// Mode is the mode session s resolves to without an explicit Request.Mode.
func (r *Resolver) Mode(ctx context.Context, s session.ID) (sandbox.Mode, error) {
	st, err := r.state(ctx, s)
	if err != nil {
		return "", err
	}
	return r.modeOf(st), nil
}

func (r *Resolver) modeOf(st ModeState) sandbox.Mode {
	if st.Mode.Valid() {
		return st.Mode
	}
	return r.cfg.defaultMode()
}

func (r *Resolver) networkOf(st ModeState) Network {
	if st.Network.Valid() {
		return st.Network
	}
	return r.cfg.network()
}

// state is the sandbox-mode fold of session s: the cached one, else folded from
// its live log (and cached), else folded from its stored log. Without Sessions
// every session has the zero state.
func (r *Resolver) state(ctx context.Context, s session.ID) (ModeState, error) {
	r.mu.Lock()
	st, ok := r.modes[s]
	r.mu.Unlock()
	if r.deps.Sessions == nil {
		return st, nil
	}
	if live, isLive := r.deps.Sessions.Get(s); isLive {
		if ok {
			return st, nil
		}
		st, err := fold(ctx, live)
		if err != nil {
			return ModeState{}, err
		}
		r.mu.Lock()
		// A SetMode that finished meanwhile wins over this older read.
		if cur, ok := r.modes[s]; ok {
			st = cur
		} else {
			r.modes[s] = st
		}
		sweep := len(r.modes) > 2*r.sweptSize
		r.mu.Unlock()
		if sweep {
			r.evictEnded()
		}
		return st, nil
	}
	if ok {
		r.mu.Lock()
		r.evictLocked(s)
		r.mu.Unlock()
	}
	rs, release, err := r.deps.Sessions.OpenRead(ctx, s)
	if err != nil {
		return ModeState{}, herr.Wrap(sandbox.CodeUnavailable, err, fmt.Sprintf("read the sandbox mode of session %s", s))
	}
	defer release()
	return fold(ctx, rs)
}

// evictLocked drops the cached fold and transition slot of session s, an ended
// session; a slot still held by a running SetMode is left for the next sweep.
// r.mu must be held.
func (r *Resolver) evictLocked(s session.ID) {
	delete(r.modes, s)
	if slot, ok := r.setting[s]; ok && len(slot) == 0 {
		delete(r.setting, s)
	}
}

// evictEnded sweeps modes and setting of every session Sessions.Get no longer
// returns. Get is called without r.mu held.
func (r *Resolver) evictEnded() {
	r.mu.Lock()
	ids := make([]session.ID, 0, len(r.modes)+len(r.setting))
	for s := range r.modes {
		ids = append(ids, s)
	}
	for s := range r.setting {
		if _, ok := r.modes[s]; !ok {
			ids = append(ids, s)
		}
	}
	r.mu.Unlock()
	var ended []session.ID
	for _, s := range ids {
		if _, ok := r.deps.Sessions.Get(s); !ok {
			ended = append(ended, s)
		}
	}
	r.mu.Lock()
	for _, s := range ended {
		r.evictLocked(s)
	}
	r.sweptSize = len(r.modes)
	r.mu.Unlock()
}

func fold(ctx context.Context, rs session.ReadSession) (ModeState, error) {
	evs, err := rs.Read(ctx, 0, 0)
	if err != nil {
		return ModeState{}, herr.Wrap(sandbox.CodeUnavailable, err, fmt.Sprintf("read the log of session %s", rs.ID()))
	}
	var st ModeState
	for _, ev := range evs {
		st, _ = apply(st, ev)
	}
	return st, nil
}

// issue builds the policy of mode m for session s, with extra escalation roots.
func (r *Resolver) issue(ctx context.Context, s session.ID, m sandbox.Mode, n Network, extra []string) (sandbox.Policy, error) {
	spec := mint.Spec{Mode: string(m), Network: n == NetworkAllowed, Require: string(r.cfg.Require)}
	if t, err := Text(r.cfg.platform()); err == nil {
		spec.TextVersion = TextVersion(t)
	}
	if m != sandbox.WorkspaceWrite {
		return sandbox.Issue(spec)
	}
	roots, err := r.roots(ctx, s)
	if err != nil {
		return sandbox.Policy{}, err
	}
	for _, p := range extra {
		if err := r.checkRoot(ctx, p); err != nil {
			return sandbox.Policy{}, err
		}
		roots = addRoot(roots, p, sandbox.SourceEscalation)
	}
	for _, w := range roots {
		spec.Roots = append(spec.Roots, mint.Root{Path: w.Path, Source: string(w.Source)})
	}
	return sandbox.Issue(spec)
}

// roots are the workspace-write roots of session s: workspace, configured
// roots, grants, scratch; each path once, under its first source.
func (r *Resolver) roots(ctx context.Context, s session.ID) ([]sandbox.WriteRoot, error) {
	var out []sandbox.WriteRoot
	if r.deps.Workdir != nil {
		a, err := r.deps.Workdir.Access(s)
		if err != nil {
			return nil, err
		}
		root, err := a.Root()
		if err != nil {
			return nil, err
		}
		out = addRoot(out, root, sandbox.SourceWorkspace)
	}
	for _, p := range r.cfg.ExtraWriteRoots {
		if err := r.checkRoot(ctx, p); err != nil {
			return nil, err
		}
		out = addRoot(out, p, sandbox.SourceConfig)
	}
	r.mu.Lock()
	grants := make([]string, 0, len(r.grants[s]))
	for _, g := range r.grants[s] {
		grants = append(grants, *g)
	}
	r.mu.Unlock()
	for _, p := range grants {
		if err := r.checkRoot(ctx, p); err != nil {
			return nil, err
		}
		out = addRoot(out, p, sandbox.SourceGrant)
	}
	if r.deps.Scratch != nil {
		p, err := r.deps.Scratch(s)
		if err != nil {
			return nil, herr.Wrap(sandbox.CodeUnavailable, err, fmt.Sprintf("the scratch directory of session %s", s))
		}
		if p != "" {
			out = addRoot(out, p, sandbox.SourceScratch)
		}
	}
	return out, nil
}

func addRoot(roots []sandbox.WriteRoot, p string, src sandbox.RootSource) []sandbox.WriteRoot {
	if slices.ContainsFunc(roots, func(w sandbox.WriteRoot) bool { return samePath(w.Path, p) }) {
		return roots
	}
	return append(roots, sandbox.WriteRoot{Path: p, Source: src})
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether path is root or below it, case-insensitively on
// Windows.
func within(root, path string) bool {
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkRoot refuses, with sandbox/invalid-root, a root that is not absolute and
// clean or (with FS) is missing, not a directory or a symbolic link
// (sandbox-ssh.md sec. 3.2).
func (r *Resolver) checkRoot(ctx context.Context, p string) error {
	if !cleanAbs(p) {
		return herr.Newf(sandbox.CodeInvalidRoot, "write root %q is not an absolute, clean path", p)
	}
	if r.deps.FS == nil {
		return nil
	}
	info, err := r.deps.FS.Stat(ctx, fs.Ref{Path: p, NoFollowFinal: true})
	switch {
	case err != nil:
		return herr.Wrap(sandbox.CodeInvalidRoot, err, fmt.Sprintf("write root %q cannot be checked", p))
	case !info.Exists:
		return herr.Newf(sandbox.CodeInvalidRoot, "write root %q does not exist", p)
	case info.Type == fs.TypeSymlink:
		return herr.Newf(sandbox.CodeInvalidRoot, "write root %q is a symbolic link", p)
	case info.Type != fs.TypeDir:
		return herr.Newf(sandbox.CodeInvalidRoot, "write root %q is not a directory", p)
	}
	return nil
}
