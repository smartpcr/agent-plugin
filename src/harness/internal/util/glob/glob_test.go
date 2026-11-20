package glob

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

// TestFoldIsSimpleFolding checks FoldCase against its definition: a name rune
// matches a folded pattern exactly when some member of its simple-fold orbit
// matches the pattern as written. Classes keep the membership they were written
// with, ranges and negations included.
func TestFoldIsSimpleFolding(t *testing.T) {
	patterns := []string{
		"k", "\u212a", "\u03c3", "\u0130", "\u0131", "\\K",
		"[\u00df-\u00ff]", "[\u00e0-\u00ff]", "[!\u00e0-\u00ff]", "[^a-z]", "[A-C]", "[a\\-z]",
		"[\\]-\u00e0]", "[-k]", "[k-]", "[\u0400-\u042f]", "[!\u03c3]", "[\u1e00-\u1eff]",
	}
	for _, p := range patterns {
		plain, folded := MustCompile(p), MustCompile(p, FoldCase())
		for r := rune(0); r <= 0x2200; r++ {
			if r == '/' || !utf8.ValidRune(r) {
				continue
			}
			want := false
			for _, o := range orbit(r) {
				want = want || plain.Match(string(o))
			}
			if !strings.HasPrefix(p, "[!") && !strings.HasPrefix(p, "[^") {
				if got := folded.Match(string(r)); got != want {
					t.Fatalf("%q with FoldCase on %U: %v, want %v", p, r, got, want)
				}
				continue
			}
			// A negated class refuses a rune when it lists any orbit member.
			want = true
			for _, o := range orbit(r) {
				want = want && plain.Match(string(o))
			}
			if got := folded.Match(string(r)); got != want {
				t.Fatalf("%q with FoldCase on %U: %v, want %v", p, r, got, want)
			}
		}
	}
	for _, r := range `*?[]{},!^-/\` {
		if len(orbit(r)) != 1 {
			t.Fatalf("glob syntax %q has fold partners", r)
		}
	}
}

// TestDialect is DH-T-PICOMATCH-1 and the stage scenario "Glob meaning is single":
// the answers are a pure function of the two strings, so they are the same on every
// operating system lane.
func TestDialect(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		// The stage scenario table (DH-D-UTIL-4).
		{"**/*.go", "a/b/c.go", true},
		{"*.go", "a/b.go", false},
		{"**/*.go", "main.go", true},
		// `*` stops at `/`; `?` is one character.
		{"*.go", "b.go", true},
		{"a/*.go", "a/b/c.go", false},
		{"a/?.go", "a/b.go", true},
		{"a/?.go", "a/bb.go", false},
		// Dot names match `*` and `**` (R2).
		{"*", ".env", true},
		{"**/.*", "a/.git", true},
		{"**", ".hidden/x", true},
		// `**` is any depth including none.
		{"**/node_modules", "node_modules", true},
		{"**/node_modules", "a/b/node_modules", true},
		{"tests/**", "tests", true},
		{"tests/**", "tests/a/b.ts", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		// `**` not alone in a segment acts as `*`.
		{"a/**b", "a/xb", true},
		{"a/**b", "a/x/b", false},
		// Classes and alternatives, nested.
		{"[a-c].txt", "b.txt", true},
		{"[!a-c].txt", "b.txt", false},
		{"[^a-c].txt", "d.txt", true},
		{"openapi.{json,yaml,yml}", "openapi.yml", true},
		{"openapi.{json,yaml,yml}", "openapi.toml", false},
		{"{src,lib/{a,b}}/*.go", "lib/b/x.go", true},
		{"{src,lib/{a,b}}/*.go", "lib/c/x.go", false},
		{"**/*.d.ts", "types/index.d.ts", true},
		// Addresses are `/`-separated strings; the empty segment is ordinary.
		{"dsh-resource://file/**", "dsh-resource://file/a/b.md", true},
		{"dsh-resource://plan/*/b", "dsh-resource://plan/a/b", true},
		// No extended globs, no negation, no regular expressions: literal text.
		{"@(a|b)", "a", false},
		{"!a", "b", false},
		{"!a", "!a", true},
		// Escapes.
		{`\*.go`, "*.go", true},
		{`\*.go`, "a.go", false},
		// Case is significant without FoldCase; base names only with BaseName.
		{"*.MD", "readme.md", false},
		{"*.md", "a/b/readme.md", false},
	} {
		m, err := Compile(tc.pattern)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.pattern, err)
		}
		if got := m.Match(tc.name); got != tc.want {
			t.Errorf("%q vs %q = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestFromOS(t *testing.T) {
	// FromOS converts this platform's separator only; a `/` path is unchanged.
	if got := FromOS("a/b/c.go"); got != "a/b/c.go" {
		t.Fatalf("FromOS = %q", got)
	}
	if !MustCompile("**/*.go").Match(FromOS("a/b/c.go")) {
		t.Fatal("a converted path does not match")
	}
}

// TestOptions is DH-T-PICOMATCH-2.
func TestOptions(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		opts    []Option
		name    string
		want    bool
	}{
		{"*.md", []Option{BaseName()}, "/a/b/README.MD", false},
		{"*.md", []Option{BaseName(), FoldCase()}, "/a/b/README.MD", true},
		{"*.md", []Option{FoldCase()}, "/a/b/README.MD", false},
		{"*.MD", []Option{FoldCase()}, "readme.md", true},
		{"ÄRGER.txt", []Option{FoldCase()}, "ärger.TXT", true},
		{"[A-C]*", []Option{FoldCase()}, "bravo", true},
		// Unicode simple folding, in both directions: the Kelvin sign is k, long s
		// is s, and final, small and capital sigma are one letter.
		{"\u212a*", []Option{FoldCase()}, "kilo", true},
		{"k*", []Option{FoldCase()}, "\u212ailo", true},
		{"[j-l]ilo", []Option{FoldCase()}, "\u212ailo", true},
		{"\u017fun", []Option{FoldCase()}, "SUN", true},
		{"ΣΟΦΟΣ", []Option{FoldCase()}, "σοφος", true},
		{"σοφος", []Option{FoldCase()}, "ΣΟΦΟς", true},
		{"{ΣΟΦΟΣ,x}.md", []Option{FoldCase()}, "σοφος.MD", true},
		// Dotted and dotless i are not simple-fold partners of ASCII I and i.
		{"I*", []Option{FoldCase()}, "\u0131ndex", false},
		{"\u0131*", []Option{FoldCase()}, "Index", false},
		{"i*", []Option{FoldCase()}, "\u0130ndex", false},
		{"\u0130*", []Option{FoldCase()}, "index", false},
		{"\u0130*", []Option{FoldCase()}, "\u0130NDEX", true},
		{"\u0131*", []Option{FoldCase()}, "\u0131ndex", true},
		// Folding is not transliteration: ß does not fold to ss.
		{"stra\u00dfe", []Option{FoldCase()}, "STRASSE", false},
		{"\u212a*", nil, "kilo", false},
		// A class keeps the membership it was written with: à is inside ß-ÿ;
		// the multiplication sign is not inside à-ÿ though its neighbours fold there.
		{"[\u00df-\u00ff]", []Option{FoldCase()}, "\u00e0", true},
		{"[\u00df-\u00ff]", []Option{FoldCase()}, "\u00c0", true},
		{"[\u00e0-\u00ff]", []Option{FoldCase()}, "\u00d7", false},
		{"[\u00e0-\u00ff]", []Option{FoldCase()}, "\u00c9", true},
		{"[\u00e0-\u00ff]", []Option{FoldCase()}, "\u0178", true},
		// Negated ranges refuse every fold partner of what they list.
		{"[!\u00e0-\u00ff]", []Option{FoldCase()}, "\u00d7", true},
		{"[!\u00e0-\u00ff]", []Option{FoldCase()}, "\u00c0", false},
		{"[^a-z]x", []Option{FoldCase()}, "Kx", false},
		{"[^a-z]x", []Option{FoldCase()}, "\u212ax", false},
		{"[^a-z]x", []Option{FoldCase()}, "1X", true},
		{"[a\\-z]", []Option{FoldCase()}, "Z", true},
		{"[a\\-z]", []Option{FoldCase()}, "m", false},
		// A pattern with a `/` is matched against the whole name even with BaseName.
		{"b/*.md", []Option{BaseName()}, "a/b/x.md", false},
		{"a/b/*.md", []Option{BaseName()}, "a/b/x.md", true},
	} {
		m, err := Compile(tc.pattern, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Match(tc.name); got != tc.want {
			t.Errorf("%q vs %q (%d options) = %v, want %v", tc.pattern, tc.name, len(tc.opts), got, tc.want)
		}
	}
}

func TestSet(t *testing.T) {
	m, err := CompileSet([]string{"src/**/*.go", "*.md"}, []string{"**/*_test.go", "**/testdata/**"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"src/a/b.go":          true,
		"src/a/b_test.go":     false,
		"src/a/testdata/x.go": false,
		"README.md":           true,
		"docs/README.md":      false,
		"src/a/b.txt":         false,
	} {
		if got := m.Match(name); got != want {
			t.Errorf("set vs %q = %v, want %v", name, got, want)
		}
	}
	empty, err := CompileSet(nil, nil)
	if err != nil || empty.Match("a") {
		t.Fatalf("an empty include list must match nothing (%v)", err)
	}
	if _, err := CompileSet([]string{"ok"}, []string{"[bad"}); err == nil {
		t.Fatal("a malformed exclude pattern was accepted")
	}
}

// TestRefused is DH-T-PICOMATCH-3: malformed and over-long patterns are errors.
func TestRefused(t *testing.T) {
	for _, p := range []string{
		"",
		"[a",
		"{a,b",
		`a\`,
		strings.Repeat("a", MaxPatternLen+1),
		strings.Repeat("{a,b}", 11), // 2^11 alternatives
	} {
		_, err := Compile(p)
		var pe *PatternError
		if !errors.As(err, &pe) || pe.Pattern != p || pe.Reason == "" {
			t.Errorf("Compile(%.40q) = %v, want a *PatternError naming it", p, err)
		}
	}
	if _, err := Compile(strings.Repeat("{a,b}", 10)); err != nil {
		t.Fatalf("1024 alternatives are within the bound: %v", err)
	}
}

func TestBoundedWork(t *testing.T) {
	long := strings.Repeat("a", MaxNameLen)
	start := time.Now()
	for _, p := range []string{
		strings.Repeat("*", 200) + "b",
		strings.Repeat("*a", 200) + "b",
		strings.Repeat("**/", 100) + "b",
		strings.Repeat("{a,b}", 10) + "c",
	} {
		if MustCompile(p).Match(long) {
			t.Fatalf("%q matched", p)
		}
	}
	if MustCompile("*").Match(long + "a") {
		t.Fatal("a name over MaxNameLen matched")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("adversarial matching took %v", d)
	}
}

func FuzzCompile(f *testing.F) {
	for _, s := range []string{"**/*.go", "{a,{b,c}}/[!x]?", `\[`, "[]a]", "a/**b", "{,}", "[a-]"} {
		f.Add(s, "a/b/c.go")
	}
	f.Fuzz(func(t *testing.T, p, name string) {
		if len(p) > 128 || len(name) > 256 {
			return
		}
		m, err := Compile(p)
		if err != nil {
			var pe *PatternError
			if !errors.As(err, &pe) {
				t.Fatalf("Compile(%q) returned %T, not *PatternError", p, err)
			}
			return
		}
		m.Match(name)
		if !doublestar.ValidatePattern(foldPattern(p)) {
			t.Fatalf("foldPattern(%q) = %q is not a valid pattern", p, foldPattern(p))
		}
		// Folding only widens a pattern without negated classes.
		negated := strings.Contains(p, "[!") || strings.Contains(p, "[^")
		if !negated && m.Match(name) && !MustCompile(p, FoldCase()).Match(name) {
			t.Fatalf("%q matches %q but not with FoldCase", p, name)
		}
		if f, err := Compile(p, FoldCase(), BaseName()); err == nil {
			f.Match(name)
		}
	})
}
