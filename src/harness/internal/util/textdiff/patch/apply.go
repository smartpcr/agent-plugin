package patch

import (
	"slices"
	"strings"
	"unicode"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// Apply returns the text of h's file after the hunk: the contents of an add, ""
// for a delete, and original with the chunks applied for an update. original's
// line endings are normalised for matching and its dominant style restored in the
// result. A chunk that does not match returns an *Error with ReasonMismatch, the
// chunk's patch line and the stage, and no text.
func (h Hunk) Apply(original string) (string, error) {
	switch h.Op {
	case OpAdd:
		return h.Contents, nil
	case OpDelete:
		return "", nil
	}
	eol := fsx.DominantEOL(original)
	lines := strings.Split(fsx.NormalizeEOL(original), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	type replacement struct {
		start, n int
		with     []string
	}
	var reps []replacement
	at := 0
	for _, c := range h.Chunks {
		if c.HasContext {
			i := seek(lines, []string{c.Context}, at, false)
			if i < 0 {
				return "", h.mismatch(c, StageContext, "Failed to find context '"+c.Context+"' in "+h.Path)
			}
			at = i + 1
		}
		if len(c.Old) == 0 {
			// A pure addition goes at the end of the file.
			end := len(lines)
			if end > 0 && lines[end-1] == "" {
				end--
			}
			reps = append(reps, replacement{end, 0, c.New})
			continue
		}
		old, with := c.Old, c.New
		i := seek(lines, old, at, c.EOF)
		if i < 0 && old[len(old)-1] == "" {
			// The model often ends a chunk with an empty line the file lacks.
			old = old[:len(old)-1]
			if len(with) > 0 && with[len(with)-1] == "" {
				with = with[:len(with)-1]
			}
			i = seek(lines, old, at, c.EOF)
		}
		if i < 0 || i < at {
			// i < at: an end-of-file chunk overlapping an earlier chunk's lines,
			// which upstream would splice out of range.
			return "", h.mismatch(c, StageLines, "Failed to find expected lines in "+h.Path+":\n"+strings.Join(c.Old, "\n"))
		}
		reps = append(reps, replacement{i, len(old), with})
		at = i + len(old)
	}
	slices.SortStableFunc(reps, func(a, b replacement) int { return a.start - b.start })
	for i := len(reps) - 1; i >= 0; i-- {
		r := reps[i]
		lines = slices.Replace(lines, r.start, r.start+r.n, r.with...)
	}
	if len(lines) == 0 {
		return "", nil
	}
	// Every line, a trailing empty one included, gets its terminator. Upstream
	// adds one only when the last line is not empty, which drops a file's final
	// blank line on any edit.
	return fsx.RestoreEOL(strings.Join(lines, "\n")+"\n", eol), nil
}

func (h Hunk) mismatch(c Chunk, stage, msg string) *Error {
	return &Error{reason: ReasonMismatch, Line: c.Line, Path: h.Path, Stage: stage, Msg: msg}
}

// seek returns the first index at or after start where pattern occurs in lines, or
// -1. With eof it looks only at the end of lines. Four passes, each tried over every
// position before the next: exact, then trailing white space ignored, then white
// space at both ends ignored, then typographic punctuation and spaces folded too.
func seek(lines, pattern []string, start int, eof bool) int {
	if len(pattern) == 0 {
		return start
	}
	if len(pattern) > len(lines) {
		return -1
	}
	from := start
	if eof {
		from = len(lines) - len(pattern)
	}
	passes := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) },
		strings.TrimSpace,
		normalise,
	}
	for _, norm := range passes {
	positions:
		for i := from; i <= len(lines)-len(pattern); i++ {
			for j, p := range pattern {
				if norm(lines[i+j]) != norm(p) {
					continue positions
				}
			}
			return i
		}
	}
	return -1
}

// normalise trims s and folds the typographic characters a model tends to emit for
// their ASCII counterparts.
func normalise(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007',
			'\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}
