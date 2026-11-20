package analyzers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// parse parses src as filename, without type information, as the gate's in-process
// scenarios do (TC-01a).
func parse(t *testing.T, filename, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	return fset, file
}

func TestNoSleepFlagsSleepInTestFile(t *testing.T) {
	const src = `package x

import (
	"testing"
	"time"
)

func TestX(t *testing.T){ time.Sleep(1) }
`
	fset, file := parse(t, "x_test.go", src)
	got := NoSleepFindings("x_test.go", file, nil)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(got), got)
	}
	var call *ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && call == nil {
			call = c
		}
		return true
	})
	if got[0].Pos != call.Pos() {
		t.Errorf("finding at %v, want the call at %v", fset.Position(got[0].Pos), fset.Position(call.Pos()))
	}

	if got := NoSleepFindings("x.go", file, nil); len(got) != 0 {
		t.Errorf("non-test file: got %d findings, want 0", len(got))
	}
}

func TestPredicatesOnInMemorySource(t *testing.T) {
	tests := []struct {
		name     string
		decide   predicate
		filename string
		src      string
		want     int
	}{
		{"nosleep", NoSleepFindings, "x_test.go", "package x\nimport (\"testing\"; \"time\")\nfunc TestX(t *testing.T){ time.Sleep(1) }", 1},
		{"nosleep dot import", NoSleepFindings, "x_test.go", "package x\nimport (\"testing\"; . \"time\")\nfunc TestX(t *testing.T){ Sleep(1) }", 1},
		{"nosleep no time import", NoSleepFindings, "x_test.go", "package x\nfunc TestX(){ time.Sleep(1) }", 0},
		{"onespawn outside", OneSpawnFindings, "internal/tools/a.go", "package tools\nimport \"os/exec\"", 1},
		{"onespawn inside", OneSpawnFindings, "internal/world/subprocess/a.go", "package subprocess\nimport \"os/exec\"", 0},
		{"onespawn windows path", OneSpawnFindings, `C:\repo\src\harness\internal\world\subprocess\a.go`, "package subprocess\nimport \"os/exec\"", 0},
		{"onespawn windows path outside", OneSpawnFindings, `C:\repo\src\harness\internal\tools\a.go`, "package tools\nimport \"os/exec\"", 1},
		{"onespawn lookalike", OneSpawnFindings, "internal/world/subprocessx/a.go", "package subprocessx\nimport \"os/exec\"", 1},
		{"cleanup missing", CleanupFindings, "y_test.go", "package y\nimport \"testing\"\nfunc TestY(t *testing.T){ go func(){}() }", 1},
		{"cleanup registered", CleanupFindings, "y_test.go", "package y\nimport \"testing\"\nfunc TestY(t *testing.T){ t.Cleanup(func(){}); go func(){}() }", 0},
		{"cleanup other receiver", CleanupFindings, "y_test.go", "package y\nimport \"testing\"\nfunc TestY(t *testing.T){ u := t; u.Cleanup(func(){}); go func(){}() }", 1},
		{"cleanup listener", CleanupFindings, "y_test.go", "package y\nimport (\"net\"; \"testing\")\nfunc TestY(t *testing.T){ net.Listen(\"tcp\", \":0\") }", 1},
		{"cleanup child", CleanupFindings, "y_test.go", "package y\nimport (\"os/exec\"; \"testing\")\nfunc TestY(t *testing.T){ exec.Command(\"go\").Start() }", 1},
		{"cleanup non-test file", CleanupFindings, "y.go", "package y\nimport \"testing\"\nfunc TestY(t *testing.T){ go func(){}() }", 0},
		{"cleanup not a test", CleanupFindings, "y_test.go", "package y\nfunc helper(){ go func(){}() }", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, file := parse(t, tt.filename, tt.src)
			if got := tt.decide(tt.filename, file, nil); len(got) != tt.want {
				t.Errorf("got %d findings, want %d: %v", len(got), tt.want, got)
			}
		})
	}
}

// TestAnalyzersOnFixtures loads the committed fixture packages through the
// toolchain's package loader and checks every "// want" diagnostic, and no extra
// one (TC-01b).
func TestAnalyzersOnFixtures(t *testing.T) {
	dir := analysistest.TestData()
	analysistest.Run(t, dir, NoSleep, "nosleep")
	analysistest.Run(t, dir, OneSpawn, "onespawn", "internal/world/subprocess")
	analysistest.Run(t, dir, Cleanup, "cleanup")
	analysistest.Run(t, dir, FuzzCheck, "fuzzcheck/...")
}

// parsePackage parses the in-memory files of one package, in the order given.
func parsePackage(t *testing.T, files ...string) []PackageFile {
	t.Helper()
	var out []PackageFile
	for i := 0; i < len(files); i += 2 {
		_, f := parse(t, files[i], files[i+1])
		out = append(out, PackageFile{Name: files[i], File: f})
	}
	return out
}

const (
	markedParser = "// Package p decodes frames from the network.\n//\n//dsh:untrusted-parser frames\npackage p\n\nfunc Decode(b []byte) error { return nil }\n"
	plainTest    = "package p\n\nimport \"testing\"\n\nfunc TestDecode(t *testing.T) {}\n"
)

// TestFuzzCheckMissingTargetIsReported is the stage scenario: a marked package without
// a Fuzz* function has exactly one finding, at the marker; adding
// func FuzzDecode(f *testing.F), seeded in seeds_test.go, clears it.
func TestFuzzCheckMissingTargetIsReported(t *testing.T) {
	fset, _ := parse(t, "p.go", markedParser)
	pkg := parsePackage(t, "p.go", markedParser, "p_test.go", plainTest)
	got := FuzzCheckFindings(pkg)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(got), got)
	}
	if pos := fset.Position(got[0].Pos); pos.Line != 3 || pos.Column != 1 {
		t.Errorf("finding at %v, want the marker at 3:1", pos)
	}
	if !strings.Contains(got[0].Message, "no fuzz target") {
		t.Errorf("message = %q", got[0].Message)
	}

	const seeds = "package p\n\nimport \"testing\"\n\nvar decodeSeeds = [][]byte{[]byte(\"{}\"), []byte(\"[\")}\n\n" +
		"func FuzzDecode(f *testing.F) {\n\tfor _, s := range decodeSeeds {\n\t\tf.Add(s)\n\t}\n\tf.Fuzz(func(t *testing.T, b []byte) { Decode(b) })\n}\n"
	pkg = parsePackage(t, "p.go", markedParser, "p_test.go", plainTest, "seeds_test.go", seeds)
	if got := FuzzCheckFindings(pkg); len(got) != 0 {
		t.Errorf("with FuzzDecode: got %v, want no finding", got)
	}
}

func TestFuzzCheckRules(t *testing.T) {
	const (
		fuzzNoAdd      = "package p\nimport \"testing\"\nfunc FuzzDecode(f *testing.F) { f.Fuzz(func(t *testing.T, b []byte) {}) }\n"
		fuzzSeeded     = "package p\nimport \"testing\"\nfunc FuzzDecode(f *testing.F) { f.Add([]byte(\"x\")) }\n"
		fuzzViaHelper  = "package p\nimport \"testing\"\nfunc FuzzDecode(f *testing.F) { addDecodeSeeds(f) }\n"
		helperSeeds    = "package p\nimport (_ \"embed\"; \"testing\")\n//go:embed testdata/seed.bin\nvar seed []byte\nfunc addDecodeSeeds(f *testing.F) { f.Add(seed) }\n"
		helperNoAdd    = "package p\nimport \"testing\"\nfunc addDecodeSeeds(f *testing.F) {}\n"
		seedsReadsDisk = "package p\nimport (\"os\"; \"testing\")\nfunc FuzzDecode(f *testing.F) { b, _ := os.ReadFile(\"seed\"); f.Add(b) }\n"
		seedsIoutil    = "package p\nimport (\"io/ioutil\"; \"testing\")\nfunc FuzzDecode(f *testing.F) { b, _ := ioutil.ReadFile(\"seed\"); f.Add(b) }\n"
		dotTesting     = "package p\nimport . \"testing\"\nfunc FuzzDecode(f *F) { f.Add(1) }\n"
		notFuzzSig     = "package p\nimport \"testing\"\nfunc FuzzDecode(t *testing.T) {}\nfunc Fuzzy(f *testing.F) {}\nfunc FuzzTwo(f, g *testing.F) {}\n"
		unnamedParam   = "package p\nimport \"testing\"\nfunc FuzzDecode(*testing.F) {}\n"
		blankParam     = "package p\nimport \"testing\"\nfunc FuzzDecode(_ *testing.F) { addDecodeSeeds(_) }\n"
		otherAdd       = "package p\nimport \"testing\"\nfunc FuzzDecode(f *testing.F) { var g *testing.F; g.Add(1) }\n"
		unmarked       = "package p\n\n// dsh:untrusted-parser is not the marker; neither is //dsh:untrusted-parsers\n//dsh:untrusted-parsers\nfunc Decode() {}\n"
		markerInTest   = "package p\n//dsh:untrusted-parser\nimport \"testing\"\nfunc TestX(t *testing.T) {}\n"
	)
	tests := []struct {
		name  string
		files []string
		want  []string // a substring of each finding, in order
	}{
		{"unmarked package", []string{"p.go", unmarked, "p_test.go", plainTest}, nil},
		{"marker only in a test file", []string{"p.go", "package p\n", "p_test.go", markerInTest}, nil},
		{"no test files", []string{"p.go", markedParser}, []string{"no fuzz target"}},
		{"wrong signatures are not targets", []string{"p.go", markedParser, "seeds_test.go", notFuzzSig}, []string{"no fuzz target"}},
		{"no seeds file", []string{"p.go", markedParser, "fuzz_test.go", fuzzSeeded}, []string{"has no seeds_test.go"}},
		{"seeded in seeds_test.go", []string{"p.go", markedParser, "seeds_test.go", fuzzSeeded}, nil},
		{"seeds file in a directory path", []string{`C:\m\p\p.go`, markedParser, `C:\m\p\seeds_test.go`, fuzzSeeded}, nil},
		{"seeded through a seeds_test.go helper", []string{"p.go", markedParser, "fuzz_test.go", fuzzViaHelper, "seeds_test.go", helperSeeds}, nil},
		{"helper adds nothing", []string{"p.go", markedParser, "fuzz_test.go", fuzzViaHelper, "seeds_test.go", helperNoAdd}, []string{"FuzzDecode registers no seeds"}},
		{"target adds outside seeds_test.go", []string{"p.go", markedParser, "fuzz_test.go", fuzzSeeded, "seeds_test.go", helperSeeds}, []string{"FuzzDecode registers no seeds"}},
		{"target never calls Add", []string{"p.go", markedParser, "seeds_test.go", fuzzNoAdd}, []string{"FuzzDecode registers no seeds"}},
		{"Add on another F", []string{"p.go", markedParser, "seeds_test.go", otherAdd}, []string{"FuzzDecode registers no seeds"}},
		{"unnamed parameter", []string{"p.go", markedParser, "seeds_test.go", unnamedParam}, []string{"FuzzDecode registers no seeds"}},
		{"blank parameter", []string{"p.go", markedParser, "fuzz_test.go", blankParam, "seeds_test.go", helperSeeds}, []string{"FuzzDecode registers no seeds"}},
		{"seeds read from disk", []string{"p.go", markedParser, "seeds_test.go", seedsReadsDisk}, []string{"seeds_test.go imports os"}},
		{"seeds read with ioutil", []string{"p.go", markedParser, "seeds_test.go", seedsIoutil}, []string{"seeds_test.go imports io/ioutil"}},
		{"dot-imported testing", []string{"p.go", markedParser, "seeds_test.go", dotTesting}, nil},
		{"bare Fuzz name", []string{"p.go", markedParser, "seeds_test.go", strings.Replace(fuzzSeeded, "FuzzDecode", "Fuzz", 1)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FuzzCheckFindings(parsePackage(t, tt.files...))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d findings, want %d: %v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if !strings.Contains(got[i].Message, w) {
					t.Errorf("finding %d = %q, want it to contain %q", i, got[i].Message, w)
				}
			}
		})
	}
}
