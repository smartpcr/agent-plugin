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
// Writes follow storage, not names. A variable defined or assigned from an
// expression that may share another's backing array joins that variable's alias
// class, and a write to any member is a write to all of them: alias := b, alias :=
// b[1:], x.f, &b, [][]byte{b}, y := append(b[:0], ...), a slice-to-slice conversion,
// the bytes functions that may return a subslice, any other call of its operands, and
// -- unless the source is flat (only bytes, numbers or strings, which hold no shared
// storage) -- range variables, indexing, copy sources and append ... operands.
// Expressions that allocate or yield scalars or strings ([]byte("x"), make,
// bytes.Clone, string(b), len(b), b[0] of a []byte, operators) share nothing, so
// independent allocations and detached copies stay independent. Package-level
// variables of every file form one alias class graph: var alias = seed, and a write
// in any function that joins two package variables through its locals. In another
// test file a seeds_test.go variable, and any variable of its class, may only be
// read: a write other than len, cap or a conversion makes it opaque, and a variable
// of a non-test file is opaque in a seed's class (production code may write it).

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
	elem bool // a range value: an element of x
	key  bool // a range key: an index or a map key of x
	cnst bool // a constant: a number or a string, holding no storage
}

// seedEnv holds the definitions of the names one function declares and assigns.
type seedEnv struct {
	locals   map[string][]seedDef
	declared map[string]bool
	alias    map[string]string // union-find parent of the names that may share storage
	prov     *provenance       // resolves package-level names, or nil
}

func newSeedEnv() *seedEnv {
	return &seedEnv{locals: map[string][]seedDef{}, declared: map[string]bool{}, alias: map[string]string{}}
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
	pkg      map[string][]seedDef // package-level names of seeds_test.go
	foreign  map[string][]seedDef // package-level variables of the other files
	pkgAlias *seedEnv             // alias classes of package-level variables
	pkgEnv   *seedEnv             // the scope of package-level initializers
	global   map[string]bool      // every package-level name, of every file
	visiting map[seedKey]bool
}

// newProvenance collects the package-level names of seeds and every write to them,
// from seeds itself and from the other test files of the package.
func newProvenance(seeds *PackageFile, files []PackageFile) *provenance {
	p := &provenance{file: seeds.File, pkg: map[string][]seedDef{}, foreign: map[string][]seedDef{},
		pkgAlias: newSeedEnv(), pkgEnv: newSeedEnv(), global: packageNames(files), visiting: map[seedKey]bool{}}
	p.pkgEnv.prov = p
	type pkgValue struct {
		file *ast.File
		name string
		x    ast.Expr
	}
	var values []pkgValue
	for _, pf := range files {
		inSeeds := pf.Name == seeds.Name
		base := path.Base(strings.ReplaceAll(pf.Name, `\`, "/"))
		for _, decl := range pf.File.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				embedded := gd.Tok == token.VAR && (hasEmbed(gd.Doc) || hasEmbed(vs.Doc))
				for i, n := range vs.Names {
					var d seedDef
					switch {
					case !isTestFile(pf.Name):
						d = seedDef{kind: defOpaque, why: n.Name + " is a variable of " + base}
					case !inSeeds:
						// Only whether the variable can write storage it shares with a
						// seed matters: it is never a seed source itself.
						d = seedDef{kind: defZero}
						for _, v := range vs.Values {
							if !viewExpr(v) {
								d = seedDef{kind: defOpaque, why: "the initializer of " + n.Name + " in " + base + " may write the storage it shares"}
							}
						}
					case gd.Tok == token.VAR && !embedded && vs.Type != nil && !p.seedType(vs.Type, nil):
						d = seedDef{kind: defOpaque, why: n.Name + " has type " + types.ExprString(vs.Type)}
					case gd.Tok == token.CONST:
						d = seedDef{kind: defZero, cnst: true}
					case !embedded && len(vs.Values) == 0 && vs.Type != nil:
						d = seedDef{kind: defType, x: vs.Type, why: n.Name + " has type " + types.ExprString(vs.Type)}
					case embedded || len(vs.Values) == 0:
						d = seedDef{kind: defZero}
					case len(vs.Values) == len(vs.Names):
						d = seedDef{kind: defExpr, x: vs.Values[i]}
					default:
						d = seedDef{kind: defOpaque, why: n.Name + " gets one of several results"}
					}
					if inSeeds {
						p.pkg[n.Name] = append(p.pkg[n.Name], d)
					} else if gd.Tok == token.VAR {
						p.foreign[n.Name] = append(p.foreign[n.Name], d)
					}
					if gd.Tok == token.VAR {
						for j, v := range vs.Values {
							if len(vs.Values) != len(vs.Names) || i == j {
								values = append(values, pkgValue{pf.File, n.Name, v})
							}
						}
					}
				}
			}
		}
	}
	for _, v := range values { // once every package-level name is known
		for _, r := range storageRoots(v.file, p.pkgEnv, v.x, false) {
			p.pkgAlias.union(v.name, r)
		}
	}
	type fileEnv struct {
		env     *seedEnv
		inSeeds bool
		name    string
	}
	var envs []fileEnv
	for _, pf := range files {
		if !isTestFile(pf.Name) {
			continue
		}
		for _, decl := range pf.File.Decls {
			env := p.collectEnv(pf.File, decl)
			envs = append(envs, fileEnv{env, pf.Name == seeds.Name, pf.Name})
			for name := range env.alias { // locals that join two package variables
				if p.packageVar(name, env) {
					for _, m := range env.class(name) {
						if p.packageVar(m, env) {
							p.pkgAlias.union(name, m)
						}
					}
				}
			}
		}
	}
	for _, fe := range envs {
		env := fe.env
		p.propagateAliases(env, fe.inSeeds, fe.name)
		for name, defs := range env.writes() {
			target := p.pkg
			if _, ok := p.pkg[name]; !ok {
				if _, ok := p.foreign[name]; !ok {
					continue
				}
				target = p.foreign
			}
			if fe.inSeeds {
				target[name] = append(target[name], defs...)
				continue
			}
			if env.declared[name] {
				continue // a local of the same name
			}
			for _, d := range defs {
				if !p.onlyReads(d, env) {
					target[name] = append(target[name], seedDef{kind: defOpaque,
						why: name + " is written in " + path.Base(strings.ReplaceAll(fe.name, `\`, "/"))})
					break
				}
			}
		}
	}
	return p
}

// packageVar reports whether name, which env does not declare, is a package-level
// variable of some file.
func (p *provenance) packageVar(name string, env *seedEnv) bool {
	if env != nil && env.declared[name] {
		return false
	}
	_, seeds := p.pkg[name]
	_, other := p.foreign[name]
	return seeds || other
}

// pkgAliasDefs returns the definitions that resolving the package-level name must
// also check: one per other variable of its package alias class.
func (p *provenance) pkgAliasDefs(name string) []seedDef {
	var defs []seedDef
	for _, m := range p.pkgAlias.class(name) {
		if _, ok := p.pkg[m]; ok {
			defs = append(defs, seedDef{kind: defExpr, x: ast.NewIdent(m)})
		} else if fd, ok := p.foreign[m]; ok {
			defs = append(defs, fd...)
		}
	}
	return defs
}

// propagateAliases makes every write that env records to a local sharing storage
// with a seeds_test.go variable a write to that variable. In seeds_test.go the
// variable is resolved through its aliases; in another file any such write other
// than a read makes it opaque.
func (p *provenance) propagateAliases(env *seedEnv, inSeeds bool, file string) {
	var names []string
	for name := range env.alias {
		if p.packageVar(name, env) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		target := p.pkg
		if _, ok := p.pkg[name]; !ok {
			target = p.foreign
		}
		if inSeeds {
			target[name] = append(target[name], p.aliasDefs(env, name)...)
			continue
		}
	members:
		for _, m := range env.class(name) {
			if !env.declared[m] {
				continue // another package variable: its own writes are checked
			}
			for _, d := range env.locals[m] {
				if !d.decl && !p.onlyReads(d, env) {
					target[name] = append(target[name], seedDef{kind: defOpaque,
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
		} else if fd, ok := p.foreign[m]; ok {
			defs = append(defs, fd...)
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
		defs = append(defs, p.pkgAliasDefs(id.Name)...)
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
	return pureStd[importPath(file, name)]
}

// importPath returns the path of the package file imports as name, or "".
func importPath(file *ast.File, name string) string {
	for _, imp := range file.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		n := path.Base(ip)
		if imp.Name != nil {
			n = imp.Name.Name
		}
		if n == name {
			return ip
		}
	}
	return ""
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
	return collectEnvIn(file, node, nil)
}

// collectEnv is collectEnv resolving package-level names through p, so a copy of a
// package-level number or string is no alias.
func (p *provenance) collectEnv(file *ast.File, node ast.Node) *seedEnv {
	return collectEnvIn(file, node, p)
}

func collectEnvIn(file *ast.File, node ast.Node, prov *provenance) *seedEnv {
	env := newSeedEnv()
	env.prov = prov
	type aliasEdge struct { // name may share storage with x, or with only x's elements
		name  string
		x     ast.Expr
		elems bool
	}
	var edges []aliasEdge
	edge := func(name string, x ast.Expr, elems bool) {
		edges = append(edges, aliasEdge{name, x, elems})
	}
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
				for j, r := range n.Rhs {
					if len(n.Lhs) != len(n.Rhs) || i == j {
						edge(rootName(l), r, false)
					}
				}
			}
		case *ast.RangeStmt:
			for _, l := range []ast.Expr{n.Key, n.Value} {
				if l == nil {
					continue
				}
				d := seedDef{kind: defExpr, x: n.X, elem: l == n.Value, key: l == n.Key}
				if id, ok := l.(*ast.Ident); ok && n.Tok == token.DEFINE {
					add(id.Name, d, true)
				} else {
					add(rootName(l), d, false)
				}
				if l == n.Value { // the key is an index or a map key
					edge(rootName(l), n.X, true)
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
						add(id.Name, seedDef{kind: defZero, cnst: gd.Tok == token.CONST}, true)
					case len(vs.Values) == len(vs.Names):
						add(id.Name, seedDef{kind: defExpr, x: vs.Values[i]}, true)
						edge(id.Name, vs.Values[i], false)
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
				edge(rootName(n.Args[0]), n.Args[1], true)
				break
			}
			if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok && id.Name == "append" && len(n.Args) > 0 {
				for j, a := range n.Args[1:] { // the first operand's array receives the others
					edge(rootName(n.Args[0]), a, n.Ellipsis.IsValid() && j == len(n.Args)-2)
				}
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
	for _, e := range edges {
		for _, r := range storageRoots(file, env, e.x, e.elems) {
			env.union(e.name, r)
		}
	}
	return env
}

// bytesCopies are the bytes functions whose result never shares an operand's array.
var bytesCopies = map[string]bool{
	"Clone": true, "Join": true, "Map": true, "Repeat": true, "Replace": true, "ReplaceAll": true,
	"Runes": true, "ToLower": true, "ToTitle": true, "ToUpper": true, "ToValidUTF8": true,
	"Compare": true, "Contains": true, "ContainsAny": true, "ContainsFunc": true, "ContainsRune": true,
	"Count": true, "Equal": true, "EqualFold": true, "HasPrefix": true, "HasSuffix": true,
	"Index": true, "IndexAny": true, "IndexByte": true, "IndexFunc": true, "IndexRune": true,
	"LastIndex": true, "LastIndexAny": true, "LastIndexByte": true, "LastIndexFunc": true,
}

// bytesNested are the bytes functions whose result holds subslices of an operand.
var bytesNested = map[string]bool{
	"Fields": true, "FieldsFunc": true, "Split": true, "SplitAfter": true, "SplitAfterN": true, "SplitN": true,
}

// storageRoots returns the variables whose storage the value of x may share, or, with
// elems, whose storage the elements of x may share (a flat x has none).
func storageRoots(file *ast.File, env *seedEnv, x ast.Expr, elems bool) []string {
	if elems && flat(file, env, x, map[seedKey]bool{}) {
		return nil
	}
	switch x := x.(type) {
	case *ast.Ident:
		if scalar(file, env, x, map[seedKey]bool{}) {
			return nil // a copy of a number or a string shares nothing
		}
		return []string{x.Name}
	case *ast.ParenExpr:
		return storageRoots(file, env, x.X, elems)
	case *ast.SliceExpr:
		return storageRoots(file, env, x.X, elems)
	case *ast.StarExpr:
		return storageRoots(file, env, x.X, elems)
	case *ast.TypeAssertExpr:
		return storageRoots(file, env, x.X, elems)
	case *ast.IndexExpr:
		return storageRoots(file, env, x.X, true)
	case *ast.SelectorExpr:
		if id, ok := ast.Unparen(x.X).(*ast.Ident); ok && importPath(file, id.Name) != "" {
			return nil // a member of another package
		}
		return storageRoots(file, env, x.X, elems)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return storageRoots(file, env, x.X, false)
		}
	case *ast.CompositeLit:
		var out []string
		for _, e := range x.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				e = kv.Value
			}
			out = append(out, storageRoots(file, env, e, false)...)
		}
		return out
	case *ast.CallExpr:
		return callRoots(file, env, x)
	}
	return nil // a literal, an operator, a function literal: fresh or scalar
}

// callRoots returns the variables whose storage the result of call may share.
func callRoots(file *ast.File, env *seedEnv, call *ast.CallExpr) []string {
	var out []string
	operands := func(from int) []string {
		for _, a := range call.Args[from:] {
			out = append(out, storageRoots(file, env, a, false)...)
		}
		return out
	}
	switch fn := ast.Unparen(call.Fun).(type) {
	case *ast.ArrayType: // a slice-to-slice conversion shares its operand
		return operands(0)
	case *ast.Ident:
		switch {
		case fn.Name == "append" && len(call.Args) > 0:
			out = storageRoots(file, env, call.Args[0], false)
			for j, a := range call.Args[1:] {
				out = append(out, storageRoots(file, env, a, call.Ellipsis.IsValid() && j == len(call.Args)-2)...)
			}
			return out
		case predeclaredTypes[fn.Name] || pureBuiltins[fn.Name] || fn.Name == "copy":
			return nil // a scalar, a string or a fresh allocation
		}
	case *ast.SelectorExpr:
		if id, ok := ast.Unparen(fn.X).(*ast.Ident); ok {
			if ip := importPath(file, id.Name); pureStd[ip] {
				switch {
				case ip == "bytes" && !bytesCopies[fn.Sel.Name]:
					return operands(0)
				case strings.HasPrefix(fn.Sel.Name, "Append") && len(call.Args) > 0:
					return storageRoots(file, env, call.Args[0], false)
				}
				return nil
			}
		}
		out = storageRoots(file, env, fn.X, false) // a method may return its receiver's storage
	}
	return operands(0) // any other call may return storage of its operands
}

// flat reports whether x holds no storage another variable can share: bytes,
// numbers, strings, or a slice of them.
func flat(file *ast.File, env *seedEnv, x ast.Expr, seen map[seedKey]bool) bool {
	switch x := x.(type) {
	case *ast.BasicLit, *ast.BinaryExpr:
		return true
	case *ast.UnaryExpr:
		return x.Op != token.AND
	case *ast.ParenExpr:
		return flat(file, env, x.X, seen)
	case *ast.SliceExpr:
		return flat(file, env, x.X, seen)
	case *ast.CompositeLit:
		return flatType(x.Type)
	case *ast.CallExpr:
		switch fn := ast.Unparen(x.Fun).(type) {
		case *ast.ArrayType:
			return flatType(fn)
		case *ast.Ident:
			switch fn.Name {
			case "append":
				return len(x.Args) > 0 && flat(file, env, x.Args[0], seen)
			case "make":
				return len(x.Args) > 0 && flatType(x.Args[0])
			case "len", "cap", "copy", "complex", "real", "imag", "min", "max":
				return true
			}
			return predeclaredTypes[fn.Name]
		case *ast.SelectorExpr:
			if id, ok := ast.Unparen(fn.X).(*ast.Ident); ok {
				ip := importPath(file, id.Name)
				return pureStd[ip] && !(ip == "bytes" && bytesNested[fn.Sel.Name])
			}
		}
	case *ast.Ident:
		return everyDef(env, x.Name, seen, func(d seedDef, denv *seedEnv) bool {
			switch d.kind {
			case defOpaque:
				return false
			case defType:
				return flatType(d.x)
			case defExpr:
				return d.call || d.key || flat(file, denv, d.x, seen)
			}
			return true
		})
	}
	return false
}

// scalar reports whether x is a number, a boolean or a string: a value that holds no
// storage, so copying it aliases nothing.
func scalar(file *ast.File, env *seedEnv, x ast.Expr, seen map[seedKey]bool) bool {
	switch x := x.(type) {
	case *ast.BasicLit, *ast.BinaryExpr:
		return true
	case *ast.UnaryExpr:
		return x.Op != token.AND && x.Op != token.ARROW
	case *ast.ParenExpr:
		return scalar(file, env, x.X, seen)
	case *ast.IndexExpr:
		return flat(file, env, x.X, seen) // an element of bytes, numbers or strings
	case *ast.CallExpr:
		switch fn := ast.Unparen(x.Fun).(type) {
		case *ast.Ident:
			switch fn.Name {
			case "len", "cap", "copy", "complex", "real", "imag", "min", "max":
				return true
			}
			return predeclaredTypes[fn.Name]
		case *ast.SelectorExpr:
			if id, ok := ast.Unparen(fn.X).(*ast.Ident); ok {
				switch importPath(file, id.Name) {
				case "math", "strconv", "unicode/utf8":
					return !strings.HasPrefix(fn.Sel.Name, "Append")
				case "strings":
					return !bytesNested[fn.Sel.Name]
				}
			}
		}
	case *ast.Ident:
		ok := everyDef(env, x.Name, seen, func(d seedDef, denv *seedEnv) bool {
			switch d.kind {
			case defZero:
				return d.cnst
			case defType:
				id, ok := ast.Unparen(d.x).(*ast.Ident)
				return ok && predeclaredTypes[id.Name]
			case defExpr:
				switch {
				case d.call || d.key:
					return true
				case d.elem:
					return flat(file, denv, d.x, seen)
				}
				return scalar(file, denv, d.x, seen)
			}
			return false
		})
		if ok {
			return true
		}
		if _, found := identDefs(env, x.Name); !found {
			switch x.Name {
			case "true", "false", "iota":
				return true
			}
		}
	}
	return false
}

// identDefs returns the definitions of name in env, or else of the package-level
// name of seeds_test.go.
func identDefs(env *seedEnv, name string) ([]seedDef, bool) {
	if env == nil {
		return nil, false
	}
	if env.declared[name] {
		return env.locals[name], true
	}
	if env.prov != nil {
		if defs, ok := env.prov.pkg[name]; ok {
			return defs, true
		}
	}
	return nil, false
}

// everyDef reports whether name has definitions in env and pred holds for each,
// evaluated in the definition's scope. A name on a cycle is not proved.
func everyDef(env *seedEnv, name string, seen map[seedKey]bool, pred func(seedDef, *seedEnv) bool) bool {
	defs, ok := identDefs(env, name)
	if !ok || len(defs) == 0 {
		return false
	}
	key := seedKey{env, name}
	if !env.declared[name] {
		key.env = nil
	}
	if seen[key] {
		return false
	}
	seen[key] = true
	defer delete(seen, key)
	for _, d := range defs {
		denv := d.env
		if denv == nil && env.prov != nil {
			denv = env.prov.pkgEnv
		}
		if !pred(d, denv) {
			return false
		}
	}
	return true
}

// viewExpr reports whether x only names or literally restates storage -- names,
// literals, indexing, slicing, fields, conversions and composite literals of them --
// and so cannot write into storage it shares.
func viewExpr(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident, *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return viewExpr(x.X)
	case *ast.SelectorExpr:
		return viewExpr(x.X)
	case *ast.StarExpr:
		return viewExpr(x.X)
	case *ast.IndexExpr:
		return viewExpr(x.X) && viewExpr(x.Index)
	case *ast.SliceExpr:
		for _, e := range []ast.Expr{x.X, x.Low, x.High, x.Max} {
			if e != nil && !viewExpr(e) {
				return false
			}
		}
		return true
	case *ast.UnaryExpr:
		return viewExpr(x.X)
	case *ast.BinaryExpr:
		return viewExpr(x.X) && viewExpr(x.Y)
	case *ast.CompositeLit:
		for _, e := range x.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if !viewExpr(kv.Key) {
					return false
				}
				e = kv.Value
			}
			if !viewExpr(e) {
				return false
			}
		}
		return true
	case *ast.CallExpr:
		switch fn := ast.Unparen(x.Fun).(type) {
		case *ast.ArrayType:
		case *ast.Ident:
			if !predeclaredTypes[fn.Name] && fn.Name != "len" && fn.Name != "cap" {
				return false
			}
		default:
			return false
		}
		return len(x.Args) == 1 && viewExpr(x.Args[0])
	}
	return false
}

// flatType reports whether t is a predeclared type or a slice or array of one.
func flatType(t ast.Expr) bool {
	switch t := ast.Unparen(t).(type) {
	case *ast.Ident:
		return predeclaredTypes[t.Name]
	case *ast.ArrayType:
		id, ok := ast.Unparen(t.Elt).(*ast.Ident)
		return ok && predeclaredTypes[id.Name]
	}
	return false
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
