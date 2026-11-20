package search

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
)

// Ignore files read at each directory, highest precedence first.
const (
	IgnoreFile    = ".ignore"
	GitIgnoreFile = ".gitignore"
	gitExclude    = ".git/info/exclude"
	maxIgnoreSize = 1 << 20
)

// rule is one line of an ignore file in gitignore syntax.
type rule struct {
	pattern  string // doublestar pattern
	negate   bool   // "!pattern": re-include
	dirOnly  bool   // "pattern/": directories only
	anchored bool   // contains a slash: matched against the path from the file's directory
	inside   bool   // "dir/**": matches below dir, not dir itself
}

// parseIgnore reads gitignore lines. A line that is not a valid pattern is
// skipped, as Git does.
func parseIgnore(data []byte) []rule {
	var rules []rule
	for raw := range bytes.SplitSeq(data, []byte("\n")) {
		line := strings.TrimSuffix(string(raw), "\r")
		line = trimUnescapedSpaces(line)
		if line == "" || line[0] == '#' {
			continue
		}
		var r rule
		if line[0] == '!' {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		if strings.Contains(line, "/") {
			r.anchored, line = true, strings.TrimLeft(line, "/")
		}
		r.inside = strings.HasSuffix(line, "/**")
		r.pattern = escapeBraces(line)
		if r.pattern == "" || !doublestar.ValidatePattern(r.pattern) {
			continue
		}
		rules = append(rules, r)
	}
	return rules
}

// trimUnescapedSpaces drops trailing spaces not escaped with a backslash.
func trimUnescapedSpaces(s string) string {
	for strings.HasSuffix(s, " ") && !strings.HasSuffix(s, `\ `) {
		s = s[:len(s)-1]
	}
	return s
}

// escapeBraces makes '{' and '}' literal: gitignore has no alternation.
func escapeBraces(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s):
			b.WriteByte(c)
			i++
			b.WriteByte(s[i])
		case c == '{' || c == '}':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (r rule) match(rel string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	subject := rel
	if !r.anchored {
		subject = path.Base(rel)
	}
	if ok, _ := doublestar.Match(r.pattern, subject); !ok {
		return false
	}
	if r.inside {
		if self, _ := doublestar.Match(strings.TrimSuffix(r.pattern, "/**"), subject); self {
			return false
		}
	}
	return true
}

// level is the ignore rules of one directory: sets in precedence order.
type level struct {
	dir  string
	sets [][]rule
	// err is set when an ignore file of dir could not be read in full. Its rules
	// are unknown, so everything below dir counts as ignored: a path a rule
	// would have hidden is never shown as if it were not.
	err error
}

// ignores is the stack of levels from the source root down to the directory being
// walked.
type ignores []*level

// ignored reports whether p (a path from the source root) is ignored. The deepest
// level with a matching rule decides; within a level the highest-precedence file
// with a match decides; within a file the last matching line decides.
func (s ignores) ignored(p string, isDir bool) bool {
	for i := len(s) - 1; i >= 0; i-- {
		l := s[i]
		if l.err != nil {
			return true
		}
		rel := p
		if l.dir != "." {
			rel = strings.TrimPrefix(p, l.dir+"/")
		}
		for _, set := range l.sets {
			for j := len(set) - 1; j >= 0; j-- {
				if set[j].match(rel, isDir) {
					return !set[j].negate
				}
			}
		}
	}
	return false
}

// loadLevel reads the ignore files of dir, whose entries are given. It returns nil
// when dir has none, and a level with err set when one cannot be read in full.
func loadLevel(src Source, dir string, entries []fs.DirEntry) *level {
	has := map[string]bool{}
	for _, e := range entries {
		switch e.Name() {
		case IgnoreFile, GitIgnoreFile:
			has[e.Name()] = !e.IsDir()
		case ".git":
			has[".git"] = e.IsDir()
		}
	}
	l := &level{dir: dir}
	for _, name := range []string{IgnoreFile, GitIgnoreFile, gitExclude} {
		if !has[name] && !(name == gitExclude && has[".git"]) {
			continue
		}
		data, err := readIgnore(src, join(dir, name), name == gitExclude)
		if err != nil {
			return &level{dir: dir, err: err}
		}
		if rules := parseIgnore(data); len(rules) > 0 {
			l.sets = append(l.sets, rules)
		}
	}
	if len(l.sets) == 0 {
		return nil
	}
	return l
}

// readIgnore reads an ignore file whole. A file over maxIgnoreSize is an error,
// not a prefix: a rule past the cut would otherwise be dropped silently. optional
// says a missing file is no error (.git/info/exclude need not exist).
func readIgnore(src Source, name string, optional bool) ([]byte, error) {
	f, err := src.Open(name)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ignore file %s: %w", name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxIgnoreSize+1))
	if err != nil {
		return nil, fmt.Errorf("ignore file %s: %w", name, err)
	}
	if len(data) > maxIgnoreSize {
		return nil, fmt.Errorf("ignore file %s: %w", name, ErrIgnoreTooLarge)
	}
	return data, nil
}

// ErrIgnoreTooLarge is an ignore file over 1 MiB, which is not read.
var ErrIgnoreTooLarge = errors.New("larger than 1 MiB")

func join(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// Matcher answers whether a path is ignored by the ignore files of a Source, using
// the same rules as a listing, without walking the tree or running git. Levels are
// read on first use and cached; a Matcher does not see later edits to ignore
// files. It is safe for concurrent use.
type Matcher struct {
	src    Source
	mu     sync.Mutex
	levels map[string]*level // nil value: the directory has no ignore files
}

// NewMatcher returns a Matcher over src.
func NewMatcher(src Source) *Matcher {
	return &Matcher{src: src, levels: map[string]*level{}}
}

// Ignored reports whether p (a slash or backslash path from the source root) is
// ignored: by a rule, by lying under an ignored directory, or by lying in a
// version-control directory. isDir says whether p names a directory. A path
// below a directory whose ignore file cannot be read is ignored; Check says why.
func (m *Matcher) Ignored(p string, isDir bool) bool {
	ignored, _ := m.Check(p, isDir)
	return ignored
}

// Check is Ignored, and also returns the error of an ignore file that could not
// be read in full on the way to p, in which case ignored is true: the rules that
// would decide are unknown.
func (m *Matcher) Check(p string, isDir bool) (ignored bool, err error) {
	p = clean(p)
	if p == "." {
		return false, nil
	}
	parts := strings.Split(p, "/")
	var stack ignores
	dir := "."
	for i, part := range parts {
		last := i == len(parts)-1
		if (!last || isDir) && vcsDirs[part] {
			return true, nil
		}
		if l := m.level(dir); l != nil {
			if l.err != nil {
				return true, l.err
			}
			stack = append(stack, l)
		}
		sub := join(dir, part)
		if stack.ignored(sub, !last || isDir) {
			return true, nil
		}
		dir = sub
	}
	return false, nil
}

func (m *Matcher) level(dir string) *level {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.levels[dir]; ok {
		return l
	}
	var l *level
	if entries, err := m.src.ReadDir(dir); err == nil {
		l = loadLevel(m.src, dir, entries)
	}
	m.levels[dir] = l
	return l
}
