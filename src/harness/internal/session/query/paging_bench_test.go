//go:build bench

// DH-T-VIEW-15 (session-views.md, DH-D-VIEW-5): "a 10,000-session benchmark is
// recorded". query.List over a home of 10,000 sessions: the heavy-tailed
// corpus (testkit/corpus, its 1,000 sessions, the newest the longest) and 9,000
// older sessions of the corpus's smallest anchor, its first page and a page two
// pages from the end, each again in the memory-limited rerun (corpus.Budget).
// A case fails when its p95 is over its budget. Run it with
//
//	go test -tags bench -run '^$' -bench BudgetList10k -benchtime 20x -timeout 60m ./internal/session/query/
//
// Seeding the home (outside every timer) takes most of its run. The 1,000
// sessions of the heavy tail hold every measured anchor (corpus.Corpus); a
// heavy-tailed corpus of 10,000 would hold about 8 GB, and a page's cost is
// its sessions' (each read at Stored freshness), not the home's.

package query_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus/corpussqlite"
)

// The 10,000-session list budgets (p95, nearest rank), compiled in so no
// environment can relax them. Provisional, from the first measured run, pinned
// with the DH-T-VIEW-25 budgets at impl.md 16.7 (held-open entry)
// (M1-D-STORE-B (a)). The first run (Windows 11, i9-13900K, NVMe, go1.27.4,
// -benchtime 20x; plain and gomemlimit-4GiB runs) measured p95s of 31.3-32.1 ms
// for the first page (7.2 MB, 17.7 K allocations; the 1,000-session home's
// first page, DH-T-VIEW-25, is 21.3-23.0 ms: this one holds the same sessions)
// and 22.0-22.6 ms for the deep page (0.9 MB, 16.9 K allocations); each budget
// is about three times the worst p95.
const (
	budgetList10kFirstP95 = 100 * time.Millisecond
	budgetList10kDeepP95  = 70 * time.Millisecond
)

// homeSessions is the size of the home; fillers are the sessions past the
// heavy-tailed corpus.
const (
	homeSessions = 10_000
	fillers      = homeSessions - corpusSessions
)

//dsh:test DH-T-VIEW-15
func BenchmarkBudgetList10k(b *testing.B) {
	ctx := context.Background()
	home := openBench(b, filepath.Join(b.TempDir(), "home.db"))
	shapes, err := corpus.Corpus(corpusSessions)
	if err != nil {
		b.Fatal(err)
	}
	ids := corpussqlite.Home(b, home, shapes)
	small, err := corpus.Anchor(0)
	if err != nil {
		b.Fatal(err)
	}
	// The fillers are older than every corpus session, filler 0 the oldest, so
	// the listing is the corpus, then the fillers newest first.
	for i := range fillers {
		id := fmt.Sprintf("filler-%04d", i)
		corpussqlite.Seed(b, home, store.Header{ID: id}, small, corpussqlite.Epoch-int64(fillers-i)*corpussqlite.Slot.Milliseconds())
	}
	at := func(pos int) string {
		if pos < corpusSessions {
			return ids[pos]
		}
		return fmt.Sprintf("filler-%04d", fillers-1-(pos-corpusSessions))
	}
	b.Logf("seeded a %d-session home: the %d-session corpus and %d fillers", homeSessions, corpusSessions, fillers)

	q := query.New(home)
	corpus.Budget(b, "first-page", budgetList10kFirstP95, func(b *testing.B, t *corpus.Timer) {
		var (
			p   query.Page
			err error
		)
		t.Time(func() { p, err = q.List(ctx, query.ListOptions{}) })
		if err != nil {
			b.Fatal(err)
		}
		if len(p.Entries) != query.DefaultLimit || string(p.Entries[0].ID) != at(0) || p.Next == "" {
			b.Fatalf("first page: %d entries, next %q", len(p.Entries), p.Next)
		}
	})

	// The page two pages from the end: its cursor is after the session at
	// position homeSessions - 2*DefaultLimit - 1.
	before := homeSessions - 2*query.DefaultLimit - 1
	sum, err := home.Stat(ctx, at(before))
	if err != nil {
		b.Fatal(err)
	}
	cur := store.PromptCursor{AsOf: time.Now().UnixNano(), Key: sum.LastPromptAt.UnixNano(), ID: sum.Header.ID}.String()
	corpus.Budget(b, "deep-page", budgetList10kDeepP95, func(b *testing.B, t *corpus.Timer) {
		var (
			p   query.Page
			err error
		)
		t.Time(func() { p, err = q.List(ctx, query.ListOptions{Cursor: cur}) })
		if err != nil {
			b.Fatal(err)
		}
		if len(p.Entries) != query.DefaultLimit || string(p.Entries[0].ID) != at(before+1) || p.Next == "" {
			b.Fatalf("deep page: %d entries, first %v, next %q", len(p.Entries), p.Entries, p.Next)
		}
	})
}
