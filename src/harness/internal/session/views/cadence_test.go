package views_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// upsert is one checkpoint row a committed batch carried.
type upsert struct {
	key   string
	seq   event.Seq
	bytes int
}

// recorder keeps the rows of every committed batch.
type recorder struct {
	mu  sync.Mutex
	ups []upsert
}

func (r *recorder) all() []upsert {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]upsert(nil), r.ups...)
}

// recordingStore records the checkpoint rows its writers commit.
type recordingStore struct {
	store.Store
	rec *recorder
}

func (s recordingStore) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := s.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return recordingWriter{w, s.rec}, nil
}

func (s recordingStore) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	w, err := s.Store.OpenWrite(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return recordingWriter{w, s.rec}, nil
}

type recordingWriter struct {
	store.Writer
	rec *recorder
}

func (w recordingWriter) Append(ctx context.Context, b store.Batch) error {
	if err := w.Writer.Append(ctx, b); err != nil {
		return err
	}
	w.rec.mu.Lock()
	for _, c := range b.Checkpoints {
		w.rec.ups = append(w.rec.ups, upsert{key: c.Key, seq: c.Seq, bytes: len(c.State)})
	}
	w.rec.mu.Unlock()
	return nil
}

// seqsOf is the seqs of key's rows in ups, in commit order.
func seqsOf(ups []upsert, key string) []event.Seq {
	var out []event.Seq
	for _, u := range ups {
		if u.key == key {
			out = append(out, u.seq)
		}
	}
	return out
}

// A Periodic row is written with the first batch, once it is
// CheckpointEveryEvents behind, with a batch holding a turn end and at close;
// an EveryCommit row with every batch, so it always reaches the head.
func TestPeriodicAndEveryCommitCadences(t *testing.T) {
	rec := &recorder{}
	reg := views.New(views.WithCheckpointEveryEvents(10))
	must(views.Register(reg, periodic(nil)))
	must(views.Register(reg, stored(1, nil)))
	e := newEnv(t, reg, func(s store.Store) store.Store { return recordingStore{s, rec} })
	h := e.create(t)
	appendN(t, h, 1) // seq 0: the first batch writes every stored row
	for range 9 {
		appendN(t, h, 1) // seqs 1..9: 9 behind, not due
	}
	appendN(t, h, 1) // seq 10: 10 behind, due
	appendN(t, h, 2) // seqs 11..12
	turn := turnDrafts(1)
	must(h.AppendGroup(bg, turn)) // holds a turn end: due
	appendN(t, h, 3)
	last := h.Len() - 1
	must(0, h.Close(bg))

	ups := rec.all()
	wantPeriodic := []event.Seq{0, 10, 12 + event.Seq(len(turn)), last}
	if got := seqsOf(ups, periodicKey); !equalSeqs(got, wantPeriodic) {
		t.Fatalf("periodic rows at %v, want %v", got, wantPeriodic)
	}
	every := seqsOf(ups, storedKey)
	if len(every) != 14 || every[len(every)-1] != last {
		t.Fatalf("every-commit rows at %v: want one per batch (14), the last at %d", every, last)
	}
	for _, u := range ups {
		if u.seq > last {
			t.Fatalf("row %+v is ahead of the log (last seq %d)", u, last)
		}
	}
}

func equalSeqs(a, b []event.Seq) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A cadence is EveryCommit or Periodic, and Periodic is for stored folds; the
// configuration fills unset fields with their defaults.
func TestCadenceAndConfigValidation(t *testing.T) {
	reg := views.New()
	notStored := periodic(nil)
	notStored.Stored = false
	if _, err := views.Register(reg, notStored); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("a Periodic fold that is not stored = %v", err)
	}
	odd := periodic(nil)
	odd.Cadence = views.Cadence(7)
	if _, err := views.Register(reg, odd); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("cadence 7 = %v", err)
	}
	if got := views.New().Config(); got != views.DefaultConfig() || got.CheckpointEveryEvents != 200 || got.MaxCatchUpEvents != 2_000 || got.RebuildConcurrency != 2 {
		t.Fatalf("default config = %+v", got)
	}
	got := views.New(views.WithConfig(views.Config{MaxCatchUpEvents: 5}), views.WithRebuildConcurrency(3), views.WithCheckpointEveryEvents(-1)).Config()
	if want := (views.Config{CheckpointEveryEvents: 200, MaxCatchUpEvents: 5, RebuildConcurrency: 3}); got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

// Scenario: The event index is written periodically (M1-G-STORE-13). Given
// 10,000 events appended to a sqlite session in 100 blocks (a whole turn, then
// request-context events four at a time), When the bytes upserted into
// checkpoints are summed, Then the every-commit rows stay the same size, so
// their total grows linearly; the event index is written at most once per turn
// end, per 200 events and at close, each write linear in the events it covers;
// and a non-holder's Stored read sees the index behind while an Exact read and
// the reopened views equal a refold.
func TestEventIndexIsWrittenPeriodically(t *testing.T) {
	const total, block = 10_000, 100
	path := filepath.Join(t.TempDir(), "dsh.db")
	rec := &recorder{}
	reg := views.New()
	must(views.RegisterBuiltins(reg))
	st, _ := bootAt(t, path, views.New())
	svc := session.NewService(cordisRoot(t), recordingStore{st, rec}, session.Config{Folder: reg})
	h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s-10k"}, Scope: scope.Key{}}))
	turns := 0
	for n := 1; int(h.Len())+block <= total; n++ {
		end := h.Len() + block
		must(h.AppendGroup(bg, turnDrafts(n)))
		turns++
		for h.Len() < end {
			appendN(t, h, min(4, int(end-h.Len())))
		}
	}
	if h.Len() != total {
		t.Fatalf("the session has %d events, want %d", h.Len(), total)
	}
	if _, err := h.Flush(bg); err != nil {
		t.Fatal(err)
	}

	// A process that is not the holder: the index row is behind (the last block
	// ends with request-context events), and Exact catches up from it.
	other := views.New()
	ob := must(views.RegisterBuiltins(other))
	_, osvc := bootAt(t, path, other)
	rs, release, err := osvc.OpenRead(bg, h.ID())
	if err != nil {
		t.Fatal(err)
	}
	ss := must(other.Snapshot(bg, rs, nil, views.Stored))
	if v, _ := ss.Value(views.EventIndexKey); v.State != views.Behind || v.AsOfSeq >= total {
		t.Fatalf("Stored event index = %+v, want behind", v)
	}
	if v, _ := ss.Value(views.ListSummaryKey); v != (views.Value{State: views.Current, AsOfSeq: total}) {
		t.Fatalf("Stored list summary = %+v, want current at %d", v, total)
	}
	exact := must(other.Snapshot(bg, rs, nil, views.Exact))
	release()
	must(0, h.Close(bg))

	ups := rec.all()
	r := must(st.OpenRead(bg, "s-10k"))
	recs := must(r.Read(bg, 0, 0, store.ReadOptions{}))
	header := r.Header()
	_ = r.Close(bg)
	evs := make([]event.Event, len(recs))
	for i, rc := range recs {
		evs[i] = event.Of(rc)
	}

	// Totals by class, cumulative at three points of the log.
	sum := func(at event.Seq, keep func(string) bool) (n int) {
		for _, u := range ups {
			if u.seq < at && keep(u.key) {
				n += u.bytes
			}
		}
		return n
	}
	everyCommit := func(k string) bool { return k == views.ListSummaryKey || k == views.StatsKey }
	periodicKeys := func(k string) bool { return k == views.TurnIndexKey || k == views.EventIndexKey }
	core := func(k string) bool { return strings.HasPrefix(k, "core/") }
	for _, at := range []event.Seq{2_500, 5_000, total} {
		t.Logf("upserted through %5d events: every-commit %9d B, periodic %10d B (event index %10d B), core %9d B",
			at, sum(at, everyCommit), sum(at, periodicKeys), sum(at, func(k string) bool { return k == views.EventIndexKey }), sum(at, core))
	}
	ec5, ec10 := sum(total/2, everyCommit), sum(total, everyCommit)
	if ratio := float64(ec10) / float64(ec5); ratio < 1.8 || ratio > 2.2 {
		t.Fatalf("every-commit bytes %d at 5,000 events and %d at 10,000: ratio %.2f, not linear", ec5, ec10, ratio)
	}
	// Each index write is linear in the events it covers.
	var writes int
	lo, hi := 1e18, 0.0
	var cf float64 // the index bytes had it been written with every batch
	for _, u := range ups {
		if u.key != views.EventIndexKey {
			continue
		}
		writes++
		if u.seq >= 200 {
			per := float64(u.bytes) / float64(u.seq+1)
			lo, hi = min(lo, per), max(hi, per)
		}
	}
	if hi > 2*lo {
		t.Fatalf("event index bytes per covered event range %.1f..%.1f: a write is not linear in the log", lo, hi)
	}
	if limit := turns + total/views.DefaultCheckpointEveryEvents + 2; writes > limit {
		t.Fatalf("the event index was written %d times, over turns + every 200 events + first + close = %d", writes, limit)
	}
	for _, s := range seqsOf(ups, views.StatsKey) {
		cf += hi * float64(s+1)
	}
	t.Logf("event index: %d writes over %d turns, %.1f..%.1f B per covered event; written with every batch it would be about %.0f B",
		writes, turns, lo, hi, cf)

	// The rows after close and the Exact read equal a refold; so do the views
	// a reopening process restores from the rows.
	reg2 := views.New()
	b2 := must(views.RegisterBuiltins(reg2))
	_, svc2 := bootAt(t, path, reg2)
	h2, _, err := svc2.Open(bg, nil, "s-10k", session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close(bg)
	reopened := must(reg2.Read(bg, h2))
	sameAsRefold(t, "Exact", views.ListSummaryView(), header, evs, must(ob.ListSummary.Get(exact)))
	sameAsRefold(t, "Exact", views.TurnIndexView(), header, evs, must(ob.TurnIndex.Get(exact)))
	sameAsRefold(t, "Exact", views.StatsView(), header, evs, must(ob.Stats.Get(exact)))
	sameAsRefold(t, "Exact", views.EventIndexView(), header, evs, must(ob.EventIndex.Get(exact)))
	sameAsRefold(t, "reopened", views.ListSummaryView(), header, evs, must(b2.ListSummary.Get(reopened)))
	sameAsRefold(t, "reopened", views.TurnIndexView(), header, evs, must(b2.TurnIndex.Get(reopened)))
	sameAsRefold(t, "reopened", views.StatsView(), header, evs, must(b2.Stats.Get(reopened)))
	sameAsRefold(t, "reopened", views.EventIndexView(), header, evs, must(b2.EventIndex.Get(reopened)))
}

// cordisRoot is a root context whose logs stay in memory, disposed at cleanup.
func cordisRoot(t *testing.T) *cordis.Context {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	return root
}

// sameAsRefold fails unless got equals d refolded over evs, as JSON.
func sameAsRefold[S any](t *testing.T, what string, d views.Definition[S], h session.Header, evs []event.Event, got S) {
	t.Helper()
	a, b := must(json.Marshal(got)), must(json.Marshal(views.Refold(d, h, evs)))
	if string(a) != string(b) {
		t.Fatalf("%s %s differs from the refold (%d vs %d bytes)", what, d.Key, len(a), len(b))
	}
}
