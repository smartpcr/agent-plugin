package analyzers

import (
	"go/ast"
	"go/types"
	"strings"
)

// ErrDiscard reports an error result that is thrown away: assigned to the blank
// identifier, or dropped by calling the function as a statement (DH-ADR-TOOLCHAIN-8,
// the Go form of the upstream empty-catch rule).
//
// The rule is narrowed to where a dropped error can hide a defect (owner decision of
// 2026-11-22, plan stage 13.5):
//
//   - _test.go files are not checked: a test fails on the behaviour it asserts, and a
//     teardown error has no caller to return to.
//   - fmt.Print* and fmt.Fprint* are exempt: they report only the writer's error,
//     which the writer's owner sees on its next write, Flush or Close.
//   - a deferred Close of a read-only handle is exempt: a file opened with os.Open
//     (or (*os.Root).Open, or os.OpenFile with os.O_RDONLY), or any value that can
//     Read but not Write, such as an http.Response Body. Closing what was only read
//     loses no data, so its error carries nothing to act on. A Close of anything
//     written -- a file opened for writing, a database, a writer -- is still reported,
//     because there the error is where a failed write surfaces.
var ErrDiscard = newAnalyzer("errdiscard",
	"forbid discarding an error result, by assigning it to _ or by calling as a statement",
	ErrDiscardFindings)

// errorType is the predeclared error interface.
var errorType = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

// isError reports whether t is, or implements, error.
func isError(t types.Type) bool {
	return t != nil && types.Implements(t, errorType)
}

// ErrDiscardFindings is the errdiscard predicate. It needs type information. Reported:
//
//   - "_ = f()", "_, _ = f()", "v, _ := f()" and "var _ = f()", where the result at a
//     blank position is an error;
//   - "f()" as a statement, and "defer f()" or "go f()", where f returns an error
//     among its results.
//
// Not reported: fmt.Print* and fmt.Fprint*, the Write* methods of *bytes.Buffer and
// *strings.Builder (their error is always nil), and a deferred Close of a read-only
// handle (see ErrDiscard). Only call results count: "_ = err" names an error that was
// already seen.
func ErrDiscardFindings(filename string, file *ast.File, info *types.Info) []Finding {
	if info == nil || isTestFile(filename) {
		return nil
	}
	var out []Finding
	blank := func(lhs []ast.Expr, rhs []ast.Expr) {
		if len(rhs) == 1 && len(lhs) > 1 {
			call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
			if !ok {
				return
			}
			tuple, ok := info.TypeOf(call).(*types.Tuple)
			if !ok {
				return
			}
			for i, l := range lhs {
				if isBlank(l) && i < tuple.Len() && isError(tuple.At(i).Type()) {
					out = append(out, discarded(l, call, "assigned to _"))
				}
			}
			return
		}
		for i, l := range lhs {
			if i >= len(rhs) || !isBlank(l) {
				continue
			}
			call, ok := ast.Unparen(rhs[i]).(*ast.CallExpr)
			if ok && isError(info.TypeOf(call)) {
				out = append(out, discarded(l, call, "assigned to _"))
			}
		}
	}
	dropped := func(at ast.Node, call *ast.CallExpr, how string) {
		if returnsError(info, call) && !errDiscardExempt(file, info, call) {
			out = append(out, discarded(at, call, how))
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			blank(s.Lhs, s.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(s.Names))
			for i, id := range s.Names {
				lhs[i] = id
			}
			blank(lhs, s.Values)
		case *ast.ExprStmt:
			if call, ok := ast.Unparen(s.X).(*ast.CallExpr); ok {
				dropped(s, call, "dropped by a call statement")
			}
		case *ast.DeferStmt:
			if !readOnlyClose(file, info, s.Call) {
				dropped(s, s.Call, "dropped by defer")
			}
		case *ast.GoStmt:
			dropped(s, s.Call, "dropped by go")
		}
		return true
	})
	return out
}

// discarded is the finding at node for the error result of call.
func discarded(at ast.Node, call *ast.CallExpr, how string) Finding {
	return Finding{Pos: at.Pos(), Message: "error result of " + types.ExprString(call.Fun) + " " + how +
		": handle it, or write //lint:ignore errdiscard <reason> (DH-ADR-TOOLCHAIN-8)"}
}

func isBlank(e ast.Expr) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)
	return ok && id.Name == "_"
}

// returnsError reports whether call has an error among its results. A conversion or
// a builtin is not a call that returns an error.
func returnsError(info *types.Info, call *ast.CallExpr) bool {
	if tv, ok := info.Types[call.Fun]; ok && (tv.IsType() || tv.IsBuiltin()) {
		return false
	}
	switch t := info.TypeOf(call).(type) {
	case nil:
		return false
	case *types.Tuple:
		for v := range t.Variables() {
			if isError(v.Type()) {
				return true
			}
		}
		return false
	default:
		return isError(t)
	}
}

// errDiscardExempt reports whether call is one whose error may be dropped: fmt.Print*,
// fmt.Fprint*, or a Write* method of *bytes.Buffer or *strings.Builder.
func errDiscardExempt(file *ast.File, info *types.Info, call *ast.CallExpr) bool {
	if pkgFunc(file, info, call.Fun, "fmt", "Print", "Printf", "Println", "Fprint", "Fprintf", "Fprintln") {
		return true
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "Write") {
		return false
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Signature().Recv() == nil {
		return false
	}
	t := fn.Signature().Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	switch named.Obj().Pkg().Path() + "." + named.Obj().Name() {
	case "bytes.Buffer", "strings.Builder":
		return true
	}
	return false
}

// readOnlyClose reports whether call is "x.Close()" on a read-only handle: a value
// that has a Read method and no Write method, or an *os.File variable every
// assignment of which in file opens it for reading (os.Open, (*os.Root).Open, or
// os.OpenFile with the flag os.O_RDONLY).
func readOnlyClose(file *ast.File, info *types.Info, call *ast.CallExpr) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Close" || len(call.Args) != 0 {
		return false
	}
	t := info.TypeOf(sel.X)
	if t == nil {
		return false
	}
	if isOSFile(t) {
		id, ok := ast.Unparen(sel.X).(*ast.Ident)
		if !ok {
			return false
		}
		v, ok := info.Uses[id].(*types.Var)
		return ok && openedReadOnly(file, info, v)
	}
	return hasMethod(t, "Read") && !hasMethod(t, "Write")
}

// hasMethod reports whether t, or *t, has the exported method name.
func hasMethod(t types.Type, name string) bool {
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, name)
	_, ok := obj.(*types.Func)
	return ok
}

// isOSFile reports whether t is *os.File.
func isOSFile(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := p.Elem().(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "os" && named.Obj().Name() == "File"
}

// openedReadOnly reports whether v is assigned at least once in file and every
// assignment is the first result of a read-only open.
func openedReadOnly(file *ast.File, info *types.Info, v *types.Var) bool {
	seen, readOnly := false, true
	assign := func(lhs []ast.Expr, rhs []ast.Expr) {
		for i, l := range lhs {
			id, ok := ast.Unparen(l).(*ast.Ident)
			if !ok || (info.Defs[id] != v && info.Uses[id] != v) {
				continue
			}
			seen = true
			if i != 0 || len(rhs) != 1 {
				readOnly = false
				continue
			}
			call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
			readOnly = readOnly && ok && readOnlyOpen(file, info, call)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			assign(s.Lhs, s.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(s.Names))
			for i, id := range s.Names {
				lhs[i] = id
			}
			assign(lhs, s.Values)
		}
		return true
	})
	return seen && readOnly
}

// readOnlyOpen reports whether call opens a file for reading only.
func readOnlyOpen(file *ast.File, info *types.Info, call *ast.CallExpr) bool {
	if pkgFunc(file, info, call.Fun, "os", "Open") {
		return true
	}
	if pkgFunc(file, info, call.Fun, "os", "OpenFile") && len(call.Args) == 3 {
		flag, ok := ast.Unparen(call.Args[1]).(*ast.SelectorExpr)
		return ok && flag.Sel.Name == "O_RDONLY" && isPkgName(file, info, identOf(flag.X), "os")
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Open" {
		return false
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Signature().Recv() == nil {
		return false
	}
	t := fn.Signature().Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "os" && named.Obj().Name() == "Root"
}

// identOf is e as an identifier, or a blank one.
func identOf(e ast.Expr) *ast.Ident {
	if id, ok := ast.Unparen(e).(*ast.Ident); ok {
		return id
	}
	return ast.NewIdent("_")
}
