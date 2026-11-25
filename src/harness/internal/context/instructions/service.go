package instructions

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// ServiceOptions are a Service's collaborators.
type ServiceOptions struct {
	// FS is the filesystem seam, an fs.BoundedOpener. Required.
	FS fs.FS
	// Workdir gives each session's working directory and project root (C31).
	// Required.
	Workdir workdir.Lookup
	// Pipeline is the request pipeline the contributor joins. Required.
	Pipeline *request.Pipeline
	Config   Config
	// Watch, when set, watches every candidate of each session's chain.
	Watch watch.Engine
}

// Service is the instructions component of a host (C41): one Reconciler per
// session, marked by the seam's fs.Observed touches and the watch and moved by
// the working directory each step reads, and the instructions contributor of
// the pipeline. A resumed session starts a fresh reconciler, and the
// contributor compares it with the record on the surface. The listeners, the
// contributor and every reconciler's watches are effects of the owner; a
// disposed agent's reconciler is closed.
type Service struct {
	o      ServiceOptions
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	rs     map[session.ID]*Reconciler
	closed bool
}

// Start builds the service and registers its listeners and contributor in
// owner's scope.
func Start(owner *cordis.Context, o ServiceOptions) (*Service, error) {
	if owner == nil || o.FS == nil || o.Workdir == nil || o.Pipeline == nil {
		return nil, errors.New("instructions: Start needs an owner, a filesystem seam, a working-directory lookup and a pipeline")
	}
	if _, ok := o.FS.(fs.BoundedOpener); !ok {
		return nil, fmt.Errorf("instructions: the filesystem seam %T cannot read a bounded prefix", o.FS)
	}
	if err := o.Config.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{o: o, ctx: ctx, cancel: cancel, rs: map[session.ID]*Reconciler{}}
	steps := []func() (*cordis.EffectHandle, error){
		func() (*cordis.EffectHandle, error) {
			return owner.Effect("context.instructions", func() (cordis.Disposer, error) { return s.close, nil })
		},
		func() (*cordis.EffectHandle, error) {
			return fs.Observed.On(owner, func(_ *cordis.Context, ob fs.Observation) error {
				s.touch(ob.Session, ob.Target.Display)
				return nil
			})
		},
		func() (*cordis.EffectHandle, error) {
			return agent.Disposed.On(owner, func(_ *cordis.Context, n agent.DisposedNotice) error {
				s.forget(n.Ref.Session)
				return nil
			})
		},
		func() (*cordis.EffectHandle, error) {
			return o.Pipeline.Register(owner, Contributor{For: s.reconciler})
		},
	}
	var done []*cordis.EffectHandle
	for _, step := range steps {
		h, err := step()
		if err != nil {
			for _, d := range done {
				d.Dispose()
			}
			_ = s.close()
			return nil, err
		}
		done = append(done, h)
	}
	return s, nil
}

// reconciler is the session's reconciler, at the working directory the
// session has now; nil when the directory is unavailable (its own notice
// tells the agent).
func (s *Service) reconciler(a agent.Ref) (*Reconciler, error) {
	acc, err := s.o.Workdir.Access(a.Session)
	if err != nil {
		return nil, err
	}
	wd, err := acc.Current()
	if err != nil {
		return nil, err
	}
	if wd.State == workdir.Unavailable {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("instructions: the service is closed")
	}
	r, ok := s.rs[a.Session]
	if !ok {
		if r, err = NewReconciler(s.ctx, Options{FS: s.o.FS, Config: s.o.Config, Watch: s.o.Watch}); err != nil {
			return nil, err
		}
		s.rs[a.Session] = r
	}
	r.SetDirectory(wd.Path, wd.Root)
	return r, nil
}

// touch marks the chain of session id when path is one of its candidates.
func (s *Service) touch(id session.ID, path string) {
	s.mu.Lock()
	r := s.rs[id]
	s.mu.Unlock()
	if r != nil {
		r.Touch(path)
	}
}

// forget closes the reconciler of a session whose agent was disposed.
func (s *Service) forget(id session.ID) {
	s.mu.Lock()
	r := s.rs[id]
	delete(s.rs, id)
	s.mu.Unlock()
	if r != nil {
		_ = r.Close()
	}
}

func (s *Service) close() error {
	s.mu.Lock()
	rs := s.rs
	s.rs, s.closed = map[session.ID]*Reconciler{}, true
	s.mu.Unlock()
	var errs []error
	for _, r := range rs {
		errs = append(errs, r.Close())
	}
	s.cancel()
	return errors.Join(errs...)
}
