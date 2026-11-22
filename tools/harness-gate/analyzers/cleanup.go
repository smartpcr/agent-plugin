package analyzers

import (
	"go/ast"
	"go/types"
)

// Cleanup reports a goroutine, listener or child process started in a test function
// without a release registered with t.Cleanup (QG-5). Any t.Cleanup on the test
// releases its goroutines; a listener or child process is released only by a
// t.Cleanup whose argument references the started value. A test function is any
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

// start is a goroutine, listener or child process started in a test. Any t.Cleanup
// on the test releases a goroutine. For a listener or child process, value is the
// started value a t.Cleanup must reference; it is nil when the value is discarded,
// and then nothing releases the start.
type start struct {
	finding   Finding
	goroutine bool
	value     any
}

// unreleasedStarts returns every start in body that no t.Cleanup releases. A
// goroutine has no value to reference, so any t.Cleanup on the test releases it. A
// listener or child process is released only by a t.Cleanup whose argument (a method
// value or a closure) references the started value: the variable assigned from
// net.Listen*, httptest.New*Server or os.StartProcess, or the *exec.Cmd whose Start
// is called. Nested test functions are skipped here; CleanupFindings judges them on
// their own.
func unreleasedStarts(file *ast.File, info *types.Info, t *ast.Ident, body *ast.BlockStmt) []Finding {
	assigned := assignedIdents(body)
	var starts []start
	cleanedUp := false
	referenced := map[any]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.FuncLit:
			return testingParam(file, info, e.Type) == nil
		case *ast.GoStmt:
			starts = append(starts, start{
				finding:   Finding{Pos: e.Pos(), Message: "goroutine started in a test without registering its release with t.Cleanup (QG-5)"},
				goroutine: true,
			})
		case *ast.CallExpr:
			switch {
			case isCleanupCall(info, t, e):
				cleanedUp = true
				for _, arg := range e.Args {
					ast.Inspect(arg, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok {
							if key := identKey(info, id); key != nil {
								referenced[key] = true
							}
						}
						return true
					})
				}
			case pkgFunc(file, info, e.Fun, "net", netListen...),
				pkgFunc(file, info, e.Fun, "net/http/httptest", httptestServer...):
				starts = append(starts, start{
					finding: Finding{Pos: e.Pos(), Message: "listener started in a test without registering its release with t.Cleanup (QG-5)"},
					value:   valueKey(info, assigned[e]),
				})
			case pkgFunc(file, info, e.Fun, "os", "StartProcess"):
				starts = append(starts, start{
					finding: Finding{Pos: e.Pos(), Message: "child process started in a test without registering its release with t.Cleanup (QG-5)"},
					value:   valueKey(info, assigned[e]),
				})
			case isCmdStart(file, info, e):
				var cmd *ast.Ident
				if sel, ok := ast.Unparen(e.Fun).(*ast.SelectorExpr); ok {
					cmd, _ = ast.Unparen(sel.X).(*ast.Ident)
				}
				starts = append(starts, start{
					finding: Finding{Pos: e.Pos(), Message: "child process started in a test without registering its release with t.Cleanup (QG-5)"},
					value:   valueKey(info, cmd),
				})
			}
		}
		return true
	})
	var out []Finding
	for _, s := range starts {
		released := referenced[s.value]
		if s.goroutine {
			released = cleanedUp
		}
		if !released {
			out = append(out, s.finding)
		}
	}
	return out
}

// assignedIdents maps each call in body that is the sole right-hand side of an
// assignment or declaration to the identifier receiving its first result. A selector
// left-hand side (x.F = net.Listen(...), x.a.F = ...) is recorded under its root
// identifier x, so a t.Cleanup that references x (t.Cleanup(x.Close)) releases it
// (M1-G-GATE-5).
func assignedIdents(body *ast.BlockStmt) map[*ast.CallExpr]*ast.Ident {
	out := map[*ast.CallExpr]*ast.Ident{}
	record := func(lhs ast.Expr, rhs []ast.Expr) {
		if len(rhs) != 1 {
			return
		}
		call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
		if !ok {
			return
		}
		if id := selectorRoot(lhs); id != nil {
			out[call] = id
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) > 0 {
				record(s.Lhs[0], s.Rhs)
			}
		case *ast.ValueSpec:
			if len(s.Names) > 0 {
				record(s.Names[0], s.Values)
			}
		}
		return true
	})
	return out
}

// selectorRoot returns the identifier at the root of a chain of field selectors
// (x in x, x.F or x.a.F), or nil for any other expression. Unlike fuzzseeds'
// rootIdent, only selections count: an index, a slice or a dereference names no
// single variable whose cleanup would release the value.
func selectorRoot(e ast.Expr) *ast.Ident {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// valueKey identifies the variable id names, or returns nil when id is absent or
// blank. With type information the key is the variable's object; without it, its name.
func valueKey(info *types.Info, id *ast.Ident) any {
	if id == nil || id.Name == "_" {
		return nil
	}
	return identKey(info, id)
}

func identKey(info *types.Info, id *ast.Ident) any {
	if info == nil {
		return id.Name
	}
	if obj := info.Defs[id]; obj != nil {
		return obj
	}
	if obj := info.Uses[id]; obj != nil {
		return obj
	}
	return nil
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
