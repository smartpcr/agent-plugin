//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
)

// wdfSuiteT is the entrypoint's *testing.T: synctest.Test needs a real one.
var wdfSuiteT *testing.T

var wdfBg = context.Background()

// wdfInbox records the notices the service queues; like the agent's inbox
// adapter, an id it already holds is a no-op.
type wdfInbox struct {
	mu  sync.Mutex
	got []workdir.Notice
}

func (n *wdfInbox) Notify(_ context.Context, x workdir.Notice) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, g := range n.got {
		if g.ID == x.ID {
			return nil
		}
	}
	n.got = append(n.got, x)
	return nil
}

func (n *wdfInbox) all() []workdir.Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]workdir.Notice(nil), n.got...)
}

// wdfRig is one process: a session service over a store whose folds are a
// fresh registry, and the working-directory service over a seam.
type wdfRig struct {
	svc session.Service
	wd  *workdir.Service
	in  *wdfInbox
}

func wdfNewRig(st store.Store, seam workdir.Stater, cleanup func(func())) (*wdfRig, error) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	cleanup(root.Dispose)
	reg := views.New()
	in := &wdfInbox{}
	wd, err := workdir.New(reg, seam, workdir.WithNotifier(in))
	if err != nil {
		return nil, err
	}
	return &wdfRig{svc: session.NewService(root, st, session.Config{Folder: reg}), wd: wd, in: in}, nil
}

func (r *wdfRig) create(cwd string, cleanup func(func())) (session.Handle, error) {
	h, err := r.svc.Create(wdfBg, nil, session.CreateOptions{Header: session.Header{Cwd: cwd}, Scope: scope.Key{}})
	if err != nil {
		return nil, err
	}
	cleanup(func() { _ = h.Close(wdfBg) })
	return h, nil
}

// wdfChanges is the log's workspace-root/change events and, for each, the
// request/context line right after it (empty when none follows).
func wdfChanges(s session.ReadSession) ([]workdir.ChangePayload, []string, error) {
	evs, err := s.Read(wdfBg, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	var cs []workdir.ChangePayload
	var lines []string
	for i, ev := range evs {
		if ev.Type() != workdir.ChangeType.Name() {
			continue
		}
		c, err := workdir.ChangeType.As(ev)
		if err != nil {
			return nil, nil, err
		}
		cs = append(cs, c)
		line := ""
		if i+1 < len(evs) && evs[i+1].Type() == event.RequestContext.Name() {
			p, err := event.RequestContext.As(evs[i+1])
			if err != nil {
				return nil, nil, err
			}
			if p.Key == workdir.ContextKey {
				line = p.Value
			}
		}
		lines = append(lines, line)
	}
	return cs, lines, nil
}

type wdfWorld struct {
	cleanups []func()

	// Fold rebuilt from the log.
	st         store.Store
	seam       *fstest.MemFS
	id         session.ID
	second     string
	rebuiltAcc workdir.Access
	rebuilt    workdir.WorkingDirectory
	refold     workdir.WorkingDirectory

	// Concurrent changes serialize.
	paths      []string
	bubbleErr  error
	cs         []workdir.ChangePayload
	lines      []string
	final      workdir.WorkingDirectory
	concNotice []workdir.Notice

	// Deleted directory reported.
	rig       *wdfRig
	sub       string
	accessRes workdir.Access
	accessErr error
}

func (w *wdfWorld) cleanup(f func()) { w.cleanups = append(w.cleanups, f) }

func (w *wdfWorld) runCleanups() {
	for i := len(w.cleanups) - 1; i >= 0; i-- {
		w.cleanups[i]()
	}
	w.cleanups = nil
}

// --- Scenario: Fold rebuilt from the log ---

func (w *wdfWorld) givenLogWithTwoChanges() error {
	tmp, err := os.MkdirTemp("", "dsh-wdf-e2e-")
	if err != nil {
		return err
	}
	w.cleanup(func() { _ = os.RemoveAll(tmp) })
	base := filepath.Join(tmp, "proj")
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	w.seam = fstest.NewMemFS()
	for _, d := range []string{a, b} {
		if err := w.seam.Mkdir(d); err != nil {
			return err
		}
	}
	w.st = memstore.New()
	first, err := wdfNewRig(w.st, w.seam, w.cleanup)
	if err != nil {
		return err
	}
	h, err := first.create(base, w.cleanup)
	if err != nil {
		return err
	}
	if _, err := first.wd.Attach(wdfBg, h, nil); err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	for _, p := range []string{a, b} {
		if _, err := first.wd.Change(wdfBg, h.ID(), p); err != nil {
			return fmt.Errorf("change to %q: %w", p, err)
		}
	}
	cs, _, err := wdfChanges(h)
	if err != nil {
		return err
	}
	if len(cs) != 2 {
		return fmt.Errorf("log holds %d workingDirectory changes, want 2: %+v", len(cs), cs)
	}
	if err := h.Close(wdfBg); err != nil {
		return err
	}
	w.id, w.second = h.ID(), b
	return nil
}

func (w *wdfWorld) whenViewsRebuilt() error {
	r, err := wdfNewRig(w.st, w.seam, w.cleanup)
	if err != nil {
		return err
	}
	h, _, err := r.svc.Open(wdfBg, nil, w.id, session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	w.cleanup(func() { _ = h.Close(wdfBg) })
	if w.rebuilt, err = r.wd.Attach(wdfBg, h, nil); err != nil {
		return fmt.Errorf("attach after reopen: %w", err)
	}
	if w.rebuiltAcc, err = r.wd.Access(h.ID()); err != nil {
		return fmt.Errorf("access after reopen: %w", err)
	}
	evs, err := h.Read(wdfBg, 0, 0)
	if err != nil {
		return err
	}
	w.refold = views.Refold(workdir.FoldView(), h.Header(), evs)
	return nil
}

func (w *wdfWorld) thenAccessPathIsSecond() error {
	p, err := w.rebuiltAcc.Path()
	if err != nil {
		return err
	}
	if p != w.second {
		return fmt.Errorf("Access.Path() = %q, want the second path %q", p, w.second)
	}
	return nil
}

func (w *wdfWorld) thenWorkspaceAndRootMatch() error {
	cur, err := w.rebuiltAcc.Current()
	if err != nil {
		return err
	}
	if cur.Workspace != w.second || cur.Root != w.second {
		return fmt.Errorf("fold workspace %q root %q, want both %q", cur.Workspace, cur.Root, w.second)
	}
	return nil
}

func (w *wdfWorld) thenRevisionTwoAvailable() error {
	if w.rebuilt.Revision != 2 || w.rebuilt.State != workdir.Available {
		return fmt.Errorf("rebuilt fold %+v, want revision 2 and available", w.rebuilt)
	}
	return nil
}

func (w *wdfWorld) thenRefoldEqual() error {
	if w.refold != w.rebuilt {
		return fmt.Errorf("refold %+v differs from the service's %+v", w.refold, w.rebuilt)
	}
	return nil
}

// --- Scenario: Concurrent changes serialize ---

func (w *wdfWorld) givenTwoGoroutinesInBubble() error {
	if wdfSuiteT == nil {
		return errors.New("no suite *testing.T for synctest")
	}
	w.paths = []string{"a", "b"}
	return nil
}

func (w *wdfWorld) whenBothComplete() error {
	tmp, err := os.MkdirTemp("", "dsh-wdf-e2e-")
	if err != nil {
		return err
	}
	w.cleanup(func() { _ = os.RemoveAll(tmp) })
	base := filepath.Join(tmp, "proj")
	ok := wdfSuiteT.Run("synctest-concurrent-changes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			w.bubbleErr = w.runConcurrent(t, base)
		})
	})
	if !ok && w.bubbleErr == nil {
		return errors.New("the synctest bubble failed")
	}
	return w.bubbleErr
}

// runConcurrent runs entirely inside the bubble; its cleanups run there too.
func (w *wdfWorld) runConcurrent(t *testing.T, base string) error {
	seam := fstest.NewMemFS()
	var dirs []string
	for _, p := range w.paths {
		d := filepath.Join(base, p)
		if err := seam.Mkdir(d); err != nil {
			return err
		}
		dirs = append(dirs, d)
	}
	r, err := wdfNewRig(memstore.New(), seam, t.Cleanup)
	if err != nil {
		return err
	}
	h, err := r.create(base, t.Cleanup)
	if err != nil {
		return err
	}
	if _, err := r.wd.Attach(wdfBg, h, nil); err != nil {
		return err
	}
	var wg sync.WaitGroup
	errs := make([]error, len(dirs))
	for i, d := range dirs {
		wg.Go(func() { _, errs[i] = r.wd.Change(wdfBg, h.ID(), d) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("concurrent Change: %w", err)
	}
	if w.cs, w.lines, err = wdfChanges(h); err != nil {
		return err
	}
	acc, err := r.wd.Access(h.ID())
	if err != nil {
		return err
	}
	if w.final, err = acc.Current(); err != nil {
		return err
	}
	w.concNotice = r.in.all()
	return nil
}

func (w *wdfWorld) thenTwoOrderedEvents() error {
	if len(w.cs) != 2 || w.cs[0].Directory == w.cs[1].Directory {
		return fmt.Errorf("log holds %+v, want one change to each directory", w.cs)
	}
	for i, c := range w.cs {
		if c.Cause != workdir.CauseMove {
			return fmt.Errorf("change %d cause %q, want move", i, c.Cause)
		}
	}
	return nil
}

func (w *wdfWorld) thenEachChangeFollowedByLine() error {
	for i, c := range w.cs {
		if w.lines[i] != workdir.ContextLine(c.Directory) {
			return fmt.Errorf("change %d to %q is followed by context line %q (interleaved)", i, c.Directory, w.lines[i])
		}
	}
	return nil
}

func (w *wdfWorld) thenFinalIsLater() error {
	if len(w.cs) != 2 {
		return fmt.Errorf("want 2 changes, got %d", len(w.cs))
	}
	later := w.cs[1]
	if w.final.Path != later.Directory || w.final.Root != later.Root || w.final.Workspace != later.Workspace || w.final.Revision != 2 {
		return fmt.Errorf("final fold %+v, want the later change %+v at revision 2", w.final, later)
	}
	return nil
}

func (w *wdfWorld) thenNoticesInOrder() error {
	got := w.concNotice
	if len(got) != 2 || got[0].Revision != 1 || got[1].Revision != 2 ||
		got[0].Path != w.cs[0].Directory || got[1].Path != w.cs[1].Directory ||
		got[0].Kind != workdir.NoticeMoved || got[1].Kind != workdir.NoticeMoved {
		return fmt.Errorf("notices %+v are not one moved notice per change in log order", got)
	}
	return nil
}

// --- Scenario: Deleted directory reported ---

func (w *wdfWorld) givenRemovedTempSubdir() error {
	tmp, err := os.MkdirTemp("", "dsh-wdf-e2e-")
	if err != nil {
		return err
	}
	w.cleanup(func() { _ = os.RemoveAll(tmp) })
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return err
	}
	sub := filepath.Join(tmp, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		return err
	}
	r, err := wdfNewRig(memstore.New(), local.New(), w.cleanup)
	if err != nil {
		return err
	}
	h, err := r.create(sub, w.cleanup)
	if err != nil {
		return err
	}
	wd, err := r.wd.Attach(wdfBg, h, nil)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	if wd.State != workdir.Available {
		return fmt.Errorf("attached fold %+v is not available", wd)
	}
	if n := r.in.all(); len(n) != 0 {
		return fmt.Errorf("attach queued notices %+v", n)
	}
	if err := os.RemoveAll(sub); err != nil {
		return err
	}
	w.rig, w.id, w.sub = r, h.ID(), wd.Path
	return nil
}

func (w *wdfWorld) whenToolAsksAccess() error {
	w.accessRes, w.accessErr = w.rig.wd.Access(w.id)
	return nil
}

func (w *wdfWorld) thenReturnsCode(code string) error {
	if w.accessErr == nil {
		return fmt.Errorf("Access returned no error, want %s", code)
	}
	if !herr.Has(w.accessErr, herr.Reason(code)) {
		return fmt.Errorf("Access error %v (kind %q), want %s", w.accessErr, herr.Kind(w.accessErr), code)
	}
	if w.accessRes != nil {
		return errors.New("Access returned a view alongside workdir/unavailable")
	}
	return nil
}

func (w *wdfWorld) thenExactlyOneNotice() error {
	if got := w.rig.in.all(); len(got) != 1 {
		return fmt.Errorf("inbox holds %d notices, want exactly 1: %+v", len(got), got)
	}
	return nil
}

func (w *wdfWorld) thenNoticeIsUnavailable() error {
	n := w.rig.in.all()[0]
	if n.Kind != workdir.NoticeUnavailable || n.Path != w.sub || n.Session != w.id {
		return fmt.Errorf("notice %+v, want an unavailable notice for %q", n, w.sub)
	}
	return nil
}

// InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_working_directory_fold
// registers the steps of the working-directory fold stage.
func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_working_directory_fold(ctx *godog.ScenarioContext) {
	w := &wdfWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.runCleanups()
		return c, err
	})

	ctx.Step(`^an in-memory event log with two workingDirectory changes$`, w.givenLogWithTwoChanges)
	ctx.Step(`^the views are rebuilt$`, w.whenViewsRebuilt)
	ctx.Step(`^workdir\.Access\.Path\(\) equals the second path$`, w.thenAccessPathIsSecond)
	ctx.Step(`^the fold's workspace and root match the second path$`, w.thenWorkspaceAndRootMatch)
	ctx.Step(`^the rebuilt fold is at revision 2 and available$`, w.thenRevisionTwoAvailable)
	ctx.Step(`^replaying the log through the fold definition gives the same record$`, w.thenRefoldEqual)

	ctx.Step(`^two goroutines calling Change on one session inside a synctest bubble$`, w.givenTwoGoroutinesInBubble)
	ctx.Step(`^both complete$`, w.whenBothComplete)
	ctx.Step(`^the log holds two ordered events$`, w.thenTwoOrderedEvents)
	ctx.Step(`^each change is immediately followed by its own context line$`, w.thenEachChangeFollowedByLine)
	ctx.Step(`^the final fold equals the later one$`, w.thenFinalIsLater)
	ctx.Step(`^one moved notice was queued per change in log order$`, w.thenNoticesInOrder)

	ctx.Step(`^a session whose working directory is a temp subdirectory that the test then removes$`, w.givenRemovedTempSubdir)
	ctx.Step(`^the next tool call asks workdir\.Access(?: again)?$`, w.whenToolAsksAccess)
	ctx.Step(`^it returns "([^"]*)"$`, w.thenReturnsCode)
	ctx.Step(`^exactly one inbox notice is queued$`, w.thenExactlyOneNotice)
	ctx.Step(`^the queued notice is an unavailable notice for the removed directory$`, w.thenNoticeIsUnavailable)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_working_directory_fold(t *testing.T) {
	wdfSuiteT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_working_directory_fold,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_working_directory_fold.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
