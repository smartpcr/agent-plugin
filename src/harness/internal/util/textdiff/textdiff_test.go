package textdiff

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestLinesHunks(t *testing.T) {
	cases := []struct {
		name, a, b string
		context    int
		want       string // Unified output
		added, del int
	}{
		{"equal", "a\nb\n", "a\nb\n", 3, "", 0, 0},
		{"empty to text", "", "x\ny\n", 3, "--- a\n+++ b\n@@ -0,0 +1,2 @@\n+x\n+y\n", 2, 0},
		{"text to empty", "x\n", "", 3, "--- a\n+++ b\n@@ -1 +0,0 @@\n-x\n", 0, 1},
		{"change middle", "1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n", 1,
			"--- a\n+++ b\n@@ -4,3 +4,3 @@\n 4\n-5\n+X\n 6\n", 1, 1},
		{"two hunks", "1\n2\n3\n4\n5\n6\n7\n8\n9\n", "X\n2\n3\n4\n5\n6\n7\n8\nY\n", 1,
			"--- a\n+++ b\n@@ -1,2 +1,2 @@\n-1\n+X\n 2\n@@ -8,2 +8,2 @@\n 8\n-9\n+Y\n", 2, 2},
		{"merged hunks", "1\n2\n3\n4\n", "X\n2\n3\nY\n", 1,
			"--- a\n+++ b\n@@ -1,4 +1,4 @@\n-1\n+X\n 2\n 3\n-4\n+Y\n", 2, 2},
		{"final newline added", "a\nb", "a\nb\n", 3,
			"--- a\n+++ b\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n", 1, 1},
		{"crlf is visible", "a\r\nb\n", "a\nb\n", 0, "--- a\n+++ b\n@@ -1 +1 @@\n-a\r\n+a\n", 1, 1},
		{"insert after line", "a\nb\n", "a\nx\nb\n", 0, "--- a\n+++ b\n@@ -1,0 +2 @@\n+x\n", 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Lines(c.a, c.b, c.context, Limits{})
			if got := r.Unified("a", "b"); got != c.want {
				t.Errorf("unified:\n%q\nwant\n%q", got, c.want)
			}
			if r.Added != c.added || r.Deleted != c.del || r.Changed() != (c.added+c.del > 0) {
				t.Errorf("counts +%d -%d, want +%d -%d", r.Added, r.Deleted, c.added, c.del)
			}
			assertApply(t, r, c.a, c.b)
		})
	}
}

func assertApply(t *testing.T, r Result, a, b string) {
	t.Helper()
	got, err := r.Apply(a)
	if err != nil || got != b {
		t.Fatalf("Apply = %q, %v; want %q", got, err, b)
	}
}

func TestBeforeAfter(t *testing.T) {
	r := Lines("a\nb\nc", "a\nB\nc", 1, Limits{})
	if len(r.Hunks) != 1 {
		t.Fatalf("hunks %+v", r.Hunks)
	}
	before, after := r.Hunks[0].BeforeAfter()
	if before != "a\nb\nc" || after != "a\nB\nc" {
		t.Fatalf("before %q after %q", before, after)
	}
}

func TestLimits(t *testing.T) {
	a := strings.Repeat("line\n", 20)
	b := strings.Repeat("line\n", 10) + "x\n" + strings.Repeat("line\n", 9)

	r := Lines(a, b, 3, Limits{MaxLines: 5})
	if !r.Coarse || len(r.Hunks) != 1 || r.Added != 20 || r.Deleted != 20 {
		t.Fatalf("line bound: %+v", r)
	}
	assertApply(t, r, a, b)
	if r := Lines(a, b, 3, Limits{MaxBytes: 10}); !r.Coarse {
		t.Fatal("byte bound ignored")
	}
	if r := Lines("", "", 3, Limits{MaxBytes: -1, MaxLines: 1}); r.Changed() || len(r.Hunks) != 0 {
		t.Fatalf("empty coarse: %+v", r)
	}

	r = Lines(a, b, 3, Limits{HunkMaxBytes: 10})
	if !r.CountsOnly || r.Hunks != nil || r.Added != 1 || r.Deleted != 1 {
		t.Fatalf("hunk bound: %+v", r)
	}
	if _, err := r.Apply(a); !errors.Is(err, ErrNoHunks) {
		t.Fatalf("Apply of counts: %v", err)
	}
	if r := Diff(a, b); r.Coarse || r.CountsOnly || len(r.Hunks) != 1 {
		t.Fatalf("defaults: %+v", r)
	}
	if r := Lines(a, b, -4, Limits{}); r.Hunks[0].OldLines != 1 {
		t.Fatalf("negative context: %+v", r.Hunks[0])
	}
}

func TestApplyMismatch(t *testing.T) {
	r := Diff("a\nb\nc\n", "a\nB\nc\n")
	if _, err := r.Apply("a\nz\nc\n"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("changed text: %v", err)
	}
	if _, err := r.Apply(""); !errors.Is(err, ErrMismatch) {
		t.Fatalf("empty text: %v", err)
	}
	bad := Result{Hunks: []Hunk{{OldStart: 9, OldLines: 1}}}
	if _, err := bad.Apply("a\n"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("hunk past the end: %v", err)
	}
}

// TestApplyInvertsLines checks Lines(a, b).Apply(a) == b over random texts built
// from a small alphabet of lines, so equal lines and moves are common.
func TestApplyInvertsLines(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a\n", "b\n", "c\n", "\n", "a\r\n", "d"}
	gen := func() string {
		var s strings.Builder
		for range rng.IntN(30) {
			s.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		return s.String()
	}
	for i := range 2000 {
		a, b := gen(), gen()
		ctx := rng.IntN(4)
		r := Lines(a, b, ctx, Limits{})
		got, err := r.Apply(a)
		if err != nil || got != b {
			t.Fatalf("case %d context %d: Apply(%q) = %q, %v; want %q", i, ctx, a, got, err, b)
		}
		if (a == b) == r.Changed() {
			t.Fatalf("case %d: Changed() = %v for %q vs %q", i, r.Changed(), a, b)
		}
	}
}

// TestEqualAboveLimits: identical inputs are unchanged even past MaxLines or
// MaxBytes, where unequal inputs get a coarse whole-text hunk.
func TestEqualAboveLimits(t *testing.T) {
	s := "one\ntwo\nthree\n"
	for _, lim := range []Limits{{MaxLines: 1}, {MaxBytes: 4}} {
		r := Lines(s, s, DefaultContext, lim)
		if r.Changed() || r.Coarse || len(r.Hunks) != 0 || r.Added+r.Deleted != 0 {
			t.Errorf("%+v: equal inputs gave %+v", lim, r)
		}
		if r := Lines(s, s+"four\n", DefaultContext, lim); !r.Changed() || !r.Coarse {
			t.Errorf("%+v: unequal inputs gave %+v", lim, r)
		}
	}
}
