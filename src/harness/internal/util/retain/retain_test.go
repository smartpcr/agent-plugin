package retain_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/retain"
)

// write feeds s to r in chunks of n bytes, so cuts fall inside runes and chunks.
func write(r *retain.TextRetainer, s string, n int) {
	for len(s) > 0 {
		k := min(n, len(s))
		if c, err := r.WriteString(s[:k]); c != k || err != nil {
			panic(fmt.Sprintf("Write = %d, %v", c, err))
		}
		s = s[k:]
	}
}

// Head, tail and head-and-tail windows over multibyte text never split a rune,
// whatever the chunking; the omitted count is exactly the bytes not returned;
// the head is a prefix and the tail a suffix of the input.
//
//dsh:test DH-T-UTIL-9
func TestTextWindowsNeverSplitARune(t *testing.T) {
	inputs := []string{
		"",
		"ascii only, longer than every budget here",
		"é",                                 // 2 bytes
		strings.Repeat("日本語", 9),            // 3-byte runes
		strings.Repeat("a😀b", 7),            // 4-byte runes between ASCII
		"x" + strings.Repeat("é", 20) + "y", // a cut lands on every offset
	}
	for _, in := range inputs {
		for _, mode := range []retain.Mode{retain.Head, retain.Tail, retain.HeadTail} {
			for budget := 0; budget <= 13; budget++ {
				for _, chunk := range []int{1, 2, 5, 1 << 10} {
					r := retain.Text(mode, budget)
					write(r, in, chunk)
					res := r.Result()
					name := fmt.Sprintf("%s budget %d chunk %d over %q", mode, budget, chunk, in)
					if !utf8.ValidString(res.Head) || !utf8.ValidString(res.Tail) {
						t.Fatalf("%s: split a rune: head %q tail %q", name, res.Head, res.Tail)
					}
					if !strings.HasPrefix(in, res.Head) || !strings.HasSuffix(in, res.Tail) {
						t.Fatalf("%s: head %q / tail %q are not a prefix / suffix", name, res.Head, res.Tail)
					}
					if got := int64(len(res.Head) + len(res.Tail)); res.Omitted != res.Total-got || res.Total != int64(len(in)) || r.Total() != res.Total {
						t.Fatalf("%s: omitted %d, total %d, kept %d", name, res.Omitted, res.Total, got)
					}
					if len(res.Head)+len(res.Tail) > budget {
						t.Fatalf("%s: kept %d bytes over the budget", name, len(res.Head)+len(res.Tail))
					}
					if res.Truncated() != (len(in) > budget) || (!res.Truncated() && res.String() != in) {
						t.Fatalf("%s: truncated %v, text %q", name, res.Truncated(), res.String())
					}
					// A rune is dropped only when the cut split it: at most UTFMax-1
					// bytes per cut beyond the budget's own cut.
					over := int64(len(in) - budget)
					if over > 0 && res.Omitted > over+2*(utf8.UTFMax-1) {
						t.Fatalf("%s: omitted %d bytes for a budget %d bytes short", name, res.Omitted, over)
					}
				}
			}
		}
	}
}

// The exact windows of each mode, and the notice the omitted count gives.
//
//dsh:test DH-T-UTIL-9
func TestTextWindowsAndNotice(t *testing.T) {
	for _, c := range []struct {
		mode       retain.Mode
		budget     int
		in         string
		head, tail string
		omitted    int64
	}{
		{retain.Head, 4, "abcdefgh", "abcd", "", 4},
		{retain.Tail, 4, "abcdefgh", "", "efgh", 4},
		{retain.HeadTail, 5, "abcdefgh", "ab", "fgh", 3},
		{retain.HeadTail, 8, "abcdefgh", "abcdefgh", "", 0},
		{retain.Head, 3, "aé€", "aé", "", 3},                 // the euro sign is cut whole
		{retain.Tail, 4, "€é", "", "é", 3},                   // the tail starts after the cut euro
		{retain.HeadTail, 4, "é€é", "é", "é", 3},             // both sides cut, the middle rune is gone
		{retain.Head, -1, "abc", "", "", 3},                  // a negative budget keeps nothing
		{retain.Mode(0), 10, "abc", "", "", 3},               // an unknown mode keeps nothing
		{retain.Tail, 2, "\x80\x80\x80\x80abc", "", "bc", 5}, // plain ASCII tail
	} {
		r := retain.Text(c.mode, c.budget)
		write(r, c.in, 1)
		res := r.Result()
		if res.Head != c.head || res.Tail != c.tail || res.Omitted != c.omitted {
			t.Errorf("%s %d over %q = head %q tail %q omitted %d; want %q %q %d", c.mode, c.budget, c.in, res.Head, res.Tail, res.Omitted, c.head, c.tail, c.omitted)
		}
		want := ""
		if c.omitted > 0 {
			want = fmt.Sprintf("Omitted %d bytes.", c.omitted)
		}
		if res.Notice() != want {
			t.Errorf("Notice = %q; want %q", res.Notice(), want)
		}
	}
	if got := retain.Notice(0, "items"); got != "" {
		t.Errorf("Notice(0) = %q; want nothing", got)
	}
	if got := retain.Notice(3, "lines"); got != "Omitted 3 lines." {
		t.Errorf("Notice(3, lines) = %q", got)
	}
}

// Invalid bytes already in the input are not a cut: a tail of four or more
// continuation bytes is returned as it was.
//
//dsh:test DH-T-UTIL-9
func TestTextLeavesInvalidInputAlone(t *testing.T) {
	r := retain.Text(retain.Tail, 5)
	write(r, "abc\x80\x80\x80\x80\x80", 3)
	if res := r.Result(); res.Tail != "\x80\x80\x80\x80\x80" || res.Omitted != 3 {
		t.Fatalf("tail %q omitted %d; want the five bytes and 3", res.Tail, res.Omitted)
	}
	r = retain.Text(retain.Head, 2)
	write(r, "\xff\xffabc", 10)
	if res := r.Result(); res.Head != "\xff\xff" || res.Omitted != 3 {
		t.Fatalf("head %q omitted %d; want the two invalid bytes and 3", res.Head, res.Omitted)
	}
}

// A retainer holds at most its budget however much passes through it.
//
//dsh:test DH-T-UTIL-9
func TestTextHoldsOnlyItsBudget(t *testing.T) {
	r := retain.Text(retain.HeadTail, 64)
	chunk := strings.Repeat("0123456789", 100)
	for range 1000 {
		write(r, chunk, len(chunk))
	}
	res := r.Result()
	if res.Total != 1_000_000 || len(res.Head) != 32 || len(res.Tail) != 32 || res.Omitted != 1_000_000-64 {
		t.Fatalf("head %d tail %d omitted %d total %d", len(res.Head), len(res.Tail), res.Omitted, res.Total)
	}
	if res.Head != chunk[:32] || res.Tail != chunk[len(chunk)-32:] {
		t.Fatalf("head %q tail %q", res.Head, res.Tail)
	}
}

// Items keep their order in every mode; the omitted count is exact; Push says
// whether the item is held now.
//
//dsh:test DH-T-UTIL-9
func TestItemsKeepOrder(t *testing.T) {
	seq := func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	for _, c := range []struct {
		mode       retain.Mode
		n, pushed  int
		head, tail []int
	}{
		{retain.Head, 3, 10, []int{0, 1, 2}, nil},
		{retain.Tail, 3, 10, nil, []int{7, 8, 9}},
		{retain.HeadTail, 5, 10, []int{0, 1}, []int{7, 8, 9}},
		{retain.HeadTail, 5, 4, []int{0, 1, 2, 3}, nil},
		{retain.Tail, 3, 2, []int{0, 1}, nil},
		{retain.Head, 0, 3, nil, nil},
	} {
		r := retain.Items[int](c.mode, c.n)
		for _, v := range seq(c.pushed) {
			held := r.Push(v)
			if want := c.mode != retain.Head || v < c.n; held != want {
				t.Errorf("%s %d: Push(%d) = %v; want %v", c.mode, c.n, v, held, want)
			}
		}
		res := r.Result()
		if !slices.Equal(res.Head, c.head) || !slices.Equal(res.Tail, c.tail) {
			t.Errorf("%s %d of %d = %v %v; want %v %v", c.mode, c.n, c.pushed, res.Head, res.Tail, c.head, c.tail)
		}
		kept := len(c.head) + len(c.tail)
		if res.Seen != int64(c.pushed) || res.Omitted != int64(c.pushed-kept) || res.Truncated() != (c.pushed > kept) {
			t.Errorf("%s %d of %d: seen %d omitted %d", c.mode, c.n, c.pushed, res.Seen, res.Omitted)
		}
		if got := res.Items(); !slices.Equal(got, append(append([]int(nil), c.head...), c.tail...)) {
			t.Errorf("Items = %v", got)
		}
		want := ""
		if res.Omitted > 0 {
			want = fmt.Sprintf("Omitted %d items.", res.Omitted)
		}
		if res.Notice() != want {
			t.Errorf("Notice = %q; want %q", res.Notice(), want)
		}
	}
}

// Modes print their names.
//
//dsh:test DH-T-UTIL-9
func TestModeNames(t *testing.T) {
	for m, want := range map[retain.Mode]string{retain.Head: "head", retain.Tail: "tail", retain.HeadTail: "head-tail", 9: "mode(9)"} {
		if m.String() != want {
			t.Errorf("%d = %q; want %q", int(m), m.String(), want)
		}
	}
}
