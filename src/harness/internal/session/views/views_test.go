package views_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
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

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ctxDraft(v string) event.Draft {
	return must(event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: v}))
}

type env struct {
	root *cordis.Context
	st   store.Store
	svc  session.Service
	reg  *views.Registry
}

func newEnv(t *testing.T, reg *views.Registry, wrap ...func(store.Store) store.Store) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	var st store.Store = memstore.New()
	for _, w := range wrap {
		st = w(st)
	}
	return &env{root: root, st: st, reg: reg, svc: session.NewService(root, st, session.Config{Folder: reg})}
}

func (e *env) create(t *testing.T) session.Handle {
	t.Helper()
	h := must(e.svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	return h
}

// count counts every event; last is the last seq seen, as next-seq.
func count() views.Definition[int] {
	return views.Definition[int]{Key: "test/count", Version: 1,
		Init:  func(session.Header) int { return 0 },
		Apply: func(n int, _ event.Event) (int, bool) { return n + 1, true }}
}

func last() views.Definition[event.Seq] {
	return views.Definition[event.Seq]{Key: "test/next", Version: 1,
		Init:  func(session.Header) event.Seq { return 0 },
		Apply: func(_ event.Seq, ev event.Event) (event.Seq, bool) { return ev.Seq() + 1, true }}
}

// Scenario: Folds read at one cut (DH-D-VIEW-1).
//
//dsh:test DH-T-VIEW-2
func TestFoldsReadAtOneCut(t *testing.T) {
	reg := views.New()
	cnt := must(views.Register(reg, count()))
	nxt := must(views.Register(reg, last()))
	e := newEnv(t, reg)
	h := e.create(t)

	var stop atomic.Bool
	var wg sync.WaitGroup
	// P-C: the cleanup stops the appenders (unblock), then joins them.
	t.Cleanup(func() { stop.Store(true); wg.Wait() })
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := h.Append(bg, ctxDraft("v")); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	cuts := map[event.Seq]bool{}
	deadline := time.Now().Add(10 * time.Second)
	for reads := 0; reads < 300 || len(cuts) < 20; reads++ {
		if time.Now().After(deadline) {
			break
		}
		snap := must(reg.Read(bg, h))
		c, n := must(cnt.Get(snap)), must(nxt.Get(snap))
		if event.Seq(c) != snap.AsOf || n != snap.AsOf {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("one read, two cuts: count %d, next %d, AsOf %d", c, n, snap.AsOf)
		}
		cuts[snap.AsOf] = true
	}
	stop.Store(true)
	wg.Wait()
	if len(cuts) < 20 {
		t.Fatalf("the reader saw %d cuts; the appender did not run concurrently", len(cuts))
	}
	final := must(reg.Read(bg, h))
	if final.AsOf != h.Len() || must(cnt.Get(final)) != int(h.Len()) {
		t.Fatalf("final read at %d, count %d, session len %d", final.AsOf, must(cnt.Get(final)), h.Len())
	}
	// A read-only open of the same session folds from the log to the same values.
	rs, done := must2(e.svc.OpenRead(bg, h.ID()))
	defer done()
	if _, err := h.Flush(bg); err != nil {
		t.Fatal(err)
	}
	cold := must(reg.Read(bg, rs))
	if must(cnt.Get(cold)) != int(cold.AsOf) {
		t.Fatalf("read-only fold %d at %d", must(cnt.Get(cold)), cold.AsOf)
	}
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

//dsh:test DH-T-VIEW-3
func TestRegistrationIsOnceAndBeforeUse(t *testing.T) {
	reg := views.New()
	must(views.Register(reg, count()))
	if _, err := views.Register(reg, count()); !errors.Is(err, views.ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	bad := count()
	bad.Key, bad.Version = "test/bad", 0
	if _, err := views.Register(reg, bad); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("version 0 = %v", err)
	}
	none := count()
	none.Key, none.Types = "test/none", []string{}
	if _, err := views.Register(reg, none); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("empty types = %v", err)
	}
	e := newEnv(t, reg)
	h := e.create(t)
	must(h.Append(bg, ctxDraft("v")))
	late := last()
	if _, err := views.Register(reg, late); !errors.Is(err, views.ErrSealed) {
		t.Fatalf("late registration = %v", err)
	}
	if _, err := reg.Read(bg, h, "test/next"); !errors.Is(err, views.ErrUnknownKey) {
		t.Fatalf("unknown key = %v", err)
	}
	snap := must(reg.Read(bg, h, "test/count"))
	other := views.Handle[int]{}
	if _, err := other.Get(snap); !errors.Is(err, views.ErrUnknownKey) {
		t.Fatalf("Get of a key the snapshot lacks = %v", err)
	}
	if got := snap.Keys(); !slices.Equal(got, []string{"test/count"}) {
		t.Fatalf("Keys = %v", got)
	}
}

//dsh:test DH-T-VIEW-3, DH-T-VIEW-7
func TestDispatchByTypeAndContainedPanic(t *testing.T) {
	reg := views.New()
	var calls atomic.Int32
	only := must(views.Register(reg, views.Definition[[]string]{Key: "test/turns", Version: 1,
		Types: []string{event.TurnStartType.Name()},
		Init:  func(session.Header) []string { return nil },
		Apply: func(s []string, ev event.Event) ([]string, bool) {
			calls.Add(1)
			return append(slices.Clone(s), ev.Type()), true
		}}))
	boom := must(views.Register(reg, views.Definition[int]{Key: "test/boom", Version: 1,
		Init: func(session.Header) int { return 0 },
		Apply: func(n int, ev event.Event) (int, bool) {
			if ev.Type() == event.RequestContext.Name() {
				panic("bad fold")
			}
			return n + 1, true
		}}))
	cnt := must(views.Register(reg, count()))
	e := newEnv(t, reg)
	h := e.create(t)
	must(h.Append(bg, ctxDraft("a")))
	must(h.Append(bg, ctxDraft("b")))
	snap := must(reg.Read(bg, h))
	if got := must(only.Get(snap)); len(got) != 0 || calls.Load() != 0 {
		t.Fatalf("turns fold saw undeclared types: %v (%d calls)", got, calls.Load())
	}
	if _, err := boom.Get(snap); !errors.Is(err, views.ErrFailed) {
		t.Fatalf("panicking fold = %v, want view/failed", err)
	}
	if must(cnt.Get(snap)) != int(snap.AsOf) {
		t.Fatalf("a sibling's panic disturbed count: %d at %d", must(cnt.Get(snap)), snap.AsOf)
	}
}

// failing refuses the nth append, so the registry must discard that batch's fold.
type failing struct {
	store.Store
	n *atomic.Int32
}

func (f failing) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := f.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return failingWriter{w, f.n}, nil
}

type failingWriter struct {
	store.Writer
	n *atomic.Int32
}

func (w failingWriter) Append(ctx context.Context, b store.Batch) error {
	if w.n.Add(-1) == 0 {
		return errors.New("disk full")
	}
	return w.Writer.Append(ctx, b)
}

func TestFailedAppendIsNotFolded(t *testing.T) {
	reg := views.New()
	cnt := must(views.Register(reg, count()))
	n := &atomic.Int32{}
	e := newEnv(t, reg, func(s store.Store) store.Store { return failing{s, n} })
	h := e.create(t)
	must(h.Append(bg, ctxDraft("a")))
	n.Store(1)
	if _, err := h.Append(bg, ctxDraft("lost")); err == nil {
		t.Fatal("the append should have failed")
	}
	must(h.Append(bg, ctxDraft("b")))
	snap := must(reg.Read(bg, h))
	if must(cnt.Get(snap)) != int(h.Len()) || snap.AsOf != h.Len() {
		t.Fatalf("count %d at %d, len %d: the failed batch was folded", must(cnt.Get(snap)), snap.AsOf, h.Len())
	}
}
