package views_test

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

const storedKey = "test/stored"

// stored counts events by step: version 1 adds 1 per event, version 2 adds 2,
// so a row of one version read as the other would show.
func stored(version int, calls *atomic.Int64) views.Definition[int] {
	return views.Definition[int]{Key: storedKey, Version: version, Stored: true,
		Init: func(session.Header) int { return 0 },
		Apply: func(n int, _ event.Event) (int, bool) {
			if calls != nil {
				calls.Add(1)
			}
			return n + version, true
		}}
}

// boot is one process: a sqlite store over path and a session service with reg.
func boot(t *testing.T, path string, reg *views.Registry) (*sqlite.Store, session.Service, func()) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	st, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	return st, session.NewService(root, st, session.Config{Folder: reg}), func() {
		root.Dispose()
		if err := st.Close(bg); err != nil {
			t.Fatal(err)
		}
	}
}

func checkpointsOf(t *testing.T, st store.Store, id session.ID) map[int]store.Checkpoint {
	t.Helper()
	r, err := st.OpenRead(bg, string(id))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(bg)
	cps, err := r.Checkpoints(bg)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]store.Checkpoint{}
	for _, c := range cps {
		if c.Key == storedKey {
			out[c.StateVersion] = c
		}
	}
	return out
}

// Scenario: Changed fold version rebuilds (DH-D-VIEW-2, TC-08). A stored fold
// at version 1 over 20 events, re-registered at version 2: the store opens, the
// stored state is rebuilt, records version 2 and its seq is the log head.
func TestChangedFoldVersionRebuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")

	reg1 := views.New()
	must(views.Register(reg1, stored(1, nil)))
	st, svc, stop := boot(t, path, reg1)
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	id := h.ID()
	for i := range 20 {
		if _, err := h.Append(bg, ctxDraft("v")); err != nil {
			t.Fatal(err)
		}
		// Stored state is never ahead of the log.
		if cps := checkpointsOf(t, st, id); cps[1].Seq >= h.Len() {
			t.Fatalf("after event %d the row is at seq %d, the log ends at %d", i, cps[1].Seq, h.Len())
		}
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	v1 := checkpointsOf(t, st, id)[1]
	if v1.Seq != 19 || v1.State != "20" {
		t.Fatalf("version 1 row = %+v, want seq 19 state 20", v1)
	}
	stop()

	// Restart with the fold at version 2.
	reg2 := views.New()
	h2fold := must(views.Register(reg2, stored(2, nil)))
	st, svc, stop = boot(t, path, reg2)
	h2, _, err := svc.Open(bg, nil, id, session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head := h2.Len()
	if head != 20 {
		t.Fatalf("reopened log head = %d, want 20", head)
	}
	v2, ok := checkpointsOf(t, st, id)[2]
	if !ok || v2.StateVersion != 2 || v2.Seq != head-1 || v2.State != "40" {
		t.Fatalf("version 2 row = %+v (present %v), want seq %d state 40", v2, ok, head-1)
	}
	snap := must(reg2.Read(bg, h2))
	if got := must(h2fold.Get(snap)); got != 40 || snap.AsOf != head {
		t.Fatalf("read = %d at %d, want 40 at %d", got, snap.AsOf, head)
	}
	if err := h2.Close(bg); err != nil {
		t.Fatal(err)
	}
	stop()

	// A third open at the same version takes the fold up from its row: no
	// event is folded again, and later batches advance the row.
	var calls atomic.Int64
	reg3 := views.New()
	h3fold := must(views.Register(reg3, stored(2, &calls)))
	st, svc, stop = boot(t, path, reg3)
	defer stop()
	h3, _, err := svc.Open(bg, nil, id, session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer h3.Close(bg)
	if n := calls.Load(); n != 0 {
		t.Fatalf("restoring a current row folded %d events", n)
	}
	if _, err := h3.Append(bg, ctxDraft("w")); err != nil {
		t.Fatal(err)
	}
	if got := must(h3fold.Get(must(reg3.Read(bg, h3)))); got != 42 {
		t.Fatalf("after one more event = %d, want 42", got)
	}
	if v2 := checkpointsOf(t, st, id)[2]; v2.Seq != 20 || v2.State != "42" {
		t.Fatalf("row after append = %+v, want seq 20 state 42", v2)
	}
}

// A read of a session the registry has not followed starts each stored fold
// from its row, ignoring a row of another version.
func TestUnfollowedReadStartsFromRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	reg1 := views.New()
	must(views.Register(reg1, stored(2, nil)))
	_, svc, stop := boot(t, path, reg1)
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	for range 5 {
		must(h.Append(bg, ctxDraft("v")))
	}
	id := h.ID()
	must(0, h.Close(bg))
	stop()

	var calls atomic.Int64
	reg2 := views.New()
	fold := must(views.Register(reg2, stored(2, &calls)))
	_, svc, stop = boot(t, path, views.New())
	defer stop()
	r, release, err := svc.OpenRead(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := must(fold.Get(must(reg2.Read(bg, r)))); got != 10 || calls.Load() != 0 {
		t.Fatalf("read = %d after %d folded events, want 10 after 0", got, calls.Load())
	}

	reg3 := views.New()
	fold3 := must(views.Register(reg3, stored(3, &calls)))
	if got := must(fold3.Get(must(reg3.Read(bg, r)))); got != 15 || calls.Load() != 5 {
		t.Fatalf("read at another version = %d after %d folded events, want 15 after 5", got, calls.Load())
	}
}

func TestCoreKeysAreReserved(t *testing.T) {
	if _, err := views.Register(views.New(), views.Definition[int]{Key: "core/x", Version: 1,
		Init: func(session.Header) int { return 0 }, Apply: func(n int, _ event.Event) (int, bool) { return n, false }}); err == nil {
		t.Fatal("a fold under core/ was registered")
	}
}
