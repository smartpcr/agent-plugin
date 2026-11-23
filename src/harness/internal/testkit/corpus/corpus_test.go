package corpus_test

import (
	"runtime/debug"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
)

// The anchors are upstream's measured table (corpus-shape.ts): the p50, p90,
// p99 and max rows its note prints, ascending in every dimension.
func TestAnchorsAreTheMeasuredTable(t *testing.T) {
	for _, c := range []struct {
		anchor               int
		q                    float64
		events, bytes, turns int
	}{
		{4, 50, 53, 86_195, 1}, {10, 90, 709, 1_849_503, 3}, {16, 99, 7_229, 8_479_554, 18}, {21, 100, 84_467, 48_139_877, 2_181},
	} {
		sh, err := corpus.Anchor(c.anchor)
		if err != nil || sh.Quantile != c.q || sh.Events != c.events || sh.Bytes != c.bytes || sh.Turns != c.turns {
			t.Fatalf("Anchor(%d) = %+v, %v", c.anchor, sh, err)
		}
	}
	prev := corpus.Shape{}
	for i := range corpus.AnchorCount {
		sh, _ := corpus.Anchor(i)
		if sh.Quantile <= prev.Quantile || sh.Events < prev.Events || sh.Bytes < prev.Bytes || sh.Turns < prev.Turns {
			t.Fatalf("anchor %d %+v is below anchor %d %+v", i, sh, i-1, prev)
		}
		prev = sh
	}
	for _, i := range []int{-1, corpus.AnchorCount} {
		if _, err := corpus.Anchor(i); err == nil {
			t.Fatalf("Anchor(%d) did not fail", i)
		}
	}
}

// Rank r of n takes the first anchor at or above (r+1)/n, so a 1,000-session
// corpus holds every anchor and its quantiles are at least the measured ones.
func TestCorpusTakesTheUpperAnchor(t *testing.T) {
	shapes, err := corpus.Corpus(1000)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for r, sh := range shapes {
		seen[sh.Anchor] = true
		if q := 100 * float64(r+1) / 1000; sh.Quantile < q {
			t.Fatalf("rank %d: anchor quantile %v below %v", r, sh.Quantile, q)
		}
	}
	if len(seen) != corpus.AnchorCount {
		t.Fatalf("a 1,000-session corpus holds %d anchors, want %d", len(seen), corpus.AnchorCount)
	}
	if shapes[0].Anchor != 0 || shapes[499].Anchor != 4 || shapes[999].Anchor != corpus.AnchorCount-1 {
		t.Fatalf("ranks 0, 499, 999 take anchors %d, %d, %d", shapes[0].Anchor, shapes[499].Anchor, shapes[999].Anchor)
	}
	if _, err := corpus.Corpus(0); err == nil {
		t.Fatal("Corpus(0) did not fail")
	}
	if _, err := corpus.At(5, 5); err == nil {
		t.Fatal("At(5, 5) did not fail")
	}
}

func TestLadderAndLong(t *testing.T) {
	var names []string
	for _, r := range corpus.Ladder() {
		names = append(names, r.Name)
	}
	if got := len(names); got != 4 || names[0] != "p50" || names[3] != "max" {
		t.Fatalf("Ladder = %v", names)
	}
	if l := corpus.Ladder()[3]; l.Events != 84_467 {
		t.Fatalf("the max rung holds %d events", l.Events)
	}
	long := corpus.Long()
	if long.Events <= corpus.LongEvents || long.Anchor != -1 || long.Bytes < 56_000_000 || long.Turns < 2_582 {
		t.Fatalf("Long = %+v", long)
	}
}

// A log of each anchor's shape through p96 is valid (the core folds take every
// record), at least as long as its shape in every dimension, and the same on
// every call. The longer anchors take the same path (and the benchmarks seed
// them through the core folds); they are left out to keep the race-instrumented
// gate run short.
func TestLogMeetsItsShape(t *testing.T) {
	for i := range 14 {
		sh, _ := corpus.Anchor(i)
		recs, err := corpus.Log(sh, 1_000)
		if err != nil {
			t.Fatal(err)
		}
		turns := 0
		for j, r := range recs {
			if r.Seq != event.Seq(j) || r.Time != 1_000+int64(j) {
				t.Fatalf("anchor %d: record %d is seq %d at %d", i, j, r.Seq, r.Time)
			}
			if r.Type == event.TurnEndType.Name() {
				turns++
			}
		}
		if len(recs) < sh.Events || corpus.Bytes(recs) < sh.Bytes || turns != sh.Turns {
			t.Fatalf("anchor %d: %d records, %d bytes, %d turns for %+v", i, len(recs), corpus.Bytes(recs), turns, sh)
		}
		folds, err := surface.Replay(event.Default(), recs)
		if err != nil || folds.Len() != event.Seq(len(recs)) || folds.Open().IsOpen() {
			t.Fatalf("anchor %d: replay %v, %d folded", i, err, folds.Len())
		}
	}
	sh, _ := corpus.Anchor(7)
	a, _ := corpus.Log(sh, 5)
	b, _ := corpus.Log(sh, 5)
	if len(a) != len(b) || a[len(a)-1].Payload != b[len(b)-1].Payload || a[40].Payload != b[40].Payload {
		t.Fatal("two logs of one shape differ")
	}
	if _, err := corpus.Log(corpus.Shape{}, 0); err == nil {
		t.Fatal("a shape of no turns did not fail")
	}
	// A shape no tool step can pad is still a valid log of its turns.
	small, err := corpus.Log(corpus.Shape{Turns: 2, Events: 1, Bytes: 1 << 20}, 0)
	if err != nil || len(small) != 12 {
		t.Fatalf("Log of two bare turns = %d records, %v", len(small), err)
	}
}

func TestPercentileAndOverBudget(t *testing.T) {
	ms := time.Millisecond
	if corpus.Percentile(nil, 95) != 0 {
		t.Fatal("the percentile of no samples is not 0")
	}
	samples := make([]time.Duration, 20)
	for i := range samples {
		samples[i] = time.Duration(20-i) * ms
	}
	if got := corpus.Percentile(samples, 95); got != 19*ms {
		t.Fatalf("p95 of 1..20ms = %v", got)
	}
	if got := corpus.Percentile(samples, 100); got != 20*ms || samples[0] != 20*ms {
		t.Fatalf("p100 = %v, samples reordered: %v", got, samples[0])
	}
	if p, over := corpus.OverBudget(samples, 18*ms); !over || p != 19*ms {
		t.Fatalf("OverBudget(18ms) = %v, %v", p, over)
	}
	if _, over := corpus.OverBudget(samples, 19*ms); over {
		t.Fatal("a p95 equal to its budget is over it")
	}
}

// Budget times at least MinSamples operations in each of its two runs (the
// plain run and the memory-limited rerun) and restores the memory limit. The
// name must not start with TestBudget: the coverage gate skips ^TestBudget (the
// budget gate runs those alone), and this test is what covers Budget.
func TestBothBudgetRunsAreTimed(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	limits := map[int64]int{}
	res := testing.Benchmark(func(b *testing.B) {
		corpus.Budget(b, "op", time.Hour, func(b *testing.B, tm *corpus.Timer) {
			limits[debug.SetMemoryLimit(-1)]++
			tm.Time(func() {
				for start := time.Now(); time.Since(start) < time.Millisecond; {
				}
			})
		})
	})
	if res.N == 0 || limits[before] < corpus.MinSamples || limits[corpus.MemoryLimit] < corpus.MinSamples {
		t.Fatalf("Budget ran operations under the limits %v, result N=%d", limits, res.N)
	}
	if after := debug.SetMemoryLimit(-1); after != before {
		t.Fatalf("the memory limit is %d after Budget, %d before", after, before)
	}
}
