package search

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// tree writes files (slash paths to contents) under a new temp directory and
// returns a Source over it.
func tree(t *testing.T, files map[string]string) *DirSource {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

var fixture = map[string]string{
	".gitignore":        "build/\n*.log\n!keep.log\n",
	".git/config":       "[core]\n",
	".git/info/exclude": "secret.txt\n",
	".hidden/h.txt":     "needle\n",
	"README.md":         "needle in the readme\n",
	"build/out.txt":     "needle\n",
	"build/sub/x.txt":   "needle\n",
	"docs/.gitignore":   "draft*\n",
	"docs/draft1.md":    "needle\n",
	"docs/guide.md":     "a\nneedle\nb\n",
	"secret.txt":        "needle\n",
	"src/app.log":       "needle\n",
	"src/build/gen.go":  "needle\n",
	"src/keep.log":      "needle\n",
	"src/main.go":       "package main\n\n// needle\nfunc main() {}\n",
	"src/zz.bin":        "needle\x00binary\n",
}

// TestListGolden: a .gitignore excluding build/ keeps every path under build/ (at
// any depth) out of the listing, and the listing equals the committed golden.
func TestListGolden(t *testing.T) {
	src := tree(t, fixture)
	got, sum, err := List(context.Background(), FilesQuery{From: src})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if strings.HasPrefix(p, "build/") || strings.Contains(p, "/build/") {
			t.Errorf("ignored path listed: %s", p)
		}
	}
	if sum.Incomplete || sum.Matches != len(got) {
		t.Fatalf("summary %+v for %d paths", sum, len(got))
	}
	golden.Assert(t, "list", strings.Join(got, "\n")+"\n")
}

func TestListSwitches(t *testing.T) {
	src := tree(t, fixture)
	ctx := context.Background()
	all, _, err := List(ctx, FilesQuery{From: src, Hidden: true, NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".gitignore", ".hidden/h.txt", "README.md", "build/out.txt", "build/sub/x.txt",
		"docs/.gitignore", "docs/draft1.md", "docs/guide.md", "secret.txt",
		"src/app.log", "src/build/gen.go", "src/keep.log", "src/main.go", "src/zz.bin",
	}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("everything but .git:\n%q\nwant\n%q", all, want)
	}

	cases := []struct {
		name string
		q    FilesQuery
		want []string
	}{
		{"glob by base name", FilesQuery{Pattern: "*.go", NoIgnore: true}, []string{"src/build/gen.go", "src/main.go"}},
		{"glob by path", FilesQuery{Pattern: "src/*.log"}, []string{"src/keep.log"}},
		{"sub root", FilesQuery{Root: "src"}, []string{"src/keep.log", "src/main.go", "src/zz.bin"}},
		{"sub root glob is relative", FilesQuery{Root: "docs", Pattern: "*.md"}, []string{"docs/guide.md"}},
		{"file root", FilesQuery{Root: "src/main.go"}, []string{"src/main.go"}},
		{"exclude", FilesQuery{Exclude: []string{"src", "*.md"}}, nil},
		{"max matches", FilesQuery{Limits: Limits{MaxMatches: 2}}, []string{"README.md", "docs/guide.md"}},
		{"max files", FilesQuery{Limits: Limits{MaxFiles: 1}}, []string{"README.md"}},
	}
	for _, c := range cases {
		c.q.From = src
		got, _, err := List(ctx, c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	_, sum, _ := List(ctx, FilesQuery{From: src, Limits: Limits{MaxMatches: 2}})
	if !sum.Incomplete || sum.Reason != CutMatches {
		t.Errorf("matches cap: %+v", sum)
	}
	_, sum, _ = List(ctx, FilesQuery{From: src, Limits: Limits{MaxFiles: 1}})
	if !sum.Incomplete || sum.Reason != CutFiles {
		t.Errorf("files cap: %+v", sum)
	}
	_, sum, _ = List(ctx, FilesQuery{From: src, Limits: Limits{MaxMatches: 50}})
	if sum.Incomplete {
		t.Errorf("a cap that was not reached: %+v", sum)
	}
	sum, _ = Files(ctx, FilesQuery{From: src}, func(string) bool { return false })
	if sum.Reason != CutStopped || sum.Matches != 1 {
		t.Errorf("caller stop: %+v", sum)
	}
	if _, _, err := List(ctx, FilesQuery{From: src, Pattern: "[a"}); !IsInvalidPattern(err) {
		t.Errorf("bad glob: %v", err)
	}
	var e *Error
	if _, _, err := List(ctx, FilesQuery{From: src, Root: "missing"}); !errors.As(err, &e) || e.Reason() != ReasonFailed {
		t.Errorf("missing root: %v", err)
	}
}

func TestListByTime(t *testing.T) {
	src := tree(t, map[string]string{"a": "", "b": "", "c": "", "d": ""})
	base := time.Now().Add(-time.Hour)
	for name, age := range map[string]time.Duration{"a": 3, "b": 1, "c": 2, "d": 1} {
		mod := base.Add(-age * time.Minute)
		if err := os.Chtimes(filepath.Join(src.root.Name(), name), mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := List(context.Background(), FilesQuery{From: src, Order: ByTime})
	if err != nil || !reflect.DeepEqual(got, []string{"b", "d", "c", "a"}) {
		t.Fatalf("by time: %q %v", got, err)
	}
	got, sum, _ := List(context.Background(), FilesQuery{From: src, Order: ByTime, Limits: Limits{MaxMatches: 1}})
	if !reflect.DeepEqual(got, []string{"b"}) || sum.Reason != CutMatches {
		t.Fatalf("by time capped: %q %+v", got, sum)
	}
	sum, _ = Files(context.Background(), FilesQuery{From: src, Order: ByTime}, func(string) bool { return false })
	if sum.Reason != CutStopped {
		t.Fatalf("by time stopped: %+v", sum)
	}
}

// TestIgnoreRules is the rule table: nesting, negation, directory-only, anchored,
// "**", escapes, and .ignore over .gitignore.
func TestIgnoreRules(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"anchored", map[string]string{".gitignore": "/a.txt\n", "a.txt": "", "d/a.txt": ""}, []string{"d/a.txt"}},
		{"slash in middle anchors", map[string]string{".gitignore": "d/a.txt\n", "d/a.txt": "", "e/d/a.txt": ""}, []string{"e/d/a.txt"}},
		{"dir only", map[string]string{".gitignore": "out/\n", "out/x": "", "f/out": ""}, []string{"f/out"}},
		{"negation last wins", map[string]string{".gitignore": "*.txt\n!b.txt\n", "a.txt": "", "b.txt": ""}, []string{"b.txt"}},
		{"negation then ignore", map[string]string{".gitignore": "!b.txt\n*.txt\n", "b.txt": "", "c": ""}, []string{"c"}},
		{"deeper file wins", map[string]string{".gitignore": "*.txt\n", "d/.gitignore": "!keep.txt\n", "d/keep.txt": "", "d/x.txt": ""}, []string{"d/keep.txt"}},
		{"ignored dir cannot be re-included", map[string]string{".gitignore": "d/\n!d/keep\n", "d/keep": "", "e": ""}, []string{"e"}},
		{"double star", map[string]string{".gitignore": "a/**/z\n", "a/z": "", "a/b/c/z": "", "b/z": ""}, []string{"b/z"}},
		{"inside only", map[string]string{".gitignore": "a/**\n!a/keep\n", "a/keep": "", "a/x": ""}, []string{"a/keep"}},
		{".ignore over .gitignore", map[string]string{".gitignore": "*.log\n", ".ignore": "!app.log\n", "app.log": "", "x.log": ""}, []string{"app.log"}},
		{"comments, blanks, escapes", map[string]string{".gitignore": "# c\n\n\\#h\n\\!bang\ntrail  \n{a,b}\n", "#h": "", "!bang": "", "trail": "", "{a,b}": "", "a": ""}, []string{"a"}},
		{"crlf ignore file", map[string]string{".gitignore": "x\r\ny\r\n", "x": "", "y": "", "z": ""}, []string{"z"}},
		{"bad pattern skipped", map[string]string{".gitignore": "[\nb\n", "[": "", "b": ""}, []string{"["}},
	}
	for _, c := range cases {
		src := tree(t, c.files)
		got, _, err := List(context.Background(), FilesQuery{From: src})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestIgnoreAboveRoot: the ignore files of the directories above a sub-root apply.
func TestIgnoreAboveRoot(t *testing.T) {
	src := tree(t, map[string]string{".gitignore": "*.tmp\n", "a/.gitignore": "x\n", "a/b/x": "", "a/b/y.tmp": "", "a/b/z": ""})
	got, _, err := List(context.Background(), FilesQuery{From: src, Root: "a/b"})
	if err != nil || !reflect.DeepEqual(got, []string{"a/b/z"}) {
		t.Fatalf("%q %v", got, err)
	}
}

func TestGrep(t *testing.T) {
	src := tree(t, fixture)
	got := grepAll(t, GrepQuery{From: src, Pattern: "needle"})
	want := []Match{
		{Path: "README.md", Line: 1, Text: "needle in the readme"},
		{Path: "docs/guide.md", Line: 2, Text: "needle"},
		{Path: "src/keep.log", Line: 1, Text: "needle"},
		{Path: "src/main.go", Line: 3, Text: "// needle"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grep:\n%+v\nwant\n%+v", got, want)
	}
	if got := grepAll(t, GrepQuery{From: src, Pattern: "needle", Include: "*.go"}); len(got) != 1 || got[0].Path != "src/main.go" {
		t.Fatalf("include: %+v", got)
	}
	if got := grepAll(t, GrepQuery{From: src, Pattern: "needle", Root: "docs/guide.md"}); len(got) != 1 || got[0].Line != 2 {
		t.Fatalf("file root: %+v", got)
	}
	if got := grepAll(t, GrepQuery{From: src, Pattern: "(?i)NEEDLE in", Hidden: true, NoIgnore: true}); len(got) != 1 {
		t.Fatalf("fold case: %+v", got)
	}
	if got := grepAll(t, GrepQuery{From: src, Pattern: "needle", Hidden: true, NoIgnore: true}); len(got) != 11 {
		t.Fatalf("everything but .git and binary: %d %+v", len(got), got)
	}
}

func grepAll(t *testing.T, q GrepQuery) []Match {
	t.Helper()
	var got []Match
	if _, err := Grep(context.Background(), q, func(m Match) bool { got = append(got, m); return true }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGrepLines(t *testing.T) {
	long := strings.Repeat("é", 20) // 40 bytes
	src := tree(t, map[string]string{
		"crlf":    "x hit\r\ny\r\nhit\r\n",
		"invalid": "hit \xff\xfe end\n",
		"long":    "hit " + long + "\n",
		"nofinal": "a\nhit",
		"huge":    strings.Repeat("a", 70<<10) + "hit\nhit\n",
	})
	got := grepAll(t, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxLineBytes: 9}})
	want := []Match{
		{Path: "crlf", Line: 1, Text: "x hit"},
		{Path: "crlf", Line: 3, Text: "hit"},
		{Path: "huge", Line: 1, Text: "aaaaaaaaa", Truncated: true},
		{Path: "huge", Line: 2, Text: "hit"},
		{Path: "invalid", Line: 1, Text: "hit \uFFFD en", Truncated: true},
		{Path: "long", Line: 1, Text: "hit éé", Truncated: true},
		{Path: "nofinal", Line: 2, Text: "hit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines:\n%+v\nwant\n%+v", got, want)
	}
	if got := grepAll(t, GrepQuery{From: src, Pattern: "end$", Limits: Limits{MaxLineBytes: -1}}); len(got) != 1 || got[0].Text != "hit \uFFFD end" {
		t.Fatalf("placeholder: %+v", got)
	}
}

func TestGrepLimits(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files[n] = strings.Repeat("hit\n", 10)
	}
	src := tree(t, files)
	ctx := context.Background()

	for _, workers := range []int{1, 4} {
		var got []Match
		sum, err := Local{Workers: workers}.Grep(ctx, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxMatches: 25}},
			func(m Match) bool { got = append(got, m); return true })
		if err != nil || len(got) != 25 || sum.Reason != CutMatches || got[24].Path != "c" || got[24].Line != 5 {
			t.Fatalf("workers %d: %d matches, %+v, %v", workers, len(got), sum, err)
		}
	}
	sum, err := Grep(ctx, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxMatches: 5}}, func(Match) bool { return true })
	if err != nil || sum.Reason != CutMatches || sum.Matches != 5 {
		t.Fatalf("cap inside the first file: %+v %v", sum, err)
	}
	sum, err = Grep(ctx, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxMatches: 50}}, func(Match) bool { return true })
	if err != nil || sum.Incomplete || sum.Matches != 50 || sum.Files != 5 {
		t.Fatalf("cap exactly reached by the last file: %+v %v", sum, err)
	}
	sum, _ = Grep(ctx, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxMatches: -1, MaxFiles: 2}}, func(Match) bool { return true })
	if sum.Reason != CutFiles || sum.Matches != 20 {
		t.Fatalf("files cap: %+v", sum)
	}
	sum, _ = Grep(ctx, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxMatches: -1, MaxBytes: 60}}, func(Match) bool { return true })
	if sum.Reason != CutBytes || sum.Matches >= 50 {
		t.Fatalf("bytes cap: %+v", sum)
	}
	sum, _ = Grep(ctx, GrepQuery{From: src, Pattern: "hit"}, func(Match) bool { return false })
	if sum.Reason != CutStopped || sum.Matches != 1 {
		t.Fatalf("caller stop: %+v", sum)
	}
}

func TestGrepErrors(t *testing.T) {
	src := tree(t, map[string]string{"a": "x\n"})
	ctx := context.Background()
	for _, q := range []GrepQuery{{Pattern: "(?=x)"}, {Pattern: "a{2,1}"}, {Pattern: "x", Include: "[z"}} {
		q.From = src
		_, err := Grep(ctx, q, func(Match) bool { return true })
		if !IsInvalidPattern(err) || !strings.Contains(err.Error(), ReasonInvalidPattern) {
			t.Errorf("%+v: %v", q, err)
		}
	}
	var e *Error
	if _, err := Grep(ctx, GrepQuery{From: src, Pattern: "x", Root: "nope"}, func(Match) bool { return true }); !errors.As(err, &e) || e.Reason() != ReasonFailed || IsInvalidPattern(err) {
		t.Errorf("missing root: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	sum, err := Grep(cancelled, GrepQuery{From: src, Pattern: "x"}, func(Match) bool { return true })
	if !errors.As(err, &e) || e.Reason() != ReasonAborted || !errors.Is(err, context.Canceled) || sum.Reason != CutAborted {
		t.Errorf("cancelled: %+v %v", sum, err)
	}
	if _, _, err := List(cancelled, FilesQuery{From: src}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled list: %v", err)
	}
}

// TestGrepCancelPromptly: cancelling mid-search returns promptly with the aborted
// error, even with a large tree left.
func TestGrepCancelPromptly(t *testing.T) {
	files := map[string]string{}
	for i := range 300 {
		files["d/"+strings.Repeat("f", 1+i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("hit\n", 200)
	}
	src := tree(t, files)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	start := time.Now()
	sum, err := Grep(ctx, GrepQuery{From: src, Pattern: "hit", Limits: Limits{MaxMatches: -1}}, func(Match) bool {
		if n++; n == 10 {
			cancel()
		}
		return true
	})
	if !errors.Is(err, context.Canceled) || !sum.Incomplete || time.Since(start) > 10*time.Second {
		t.Fatalf("%+v %v after %v", sum, err, time.Since(start))
	}
}

// failing is a Source whose named entries cannot be read.
type failing struct {
	Source
	bad map[string]bool
}

func (f failing) ReadDir(name string) ([]fs.DirEntry, error) {
	if f.bad[name] {
		return nil, fs.ErrPermission
	}
	return f.Source.ReadDir(name)
}

func (f failing) Open(name string) (io.ReadCloser, error) {
	if f.bad[name] {
		return nil, fs.ErrPermission
	}
	return f.Source.Open(name)
}

func TestUnreadableIsIncomplete(t *testing.T) {
	src := failing{tree(t, map[string]string{"a/x": "hit\n", "b": "hit\n", "c": "hit\n"}), map[string]bool{"a": true, "b": true}}
	got, sum, err := List(context.Background(), FilesQuery{From: src})
	if err != nil || !reflect.DeepEqual(got, []string{"b", "c"}) || sum.Reason != CutUnreadable {
		t.Fatalf("list: %q %+v %v", got, sum, err)
	}
	var ms []Match
	sum, err = Grep(context.Background(), GrepQuery{From: src, Pattern: "hit"}, func(m Match) bool { ms = append(ms, m); return true })
	if err != nil || len(ms) != 1 || ms[0].Path != "c" || sum.Reason != CutUnreadable {
		t.Fatalf("grep: %+v %+v %v", ms, sum, err)
	}
}

// TestConfinedSource: no name reaches outside the DirSource's directory.
func TestConfinedSource(t *testing.T) {
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "secret"), []byte("hit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "ws")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := OpenDir(inner)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := Grep(context.Background(), GrepQuery{From: src, Pattern: "hit", Root: "../secret"}, func(Match) bool { return true }); err == nil {
		t.Fatal("a root outside the source was searched")
	}
	if _, err := OpenDir(filepath.Join(outer, "missing")); err == nil {
		t.Fatal("OpenDir of a missing directory")
	}
}

func TestRequiredLiteral(t *testing.T) {
	for pattern, want := range map[string]string{
		"needle":        "needle",
		`foo\d+barbaz`:  "barbaz",
		"(?i)needle":    "",
		"a|b":           "",
		"[":             "",
		`^func \w+\(`:   "func ",
		`x.*longer one`: "longer one",
	} {
		if got := string(requiredLiteral(pattern)); got != want {
			t.Errorf("%q: %q, want %q", pattern, got, want)
		}
	}
}
