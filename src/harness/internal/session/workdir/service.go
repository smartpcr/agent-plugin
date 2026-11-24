package workdir

import (
	"context"
	"errors"
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

// CodeUndelivered: a notice could not be queued in the inbox. What caused it
// (the move, the loss) is in force; the notice is kept and the next Ensure,
// Change or Attach of the session sends it again under the same id.
const CodeUndelivered herr.Reason = "workdir/notice-undelivered"

// ErrUndelivered matches a CodeUndelivered error with errors.Is.
var ErrUndelivered = herr.New(CodeUndelivered, "")

// CodeUndurable: a change was appended (the fold holds it, so it is in force)
// but the session could not flush it. It stays pending: the next Change, Ensure
// or Attach flushes it and only then sends its moved notice; it is never
// appended again.
const CodeUndurable herr.Reason = "workdir/undurable"

// OnlyUndelivered reports that err is a notice delivery failure and nothing
// else: the directory is available, and only a notice is still owed.
func OnlyUndelivered(err error) bool {
	return err != nil && herr.Has(err, CodeUndelivered) && !herr.Has(err, CodeUnavailable)
}

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

// Notice is one message for the agent loop's inbox. ID is stable across the
// redeliveries of one notice, so the inbox refuses a second copy: a moved
// notice is named by its revision (one per logged change), an unavailable one
// by its revision and the loss it reports (each disappearance is a new loss).
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
// cannot import the agent, so the host adapts its inbox to this (the agent
// driver attaches each session with its inbox). A notice the inbox already
// holds is delivered: Notify returns nil for it.
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

// WithNotifier sets where the notices of a session attached without its own
// notifier go; without either they are dropped.
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

// entry is one attached session. mu guards every field and serializes the
// session's changes, checks and reads.
type entry struct {
	mu     sync.Mutex
	s      session.Session
	notify Notifier
	// gone is set when a check found the directory missing; a later check that
	// finds it, or a Change, clears it. Each set starts a new loss.
	gone   bool
	losses int
	// owed is the notices not yet delivered, oldest first.
	owed []Notice
	// pending is a change appended to the session (so its fold already holds
	// it) whose flush failed; it is finished, never appended again, by the
	// next Change, Ensure or Attach.
	pending *pendingChange
}

// pendingChange is an appended change still to be made durable and announced.
type pendingChange struct {
	dir    string
	notice bool
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
// and the directory is checked as Ensure does. n is where the session's notices
// go (the agent's inbox); nil uses the service's WithNotifier. A session whose
// log holds no change yet has the header's project resolved through the seam
// and the workspaces: when its project root or workspace id differs from the
// header's path, one resolve change records them (no notice: the directory did
// not move), so a resume reads them from the log. Attaching a session again
// replaces its handle and notifier for every reader, keeping the notices still
// owed. The record is returned with Ensure's error.
func (s *Service) Attach(ctx context.Context, sess session.Session, n Notifier) (WorkingDirectory, error) {
	if sess == nil {
		return WorkingDirectory{}, fmt.Errorf("workdir: Attach needs the session")
	}
	if n == nil {
		n = s.notify
	}
	s.mu.Lock()
	e, ok := s.sessions[sess.ID()]
	if !ok {
		e = &entry{s: sess, notify: n}
		s.sessions[sess.ID()] = e
	}
	s.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ok {
		e.s, e.notify = sess, n
	}
	if err := s.resolveLocked(ctx, e); err != nil {
		wd, _ := s.read(ctx, e)
		return wd, err
	}
	return s.ensureLocked(ctx, e)
}

// resolveLocked records the root and workspace of the header's project when
// the log has no change yet and they differ from the fold's Init; the caller
// holds e.mu. A directory that is gone is left to the check.
func (s *Service) resolveLocked(ctx context.Context, e *entry) error {
	if err := s.finishLocked(ctx, e); err != nil {
		return err
	}
	wd, err := s.read(ctx, e)
	if err != nil {
		return fmt.Errorf("workdir: read the fold: %w", err)
	}
	if wd.Revision != 0 || wd.Path == "" {
		return nil
	}
	ok, err := s.exists(ctx, wd.Path)
	if err != nil || !ok {
		return err
	}
	root := s.rootOf(ctx, wd.Path)
	ws, err := s.workspaceOf(ctx, e, root)
	if err != nil {
		return err
	}
	if root == wd.Root && ws == wd.Workspace {
		return nil
	}
	_, err = s.recordLocked(ctx, e, ChangePayload{Directory: wd.Path, Workspace: ws, Root: root, Cause: CauseResolve}, false)
	return err
}

func (s *Service) workspaceOf(ctx context.Context, e *entry, root string) (string, error) {
	if s.ws == nil {
		return root, nil
	}
	ws, err := s.ws.WorkspaceOf(ctx, e.s.ID(), root)
	if err != nil {
		return "", fmt.Errorf("workdir: workspace of %s: %w", root, err)
	}
	return ws, nil
}

// Detach stops following a session; its undelivered notices are dropped, and
// every Access of it is workdir/unavailable from now on.
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
// moved notice is queued. Changes of one session are serialized. When only the
// notice could not be queued, the committed record is returned with a
// workdir/notice-undelivered error (OnlyUndelivered): the move stands and the
// notice is sent again by the next check. When the change was appended but not
// flushed, the record is returned with workdir/undurable: the change is in
// force and pending, and a retry (or the next check) flushes it and sends its
// notice without appending it again.
func (s *Service) Change(ctx context.Context, id session.ID, path string) (WorkingDirectory, error) {
	e, err := s.entry(id)
	if err != nil {
		return WorkingDirectory{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return WorkingDirectory{}, err
	}
	if p := e.pending; p != nil {
		// A retry of a change whose flush failed finishes it, not a second one.
		if err := s.finishLocked(ctx, e); err != nil {
			wd, _ := s.read(ctx, e)
			return wd, err
		}
		if dir, err := s.validate(ctx, path); err == nil && dir == p.dir {
			wd, err := s.read(ctx, e)
			if err != nil {
				return WorkingDirectory{}, err
			}
			return wd, s.deliverLocked(ctx, e)
		}
	}
	return s.moveLocked(ctx, e, path, CauseMove)
}

// moveLocked validates path and records the move; the caller holds e.mu. Any
// error other than notice-undelivered or workdir/undurable means nothing was
// recorded.
func (s *Service) moveLocked(ctx context.Context, e *entry, path string, cause Cause) (WorkingDirectory, error) {
	dir, err := s.validate(ctx, path)
	if err != nil {
		return WorkingDirectory{}, err
	}
	root := s.rootOf(ctx, dir)
	ws, err := s.workspaceOf(ctx, e, root)
	if err != nil {
		return WorkingDirectory{}, err
	}
	return s.recordLocked(ctx, e, ChangePayload{Directory: dir, Workspace: ws, Root: root, Cause: cause}, true)
}

// recordLocked appends the change and its context line as one group, then
// finishes it: flushed, and with notice a moved notice queued and sent. Once
// the append succeeds the change is in force; a failed flush keeps it pending
// (finishLocked) and returns the record with a workdir/undurable error. The
// caller holds e.mu.
func (s *Service) recordLocked(ctx context.Context, e *entry, p ChangePayload, notice bool) (WorkingDirectory, error) {
	change, err := ChangeType.Draft(p)
	if err != nil {
		return WorkingDirectory{}, herr.Wrap(CodeInvalid, err, "workdir: record the change")
	}
	line, err := event.RequestContext.Draft(event.RequestContextPayload{Key: ContextKey, Value: ContextLine(p.Directory)})
	if err != nil {
		return WorkingDirectory{}, err
	}
	if _, err := e.s.AppendGroup(ctx, []event.Draft{change, line}); err != nil {
		return WorkingDirectory{}, fmt.Errorf("workdir: append the change: %w", err)
	}
	e.gone = false
	e.pending = &pendingChange{dir: p.Directory, notice: notice}
	if err := s.finishLocked(ctx, e); err != nil {
		wd, rerr := s.read(ctx, e)
		if rerr != nil {
			return WorkingDirectory{}, errors.Join(err, rerr)
		}
		return wd, err
	}
	wd, err := s.read(ctx, e)
	if err != nil {
		return WorkingDirectory{}, err
	}
	return wd, s.deliverLocked(ctx, e)
}

// finishLocked makes e's pending change durable and queues its moved notice;
// the caller holds e.mu. With nothing pending it does nothing. A failed flush
// leaves it pending and is returned as workdir/undurable.
func (s *Service) finishLocked(ctx context.Context, e *entry) error {
	p := e.pending
	if p == nil {
		return nil
	}
	if _, err := e.s.Flush(ctx); err != nil {
		return herr.Wrap(CodeUndurable, err, fmt.Sprintf("workdir: flush the change to %q", p.dir))
	}
	e.pending = nil
	if !p.notice {
		return nil
	}
	wd, err := s.read(ctx, e)
	if err != nil {
		return fmt.Errorf("workdir: read the fold: %w", err)
	}
	s.queue(e, wd, NoticeMoved, fmt.Sprintf("workdir-%s-r%d-moved", e.s.ID(), wd.Revision), ContextLine(wd.Path))
	return nil
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

// exists reports whether p is an existing directory through the seam. A missing
// path, or one that is not a directory, is false with no error; any other
// answer of the seam (a cancelled context, an I/O failure) is its error, which
// is not taken for the directory's absence.
func (s *Service) exists(ctx context.Context, p string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := s.seam.Stat(ctx, fs.Ref{Path: p})
	if err != nil {
		return false, fmt.Errorf("workdir: check %q: %w", p, err)
	}
	return info.Exists && info.Type == fs.TypeDir, nil
}

// Ensure checks session id's directory through the seam: the check before each
// tool call and on resume. A directory that exists is returned. A missing one
// recovers to the header's original project when that exists and differs (a
// recovery change, its context line and a moved notice); otherwise the session
// is workdir/unavailable, and the check that first finds a loss queues one
// unavailable notice for it. A seam failure, a cancelled context or a failed
// fold read is returned as itself and changes nothing; a failed recovery is
// returned inside the workdir/unavailable error. Notices still owed are sent
// after the check, and a failure to send is joined to the result as
// workdir/notice-undelivered. A change whose flush failed is flushed first; while
// it still cannot be, Ensure returns workdir/undurable.
func (s *Service) Ensure(ctx context.Context, id session.ID) (WorkingDirectory, error) {
	e, err := s.entry(id)
	if err != nil {
		return WorkingDirectory{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.ensureLocked(ctx, e)
}

func (s *Service) ensureLocked(ctx context.Context, e *entry) (WorkingDirectory, error) {
	if err := s.finishLocked(ctx, e); err != nil {
		wd, _ := s.read(ctx, e)
		return wd, err
	}
	wd, err := s.read(ctx, e)
	if err != nil {
		return WorkingDirectory{}, fmt.Errorf("workdir: read the fold: %w", err)
	}
	if wd.Path != "" {
		ok, err := s.exists(ctx, wd.Path)
		if err != nil {
			return wd, err
		}
		if ok {
			e.gone = false
			return wd, s.deliverLocked(ctx, e)
		}
	}
	var failed error
	if anchor := initial(e.s.Header()); anchor.Path != "" && anchor.Path != wd.Path {
		ok, err := s.exists(ctx, anchor.Path)
		if err != nil {
			return wd, err
		}
		if ok {
			rec, err := s.moveLocked(ctx, e, anchor.Path, CauseRecovery)
			if err == nil || OnlyUndelivered(err) || herr.Has(err, CodeUndurable) {
				return rec, err
			}
			if ctx.Err() != nil {
				return wd, err
			}
			failed = fmt.Errorf("recovery to %q failed: %w", anchor.Path, err)
		}
	}
	wd.State = Unavailable
	if !e.gone {
		e.gone = true
		// A session that never had a directory has nothing to lose.
		if wd.Path != "" {
			e.losses++
			id := fmt.Sprintf("workdir-%s-r%d-unavailable-%d-%d", e.s.ID(), wd.Revision, s.now().UnixNano(), e.losses)
			s.queue(e, wd, NoticeUnavailable, id, fmt.Sprintf("The working directory %s is no longer available; a person must choose another one before tools can run.", orNone(wd.Path)))
		}
	}
	msg := fmt.Sprintf("working directory %s is unavailable", orNone(wd.Path))
	unavailable := error(herr.New(CodeUnavailable, msg))
	if failed != nil {
		unavailable = herr.Wrap(CodeUnavailable, failed, msg)
	}
	if err := s.deliverLocked(ctx, e); err != nil {
		return wd, errors.Join(unavailable, err)
	}
	return wd, unavailable
}

func orNone(p string) string {
	if p == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", p)
}

// queue adds a notice to the ones e owes; the caller holds e.mu.
func (s *Service) queue(e *entry, wd WorkingDirectory, kind NoticeKind, id, text string) {
	if e.notify == nil {
		return
	}
	e.owed = append(e.owed, Notice{ID: id, Session: e.s.ID(), Kind: kind, Revision: wd.Revision, Path: wd.Path, Text: text, At: s.now()})
}

// deliverLocked sends e's owed notices in order. The first failure stops it:
// that notice and the later ones stay owed, under the same ids, and the
// failure is returned as workdir/notice-undelivered. The caller holds e.mu.
func (s *Service) deliverLocked(ctx context.Context, e *entry) error {
	if e.notify == nil {
		e.owed = nil
		return nil
	}
	for len(e.owed) > 0 {
		n := e.owed[0]
		if err := e.notify.Notify(ctx, n); err != nil {
			return herr.Wrap(CodeUndelivered, err, fmt.Sprintf("workdir: queue notice %s", n.ID))
		}
		e.owed = e.owed[1:]
	}
	return nil
}

// ContextLine is the prompt context line of session id's current directory.
func (s *Service) ContextLine(id session.ID) (string, error) {
	p, err := (&access{s: s, id: id}).Path()
	if err != nil {
		return "", err
	}
	return ContextLine(p), nil
}

// Access implements Lookup: the check a tool call makes before it runs. The
// directory is checked through the seam (Ensure), and its error is returned:
// workdir/unavailable while the directory is gone (or the session is not
// attached), or the seam's or the fold's own failure. Only a notice that could
// not be queued yet does not fail it; the notice stays owed. The Access
// returned reads the fold as it is now, and its methods return
// workdir/unavailable while the directory is gone or the session detached.
func (s *Service) Access(id session.ID) (Access, error) {
	if _, err := s.Ensure(context.Background(), id); err != nil && !OnlyUndelivered(err) {
		return nil, err
	}
	return &access{s: s, id: id}, nil
}

// access is the live read-only view of one session; it finds the session's
// entry on each read, so it follows a re-attach and ends with a detach.
type access struct {
	s  *Service
	id session.ID
}

func (a *access) Current() (WorkingDirectory, error) {
	e, err := a.s.entry(a.id)
	if err != nil {
		return WorkingDirectory{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	wd, err := a.s.read(context.Background(), e)
	if err != nil {
		// A fold that cannot be read is that failure, not a missing directory.
		return WorkingDirectory{}, fmt.Errorf("workdir: read the fold: %w", err)
	}
	if e.gone {
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
