// Package retain is the one way the harness cuts output to a budget (DH-D-UTIL-5,
// upstream util.output-retention): a text retainer keeps a head, a tail or both of
// a byte stream, an item retainer keeps a head, a tail or both of a sequence, and
// each counts exactly what it dropped between them. Notice words the count the
// same way everywhere; the recovery advice ("read the saved file", "narrow the
// pattern") stays the caller's.
//
// A retainer holds at most its budget, however much is written to it: the head is
// a buffer that stops filling, the tail is a ring that keeps the newest part.
// Text is cut on rune boundaries only: a rune split by a cut is dropped whole and
// counted as omitted, so a result never carries a partial character the cut made
// (invalid bytes already in the input are left as they were).
//
// "Truncated" means only that a budget cut something. It never means the source
// was incomplete; that is the caller's to report.
//
// retain imports the standard library only.
package retain

import (
	"strconv"
	"unicode/utf8"
)

// Mode is which part a retainer keeps.
type Mode int

// The modes.
const (
	// Head keeps the first part.
	Head Mode = iota + 1
	// Tail keeps the last part.
	Tail
	// HeadTail keeps the first and the last part, each half of the budget (the
	// tail gets the odd unit), and drops the middle.
	HeadTail
)

// String names the mode.
func (m Mode) String() string {
	switch m {
	case Head:
		return "head"
	case Tail:
		return "tail"
	case HeadTail:
		return "head-tail"
	}
	return "mode(" + strconv.Itoa(int(m)) + ")"
}

// split divides budget between the head and the tail of mode. A budget below
// zero is zero; an unknown mode keeps nothing, so a mistake drops output rather
// than passing it unbounded.
func split(mode Mode, budget int) (head, tail int) {
	budget = max(budget, 0)
	switch mode {
	case Head:
		return budget, 0
	case Tail:
		return 0, budget
	case HeadTail:
		return budget / 2, budget - budget/2
	}
	return 0, 0
}

// Notice is the standard omission clause: "Omitted 3 items." for a positive
// count, "" for none. unit is the plural noun of what was counted: "bytes",
// "items", "lines".
func Notice(omitted int64, unit string) string {
	if omitted <= 0 {
		return ""
	}
	return "Omitted " + strconv.FormatInt(omitted, 10) + " " + unit + "."
}

// TextRetainer keeps a window of a byte stream within a byte budget. It is an
// io.Writer that never fails. It is not safe for concurrent use.
type TextRetainer struct {
	headCap, tailCap int
	head             []byte
	ring             []byte // the tail ring, tailCap long once allocated
	pos              int    // where the next tail byte goes in ring
	filled           int    // tail bytes held, at most tailCap
	total            int64
}

// Text returns a retainer that keeps the mode's part of what is written, within
// budget bytes.
func Text(mode Mode, budget int) *TextRetainer {
	h, t := split(mode, budget)
	return &TextRetainer{headCap: h, tailCap: t}
}

// Write keeps what fits and counts the rest; it always accepts all of p.
func (r *TextRetainer) Write(p []byte) (int, error) {
	n := len(p)
	r.total += int64(n)
	if room := r.headCap - len(r.head); room > 0 {
		take := min(room, len(p))
		r.head = append(r.head, p[:take]...)
		p = p[take:]
	}
	if r.tailCap == 0 || len(p) == 0 {
		return n, nil
	}
	if r.ring == nil {
		r.ring = make([]byte, r.tailCap)
	}
	if len(p) > r.tailCap {
		p = p[len(p)-r.tailCap:]
	}
	for len(p) > 0 {
		c := copy(r.ring[r.pos:], p)
		p = p[c:]
		r.pos = (r.pos + c) % r.tailCap
		r.filled = min(r.filled+c, r.tailCap)
	}
	return n, nil
}

// WriteString is Write for a string.
func (r *TextRetainer) WriteString(s string) (int, error) { return r.Write([]byte(s)) }

// Total is how many bytes were written.
func (r *TextRetainer) Total() int64 { return r.total }

// Result is what the retainer kept: the head and the tail, each cut on a rune
// boundary, and the exact number of bytes between them that were dropped.
func (r *TextRetainer) Result() TextResult {
	tail := make([]byte, 0, r.filled)
	if r.filled == r.tailCap {
		tail = append(tail, r.ring[r.pos:]...)
		tail = append(tail, r.ring[:r.pos]...)
	} else {
		tail = append(tail, r.ring[:r.filled]...)
	}
	head := r.head
	if r.total == int64(len(head))+int64(len(tail)) {
		// Nothing was cut: head and tail are adjacent, and a rune may span them.
		whole := string(append(append([]byte(nil), head...), tail...))
		return TextResult{Head: whole, Total: r.total}
	}
	// Each side of a real gap is a cut: a rune it splits goes whole.
	head, tail = trimEnd(head), trimStart(tail)
	return TextResult{
		Head:    string(head),
		Tail:    string(tail),
		Omitted: r.total - int64(len(head)) - int64(len(tail)),
		Total:   r.total,
	}
}

// trimEnd drops a rune cut off at the end of b: the last rune start and the
// continuation bytes after it, when they are fewer than that rune needs.
func trimEnd(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			return b
		}
	}
	return b
}

// trimStart drops the continuation bytes at the start of b, the end of a rune the
// cut began inside (at most UTFMax-1 of them; a longer run is not a cut rune).
func trimStart(b []byte) []byte {
	for i := 0; i < len(b) && i < utf8.UTFMax; i++ {
		if utf8.RuneStart(b[i]) {
			return b[i:]
		}
	}
	if len(b) < utf8.UTFMax {
		return b[len(b):]
	}
	return b
}

// TextResult is a retained text. When nothing was cut, Head is the whole text and
// Tail is empty, whatever the mode.
type TextResult struct {
	// Head is the kept start of the text ("" in Tail mode once a cut was made).
	Head string
	// Tail is the kept end of the text ("" in Head mode).
	Tail string
	// Omitted is the number of bytes dropped between Head and Tail: the budget
	// cut and any rune split by it.
	Omitted int64
	// Total is the number of bytes written.
	Total int64
}

// Truncated reports whether a budget cut anything.
func (t TextResult) Truncated() bool { return t.Omitted > 0 }

// String is the kept text, the head followed by the tail. A caller that shows
// the gap puts its notice between Head and Tail instead.
func (t TextResult) String() string { return t.Head + t.Tail }

// Notice is the omission clause of the result in bytes, "" when nothing was
// cut.
func (t TextResult) Notice() string { return Notice(t.Omitted, "bytes") }

// ItemRetainer keeps a window of a sequence within an item budget. It is not
// safe for concurrent use.
type ItemRetainer[T any] struct {
	headCap, tailCap int
	head             []T
	ring             []T
	pos, filled      int
	seen             int64
}

// Items returns a retainer that keeps the mode's part of what is pushed, within
// n items.
func Items[T any](mode Mode, n int) *ItemRetainer[T] {
	h, t := split(mode, n)
	return &ItemRetainer[T]{headCap: h, tailCap: t}
}

// Push offers one item. It reports whether the item is held now; a tail item may
// still be pushed out by later ones. Keep pushing every item seen, so the omitted
// count is exact.
func (r *ItemRetainer[T]) Push(v T) bool {
	r.seen++
	if len(r.head) < r.headCap {
		r.head = append(r.head, v)
		return true
	}
	if r.tailCap == 0 {
		return false
	}
	if r.ring == nil {
		r.ring = make([]T, r.tailCap)
	}
	r.ring[r.pos] = v
	r.pos = (r.pos + 1) % r.tailCap
	r.filled = min(r.filled+1, r.tailCap)
	return true
}

// Result is what the retainer kept, in the order pushed.
func (r *ItemRetainer[T]) Result() ItemsResult[T] {
	tail := make([]T, 0, r.filled)
	if r.filled == r.tailCap {
		tail = append(tail, r.ring[r.pos:]...)
		tail = append(tail, r.ring[:r.pos]...)
	} else {
		tail = append(tail, r.ring[:r.filled]...)
	}
	head := append([]T(nil), r.head...)
	omitted := r.seen - int64(len(head)) - int64(len(tail))
	if omitted == 0 {
		return ItemsResult[T]{Head: append(head, tail...), Seen: r.seen}
	}
	return ItemsResult[T]{Head: head, Tail: tail, Omitted: omitted, Seen: r.seen}
}

// ItemsResult is a retained sequence. When nothing was cut, Head is every item
// and Tail is empty, whatever the mode.
type ItemsResult[T any] struct {
	// Head is the kept start, in order.
	Head []T
	// Tail is the kept end, in order.
	Tail []T
	// Omitted is how many items were dropped between Head and Tail.
	Omitted int64
	// Seen is how many items were pushed.
	Seen int64
}

// Truncated reports whether a budget cut anything.
func (r ItemsResult[T]) Truncated() bool { return r.Omitted > 0 }

// Items is the kept items in order: the head, then the tail.
func (r ItemsResult[T]) Items() []T { return append(append([]T(nil), r.Head...), r.Tail...) }

// Notice is the omission clause of the result in items, "" when nothing was
// cut.
func (r ItemsResult[T]) Notice() string { return Notice(r.Omitted, "items") }
