package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

// A provider registered after construction is asked by every later archive,
// and the refusal names each activity under workspace/session-active.
func TestRegisteredProviderRefusesTheArchive(t *testing.T) {
	marks := workspace.NewMemoryMarks()
	arch := workspace.NewArchiver(marks)
	if err := arch.Archive(bg, "s", workspace.ArchiveOptions{}); err != nil {
		t.Fatalf("an archive with no provider: %v", err)
	}
	if err := arch.Unarchive(bg, "s"); err != nil {
		t.Fatal(err)
	}
	arch.Register(busy{})
	err := arch.Archive(bg, "s", workspace.ArchiveOptions{})
	var active *workspace.ActiveError
	if !errors.As(err, &active) || active.Reason() != string(workspace.ReasonActive) {
		t.Fatalf("archive with a registered busy provider = %v", err)
	}
	if msg := active.Error(); !strings.HasPrefix(msg, "workspace/session-active: session s is active (busy: ") {
		t.Fatalf("ActiveError = %q", msg)
	}
	if f := must(marks.Flags(bg, "s")); f.Archive != workspace.ArchiveNone {
		t.Fatalf("the refused archive left %q", f.Archive)
	}
}

// Archiving an archived session without Stop checks nothing and succeeds; a
// hide whose confirm fails (a cancelled context) removes its pending mark and
// stops nothing.
func TestArchiveOfAnArchivedSessionAndACancelledHide(t *testing.T) {
	e := newAgentEnv(t, true)
	if err := e.marks.SetArchive(bg, e.id, workspace.ArchiveArchived); err != nil {
		t.Fatal(err)
	}
	if err := e.arch.Archive(bg, e.id, workspace.ArchiveOptions{}); err != nil {
		t.Fatalf("archive of an archived session = %v", err)
	}
	if got := e.fake.stopped(); len(got) != 0 {
		t.Fatalf("an archive without Stop stopped %v", got)
	}

	f := newAgentEnv(t, true)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if err := f.arch.Hide(ctx, f.id); !errors.Is(err, context.Canceled) {
		t.Fatalf("a hide with a cancelled context = %v", err)
	}
	if fl := must(f.marks.Flags(bg, f.id)); fl.Archive != workspace.ArchiveNone {
		t.Fatalf("a failed hide left %q", fl.Archive)
	}
	if got := f.fake.stopped(); len(got) != 0 {
		t.Fatalf("a failed hide stopped %v", got)
	}
}

// churning is marks whose pending mark changes owner before every transition
// lands, as racing archives in other processes would.
type churning struct {
	n int
}

func (c *churning) Flags(context.Context, session.ID) (workspace.Flags, error) {
	c.n++
	return workspace.Flags{Archive: workspace.ArchivePending, Owner: fmt.Sprintf("other-%d", c.n)}, nil
}
func (*churning) SetArchive(context.Context, session.ID, workspace.ArchiveState) error { return nil }
func (*churning) TransitionArchive(context.Context, session.ID, workspace.Transition) (bool, error) {
	return false, nil
}

// unreadable is marks whose reads fail.
type unreadable struct{ workspace.Marks }

var errMarks = errors.New("marks unreadable")

func (unreadable) Flags(context.Context, session.ID) (workspace.Flags, error) {
	return workspace.Flags{}, errMarks
}

// An archive gives up with workspace/archive-conflict when the mark keeps
// changing under it, and fails with the read error when the marks cannot be
// read; neither asks a provider.
func TestArchiveBeginFailures(t *testing.T) {
	c := &churning{}
	err := workspace.NewArchiver(c, busy{}).Archive(bg, "s", workspace.ArchiveOptions{})
	if !errors.Is(err, workspace.ErrArchiveConflict) || !strings.Contains(err.Error(), "keeps changing") {
		t.Fatalf("archive over a churning mark = %v", err)
	}
	if c.n != 8 {
		t.Fatalf("the archive read the mark %d times, want the 8 tries", c.n)
	}
	if err := workspace.NewArchiver(unreadable{workspace.NewMemoryMarks()}, busy{}).Archive(bg, "s", workspace.ArchiveOptions{}); !errors.Is(err, errMarks) {
		t.Fatalf("archive over unreadable marks = %v", err)
	}
}

// The step gate needs a context and marks; it admits a step whose request has
// no context; it fails the step when the marks cannot be read or the parent
// lookup fails, and ends the walk at a parent the store no longer has.
func TestStepGateEdges(t *testing.T) {
	if _, err := workspace.GuardSteps(nil, workspace.NewMemoryMarks(), nil); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("GuardSteps without a context = %v", err)
	}
	e := newAgentEnv(t, false)
	if _, err := workspace.GuardSteps(e.root, nil, nil); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("GuardSteps without marks = %v", err)
	}
	a, ok := e.reg.Get(e.id)
	if !ok {
		t.Fatal("no live agent")
	}
	gone := func(context.Context, session.ID) (session.ID, bool, error) { return "", false, store.ErrNotFound }
	h := must(workspace.GuardSteps(e.root, e.marks, gone))
	if d := must(agent.AskPreStep(e.root, a.ScopeKey(), agent.PreStepRequest{Agent: a.Ref(), Turn: 1, Step: 1})); !isEnter(d) {
		t.Fatalf("a step with no context, parent gone = %#v", d)
	}
	h.Dispose()

	broken := errors.New("headers unreadable")
	h = must(workspace.GuardSteps(e.root, e.marks, func(context.Context, session.ID) (session.ID, bool, error) { return "", false, broken }))
	if _, err := agent.AskPreStep(e.root, a.ScopeKey(), agent.PreStepRequest{Ctx: bg, Agent: a.Ref(), Turn: 1, Step: 1}); !errors.Is(err, broken) {
		t.Fatalf("a step whose parent lookup fails = %v", err)
	}
	h.Dispose()

	must(workspace.GuardSteps(e.root, unreadable{e.marks}, nil))
	if _, err := agent.AskPreStep(e.root, a.ScopeKey(), agent.PreStepRequest{Ctx: bg, Agent: a.Ref(), Turn: 1, Step: 1}); !errors.Is(err, errMarks) {
		t.Fatalf("a step whose marks cannot be read = %v", err)
	}
}

// The lease provider reports a session whose lease another instance holds,
// not one this instance holds, fails for an unknown session, and its Stop does
// nothing. The agent provider's Stop of a session with no agent is a no-op.
func TestLeaseAndAgentProviders(t *testing.T) {
	home := t.TempDir()
	db := openDB(t, filepath.Join(home, "dsh.db"))
	w, err := db.Create(bg, store.Header{ID: "held"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close(bg) })

	mine := workspace.LeaseProvider(db, db.Instance())
	if mine.Name() != "lease" {
		t.Fatalf("Name = %q", mine.Name())
	}
	if got, err := mine.Report(bg, "held"); err != nil || len(got) != 0 {
		t.Fatalf("this instance's own lease = %+v, %v; want no activity", got, err)
	}
	other := workspace.LeaseProvider(db, "another-instance")
	got, err := other.Report(bg, "held")
	if err != nil || len(got) != 1 || got[0].Provider != "lease" || !strings.Contains(got[0].Detail, fmt.Sprintf("held by process %d", os.Getpid())) {
		t.Fatalf("another instance's view = %+v, %v", got, err)
	}
	if _, err := other.Report(bg, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Report of an unknown session = %v", err)
	}
	if err := other.Stop(bg, "held", agent.StopHidden); err != nil {
		t.Fatalf("lease Stop = %v", err)
	}
	if got, _ := mine.Report(bg, "held"); len(got) != 0 {
		t.Fatal("the lease Stop changed the lease")
	}

	e := newAgentEnv(t, true)
	if err := workspace.AgentProvider(e.reg).Stop(bg, "no-agent", agent.StopHidden); err != nil {
		t.Fatalf("agent Stop of a session with no agent = %v", err)
	}
	if got := e.fake.stopped(); len(got) != 0 {
		t.Fatalf("stopping another session stopped the live agent: %v", got)
	}
}
