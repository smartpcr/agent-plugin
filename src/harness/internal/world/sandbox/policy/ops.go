package policy

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
)

// Escalate implements sandbox.Resolver: the model's one wider retry (A17). The
// requested mode is req.Mode, else req.Permissions read as a mode; it must be
// strictly wider than the session's mode, or equal to it with extra Paths that
// are not already writable. Paths widen only a workspace-write call. A request
// without a justification, or that the Approver refuses, is
// sandbox/escalation-rejected; the policy returned is for that one call and
// changes no session state.
func (r *Resolver) Escalate(ctx context.Context, req sandbox.EscalationRequest) (sandbox.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeUnavailable, err, "escalate")
	}
	to, err := requested(req)
	if err != nil {
		return sandbox.Policy{}, err
	}
	if strings.TrimSpace(req.Justification) == "" {
		return sandbox.Policy{}, herr.New(sandbox.CodeEscalationRejected, "an escalation needs a justification")
	}
	paths := make([]string, 0, len(req.Paths))
	for _, p := range req.Paths {
		if !filepath.IsAbs(p) {
			return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "extra write root %q is not absolute", p)
		}
		if p = filepath.Clean(p); !slices.ContainsFunc(paths, func(q string) bool { return samePath(p, q) }) {
			paths = append(paths, p)
		}
	}
	if len(paths) > 0 && to != sandbox.WorkspaceWrite {
		return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "extra write roots widen only a workspace-write call, not %s", to)
	}
	// Checked before anyone is asked to approve a root that cannot be one; issue
	// checks them again, as it does every root, on the call.
	for _, p := range paths {
		if err := r.checkRoot(ctx, p); err != nil {
			return sandbox.Policy{}, err
		}
	}
	st, err := r.state(ctx, req.Session)
	if err != nil {
		return sandbox.Policy{}, err
	}
	from, n := r.modeOf(st), r.networkOf(st)
	switch {
	case to.Wider(from):
	case to != from:
		return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "%s is not wider than %s", to, from)
	case len(paths) == 0:
		return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "%s is the current mode and no extra write root is asked for", to)
	default:
		base, err := r.issue(ctx, req.Session, from, n, nil)
		if err != nil {
			return sandbox.Policy{}, err
		}
		widens := slices.ContainsFunc(paths, func(p string) bool {
			return !slices.ContainsFunc(base.WriteRoots(), func(w sandbox.WriteRoot) bool { return within(w.Path, p) })
		})
		if !widens {
			return sandbox.Policy{}, herr.Newf(sandbox.CodeEscalationRejected, "%v are already writable; the escalation widens nothing", paths)
		}
	}
	if r.deps.Approver == nil {
		return sandbox.Policy{}, herr.New(sandbox.CodeEscalationRejected, "no approver can allow an escalation")
	}
	if err := r.deps.Approver.ApproveEscalation(ctx, Escalation{Request: req, From: from, To: to, Paths: slices.Clone(paths)}); err != nil {
		if herr.Kind(err) == sandbox.CodeEscalationRejected {
			return sandbox.Policy{}, err
		}
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeEscalationRejected, err, fmt.Sprintf("escalation from %s to %s was not approved", from, to))
	}
	return r.issue(ctx, req.Session, to, n, paths)
}

func requested(req sandbox.EscalationRequest) (sandbox.Mode, error) {
	to := req.Mode
	if p := strings.TrimSpace(req.Permissions); p != "" {
		m, err := sandbox.ParseMode(p)
		if err != nil {
			return "", herr.Newf(sandbox.CodeEscalationRejected, "sandbox_permissions %q is not a sandbox mode", req.Permissions)
		}
		if to != "" && to != m {
			return "", herr.Newf(sandbox.CodeEscalationRejected, "sandbox_permissions %q disagrees with the requested mode %s", req.Permissions, to)
		}
		to = m
	}
	if to == "" {
		return "", herr.New(sandbox.CodeEscalationRejected, "an escalation must name the mode it asks for")
	}
	if !to.Valid() {
		return "", herr.Newf(sandbox.CodeInvalidMode, "%q is not a sandbox mode", to)
	}
	return to, nil
}

// ForHelper implements sandbox.Resolver: a helper child is read-only plus the
// named write roots, whatever the session's mode; it runs unconfined only for a
// kind in Config.UnconfinedHelpers. Its confinement level is the stricter of
// req.Require and Config.Require.
func (r *Resolver) ForHelper(ctx context.Context, req sandbox.HelperRequest) (sandbox.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Policy{}, herr.Wrap(sandbox.CodeUnavailable, err, "helper")
	}
	if req.Kind == "" {
		return sandbox.Policy{}, herr.New(sandbox.CodeUnavailable, "a helper must name its kind")
	}
	// The deployment's level is a floor: a helper may ask for full but never
	// loosen a configured full to any.
	require := req.Require
	if require == "" || r.cfg.Require == sandbox.RequireFull {
		require = r.cfg.Require
	}
	if req.Unconfined {
		if _, ok := r.cfg.UnconfinedHelpers[req.Kind]; !ok {
			return sandbox.Policy{}, herr.Newf(sandbox.CodeUnavailable, "helper kind %q may not run unconfined", req.Kind)
		}
		return sandbox.Issue(mint.Spec{Mode: string(sandbox.DangerFullAccess), Network: req.Network, Require: string(require)})
	}
	s := mint.Spec{Mode: string(sandbox.ReadOnly), Network: req.Network, Require: string(require)}
	if len(req.WriteRoots) > 0 {
		s.Mode = string(sandbox.WorkspaceWrite)
		for _, p := range req.WriteRoots {
			if err := r.checkRoot(ctx, p); err != nil {
				return sandbox.Policy{}, err
			}
			s.Roots = append(s.Roots, mint.Root{Path: p, Source: string(sandbox.SourceHelper)})
		}
	}
	return sandbox.Issue(s)
}

// GrantRoot implements sandbox.Resolver: dir is a write root of session s's
// workspace-write calls until release, which is idempotent. dir is checked now
// and again on every call.
func (r *Resolver) GrantRoot(ctx context.Context, s session.ID, dir string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, herr.Wrap(sandbox.CodeUnavailable, err, "grant")
	}
	if err := r.checkRoot(ctx, dir); err != nil {
		return nil, err
	}
	g := &dir
	r.mu.Lock()
	r.grants[s] = append(r.grants[s], g)
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.grants[s] = slices.DeleteFunc(r.grants[s], func(x *string) bool { return x == g })
			if len(r.grants[s]) == 0 {
				delete(r.grants, s)
			}
		})
	}, nil
}

// SetMode implements sandbox.Resolver. Transitions of one session are
// serialized. A change to the mode in force writes nothing. Otherwise
// sandbox.ModeChanging is dispatched in the session's scope (the first listener
// error refuses the change), then sandbox/mode is appended, attributed to
// WithActor's principal; only after the append does the mode apply to the next
// Resolve. The session must be live here.
func (r *Resolver) SetMode(ctx context.Context, s session.ID, m sandbox.Mode) error {
	if err := ctx.Err(); err != nil {
		return herr.Wrap(sandbox.CodeUnavailable, err, "set mode")
	}
	if !m.Valid() {
		return herr.Newf(sandbox.CodeInvalidMode, "%q is not a sandbox mode", m)
	}
	if r.deps.Sessions == nil {
		return herr.New(sandbox.CodeUnavailable, "set mode: the resolver has no session log to write sandbox/mode to")
	}
	live, ok := r.deps.Sessions.Get(s)
	if !ok {
		return herr.Newf(sandbox.CodeUnavailable, "set mode: session %s is not live", s)
	}
	r.mu.Lock()
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
	st, err := r.state(ctx, s)
	if err != nil {
		return err
	}
	from := r.modeOf(st)
	if from == m {
		return nil
	}
	if err := sandbox.ModeChanging.Emit(r.deps.Owner, live.Scope(), sandbox.ModeChange{Session: s, From: from, To: m}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return herr.Wrap(sandbox.CodeUnavailable, err, "set mode: cancelled during ModeChanging")
	}
	next := ModeSet{Mode: m, From: from, Network: r.networkOf(st), By: actorOf(ctx)}
	d, err := ModeType.Draft(next)
	if err == nil {
		_, err = live.Append(ctx, d)
	}
	if err != nil {
		return herr.Wrap(sandbox.CodeUnavailable, err, "set mode: append sandbox/mode")
	}
	r.mu.Lock()
	r.modes[s] = ModeState{Mode: next.Mode, Network: next.Network, By: next.By}
	r.mu.Unlock()
	return nil
}
