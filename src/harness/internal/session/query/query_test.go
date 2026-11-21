package query_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ctxDrafts(prefix string, n int) []event.Draft {
	ds := make([]event.Draft, n)
	for i := range ds {
		ds[i] = must(event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: fmt.Sprintf("%s-%d", prefix, i)}))
	}
	return ds
}

func valueOf(t *testing.T, rec store.Record) string {
	t.Helper()
	p, err := event.RequestContext.As(event.Of(rec))
	if err != nil {
		t.Fatalf("seq %d: %v", rec.Seq, err)
	}
	return p.Value
}

type env struct {
	st  store.Store
	svc session.Service
}

func newEnv(t *testing.T) *env { return newEnvOn(t, memstore.New()) }

func newEnvOn(t *testing.T, st store.Store) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	reg := views.New()
	must(views.RegisterBuiltins(reg))
	return &env{st: st, svc: session.NewService(root, st, session.Config{Folder: reg})}
}

// create makes a session of n request-context events and closes it.
func (e *env) create(t *testing.T, id string, n int) { e.createIn(t, id, "", n) }

func (e *env) createIn(t *testing.T, id, cwd string, n int) {
	t.Helper()
	h := must(e.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id, Cwd: cwd}}))
	if _, err := h.AppendGroup(bg, ctxDrafts(id, n)); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
}

// fork forks parent through seq into id and appends n events of its own.
func (e *env) fork(t *testing.T, id, parent string, through event.Seq, n int) {
	e.forkIn(t, id, "", parent, through, n)
}

func (e *env) forkIn(t *testing.T, id, cwd, parent string, through event.Seq, n int) {
	t.Helper()
	w := must(e.st.Create(bg, store.Header{ID: id, Cwd: cwd}, store.CreateOptions{ForkOf: &store.ForkOf{ID: parent, Through: through}}))
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return
	}
	h, _, err := e.svc.Open(bg, nil, session.ID(id), session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.AppendGroup(bg, ctxDrafts(id, n)); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
}

// DH-D-VIEW-5: a fork's trace labels its inherited prefix with the ancestor
// that wrote it, across two forks, and the lineage lists the chain root first
// and the direct forks.
func TestTraceAcrossForks(t *testing.T) {
	e := newEnv(t)
	e.create(t, "p", 15)
	e.fork(t, "c", "p", 9, 5)  // c: p[0..9] + 5 of its own = 15
	e.fork(t, "g", "c", 12, 2) // g: p[0..9] + c[10..12] + 2 = 15
	e.fork(t, "c2", "p", 3, 1)
	q := query.New(e.st)

	l := must(q.Lineage(bg, "g"))
	if len(l.Chain) != 3 || l.Chain[0].ID != "p" || l.Chain[0].Through != 9 || l.Chain[1].ID != "c" || l.Chain[1].Through != 12 || l.Chain[2].ID != "g" {
		t.Fatalf("lineage of g = %+v", l.Chain)
	}
	if pl := must(q.Lineage(bg, "p")); len(pl.Forks) != 2 || pl.Forks[0] != "c" || pl.Forks[1] != "c2" {
		t.Fatalf("forks of p = %v", pl.Forks)
	}

	tr := must(q.Trace(bg, "g", query.Window{Limit: 100}))
	if len(tr.Events) != 15 || tr.More {
		t.Fatalf("trace of g: %d events, more %v", len(tr.Events), tr.More)
	}
	for _, ev := range tr.Events {
		want := session.ID("g")
		switch {
		case ev.Seq <= 9:
			want = "p"
		case ev.Seq <= 12:
			want = "c"
		}
		if ev.Origin != want {
			t.Fatalf("seq %d origin %s, want %s", ev.Seq, ev.Origin, want)
		}
		if v := valueOf(t, ev.Record); v[:len(want)+1] != string(want)+"-" {
			t.Fatalf("seq %d value %q not written by %s", ev.Seq, v, want)
		}
	}

	// Bounded: a window of 4 from 8 pages.
	page := must(q.Trace(bg, "g", query.Window{From: 8, Limit: 4}))
	if len(page.Events) != 4 || page.Next != 12 || !page.More || page.Events[1].Origin != "p" || page.Events[2].Origin != "c" {
		t.Fatalf("trace page = %d events next %d more %v", len(page.Events), page.Next, page.More)
	}

	// A fork that keeps less of its parent than the parent kept of its own
	// (p -> c through 9, c -> g2 through 3): g2's own records past 3 are g2's,
	// not p's.
	e.fork(t, "g2", "c", 3, 2)
	short := must(q.Trace(bg, "g2", query.Window{}))
	if len(short.Events) != 6 {
		t.Fatalf("trace of g2: %d events", len(short.Events))
	}
	for _, ev := range short.Events {
		want := session.ID("g2")
		if ev.Seq <= 3 {
			want = "p"
		}
		if ev.Origin != want {
			t.Fatalf("g2 seq %d origin %s, want %s", ev.Seq, ev.Origin, want)
		}
		if v := valueOf(t, ev.Record); v[:len(want)+1] != string(want)+"-" {
			t.Fatalf("g2 seq %d value %q not written by %s", ev.Seq, v, want)
		}
	}

	// The parent deleted: the lineage marks it gone, the trace still labels.
	if err := e.st.Delete(bg, "c2", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	e.fork(t, "o", "g", 14, 0)
	if err := e.st.Delete(bg, "g", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	ol := must(q.Lineage(bg, "o"))
	if len(ol.Chain) != 2 || !ol.Chain[0].Gone || ol.Chain[0].ID != "g" {
		t.Fatalf("lineage past a deleted parent = %+v", ol.Chain)
	}
}

// DH-D-VIEW-5: observe is a bounded window plus the built-in views; follow
// delivers a fork's inherited prefix, then its new records, once and in order.
func TestObserveAndFollowAFork(t *testing.T) {
	e := newEnv(t)
	e.create(t, "p", 12)
	e.fork(t, "c", "p", 9, 0)
	q := query.New(e.st, query.WithFollowInterval(5*time.Millisecond), query.WithMarks(workspace.NewMemoryMarks()))

	o := must(q.Observe(bg, "c", query.Window{From: 4, Limit: 3}))
	if o.Head != 10 || len(o.Events) != 3 || o.Events[0].Seq != 4 || o.Next != 7 || !o.More {
		t.Fatalf("observe = head %d, %d events, next %d, more %v", o.Head, len(o.Events), o.Next, o.More)
	}
	if o.Views.Stats.Events != 10 || len(o.Views.Index.Events) != 10 || !o.Views.Summary.Blank {
		t.Fatalf("observe views = %+v", o.Views.Stats)
	}

	f := must(q.Follow(bg, "c", 0))
	defer f.Close()
	h, _, err := e.svc.Open(bg, nil, "c", session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(bg)
	var got []store.Record
	deadline := time.After(5 * time.Second)
	appended := false
	for len(got) < 13 {
		if len(got) == 10 && !appended {
			if _, err := h.AppendGroup(bg, ctxDrafts("c", 3)); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Flush(bg); err != nil {
				t.Fatal(err)
			}
			appended = true
		}
		select {
		case rec, ok := <-f.Events():
			if !ok {
				t.Fatalf("follower stopped: %v", f.Err())
			}
			got = append(got, rec)
		case <-deadline:
			t.Fatalf("followed %d records, want 13", len(got))
		}
	}
	for i, rec := range got {
		if rec.Seq != event.Seq(i) {
			t.Fatalf("record %d has seq %d", i, rec.Seq)
		}
		want := "p-"
		if i >= 10 {
			want = "c-"
		}
		if v := valueOf(t, rec); v[:2] != want {
			t.Fatalf("seq %d = %q, want from %s", i, v, want)
		}
	}
	f.Close()
	if _, ok := <-f.Events(); ok {
		t.Fatal("events open after Close")
	}
	if err := f.Err(); err != nil {
		t.Fatalf("Err after Close = %v", err)
	}
}
