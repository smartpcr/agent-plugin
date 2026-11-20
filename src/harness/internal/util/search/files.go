package search

import (
	"cmp"
	"context"
	"slices"
	"strings"
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
	// Offset skips the first Offset paths of the ordered result, so successive
	// offsets page through one listing.
	Offset int
	// Sample, when more paths match than MaxMatches, returns MaxMatches of them
	// spread over the tree instead of the first ones in walk order: each
	// directory's budget is split across its children, at every depth.
	Sample bool
	Limits Limits // MaxMatches (paths emitted) and MaxFiles (files visited)
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

// Files emits the paths (from the Source's root) of the files q selects, in the
// order q.Order names. Offset skips that many paths first; MaxMatches bounds the
// paths emitted after it.
func (Local) Files(ctx context.Context, q FilesQuery, emit func(path string) bool) (Summary, error) {
	var sum Summary
	g, err := newGlob("glob", q.Pattern)
	if err != nil {
		return sum, err
	}
	root := clean(q.Root)
	maxOut := max(q.Limits.MaxMatches, 0)
	skip := max(q.Offset, 0)
	w := &walker{ctx: ctx, src: q.From, sum: &sum, opt: walkOptions{
		hidden: q.Hidden, noIgnore: q.NoIgnore, exclude: q.Exclude,
		maxFiles: pick(q.Limits.MaxFiles, DefaultMaxFiles),
		enter:    g.mayContain(root),
	}}
	// out releases one path in final order, applying Offset and MaxMatches.
	out := func(p string) bool {
		if skip > 0 {
			skip--
			return true
		}
		if maxOut > 0 && sum.Matches >= maxOut {
			sum.cut(CutMatches)
			return false
		}
		sum.Matches++
		if !emit(p) {
			sum.cut(CutStopped)
			return false
		}
		return true
	}
	hold := q.Order == ByTime || q.Sample && maxOut > 0
	type stamped struct {
		path string
		mod  time.Time
	}
	var held []stamped
	err = w.walk(root, func(f file) bool {
		if !g.match(relTo(root, f.path)) {
			return true
		}
		if !hold {
			return out(f.path)
		}
		var mod time.Time
		if q.Order == ByTime && f.entry != nil {
			if info, err := f.entry.Info(); err == nil {
				mod = info.ModTime()
			}
		}
		held = append(held, stamped{f.path, mod})
		return true
	})
	if !hold {
		return sum, err
	}
	if q.Sample && maxOut > 0 && len(held)-skip > maxOut {
		paths := make([]string, len(held))
		for i, h := range held {
			paths[i] = h.path
		}
		keep := sample(root, paths, skip+maxOut)
		kept := held[:0]
		for i, h := range held {
			if keep[i] {
				kept = append(kept, h)
			}
		}
		held = kept
		sum.cut(CutMatches)
	}
	if q.Order == ByTime {
		slices.SortStableFunc(held, func(a, b stamped) int {
			if c := b.mod.Compare(a.mod); c != 0 {
				return c
			}
			return cmp.Compare(a.path, b.path)
		})
	}
	for _, h := range held {
		if !out(h.path) {
			break
		}
	}
	return sum, err
}

// sample picks budget of paths (walk order, from root) spread over the tree: each
// directory splits its budget across its children in turn, a child that needs
// less than its share passing the rest on, so early siblings cannot take the
// whole sample at any depth.
func sample(root string, paths []string, budget int) []bool {
	type node struct {
		kids  []*node
		leaf  int // index into paths, or -1
		count int
	}
	top := &node{leaf: -1}
	index := map[string]*node{}
	for i, p := range paths {
		cur, key := top, ""
		parts := strings.Split(relTo(root, p), "/")
		for j, part := range parts {
			key += "/" + part
			n := index[key]
			if n == nil {
				n = &node{leaf: -1}
				if j == len(parts)-1 {
					n.leaf = i
				}
				index[key] = n
				cur.kids = append(cur.kids, n)
			}
			n.count++
			cur = n
		}
		top.count++
	}
	keep := make([]bool, len(paths))
	var fill func(n *node, b int)
	fill = func(n *node, b int) {
		if b <= 0 {
			return
		}
		if n.leaf >= 0 {
			keep[n.leaf] = true
			return
		}
		share := make([]int, len(n.kids))
		left := b
		// Repeatedly give every unsatisfied child an equal part of what is left.
		for left > 0 {
			var open []int
			for i, k := range n.kids {
				if share[i] < k.count {
					open = append(open, i)
				}
			}
			if len(open) == 0 {
				break
			}
			if left < len(open) {
				// Fewer than one each: spread the rest evenly, not to the first.
				for j := range left {
					share[open[j*len(open)/left]]++
				}
				break
			}
			part := left / len(open)
			for _, i := range open {
				give := min(part, n.kids[i].count-share[i])
				share[i] += give
				left -= give
			}
		}
		for i, k := range n.kids {
			fill(k, share[i])
		}
	}
	fill(top, budget)
	return keep
}
