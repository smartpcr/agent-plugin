package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// ReasonActive is an archive refused because the session is working; the
// error is an *ActiveError naming what runs.
const ReasonActive herr.Reason = "workspace/session-active"

// ErrActive matches an archive refused for activity.
var ErrActive = herr.New(ReasonActive, "")

// ReasonArchiveConflict is an archive whose mark another operation changed
// before it could confirm (an unarchive in between).
const ReasonArchiveConflict herr.Reason = "workspace/archive-conflict"

// ErrArchiveConflict matches ReasonArchiveConflict.
var ErrArchiveConflict = herr.New(ReasonArchiveConflict, "")

// Marks is where archive marks live: the Registry's session_flags rows, or
// MemoryMarks for a single process without a database. TransitionArchive is
// atomic: it is how concurrent archives (in any process) never clobber each
// other's marks.
type Marks interface {
	Flags(ctx context.Context, id session.ID) (Flags, error)
	SetArchive(ctx context.Context, id session.ID, s ArchiveState) error
	TransitionArchive(ctx context.Context, id session.ID, t Transition) (bool, error)
}

var _ Marks = (*Registry)(nil)

// MemoryMarks is Marks held in memory: for one process on a store without
// machine-state tables (the in-memory store of tests). Rows, not this, are what
// works across processes (DH-ADR-STORAGE-7).
type MemoryMarks struct {
	mu    sync.Mutex
	now   func() time.Time
	flags map[session.ID]Flags
}

// NewMemoryMarks is empty MemoryMarks.
func NewMemoryMarks() *MemoryMarks {
	return &MemoryMarks{now: time.Now, flags: map[session.ID]Flags{}}
}

// Flags is id's flags.
func (m *MemoryMarks) Flags(_ context.Context, id session.ID) (Flags, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flags[id], nil
}

// SetArchive sets id's archive mark; any mark but none clears the pin.
func (m *MemoryMarks) SetArchive(_ context.Context, id session.ID, s ArchiveState) error {
	if s != ArchiveNone && s != ArchivePending && s != ArchiveArchived {
		return herr.Newf(ReasonInvalid, "archive state %q", s)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set(id, s, "")
	return nil
}

func (m *MemoryMarks) set(id session.ID, s ArchiveState, owner string) {
	f := m.flags[id]
	f.Archive, f.ArchiveAt, f.Owner = s, m.now().UTC(), owner
	if s != ArchiveNone {
		f.Pinned = false
	}
	m.flags[id] = f
}

// TransitionArchive applies t to id's mark when it matches.
func (m *MemoryMarks) TransitionArchive(_ context.Context, id session.ID, t Transition) (bool, error) {
	if err := t.valid(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.matches(m.flags[id]) {
		return false, nil
	}
	m.set(id, t.To, t.owner())
	return true, nil
}

// Activity is one thing working on a session, as a provider reports it.
type Activity struct {
	// Provider names the reporting provider.
	Provider string
	// Detail says what runs, for the user.
	Detail string
}

// ActivityProvider is one source of work on sessions: the driver's agents,
// subagents, jobs, schedules, another process's lease. Report says what runs on
// a session now; Stop ends it with cause. Providers register with the Archiver.
type ActivityProvider interface {
	Name() string
	Report(ctx context.Context, id session.ID) ([]Activity, error)
	Stop(ctx context.Context, id session.ID, cause agent.StopCause) error
}

// ActiveError is an archive refused because the session is working.
type ActiveError struct {
	Session  session.ID
	Activity []Activity
}

func (e *ActiveError) Error() string {
	parts := make([]string, len(e.Activity))
	for i, a := range e.Activity {
		parts[i] = a.Provider + ": " + a.Detail
	}
	return fmt.Sprintf("%s: session %s is active (%s)", ReasonActive, e.Session, strings.Join(parts, "; "))
}

// Reason is workspace/session-active.
func (e *ActiveError) Reason() string { return string(ReasonActive) }

// Is matches ErrActive.
func (e *ActiveError) Is(target error) bool { return target == ErrActive }

// ArchiveOptions configures Archive.
type ArchiveOptions struct {
	// Stop archives a working session by stopping its work first; without it a
	// working session's archive is refused.
	Stop bool
}

// Archiver hides sessions so that a hidden session never keeps working
// (DH-D-STORAGE-7, DH-ADR-STORAGE-7). Safe for concurrent use.
type Archiver struct {
	marks Marks

	mu        sync.Mutex
	providers []ActivityProvider
}

// NewArchiver is an Archiver writing marks to m, asking providers.
func NewArchiver(m Marks, providers ...ActivityProvider) *Archiver {
	return &Archiver{marks: m, providers: append([]ActivityProvider(nil), providers...)}
}

// Register adds p to the providers asked by every later archive.
func (a *Archiver) Register(p ActivityProvider) {
	a.mu.Lock()
	a.providers = append(a.providers, p)
	a.mu.Unlock()
}

func (a *Archiver) list() []ActivityProvider {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ActivityProvider(nil), a.providers...)
}

// Archive hides session id. It writes a pending mark owned by this operation
// first (which the step gate already treats as blocked, closing the window
// between the check and the write; another operation's pending mark is taken
// over), then asks every provider. Without Stop, any activity or a failing
// Report removes the mark and refuses: *ActiveError (workspace/session-active)
// or the Report error; with none the mark is confirmed. With Stop the mark is
// confirmed first and then every provider is asked to stop with cause hidden;
// the archive stands even when a provider fails, and the failures are returned.
// Every change after the first is conditional on this operation still holding
// the pending mark, so an overlapping archive or hide (in any process) is never
// undone by this one's rollback; a mark unarchived before the confirm is
// workspace/archive-conflict. Archiving an archived session checks nothing (a
// hide still stops its work).
func (a *Archiver) Archive(ctx context.Context, id session.ID, o ArchiveOptions) error {
	owner, err := newOwner()
	if err != nil {
		return err
	}
	archived, err := a.begin(ctx, id, owner)
	if err != nil {
		return err
	}
	providers := a.list()
	if o.Stop {
		if !archived {
			if err := a.confirm(ctx, id, owner); err != nil {
				return errors.Join(err, a.rollback(ctx, id, owner))
			}
		}
		var errs []error
		for _, p := range providers {
			if err := p.Stop(ctx, id, agent.StopHidden); err != nil {
				errs = append(errs, fmt.Errorf("%s: stopping session %s: %w", p.Name(), id, err))
			}
		}
		return errors.Join(errs...)
	}
	if archived {
		return nil
	}
	var active []Activity
	for _, p := range providers {
		got, err := p.Report(ctx, id)
		if err != nil {
			return errors.Join(fmt.Errorf("%s: activity of session %s: %w", p.Name(), id, err), a.rollback(ctx, id, owner))
		}
		active = append(active, got...)
	}
	if len(active) > 0 {
		return errors.Join(&ActiveError{Session: id, Activity: active}, a.rollback(ctx, id, owner))
	}
	return a.confirm(ctx, id, owner)
}

// maxTakeover bounds begin's retries against a mark that keeps changing.
const maxTakeover = 8

// begin writes owner's pending mark over none or another operation's pending
// mark; archived is true (and nothing is written) when id is already archived.
func (a *Archiver) begin(ctx context.Context, id session.ID, owner string) (archived bool, err error) {
	for range maxTakeover {
		f, err := a.marks.Flags(ctx, id)
		if err != nil {
			return false, err
		}
		if f.Archive == ArchiveArchived {
			return true, nil
		}
		ok, err := a.marks.TransitionArchive(ctx, id, Transition{From: f.Archive, Owner: f.Owner, To: ArchivePending, NewOwner: owner})
		if err != nil || ok {
			return false, err
		}
	}
	return false, herr.Newf(ReasonArchiveConflict, "session %s: its archive mark keeps changing", id)
}

// confirm turns owner's pending mark into archived. A mark another operation
// already archived is success; anything else (unarchived) is a conflict.
func (a *Archiver) confirm(ctx context.Context, id session.ID, owner string) error {
	ok, err := a.marks.TransitionArchive(ctx, id, Transition{From: ArchivePending, Owner: owner, To: ArchiveArchived})
	if err != nil || ok {
		return err
	}
	f, err := a.marks.Flags(ctx, id)
	if err != nil || f.Archive == ArchiveArchived {
		return err
	}
	return herr.Newf(ReasonArchiveConflict, "session %s's archive mark changed to %q before the archive was confirmed", id, f.Archive)
}

// rollback removes owner's pending mark, if it still holds it.
func (a *Archiver) rollback(ctx context.Context, id session.ID, owner string) error {
	_, err := a.marks.TransitionArchive(context.WithoutCancel(ctx), id, Transition{From: ArchivePending, Owner: owner, To: ArchiveNone})
	return err
}

func newOwner() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Hide is Archive with Stop: a hidden session never keeps working, so hiding
// stops its agents with cause hidden.
func (a *Archiver) Hide(ctx context.Context, id session.ID) error {
	return a.Archive(ctx, id, ArchiveOptions{Stop: true})
}

// Unarchive shows session id again; its steps are no longer blocked.
func (a *Archiver) Unarchive(ctx context.Context, id session.ID) error {
	return a.marks.SetArchive(ctx, id, ArchiveNone)
}

// Hidden reports whether session id is pending or archived.
func (a *Archiver) Hidden(ctx context.Context, id session.ID) (bool, error) {
	f, err := a.marks.Flags(ctx, id)
	return f.Archive.Hidden(), err
}

// --------------------------------------------------------------- providers

// AgentProvider is the activity of the agent registry: a session's live agent
// running a turn is activity, and Stop asks that agent to stop (agent.Stop).
func AgentProvider(r agent.Registry) ActivityProvider { return agentProvider{r} }

type agentProvider struct{ r agent.Registry }

func (agentProvider) Name() string { return "agent" }

func (p agentProvider) Report(_ context.Context, id session.ID) ([]Activity, error) {
	a, ok := p.r.Get(id)
	if !ok || a.Status() != agent.StatusRunning {
		return nil, nil
	}
	return []Activity{{Provider: "agent", Detail: "agent " + a.Ref().String() + " is running a turn"}}, nil
}

func (p agentProvider) Stop(ctx context.Context, id session.ID, cause agent.StopCause) error {
	a, ok := p.r.Get(id)
	if !ok {
		return nil
	}
	return agent.Stop(ctx, a, cause)
}

// LeaseProvider reports a session whose lease another process holds as
// activity; self is this process's store instance. Its Stop does nothing: that
// process stops its own work when it sees the flag.
func LeaseProvider(st store.Store, self string) ActivityProvider { return leaseProvider{st, self} }

type leaseProvider struct {
	st   store.Store
	self string
}

func (leaseProvider) Name() string { return "lease" }

func (p leaseProvider) Report(ctx context.Context, id session.ID) ([]Activity, error) {
	sum, err := p.st.Stat(ctx, string(id))
	if err != nil {
		return nil, err
	}
	if h := sum.Holder; h != nil && h.Instance != p.self {
		return []Activity{{Provider: "lease", Detail: fmt.Sprintf("held by process %d (%s)", h.PID, h.Kind)}}, nil
	}
	return nil, nil
}

func (leaseProvider) Stop(context.Context, session.ID, agent.StopCause) error { return nil }

// --------------------------------------------------------------- step gate

// Parent is the fork parent of a session; ok is false for a root session.
type Parent func(ctx context.Context, id session.ID) (parent session.ID, ok bool, err error)

// ForkParent is Parent from the store's headers.
func ForkParent(st store.Store) Parent {
	return func(ctx context.Context, id session.ID) (session.ID, bool, error) {
		sum, err := st.Stat(ctx, string(id))
		if err != nil {
			return "", false, err
		}
		return session.ID(sum.Header.ForkSource), sum.Header.ForkSource != "", nil
	}
}

// GuardBy is the By of the step gate's Reject.
const GuardBy = "workspace/archived"

// maxAncestry bounds the fork chain the gate walks.
const maxAncestry = 64

// GuardSteps registers, as an effect of ctx's fiber, the deny-only step gate on
// agent.PreStep: a step of a session that is pending or archived, or whose fork
// ancestor is, is rejected; any other step goes on downstream unchanged. parent
// may be nil (no ancestry walk).
func GuardSteps(ctx *cordis.Context, m Marks, parent Parent) (*cordis.EffectHandle, error) {
	if ctx == nil || m == nil {
		return nil, herr.New(ReasonInvalid, "the step gate needs a context and marks")
	}
	return agent.PreStep.On(ctx, func(_ *cordis.Context, req agent.PreStepRequest, next func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		c := req.Ctx
		if c == nil {
			c = context.Background()
		}
		id := req.Agent.Session
		for depth := 0; id != "" && depth < maxAncestry; depth++ {
			f, err := m.Flags(c, id)
			if err != nil {
				return nil, err
			}
			if f.Archive.Hidden() {
				reason := fmt.Sprintf("session %s is %s", id, f.Archive)
				if id != req.Agent.Session {
					reason = fmt.Sprintf("session %s's ancestor %s is %s", req.Agent.Session, id, f.Archive)
				}
				return agent.Reject{By: GuardBy, Reason: reason}, nil
			}
			if parent == nil {
				break
			}
			p, ok, err := parent(c, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			id = p
		}
		return next(req)
	})
}
