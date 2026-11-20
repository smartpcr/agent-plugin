package analyzers

import (
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"strconv"
	"strings"
)

// Seed provenance (DH-ADR-TOOLCHAIN-3): every f.Add argument in seeds_test.go must be
// computed from data compiled into the test binary. The check is syntactic and
// conservative: an argument is compiled in when it is built only from
//
//   - literals, nil, true, false and iota;
//   - composite literals, operators, indexing and slicing of compiled-in values;
//   - conversions to predeclared types and slices of them ([]byte(...)), the builtins
//     of pureBuiltins, and calls and selectors of the stdlib packages of pureStd;
//   - package-level constants and variables of seeds_test.go whose initializer is
//     compiled in, variables with a //go:embed directive, and zero-valued variables;
//   - locals of the same function whose every definition and assignment is compiled
//     in (a range variable is compiled in when the ranged value is).
//
// Anything else is not compiled in: any other call (so a helper of another file that
// reads the disk cannot hide behind a call), a parameter, a name not declared in
// seeds_test.go, a variable whose address is taken or that is passed to such a call,
// and a seeds_test.go variable that another test file assigns, takes the address of
// or copies into. A local of the same name as a package variable is checked
// together with it. Not tracked: a seed variable mutated through a call made from
// another test file, e.g. readInto(seedVar).

// pureBuiltins are the builtins an argument may call; none reads outside its operands.
var pureBuiltins = map[string]bool{
	"append": true, "cap": true, "complex": true, "imag": true, "len": true,
	"make": true, "max": true, "min": true, "real": true,
}

// predeclaredTypes are the conversions an argument may make.
var predeclaredTypes = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true, "float32": true,
	"float64": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true,
}

// pureStd are the stdlib packages whose functions, constants and types an argument
// may use: they compute only from their operands and read no file or environment.
var pureStd = map[string]bool{
	"bytes": true, "encoding/base64": true, "encoding/binary": true, "encoding/hex": true,
	"encoding/json": true, "fmt": true, "math": true, "strconv": true, "strings": true,
	"unicode/utf16": true, "unicode/utf8": true,
}

type defKind int

const (
	defExpr   defKind = iota // assigned the value of x
	defZero                  // a zero value, a constant or //go:embed bytes
	defOpaque                // not known at compile time; why says what it is
)

// seedDef is one definition of or assignment to a name; x is resolved in env (nil
// for a package-level initializer).
type seedDef struct {
	kind defKind
	x    ast.Expr
	env  *seedEnv
	why  string
	decl bool // a declaration (parameter, :=, var, range :=) rather than a write
	pass bool // the variable is passed to a call that may write through it
}

// seedEnv holds the definitions of the names one function declares and assigns.
type seedEnv struct {
	locals   map[string][]seedDef
	declared map[string]bool
}

type seedKey struct {
	env  *seedEnv
	name string
}

// provenance resolves f.Add arguments of seeds_test.go.
type provenance struct {
	file     *ast.File
	pkg      map[string][]seedDef
	visiting map[seedKey]bool
}

// newProvenance collects the package-level names of seeds and every write to them,
// from seeds itself and from the other test files of the package.
func newProvenance(seeds *PackageFile, files []PackageFile) *provenance {
	p := &provenance{file: seeds.File, pkg: map[string][]seedDef{}, visiting: map[seedKey]bool{}}
	for _, decl := range seeds.File.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			embedded := gd.Tok == token.VAR && (hasEmbed(gd.Doc) || hasEmbed(vs.Doc))
			for i, n := range vs.Names {
				switch {
				case gd.Tok == token.CONST || embedded || len(vs.Values) == 0:
					p.pkg[n.Name] = append(p.pkg[n.Name], seedDef{kind: defZero})
				case len(vs.Values) == len(vs.Names):
					p.pkg[n.Name] = append(p.pkg[n.Name], seedDef{kind: defExpr, x: vs.Values[i]})
				default:
					p.pkg[n.Name] = append(p.pkg[n.Name], seedDef{kind: defOpaque, why: n.Name + " gets one of several results"})
				}
			}
		}
	}
	for _, pf := range files {
		if !isTestFile(pf.Name) {
			continue
		}
		inSeeds := pf.Name == seeds.Name
		for _, decl := range pf.File.Decls {
			env := collectEnv(decl)
			for name, defs := range env.writes() {
				if _, ok := p.pkg[name]; !ok {
					continue
				}
				if inSeeds {
					p.pkg[name] = append(p.pkg[name], defs...)
				} else if !env.declared[name] {
					p.pkg[name] = append(p.pkg[name], seedDef{kind: defOpaque,
						why: name + " is written in " + path.Base(strings.ReplaceAll(pf.Name, `\`, "/"))})
				}
			}
		}
	}
	return p
}

// hasEmbed reports whether cg holds a //go:embed directive.
func hasEmbed(cg *ast.CommentGroup) bool {
	if cg == nil {
		return false
	}
	for _, c := range cg.List {
		if strings.HasPrefix(c.Text, "//go:embed ") {
			return true
		}
	}
	return false
}

// check returns the first part of x that is not compiled in and why, or nil.
func (p *provenance) check(x ast.Expr, env *seedEnv) (ast.Node, string) {
	switch x := x.(type) {
	case *ast.BasicLit, *ast.ArrayType, *ast.MapType: // a type is an operand only of make
		return nil, ""
	case *ast.Ident:
		return p.checkName(x, env)
	case *ast.ParenExpr:
		return p.check(x.X, env)
	case *ast.CompositeLit:
		for _, e := range x.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); !ok || p.shadowed(id.Name, env) { // else a field name
					if bad, why := p.check(kv.Key, env); bad != nil {
						return bad, why
					}
				}
				e = kv.Value
			}
			if bad, why := p.check(e, env); bad != nil {
				return bad, why
			}
		}
		return nil, ""
	case *ast.UnaryExpr:
		if x.Op == token.AND || x.Op == token.ARROW {
			return x, "uses " + types.ExprString(x)
		}
		return p.check(x.X, env)
	case *ast.BinaryExpr:
		if bad, why := p.check(x.X, env); bad != nil {
			return bad, why
		}
		return p.check(x.Y, env)
	case *ast.IndexExpr:
		if bad, why := p.check(x.X, env); bad != nil {
			return bad, why
		}
		return p.check(x.Index, env)
	case *ast.SliceExpr:
		for _, e := range []ast.Expr{x.X, x.Low, x.High, x.Max} {
			if e != nil {
				if bad, why := p.check(e, env); bad != nil {
					return bad, why
				}
			}
		}
		return nil, ""
	case *ast.SelectorExpr:
		if p.pureSelector(x, env) {
			return nil, ""
		}
		if _, ok := ast.Unparen(x.X).(*ast.Ident); ok {
			return p.check(x.X, env) // a field of a compiled-in value
		}
		return x, "uses " + types.ExprString(x)
	case *ast.CallExpr:
		if !p.pureCall(x, env) {
			return x, "calls " + types.ExprString(x.Fun)
		}
		for _, a := range x.Args {
			if bad, why := p.check(a, env); bad != nil {
				return bad, why
			}
		}
		return nil, ""
	}
	return x, "uses " + types.ExprString(x)
}

// checkName resolves id among env's locals and the package-level names of
// seeds_test.go, checking every definition it has in either.
func (p *provenance) checkName(id *ast.Ident, env *seedEnv) (ast.Node, string) {
	switch id.Name {
	case "nil", "true", "false", "iota":
		return nil, ""
	}
	var defs []seedDef
	found := false
	if env != nil && env.declared[id.Name] {
		defs, found = env.locals[id.Name], true
	}
	if pd, ok := p.pkg[id.Name]; ok {
		for _, d := range pd {
			if d.env == nil || d.env != env || !env.declared[id.Name] {
				defs = append(defs, d)
			}
		}
		found = true
	}
	if !found {
		return id, id.Name + " is not declared in " + SeedsFile
	}
	key := seedKey{env, id.Name}
	if p.visiting[key] {
		return nil, "" // every definition on the cycle is checked where it is first reached
	}
	p.visiting[key] = true
	defer delete(p.visiting, key)
	for _, d := range defs {
		switch d.kind {
		case defOpaque:
			return id, d.why
		case defExpr:
			if bad, why := p.check(d.x, d.env); bad != nil {
				return bad, why
			}
		}
	}
	return nil, ""
}

// pureCall reports whether call is a conversion to a predeclared type, a pure
// builtin, or a call of a pureStd package.
func (p *provenance) pureCall(call *ast.CallExpr, env *seedEnv) bool {
	switch fn := ast.Unparen(call.Fun).(type) {
	case *ast.ArrayType:
		id, ok := fn.Elt.(*ast.Ident)
		return fn.Len == nil && ok && predeclaredTypes[id.Name]
	case *ast.Ident:
		if p.shadowed(fn.Name, env) {
			return false
		}
		return predeclaredTypes[fn.Name] || pureBuiltins[fn.Name]
	case *ast.SelectorExpr:
		return p.pureSelector(fn, env)
	}
	return false
}

// pureSelector reports whether sel names a member of a pureStd import.
func (p *provenance) pureSelector(sel *ast.SelectorExpr, env *seedEnv) bool {
	id, ok := sel.X.(*ast.Ident)
	if !ok || p.shadowed(id.Name, env) {
		return false
	}
	for _, imp := range p.file.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !pureStd[ip] {
			continue
		}
		name := path.Base(ip)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == id.Name {
			return true
		}
	}
	return false
}

// shadowed reports whether name is declared in env or at package level in
// seeds_test.go, hiding a predeclared or imported name.
func (p *provenance) shadowed(name string, env *seedEnv) bool {
	_, pkg := p.pkg[name]
	return pkg || (env != nil && env.declared[name])
}

// writes returns the assignments env records, by name; definitions are left out.
func (e *seedEnv) writes() map[string][]seedDef {
	out := map[string][]seedDef{}
	for name, defs := range e.locals {
		for _, d := range defs {
			if !d.decl {
				out[name] = append(out[name], d)
			}
		}
	}
	return out
}

// reassigned reports whether name (a *testing.F parameter) is redeclared, assigned
// or has its address taken in env; passing it to a call is not reassigning it.
func reassigned(env *seedEnv, name string) bool {
	for i, d := range env.locals[name] {
		if i > 0 && !d.pass {
			return true
		}
	}
	return false
}

// collectEnv records every definition of and write to a name inside node: parameters,
// :=, var declarations, range variables, assignments (also through an index, a field
// or a pointer), copy destinations, taken addresses, and variables passed to a call
// that is not pure or an Add.
func collectEnv(node ast.Node) *seedEnv {
	env := &seedEnv{locals: map[string][]seedDef{}, declared: map[string]bool{}}
	add := func(name string, d seedDef, declare bool) {
		if name == "" || name == "_" {
			return
		}
		d.env, d.decl = env, declare
		if declare {
			env.declared[name] = true
		}
		env.locals[name] = append(env.locals[name], d)
	}
	params := func(ft *ast.FuncType, recv *ast.FieldList) {
		for _, fl := range []*ast.FieldList{recv, ft.Params, ft.Results} {
			if fl == nil {
				continue
			}
			for _, field := range fl.List {
				for _, n := range field.Names {
					add(n.Name, seedDef{kind: defOpaque, why: n.Name + " is a parameter"}, true)
				}
			}
		}
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			params(n.Type, n.Recv)
		case *ast.FuncLit:
			params(n.Type, nil)
		case *ast.AssignStmt:
			for i, l := range n.Lhs {
				d := seedDef{kind: defOpaque, why: types.ExprString(l) + " gets one of several results"}
				if len(n.Lhs) == len(n.Rhs) {
					d = seedDef{kind: defExpr, x: n.Rhs[i]}
				}
				if id, ok := l.(*ast.Ident); ok && n.Tok == token.DEFINE {
					add(id.Name, d, true)
				} else {
					add(rootName(l), d, false)
				}
			}
		case *ast.RangeStmt:
			for _, l := range []ast.Expr{n.Key, n.Value} {
				if l == nil {
					continue
				}
				if id, ok := l.(*ast.Ident); ok && n.Tok == token.DEFINE {
					add(id.Name, seedDef{kind: defExpr, x: n.X}, true)
				} else {
					add(rootName(l), seedDef{kind: defExpr, x: n.X}, false)
				}
			}
		case *ast.DeclStmt:
			gd, ok := n.Decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
				break
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					switch {
					case gd.Tok == token.CONST || len(vs.Values) == 0:
						add(id.Name, seedDef{kind: defZero}, true)
					case len(vs.Values) == len(vs.Names):
						add(id.Name, seedDef{kind: defExpr, x: vs.Values[i]}, true)
					default:
						add(id.Name, seedDef{kind: defOpaque, why: id.Name + " gets one of several results"}, true)
					}
				}
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				name := rootName(n.X)
				add(name, seedDef{kind: defOpaque, why: "the address of " + name + " is taken"}, false)
			}
		case *ast.CallExpr:
			if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok && id.Name == "copy" && len(n.Args) == 2 {
				add(rootName(n.Args[0]), seedDef{kind: defExpr, x: n.Args[1]}, false)
				break
			}
			if sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "Add" {
				break
			}
			if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok && (pureBuiltins[id.Name] || predeclaredTypes[id.Name]) {
				break
			}
			if _, ok := ast.Unparen(n.Fun).(*ast.ArrayType); ok {
				break
			}
			for _, a := range n.Args {
				if id, ok := ast.Unparen(a).(*ast.Ident); ok {
					add(id.Name, seedDef{kind: defOpaque, pass: true, why: id.Name + " is passed to " + types.ExprString(n.Fun)}, false)
				}
			}
		}
		return true
	})
	return env
}

// rootName returns the variable an assignable expression writes to: x for x, x[i],
// x.f, *x and their combinations, or "" when there is none.
func rootName(x ast.Expr) string {
	for {
		switch e := x.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.IndexExpr:
			x = e.X
		case *ast.SelectorExpr:
			x = e.X
		case *ast.StarExpr:
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		default:
			return ""
		}
	}
}
