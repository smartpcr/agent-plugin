package analyzers

import (
	"go/ast"
	"go/types"
)

// TypeAssert reports an unchecked type assertion -- x.(T) whose failure panics --
// and every import of unsafe (DH-ADR-TOOLCHAIN-8, the Go form of the upstream
// no-unknown-casts rule). Assertions are checked in non-test files only (owner
// decision of 2026-11-22): in a test a failed assertion panics inside the test and
// fails it, which is the outcome a checked assertion would produce. An unsafe import
// is reported in every file, test files included: it bypasses the type system
// wherever it is.
var TypeAssert = newAnalyzer("typeassert",
	"forbid unchecked type assertions x.(T) and imports of unsafe",
	TypeAssertFindings)

// TypeAssertFindings is the typeassert predicate; it needs no type information. An
// assertion is checked when it is the single value of a two-value assignment or
// declaration ("v, ok := x.(T)", "v, ok = x.(T)", "var v, ok = x.(T)"); the x.(type)
// of a type switch is not an assertion. Every other x.(T) is reported, and so is
// every import of unsafe. In a _test.go file only the unsafe import is reported.
func TypeAssertFindings(filename string, file *ast.File, _ *types.Info) []Finding {
	var out []Finding
	for _, imp := range file.Imports {
		if imp.Path.Value == `"unsafe"` {
			out = append(out, Finding{Pos: imp.Pos(), Message: "unsafe imported: write //lint:ignore typeassert <reason> naming the API that needs it (DH-ADR-TOOLCHAIN-8)"})
		}
	}
	if isTestFile(filename) {
		return out
	}
	checked := map[*ast.TypeAssertExpr]bool{}
	commaOK := func(nlhs int, rhs []ast.Expr) {
		if nlhs == 2 && len(rhs) == 1 {
			if ta, ok := ast.Unparen(rhs[0]).(*ast.TypeAssertExpr); ok {
				checked[ta] = true
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			commaOK(len(s.Lhs), s.Rhs)
		case *ast.ValueSpec:
			commaOK(len(s.Names), s.Values)
		case *ast.TypeAssertExpr:
			if s.Type != nil && !checked[s] {
				out = append(out, Finding{Pos: s.Pos(), Message: "unchecked type assertion to " + types.ExprString(s.Type) +
					": use v, ok := x.(T) or a type switch, or write //lint:ignore typeassert <reason> (DH-ADR-TOOLCHAIN-8)"})
			}
		}
		return true
	})
	return out
}
