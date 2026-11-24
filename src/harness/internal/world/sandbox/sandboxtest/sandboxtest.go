// Package sandboxtest is the fake policy resolver: it issues real sandbox.Policy
// values from explicit inputs so the seam, ops and the tools can be tested
// without world/sandbox/policy (TC-01a). Only tests and other fakes import it
// (archtest).
package sandboxtest

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
)

// Policy issues a policy of mode m with write roots roots (each sourced
// "working-directory"); it panics on input Issue refuses, a test's mistake.
func Policy(m sandbox.Mode, roots ...string) sandbox.Policy {
	p, err := Issue(m, false, roots...)
	if err != nil {
		panic(fmt.Sprintf("sandboxtest: %v", err))
	}
	return p
}

// Issue issues a policy of mode m, network setting network and write roots
// roots, returning Issue's error for bad input.
func Issue(m sandbox.Mode, network bool, roots ...string) (sandbox.Policy, error) {
	s := mint.Spec{Mode: string(m), Network: network}
	for _, r := range roots {
		s.Roots = append(s.Roots, mint.Root{Path: r, Source: string(sandbox.SourceWorkingDirectory)})
	}
	return sandbox.Issue(s)
}

// Resolver is a fake sandbox.Resolver. Its precedence follows the real one: an
// explicit Request.Mode, then the session's mode (SetMode), then Default. Under
// workspace-write the write roots are the session's project root (from Workdir),
// its grants and Scratch.
type Resolver struct {
	// Default is the mode of a session SetMode never changed; "" is read-only.
	Default sandbox.Mode
	// Workdir, when set, supplies each session's root.
	Workdir workdir.Lookup
	// Scratch, when set, is a write root of every workspace-write policy.
	Scratch string
	// Approve decides an escalation: the returned mode and paths widen one call.
	// nil rejects every escalation with sandbox/escalation-rejected.
	Approve func(sandbox.EscalationRequest) (sandbox.Mode, []string, error)
	// OnModeChanging, when set, is called by SetMode before the change; an error
	// refuses it, as a ModeChanging listener does. Transitions of one session are
	// serialized, so it sees the actual previous mode; it may call Resolve,
	// Escalate or ForHelper, but not SetMode for the same session.
	OnModeChanging func(sandbox.ModeChange) error
	// UnconfinedKinds are the helper kinds allowed to run unconfined.
	UnconfinedKinds []string

	mu      sync.Mutex
	modes   map[session.ID]sandbox.Mode
	grants  map[session.ID][]*string
	changes []sandbox.ModeChange
	setting map[session.ID]chan struct{}
}

var _ sandbox.Resolver = (*Resolver)(nil)

// Resolve implements sandbox.Resolver.
func (r *Resolver) Resolve(ctx context.Context, req sandbox.Request) (sandbox.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeUnavailable, err, "resolve")
	}
	m := req.Mode
	if m == "" {
		m = r.mode(req.Session)
	} else if !m.Valid() {
		return sandbox.Policy{}, herr.Newf(sandbox.CodeInvalidMode, "%q is not a mode", m)
	}
	return r.issue(req.Session, m, nil)
}

// Escalate implements sandbox.Resolver.
func (r *Resolver) Escalate(ctx context.Context, req sandbox.EscalationRequest) (sandbox.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeUnavailable, err, "escalate")
	}
	if r.Approve == nil {
		return sandbox.Policy{}, herr.New(sandbox.CodeEscalationRejected, "escalation was not approved")
	}
	m, paths, err := r.Approve(req)
	if err != nil {
		return sandbox.Policy{}, err
	}
	cur := r.mode(req.Session)
	if len(paths) > 0 && m != sandbox.WorkspaceWrite {
		return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "extra write roots widen only a workspace-write call, not %s", m)
	}
	if m.Wider(cur) {
		return r.issue(req.Session, m, paths)
	}
	if m != cur || len(paths) == 0 {
		return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "%s is not wider than %s", m, cur)
	}
	base, err := r.issue(req.Session, m, nil)
	if err != nil {
		return sandbox.Policy{}, err
	}
	widens := slices.ContainsFunc(paths, func(p string) bool {
		return !slices.ContainsFunc(base.WriteRoots(), func(w sandbox.WriteRoot) bool { return within(w.Path, filepath.Clean(p)) })
	})
	if !widens {
		return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "%v are already writable; the escalation widens nothing", paths)
	}
	return r.issue(req.Session, m, paths)
}

// within reports whether path is root or below it, case-insensitively on Windows.
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

// ForHelper implements sandbox.Resolver.
func (r *Resolver) ForHelper(ctx context.Context, req sandbox.HelperRequest) (sandbox.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeUnavailable, err, "helper")
	}
	if req.Unconfined {
		if !slices.Contains(r.UnconfinedKinds, req.Kind) {
			return sandbox.Policy{}, herr.Newf(sandbox.CodeUnavailable, "helper kind %q may not run unconfined", req.Kind)
		}
		return sandbox.Issue(mint.Spec{Mode: string(sandbox.DangerFullAccess), Network: req.Network, Require: string(req.Require)})
	}
	s := mint.Spec{Mode: string(sandbox.ReadOnly), Network: req.Network, Require: string(req.Require)}
	if len(req.WriteRoots) > 0 {
		s.Mode = string(sandbox.WorkspaceWrite)
		for _, p := range req.WriteRoots {
			s.Roots = append(s.Roots, mint.Root{Path: p, Source: string(sandbox.SourceHelper)})
		}
	}
	return sandbox.Issue(s)
}

// GrantRoot implements sandbox.Resolver; release is idempotent.
func (r *Resolver) GrantRoot(ctx context.Context, s session.ID, dir string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, herr.Wrap(sandbox.CodeUnavailable, err, "grant")
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, herr.Newf(sandbox.CodeInvalidRoot, "grant %q is not an absolute, clean path", dir)
	}
	g := &dir
	r.mu.Lock()
	if r.grants == nil {
		r.grants = map[session.ID][]*string{}
	}
	r.grants[s] = append(r.grants[s], g)
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.grants[s] = slices.DeleteFunc(r.grants[s], func(x *string) bool { return x == g })
		})
	}, nil
}

// SetMode implements sandbox.Resolver.
func (r *Resolver) SetMode(ctx context.Context, s session.ID, m sandbox.Mode) error {
	if err := ctx.Err(); err != nil {
		return herr.Wrap(sandbox.CodeUnavailable, err, "set mode")
	}
	if !m.Valid() {
		return herr.Newf(sandbox.CodeInvalidMode, "%q is not a mode", m)
	}
	r.mu.Lock()
	if r.setting == nil {
		r.setting = map[session.ID]chan struct{}{}
	}
	slot, ok := r.setting[s]
	if !ok {
		slot = make(chan struct{}, 1)
		r.setting[s] = slot
	}
	r.mu.Unlock()
	// A one-slot channel, not a mutex: a queued transition gives up when its
	// context ends instead of running after its caller has gone.
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return herr.Wrap(sandbox.CodeUnavailable, ctx.Err(), "set mode: cancelled while another transition ran")
	}
	defer func() { <-slot }()
	if err := ctx.Err(); err != nil {
		return herr.Wrap(sandbox.CodeUnavailable, err, "set mode")
	}
	c := sandbox.ModeChange{Session: s, From: r.mode(s), To: m}
	if r.OnModeChanging != nil {
		if err := r.OnModeChanging(c); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return herr.Wrap(sandbox.CodeUnavailable, err, "set mode: cancelled during the listener")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.modes == nil {
		r.modes = map[session.ID]sandbox.Mode{}
	}
	r.modes[s] = m
	r.changes = append(r.changes, c)
	return nil
}

// Changes returns the mode changes SetMode accepted, in order: the sandbox/mode
// events the real resolver would have appended.
func (r *Resolver) Changes() []sandbox.ModeChange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.changes)
}

func (r *Resolver) mode(s session.ID) sandbox.Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.modes[s]; ok {
		return m
	}
	if r.Default != "" {
		return r.Default
	}
	return sandbox.ReadOnly
}

func (r *Resolver) issue(s session.ID, m sandbox.Mode, extra []string) (sandbox.Policy, error) {
	spec := mint.Spec{Mode: string(m)}
	if m != sandbox.WorkspaceWrite {
		return sandbox.Issue(spec)
	}
	add := func(p string, src sandbox.RootSource) {
		spec.Roots = append(spec.Roots, mint.Root{Path: p, Source: string(src)})
	}
	if r.Workdir != nil {
		a, err := r.Workdir.Access(s)
		if err != nil {
			return sandbox.Policy{}, err
		}
		root, err := a.Root()
		if err != nil {
			return sandbox.Policy{}, err
		}
		add(root, sandbox.SourceWorkingDirectory)
	}
	r.mu.Lock()
	for _, g := range r.grants[s] {
		add(*g, sandbox.SourceGrant)
	}
	r.mu.Unlock()
	if r.Scratch != "" {
		add(r.Scratch, sandbox.SourceScratch)
	}
	for _, p := range extra {
		add(p, sandbox.SourceEscalation)
	}
	return sandbox.Issue(spec)
}
