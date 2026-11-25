// Package analyzers holds the project's own static checks, run by the vet gate of
// tools/harness-gate through "go vet -vettool" (QG-2; DH-T-TOOLCHAIN-8):
//
//   - nosleep:  no time.Sleep in a _test.go file; tests use virtual time (QG-4)
//   - onespawn: no os/exec import outside internal/world/subprocess (SP-12), and
//     no subprocess.Spec literal without Launch outside tests (DH-D-EXEC-4)
//   - cleanup:  a test that starts a goroutine, listener or child process registers
//     its release with t.Cleanup (QG-5)
//   - fuzzcheck: a package marked //dsh:untrusted-parser has a Fuzz* target whose
//     seeds are registered from seeds_test.go (QG-6, DH-ADR-TOOLCHAIN-3)
//   - rootcall: no use of (*cordis.Context).Root outside src/go/cordis
//     (DH-ADR-TOOLCHAIN-8)
//   - typeassert: no unchecked x.(T) in non-test code, no unsafe import
//     (DH-ADR-TOOLCHAIN-8)
//   - lintignore: every //lint:ignore names a known check and gives a reason
//     (DH-ADR-TOOLCHAIN-8); see directive.go
//
// Pending holds the analyzers that are built but not yet in All: errdiscard
// (discarded error results). An analyzer joins All only in the change that leaves
// the tree clean under it (DH-ADR-TOOLCHAIN-8).
//
// Each analyzer is a thin wrapper over a predicate that takes one parsed file (one
// package's files for fuzzcheck), so the decision can be tested on ASTs parsed from
// in-memory strings. A predicate uses type information when it is given and falls
// back to import names when it is not.
//
// There is no baseline and no exemption file: every finding is reported, from the
// first commit (QG-2). The one way to accept a finding is a //lint:ignore directive
// at its site that names the check and gives a reason (directive.go); the vet gate
// prints how many there are.
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

// All is the fixed list of the project's own analyzers, in the order they report:
// the ones the vet gate runs.
var All = []*analysis.Analyzer{NoSleep, OneSpawn, Cleanup, FuzzCheck, RootCall, TypeAssert, LintIgnore}

// Pending are the analyzers that are built and tested but not yet in All, because the
// tree is not clean under them yet (DH-ADR-TOOLCHAIN-8; plan stage 13.5).
var Pending = []*analysis.Analyzer{ErrDiscard}

// Finding is one diagnostic of a predicate.
type Finding struct {
	Pos     token.Pos
	Message string
}

// predicate decides the findings of one file. info may be nil.
type predicate func(filename string, file *ast.File, info *types.Info) []Finding

// newAnalyzer wraps a predicate as an analyzer that reports every finding an
// accepted //lint:ignore at its site does not suppress.
func newAnalyzer(name, doc string, decide predicate) *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: name,
		Doc:  doc,
		Run: func(pass *analysis.Pass) (any, error) {
			var findings []Finding
			for _, file := range pass.Files {
				filename := pass.Fset.File(file.FileStart).Name()
				findings = append(findings, decide(filename, file, pass.TypesInfo)...)
			}
			report(pass, name, findings)
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
