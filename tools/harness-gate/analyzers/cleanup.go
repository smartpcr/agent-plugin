package analyzers

import (
	"go/ast"
	"go/types"
)

// Cleanup reports a goroutine, listener or child process started in a test function
// that never registers a release with t.Cleanup (QG-5). A test function is any
// function or function literal in a _test.go file with a *testing.T, *testing.B,
// *testing.F or testing.TB parameter; a nested one (a t.Run body, a helper taking
// the test) is judged on its own.
var Cleanup = newAnalyzer("cleanup",
	"require t.Cleanup in a test that starts a goroutine, listener or child process (QG-5)",
	CleanupFindings)

// netListen and httptestServer name the functions that open a listener (or a
// server) that outlives the call.
var (
	netListen      = []string{"Listen", "ListenPacket", "ListenTCP", "ListenUDP", "ListenUnix", "ListenUnixgram", "ListenIP", "ListenMulticastUDP"}
	httptestServer = []string{"NewServer", "NewTLSServer", "NewUnstartedServer"}
)

// CleanupFindings is the cleanup predicate.
func CleanupFindings(filename string, file *ast.File, info *types.Info) []Finding {
	if !isTestFile(filename) {
		return nil
	}
	var out []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		var typ *ast.FuncType
		var body *ast.BlockStmt
		switch fn := n.(type) {
		case *ast.FuncDecl:
			typ, body = fn.Type, fn.Body
		case *ast.FuncLit:
			typ, body = fn.Type, fn.Body
		default:
			return true
		}
		if body == nil {
			return true
		}
		if t := testingParam(file, info, typ); t != nil {
			out = append(out, unreleasedStarts(file, info, t, body)...)
		}
		return true
	})
	return out
}

// testingParam returns the first parameter of typ that is a *testing.T, *testing.B,
// *testing.F or testing.TB, or nil.
func testingParam(file *ast.File, info *types.Info, typ *ast.FuncType) *ast.Ident {
	if typ.Params == nil {
		return nil
	}
	for _, field := range typ.Params.List {
		if len(field.Names) == 0 || !isTestingType(file, info, field.Type) {
			continue
		}
		if name := field.Names[0]; name.Name != "_" {
			return name
		}
	}
	return nil
}

func isTestingType(file *ast.File, info *types.Info, expr ast.Expr) bool {
	want := []string{"TB"}
	if star, ok := expr.(*ast.StarExpr); ok {
		expr, want = star.X, []string{"T", "B", "F"}
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || !isPkgName(file, info, x, "testing") {
		return false
	}
	for _, name := range want {
		if sel.Sel.Name == name {
			return true
		}
	}
	return false
}

// unreleasedStarts returns every start in body when body never calls t.Cleanup.
// Nested test functions are skipped here; CleanupFindings judges them on their own.
func unreleasedStarts(file *ast.File, info *types.Info, t *ast.Ident, body *ast.BlockStmt) []Finding {
	var starts []Finding
	released := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.FuncLit:
			return testingParam(file, info, e.Type) == nil
		case *ast.GoStmt:
			starts = append(starts, Finding{Pos: e.Pos(), Message: "goroutine started in a test without registering its release with t.Cleanup (QG-5)"})
		case *ast.CallExpr:
			switch {
			case isCleanupCall(info, t, e):
				released = true
			case pkgFunc(file, info, e.Fun, "net", netListen...),
				pkgFunc(file, info, e.Fun, "net/http/httptest", httptestServer...):
				starts = append(starts, Finding{Pos: e.Pos(), Message: "listener started in a test without registering its release with t.Cleanup (QG-5)"})
			case pkgFunc(file, info, e.Fun, "os", "StartProcess"), isCmdStart(file, info, e):
				starts = append(starts, Finding{Pos: e.Pos(), Message: "child process started in a test without registering its release with t.Cleanup (QG-5)"})
			}
		}
		return true
	})
	if released {
		return nil
	}
	return starts
}

// isCleanupCall reports whether call is t.Cleanup(...) on the test's own parameter.
func isCleanupCall(info *types.Info, t *ast.Ident, call *ast.CallExpr) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Cleanup" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if info != nil {
		return info.Uses[x] != nil && info.Uses[x] == info.Defs[t]
	}
	return x.Name == t.Name
}

// isCmdStart reports whether call is the Start method of an *exec.Cmd. Without type
// information, any argument-free Start call in a file that imports os/exec counts.
func isCmdStart(file *ast.File, info *types.Info, call *ast.CallExpr) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Start" || len(call.Args) != 0 {
		return false
	}
	if info == nil {
		_, imported := importName(file, "os/exec")
		return imported
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return false
	}
	recv := fn.Signature().Recv()
	if recv == nil {
		return false
	}
	ptr, ok := recv.Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "os/exec" && named.Obj().Name() == "Cmd"
}
