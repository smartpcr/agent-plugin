//go:build bench

// DH-T-VIEW-25 (session-views.md, DH-ADR-TOOLCHAIN-14), the DH-D-VIEW-5 clauses:
// the first list page over the heavy-tailed session corpus (testkit/corpus) and
// a snapshot of the built-in views of a session above 100,000 events, each again
// in the memory-limited rerun (corpus.Budget). A case fails when its p95 is over
// its budget. Run it with
//
//	go test -tags bench -run '^$' -bench BudgetQuery -benchtime 20x -timeout 60m ./internal/session/query/
//
// It takes about three minutes on the machine of the first run: seeding the two
// homes (outside every timer), then each case twice (Go's b.N = 1 run also
// times corpus.MinSamples operations).
//
// The test's ranked-search and TreeMessages clauses are not here: they belong
// to DH-D-VIEW-6 and are built at DH-S-23 (M1-D-STORE-B (a)).

package query_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus/corpussqlite"
)

// The DH-T-VIEW-25 budgets (p95, nearest rank), compiled in so no environment
// can relax them. Provisional, from the first measured run, pinned at impl.md
// 16.7 (held-open entry) (M1-D-STORE-B (a)). The first run (Windows 11, i9-13900K,
// NVMe, go1.27.4, -benchtime 20x; plain and gomemlimit-4GiB runs) measured p95s
// of 530-547 ms for the first page (726 MB and 2.5 M allocations per page: each
// entry's reader opens its whole log, the newest session's 48 MB included) and
// 578-733 ms for the snapshot (790 MB, 2.9 M allocations). The budgets are from
// the run after the bounded reader open (an open judges the records from the
// newest checkpoint, not the whole log), same machine and flags: p95s of
// 21.3-23.0 ms for the first page (7.1 MB, 14.4 K allocations) and 47.3-47.9 ms
// for the snapshot (67.5 MB, 67.5 K allocations); each budget is about three
// times the worst p95.
const (
	budgetFirstPageP95 = 70 * time.Millisecond
	budgetSnapshotP95  = 150 * time.Millisecond
)

// corpusSessions is the size of the heavy-tailed corpus home: the smallest
// corpus holding every measured anchor (corpus.Corpus).
const corpusSessions = 1000

//dsh:test DH-T-VIEW-25
func BenchmarkBudgetQuery(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	long := openBench(b, filepath.Join(dir, "long.db"))
	n := corpussqlite.Seed(b, long, store.Header{ID: "long"}, corpus.Long(), corpussqlite.Epoch)
	if n <= corpus.LongEvents {
		b.Fatalf("the long session holds %d events, want more than %d", n, corpus.LongEvents)
	}
	shapes, err := corpus.Corpus(corpusSessions)
	if err != nil {
		b.Fatal(err)
	}
	home := openBench(b, filepath.Join(dir, "corpus.db"))
	ids := corpussqlite.Home(b, home, shapes)
	b.Logf("seeded: the long session (%d events), a %d-session corpus", n, len(ids))

	// The sidebar's first page: a query.DefaultLimit page by the last prompt,
	// each entry with its list summary. The corpus's longest session is the
	// newest, so the page holds it.
	q := query.New(home)
	corpus.Budget(b, "first-page", budgetFirstPageP95, func(b *testing.B, t *corpus.Timer) {
		var (
			p   query.Page
			err error
		)
		t.Time(func() { p, err = q.List(ctx, query.ListOptions{}) })
		if err != nil {
			b.Fatal(err)
		}
		if len(p.Entries) != query.DefaultLimit || string(p.Entries[0].ID) != ids[0] || p.Next == "" {
			b.Fatalf("first page: %d entries, next %q", len(p.Entries), p.Next)
		}
	})

	// A snapshot of the long session: the four built-in views at its head,
	// through Observe with a one-record window.
	ql := query.New(long)
	corpus.Budget(b, "snapshot", budgetSnapshotP95, func(b *testing.B, t *corpus.Timer) {
		var (
			o   query.Observation
			err error
		)
		t.Time(func() { o, err = ql.Observe(ctx, "long", query.Window{Limit: 1}) })
		if err != nil {
			b.Fatal(err)
		}
		if o.Head != n || o.Views.Summary.Turns != corpus.Long().Turns || len(o.Events) != 1 {
			b.Fatalf("snapshot: head %d of %d, %d turns, %d events", o.Head, n, o.Views.Summary.Turns, len(o.Events))
		}
	})
}

// openBench opens the store at path, closed when the benchmark ends.
func openBench(b *testing.B, path string) *sqlite.Store {
	b.Helper()
	s, err := sqlite.Open(context.Background(), path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			b.Error(err)
		}
	})
	return s
}
