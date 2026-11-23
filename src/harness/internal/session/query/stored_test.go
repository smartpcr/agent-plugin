package query_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// logCounting counts the log reads of the readers it opens.
type logCounting struct {
	store.Store
	reads *atomic.Int64
}

func (s logCounting) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	r, err := s.Store.OpenRead(ctx, id)
	if err != nil {
		return nil, err
	}
	return logCountingReader{r, s.reads}, nil
}

type logCountingReader struct {
	store.Reader
	reads *atomic.Int64
}

func (r logCountingReader) Read(ctx context.Context, from event.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	r.reads.Add(1)
	return r.Reader.Read(ctx, from, limit, o)
}

func (r logCountingReader) ReadSeqs(ctx context.Context, seqs []event.Seq, o store.ReadOptions) ([]store.Record, error) {
	r.reads.Add(1)
	return r.Reader.ReadSeqs(ctx, seqs, o)
}

// failingRows fails the reader opens (open) or the row reads of its readers.
type failingRows struct {
	store.Store
	open bool
}

func (s failingRows) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	if s.open {
		return nil, errors.New("open refused")
	}
	r, err := s.Store.OpenRead(ctx, id)
	if err != nil {
		return nil, err
	}
	return failingRowsReader{r}, nil
}

type failingRowsReader struct{ store.Reader }

func (failingRowsReader) Checkpoints(context.Context) ([]store.Checkpoint, error) {
	return nil, errors.New("rows refused")
}

// A listing whose summary rows cannot be read fails, rather than listing a
// value it did not read; a session with no events needs no rows.
func TestListingFailsOnUnreadableRows(t *testing.T) {
	mem := memstore.New()
	reg := views.New()
	must(views.RegisterBuiltins(reg))
	writeWith(t, mem, reg, "s", []event.Draft{promptDraft(t, "hi")})
	for _, st := range []failingRows{{mem, true}, {mem, false}} {
		if _, err := query.New(st).List(bg, query.ListOptions{}); err == nil {
			t.Fatalf("listing over %+v succeeded", st.open)
		}
	}
	w := must(mem.Create(bg, store.Header{ID: "empty"}, store.CreateOptions{}))
	must(0, w.Close(bg))
	if err := mem.Delete(bg, "s", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	p := must(query.New(failingRows{mem, true}).List(bg, query.ListOptions{IncludeBlank: true}))
	if len(p.Entries) != 1 || p.Entries[0].SummaryState != views.Current || !p.Entries[0].Summary.Blank {
		t.Fatalf("an empty session = %+v", p.Entries)
	}
}

// writeWith creates session id on st through a service folding with reg and
// appends ds, then closes it.
func writeWith(t *testing.T, st store.Store, reg *views.Registry, id string, ds []event.Draft) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	defer root.Dispose()
	svc := session.NewService(root, st, session.Config{Folder: reg})
	h, _, err := svc.Open(bg, nil, session.ID(id), session.OpenOptions{})
	if errors.Is(err, store.ErrNotFound) {
		h, err = svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}})
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) > 0 {
		if _, err := h.AppendGroup(bg, ds); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
}

// Listing reads the list summary at Stored freshness (M1-G-STORE-12): a session
// whose summary fold is stale (its row is of another version) lists as pending
// without reading the log, even when blank sessions are hidden; one whose row
// is behind lists the row's value as behind with its seq; one with a current
// row lists as current. Observe reads at Exact and refuses a catch-up past its
// bound with view/behind.
//
//dsh:test DH-T-VIEW-7
func TestListingReadsStoredSummaries(t *testing.T) {
	mem := memstore.New()
	builtins := views.New()
	must(views.RegisterBuiltins(builtins))
	// A summary fold of another version stands for a fold that has changed
	// since its rows were written.
	stale := views.New()
	old := views.ListSummaryView()
	old.Version = 99
	must(views.Register(stale, old))
	noSummary := views.New()
	must(views.Register(noSummary, views.StatsView()))

	writeWith(t, mem, builtins, "current", []event.Draft{promptDraft(t, "hello current")})
	writeWith(t, mem, stale, "stale", append([]event.Draft{promptDraft(t, "hello stale")}, ctxDrafts("stale", 30)...))
	writeWith(t, mem, builtins, "behind", []event.Draft{promptDraft(t, "hello behind")})
	writeWith(t, mem, noSummary, "behind", ctxDrafts("behind", 3))
	writeWith(t, mem, builtins, "blank", ctxDrafts("blank", 2))

	var reads atomic.Int64
	q := query.New(logCounting{mem, &reads})
	p := must(q.List(bg, query.ListOptions{}))
	got := map[session.ID]query.Entry{}
	for _, e := range p.Entries {
		got[e.ID] = e
	}
	if reads.Load() != 0 {
		t.Fatalf("listing read the log %d times", reads.Load())
	}
	if _, listed := got["blank"]; listed || len(got) != 3 {
		t.Fatalf("listed %v, want current, stale and behind (blank hidden)", p.Entries)
	}
	if e := got["current"]; e.SummaryState != views.Current || e.AsOfSeq != 1 || e.Summary.Preview != "hello current" {
		t.Fatalf("current = %s at %d, %+v", e.SummaryState, e.AsOfSeq, e.Summary)
	}
	if e := got["stale"]; e.SummaryState != views.Pending || e.AsOfSeq != 0 || e.Summary != (views.ListSummary{}) || e.Events != 31 {
		t.Fatalf("stale = %s at %d, %+v over %d events", e.SummaryState, e.AsOfSeq, e.Summary, e.Events)
	}
	if e := got["behind"]; e.SummaryState != views.Behind || e.AsOfSeq != 1 || e.Events != 4 || e.Summary.Preview != "hello behind" {
		t.Fatalf("behind = %s at %d over %d events, %+v", e.SummaryState, e.AsOfSeq, e.Events, e.Summary)
	}
	l := must(q.ListByProject(bg, query.ListOptions{}))
	if len(l.Groups) != 1 || len(l.Groups[0].Sessions) != 3 || reads.Load() != 0 {
		t.Fatalf("by project = %+v after %d log reads", l.Groups, reads.Load())
	}
	for _, e := range l.Groups[0].Sessions {
		if e.SummaryState != got[e.ID].SummaryState {
			t.Fatalf("%s lists %s by project and %s by page", e.ID, e.SummaryState, got[e.ID].SummaryState)
		}
	}

	// Observe is Exact: the stale session's views fold its 31 events, within the
	// bound; with a bound of 10 it is view/behind with both positions.
	o := must(q.Observe(bg, "stale", query.Window{}))
	if o.Views.Summary.Preview != "hello stale" || o.Views.Stats.Events != 31 {
		t.Fatalf("observed views = %+v / %d events", o.Views.Summary, o.Views.Stats.Events)
	}
	_, err := query.New(mem, query.WithMaxCatchUp(10)).Observe(bg, "stale", query.Window{})
	var ve *views.Error
	if !errors.Is(err, views.ErrBehind) || !errors.As(err, &ve) || ve.AsOfSeq != 0 || ve.HeadSeq != 31 {
		t.Fatalf("a bounded Observe = %v (%+v), want view/behind at 0 of 31", err, ve)
	}
	if o := must(query.New(mem, query.WithMaxCatchUp(10)).Observe(bg, "current", query.Window{})); o.Views.Summary.Preview != "hello current" {
		t.Fatalf("a bounded Observe of a current session = %+v", o.Views.Summary)
	}
}
