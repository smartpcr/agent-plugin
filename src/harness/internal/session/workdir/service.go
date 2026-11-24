package workdir

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// CodeInvalid: a Change named a path that is not an absolute, existing
// directory. The seam's own code, when it has one, is the innermost reason.
const CodeInvalid herr.Reason = "workdir/invalid"

// Stater is the part of the filesystem seam the service validates through; every
// fs.FS is one. The service never mutates the filesystem.
type Stater interface {
	Stat(ctx context.Context, r fs.Ref) (fs.Info, error)
}

// Workspaces names the storage/workspace entry a directory belongs to. The
// default (nil) is the root itself, until the workspace registry is wired.
type Workspaces interface {
	WorkspaceOf(ctx context.Context, s session.ID, root string) (string, error)
}

// NoticeKind is what a notice tells the agent.
type NoticeKind string

// The notice kinds.
const (
	// NoticeMoved: the directory moved (a Change or a recovery); Text carries
	// the context line.
	NoticeMoved NoticeKind = "moved"
	// NoticeUnavailable: the directory is gone and no recovery root is valid.
	NoticeUnavailable NoticeKind = "unavailable"
)

// NoticeSource is the inbox source kind of a working-directory notice.
const NoticeSource = "workdir"

// Notice is one message for the agent loop's inbox. ID is deterministic per
// session, revision and kind, so a redelivery is refused as a duplicate.
type Notice struct {
	ID       string
	Session  session.ID
	Kind     NoticeKind
	Revision int64
	Path     string
	Text     string
	At       time.Time
}

// Notifier queues a notice in the session's agent inbox. Session packages
// cannot import the agent, so the host adapts its inbox to this.
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

// NotifierFunc is a func as a Notifier.
type NotifierFunc func(ctx context.Context, n Notice) error

// Notify implements Notifier.
func (f NotifierFunc) Notify(ctx context.Context, n Notice) error { return f(ctx, n) }

// Option configures New.
type Option func(*Service)

// WithClock sets the clock that stamps notices; the default is time.Now.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithWorkspaces sets the workspace lookup.
func WithWorkspaces(w Workspaces) Option { return func(s *Service) { s.ws = w } }

// WithNotifier sets where notices go; without one they are dropped.
func WithNotifier(n Notifier) Option { return func(s *Service) { s.notify = n } }

// Service is the working-directory component (C31): it owns the fold, moves the
// directory for the host (Change), checks it before each tool call and on
// resume (Ensure), and gives readers a Lookup. Changes are serialized per
// session. Nothing a tool can reach moves the directory: Change is a method of
// Service only, never of Lookup or Access.
type Service struct {
	reg    *views.Registry
	h      views.Handle[WorkingDirectory]
	seam   Stater
	ws     Workspaces
	notify Notifier
	now    func() time.Time

	mu       sync.Mutex
	sessions map[session.ID]*entry
}

var _ Lookup = (*Service)(nil)

// entry is one attached session. mu serializes its changes and checks.
type entry struct {
	mu sync.Mutex
	s  session.Session
	// gone is set when a check found the directory missing and the unavailable
	// notice was queued; a later check that finds it, or a Change, clears it.
	gone bool
}

// New registers the workingDirectory fold in reg and returns the service that
// validates through seam.
func New(reg *views.Registry, seam Stater, opts ...Option) (*Service, error) {
	if reg == nil || seam == nil {
		return nil, fmt.Errorf("workdir: the service needs the view registry and the filesystem seam")
	}
	h, err := views.Register(reg, FoldView())
	if err != nil {
		return nil, err
	}
	s := &Service{reg: reg, h: h, seam: seam, now: time.Now, sessions: map[session.ID]*entry{}}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Handle is the fold's typed read.
func (s *Service) Handle() views.Handle[WorkingDirectory] { return s.h }

// Attach follows a created or resumed session: the fold is rebuilt from its log
// and the directory is checked as Ensure does. The record is returned with a
// workdir/unavailable error when the directory is gone.
func (s *Service) Attach(ctx context.Context, sess session.Session) (WorkingDirectory, error) {
	s.mu.Lock()
	e, ok := s.sessions[sess.ID()]
	if !ok {
		e = &entry{s: sess}
		s.sessions[sess.ID()] = e
	}
	e.s = sess
	s.mu.Unlock()
	return s.Ensure(ctx, sess.ID())
}

// Detach stops following a session.
func (s *Service) Detach(id session.ID) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *Service) entry(id session.ID) (*entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.sessions[id]; ok {
		return e, nil
	}
	return nil, herr.Newf(CodeUnavailable, "session %s has no working directory", id)
}

// read is the fold of e's session at its current cut.
func (s *Service) read(ctx context.Context, e *entry) (WorkingDirectory, error) {
	snap, err := s.reg.Read(ctx, e.s, FoldKey)
	if err != nil {
		return WorkingDirectory{}, err
	}
	return s.h.Get(snap)
}

// Change moves session id's working directory to path, for the host. The path
// must be absolute and name an existing directory through the seam; its
// canonical form is recorded with its project root and workspace. The change
// and the prompt context line are appended as one group and flushed, then a
// moved notice is queued. Changes of one session are serialized.
func (s *Service) Change(ctx context.Context, id session.ID, path string) (WorkingDirectory, error) {
	e, err := s.entry(id)
	if err != nil {
		return WorkingDirectory{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.moveLocked(ctx, e, path, CauseMove)
}

// moveLocked validates path and records the move; the caller holds e.mu.
func (s *Service) moveLocked(ctx context.Context, e *entry, path string, cause Cause) (WorkingDirectory, error) {
	dir, err := s.validate(ctx, path)
	if err != nil {
		return WorkingDirectory{}, err
	}
	root := s.rootOf(ctx, dir)
	ws := root
	if s.ws != nil {
		if ws, err = s.ws.WorkspaceOf(ctx, e.s.ID(), root); err != nil {
			return WorkingDirectory{}, fmt.Errorf("workdir: workspace of %s: %w", root, err)
		}
	}
	change, err := ChangeType.Draft(ChangePayload{Directory: dir, Workspace: ws, Root: root, Cause: cause})
	if err != nil {
		return WorkingDirectory{}, herr.Wrap(CodeInvalid, err, "workdir: record the change")
	}
	line, err := event.RequestContext.Draft(event.RequestContextPayload{Key: ContextKey, Value: ContextLine(dir)})
	if err != nil {
		return WorkingDirectory{}, err
	}
	if _, err := e.s.AppendGroup(ctx, []event.Draft{change, line}); err != nil {
		return WorkingDirectory{}, fmt.Errorf("workdir: append the change: %w", err)
	}
	if _, err := e.s.Flush(ctx); err != nil {
		return WorkingDirectory{}, fmt.Errorf("workdir: flush the change: %w", err)
	}
	e.gone = false
	wd, err := s.read(ctx, e)
	if err != nil {
		return WorkingDirectory{}, err
	}
	s.send(ctx, e, wd, NoticeMoved, ContextLine(wd.Path))
	return wd, nil
}

// validate is path's canonical form when it is an absolute, existing directory.
func (s *Service) validate(ctx context.Context, path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", herr.Newf(CodeInvalid, "working directory %q is not an absolute path", path)
	}
	info, err := s.seam.Stat(ctx, fs.Ref{Path: filepath.Clean(path)})
	if err != nil {
		return "", herr.Wrap(CodeInvalid, err, fmt.Sprintf("working directory %q", path))
	}
	if !info.Exists {
		return "", herr.Newf(CodeInvalid, "working directory %q does not exist", path)
	}
	if info.Type != fs.TypeDir {
		return "", herr.Newf(CodeInvalid, "working directory %q is not a directory", path)
	}
	dir := path
	if info.Target.Display != "" {
		dir = info.Target.Display
	}
	return filepath.Clean(dir), nil
}

// rootOf is the project root of dir: the nearest directory at or above it that
// holds a .git entry (a repository, or a linked worktree's own root), else dir.
func (s *Service) rootOf(ctx context.Context, dir string) string {
	for d := dir; ; {
		// An ancestor the seam cannot stat (no permission) is not a root.
		if info, err := s.seam.Stat(ctx, fs.Ref{Path: filepath.Join(d, ".git"), NoFollowFinal: true}); err == nil && info.Exists {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}

// exists reports whether p is an existing directory through the seam.
func (s *Service) exists(ctx context.Context, p string) bool {
	info, err := s.seam.Stat(ctx, fs.Ref{Path: p})
	return err == nil && info.Exists && info.Type == fs.TypeDir
}

// Ensure checks session id's directory through the seam: the check before each
// tool call and on resume. A directory that exists is returned. A missing one
// recovers to the header's original project when that exists and differs (a
// recovery change, its context line and a moved notice); otherwise the session
// is workdir/unavailable, and the first check that finds it so queues one
// unavailable notice.
func (s *Service) Ensure(ctx context.Context, id session.ID) (WorkingDirectory, error) {
	e, err := s.entry(id)
	if err != nil {
		return WorkingDirectory{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	wd, err := s.read(ctx, e)
	if err != nil {
		return WorkingDirectory{}, err
	}
	if wd.Path != "" && s.exists(ctx, wd.Path) {
		e.gone = false
		return wd, nil
	}
	if anchor := initial(e.s.Header()); anchor.Path != "" && anchor.Path != wd.Path && s.exists(ctx, anchor.Path) {
		if rec, err := s.moveLocked(ctx, e, anchor.Path, CauseRecovery); err == nil {
			return rec, nil
		}
	}
	wd.State = Unavailable
	if !e.gone {
		e.gone = true
		s.send(ctx, e, wd, NoticeUnavailable, fmt.Sprintf("The working directory %s is no longer available; a person must choose another one before tools can run.", orNone(wd.Path)))
	}
	return wd, herr.Newf(CodeUnavailable, "working directory %s is unavailable", orNone(wd.Path))
}

func orNone(p string) string {
	if p == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", p)
}

// send queues a notice; a failure is not the change's failure, since the move
// is already in the log and its context line reaches the next request.
func (s *Service) send(ctx context.Context, e *entry, wd WorkingDirectory, kind NoticeKind, text string) {
	if s.notify == nil {
		return
	}
	n := Notice{
		ID:      fmt.Sprintf("workdir-%s-%d-%s", e.s.ID(), wd.Revision, kind),
		Session: e.s.ID(), Kind: kind, Revision: wd.Revision, Path: wd.Path, Text: text, At: s.now(),
	}
	_ = s.notify.Notify(ctx, n)
}

// ContextLine is the prompt context line of session id's current directory.
func (s *Service) ContextLine(id session.ID) (string, error) {
	e, err := s.entry(id)
	if err != nil {
		return "", err
	}
	a := &access{s: s, e: e}
	p, err := a.Path()
	if err != nil {
		return "", err
	}
	return ContextLine(p), nil
}

// Access implements Lookup: the check a tool call makes before it runs. The
// directory is checked through the seam (Ensure); the Access returned reads the
// fold as it is now, and its methods return workdir/unavailable while the
// directory is gone. An unattached session is workdir/unavailable.
func (s *Service) Access(id session.ID) (Access, error) {
	e, err := s.entry(id)
	if err != nil {
		return nil, err
	}
	_, _ = s.Ensure(context.Background(), id)
	return &access{s: s, e: e}, nil
}

// access is the live read-only view of one attached session.
type access struct {
	s *Service
	e *entry
}

func (a *access) Current() (WorkingDirectory, error) {
	a.e.mu.Lock()
	defer a.e.mu.Unlock()
	wd, err := a.s.read(context.Background(), a.e)
	if err != nil {
		return WorkingDirectory{}, herr.Wrap(CodeUnavailable, err, "working directory")
	}
	if a.e.gone {
		wd.State = Unavailable
	}
	if wd.State != Available {
		return wd, herr.Newf(CodeUnavailable, "working directory %s is unavailable", orNone(wd.Path))
	}
	return wd, nil
}

func (a *access) Path() (string, error) {
	wd, err := a.Current()
	if err != nil {
		return "", err
	}
	return wd.Path, nil
}

func (a *access) Root() (string, error) {
	wd, err := a.Current()
	if err != nil {
		return "", err
	}
	return wd.Root, nil
}
