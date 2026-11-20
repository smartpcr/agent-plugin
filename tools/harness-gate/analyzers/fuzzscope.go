package analyzers

import (
	"go/ast"
	"go/token"
	"strconv"
)

// scopeObj is one variable, constant, type or function a declaration inside a
// function introduces.
type scopeObj struct {
	name string
	sig  bool // declared by the signature of the outermost function
}

// resolveNames binds every identifier inside node to the declaration it denotes,
// following Go's block scopes: function literals, blocks, if, for, range, switch,
// select and their clauses. It returns, for each identifier, the key of the
// variable it denotes, and the names any local declaration hides.
//
// A name keeps its spelling unless several variables share it: then the
// declarations other than the outermost function's parameters get a key of their own
// (name·1, name·2, ...), so an unrelated b of a fuzz callback is not the b a seed
// loop declares. Identifiers that no local declaration binds (package-level names,
// builtins, imports) keep their spelling.
func resolveNames(node ast.Node) (map[*ast.Ident]string, map[string]bool) {
	refs := map[*ast.Ident]*scopeObj{}
	var objs []*scopeObj
	free := map[string]bool{}
	scopes := []map[string]*scopeObj{{}}
	push := func() { scopes = append(scopes, map[string]*scopeObj{}) }
	pop := func() { scopes = scopes[:len(scopes)-1] }
	declare := func(id *ast.Ident, sig bool) {
		if id == nil || id.Name == "_" {
			return
		}
		o := &scopeObj{name: id.Name, sig: sig}
		objs = append(objs, o)
		scopes[len(scopes)-1][id.Name] = o
		refs[id] = o
	}
	use := func(id *ast.Ident) {
		for i := len(scopes) - 1; i >= 0; i-- {
			if o := scopes[i][id.Name]; o != nil {
				refs[id] = o
				return
			}
		}
		free[id.Name] = true
	}
	var visit func(n ast.Node)
	children := func(n ast.Node) {
		ast.Inspect(n, func(c ast.Node) bool {
			if c == n {
				return true
			}
			if c != nil {
				visit(c)
			}
			return false
		})
	}
	visitAll := func(list ...ast.Node) {
		for _, n := range list {
			if n != nil {
				visit(n)
			}
		}
	}
	fieldTypes := func(fls ...*ast.FieldList) {
		for _, fl := range fls {
			if fl != nil {
				for _, f := range fl.List {
					visitAll(f.Type)
				}
			}
		}
	}
	fieldNames := func(sig bool, fls ...*ast.FieldList) {
		for _, fl := range fls {
			if fl != nil {
				for _, f := range fl.List {
					for _, n := range f.Names {
						declare(n, sig)
					}
				}
			}
		}
	}
	function := func(recv *ast.FieldList, ft *ast.FuncType, body *ast.BlockStmt, sig bool) {
		push()
		fieldTypes(ft.TypeParams)
		fieldNames(sig, ft.TypeParams)
		fieldTypes(recv, ft.Params, ft.Results)
		fieldNames(sig, recv, ft.Params, ft.Results)
		if body != nil {
			for _, st := range body.List {
				visit(st)
			}
		}
		pop()
	}
	stmts := func(list []ast.Stmt) {
		for _, st := range list {
			visit(st)
		}
	}
	visit = func(n ast.Node) {
		switch n := n.(type) {
		case *ast.Ident:
			use(n)
		case *ast.SelectorExpr:
			visit(n.X)
		case *ast.FuncDecl:
			function(n.Recv, n.Type, n.Body, true)
		case *ast.FuncLit:
			function(nil, n.Type, n.Body, false)
		case *ast.FuncType:
			fieldTypes(n.TypeParams, n.Params, n.Results)
		case *ast.StructType:
			fieldTypes(n.Fields)
		case *ast.InterfaceType:
			fieldTypes(n.Methods)
		case *ast.GenDecl: // package level: its names are not locals
			for _, spec := range n.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					visitAll(s.Type)
					for _, v := range s.Values {
						visit(v)
					}
				case *ast.TypeSpec:
					visitAll(s.Type)
				}
			}
		case *ast.DeclStmt:
			gd, ok := n.Decl.(*ast.GenDecl)
			if !ok {
				return
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					visitAll(s.Type)
					for _, v := range s.Values {
						visit(v)
					}
					for _, id := range s.Names {
						declare(id, false)
					}
				case *ast.TypeSpec:
					declare(s.Name, false)
					fieldTypes(s.TypeParams)
					visit(s.Type)
				}
			}
		case *ast.AssignStmt:
			for _, r := range n.Rhs {
				visit(r)
			}
			for _, l := range n.Lhs {
				id, ok := l.(*ast.Ident)
				if !ok || n.Tok != token.DEFINE {
					visit(l)
					continue
				}
				if o := scopes[len(scopes)-1][id.Name]; o != nil {
					refs[id] = o // := redeclares nothing in the same scope
				} else {
					declare(id, false)
				}
			}
		case *ast.BlockStmt:
			push()
			stmts(n.List)
			pop()
		case *ast.IfStmt:
			push()
			visitAll(n.Init, n.Cond, n.Body, n.Else)
			pop()
		case *ast.ForStmt:
			push()
			visitAll(n.Init, n.Cond, n.Post, n.Body)
			pop()
		case *ast.RangeStmt:
			visit(n.X)
			push()
			for _, l := range []ast.Expr{n.Key, n.Value} {
				if l == nil {
					continue
				}
				if id, ok := l.(*ast.Ident); ok && n.Tok == token.DEFINE {
					declare(id, false)
				} else {
					visit(l)
				}
			}
			visit(n.Body)
			pop()
		case *ast.SwitchStmt:
			push()
			visitAll(n.Init, n.Tag)
			stmts(n.Body.List)
			pop()
		case *ast.TypeSwitchStmt:
			push()
			visitAll(n.Init)
			if as, ok := n.Assign.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 {
				visit(as.Rhs[0])
				declare(as.Lhs[0].(*ast.Ident), false) // one variable for every clause
			} else {
				visitAll(n.Assign)
			}
			stmts(n.Body.List)
			pop()
		case *ast.SelectStmt:
			stmts(n.Body.List)
		case *ast.CaseClause:
			push()
			for _, e := range n.List {
				visit(e)
			}
			stmts(n.Body)
			pop()
		case *ast.CommClause:
			push()
			visitAll(n.Comm)
			stmts(n.Body)
			pop()
		case *ast.LabeledStmt:
			visit(n.Stmt)
		case *ast.BranchStmt:
		default:
			children(n)
		}
	}
	visit(node)

	byName := map[string][]*scopeObj{}
	shadows := map[string]bool{}
	for _, o := range objs {
		byName[o.name] = append(byName[o.name], o)
		shadows[o.name] = true
	}
	key := map[*scopeObj]string{}
	for name, os := range byName {
		if len(os) == 1 && !free[name] {
			key[os[0]] = name
			continue
		}
		k := 0
		for _, o := range os {
			if o.sig {
				key[o] = name // free uses cannot occur where a parameter is in scope
				continue
			}
			k++
			key[o] = name + "·" + strconv.Itoa(k)
		}
	}
	names := make(map[*ast.Ident]string, len(refs))
	for id, o := range refs {
		names[id] = key[o]
	}
	return names, shadows
}
