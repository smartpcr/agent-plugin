package analyzers

import (
	"go/ast"
	"go/types"
)

// NoSleep reports every reference to time.Sleep in a _test.go file. A test waits on
// virtual time (testing/synctest) or on an explicit signal, never on the wall clock
// (QG-4).
var NoSleep = newAnalyzer("nosleep",
	"forbid time.Sleep in _test.go files; tests use virtual time (testing/synctest)",
	NoSleepFindings)

// NoSleepFindings is the nosleep predicate. A reference that is not called (for
// example "pause := time.Sleep") is reported too, so the rule cannot be sidestepped.
func NoSleepFindings(filename string, file *ast.File, info *types.Info) []Finding {
	if !isTestFile(filename) {
		return nil
	}
	if _, ok := importName(file, "time"); !ok {
		return nil
	}
	const message = "time.Sleep in a test file: wait on virtual time (testing/synctest) or an explicit signal (QG-4)"
	var out []Finding
	selected := map[*ast.Ident]bool{} // the Sel of every selector, judged with its selector
	ast.Inspect(file, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.SelectorExpr:
			selected[e.Sel] = true
			if pkgFunc(file, info, e, "time", "Sleep") {
				out = append(out, Finding{Pos: e.Pos(), Message: message})
			}
		case *ast.Ident:
			if !selected[e] && pkgFunc(file, info, e, "time", "Sleep") {
				out = append(out, Finding{Pos: e.Pos(), Message: message})
			}
		}
		return true
	})
	return out
}
