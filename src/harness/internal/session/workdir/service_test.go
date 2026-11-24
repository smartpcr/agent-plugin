package workdir_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// notices records what the service queues for the inbox. Like the agent's
// inbox adapter, an id it already holds is delivered again as a no-op, so a
// reused id loses the notice; fail makes that many Notify calls fail first.
type notices struct {
	mu    sync.Mutex
	got   []workdir.Notice
	fail  int
	tries int
}

var errInboxDown = errors.New("inbox down")

func (n *notices) Notify(_ context.Context, x workdir.Notice) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tries++
	if n.fail > 0 {
		n.fail--
		return errInboxDown
	}
	for _, g := range n.got {
		if g.ID == x.ID {
			return nil
		}
	}
	n.got = append(n.got, x)
	return nil
}

func (n *notices) all() []workdir.Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]workdir.Notice(nil), n.got...)
}

// rig is one process: a session service over st whose folds are a fresh
// registry, and the working-directory service over seam.
type rig struct {
	svc session.Service
	wd  *workdir.Service
	in  *notices
}

func newRig(t *testing.T, st store.Store, seam workdir.Stater, now func() time.Time) *rig {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	reg := views.New()
	in := &notices{}
	wd := must(workdir.New(reg, seam, workdir.WithNotifier(in), workdir.WithClock(now)))
	return &rig{svc: session.NewService(root, st, session.Config{Folder: reg}), wd: wd, in: in}
}

func (r *rig) create(t *testing.T, cwd string) session.Handle {
	t.Helper()
	h := must(r.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{Cwd: cwd}, Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	return h
}

// changes is the log's workspace-root/change events and, for each, the
// request/context line right after it.
func changes(t *testing.T, s session.ReadSession) ([]workdir.ChangePayload, []string) {
	t.Helper()
	evs := must(s.Read(bg, 0, 0))
	var cs []workdir.ChangePayload
	var lines []string
	for i, ev := range evs {
		if ev.Type() != workdir.ChangeType.Name() {
			continue
		}
		cs = append(cs, must(workdir.ChangeType.As(ev)))
		if i+1 >= len(evs) || evs[i+1].Type() != event.RequestContext.Name() {
			t.Fatalf("change at seq %d is not followed by its context line", ev.Seq())
		}
		p := must(event.RequestContext.As(evs[i+1]))
		if p.Key != workdir.ContextKey {
			t.Fatalf("context key %q", p.Key)
		}
		lines = append(lines, p.Value)
	}
	return cs, lines
}

// Scenario: Fold rebuilt from the log (TC-01a).
func TestFoldRebuiltFromTheLog(t *testing.T) {
	base := filepath.Join(t.TempDir(), "proj")
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	seam := fstest.NewMemFS()
	for _, d := range []string{a, b, filepath.Join(base, ".git")} {
		if err := seam.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	st := memstore.New()
	first := newRig(t, st, seam, func() time.Time { return at })
	h := first.create(t, base)
	must(first.wd.Attach(bg, h, nil))
	must(first.wd.Change(bg, h.ID(), a))
	must(first.wd.Change(bg, h.ID(), b))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	second := newRig(t, st, seam, time.Now)
	h2, _, err := second.svc.Open(bg, nil, h.ID(), session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close(bg) })
	got := must(second.wd.Attach(bg, h2, nil))
	acc := must(second.wd.Access(h2.ID()))
	if p := must(acc.Path()); p != b {
		t.Fatalf("Path after the rebuild = %q, want %q", p, b)
	}
	if got.Workspace != base || got.Root != base || got.Revision != 2 || got.State != workdir.Available {
		t.Fatalf("rebuilt fold = %+v, want workspace and root %q at revision 2", got, base)
	}
	evs := must(h2.Read(bg, 0, 0))
	if ref := views.Refold(workdir.FoldView(), h2.Header(), evs); ref != got {
		t.Fatalf("refold %+v differs from the service's %+v", ref, got)
	}
	if line := must(second.wd.ContextLine(h2.ID())); line != "Working directory: "+b {
		t.Fatalf("context line %q", line)
	}
	moved := first.in.all()
	if len(moved) != 2 || moved[1].Kind != workdir.NoticeMoved || moved[1].Text != workdir.ContextLine(b) || !moved[1].At.Equal(at) {
		t.Fatalf("notices = %+v, want two moved notices stamped by the fake clock", moved)
	}
	if len(second.in.all()) != 0 {
		t.Fatalf("a resume of an existing directory queued %+v", second.in.all())
	}
}

// Scenario: Concurrent changes serialize (TC-01a).
func TestConcurrentChangesSerialize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "proj")
		a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
		seam := fstest.NewMemFS()
		for _, d := range []string{a, b} {
			if err := seam.Mkdir(d); err != nil {
				t.Fatal(err)
			}
		}
		r := newRig(t, memstore.New(), seam, time.Now)
		h := r.create(t, base)
		must(r.wd.Attach(bg, h, nil))

		var wg sync.WaitGroup
		for _, p := range []string{a, b} {
			wg.Go(func() {
				if _, err := r.wd.Change(bg, h.ID(), p); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()

		cs, lines := changes(t, h)
		if len(cs) != 2 || cs[0].Directory == cs[1].Directory {
			t.Fatalf("log holds %+v, want one change to each directory", cs)
		}
		for i, c := range cs {
			if lines[i] != workdir.ContextLine(c.Directory) || c.Cause != workdir.CauseMove {
				t.Fatalf("change %d %+v has context line %q", i, c, lines[i])
			}
		}
		acc := must(r.wd.Access(h.ID()))
		cur := must(acc.Current())
		if cur.Path != cs[1].Directory || cur.Revision != 2 {
			t.Fatalf("fold %+v, want the later change %q at revision 2", cur, cs[1].Directory)
		}
		got := r.in.all()
		if len(got) != 2 || got[0].Revision != 1 || got[1].Revision != 2 || got[1].Path != cs[1].Directory {
			t.Fatalf("notices %+v are not one per change in log order", got)
		}
	})
}

// A Change names an absolute, existing directory; a tool-facing reader has no
// way to move it, and an unknown session is unavailable (TC-01a).
func TestChangeValidatesThroughTheSeam(t *testing.T) {
	base := filepath.Join(t.TempDir(), "proj")
	seam := fstest.NewMemFS()
	if err := seam.Mkdir(base); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "f.txt")
	if _, err := seam.Write(bg, fs.Ref{Path: file}, []byte("x"), fs.NoGuard, dangerPolicy()); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, memstore.New(), seam, time.Now)
	h := r.create(t, base)
	must(r.wd.Attach(bg, h, nil))
	for _, bad := range []string{"", "rel/dir", filepath.Join(base, "missing"), file} {
		if _, err := r.wd.Change(bg, h.ID(), bad); err == nil || !herr.Has(err, workdir.CodeInvalid) {
			t.Errorf("Change(%q) = %v, want workdir/invalid", bad, err)
		}
	}
	if cs, _ := changes(t, h); len(cs) != 0 {
		t.Fatalf("refused changes were logged: %+v", cs)
	}
	if _, err := r.wd.Change(bg, "nobody", base); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Change of an unknown session: %v", err)
	}
	if _, err := r.wd.Access("nobody"); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Access of an unknown session: %v", err)
	}
}

func dangerPolicy() sandbox.Policy { return sandboxtest.Policy(sandbox.DangerFullAccess) }

// osSeam is the seam's Stat over the real filesystem, as the local provider
// answers it: symlinks resolved to the canonical path, a missing file not an
// error (TC-01b).
type osSeam struct{}

func (osSeam) Stat(_ context.Context, r fs.Ref) (fs.Info, error) {
	fi, err := os.Lstat(r.Path)
	if os.IsNotExist(err) {
		return fs.Info{Target: fs.Target{Display: r.Path}}, nil
	}
	if err != nil {
		return fs.Info{}, herr.Wrap(fs.CodeIO, err, "stat")
	}
	disp := r.Path
	if !r.NoFollowFinal {
		if disp, err = filepath.EvalSymlinks(r.Path); err != nil {
			return fs.Info{Target: fs.Target{Display: r.Path}}, nil
		}
		if fi, err = os.Stat(disp); err != nil {
			return fs.Info{}, herr.Wrap(fs.CodeIO, err, "stat")
		}
	}
	typ := fs.TypeFile
	if fi.IsDir() {
		typ = fs.TypeDir
	}
	return fs.Info{Target: fs.Target{Key: fs.TargetKey(disp), Display: disp}, Exists: true, Type: typ, Size: fi.Size()}, nil
}

func canon(t *testing.T, p string) string {
	t.Helper()
	return must(filepath.EvalSymlinks(p))
}

// Scenario: Deleted directory reported (TC-01b).
func TestDeletedDirectoryIsReportedOnce(t *testing.T) {
	sub := filepath.Join(canon(t, t.TempDir()), "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, memstore.New(), osSeam{}, time.Now)
	h := r.create(t, sub)
	must(r.wd.Attach(bg, h, nil))
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}

	acc, err := r.wd.Access(h.ID())
	if acc != nil || !herr.Has(err, workdir.CodeUnavailable) {
		t.Fatalf("Access of a deleted directory = %v, %v; want workdir/unavailable", acc, err)
	}
	if _, err := r.wd.Ensure(bg, h.ID()); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Ensure of a deleted directory: %v", err)
	}
	if _, err := r.wd.Access(h.ID()); !herr.Has(err, workdir.CodeUnavailable) {
		t.Fatalf("the next Access: %v", err)
	}
	got := r.in.all()
	if len(got) != 1 || got[0].Kind != workdir.NoticeUnavailable || got[0].Path != sub {
		t.Fatalf("notices %+v, want exactly one unavailable notice", got)
	}

	// The directory comes back: available again, and a later loss is a new
	// notice with its own id, which a deduplicating inbox admits.
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if p := must(must(r.wd.Access(h.ID())).Path()); p != sub {
		t.Fatalf("Path after the directory returned = %q", p)
	}
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if _, err := r.wd.Access(h.ID()); !herr.Has(err, workdir.CodeUnavailable) {
		t.Fatalf("Access after the second loss: %v", err)
	}
	if _, err := r.wd.Access(h.ID()); !herr.Has(err, workdir.CodeUnavailable) {
		t.Fatalf("Access again in the second loss: %v", err)
	}
	got = r.in.all()
	if len(got) != 2 || got[1].Kind != workdir.NoticeUnavailable || got[0].ID == got[1].ID {
		t.Fatalf("notices %+v, want one more unavailable notice with a new id", got)
	}
}

// A moved directory recovers to the original project: a recovery change with its
// context line, and a moved notice (TC-01b).
func TestMovedDirectoryRecoversToTheProject(t *testing.T) {
	proj := canon(t, t.TempDir())
	sub := filepath.Join(proj, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, memstore.New(), osSeam{}, time.Now)
	h := r.create(t, proj)
	must(r.wd.Attach(bg, h, nil))
	moved := must(r.wd.Change(bg, h.ID(), sub))
	if moved.Path != sub || moved.Root != sub || moved.Revision != 1 {
		t.Fatalf("Change = %+v", moved)
	}
	if err := os.Rename(sub, filepath.Join(proj, "elsewhere")); err != nil {
		t.Fatal(err)
	}

	acc := must(r.wd.Access(h.ID()))
	if p := must(acc.Path()); p != proj {
		t.Fatalf("Path after the move = %q, want the project %q", p, proj)
	}
	cs, lines := changes(t, h)
	if len(cs) != 2 || cs[1].Cause != workdir.CauseRecovery || cs[1].Directory != proj || lines[1] != "Working directory: "+proj {
		t.Fatalf("log %+v / %q, want a recovery to %q with its context line", cs, lines, proj)
	}
	got := r.in.all()
	if len(got) != 2 || got[1].Kind != workdir.NoticeMoved || got[1].Path != proj || got[1].Revision != 2 {
		t.Fatalf("notices %+v, want a moved notice for the recovery", got)
	}
}

// A notice the inbox refuses is kept, not lost: the committed move is returned
// with workdir/notice-undelivered, and the next check sends it under the same
// id without logging the move again; the same holds for an unavailable notice
// (TC-01b).
func TestUndeliveredNoticeIsKeptAndRetried(t *testing.T) {
	proj := canon(t, t.TempDir())
	sub := filepath.Join(proj, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, memstore.New(), osSeam{}, time.Now)
	h := r.create(t, sub)
	must(r.wd.Attach(bg, h, nil))

	r.in.fail = 1
	wd, err := r.wd.Change(bg, h.ID(), proj)
	if !workdir.OnlyUndelivered(err) || !errors.Is(err, workdir.ErrUndelivered) || !errors.Is(err, errInboxDown) {
		t.Fatalf("Change with a failing inbox: %v", err)
	}
	if wd.Path != proj || wd.Revision != 1 {
		t.Fatalf("Change returned %+v, want the committed move", wd)
	}
	if n := len(r.in.all()); n != 0 {
		t.Fatalf("%d notices delivered by a failing inbox", n)
	}
	if _, err := r.wd.Ensure(bg, h.ID()); err != nil {
		t.Fatalf("Ensure retrying the notice: %v", err)
	}
	if cs, _ := changes(t, h); len(cs) != 1 {
		t.Fatalf("the retry logged the move again: %+v", cs)
	}
	got := r.in.all()
	if len(got) != 1 || got[0].Kind != workdir.NoticeMoved || got[0].ID != fmt.Sprintf("workdir-%s-r1-moved", h.ID()) {
		t.Fatalf("notices %+v, want the moved notice once", got)
	}

	// The project goes too (and the header's sub with it): unavailable, and its
	// notice waits for the inbox.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	r.in.fail = 1
	_, err = r.wd.Ensure(bg, h.ID())
	if !herr.Has(err, workdir.CodeUnavailable) || !errors.Is(err, workdir.ErrUndelivered) || workdir.OnlyUndelivered(err) {
		t.Fatalf("Ensure of a lost directory with a failing inbox: %v", err)
	}
	if _, err := r.wd.Ensure(bg, h.ID()); herr.Kind(err) != workdir.CodeUnavailable || errors.Is(err, workdir.ErrUndelivered) {
		t.Fatalf("Ensure retrying the unavailable notice: %v", err)
	}
	// A third check owes nothing and sends nothing.
	_, _ = r.wd.Ensure(bg, h.ID())
	got = r.in.all()
	if len(got) != 2 || got[1].Kind != workdir.NoticeUnavailable {
		t.Fatalf("notices %+v, want the unavailable notice delivered once", got)
	}
	if r.in.tries != 4 {
		t.Fatalf("%d deliveries tried, want 4 (two failed, two sent)", r.in.tries)
	}
}

// faultSeam fails Stat of one path with err.
type faultSeam struct {
	mu   sync.Mutex
	path string
	err  error
}

func (f *faultSeam) set(path string, err error) {
	f.mu.Lock()
	f.path, f.err = path, err
	f.mu.Unlock()
}

func (f *faultSeam) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	f.mu.Lock()
	p, err := f.path, f.err
	f.mu.Unlock()
	if err != nil && r.Path == p {
		return fs.Info{}, err
	}
	return osSeam{}.Stat(ctx, r)
}

// A check the seam cannot answer, or whose context is cancelled, is that
// failure, not a lost directory: Access returns it, and no notice is queued.
func TestCheckFailuresAreNotLosses(t *testing.T) {
	dir := canon(t, t.TempDir())
	seam := &faultSeam{}
	r := newRig(t, memstore.New(), seam, time.Now)
	h := r.create(t, dir)
	must(r.wd.Attach(bg, h, nil))

	seam.set(dir, herr.Newf(fs.CodeIO, "disk on fire"))
	acc, err := r.wd.Access(h.ID())
	if acc != nil || !herr.Has(err, fs.CodeIO) || herr.Has(err, workdir.CodeUnavailable) {
		t.Fatalf("Access with a failing seam = %v, %v; want the seam's fs/io", acc, err)
	}
	seam.set("", nil)

	cancelled, cancel := context.WithCancel(bg)
	cancel()
	if _, err := r.wd.Ensure(cancelled, h.ID()); !errors.Is(err, context.Canceled) || herr.Has(err, workdir.CodeUnavailable) {
		t.Fatalf("Ensure with a cancelled context: %v", err)
	}
	if _, err := r.wd.Change(cancelled, h.ID(), dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("Change with a cancelled context: %v", err)
	}
	if p := must(must(r.wd.Access(h.ID())).Path()); p != dir {
		t.Fatalf("Path once the seam answers = %q", p)
	}
	if got := r.in.all(); len(got) != 0 {
		t.Fatalf("failed checks queued %+v", got)
	}
}

type failingWorkspaces struct{ err error }

func (f *failingWorkspaces) WorkspaceOf(_ context.Context, _ session.ID, root string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return root, nil
}

// A recovery that cannot be recorded is reported inside workdir/unavailable,
// with its cause, and leaves the log as it was (TC-01b).
func TestFailedRecoveryIsReported(t *testing.T) {
	proj := canon(t, t.TempDir())
	sub := filepath.Join(proj, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	reg := views.New()
	in, ws := &notices{}, &failingWorkspaces{}
	wd := must(workdir.New(reg, osSeam{}, workdir.WithWorkspaces(ws)))
	svc := session.NewService(root, memstore.New(), session.Config{Folder: reg})
	h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{Cwd: proj}, Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	must(wd.Attach(bg, h, in))
	must(wd.Change(bg, h.ID(), sub))
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}

	ws.err = errors.New("workspace registry offline")
	_, err := wd.Ensure(bg, h.ID())
	if !herr.Has(err, workdir.CodeUnavailable) || !errors.Is(err, ws.err) {
		t.Fatalf("Ensure with a failing recovery: %v", err)
	}
	if cs, _ := changes(t, h); len(cs) != 1 {
		t.Fatalf("a failed recovery changed the log: %+v", cs)
	}
	if got := in.all(); len(got) != 2 || got[1].Kind != workdir.NoticeUnavailable {
		t.Fatalf("notices %+v, want the move and one unavailable notice", got)
	}

	ws.err = nil
	rec := must(wd.Ensure(bg, h.ID()))
	if rec.Path != proj || rec.Revision != 2 {
		t.Fatalf("recovery once the registry answers = %+v", rec)
	}
}

// Attaching a session again swaps its notifier for every reader, while readers
// run, and a detach ends every Access of it.
func TestReattachReplacesTheSession(t *testing.T) {
	dir := canon(t, t.TempDir())
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, memstore.New(), osSeam{}, time.Now)
	h := r.create(t, dir)
	must(r.wd.Attach(bg, h, nil))
	acc := must(r.wd.Access(h.ID()))

	later := &notices{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				if _, err := acc.Path(); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	must(r.wd.Attach(bg, h, later))
	wg.Wait()
	must(r.wd.Change(bg, h.ID(), sub))
	if len(r.in.all()) != 0 || len(later.all()) != 1 {
		t.Fatalf("notices went to %+v and %+v, want the second notifier only", r.in.all(), later.all())
	}
	if p := must(acc.Path()); p != sub {
		t.Fatalf("an Access taken before the re-attach reads %q", p)
	}
	r.wd.Detach(h.ID())
	if _, err := acc.Path(); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Path after Detach: %v", err)
	}
}
