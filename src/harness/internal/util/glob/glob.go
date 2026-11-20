// Package glob is the harness's one glob dialect (DH-D-UTIL-4,
// docs/harness/libs/picomatch.md DH-ADR-PICOMATCH-1). Every pattern a person writes
// in configuration or a component declares is compiled here, and nowhere else.
//
// The dialect, for operators and plugin authors:
//
//   - `/` separates segments on every platform; a file-system path is converted
//     with FromOS before it is matched.
//   - `*` matches any run of characters except `/`, `?` one character except `/`.
//   - `[abc]`, `[a-z]`, `[!a]` and `[^a]` are character classes.
//   - `{a,b}` is one of the alternatives; alternatives may nest.
//   - `**` as a whole segment matches any depth, including none: `**/*.go`
//     matches `main.go` and `a/b/c.go`.
//   - `*` and `**` match names that start with a dot.
//   - `\` escapes the next character.
//
// There is no negation, no extended glob (`@(a|b)`) and no regular-expression
// text. Two variations exist, by name only: FoldCase and BaseName. A malformed
// pattern is an error when it is compiled, never a pattern that matches nothing.
//
//dsh:untrusted-parser patterns arrive from configuration and plugin manifests
package glob

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

// Bounds on one pattern and one matched name (DH-LIB-PICOMATCH-R6).
const (
	// MaxPatternLen is the longest pattern Compile accepts, in bytes.
	MaxPatternLen = 1024
	// MaxNameLen is the longest name a Matcher tests; a longer name never matches.
	MaxNameLen = 4096
	// MaxExpansions bounds how many literal alternatives `{a,b}` groups multiply
	// out to, which bounds the work of one failed match.
	MaxExpansions = 1024
)

// PatternError reports a pattern Compile refused.
type PatternError struct {
	Pattern string
	Reason  string
}

func (e *PatternError) Error() string {
	return fmt.Sprintf("glob: pattern %q: %s", e.Pattern, e.Reason)
}

// Option is one of the two named variations of the dialect.
type Option func(*options)

type options struct{ fold, base bool }

// FoldCase matches without regard to letter case (ASCII and Unicode simple folding).
func FoldCase() Option { return func(o *options) { o.fold = true } }

// BaseName tests a pattern that has no `/` against the last segment of the name.
func BaseName() Option { return func(o *options) { o.base = true } }

// Matcher is a compiled pattern or pattern set: an immutable value, safe for
// concurrent use, with no resource behind it.
type Matcher interface {
	Match(name string) bool
}

type pattern struct {
	text string // as compiled: with FoldCase, every letter and class closed under folding
	base bool   // the pattern has no "/" and BaseName was asked for
}

// Compile checks pattern and returns its matcher.
func Compile(p string, opts ...Option) (Matcher, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return compile(p, o)
}

// MustCompile is Compile for patterns written in Go source; it panics on an error.
func MustCompile(p string, opts ...Option) Matcher {
	m, err := Compile(p, opts...)
	if err != nil {
		panic(err)
	}
	return m
}

func compile(p string, o options) (*pattern, error) {
	switch {
	case p == "":
		return nil, &PatternError{Pattern: p, Reason: "is empty"}
	case len(p) > MaxPatternLen:
		return nil, &PatternError{Pattern: p, Reason: fmt.Sprintf("is longer than %d bytes", MaxPatternLen)}
	case !doublestar.ValidatePattern(p):
		return nil, &PatternError{Pattern: p, Reason: "is malformed (an unclosed class or alternative, or a trailing escape)"}
	}
	if n := expansions(p); n > MaxExpansions {
		return nil, &PatternError{Pattern: p, Reason: fmt.Sprintf("expands to more than %d alternatives", MaxExpansions)}
	}
	text := p
	if o.fold {
		text = foldPattern(p)
	}
	return &pattern{text: text, base: o.base && !strings.Contains(p, "/")}, nil
}

func (p *pattern) Match(name string) bool {
	if len(name) > MaxNameLen {
		return false
	}
	if p.base {
		name = path.Base(name)
	}
	return doublestar.MatchUnvalidated(p.text, name)
}

type set struct{ include, exclude []*pattern }

// CompileSet returns a matcher for "any of include and none of exclude". An empty
// include list matches nothing.
func CompileSet(include, exclude []string, opts ...Option) (Matcher, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	s := &set{}
	for _, p := range include {
		c, err := compile(p, o)
		if err != nil {
			return nil, err
		}
		s.include = append(s.include, c)
	}
	for _, p := range exclude {
		c, err := compile(p, o)
		if err != nil {
			return nil, err
		}
		s.exclude = append(s.exclude, c)
	}
	return s, nil
}

func (s *set) Match(name string) bool {
	in := false
	for _, p := range s.include {
		if p.Match(name) {
			in = true
			break
		}
	}
	if !in {
		return false
	}
	for _, p := range s.exclude {
		if p.Match(name) {
			return false
		}
	}
	return true
}

// FromOS converts a file-system path to the `/`-separated form patterns match.
func FromOS(p string) string { return filepath.ToSlash(p) }

// orbit returns r's Unicode simple-fold orbit, r first: K, k and the Kelvin
// sign are one orbit; σ, ς and Σ are one; I and dotless ı are not.
func orbit(r rune) []rune {
	out := []rune{r}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		out = append(out, f)
	}
	return out
}

var foldables = sync.OnceValue(func() []rune {
	var out []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.SimpleFold(r) != r {
			out = append(out, r)
		}
	}
	return out
})

// foldPattern rewrites a validated pattern so that matching it exactly is
// matching p under simple folding: a name rune matches where any member of its
// orbit matched p. A literal letter becomes the class of its orbit; a class gains
// every orbit partner of its members, so a range keeps the membership it was
// written with (folding endpoints would not: folding is not order-preserving), and
// a negated class still refuses every partner of what it lists.
func foldPattern(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); {
		r, n := utf8.DecodeRuneInString(p[i:])
		switch r {
		case '\\':
			e, m := utf8.DecodeRuneInString(p[i+n:])
			if len(orbit(e)) > 1 {
				writeOrbit(&b, e)
			} else {
				b.WriteString(p[i : i+n+m])
			}
			i += n + m
		case '[':
			i += n + foldClass(&b, p[i+n:])
		default:
			if len(orbit(r)) > 1 {
				writeOrbit(&b, r)
			} else {
				b.WriteString(p[i : i+n])
			}
			i += n
		}
	}
	return b.String()
}

func writeOrbit(b *strings.Builder, r rune) {
	b.WriteByte('[')
	for _, o := range orbit(r) {
		b.WriteRune(o)
	}
	b.WriteByte(']')
}

type interval struct{ lo, hi rune }

// foldClass writes the closed form of the class whose body starts s (just after
// its '[') and returns the bytes of s it consumed, through the closing ']'. It
// reads the class as doublestar does.
func foldClass(b *strings.Builder, s string) int {
	i := 0
	b.WriteByte('[')
	if s[0] == '!' || s[0] == '^' {
		b.WriteByte(s[0])
		i++
	}
	var set []interval
	have := false
	for s[i] != ']' {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if have && r == '-' && s[i] != ']' {
			if s[i] == '\\' {
				i++
			}
			hi, m := utf8.DecodeRuneInString(s[i:])
			i += m
			set[len(set)-1].hi = hi
			have = false
			continue
		}
		if r == '\\' {
			r, n = utf8.DecodeRuneInString(s[i:])
			i += n
		}
		set = append(set, interval{r, r})
		have = true
	}
	in := func(r rune) bool {
		for _, iv := range set {
			if iv.lo <= r && r <= iv.hi {
				return true
			}
		}
		return false
	}
	for _, iv := range set {
		writeClassRune(b, iv.lo)
		if iv.hi != iv.lo {
			b.WriteByte('-')
			writeClassRune(b, iv.hi)
		}
	}
	for _, f := range foldables() {
		if in(f) {
			for _, o := range orbit(f)[1:] {
				if !in(o) {
					set = append(set, interval{o, o})
					writeClassRune(b, o)
				}
			}
		}
	}
	b.WriteByte(']')
	return i + 1
}

// writeClassRune writes r as one class member; ASCII punctuation is escaped so
// it never reads as a range, a negation or the end of the class.
func writeClassRune(b *strings.Builder, r rune) {
	if r < utf8.RuneSelf && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		b.WriteByte('\\')
	}
	b.WriteRune(r)
}

// expansions counts the literal patterns the `{}` groups of p multiply out to,
// saturating above MaxExpansions. p has been validated.
func expansions(p string) int {
	n, _ := seq(p, 0, false)
	return n
}

// seq reads a sequence from p[i:] up to the end, or, inside a group, up to the
// unescaped `,` or `}` that ends the alternative. It returns the product of its
// groups' sizes and the index where it stopped.
func seq(p string, i int, inGroup bool) (int, int) {
	n := 1
	for i < len(p) {
		switch p[i] {
		case '\\':
			i += 2
		case '[':
			i = classEnd(p, i)
		case '{':
			g, j := group(p, i+1)
			n = sat(n * g)
			i = j
		case ',', '}':
			if inGroup {
				return n, i
			}
			i++
		default:
			i++
		}
	}
	return n, i
}

// group reads the alternatives after a `{` and returns the sum of their sizes and
// the index after the closing `}`.
func group(p string, i int) (int, int) {
	total := 0
	for i < len(p) {
		n, j := seq(p, i, true)
		total = sat(total + n)
		if j >= len(p) {
			return total, j
		}
		i = j + 1
		if p[j] == '}' {
			return total, i
		}
	}
	return total, i
}

// classEnd returns the index after the `]` that closes the class starting at i.
func classEnd(p string, i int) int {
	j := i + 1
	if j < len(p) && (p[j] == '!' || p[j] == '^') {
		j++
	}
	if j < len(p) && p[j] == ']' {
		j++
	}
	for j < len(p) {
		switch p[j] {
		case '\\':
			j += 2
			continue
		case ']':
			return j + 1
		}
		j++
	}
	return j
}

func sat(n int) int {
	if n > MaxExpansions {
		return MaxExpansions + 1
	}
	return n
}
