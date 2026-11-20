package patch

import (
	"errors"
	"math"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/textdiff"
)

// Format writes hunks as patch text that Parse reads back to the same hunks (line
// numbers aside). Every chunk is written with its "@@" line, its old lines as "-"
// and its new lines as "+".
func Format(hunks ...Hunk) string {
	var b strings.Builder
	b.WriteString(BeginPatch + "\n")
	for _, h := range hunks {
		switch h.Op {
		case OpAdd:
			b.WriteString(AddFile + h.Path + "\n")
			for _, l := range splitContents(h.Contents) {
				b.WriteString("+" + l + "\n")
			}
		case OpDelete:
			b.WriteString(DeleteFile + h.Path + "\n")
		case OpUpdate:
			b.WriteString(UpdateFile + h.Path + "\n")
			if h.MoveTo != "" {
				b.WriteString(MoveTo + h.MoveTo + "\n")
			}
			for _, c := range h.Chunks {
				b.WriteString("@@")
				if c.HasContext {
					b.WriteString(" " + c.Context)
				}
				b.WriteByte('\n')
				for _, l := range c.Old {
					b.WriteString("-" + l + "\n")
				}
				for _, l := range c.New {
					b.WriteString("+" + l + "\n")
				}
				if c.EOF {
					b.WriteString(EndOfFile + "\n")
				}
			}
		}
	}
	b.WriteString(EndPatch + "\n")
	return b.String()
}

func splitContents(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(fsx.NormalizeEOL(s), "\n"), "\n")
}

// Add is the hunk that creates path with contents.
func Add(path, contents string) Hunk {
	var c strings.Builder
	for _, l := range splitContents(contents) {
		c.WriteString(l + "\n")
	}
	return Hunk{Op: OpAdd, Path: path, Contents: c.String()}
}

// Delete is the hunk that deletes path.
func Delete(path string) Hunk { return Hunk{Op: OpDelete, Path: path} }

// ErrInexpressible: neither the diff's chunks nor a whole-file chunk, written as
// patch text and read back, turns before into after (a line ending in a lone
// carriage return, which the parser drops, is such a case).
var ErrInexpressible = errors.New("patch: the change cannot be expressed as a patch")

// Update is the update hunk that turns before into after: one chunk per diff hunk,
// with three lines of context and "*** End of File" on a chunk that reaches the end.
// The hunk is checked by applying it; when the matcher would place a chunk
// elsewhere (repeated lines), a single chunk replacing the whole file is returned
// instead. Line endings of both texts are normalised to LF first; Apply restores
// before's style.
func Update(path, before, after string) (Hunk, error) {
	a, b := fsx.NormalizeEOL(before), fsx.NormalizeEOL(after)
	want := b
	if want != "" && !strings.HasSuffix(want, "\n") {
		want += "\n"
	}
	h := Hunk{Op: OpUpdate, Path: path}
	if a == want {
		return h, nil
	}
	oldLines := len(splitContents(a))
	unbounded := textdiff.Limits{MaxBytes: math.MaxInt, MaxLines: math.MaxInt, HunkMaxBytes: math.MaxInt}
	for _, dh := range textdiff.Lines(a, b, textdiff.DefaultContext, unbounded).Hunks {
		c := Chunk{}
		for _, l := range dh.Lines {
			if l.Kind != textdiff.Insert {
				c.Old = append(c.Old, l.Text)
			}
			if l.Kind != textdiff.Delete {
				c.New = append(c.New, l.Text)
			}
		}
		from := dh.OldStart - 1
		if dh.OldLines == 0 {
			from = dh.OldStart
		}
		c.EOF = len(c.Old) > 0 && from+dh.OldLines == oldLines
		h.Chunks = append(h.Chunks, c)
	}
	if roundTrips(h, a, want) {
		return h, nil
	}
	whole := Hunk{Op: OpUpdate, Path: path, Chunks: []Chunk{{
		Old: splitContents(a),
		New: splitContents(b),
		EOF: a != "",
	}}}
	if roundTrips(whole, a, want) {
		return whole, nil
	}
	return Hunk{}, ErrInexpressible
}

// roundTrips reports whether h, written as patch text and parsed back, turns a into
// want.
func roundTrips(h Hunk, a, want string) bool {
	if len(h.Chunks) == 0 {
		got, err := h.Apply(a)
		return err == nil && got == want
	}
	p, err := Parse(Format(h))
	if err != nil || len(p.Hunks) != 1 {
		return false
	}
	got, err := p.Hunks[0].Apply(a)
	return err == nil && got == want
}

// Make is the patch text of Update(path, before, after).
func Make(path, before, after string) (string, error) {
	h, err := Update(path, before, after)
	if err != nil {
		return "", err
	}
	if len(h.Chunks) == 0 {
		return Format(), nil
	}
	return Format(h), nil
}
