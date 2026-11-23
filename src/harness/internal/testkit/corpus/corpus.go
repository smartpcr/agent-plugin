// Package corpus is the heavy-tailed session corpus of the budget benchmarks
// (DH-ADR-TOOLCHAIN-14; DH-T-STORE-18, DH-T-VIEW-25, DH-T-AGENT-28). It holds the
// 22 upper-quantile anchors of the upstream decision
// UP-DEC-implemented.testing.2026-09-28-session-corpus-performance (its
// benchmarks/session-corpus/corpus-shape.ts): the events, logical bytes and
// turns of 1,650 real sessions measured on 2026-09-28. A corpus of uniform small
// sessions would understate the work, because real corpora are heavy-tailed.
//
// At turns a rank of an n-session corpus into a shape: the first anchor at or
// above the quantile (rank+1)/n, so every quantile of the synthetic corpus is at
// least the measured one, and the dimensions share the rank (a long session is
// long in every dimension). Log turns a shape into a deterministic, valid log;
// the corpussqlite package seeds it, with checkpoints, into a SQLite home.
//
// Only tests and the kit's own packages import it (archtest, DH-T-UTIL-11).
package corpus

import "fmt"

// AnchorCount is the number of measured anchors, and so of distinct shapes.
const AnchorCount = 22

// The measured anchors, index by index (corpus-shape.ts QUANTILES and MEASURED).
var (
	quantiles = [AnchorCount]float64{10, 20, 30, 40, 50, 60, 70, 75, 80, 85, 90, 92, 94, 96, 97, 98, 99, 99.3, 99.6, 99.8, 99.9, 100}
	// events are logical events, compact delta rows expanded to their count.
	events = [AnchorCount]int{22, 22, 27, 32, 53, 63, 84, 154, 251, 387, 709, 975, 1398, 2398, 2652, 3492, 7229, 11620, 21995, 26580, 60699, 84467}
	// logicalBytes are the decompressed JSONL bytes, the header line included.
	logicalBytes = [AnchorCount]int{
		37_233, 46_642, 54_432, 67_432, 86_195, 95_561, 184_414, 572_320, 882_968, 1_250_060, 1_849_503,
		2_341_019, 2_849_779, 3_902_295, 4_874_426, 6_691_370, 8_479_554, 10_570_582, 13_024_700, 17_646_397,
		21_225_976, 48_139_877,
	}
	// turns are the started turns.
	turns = [AnchorCount]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 3, 4, 5, 7, 9, 11, 18, 20, 23, 39, 118, 2181}
)

// Shape is the minimum length of one synthetic session.
type Shape struct {
	// Anchor is the index of the measured anchor; -1 for a shape no anchor
	// measured (Long).
	Anchor int
	// Quantile is the anchor's upper quantile, in percent.
	Quantile float64
	// Events, Bytes and Turns are the records, payload bytes and completed turns
	// the session holds at least.
	Events, Bytes, Turns int
}

// Anchor is the shape of measured anchor i (0 <= i < AnchorCount).
func Anchor(i int) (Shape, error) {
	if i < 0 || i >= AnchorCount {
		return Shape{}, fmt.Errorf("corpus: anchor %d does not exist", i)
	}
	return Shape{Anchor: i, Quantile: quantiles[i], Events: events[i], Bytes: logicalBytes[i], Turns: turns[i]}, nil
}

// At is the shape of rank (0-based, ascending length) in a corpus of count
// sessions: the first anchor at or above the quantile (rank+1)/count.
func At(rank, count int) (Shape, error) {
	if rank < 0 || rank >= count {
		return Shape{}, fmt.Errorf("corpus: rank %d is outside a %d-session corpus", rank, count)
	}
	q := 100 * float64(rank+1) / float64(count)
	i := 0
	for quantiles[i] < q {
		i++
	}
	return Anchor(i)
}

// Corpus is the shapes of a count-session corpus, ascending by rank. Every
// anchor appears once count is at least 1,000.
func Corpus(count int) ([]Shape, error) {
	if count <= 0 {
		return nil, fmt.Errorf("corpus: a corpus of %d sessions", count)
	}
	out := make([]Shape, count)
	for r := range out {
		out[r], _ = At(r, count)
	}
	return out, nil
}

// Rung is one named size of the heavy-tailed ladder.
type Rung struct {
	Name string
	Shape
}

// The anchors of the ladder's rungs.
const (
	anchorP50 = 4
	anchorP90 = 10
	anchorP99 = 16
	anchorMax = AnchorCount - 1
)

// Ladder is the heavy-tailed ladder of single-session benchmarks: the median,
// p90, p99 and longest measured session (DH-T-AGENT-28 continues each).
func Ladder() []Rung {
	out := make([]Rung, 0, 4)
	for _, r := range []struct {
		name   string
		anchor int
	}{{"p50", anchorP50}, {"p90", anchorP90}, {"p99", anchorP99}, {"max", anchorMax}} {
		sh, _ := Anchor(r.anchor)
		out = append(out, Rung{Name: r.name, Shape: sh})
	}
	return out
}

// LongEvents is the bound the long session of DH-T-STORE-18 and DH-T-VIEW-25
// exceeds: "one session of more than 100,000 events".
const LongEvents = 100_000

// Long is the longest measured session scaled up past LongEvents: its bytes and
// turns grow in the same ratio, so the long session has the longest one's mix.
func Long() Shape {
	sh, _ := Anchor(anchorMax)
	n := int64(LongEvents + 1)
	scale := func(v int) int { return int((int64(v)*n + int64(sh.Events) - 1) / int64(sh.Events)) }
	return Shape{Anchor: -1, Quantile: 100, Events: int(n), Bytes: scale(sh.Bytes), Turns: scale(sh.Turns)}
}
