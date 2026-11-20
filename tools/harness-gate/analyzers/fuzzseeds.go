package analyzers

import (
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Seed provenance (DH-ADR-TOOLCHAIN-3): every f.Add argument in seeds_test.go must be
// computed from data compiled into the test binary. The check is syntactic and
// conservative: an argument is compiled in when it is built only from
//
//   - literals, and nil, true, false and iota where no declaration of the package
//     or the function renames them;
//   - composite literals of seedTypes (slices, arrays and maps of predeclared
//     types; never a named type, so no String, MarshalJSON or other method can run
//     on a seed), operators, indexing and slicing of compiled-in values;
//   - conversions to seedTypes ([]byte(...)), the builtins of pureBuiltins, and
//     calls and selectors of the stdlib packages of pureStd, whose operands are in
//     turn compiled in;
//   - package-level constants and variables of seeds_test.go whose initializer is
//     compiled in, variables with a //go:embed directive, and zero-valued variables
//     of a seedType;
//   - locals of the same function whose every definition and write is compiled in
//     (a range variable is compiled in when the ranged value is; a variable passed
//     to a pureStd call is written from that call, so its operands are checked).
//
// Anything else is not compiled in: any other call or method call (so a helper of
// another file that reads the disk cannot hide behind a call), a parameter, a name
// not declared in seeds_test.go, a variable whose address is taken, that is passed
// to such a call or whose method is called, and a seeds_test.go variable that
// another test file writes, takes the address of or passes to any call. A
// predeclared or imported name declared anywhere in the package or the function is
// not trusted.
//
// Writes follow storage, not names: a variable defined or assigned from an
// expression that mentions another (alias := b, alias := b[1:], for _, s := range
// seeds, y := append(b[:0], ...)) may share its backing array, so the two join one
// alias class and every write to a member is a write to all of them. In another test
// file a seeds_test.go variable may only be read: a write to it or to any local of
// its class other than len, cap or a conversion makes it opaque.

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
// fmt and encoding/json are left out: they dispatch to String, Format and
// MarshalJSON methods of their operands.
var pureStd = map[string]bool{
	"bytes": true, "encoding/base64": true, "encoding/hex": true,
	"math": true, "strconv": true, "strings": true,
	"unicode/utf16": true, "unicode/utf8": true,
}

type defKind int

const (
	defExpr   defKind = iota // assigned the value of x
	defZero                  // a zero value, a constant or //go:embed bytes
	defOpaque                // not known at compile time; why says what it is
	defType                  // the zero value of type x
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
	call bool // the variable is an operand of x, a builtin, conversion or pureStd call
}

// seedEnv holds the definitions of the names one function declares and assigns.
type seedEnv struct {
	locals   map[string][]seedDef
	declared map[string]bool
	alias    map[string]string // union-find parent of the names that may share storage
}

// find returns the representative of name's alias class.
func (e *seedEnv) find(name string) string {
	for {
		up, ok := e.alias[name]
		if !ok || up == name {
			return name
		}
		name = up
	}
}

// union joins the alias classes of a and b.
func (e *seedEnv) union(a, b string) {
	if a == "" || b == "" || a == "_" || b == "_" {
		return
	}
	if ra, rb := e.find(a), e.find(b); ra != rb {
		e.alias[ra] = rb
	}
	if _, ok := e.alias[b]; !ok {
		e.alias[b] = b
	}
	if _, ok := e.alias[a]; !ok {
		e.alias[a] = e.find(a)
	}
}

// class returns the other names of name's alias class, sorted.
func (e *seedEnv) class(name string) []string {
	if _, ok := e.alias[name]; !ok {
		return nil
	}
	root := e.find(name)
	var out []string
	for m := range e.alias {
		if m != name && e.find(m) == root {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

type seedKey struct {
	env  *seedEnv
	name string
}

// provenance resolves f.Add arguments of seeds_test.go.
type provenance struct {
	file     *ast.File
	pkg      map[string][]seedDef
	global   map[string]bool // every package-level name, of every file
	visiting map[seedKey]bool
}

// newProvenance collects the package-level names of seeds and every write to them,
// from seeds itself and from the other test files of the package.
func newProvenance(seeds *PackageFile, files []PackageFile) *provenance {
	p := &provenance{file: seeds.File, pkg: map[string][]seedDef{}, global: packageNames(files), visiting: map[seedKey]bool{}}
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
				case gd.Tok == token.VAR && !embedded && vs.Type != nil && !p.seedType(vs.Type, nil):
					p.pkg[n.Name] = append(p.pkg[n.Name], seedDef{kind: defOpaque, why: n.Name + " has type " + types.ExprString(vs.Type)})
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
			env := collectEnv(pf.File, decl)
			p.propagateAliases(env, inSeeds, pf.Name)
			for name, defs := range env.writes() {
				if _, ok := p.pkg[name]; !ok {
					continue
				}
				if inSeeds {
					p.pkg[name] = append(p.pkg[name], defs...)
					continue
				}
				if env.declared[name] {
					continue // a local of the same name
				}
				for _, d := range defs {
					if !p.onlyReads(d, env) {
						p.pkg[name] = append(p.pkg[name], seedDef{kind: defOpaque,
							why: name + " is written in " + path.Base(strings.ReplaceAll(pf.Name, `\`, "/"))})
						break
					}
				}
			}
		}
	}
	return p
}

// propagateAliases makes every write that env records to a local sharing storage
// with a seeds_test.go variable a write to that variable. In seeds_test.go the
// variable is resolved through its aliases; in another file any such write other
// than a read makes it opaque.
func (p *provenance) propagateAliases(env *seedEnv, inSeeds bool, file string) {
	var names []string
	for name := range env.alias {
		if _, ok := p.pkg[name]; ok && !env.declared[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if inSeeds {
			p.pkg[name] = append(p.pkg[name], p.aliasDefs(env, name)...)
			continue
		}
	members:
		for _, m := range env.class(name) {
			if !env.declared[m] {
				continue // another package variable: its own writes are checked
			}
			for _, d := range env.locals[m] {
				if !d.decl && !p.onlyReads(d, env) {
					p.pkg[name] = append(p.pkg[name], seedDef{kind: defOpaque,
						why: name + " shares storage with " + m + ", which is written in " + path.Base(strings.ReplaceAll(file, `\`, "/"))})
					break members
				}
			}
		}
	}
}

// aliasDefs returns a definition of name as each variable of its alias class in env,
// so resolving name also resolves every write to storage it may share.
func (p *provenance) aliasDefs(env *seedEnv, name string) []seedDef {
	var defs []seedDef
	for _, m := range env.class(name) {
		if _, pkg := p.pkg[m]; env.declared[m] || pkg {
			defs = append(defs, seedDef{kind: defExpr, x: ast.NewIdent(m), env: env})
		}
	}
	return defs
}

// onlyReads reports whether d is a variable's use as the operand of len, cap or a
// conversion that no declaration of the package or of env renames.
func (p *provenance) onlyReads(d seedDef, env *seedEnv) bool {
	call, ok := d.x.(*ast.CallExpr)
	if !ok || !d.call {
		return false
	}
	switch fn := ast.Unparen(call.Fun).(type) {
	case *ast.ArrayType:
		return fn.Len == nil && p.seedType(fn, env)
	case *ast.Ident:
		return (fn.Name == "len" || fn.Name == "cap" || predeclaredTypes[fn.Name]) && !p.shadowed(fn.Name, env)
	}
	return false
}

// packageNames returns the names every file of the package declares at package
// level; such a name hides a predeclared one throughout the package.
func packageNames(files []PackageFile) map[string]bool {
	names := map[string]bool{}
	for _, pf := range files {
		for _, decl := range pf.File.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
	return names
}

// seedType reports whether t is a predeclared type, or a slice, array or map of
// seedTypes: a type without methods.
func (p *provenance) seedType(t ast.Expr, env *seedEnv) bool {
	switch t := ast.Unparen(t).(type) {
	case *ast.Ident:
		return predeclaredTypes[t.Name] && !p.shadowed(t.Name, env)
	case *ast.ArrayType:
		switch t.Len.(type) {
		case nil, *ast.BasicLit, *ast.Ellipsis:
			return p.seedType(t.Elt, env)
		}
	case *ast.MapType:
		return p.seedType(t.Key, env) && p.seedType(t.Value, env)
	}
	return false
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
	case *ast.BasicLit:
		return nil, ""
	case *ast.ArrayType, *ast.MapType: // a type is an operand only of make
		if p.seedType(x, env) {
			return nil, ""
		}
		return x, "uses type " + types.ExprString(x)
	case *ast.Ident:
		return p.checkName(x, env)
	case *ast.ParenExpr:
		return p.check(x.X, env)
	case *ast.CompositeLit:
		if x.Type != nil && !p.seedType(x.Type, env) {
			return x, "a " + types.ExprString(x.Type) + " literal: seeds are built from predeclared types, slices, arrays and maps of them"
		}
		for _, e := range x.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if bad, why := p.check(kv.Key, env); bad != nil { // an index or a map key
					return bad, why
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
		if !p.shadowed(id.Name, env) {
			return nil, ""
		}
	}
	var defs []seedDef
	found := false
	if env != nil && env.declared[id.Name] {
		defs, found = append(append([]seedDef(nil), env.locals[id.Name]...), p.aliasDefs(env, id.Name)...), true
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
		case defType:
			if !p.seedType(d.x, d.env) {
				return id, d.why
			}
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
		return fn.Len == nil && p.seedType(fn, env)
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
	return ok && !p.shadowed(id.Name, env) && pureImport(p.file, id.Name)
}

// pureImport reports whether name is the name file imports a pureStd package as.
func pureImport(file *ast.File, name string) bool {
	for _, imp := range file.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !pureStd[ip] {
			continue
		}
		n := path.Base(ip)
		if imp.Name != nil {
			n = imp.Name.Name
		}
		if n == name {
			return true
		}
	}
	return false
}

// shadowed reports whether name is declared in env or at package level in any file
// of the package, hiding a predeclared or imported name.
func (p *provenance) shadowed(name string, env *seedEnv) bool {
	return p.global[name] || (env != nil && env.declared[name])
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
// :=, var declarations, range variables, assignments (also through an index, a slice,
// a field or a pointer), copy destinations, taken addresses, and every variable passed
// to a call or whose method is called. A variable passed to a builtin, a conversion
// or a call of a pureStd package of file is written from that call, so the call's
// operands are checked with it; any other call makes it opaque. len, cap and
// conversions only read their operand, and f.Add only reads its arguments.
func collectEnv(file *ast.File, node ast.Node) *seedEnv {
	env := &seedEnv{locals: map[string][]seedDef{}, declared: map[string]bool{}, alias: map[string]string{}}
	add := func(name string, d seedDef, declare bool) {
		if name == "" || name == "_" {
			return
		}
		d.env, d.decl = env, declare
		if declare {
			env.declared[name] = true
		}
		env.locals[name] = append(env.locals[name], d)
		if d.kind == defExpr && d.x != nil {
			for _, ref := range refNames(d.x) {
				env.union(name, ref)
			}
		}
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
					case gd.Tok == token.VAR && len(vs.Values) == 0 && vs.Type != nil:
						add(id.Name, seedDef{kind: defType, x: vs.Type, why: id.Name + " has type " + types.ExprString(vs.Type)}, true)
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
			fun := types.ExprString(n.Fun)
			pure := false // a builtin, a conversion or a pureStd call: check decides
			switch fn := ast.Unparen(n.Fun).(type) {
			case *ast.ArrayType:
				pure = true
			case *ast.Ident:
				pure = pureBuiltins[fn.Name] || predeclaredTypes[fn.Name] || fn.Name == "len" || fn.Name == "cap"
			case *ast.SelectorExpr:
				if id, ok := ast.Unparen(fn.X).(*ast.Ident); ok && pureImport(file, id.Name) {
					pure = true
					break
				}
				recv := rootName(fn.X)
				add(recv, seedDef{kind: defOpaque, pass: true, why: "method " + fun + " is called on " + recv}, false)
				if fn.Sel.Name == "Add" {
					return true // a seed registration reads its arguments
				}
			}
			for _, a := range n.Args {
				name := rootName(a)
				if pure {
					add(name, seedDef{kind: defExpr, x: n, call: true}, false)
				} else {
					add(name, seedDef{kind: defOpaque, pass: true, why: name + " is passed to " + fun}, false)
				}
			}
		}
		return true
	})
	return env
}

// refNames returns every name x mentions, but not the selected names of selectors.
func refNames(x ast.Node) []string {
	var out []string
	ast.Inspect(x, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			out = append(out, refNames(n.X)...)
			return false
		case *ast.Ident:
			out = append(out, n.Name)
		}
		return true
	})
	return out
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
		case *ast.SliceExpr:
			x = e.X
		case *ast.ParenExpr:
			x = e.X
		default:
			return ""
		}
	}
}
