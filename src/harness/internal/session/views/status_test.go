package views_test

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

const flakyKey = "test/flaky"

// flaky counts events with a Periodic row; its Init panics for a session whose
// header's Cwd is "boom", so that fold fails for that session.
func flaky() views.Definition[int] {
	return views.Definition[int]{Key: flakyKey, Version: 1, Stored: true, Cadence: views.Periodic,
		Init: func(h session.Header) int {
			if h.Cwd == "boom" {
				panic("boom")
			}
			return 0
		},
		Apply: func(n int, _ event.Event) (int, bool) { return n + 1, true }}
}

// Scenario: Status counts behind, pending and failed (M1-G-STORE-17). Given one
// session whose row reaches the head, one whose Periodic row is behind, one
// written by a process without the fold (no row), and one whose fold failed (its
// Init panicked, so it has no row either), When Status is read, Then each is
// counted: 1 current, 1 behind, 2 pending, 1 failed. A whole-store Rebuild then
// writes the pending session's row and fails again on the failed one, which
// stays pending and failed. It holds on sqlite and on memstore.
func TestStatusCountsBehindPendingAndFailed(t *testing.T) {
	stores := map[string]func(t *testing.T, reg *views.Registry) (store.Store, session.Service){
		"sqlite": func(t *testing.T, reg *views.Registry) (store.Store, session.Service) {
			st, svc := bootAt(t, filepath.Join(t.TempDir(), "dsh.db"), reg)
			return st, svc
		},
		"memstore": func(t *testing.T, reg *views.Registry) (store.Store, session.Service) {
			e := newEnv(t, reg)
			return e.st, e.svc
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			reg := views.New(views.WithCheckpointEveryEvents(1_000_000))
			must(views.Register(reg, flaky()))
			must(views.Register(reg, count()))
			st, svc := open(t, reg)
			defer reg.Bind(st, svc)()
			root := cordis.New()
			root.Loggers().ClearExporters()
			root.Loggers().AddExporter(&cordis.MemoryExporter{})
			t.Cleanup(root.Dispose)
			without := session.NewService(root, st, session.Config{})

			mk := func(svc session.Service, cwd string, batches ...int) session.Handle {
				h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{Cwd: cwd}, Scope: scope.Key{}}))
				t.Cleanup(func() { _ = h.Close(bg) })
				for _, n := range batches {
					appendN(t, h, n)
				}
				must(h.Flush(bg))
				return h
			}
			mk(svc, "", 3)     // current: the first batch writes the row at its head
			mk(svc, "", 1, 2)  // behind: the row stays at the first batch
			mk(without, "", 2) // pending: no row
			mk(svc, "boom", 2) // failed: no row, and the fold failed
			mk(svc, "")        // empty: not counted

			want := views.FoldStatus{Key: flakyKey, Version: 1, Stored: true, Current: 1, Behind: 1, Pending: 2, Failed: 1}
			st0 := reg.Status(bg)
			if len(st0) != 2 || st0[0] != want {
				t.Fatalf("Status = %+v, want %+v first", st0, want)
			}
			if c := st0[1]; c.Key != "test/count" || c.Stored || c.Current+c.Behind+c.Pending+c.Failed != 0 || c.Err != nil {
				t.Fatalf("Status of a fold that keeps no rows = %+v", c)
			}

			err := reg.Rebuild(bg, flakyKey, views.RebuildScope{})
			if !errors.Is(err, views.ErrFailed) {
				t.Fatalf("Rebuild = %v, want the failed session's view/failed", err)
			}
			want.Current, want.Pending = 2, 1
			if got := statusOf(t, reg, flakyKey); got != want {
				t.Fatalf("after the rebuild Status = %+v, want %+v", got, want)
			}
		})
	}
}

// noticeStore is a memstore whose Degraded notices the test sends by hand.
type noticeStore struct {
	*memstore.Store
	n *store.Notices
}

func (s noticeStore) OnDegraded(fn func(store.Degraded)) (stop func()) { return s.n.Subscribe(fn) }

// A derived-dropped notice marks the named fold failed for the session, at its
// registered version only; other notices are ignored. The failure outlives the
// batch whose row was dropped and ends once a later batch commits the row.
func TestStatusCountsADroppedRowUntilItIsWritten(t *testing.T) {
	var notices store.Notices
	reg := views.New()
	must(views.Register(reg, stored(1, nil)))
	e := newEnv(t, reg, func(st store.Store) store.Store {
		//lint:ignore typeassert newEnv's store is a memstore
		return noticeStore{st.(*memstore.Store), &notices}
	})
	defer reg.Bind(e.st, e.svc)()
	h := e.create(t)
	must(h.Append(bg, ctxDraft("v")))
	id := string(h.ID())
	for _, n := range []store.Degraded{
		{Reason: store.DegradedUnusable},
		{Session: id, Reason: store.DegradedDerived, Keys: []string{storedKey + "@2", "test/none@1", "malformed", storedKey + "@x"}},
	} {
		notices.Send(n)
	}
	if s := statusOf(t, reg, storedKey); s.Failed != 0 {
		t.Fatalf("notices of no registered fold version counted %d failed", s.Failed)
	}
	notices.Send(store.Degraded{Session: id, Reason: store.DegradedDerived, Keys: []string{storedKey + "@1"}, Err: errors.New("refused")})
	if s := statusOf(t, reg, storedKey); s.Failed != 1 {
		t.Fatalf("after the notice Status = %+v, want 1 failed", s)
	}
	must(h.Append(bg, ctxDraft("v")))
	if s := statusOf(t, reg, storedKey); s.Failed != 1 {
		t.Fatalf("the batch after the dropped one is not yet known committed, yet Status = %+v", s)
	}
	must(h.Append(bg, ctxDraft("v")))
	if s := statusOf(t, reg, storedKey); s.Failed != 0 {
		t.Fatalf("a later batch committed the row, yet Status = %+v", s)
	}
	notices.Send(store.Degraded{Session: "ghost", Reason: store.DegradedDerived, Keys: []string{storedKey + "@1"}})
	if s := statusOf(t, reg, storedKey); s.Failed != 1 {
		t.Fatalf("a notice of a session the registry does not follow: Status = %+v", s)
	}
}

// A rebuild marks a session failed when the store refuses its row (and the
// store's own derived-dropped notice does so for a live append), when its state
// does not encode, and when a row of the fold's version at the head does not
// decode, which a forward-only write cannot replace; a later successful rebuild
// of the session clears it.
func TestRebuildMarksFailures(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		reg := views.New()
		must(views.Register(reg, stored(1, nil)))
		e := newEnv(t, reg)
		defer reg.Bind(e.st, e.svc)()
		//lint:ignore typeassert newEnv's store is a memstore
		ms := e.st.(*memstore.Store)
		h := e.create(t)
		must(0, ms.RefuseDerived(storedKey))
		must(h.Append(bg, ctxDraft("v")))
		must(h.Flush(bg))
		if s := statusOf(t, reg, storedKey); s.Failed != 1 || s.Pending != 1 {
			t.Fatalf("after a dropped row Status = %+v, want 1 pending, failed", s)
		}
		if err := reg.Rebuild(bg, storedKey, views.RebuildScope{Sessions: []session.ID{h.ID()}}); err == nil {
			t.Fatal("a refused rebuild row returned no error")
		}
		if s := statusOf(t, reg, storedKey); s.Failed != 1 {
			t.Fatalf("after a refused rebuild Status = %+v", s)
		}
	})
	t.Run("encode", func(t *testing.T) {
		writer := views.New()
		must(views.Register(writer, stored(1, nil)))
		e := newEnv(t, writer)
		h := e.create(t)
		must(h.Append(bg, ctxDraft("v")))
		must(h.Flush(bg))
		reg := views.New()
		must(views.Register(reg, views.Definition[float64]{Key: storedKey, Version: 2, Stored: true,
			Init:  func(session.Header) float64 { return 0 },
			Apply: func(float64, event.Event) (float64, bool) { return math.NaN(), true }}))
		defer reg.Bind(e.st, e.svc)()
		if err := reg.Rebuild(bg, storedKey, views.RebuildScope{}); !errors.Is(err, views.ErrFailed) {
			t.Fatalf("Rebuild of a state that does not encode = %v", err)
		}
		if s := statusOf(t, reg, storedKey); s.Failed != 1 || s.Pending != 1 {
			t.Fatalf("Status = %+v, want 1 pending, failed", s)
		}
	})
	t.Run("undecodable", func(t *testing.T) {
		writer := views.New()
		must(views.Register(writer, views.Definition[string]{Key: storedKey, Version: 1, Stored: true,
			Init:  func(session.Header) string { return "" },
			Apply: func(s string, _ event.Event) (string, bool) { return s + "x", true }}))
		e := newEnv(t, writer)
		h := e.create(t)
		must(h.Append(bg, ctxDraft("v")))
		must(h.Flush(bg))
		reg := views.New()
		fold := must(views.Register(reg, stored(1, nil)))
		defer reg.Bind(e.st, e.svc)()
		scope := views.RebuildScope{Sessions: []session.ID{h.ID()}}
		if err := reg.Rebuild(bg, storedKey, scope); !errors.Is(err, views.ErrFailed) {
			t.Fatalf("Rebuild over an undecodable row at the head = %v", err)
		}
		if s := statusOf(t, reg, storedKey); s.Failed != 1 || s.Current != 1 {
			t.Fatalf("Status = %+v, want 1 current by its row, failed", s)
		}
		// Once the log moves past the row (appended by a process without the
		// fold), a rebuild writes over it and the failure clears.
		must(0, h.Close(bg))
		without := session.NewService(e.root, e.st, session.Config{})
		h2, _ := must2(without.Open(bg, nil, h.ID(), session.OpenOptions{}))
		must(h2.Append(bg, ctxDraft("v")))
		must(0, h2.Close(bg))
		if err := reg.Rebuild(bg, storedKey, scope); err != nil {
			t.Fatal(err)
		}
		if s := statusOf(t, reg, storedKey); s.Failed != 0 || s.Current != 1 {
			t.Fatalf("after the rebuild Status = %+v, want 1 current", s)
		}
		rs, release := must2(e.svc.OpenRead(bg, h.ID()))
		defer release()
		if got := must(fold.Get(must(reg.Snapshot(bg, rs, nil, views.Stored)))); got != 2 {
			t.Fatalf("rebuilt value = %d, want 2", got)
		}
	})
}

// The groundwork of stage 15.2's manual check (exit evidence, labelled
// manual): a fold's version is bumped on a scratch home with 50 sessions, and
// Status moves from pending to current as the views-rebuild duty runs. The
// test logs both readings; run it with -v to record them. A second run within
// the duty's period does not run.
func TestManualCheckStatusAfterAVersionBump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	seedVersion1(t, path, 50)
	reg := views.New()
	must(views.Register(reg, stored(2, nil)))
	st, svc := bootAt(t, path, reg)
	defer reg.Bind(st, svc)()
	before := statusOf(t, reg, storedKey)
	t.Logf("manual: home %s, %s bumped to version %d: before the rebuild current=%d behind=%d pending=%d failed=%d",
		path, before.Key, before.Version, before.Current, before.Behind, before.Pending, before.Failed)
	ran, err := reg.RunRebuild(bg, time.Hour)
	if err != nil || !ran {
		t.Fatalf("RunRebuild = %v, %v", ran, err)
	}
	after := statusOf(t, reg, storedKey)
	t.Logf("manual: after the views-rebuild duty current=%d behind=%d pending=%d failed=%d",
		after.Current, after.Behind, after.Pending, after.Failed)
	if before.Pending != 50 || before.Current != 0 || after.Current != 50 || after.Pending != 0 {
		t.Fatalf("Status moved from %+v to %+v; want 50 pending to 50 current", before, after)
	}
	if ran, err := reg.RunRebuild(bg, time.Hour); err != nil || ran {
		t.Fatalf("a second RunRebuild within the hour = %v, %v; want not run", ran, err)
	}
}
