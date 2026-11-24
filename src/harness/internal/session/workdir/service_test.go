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

// flakyFlush is a session whose next fail flushes fail after the append.
type flakyFlush struct {
	session.Session
	mu   sync.Mutex
	fail int
}

var errDiskFull = errors.New("disk full")

func (f *flakyFlush) Flush(ctx context.Context) (session.Offset, error) {
	f.mu.Lock()
	fail := f.fail > 0
	if fail {
		f.fail--
	}
	f.mu.Unlock()
	if fail {
		return 0, errDiskFull
	}
	return f.Session.Flush(ctx)
}

// A change appended but not flushed is in force and owed: Change returns the
// record with workdir/undurable and no notice; the next check flushes it and
// sends its moved notice once, and a retried Change finishes it instead of
// logging it again.
func TestUnflushedChangeIsFinishedNotRepeated(t *testing.T) {
	proj := canon(t, t.TempDir())
	sub := filepath.Join(proj, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, memstore.New(), osSeam{}, time.Now)
	h := r.create(t, proj)
	s := &flakyFlush{Session: h}
	must(r.wd.Attach(bg, s, nil))

	s.fail = 1
	wd, err := r.wd.Change(bg, h.ID(), sub)
	if !herr.Has(err, workdir.CodeUndurable) || !errors.Is(err, errDiskFull) {
		t.Fatalf("Change with a failing flush: %v", err)
	}
	if wd.Path != sub || wd.Revision != 1 {
		t.Fatalf("Change returned %+v, want the appended move", wd)
	}
	if n := len(r.in.all()); n != 0 {
		t.Fatalf("%d notices sent for a change not yet durable", n)
	}
	s.fail = 1
	if _, err := r.wd.Ensure(bg, h.ID()); !herr.Has(err, workdir.CodeUndurable) {
		t.Fatalf("Ensure while the flush still fails: %v", err)
	}
	if got := must(r.wd.Ensure(bg, h.ID())); got.Path != sub || got.Revision != 1 {
		t.Fatalf("Ensure once the flush succeeds = %+v", got)
	}
	got := r.in.all()
	if len(got) != 1 || got[0].Kind != workdir.NoticeMoved || got[0].Path != sub || got[0].Revision != 1 {
		t.Fatalf("notices %+v, want the moved notice once", got)
	}

	s.fail = 1
	if _, err := r.wd.Change(bg, h.ID(), proj); !herr.Has(err, workdir.CodeUndurable) {
		t.Fatalf("second Change with a failing flush: %v", err)
	}
	if again := must(r.wd.Change(bg, h.ID(), proj)); again.Path != proj || again.Revision != 2 {
		t.Fatalf("retried Change = %+v, want the first attempt finished at revision 2", again)
	}
	if cs, _ := changes(t, h); len(cs) != 2 || cs[1].Directory != proj {
		t.Fatalf("log %+v, want the retry not appended again", cs)
	}
	if got := r.in.all(); len(got) != 2 || got[1].Revision != 2 {
		t.Fatalf("notices %+v, want one moved notice per change", got)
	}
}

// idWorkspaces names each root with a stable registry id, not a path.
type idWorkspaces struct {
	mu    sync.Mutex
	calls int
}

func (w *idWorkspaces) WorkspaceOf(_ context.Context, _ session.ID, root string) (string, error) {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	return "ws_" + filepath.Base(root), nil
}

// A session created in a repository subdirectory has its project root and
// workspace id from the first attach, recorded once in the log, so a resume in
// a fresh process reads them without looking them up again; no notice is sent,
// since the directory did not move (TC-01b).
func TestAttachResolvesRootAndWorkspace(t *testing.T) {
	repo := filepath.Join(canon(t, t.TempDir()), "repo")
	sub := filepath.Join(repo, "pkg", "sub")
	for _, d := range []string{sub, filepath.Join(repo, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st := memstore.New()
	open := func(ws *idWorkspaces) (session.Service, *workdir.Service, *notices) {
		root := cordis.New()
		root.Loggers().ClearExporters()
		root.Loggers().AddExporter(&cordis.MemoryExporter{})
		t.Cleanup(root.Dispose)
		reg := views.New()
		in := &notices{}
		wd := must(workdir.New(reg, osSeam{}, workdir.WithWorkspaces(ws), workdir.WithNotifier(in)))
		return session.NewService(root, st, session.Config{Folder: reg}), wd, in
	}

	svc, wd, in := open(&idWorkspaces{})
	h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{Cwd: sub}, Scope: scope.Key{}}))
	got := must(wd.Attach(bg, h, nil))
	want := workdir.WorkingDirectory{Path: sub, Workspace: "ws_repo", Root: repo, Revision: 1, State: workdir.Available}
	if got != want {
		t.Fatalf("Attach of a new session = %+v, want %+v", got, want)
	}
	if r := must(must(wd.Access(h.ID())).Root()); r != repo {
		t.Fatalf("Root = %q, want the repository %q", r, repo)
	}
	cs, _ := changes(t, h)
	if len(cs) != 1 || cs[0].Cause != workdir.CauseResolve || cs[0].Directory != sub {
		t.Fatalf("log %+v, want one resolve change", cs)
	}
	if n := len(in.all()); n != 0 {
		t.Fatalf("resolving queued %d notices", n)
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	ws2 := &idWorkspaces{}
	svc2, wd2, _ := open(ws2)
	h2, _, err := svc2.Open(bg, nil, h.ID(), session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close(bg) })
	if again := must(wd2.Attach(bg, h2, nil)); again != want {
		t.Fatalf("Attach of the resumed session = %+v, want %+v", again, want)
	}
	if cs, _ := changes(t, h2); len(cs) != 1 || ws2.calls != 0 {
		t.Fatalf("the resume resolved again: %d changes, %d lookups", len(cs), ws2.calls)
	}
}

// A project root the seam cannot find (the repository's .git lookup fails) is
// that failure: neither the attach-time resolve nor a Change records a guessed
// root or workspace, and once the seam answers the real root is recorded.
func TestRootDiscoveryFailureRecordsNothing(t *testing.T) {
	repo := filepath.Join(canon(t, t.TempDir()), "repo")
	sub, other := filepath.Join(repo, "sub"), filepath.Join(repo, "other")
	for _, d := range []string{sub, other, filepath.Join(repo, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seam := &faultSeam{}
	r := newRig(t, memstore.New(), seam, time.Now)
	h := r.create(t, sub)
	broken := herr.Newf(fs.CodeIO, "permission denied")
	seam.set(filepath.Join(repo, ".git"), broken)

	if _, err := r.wd.Attach(bg, h, nil); !errors.Is(err, broken) {
		t.Fatalf("Attach with a failing root lookup: %v", err)
	}
	if _, err := r.wd.Change(bg, h.ID(), other); !errors.Is(err, broken) {
		t.Fatalf("Change with a failing root lookup: %v", err)
	}
	if cs, _ := changes(t, h); len(cs) != 0 {
		t.Fatalf("a failed root lookup was logged: %+v", cs)
	}
	if n := len(r.in.all()); n != 0 {
		t.Fatalf("a failed root lookup queued %d notices", n)
	}

	seam.set("", nil)
	got := must(r.wd.Attach(bg, h, nil))
	if got.Root != repo || got.Path != sub || got.Revision != 1 {
		t.Fatalf("Attach once the seam answers = %+v, want root %q", got, repo)
	}
	if cs, _ := changes(t, h); len(cs) != 1 || cs[0].Root != repo || cs[0].Cause != workdir.CauseResolve {
		t.Fatalf("log %+v, want one resolve change to the real root", cs)
	}
}
