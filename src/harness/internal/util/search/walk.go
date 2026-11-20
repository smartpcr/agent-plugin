package search

import (
	"context"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// vcsDirs are never descended into, whatever the switches say.
var vcsDirs = map[string]bool{".git": true, ".svn": true, ".hg": true, ".bzr": true, ".jj": true, ".sl": true}

// walkOptions are the switches shared by every walk.
type walkOptions struct {
	hidden   bool     // include names starting with a dot
	noIgnore bool     // do not read ignore files
	exclude  []string // base-name globs never listed nor descended into
	maxFiles int      // 0: no bound
	// enter reports whether a directory (a path from the source root) can hold a
	// file the query wants; nil enters every directory.
	enter func(dir string) bool
}

// walker visits the files below a root in name order.
type walker struct {
	ctx  context.Context
	src  Source
	opt  walkOptions
	sum  *Summary
	stop bool
}

// file is one visited file: its path from the source root and its entry (nil when
// the root itself is a file).
type file struct {
	path  string
	entry fs.DirEntry
}

// walk calls visit for every file under root that the switches admit; visit
// returning false ends the walk. It returns an *Error when the root cannot be
// read or ctx ends.
func (w *walker) walk(root string, visit func(file) bool) error {
	root = clean(root)
	entries, err := w.src.ReadDir(root)
	if err != nil {
		// The root may be a file.
		f, oerr := w.src.Open(root)
		if oerr != nil {
			return &Error{reason: ReasonFailed, Msg: "cannot read " + root, Err: err}
		}
		f.Close()
		if w.admitFile() {
			visit(file{path: root})
		}
		return w.ctxErr()
	}
	var stack ignores
	if !w.opt.noIgnore {
		stack = w.ancestors(root)
	}
	w.dir(root, entries, stack, visit)
	return w.ctxErr()
}

// ancestors loads the ignore levels of the directories above root.
func (w *walker) ancestors(root string) ignores {
	var stack ignores
	if root == "." {
		return stack
	}
	dirs := []string{"."}
	parts := strings.Split(root, "/")
	for i := 1; i < len(parts); i++ {
		dirs = append(dirs, strings.Join(parts[:i], "/"))
	}
	for _, d := range dirs {
		if entries, err := w.src.ReadDir(d); err == nil {
			if l := loadLevel(w.src, d, entries); l != nil {
				stack = append(stack, l)
			}
		}
	}
	return stack
}

func (w *walker) dir(dir string, entries []fs.DirEntry, stack ignores, visit func(file) bool) {
	if !w.opt.noIgnore {
		if l := loadLevel(w.src, dir, entries); l != nil {
			stack = append(stack[:len(stack):len(stack)], l)
		}
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, e := range entries {
		if w.stop || w.ctx.Err() != nil {
			w.stop = true
			return
		}
		name := e.Name()
		isDir := e.IsDir()
		if isDir && vcsDirs[name] || !w.opt.hidden && strings.HasPrefix(name, ".") || w.excluded(name) {
			continue
		}
		p := join(dir, name)
		if !w.opt.noIgnore && stack.ignored(p, isDir) {
			continue
		}
		if isDir {
			if w.opt.enter != nil && !w.opt.enter(p) {
				continue
			}
			sub, err := w.src.ReadDir(p)
			if err != nil {
				w.sum.cut(CutUnreadable)
				continue
			}
			w.dir(p, sub, stack, visit)
			continue
		}
		if !w.admitFile() {
			return
		}
		if !visit(file{path: p, entry: e}) {
			w.stop = true
			return
		}
	}
}

// admitFile counts a file against MaxFiles, ending the walk when it is over.
func (w *walker) admitFile() bool {
	if w.opt.maxFiles > 0 && w.sum.Files >= w.opt.maxFiles {
		w.sum.cut(CutFiles)
		w.stop = true
		return false
	}
	w.sum.Files++
	return true
}

func (w *walker) excluded(name string) bool {
	for _, x := range w.opt.exclude {
		if ok, _ := doublestar.Match(x, name); ok {
			return true
		}
	}
	return false
}

func (w *walker) ctxErr() error {
	if err := w.ctx.Err(); err != nil {
		w.sum.cut(CutAborted)
		return &Error{reason: ReasonAborted, Msg: "search cancelled", Err: err}
	}
	return nil
}

func clean(p string) string {
	if p == "" {
		return "."
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// relTo is p relative to root, both paths from the source root.
func relTo(root, p string) string {
	switch {
	case root == p:
		return path.Base(p)
	case root == ".":
		return p
	}
	return strings.TrimPrefix(p, root+"/")
}

// globMatcher matches a glob against a path relative to the walk root, or against
// the base name when the glob has no slash.
type globMatcher struct {
	pattern string
	base    bool
}

func newGlob(what, pattern string) (*globMatcher, error) {
	if pattern == "" {
		return nil, nil
	}
	if !doublestar.ValidatePattern(pattern) {
		return nil, invalidPattern(what, pattern, doublestar.ErrBadPattern)
	}
	return &globMatcher{pattern: strings.TrimPrefix(pattern, "./"), base: !strings.Contains(pattern, "/")}, nil
}

func (g *globMatcher) match(rel string) bool {
	if g == nil {
		return true
	}
	if g.base {
		rel = path.Base(rel)
	}
	ok, _ := doublestar.Match(g.pattern, rel)
	return ok
}

// mayContain returns the walk's directory filter for a walk from root: a
// directory is entered only when some path the glob matches can lie under it,
// so a walk for "src/**/*.ts" starts at src and never lists unrelated
// directories. It returns nil (enter everything) for a base-name glob.
func (g *globMatcher) mayContain(root string) func(dir string) bool {
	if g == nil || g.base || braceSpansSlash(g.pattern) {
		return nil
	}
	segs := strings.Split(g.pattern, "/")
	return func(dir string) bool {
		for i, d := range strings.Split(relTo(root, dir), "/") {
			if i < len(segs) && segs[i] == "**" {
				return true
			}
			// The last segment names the file, so a directory needs one more.
			if i >= len(segs)-1 {
				return false
			}
			if ok, _ := doublestar.Match(segs[i], d); !ok {
				return false
			}
		}
		return true
	}
}

// braceSpansSlash reports whether an alternation in pattern holds a slash, as in
// "{a,b/c}/x": its segments cannot then be matched one directory at a time.
func braceSpansSlash(pattern string) bool {
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth = max(depth-1, 0)
		case '/':
			if depth > 0 {
				return true
			}
		}
	}
	return false
}
