//go:build bench

// DH-T-STORE-18 (session-store.md, DH-ADR-TOOLCHAIN-14), the benchmark half of
// the store's budget tests (budget_test.go holds the F-2 append budgets the
// budget gate runs): opening one session of more than 100,000 events from its
// checkpoints, forking it, and the first list page over the heavy-tailed session
// corpus (testkit/corpus), each again in the memory-limited rerun
// (corpus.Budget). A case fails when its p95 is over its budget. Run it with
//
//	go test -tags bench -run '^$' -bench BudgetStore -benchtime 20x -timeout 60m ./internal/session/store/sqlite/
//
// It takes about nine minutes on the machine of the first run: seeding the two
// homes (outside every timer), then each case twice (Go's b.N = 1 run also
// times corpus.MinSamples operations).

package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus/corpussqlite"
)

// The DH-T-STORE-18 budgets (p95, nearest rank), compiled in so no environment
// can relax them. Provisional, from the first measured run, pinned at impl.md
// 16.7 (held-open entry) (M1-D-STORE-B (a)). The first run (Windows 11, i9-13900K,
// NVMe, go1.27.4, -benchtime 20x; plain and gomemlimit-4GiB runs) measured p95s
// of 2.95-3.28 s for the open (2.08 GB and 10.5 M allocations per open), 1.43-1.55 s
// for the fork (732 MB, 3.0 M allocations) and 0.51-0.55 ms for the first page
// (75 KB, 450 allocations); each budget is about three times the worst p95. The
// Windows wall clock ticks in about half a millisecond, so the first page's p95
// reads in whole ticks there.
const (
	budgetLongOpenP95  = 10 * time.Second
	budgetLongForkP95  = 5 * time.Second
	budgetFirstPageP95 = 2 * time.Millisecond
)

// corpusSessions is the size of the heavy-tailed corpus home: the smallest
// corpus holding every measured anchor (corpus.Corpus).
const corpusSessions = 1000

//dsh:test DH-T-STORE-18
func BenchmarkBudgetStore(b *testing.B) {
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
	root := testkit.Tree(b)

	// The open a resume does: the lease, the core folds from their checkpoint
	// and the built-in views from their rows (DH-D-VIEW-2), then the session is
	// live. The close (untimed) writes nothing: nothing was appended.
	corpus.Budget(b, "open", budgetLongOpenP95, func(b *testing.B, t *corpus.Timer) {
		reg := views.New()
		if _, err := views.RegisterBuiltins(reg); err != nil {
			b.Fatal(err)
		}
		svc := session.NewService(root, long, session.Config{Folder: reg})
		var (
			h   session.Handle
			rep session.OpenReport
			err error
		)
		t.Time(func() { h, rep, err = svc.Open(ctx, nil, "long", session.OpenOptions{}) })
		if err != nil {
			b.Fatal(err)
		}
		if err := h.Close(ctx); err != nil || !rep.FromCheckpoint || h.Len() != n {
			b.Fatalf("open: close %v, from a checkpoint %v, %d of %d events", err, rep.FromCheckpoint, h.Len(), n)
		}
	})

	// A fork of the whole session: the row and the copy of every event in one
	// transaction. The fork is closed and deleted, untimed.
	forks := 0
	corpus.Budget(b, "fork", budgetLongForkP95, func(b *testing.B, t *corpus.Timer) {
		forks++
		id := fmt.Sprintf("fork-%d", forks)
		var (
			w   store.Writer
			err error
		)
		t.Time(func() {
			w, err = long.Create(ctx, store.Header{ID: id}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "long", Through: n - 1}})
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := w.Close(ctx); err != nil {
			b.Fatal(err)
		}
		if sum, err := long.Stat(ctx, id); err != nil || sum.NextSeq != n {
			b.Fatalf("fork: %+v, %v", sum, err)
		}
		if err := long.Delete(ctx, id, store.DeleteOptions{}); err != nil {
			b.Fatal(err)
		}
	})

	// The store's first page in list order (the last prompt, the order
	// query.List asks for), a query.DefaultLimit page.
	corpus.Budget(b, "first-page", budgetFirstPageP95, func(b *testing.B, t *corpus.Timer) {
		var (
			p   store.Page
			err error
		)
		t.Time(func() { p, err = home.List(ctx, store.Query{Order: store.OrderPrompt, Limit: query.DefaultLimit}) })
		if err != nil {
			b.Fatal(err)
		}
		if len(p.Summaries) != query.DefaultLimit || p.Summaries[0].Header.ID != ids[0] || p.Next == "" {
			b.Fatalf("first page: %d summaries, next %q", len(p.Summaries), p.Next)
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
