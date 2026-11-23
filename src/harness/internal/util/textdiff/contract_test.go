package textdiff

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// TestGoldenCases: the hunk ranges and before/after pairs of the basic edits match
// the committed golden testdata/hunks.golden.
//
//dsh:test DH-T-DIFF-1
func TestGoldenCases(t *testing.T) {
	base := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n" +
		"eleven\ntwelve\nthirteen\nfourteen\nfifteen\nsixteen\n"
	cases := []struct{ name, before, after string }{
		{"insertion", base, strings.Replace(base, "five\n", "five\nfive and a half\n", 1)},
		{"deletion", base, strings.Replace(base, "five\n", "", 1)},
		{"replacement", base, strings.Replace(base, "five\n", "FIVE\n", 1)},
		{"two separate changes", base, strings.Replace(strings.Replace(base, "two\n", "TWO\n", 1), "fifteen\n", "FIFTEEN\n", 1)},
		{"change at the first line", base, strings.Replace(base, "one\n", "ONE\n", 1)},
		{"change at the last line", base, strings.Replace(base, "sixteen\n", "SIXTEEN\n", 1)},
		{"identical texts", base, base},
	}
	var out strings.Builder
	for _, c := range cases {
		r := Diff(c.before, c.after)
		fmt.Fprintf(&out, "== %s: +%d -%d, %d hunks\n", c.name, r.Added, r.Deleted, len(r.Hunks))
		for _, h := range r.Hunks {
			before, after := h.BeforeAfter()
			fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\nbefore %q\nafter  %q\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines, before, after)
		}
		assertApply(t, r, c.before, c.after)
	}
	golden.Assert(t, "hunks", out.String())
}

// TestLimitEdges: inputs just under each limit give a fine result, just over it a
// coarse one (one whole-text hunk) or, for the hunk bound, counts only.
//
//dsh:test DH-T-DIFF-2, DH-T-FS-6
func TestLimitEdges(t *testing.T) {
	a := "a\nb\nc\nd\n" // 8 bytes, 4 lines
	b := "a\nB\nc\nd\n"
	fine := func(name string, r Result) {
		t.Helper()
		if r.Coarse || r.CountsOnly || r.Added != 1 || r.Deleted != 1 || len(r.Hunks) != 1 {
			t.Errorf("%s: %+v, want a fine one-line change", name, r)
		}
	}
	whole := func(name string, r Result) {
		t.Helper()
		if !r.Coarse || r.CountsOnly || len(r.Hunks) != 1 || r.Added != 4 || r.Deleted != 4 {
			t.Errorf("%s: %+v, want one whole-replacement hunk", name, r)
		}
	}
	fine("bytes at the bound", Lines(a, b, 1, Limits{MaxBytes: len(a) + len(b)}))
	whole("bytes one over", Lines(a, b, 1, Limits{MaxBytes: len(a) + len(b) - 1}))
	fine("lines at the bound", Lines(a, b, 1, Limits{MaxLines: 4}))
	whole("lines one over", Lines(a, b, 1, Limits{MaxLines: 3}))

	r := Lines(a, b, 1, Limits{})
	size := r.size()
	fine("hunks at the bound", Lines(a, b, 1, Limits{HunkMaxBytes: size}))
	if r := Lines(a, b, 1, Limits{HunkMaxBytes: size - 1}); !r.CountsOnly || r.Hunks != nil || r.Added != 1 || r.Deleted != 1 {
		t.Errorf("hunks one over: %+v, want counts only", r)
	}
}

// TestCoarseAboveHunkLimit: a coarse result whose whole-replacement hunk is larger
// than HunkMaxBytes keeps its counts and no lines; under the bound it is one hunk
// that replaces the whole text.
//
//dsh:test DH-T-FS-6
func TestCoarseAboveHunkLimit(t *testing.T) {
	a := strings.Repeat("old line\n", 10)
	b := strings.Repeat("new line\n", 12)
	r := Lines(a, b, 3, Limits{MaxLines: 5, HunkMaxBytes: 50})
	if !r.Coarse || !r.CountsOnly || r.Hunks != nil || r.Added != 12 || r.Deleted != 10 {
		t.Fatalf("coarse above the hunk bound: %+v", r)
	}
	r = Lines(a, b, 3, Limits{MaxLines: 5, HunkMaxBytes: 10 << 10})
	if !r.Coarse || r.CountsOnly || len(r.Hunks) != 1 || r.Hunks[0].OldLines != 10 || r.Hunks[0].NewLines != 12 {
		t.Fatalf("coarse under the hunk bound: %+v", r)
	}
	before, after := r.Hunks[0].BeforeAfter()
	if before != a || after != b {
		t.Fatalf("the coarse hunk does not cover the whole texts: %q %q", before, after)
	}
	assertApply(t, r, a, b)
}

// TestParallelIdentical: the same inputs diffed 100 times in parallel give identical
// results.
//
//dsh:test DH-T-DIFF-2
func TestParallelIdentical(t *testing.T) {
	a := strings.Repeat("x\ny\nz\n", 40)
	b := strings.Replace(strings.Replace(a, "y\n", "Y\n", 7), "z\n", "", 3)
	want := Diff(a, b)
	got := make([]Result, 100)
	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() { got[i] = Diff(a, b) })
	}
	wg.Wait()
	for i, r := range got {
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("run %d differs:\n%+v\nwant\n%+v", i, r, want)
		}
	}
}

// TestFinalNewlineAndEmpty: pairs that differ only in the final newline, and an
// empty text on either side, diff and apply exactly.
//
//dsh:test DH-T-DIFF-3
func TestFinalNewlineAndEmpty(t *testing.T) {
	for _, c := range []struct{ name, a, b string }{
		{"newline added", "a\nb", "a\nb\n"},
		{"newline removed", "a\nb\n", "a\nb"},
		{"one line, newline added", "a", "a\n"},
		{"empty before", "", "a\n"},
		{"empty after", "a\nb\n", ""},
		{"empty before, no newline", "", "a"},
		{"both empty", "", ""},
	} {
		r := Diff(c.a, c.b)
		if r.Changed() != (c.a != c.b) {
			t.Errorf("%s: Changed() = %v", c.name, r.Changed())
		}
		got, err := r.Apply(c.a)
		if err != nil || got != c.b {
			t.Errorf("%s: Apply = %q, %v; want %q", c.name, got, err, c.b)
		}
		if c.a != c.b && !strings.Contains(r.Unified("a", "b"), "@@") {
			t.Errorf("%s: no hunk rendered", c.name)
		}
	}
}

// FuzzLines: for any two texts, applying the diff's hunks to the first gives the
// second, whatever the context.
//
//dsh:test DH-T-DIFF-2
func FuzzLines(f *testing.F) {
	f.Add("a\nb\nc\n", "a\nB\nc\n", uint8(3))
	f.Add("", "x", uint8(0))
	f.Add("a\r\nb", "a\nb\n", uint8(1))
	f.Add("same\n", "same\n", uint8(2))
	f.Fuzz(func(t *testing.T, a, b string, context uint8) {
		r := Lines(a, b, int(context%8), Limits{})
		got, err := r.Apply(a)
		if err != nil || got != b {
			t.Fatalf("Apply(%q) = %q, %v; want %q", a, got, err, b)
		}
	})
}
