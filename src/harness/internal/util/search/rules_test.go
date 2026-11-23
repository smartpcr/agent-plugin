package search

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestNoProcessNoAmbientConfig: with RIPGREP_CONFIG_PATH naming a configuration
// that would widen the search, and an rg on PATH that records being run, a search
// and a listing give the same results as without them and no process is started;
// no file of the package imports os/exec or starts a process.
//
//dsh:test DH-T-RIPGREP-4
func TestNoProcessNoAmbientConfig(t *testing.T) {
	src := tree(t, fixture)
	run := func() ([]Match, []string) {
		t.Helper()
		listed, _, err := List(context.Background(), FilesQuery{From: src})
		if err != nil {
			t.Fatal(err)
		}
		return grepAll(t, GrepQuery{From: src, Pattern: "needle"}), listed
	}
	wantMatches, wantListed := run()

	bin := t.TempDir()
	marker := filepath.Join(bin, "rg-was-run")
	config := filepath.Join(bin, "ripgreprc")
	if err := os.WriteFile(config, []byte("--no-ignore\n--hidden\n--binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		script := "@echo off\r\necho run> \"" + marker + "\"\r\n"
		for _, name := range []string{"rg.cmd", "rg.bat"} {
			if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	} else if err := os.WriteFile(filepath.Join(bin, "rg"), []byte("#!/bin/sh\necho run > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", config)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	gotMatches, gotListed := run()
	if !reflect.DeepEqual(gotMatches, wantMatches) || !reflect.DeepEqual(gotListed, wantListed) {
		t.Fatalf("results changed with ambient configuration:\n%+v\n%q\nwant\n%+v\n%q", gotMatches, gotListed, wantMatches, wantListed)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the rg on PATH was run")
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" || p == "syscall" {
				t.Errorf("%s imports %s", name, p)
			}
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "StartProcess") || strings.Contains(string(body), "Getenv") {
			t.Errorf("%s starts a process or reads the environment", name)
		}
	}
}

// TestRepeatedRunsSameOrder: the same search and listing over the same files return
// the same results in the same order, run after run and with any worker count.
//
//dsh:test DH-T-RIPGREP-5
func TestRepeatedRunsSameOrder(t *testing.T) {
	files := map[string]string{}
	for i := range 60 {
		files["d"+strconv.Itoa(i%7)+"/f"+strconv.Itoa(i)] = strings.Repeat("hit "+strconv.Itoa(i)+"\nmiss\n", 1+i%4)
	}
	src := tree(t, files)
	var first []Match
	var firstList []string
	for run := range 20 {
		var got []Match
		workers := 1 + run%8
		if _, err := (Local{Workers: workers}).Grep(context.Background(), GrepQuery{From: src, Pattern: "hit"},
			func(m Match) bool { got = append(got, m); return true }); err != nil {
			t.Fatal(err)
		}
		listed, _, err := List(context.Background(), FilesQuery{From: src})
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			first, firstList = got, listed
			if len(first) == 0 || len(firstList) != 60 {
				t.Fatalf("%d matches, %d files", len(first), len(firstList))
			}
			continue
		}
		if !reflect.DeepEqual(got, first) || !reflect.DeepEqual(listed, firstList) {
			t.Fatalf("run %d (workers %d) differs from run 0", run, workers)
		}
	}
}

// TestPatternTable: valid patterns compile and match as Go's RE2 dialect documents;
// invalid ones return the invalid-pattern error with Go's message; the known
// Rust-only forms are listed with what Go does with them (DH-ADR-RIPGREP-2).
//
//dsh:test DH-T-RIPGREP-6
func TestPatternTable(t *testing.T) {
	lines := []string{
		"func Main() {",       // 1
		"x = 42",              // 2
		"naïve café",          // 3
		"digits ٣٤",           // 4: Arabic-Indic digits
		"αβγ greek",           // 5
		"b]",                  // 6
		"b",                   // 7
		"tab\there",           // 8
		"repeat repeat",       // 9
		"Color colour COLOUR", // 10
	}
	src := tree(t, map[string]string{"f": strings.Join(lines, "\n") + "\n"})
	matchLines := func(pattern string) ([]int, error) {
		var got []int
		_, err := Grep(context.Background(), GrepQuery{From: src, Pattern: pattern}, func(m Match) bool {
			got = append(got, m.Line)
			return true
		})
		return got, err
	}
	valid := []struct {
		pattern string
		want    []int
		note    string
	}{
		{`^func \w+\(`, []int{1}, "anchors, classes, escapes"},
		{`\d+`, []int{2}, `Go: \d is ASCII only; Rust's \d also matches line 4`},
		{`\pN+`, []int{2, 4}, "Unicode digits must be asked for by property"},
		{`caf\w`, nil, `Go: \w is ASCII only, so é is not a word character; Rust matches line 3`},
		{`caf\pL`, []int{3}, "a Unicode letter by property"},
		{`\bcafé\b`, nil, `Go: \b is ASCII only, so there is no boundary after é; Rust matches line 3`},
		{`\p{Greek}+`, []int{5}, "Unicode scripts, as in Rust"},
		{`[[:alpha:]]+ greek`, nil, "POSIX classes are ASCII, as in Rust"},
		{`[a-z&&[^aeiou]]`, []int{6}, "Go: no class set operations; this is the class [a-z&[^aeiou] then a literal ], so line 7 does not match; Rust matches lines 6 and 7"},
		{`\t`, []int{8}, "escapes"},
		{`(?i)colou?r`, []int{10}, "flags"},
		{`(?P<w>repeat) repeat`, []int{9}, "named group, Python form"},
		{`(?<w>repeat) repeat`, []int{9}, "named group, the form Rust also accepts"},
	}
	for _, c := range valid {
		got, err := matchLines(c.pattern)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s (%s): lines %v, %v; want %v", c.pattern, c.note, got, err, c.want)
		}
	}
	invalid := []struct{ pattern, msg, note string }{
		{`(?=x)`, "invalid or unsupported Perl syntax", "look-ahead: refused by Rust too"},
		{`(?<!x)y`, "invalid named capture", "look-behind: refused by Rust too"},
		{`(repeat) \1`, "invalid escape sequence", "back-reference: refused by Rust too"},
		{`a{2,1}`, "invalid repeat count", "bad repetition"},
		{`(?x) a b`, "invalid or unsupported Perl syntax", "Rust-only: verbose mode is accepted by Rust, refused by Go"},
		{`\h`, "invalid escape sequence", "Rust-only escape class"},
		{`[`, "missing closing ]", "unclosed class"},
	}
	for _, c := range invalid {
		_, err := matchLines(c.pattern)
		if !IsInvalidPattern(err) || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s (%s): %v; want the invalid-pattern error with %q", c.pattern, c.note, err, c.msg)
		}
	}
}
