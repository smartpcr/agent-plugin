package corpus

import (
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"
	"time"
)

// MinSamples is the fewest timed operations a budget's p95 is taken over: a
// benchmark run with a smaller b.N (Go's first run is b.N = 1) times more,
// outside b.N's ns/op and allocations, so every run judges the same p95.
const MinSamples = 20

// MemoryLimit is the soft memory limit of the memory-limited rerun (upstream's
// 2026-10-07 memory-limited benchmarks ran under 4 GiB and 8 GiB control
// groups; Go's collector reacts to GOMEMLIMIT, and the 4 GiB limit binds first).
const MemoryLimit = 4 << 30

// Timer times the operations of one budget benchmark run.
type Timer struct {
	b       *testing.B
	samples []time.Duration
	heap    uint64
}

// Time runs op once and records its wall time. The first b.N operations of a
// run also count toward the benchmark's ns/op and allocations; the heap in use
// right after op is recorded for the heap-after-B metric.
func (t *Timer) Time(op func()) {
	counted := len(t.samples) < t.b.N
	if counted {
		t.b.StartTimer()
	}
	start := time.Now()
	op()
	d := time.Since(start)
	if counted {
		t.b.StopTimer()
	}
	t.samples = append(t.samples, d)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.heap = max(t.heap, ms.HeapAlloc)
}

// Budget runs one budgeted operation as the sub-benchmark name, and again as
// name/gomemlimit-4GiB with the soft memory limit set to MemoryLimit (the
// memory-limited rerun, restored afterwards). body prepares one operation, times
// it with exactly one t.Time call and cleans up after it; it is called until the
// run has max(b.N, MinSamples) samples. Each run reports the p50 and p95
// (nearest rank) latency and the largest heap after an operation, and fails when
// the p95 is over budget.
func Budget(b *testing.B, name string, budget time.Duration, body func(b *testing.B, t *Timer)) {
	b.Helper()
	for _, limit := range []struct {
		suffix string
		bytes  int64
	}{{"", -1}, {"/gomemlimit-4GiB", MemoryLimit}} {
		b.Run(name+limit.suffix, func(b *testing.B) {
			if limit.bytes > 0 {
				prev := debug.SetMemoryLimit(limit.bytes)
				defer debug.SetMemoryLimit(prev)
			}
			b.ReportAllocs()
			b.StopTimer()
			b.ResetTimer()
			t := &Timer{b: b}
			for len(t.samples) < max(b.N, MinSamples) {
				n := len(t.samples)
				body(b, t)
				if len(t.samples) != n+1 {
					b.Fatalf("a %s operation timed %d operations, want 1", name, len(t.samples)-n)
				}
			}
			p95, over := OverBudget(t.samples, budget)
			b.ReportMetric(float64(Percentile(t.samples, 50).Nanoseconds()), "p50-ns")
			b.ReportMetric(float64(p95.Nanoseconds()), "p95-ns")
			b.ReportMetric(float64(t.heap), "heap-after-B")
			b.Logf("%s: n=%d p50=%v p95=%v max=%v heap-after=%d B (budget %v)", name+limit.suffix, len(t.samples),
				Percentile(t.samples, 50), p95, Percentile(t.samples, 100), t.heap, budget)
			if over {
				b.Errorf("%s p95 = %v over %d operations, over its budget of %v", name+limit.suffix, p95, len(t.samples), budget)
			}
		})
	}
}

// Percentile is the nearest-rank p-th percentile (0 < p <= 100) of samples: the
// smallest sample at or above which p percent of them fall; 0 for none. It
// does not change samples.
func Percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := slices.Clone(samples)
	slices.Sort(s)
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	return s[min(max(rank, 1), len(s))-1]
}

// OverBudget is the p95 of samples and whether it is over budget.
func OverBudget(samples []time.Duration, budget time.Duration) (time.Duration, bool) {
	p95 := Percentile(samples, 95)
	return p95, p95 > budget
}
