// Package textdiff computes line diffs of two texts (DH-D-FS-2): the hunks an edit
// review displays, the counts a result line reports, and the input the model-facing
// patch format (package textdiff/patch) is made from. It is the only importer of the
// diff engine (go-udiff's lcs), so the engine can change behind it.
//
// Lines are split on "\n" only. A carriage return stays part of its line, so a
// line-ending change shows as a changed line rather than vanishing; whether a line
// ends the text without a newline is carried on the line (Line.NoEOL), so the final
// newline is diffed like any other content.
package textdiff

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff/lcs"
)

// Kind is what a diff line does.
type Kind int

const (
	Equal  Kind = iota // a context line, in both texts
	Delete             // a line only in the old text
	Insert             // a line only in the new text
)

// Line is one line of a hunk. Text has no line terminator; NoEOL says the line ends
// its text without one.
type Line struct {
	Kind  Kind
	Text  string
	NoEOL bool
}

// Hunk is one run of changes with its context. Starts are 1-based line numbers; a
// side with no lines names the line after which the hunk applies (0 for the start),
// as unified diffs do.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []Line
}

// Limits bound the work of a diff (diff.md DH-LIB-DIFF-R4).
type Limits struct {
	// MaxBytes and MaxLines bound the inputs: above either the result is one
	// whole-text replacement hunk, marked Coarse, with no line matching attempted.
	MaxBytes, MaxLines int
	// HunkMaxBytes bounds the rendered hunks: above it only the counts are kept.
	HunkMaxBytes int
}

// DefaultLimits are 10 MiB and 50,000 lines of input, 256 KiB of hunks.
var DefaultLimits = Limits{MaxBytes: 10 << 20, MaxLines: 50_000, HunkMaxBytes: 256 << 10}

// DefaultContext is the number of context lines around a change.
const DefaultContext = 3

// Result is a diff of two texts.
type Result struct {
	Hunks []Hunk
	// Coarse: an input exceeded MaxBytes or MaxLines, so the one hunk replaces the
	// whole text.
	Coarse bool
	// CountsOnly: the hunks exceeded HunkMaxBytes and were dropped; Added and
	// Deleted are still exact for the diff computed.
	CountsOnly bool
	Added      int
	Deleted    int
}

// Changed reports whether the texts differ.
func (r Result) Changed() bool { return r.Added+r.Deleted > 0 }

// split cuts s into lines that keep their "\n".
func split(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func toLine(kind Kind, raw string) Line {
	text, cut := strings.CutSuffix(raw, "\n")
	return Line{Kind: kind, Text: text, NoEOL: !cut}
}

// Diff is Lines with DefaultContext and DefaultLimits.
func Diff(before, after string) Result {
	return Lines(before, after, DefaultContext, DefaultLimits)
}

// Lines diffs before against after with context lines around each change. A zero
// field of limits takes its DefaultLimits value.
func Lines(before, after string, context int, limits Limits) Result {
	limits = limits.withDefaults()
	context = max(context, 0)
	a, b := split(before), split(after)
	var res Result
	if len(before)+len(after) > limits.MaxBytes || max(len(a), len(b)) > limits.MaxLines {
		res = coarse(a, b)
	} else {
		res = hunks(a, b, lcs.DiffLines(a, b), context)
	}
	if size := res.size(); size > limits.HunkMaxBytes {
		res.Hunks, res.CountsOnly = nil, true
	}
	return res
}

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultLimits.MaxBytes
	}
	if l.MaxLines <= 0 {
		l.MaxLines = DefaultLimits.MaxLines
	}
	if l.HunkMaxBytes <= 0 {
		l.HunkMaxBytes = DefaultLimits.HunkMaxBytes
	}
	return l
}

func (r Result) size() int {
	n := 0
	for _, h := range r.Hunks {
		for _, l := range h.Lines {
			n += len(l.Text) + 2
		}
	}
	return n
}

func coarse(a, b []string) Result {
	if len(a) == 0 && len(b) == 0 {
		return Result{Coarse: true}
	}
	h := Hunk{OldStart: start(0, len(a)), OldLines: len(a), NewStart: start(0, len(b)), NewLines: len(b)}
	for _, l := range a {
		h.Lines = append(h.Lines, toLine(Delete, l))
	}
	for _, l := range b {
		h.Lines = append(h.Lines, toLine(Insert, l))
	}
	return Result{Hunks: []Hunk{h}, Coarse: true, Added: len(b), Deleted: len(a)}
}

// start is the unified-diff start of a side beginning at index i with n lines.
func start(i, n int) int {
	if n == 0 {
		return i
	}
	return i + 1
}

// hunks groups the edits of diffs, with context lines, merging edits whose gap is at
// most twice the context.
func hunks(a, b []string, diffs []lcs.Diff, context int) Result {
	var res Result
	for i := 0; i < len(diffs); {
		// Edits i..j-1 form one hunk.
		j := i + 1
		for j < len(diffs) && diffs[j].Start-diffs[j-1].End <= 2*context {
			j++
		}
		first, last := diffs[i], diffs[j-1]
		oldFrom := max(first.Start-context, 0)
		oldTo := min(last.End+context, len(a))
		newFrom := first.ReplStart - (first.Start - oldFrom)
		newTo := last.ReplEnd + (oldTo - last.End)
		h := Hunk{
			OldStart: start(oldFrom, oldTo-oldFrom), OldLines: oldTo - oldFrom,
			NewStart: start(newFrom, newTo-newFrom), NewLines: newTo - newFrom,
		}
		at := oldFrom
		for _, d := range diffs[i:j] {
			for ; at < d.Start; at++ {
				h.Lines = append(h.Lines, toLine(Equal, a[at]))
			}
			for k := d.Start; k < d.End; k++ {
				h.Lines = append(h.Lines, toLine(Delete, a[k]))
			}
			for k := d.ReplStart; k < d.ReplEnd; k++ {
				h.Lines = append(h.Lines, toLine(Insert, b[k]))
			}
			res.Deleted += d.End - d.Start
			res.Added += d.ReplEnd - d.ReplStart
			at = d.End
		}
		for ; at < oldTo; at++ {
			h.Lines = append(h.Lines, toLine(Equal, a[at]))
		}
		res.Hunks = append(res.Hunks, h)
		i = j
	}
	return res
}

// BeforeAfter returns the old and new text the hunk covers.
func (h Hunk) BeforeAfter() (before, after string) {
	var o, n strings.Builder
	for _, l := range h.Lines {
		if l.Kind != Insert {
			writeLine(&o, l)
		}
		if l.Kind != Delete {
			writeLine(&n, l)
		}
	}
	return o.String(), n.String()
}

func writeLine(b *strings.Builder, l Line) {
	b.WriteString(l.Text)
	if !l.NoEOL {
		b.WriteByte('\n')
	}
}

// ErrMismatch: Apply found a hunk whose old lines are not in the text.
var ErrMismatch = errors.New("textdiff: hunk does not match the text")

// ErrNoHunks: Apply was given a CountsOnly result.
var ErrNoHunks = errors.New("textdiff: result carries counts only")

// Apply rebuilds the new text from before and the hunks of r. It is the inverse of
// Lines: Lines(a, b, ...).Apply(a) is b for every a and b.
func (r Result) Apply(before string) (string, error) {
	if r.CountsOnly {
		return "", ErrNoHunks
	}
	a := split(before)
	var out strings.Builder
	at := 0
	for hi, h := range r.Hunks {
		from := h.OldStart - 1
		if h.OldLines == 0 {
			from = h.OldStart
		}
		if from < at || from > len(a) {
			return "", fmt.Errorf("%w: hunk %d starts at line %d", ErrMismatch, hi+1, h.OldStart)
		}
		for ; at < from; at++ {
			out.WriteString(a[at])
		}
		for _, l := range h.Lines {
			if l.Kind == Insert {
				writeLine(&out, l)
				continue
			}
			if at >= len(a) || toLine(l.Kind, a[at]) != l {
				return "", fmt.Errorf("%w: hunk %d at old line %d", ErrMismatch, hi+1, at+1)
			}
			if l.Kind == Equal {
				out.WriteString(a[at])
			}
			at++
		}
	}
	for ; at < len(a); at++ {
		out.WriteString(a[at])
	}
	return out.String(), nil
}

// Unified renders r as a unified diff with the given file labels, for display.
func (r Result) Unified(oldName, newName string) string {
	if len(r.Hunks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range r.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", span(h.OldStart, h.OldLines), span(h.NewStart, h.NewLines))
		for _, l := range h.Lines {
			b.WriteByte(" -+"[l.Kind])
			b.WriteString(l.Text)
			b.WriteByte('\n')
			if l.NoEOL {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return b.String()
}

func span(start, n int) string {
	if n == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}
