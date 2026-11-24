// Package ops is the operation service of the filesystem seam (C32, DH-D-FS-4,
// filesystem.md sec. 6): the one caller that combines an fs.FS provider with a
// sandbox.Policy. Every tool reaches project files through it.
//
// For each call it takes the session from the Call and refuses a call without
// one, builds the Ref against the session's working directory, resolves the
// policy afresh (a mode change between two calls applies to the second), fences
// each target, emits fs.BeforeMutation, calls the provider with that policy and
// notifies fs.Observed. A read skips the policy and BeforeMutation. The roots and
// the mode come only from the resolver; ops has no configuration of its own.
//
// The fs.Intent decision and the durable fs/observed event of filesystem.md
// sec. 6.2 are later steps (DH-D-FS-5); this package dispatches the two live
// events the seam defines today.
package ops

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Key is the service ops provides.
var Key = cordis.NewServiceKey[*Service]("fs.ops")

// Call is who makes a call.
type Call struct {
	// Session is required: a call without one is refused (sandbox/unavailable).
	Session session.ID
	// CallID is the tool call, carried on the events; "" for a direct caller.
	CallID string
	// Mode is an approved explicit mode for this call; "" uses the session's.
	Mode sandbox.Mode
}

// Options are the collaborators of a Service besides the provider and resolver.
type Options struct {
	// Workdir supplies a session's working directory, the Dir of a relative
	// path whose Ref has none. nil leaves relative paths without a Dir, which
	// the provider refuses.
	Workdir workdir.Lookup
}

// Service is the operation service.
type Service struct {
	ctx      *cordis.Context
	provider fs.FS
	resolver sandbox.Resolver
	workdir  workdir.Lookup
}

// New returns the service dispatching its events on ctx.
func New(ctx *cordis.Context, provider fs.FS, resolver sandbox.Resolver, o Options) *Service {
	return &Service{ctx: ctx, provider: provider, resolver: resolver, workdir: o.Workdir}
}

// ref checks the session and gives a relative Path the session's directory.
func (s *Service) ref(c Call, r fs.Ref) (fs.Ref, error) {
	if c.Session == "" {
		return fs.Ref{}, herr.New(sandbox.CodeUnavailable, "a filesystem call needs a session")
	}
	if r.Dir != "" || filepath.IsAbs(r.Path) || s.workdir == nil {
		return r, nil
	}
	a, err := s.workdir.Access(c.Session)
	if err != nil {
		return fs.Ref{}, err
	}
	dir, err := a.Path()
	if err != nil {
		return fs.Ref{}, err
	}
	r.Dir = dir
	return r, nil
}

// policy resolves the policy of this one call.
func (s *Service) policy(ctx context.Context, c Call) (sandbox.Policy, error) {
	if s.resolver == nil {
		return sandbox.Policy{}, herr.New(sandbox.CodeUnavailable, "no policy resolver")
	}
	p, err := s.resolver.Resolve(ctx, sandbox.Request{Session: c.Session, Mode: c.Mode})
	if err != nil {
		return sandbox.Policy{}, err
	}
	return p, p.Validate()
}

// target resolves r through the provider; for a mutation the fence then judges
// the resolved path, each root resolved by the same provider, so a refused call
// emits nothing.
func (s *Service) target(ctx context.Context, p sandbox.Policy, op fs.Op, r fs.Ref) (fs.Target, error) {
	in, err := s.provider.Stat(ctx, r)
	if err != nil {
		return fs.Target{}, err
	}
	canon := func(root string) (string, error) {
		ri, err := s.provider.Stat(ctx, fs.Ref{Path: root})
		if err != nil {
			return "", err
		}
		return ri.Target.Display, nil
	}
	if err := fs.FenceCanon(p, op, in.Target.Display, canon); err != nil {
		return fs.Target{}, err
	}
	return in.Target, nil
}

// before emits BeforeMutation; a listener's error or panic stops the mutation.
func (s *Service) before(c Call, op fs.Op, t fs.Target) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = herr.Newf(fs.CodeAborted, "a %s listener panicked: %v", fs.BeforeMutation.Name(), v)
		}
	}()
	if err := fs.BeforeMutation.Emit(s.ctx, fs.Mutation{Session: c.Session, CallID: c.CallID, Op: op, Target: t}); err != nil {
		if herr.Kind(err) == "" {
			return herr.Wrap(fs.CodeAborted, err, fmt.Sprintf("%s refused the %s of %s", fs.BeforeMutation.Name(), op, t.Display))
		}
		return err
	}
	return nil
}

func (s *Service) observed(c Call, op fs.Op, t fs.Target, present bool, v fs.Version) {
	fs.Observed.Notify(s.ctx, fs.Observation{Session: c.Session, CallID: c.CallID, Op: op, Target: t, Present: present, Version: v})
}

// Stat resolves r for the session.
func (s *Service) Stat(ctx context.Context, c Call, r fs.Ref) (fs.Info, error) {
	r, err := s.ref(c, r)
	if err != nil {
		return fs.Info{}, err
	}
	return s.provider.Stat(ctx, r)
}

// Open opens r for reading and notifies Observed with the version read (or the
// absence of a missing file).
func (s *Service) Open(ctx context.Context, c Call, r fs.Ref) (io.ReadCloser, fs.Info, error) {
	r, err := s.ref(c, r)
	if err != nil {
		return nil, fs.Info{}, err
	}
	rc, in, err := s.provider.Open(ctx, r)
	if err != nil {
		if herr.Kind(err) == fs.CodeNotFound {
			if mi, serr := s.provider.Stat(ctx, r); serr == nil && !mi.Exists {
				s.observed(c, fs.OpRead, mi.Target, false, fs.Version{})
			}
		}
		return nil, fs.Info{}, err
	}
	s.observed(c, fs.OpRead, in.Target, true, in.Version)
	return rc, in, nil
}

// ReadDir lists the directory r.
func (s *Service) ReadDir(ctx context.Context, c Call, r fs.Ref, limit int) ([]fs.DirEntry, bool, error) {
	r, err := s.ref(c, r)
	if err != nil {
		return nil, false, err
	}
	return s.provider.ReadDir(ctx, r, limit)
}

// Write replaces or creates r with data under the call's policy.
func (s *Service) Write(ctx context.Context, c Call, r fs.Ref, data []byte, g fs.Guard) (fs.WriteOutcome, error) {
	r, err := s.ref(c, r)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	p, err := s.policy(ctx, c)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	t, err := s.target(ctx, p, fs.OpWrite, r)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	if err := s.before(c, fs.OpWrite, t); err != nil {
		return fs.WriteOutcome{}, err
	}
	out, err := s.provider.Write(ctx, r, data, g, p)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	s.observed(c, fs.OpWrite, out.Target, true, out.Version)
	return out, nil
}

// Delete removes the file r (the final link itself, never its target).
func (s *Service) Delete(ctx context.Context, c Call, r fs.Ref, g fs.Guard) (fs.DeleteOutcome, error) {
	r, err := s.ref(c, r)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	r.NoFollowFinal = true
	p, err := s.policy(ctx, c)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	t, err := s.target(ctx, p, fs.OpDelete, r)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	if err := s.before(c, fs.OpDelete, t); err != nil {
		return fs.DeleteOutcome{}, err
	}
	out, err := s.provider.Delete(ctx, r, g, p)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	s.observed(c, fs.OpDelete, out.Target, false, fs.Version{})
	return out, nil
}

// Move renames src to dst; both ends are fenced before either event is emitted.
func (s *Service) Move(ctx context.Context, c Call, src, dst fs.Ref, o fs.MoveOptions) (fs.MoveOutcome, error) {
	src, err := s.ref(c, src)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	if dst, err = s.ref(c, dst); err != nil {
		return fs.MoveOutcome{}, err
	}
	src.NoFollowFinal, dst.NoFollowFinal = true, true
	p, err := s.policy(ctx, c)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	st, err := s.target(ctx, p, fs.OpMoveSource, src)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	dt, err := s.target(ctx, p, fs.OpMoveDestination, dst)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	if err := s.before(c, fs.OpMoveSource, st); err != nil {
		return fs.MoveOutcome{}, err
	}
	if err := s.before(c, fs.OpMoveDestination, dt); err != nil {
		return fs.MoveOutcome{}, err
	}
	out, err := s.provider.Move(ctx, src, dst, o, p)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	s.observed(c, fs.OpMoveSource, out.Source, false, fs.Version{})
	if in, err := s.provider.Stat(ctx, fs.Ref{Path: out.Destination.Display, NoFollowFinal: true}); err == nil && in.Exists {
		s.observed(c, fs.OpMoveDestination, out.Destination, true, in.Version)
	}
	return out, nil
}
