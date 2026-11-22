package analyzers

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// CordisPackage is the import path of cordis, the one package that may use
// (*Context).Root.
const CordisPackage = "github.com/smartpcr/agent-plugin/src/go/cordis"

// RootCall reports every use of (*cordis.Context).Root outside cordis: a call, a
// method value or a method expression. The root context is cordis's own; everything
// else is handed the context it runs in (DH-ADR-TOOLCHAIN-8, the Go form of the
// upstream cordis-root rule).
var RootCall = &analysis.Analyzer{
	Name: "rootcall",
	Doc:  "forbid (*cordis.Context).Root outside " + CordisPackage,
	Run: func(pass *analysis.Pass) (any, error) {
		if InCordis(pass.Pkg.Path()) {
			return nil, nil
		}
		var findings []Finding
		for _, file := range pass.Files {
			findings = append(findings, RootCallFindings(file, pass.TypesInfo)...)
		}
		report(pass, "rootcall", findings)
		return nil, nil
	},
}

// InCordis reports whether pkgPath is cordis, one of its subpackages or its external
// test package.
func InCordis(pkgPath string) bool {
	return pkgPath == CordisPackage || pkgPath == CordisPackage+"_test" || strings.HasPrefix(pkgPath, CordisPackage+"/")
}

// RootCallFindings is the rootcall predicate: every identifier of file that refers to
// the Root method of cordis.Context. It needs type information; without it there is
// nothing to resolve and no finding.
func RootCallFindings(file *ast.File, info *types.Info) []Finding {
	if info == nil {
		return nil
	}
	var out []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != "Root" {
			return true
		}
		if isCordisRoot(info.Uses[id]) {
			out = append(out, Finding{Pos: id.Pos(), Message: "(*cordis.Context).Root used outside cordis: use the context you are given (DH-ADR-TOOLCHAIN-8)"})
		}
		return true
	})
	return out
}

// isCordisRoot reports whether obj is the method Root of cordis.Context.
func isCordisRoot(obj types.Object) bool {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != CordisPackage {
		return false
	}
	recv := fn.Signature().Recv()
	if recv == nil {
		return false
	}
	t := recv.Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Name() == "Context"
}
