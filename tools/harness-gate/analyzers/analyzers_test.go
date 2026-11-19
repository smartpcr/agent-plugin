package analyzers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// parse parses src as filename, without type information, as the gate's in-process
// scenarios do (TC-01a).
func parse(t *testing.T, filename, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
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
}
