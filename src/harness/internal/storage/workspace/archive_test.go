package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// ------------------------------------------------------- membership and flags

func newSession(t *testing.T, st store.Store, id, cwd string) {
	t.Helper()
	w, err := st.Create(bg, store.Header{ID: id, Cwd: cwd}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// DH-T-STORAGE-16 (part): a session attaches only when its directory resolves
// to the workspace; a second attach elsewhere is refused; deleting a session
// removes its membership and flags.
func TestAttachMembershipAndFlags(t *testing.T) {
	home := t.TempDir()
	db := openDB(t, filepath.Join(home, "dsh.db"))
	reg := must(workspace.Open(bg, db))
	a := mkdir(t, filepath.Join(home, "a"))
	b := mkdir(t, filepath.Join(home, "b"))
	wa, wb := must(reg.Add(bg, a)), must(reg.Add(bg, b))
	newSession(t, db, "s1", a)
	newSession(t, db, "s2", filepath.Join(a, ".", "..", "a"))
	newSession(t, db, "s3", b)

	if err := reg.Attach(bg, wa.ID, "s3"); !errors.Is(err, workspace.ErrOutside) {
		t.Fatalf("attach outside: %v", err)
	}
	if err := reg.Attach(bg, wa.ID, "nope"); !errors.Is(err, workspace.ErrNoSession) {
		t.Fatalf("attach unknown: %v", err)
	}
	for _, id := range []session.ID{"s1", "s2", "s1"} {
		if err := reg.Attach(bg, wa.ID, id); err != nil {
			t.Fatalf("attach %s: %v", id, err)
		}
	}
	if err := reg.Attach(bg, wb.ID, "s3"); err != nil {
		t.Fatal(err)
	}
	if got := must(reg.Members(bg, wa.ID)); len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Fatalf("members of a = %v", got)
	}
	if ws, ok, err := reg.WorkspaceOf(bg, "s3"); err != nil || !ok || ws != wb.ID {
		t.Fatalf("workspace of s3 = %q %v %v", ws, ok, err)
	}

	if err := reg.Pin(bg, "s1", true); err != nil {
		t.Fatal(err)
	}
	if f := must(reg.Flags(bg, "s1")); !f.Pinned || f.Archive != workspace.ArchiveNone {
		t.Fatalf("pinned flags = %+v", f)
	}
	if err := reg.SetArchive(bg, "s1", workspace.ArchiveArchived); err != nil {
		t.Fatal(err)
	}
	if f := must(reg.Flags(bg, "s1")); f.Pinned || f.Archive != workspace.ArchiveArchived || f.ArchiveAt.IsZero() {
		t.Fatalf("archiving keeps no pin: %+v", f)
	}
	if err := reg.Pin(bg, "s1", true); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("pinning an archived session: %v", err)
	}
	if err := reg.SetArchive(bg, "s2", workspace.ArchivePending); err != nil {
		t.Fatal(err)
	}
	if n, err := reg.ClearPending(bg); err != nil || n != 1 {
		t.Fatalf("clear pending = %d %v", n, err)
	}
	if f := must(reg.Flags(bg, "s2")); f.Archive != workspace.ArchiveNone {
		t.Fatalf("stale pending mark kept: %+v", f)
	}

	if err := db.Delete(bg, "s1", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := must(reg.Members(bg, wa.ID)); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("members after delete = %v", got)
	}
	newSession(t, db, "s1", b)
	if f := must(reg.Flags(bg, "s1")); f != (workspace.Flags{}) {
		t.Fatalf("a deleted session's flags survived: %+v", f)
	}
	if _, ok, _ := reg.WorkspaceOf(bg, "s1"); ok {
		t.Fatal("a deleted session's membership survived")
	}
	if err := reg.Remove(bg, wa.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := reg.WorkspaceOf(bg, "s2"); ok {
		t.Fatal("membership survived its workspace")
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatalf("removing the workspace touched its folder: %v", err)
	}
}

// ------------------------------------------------------- archive and hide

// fakeAgent is an agent through the agent contract: a real memstore session and
// scope, no loop; Stop records its causes.
type fakeAgent struct {
	ref     agent.Ref
	h       session.Handle
	sc      *scope.Scope
	mu      sync.Mutex
	running bool
	stops   []agent.StopCause
	failure error
}

func (f *fakeAgent) Ref() agent.Ref               { return f.ref }
func (f *fakeAgent) Session() session.ReadSession { return f.h }
func (f *fakeAgent) Scope() *cordis.Context       { return f.sc.Ctx() }
func (f *fakeAgent) ScopeKey() scope.Key          { return f.sc.Key() }
func (f *fakeAgent) State() agent.State           { return agent.StateLive }
func (f *fakeAgent) Status() agent.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return agent.StatusRunning
	}
	return agent.StatusIdle
}
func (f *fakeAgent) Stop(_ context.Context, cause agent.StopCause) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, cause)
	f.running = false
	return f.failure
}
func (f *fakeAgent) Close(ctx context.Context) error {
	_ = f.sc.Close()
	return f.h.Close(ctx)
}
func (f *fakeAgent) stopped() []agent.StopCause {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.StopCause(nil), f.stops...)
}

type agentEnv struct {
	root  *cordis.Context
	reg   agent.Registry
	marks *workspace.MemoryMarks
	arch  *workspace.Archiver
	fake  *fakeAgent
	id    session.ID
}

// newAgentEnv is a memstore session with one live fake agent, running a turn
// when running is set, and an archiver over in-memory marks and the agent registry.
func newAgentEnv(t *testing.T, running bool) *agentEnv {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	t.Cleanup(root.Dispose)
	svc := session.NewService(root, memstore.New(), session.Config{})
	tab := scope.NewTable(root)
	reg := agent.NewRegistry(root)
	e := &agentEnv{root: root, reg: reg, marks: workspace.NewMemoryMarks()}
	must(reg.SetFactory(root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		h, err := svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: string(o.Session)}})
		if err != nil {
			return nil, err
		}
		sc, err := tab.New(owner, string(o.Session))
		if err != nil {
			return nil, err
		}
		e.fake = &fakeAgent{ref: ref, h: h, sc: sc, running: running}
		return e.fake, nil
	})))
	e.id = "sess-hide"
	h := must(reg.Create(root, agent.CreateOptions{Session: e.id}))
	t.Cleanup(func() { _ = h.Dispose(bg) })
	e.arch = workspace.NewArchiver(e.marks, workspace.AgentProvider(reg))
	return e
}

// TC-08 logic: Hidden session stops working. Given a session with an agent
// registered through the agent contract fake, when the session is hidden, then
// the agent receives a stop with cause hidden, and its next step is rejected.
func TestHiddenSessionStopsWorking(t *testing.T) {
	e := newAgentEnv(t, true)
	must(workspace.GuardSteps(e.root, e.marks, nil))
	a, ok := e.reg.Get(e.id)
	if !ok {
		t.Fatal("no live agent")
	}
	step := agent.PreStepRequest{Ctx: bg, Agent: a.Ref(), Turn: 1, Step: 1}
	if d := must(agent.AskPreStep(e.root, a.ScopeKey(), step)); !isEnter(d) {
		t.Fatalf("a visible session's step: %#v", d)
	}

	if err := e.arch.Hide(bg, e.id); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.stopped(); len(got) != 1 || got[0] != "hidden" {
		t.Fatalf("stops = %v, want one with cause hidden", got)
	}
	if hidden, err := e.arch.Hidden(bg, e.id); err != nil || !hidden {
		t.Fatalf("hidden = %v %v", hidden, err)
	}
	d := must(agent.AskPreStep(e.root, a.ScopeKey(), step))
	if r, ok := d.(agent.Reject); !ok || r.By != workspace.GuardBy {
		t.Fatalf("a hidden session's step: %#v", d)
	}

	if err := e.arch.Unarchive(bg, e.id); err != nil {
		t.Fatal(err)
	}
	if d := must(agent.AskPreStep(e.root, a.ScopeKey(), step)); !isEnter(d) {
		t.Fatalf("an unarchived session's step: %#v", d)
	}
}

func isEnter(d agent.PreStepDecision) bool { _, ok := d.(agent.Enter); return ok }

// DH-T-STORAGE-17 (part): archive of a working session without stop writes
// nothing lasting, names the activity and stops nothing; a failing stop keeps
// the archive; an idle session archives.
func TestArchiveRefusesOrStops(t *testing.T) {
	e := newAgentEnv(t, true)
	err := e.arch.Archive(bg, e.id, workspace.ArchiveOptions{})
	var active *workspace.ActiveError
	if !errors.Is(err, workspace.ErrActive) || !errors.As(err, &active) || len(active.Activity) != 1 || active.Activity[0].Provider != "agent" {
		t.Fatalf("archive of a running session: %v", err)
	}
	if f := must(e.marks.Flags(bg, e.id)); f.Archive != workspace.ArchiveNone {
		t.Fatalf("a refused archive left %q", f.Archive)
	}
	if got := e.fake.stopped(); len(got) != 0 {
		t.Fatalf("a refused archive stopped: %v", got)
	}

	e.fake.failure = errors.New("stuck")
	if err := e.arch.Archive(bg, e.id, workspace.ArchiveOptions{Stop: true}); err == nil {
		t.Fatal("a failing stop was not reported")
	}
	if f := must(e.marks.Flags(bg, e.id)); f.Archive != workspace.ArchiveArchived {
		t.Fatalf("a failing stop dropped the archive: %q", f.Archive)
	}

	idle := newAgentEnv(t, false)
	if err := idle.arch.Archive(bg, idle.id, workspace.ArchiveOptions{}); err != nil {
		t.Fatalf("archive of an idle session: %v", err)
	}
	if f := must(idle.marks.Flags(bg, idle.id)); f.Archive != workspace.ArchiveArchived {
		t.Fatalf("idle archive = %q", f.Archive)
	}
	failing := workspace.NewArchiver(workspace.NewMemoryMarks(), failingProvider{})
	if err := failing.Archive(bg, "x", workspace.ArchiveOptions{}); err == nil {
		t.Fatal("a failing Report did not refuse")
	}
	if hidden := must(failing.Hidden(bg, "x")); hidden {
		t.Fatal("a failing Report left the session hidden")
	}
}

type failingProvider struct{}

func (failingProvider) Name() string { return "failing" }
func (failingProvider) Report(context.Context, session.ID) ([]workspace.Activity, error) {
	return nil, errors.New("cannot tell")
}
func (failingProvider) Stop(context.Context, session.ID, agent.StopCause) error { return nil }

// The step gate refuses a step of a fork whose ancestor is archived.
func TestStepGateWalksForkAncestry(t *testing.T) {
	e := newAgentEnv(t, false)
	parent := func(_ context.Context, id session.ID) (session.ID, bool, error) {
		if id == e.id {
			return "grandparent", true, nil
		}
		return "", false, nil
	}
	must(workspace.GuardSteps(e.root, e.marks, parent))
	if err := e.marks.SetArchive(bg, "grandparent", workspace.ArchivePending); err != nil {
		t.Fatal(err)
	}
	a, _ := e.reg.Get(e.id)
	d := must(agent.AskPreStep(e.root, a.ScopeKey(), agent.PreStepRequest{Ctx: bg, Agent: a.Ref()}))
	if r, ok := d.(agent.Reject); !ok || r.By != workspace.GuardBy {
		t.Fatalf("a step under a pending ancestor: %#v", d)
	}
}
