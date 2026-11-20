package search

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// Order is the order Files emits paths in.
type Order int

const (
	ByPath Order = iota // walk order: names compared byte-wise, directory by directory
	ByTime              // newest modification first, ties by path
)

// FilesQuery is a file listing. The zero switches give the UI listing: ignore
// rules honoured, hidden names skipped. The glob tool sets NoIgnore and Hidden.
type FilesQuery struct {
	From     Source
	Root     string   // directory (or file) in From; "" is the whole view
	Pattern  string   // optional doublestar glob, matched against the path from Root (the base name when it has no slash)
	Hidden   bool     // include names starting with a dot
	NoIgnore bool     // do not apply ignore files
	Exclude  []string // base-name globs never listed nor descended into
	Order    Order
	Limits   Limits // MaxMatches (paths emitted) and MaxFiles (files visited)
}

// Searcher is the search service: content search and file listing.
type Searcher interface {
	Grep(ctx context.Context, q GrepQuery, emit func(Match) bool) (Summary, error)
	Files(ctx context.Context, q FilesQuery, emit func(path string) bool) (Summary, error)
}

// Local is the in-process Searcher. Workers sizes Grep's pool; 0 is GOMAXPROCS.
type Local struct{ Workers int }

var _ Searcher = Local{}

// Files emits the paths (from the Source's root) of the files q selects.
func Files(ctx context.Context, q FilesQuery, emit func(path string) bool) (Summary, error) {
	return Local{}.Files(ctx, q, emit)
}

// List is Files collected into a slice.
func List(ctx context.Context, q FilesQuery) ([]string, Summary, error) {
	var out []string
	sum, err := Files(ctx, q, func(p string) bool { out = append(out, p); return true })
	return out, sum, err
}

// Files emits the paths (from the Source's root) of the files q selects.
func (Local) Files(ctx context.Context, q FilesQuery, emit func(path string) bool) (Summary, error) {
	var sum Summary
	g, err := newGlob("glob", q.Pattern)
	if err != nil {
		return sum, err
	}
	root := clean(q.Root)
	maxOut := 0
	if q.Limits.MaxMatches > 0 {
		maxOut = q.Limits.MaxMatches
	}
	w := &walker{ctx: ctx, src: q.From, sum: &sum, opt: walkOptions{
		hidden: q.Hidden, noIgnore: q.NoIgnore, exclude: q.Exclude,
		maxFiles: pick(q.Limits.MaxFiles, DefaultMaxFiles),
	}}
	type stamped struct {
		path string
		mod  time.Time
	}
	var held []stamped
	full := false
	err = w.walk(root, func(f file) bool {
		if !g.match(relTo(root, f.path)) {
			return true
		}
		if full {
			sum.cut(CutMatches)
			return false
		}
		if q.Order == ByTime {
			var mod time.Time
			if f.entry != nil {
				if info, err := f.entry.Info(); err == nil {
					mod = info.ModTime()
				}
			}
			held = append(held, stamped{f.path, mod})
			return true
		}
		sum.Matches++
		if !emit(f.path) {
			sum.cut(CutStopped)
			return false
		}
		full = maxOut > 0 && sum.Matches >= maxOut
		return true
	})
	if q.Order == ByTime {
		slices.SortStableFunc(held, func(a, b stamped) int {
			if c := b.mod.Compare(a.mod); c != 0 {
				return c
			}
			return cmp.Compare(a.path, b.path)
		})
		for _, h := range held {
			if maxOut > 0 && sum.Matches >= maxOut {
				sum.cut(CutMatches)
				break
			}
			sum.Matches++
			if !emit(h.path) {
				sum.cut(CutStopped)
				break
			}
		}
	}
	return sum, err
}
