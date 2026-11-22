package driver_test

import (
	"math"
	"slices"
	"testing"
	"time"
)

// percentile is the nearest-rank p-th percentile (0 < p <= 100) of samples:
// the smallest sample at or above which p percent of them fall. It does not
// change samples.
func percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := slices.Clone(samples)
	slices.Sort(s)
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	return s[min(max(rank, 1), len(s))-1]
}

// overBudget reports whether the p99 latency of samples is over budget, the
// gate BenchmarkContinuation applies to DH-T-AGENT-28.
func overBudget(samples []time.Duration, budget time.Duration) (time.Duration, bool) {
	p99 := percentile(samples, 99)
	return p99, p99 > budget
}

func TestPercentileIsNearestRank(t *testing.T) {
	ms := time.Millisecond
	hundred := make([]time.Duration, 100)
	for i := range hundred {
		hundred[i] = time.Duration(100-i) * ms
	}
	for _, c := range []struct {
		name    string
		samples []time.Duration
		p       float64
		want    time.Duration
	}{
		{"empty", nil, 99, 0},
		{"one", []time.Duration{7 * ms}, 99, 7 * ms},
		{"three p99 is the max", []time.Duration{200 * ms, 20 * ms, 25 * ms}, 99, 200 * ms},
		{"three p50", []time.Duration{200 * ms, 20 * ms, 25 * ms}, 50, 25 * ms},
		{"hundred p99", hundred, 99, 99 * ms},
		{"hundred p100", hundred, 100, 100 * ms},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := percentile(c.samples, c.p); got != c.want {
				t.Fatalf("percentile(%v) = %v, want %v", c.p, got, c.want)
			}
		})
	}
	if hundred[0] != 100*ms {
		t.Fatal("percentile reordered its samples")
	}
}

// A mostly fast sample with one slow continuation passes a median gate but
// must fail the p99 gate.
func TestP99GateCatchesASlowTail(t *testing.T) {
	ms := time.Millisecond
	mixed := []time.Duration{20 * ms, 25 * ms, 200 * ms}
	if p99, over := overBudget(mixed, 100*ms); !over || p99 != 200*ms {
		t.Fatalf("p99 %v over=%v, want 200ms over the budget", p99, over)
	}
	fast := []time.Duration{20 * ms, 25 * ms, 99 * ms}
	if p99, over := overBudget(fast, 100*ms); over {
		t.Fatalf("p99 %v is inside the budget, but the gate failed", p99)
	}
}
