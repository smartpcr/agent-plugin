package sqlite_test

import (
	"slices"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// The budgets of fact F-2 (DH-T-STORE-18, DH-ADR-TOOLCHAIN-14), compiled in so no
// environment can relax them. They were set from the first measured run
// (docs/stories/harness-DSH-M1/evidence/DH-S-7.md) with headroom for slower disks
// and the fsync cost of CI runners.
const (
	budgetOpenP95        = 250 * time.Millisecond
	budgetAppendP95      = 10 * time.Millisecond
	budgetAppendFlushP95 = 50 * time.Millisecond
)

const budgetAppends = 1000

func p(d []time.Duration, q float64) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*q)]
}

// TestBudgetStoreAppend measures the pinned driver (fact F-2): opening a new
// dsh.db (migrations included) and reopening it, and 1,000 single-event appends
// each followed by a flush, the append and the flush also timed apart.
func TestBudgetStoreAppend(t *testing.T) {
	const opens = 20
	var open, reopen []time.Duration
	for range opens {
		path := dbPath(t)
		start := time.Now()
		s, err := sqlite.Open(bg, path)
		open = append(open, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(bg); err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		s, err = sqlite.Open(bg, path)
		reopen = append(reopen, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(bg); err != nil {
			t.Fatal(err)
		}
	}

	s := open1(t)
	w, err := s.Create(bg, store.Header{ID: "budget"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recs := make([]store.Record, budgetAppends)
	for i := range recs {
		recs[i] = call(t, store.Seq(i))
	}
	appendOnly := make([]time.Duration, 0, budgetAppends)
	flushOnly := make([]time.Duration, 0, budgetAppends)
	both := make([]time.Duration, 0, budgetAppends)
	for i, rec := range recs {
		start := time.Now()
		if err := w.Append(bg, store.Batch{First: store.Seq(i), Records: []store.Record{rec}}); err != nil {
			t.Fatal(err)
		}
		mid := time.Now()
		if err := w.Flush(bg); err != nil {
			t.Fatal(err)
		}
		end := time.Now()
		appendOnly = append(appendOnly, mid.Sub(start))
		flushOnly = append(flushOnly, end.Sub(mid))
		both = append(both, end.Sub(start))
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if sum, err := s.Stat(bg, "budget"); err != nil || sum.NextSeq != budgetAppends || sum.Durable != budgetAppends {
		t.Fatalf("Stat = %+v, %v", sum, err)
	}

	// The figures for the evidence record.
	for _, m := range []struct {
		name string
		d    []time.Duration
	}{
		{"open (new, migrate)", open},
		{"open (existing)", reopen},
		{"append", appendOnly},
		{"flush", flushOnly},
		{"append+flush", both},
	} {
		t.Logf("F-2 %-20s n=%4d p50=%v p95=%v p99=%v max=%v", m.name, len(m.d), p(m.d, .5), p(m.d, .95), p(m.d, .99), p(m.d, 1))
	}
	// The test gate runs this under -race with every other package, to catch a
	// race in it; the budget gate runs it alone and uninstrumented, which is
	// where the budgets measure the real code and are enforced (QG-7).
	if raceEnabled {
		t.Log("F-2 budgets not asserted: race-instrumented build")
		return
	}
	for _, b := range []struct {
		name   string
		got    time.Duration
		budget time.Duration
	}{
		{"open p95", p(open, .95), budgetOpenP95},
		{"append p95", p(appendOnly, .95), budgetAppendP95},
		{"append+flush p95", p(both, .95), budgetAppendFlushP95},
	} {
		if b.got > b.budget {
			t.Errorf("%s = %v, over its budget of %v", b.name, b.got, b.budget)
		}
	}
}

func open1(t *testing.T) *sqlite.Store { return open(t, dbPath(t)) }
