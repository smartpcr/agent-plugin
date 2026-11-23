package views_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// The re-executed child of TestInterruptedRebuildResumes: it opens the store at
// DSH_VIEWS_REBUILD_CHILD with the stored fold at version 2 and runs the
// views-rebuild duty. With DSH_VIEWS_REBUILD_STOP=n above 0 every row write
// after the n-th blocks before it writes, and once n rows are written the child
// reports "rebuilt writes=<n>" and waits to be killed; otherwise it reports
// "done ran=<bool> writes=<rows written> err=<quoted error>" when the duty
// returns. It never closes its store: the parent kills it.
const (
	rebuildChildEnv = "DSH_VIEWS_REBUILD_CHILD"
	rebuildStopEnv  = "DSH_VIEWS_REBUILD_STOP"
)

// gated is a store whose derived-row writes stop, deterministically, after
// stopAt of them: the next write blocks before it reaches the database.
type gated struct {
	*sqlite.Store
	stopAt  int
	mu      sync.Mutex
	started int
	written int
	reached chan struct{}
}

func (g *gated) UpsertDerived(ctx context.Context, id string, rows []store.Checkpoint) (int, error) {
	g.mu.Lock()
	if g.stopAt > 0 && g.started >= g.stopAt {
		g.mu.Unlock()
		<-ctx.Done() // killed here, before the write
		return 0, ctx.Err()
	}
	g.started++
	g.mu.Unlock()
	n, err := g.Store.UpsertDerived(ctx, id, rows)
	g.mu.Lock()
	g.written++
	if g.stopAt > 0 && g.written == g.stopAt {
		close(g.reached)
	}
	g.mu.Unlock()
	return n, err
}

func TestRebuildChild(t *testing.T) {
	path := os.Getenv(rebuildChildEnv)
	if path == "" {
		t.Skip("the child of TestInterruptedRebuildResumes")
	}
	stopAt, err := strconv.Atoi(os.Getenv(rebuildStopEnv))
	if err != nil {
		t.Fatal(err)
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	st := must(sqlite.Open(bg, path, sqlite.WithKind("child")))
	g := &gated{Store: st, stopAt: stopAt, reached: make(chan struct{})}
	reg := views.New()
	must(views.Register(reg, stored(2, nil)))
	svc := session.NewService(root, g, session.Config{Folder: reg})
	reg.Bind(g, svc)
	type result struct {
		ran bool
		err error
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan result, 1)
	joined := make(chan struct{})
	// P-C: a child that is not killed cancels the sweep (unblock), then joins it.
	t.Cleanup(func() { cancel(); <-joined })
	go func() {
		defer close(joined)
		ran, err := reg.RunRebuild(ctx, 0)
		done <- result{ran, err}
	}()
	select {
	case <-g.reached:
		fmt.Printf("rebuilt writes=%d\n", stopAt)
	case r := <-done:
		g.mu.Lock()
		writes := g.written
		g.mu.Unlock()
		fmt.Printf("done ran=%v writes=%d err=%s\n", r.ran, writes, strconv.Quote(errText(r.err)))
	}
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
	}
	t.Fatal("the parent did not kill the child")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// rebuilder is one running child.
type rebuilder struct {
	t   *testing.T
	cmd *exec.Cmd
	out *bufio.Scanner
	log []string
}

func startRebuilder(t *testing.T, path string, stopAt int) *rebuilder {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRebuildChild$", "-test.count=1", "-test.v")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "DSH_HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, rebuildChildEnv+"="+path, rebuildStopEnv+"="+strconv.Itoa(stopAt))
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &rebuilder{t: t, cmd: cmd, out: bufio.NewScanner(out)}
	t.Cleanup(func() { killChild(t, cmd) })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *rebuilder) expect(prefix string) string {
	c.t.Helper()
	for c.out.Scan() {
		c.log = append(c.log, c.out.Text())
		if strings.HasPrefix(c.out.Text(), prefix) {
			return c.out.Text()
		}
	}
	c.t.Fatalf("the child never reported %q:\n%s", prefix, strings.Join(c.log, "\n"))
	return ""
}

func (c *rebuilder) kill() { killChild(c.t, c.cmd) }

// killChild kills cmd, if it still runs, waits for it and checks it is gone.
func killChild(t *testing.T, cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	cmd.Process.Kill() //nolint:errcheck // may be dead already
	cmd.Wait()         //nolint:errcheck // killed
	cmd.Process = nil
	if !subprocesstest.Exited(pid) {
		t.Errorf("child %d still runs after its kill", pid)
	}
}

// seedVersion1 creates n sessions on path with the stored fold at version 1,
// session i holding 1+i%7 events, and returns their ids and lengths.
func seedVersion1(t *testing.T, path string, n int) ([]session.ID, map[session.ID]int) {
	t.Helper()
	reg := views.New()
	must(views.Register(reg, stored(1, nil)))
	_, svc, stop := boot(t, path, reg)
	defer stop()
	ids := make([]session.ID, 0, n)
	lens := map[session.ID]int{}
	for i := range n {
		h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
		k := 1 + i%7
		for range k {
			must(h.Append(bg, ctxDraft("v")))
		}
		ids = append(ids, h.ID())
		lens[h.ID()] = int(h.Len())
		must(0, h.Close(bg))
	}
	return ids, lens
}

// readAll reads every session at Stored freshness through reg and counts the
// values that are current at version 2 (2 per event) and the pending ones. A
// version-1 value, or any other state or value, fails the test.
func readAll(t *testing.T, svc session.Service, reg *views.Registry, fold views.Handle[int], ids []session.ID) (current, pending int) {
	t.Helper()
	for _, id := range ids {
		rs, release, err := svc.OpenRead(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		snap := must(reg.Snapshot(bg, rs, []string{storedKey}, views.Stored))
		v, _ := snap.Value(storedKey)
		got, err := fold.Get(snap)
		switch {
		case v.State == views.Pending && errors.Is(err, views.ErrPending):
			pending++
		case v.State == views.Current && err == nil && got == 2*int(rs.Len()) && v.AsOfSeq == rs.Len():
			current++
		default:
			release()
			t.Fatalf("session %s read %d (%v) as %v at %d of %d; want pending or the version-2 value %d", id, got, err, v.State, v.AsOfSeq, rs.Len(), 2*rs.Len())
		}
		release()
	}
	return current, pending
}

func statusOf(t *testing.T, reg *views.Registry, key string) views.FoldStatus {
	t.Helper()
	for _, s := range reg.Status(bg) {
		if s.Key == key {
			if s.Err != nil {
				t.Fatalf("Status of %s: %v", key, s.Err)
			}
			return s
		}
	}
	t.Fatalf("Status has no %s", key)
	return views.FoldStatus{}
}

// Scenario: An interrupted rebuild resumes (M1-G-STORE-15). Given 50 sessions
// with version-1 rows and the fold now at version 2, When the sweep, the
// views-rebuild duty in a re-executed child, is stopped after writing 20 rows
// (by a write gate, not a timer), read, killed and restarted, Then readers saw
// each session pending or at its version-2 value, never a version-1 value; the
// restarted run wrote only the 30 rows still missing; and all 50 rows are at
// version 2 and equal a fresh fold of the log.
//
//dsh:test DH-T-VIEW-6
func TestInterruptedRebuildResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	ids, lens := seedVersion1(t, path, 50)

	reg := views.New()
	fold := must(views.Register(reg, stored(2, nil)))
	st, svc := bootAt(t, path, reg)
	defer reg.Bind(st, svc)()
	if s := statusOf(t, reg, storedKey); s.Version != 2 || s.Pending != 50 || s.Current != 0 || s.Behind != 0 {
		t.Fatalf("before the rebuild Status = %+v, want 50 pending at version 2", s)
	}
	if cur, pend := readAll(t, svc, reg, fold, ids); cur != 0 || pend != 50 {
		t.Fatalf("before the rebuild %d current, %d pending; want 0, 50", cur, pend)
	}

	first := startRebuilder(t, path, 20)
	first.expect("rebuilt writes=20")
	// The child is stopped mid-sweep, its other writes blocked before the
	// database: readers see the 20 rebuilt sessions at version 2, the rest pending.
	if cur, pend := readAll(t, svc, reg, fold, ids); cur != 20 || pend != 30 {
		t.Fatalf("during the rebuild %d current, %d pending; want 20, 30", cur, pend)
	}
	if s := statusOf(t, reg, storedKey); s.Current != 20 || s.Pending != 30 {
		t.Fatalf("during the rebuild Status = %+v, want 20 current, 30 pending", s)
	}
	first.kill()

	second := startRebuilder(t, path, 0)
	if line := second.expect("done "); line != `done ran=true writes=30 err=""` {
		t.Fatalf("the restarted sweep reported %q; want it to run and write only the 30 missing rows", line)
	}
	second.kill()

	if cur, pend := readAll(t, svc, reg, fold, ids); cur != 50 || pend != 0 {
		t.Fatalf("after the rebuild %d current, %d pending; want 50, 0", cur, pend)
	}
	if s := statusOf(t, reg, storedKey); s.Current != 50 || s.Pending != 0 || s.Behind != 0 || s.Failed != 0 {
		t.Fatalf("after the rebuild Status = %+v, want 50 current", s)
	}
	// Each row equals a fresh fold of the log by a registry that keeps no rows.
	fresh := views.New()
	ff := must(views.Register(fresh, views.Definition[int]{Key: storedKey, Version: 2,
		Init:  func(session.Header) int { return 0 },
		Apply: func(n int, _ event.Event) (int, bool) { return n + 2, true }}))
	for _, id := range ids {
		row, ok := checkpointsOf(t, st, id)[2]
		rs, release, err := svc.OpenRead(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		want := must(ff.Get(must(fresh.Read(bg, rs))))
		release()
		if !ok || row.Seq != event.Seq(lens[id]-1) || row.State != strconv.Itoa(want) {
			t.Fatalf("session %s: version-2 row %+v (present %v), want seq %d state %d", id, row, ok, lens[id]-1, want)
		}
	}
}

// Rebuild refuses what it cannot do: an unregistered key, a fold that keeps no
// rows, no bound store, a store that cannot write rows or list the sessions
// lacking one, and a duty on a store that runs none.
func TestRebuildRefusals(t *testing.T) {
	reg := views.New()
	must(views.Register(reg, stored(1, nil)))
	must(views.Register(reg, count()))
	if err := reg.Rebuild(bg, storedKey, views.RebuildScope{}); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("Rebuild with no store = %v, want view/invalid", err)
	}
	if _, err := reg.RunRebuild(bg, 0); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("RunRebuild with no store = %v, want view/invalid", err)
	}
	if s := reg.Status(bg); len(s) != 2 || !errors.Is(s[0].Err, views.ErrInvalid) || s[1].Err != nil || s[1].Stored {
		t.Fatalf("Status with no store = %+v", s)
	}

	e := newEnv(t, reg)
	stop := reg.Bind(e.st, e.svc)
	if err := reg.Rebuild(bg, "test/none", views.RebuildScope{}); !errors.Is(err, views.ErrUnknownKey) {
		t.Fatalf("Rebuild of an unknown key = %v", err)
	}
	if err := reg.Rebuild(bg, "test/count", views.RebuildScope{}); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("Rebuild of a fold that is not stored = %v", err)
	}
	if _, err := reg.RunRebuild(bg, 0); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("RunRebuild on memstore, which runs no duty = %v", err)
	}
	stop()
	stop()
	if err := reg.Rebuild(bg, storedKey, views.RebuildScope{}); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("Rebuild after stop = %v, want view/invalid", err)
	}

	plain := plainStore{e.st}
	reg.Bind(plain, e.svc)
	if err := reg.Rebuild(bg, storedKey, views.RebuildScope{}); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("Rebuild on a store without UpsertDerived = %v", err)
	}
	if s := reg.Status(bg); !errors.Is(s[0].Err, views.ErrInvalid) {
		t.Fatalf("Status on a store without FoldRows = %+v", s)
	}
	//lint:ignore typeassert newEnv's store is a memstore, a DerivedWriter
	reg.Bind(writerOnly{plain, e.st.(store.DerivedWriter)}, e.svc)
	if err := reg.Rebuild(bg, storedKey, views.RebuildScope{}); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("Rebuild of the whole store without FoldRows = %v", err)
	}
}

// plainStore hides every optional interface of the store it wraps.
type plainStore struct{ store.Store }

// writerOnly is a store that writes derived rows but cannot list them.
type writerOnly struct {
	store.Store
	store.DerivedWriter
}

// A Rebuild of named sessions on memstore catches each up from its usable row
// to the durable head, skips a session that is gone, an empty one and one whose
// row already reaches the head, and leaves the others alone.
func TestRebuildNamedSessions(t *testing.T) {
	var calls atomic.Int64
	writer := views.New()
	must(views.Register(writer, stored(1, nil)))
	e := newEnv(t, writer)
	a, b, empty := e.create(t), e.create(t), e.create(t)
	for range 3 {
		must(a.Append(bg, ctxDraft("v")))
	}
	must(b.Append(bg, ctxDraft("v")))
	for _, h := range []session.Handle{a, b} {
		must(h.Flush(bg))
	}

	reg := views.New()
	must(views.Register(reg, stored(1, &calls)))
	reg.Bind(e.st, e.svc)
	before := calls.Load()
	if err := reg.Rebuild(bg, storedKey, views.RebuildScope{Sessions: []session.ID{a.ID(), "gone", empty.ID(), b.ID()}}); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load() - before; n != 0 {
		t.Fatalf("rebuilding rows that reach the head folded %d events", n)
	}
	// A version-2 registry folds both from 0 and writes their rows.
	reg2 := views.New()
	fold2 := must(views.Register(reg2, stored(2, nil)))
	reg2.Bind(e.st, e.svc)
	if err := reg2.Rebuild(bg, storedKey, views.RebuildScope{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		h    session.Handle
		want int
	}{{a, 6}, {b, 2}} {
		rs, release := must2(e.svc.OpenRead(bg, c.h.ID()))
		snap := must(reg2.Snapshot(bg, rs, nil, views.Stored))
		release()
		if got, err := fold2.Get(snap); err != nil || got != c.want {
			t.Fatalf("session %s after the rebuild = %d, %v; want %d", c.h.ID(), got, err, c.want)
		}
	}
	if s := statusOf(t, reg2, storedKey); s.Current != 2 || s.Pending != 0 || s.Failed != 0 {
		t.Fatalf("Status = %+v, want 2 current", s)
	}
}
