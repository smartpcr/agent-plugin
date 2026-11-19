// Package analyzers holds the project's own static checks, run by the vet gate of
// tools/harness-gate through "go vet -vettool" (QG-2; DH-T-TOOLCHAIN-8):
//
//   - nosleep:  no time.Sleep in a _test.go file; tests use virtual time (QG-4)
//   - onespawn: no os/exec import outside internal/world/subprocess (SP-12)
//   - cleanup:  a test that starts a goroutine, listener or child process registers
//     its release with t.Cleanup (QG-5)
//
// Each analyzer is a thin wrapper over a predicate that takes one parsed file, so the
// decision can be tested on an AST parsed from an in-memory string. A predicate uses
// type information when it is given and falls back to import names when it is not.
//
// There is no baseline, no suppression comment and no exemption file: every finding
// is reported, from the first commit (QG-2).
package analyzers

import (
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// All is the fixed list of the project's own analyzers, in the order they report.
var All = []*analysis.Analyzer{NoSleep, OneSpawn, Cleanup}

// Finding is one diagnostic of a predicate.
type Finding struct {
	Pos     token.Pos
	Message string
}

// predicate decides the findings of one file. info may be nil.
type predicate func(filename string, file *ast.File, info *types.Info) []Finding

// newAnalyzer wraps a predicate as an analyzer that reports every finding.
func newAnalyzer(name, doc string, decide predicate) *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: name,
		Doc:  doc,
		Run: func(pass *analysis.Pass) (any, error) {
			for _, file := range pass.Files {
				filename := pass.Fset.File(file.FileStart).Name()
				for _, f := range decide(filename, file, pass.TypesInfo) {
					pass.Report(analysis.Diagnostic{Pos: f.Pos, Message: f.Message})
				}
			}
			return nil, nil
		},
	}
}

// isTestFile reports whether filename is a Go test file.
func isTestFile(filename string) bool {
	return strings.HasSuffix(filename, "_test.go")
}

// importName returns the name file uses for the import of importPath: its explicit
// name ("." for a dot import), or the last element of the path.
func importName(file *ast.File, importPath string) (string, bool) {
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return path.Base(p), true
	}
	return "", false
}

// pkgFunc reports whether expr refers to one of names as a package-level function of
// importPath: a selector "pkg.Name", or "Name" under a dot import. With type
// information the reference is resolved exactly, so a local variable that shadows
// the package name is not mistaken for it.
func pkgFunc(file *ast.File, info *types.Info, expr ast.Expr, importPath string, names ...string) bool {
	var id *ast.Ident
	qualified := false
	switch e := ast.Unparen(expr).(type) {
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok || !isPkgName(file, info, x, importPath) {
			return false
		}
		id, qualified = e.Sel, true
	case *ast.Ident:
		id = e
	default:
		return false
	}
	if !slices.Contains(names, id.Name) {
		return false
	}
	if info != nil {
		obj, ok := info.Uses[id].(*types.Func)
		return ok && obj.Pkg() != nil && obj.Pkg().Path() == importPath
	}
	if qualified {
		return true
	}
	name, ok := importName(file, importPath)
	return ok && name == "."
}

// isPkgName reports whether x names the import of importPath.
func isPkgName(file *ast.File, info *types.Info, x *ast.Ident, importPath string) bool {
	if info != nil {
		pn, ok := info.Uses[x].(*types.PkgName)
		return ok && pn.Imported().Path() == importPath
	}
	name, ok := importName(file, importPath)
	return ok && name == x.Name
}
