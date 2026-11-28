package variants

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// InputPrefix names the run-time inputs an operator sets for a scenario run.
const InputPrefix = "DSH_SCENARIO_"

// Inputs returns, per scenario test of tests (TestXxx names of scenarios), the
// sorted run-time inputs it requires: every DSH_SCENARIO_* name the test, or a
// function of its package it calls, passes to need, needList or scenario.Need
// (realenv_test.go), directly, through a package constant, or through a helper
// that forwards one of its parameters as the name. Names the package sets itself
// ("DSH_SCENARIO_X=..." literals: the inputs of a role a scenario starts in a
// child process) are not run-time inputs, nor those of a branch on runtime.GOOS that
// goos (the variant's machine; "" for any) never takes. Input names may be built
// with + from constants and the string fields of a package value whose method
// reads them (clientRoute.model). root is the directory the scenario files are
// relative to.
func Inputs(root string, scenarios []acceptance.Scenario, tests []string, goos string) (map[string][]string, error) {
	want := map[string]bool{}
	for _, t := range tests {
		want[t] = true
	}
	pkgs := map[string]*inputPkg{}
	out := map[string][]string{}
	for _, sc := range scenarios {
		if !want[sc.Test] {
			continue
		}
		dir := path.Dir(sc.File)
		p, ok := pkgs[dir]
		if !ok {
			var err error
			if p, err = loadInputPkg(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
				return nil, err
			}
			pkgs[dir] = p
		}
		if p.funcs[sc.Test] == nil {
			return nil, fmt.Errorf("%s: no function %s", sc.File, sc.Test)
		}
		out[sc.Test] = p.inputsOf(sc.Test, goos)
	}
	for _, t := range tests {
		if _, ok := out[t]; !ok {
			return nil, fmt.Errorf("no scenario test %s", t)
		}
	}
	return out, nil
}

// InputsFor returns the sorted union of the run-time inputs of the scenario tests
// that prove ids (the variant's required scenarios) on goos; an ID no scenario test proves
// is an error, as in acceptance.Preflight.
func InputsFor(root string, scenarios []acceptance.Scenario, ids []string, goos string) ([]string, error) {
	var tests []string
	for _, id := range ids {
		found := false
		for _, sc := range scenarios {
			if sc.ID == id {
				tests = append(tests, sc.Test)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s: no scenario test proves it", id)
		}
	}
	per, err := Inputs(root, scenarios, tests, goos)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, names := range per {
		for _, n := range names {
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

type inputPkg struct {
	funcs  map[string]*ast.FuncDecl
	consts map[string]string
	set    map[string]bool
	// forwards[f] lists the parameter indices of f whose value is an input name.
	forwards map[string][]int
	// methods[T][m] is method m of type T; vars[v] is a package variable of a
	// struct type initialised by a composite literal, with its string fields.
	methods map[string]map[string]*ast.FuncDecl
	vars    map[string]structVar
}

type structVar struct {
	typ    string
	fields map[string]string
}

func loadInputPkg(dir string) (*inputPkg, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	p := &inputPkg{funcs: map[string]*ast.FuncDecl{}, consts: map[string]string{}, set: map[string]bool{},
		forwards: map[string][]int{}, methods: map[string]map[string]*ast.FuncDecl{}, vars: map[string]structVar{}}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				if d.Recv == nil {
					p.funcs[d.Name.Name] = d
				} else if t := recvType(d); t != "" {
					if p.methods[t] == nil {
						p.methods[t] = map[string]*ast.FuncDecl{}
					}
					p.methods[t][d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					vs, ok := s.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, n := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if v, ok := stringLit(vs.Values[i]); ok && d.Tok == token.CONST {
							p.consts[n.Name] = v
						}
						if sv, ok := structLit(vs.Values[i]); ok && d.Tok == token.VAR {
							p.vars[n.Name] = sv
						}
					}
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if v, ok := stringLit(n); ok && strings.HasPrefix(v, InputPrefix) {
				if name, _, found := strings.Cut(v, "="); found {
					p.set[name] = true
				}
			}
			return true
		})
	}
	p.forwards["need"] = []int{1}
	p.forwards["needList"] = []int{1}
	p.findForwards()
	return p, nil
}

func recvType(fd *ast.FuncDecl) string {
	if len(fd.Recv.List) != 1 {
		return ""
	}
	t := fd.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func recvName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) != 1 || len(fd.Recv.List[0].Names) != 1 {
		return ""
	}
	return fd.Recv.List[0].Names[0].Name
}

// structLit reads T{F: "lit", ...}: the type and its string-literal fields.
func structLit(e ast.Expr) (structVar, bool) {
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		return structVar{}, false
	}
	id, ok := cl.Type.(*ast.Ident)
	if !ok {
		return structVar{}, false
	}
	sv := structVar{typ: id.Name, fields: map[string]string{}}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if v, ok := stringLit(kv.Value); ok {
			sv.fields[k.Name] = v
		}
	}
	return sv, true
}

// findForwards marks, to a fixpoint, each function that passes one of its
// parameters as the name of an input-reading call.
func (p *inputPkg) findForwards() {
	for changed := true; changed; {
		changed = false
		for name, fd := range p.funcs {
			if name == "need" || name == "needList" {
				continue
			}
			params := paramIndex(fd)
			have := map[int]bool{}
			for _, i := range p.forwards[name] {
				have[i] = true
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				for _, ai := range p.nameArgs(call) {
					if id, ok := call.Args[ai].(*ast.Ident); ok {
						if pi, ok := params[id.Name]; ok && !have[pi] {
							have[pi] = true
							p.forwards[name] = append(p.forwards[name], pi)
							changed = true
						}
					}
				}
				return true
			})
		}
	}
}

// nameArgs returns the argument indices of call that are input names.
func (p *inputPkg) nameArgs(call *ast.CallExpr) []int {
	var idx []int
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		idx = p.forwards[fn.Name]
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok && x.Name == "scenario" && fn.Sel.Name == "Need" {
			idx = []int{1}
		}
	}
	var out []int
	for _, i := range idx {
		if i < len(call.Args) {
			out = append(out, i)
		}
	}
	return out
}

// work is one function body to walk: a function, or a method bound to the
// string fields of one value of its type (recv names the receiver in the body).
type work struct {
	fd     *ast.FuncDecl
	recv   string
	fields map[string]string
}

// inputsOf walks test and every package function and method it reaches. goos
// ("linux", "windows", "darwin"; "" for any) prunes branches on runtime.GOOS the
// variant's machine never takes.
func (p *inputPkg) inputsOf(test, goos string) []string {
	seen := map[string]bool{test: true}
	queue := []work{{fd: p.funcs[test]}}
	names := map[string]bool{}
	enqueue := func(key string, w work) {
		if !seen[key] {
			seen[key] = true
			queue = append(queue, w)
		}
	}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		locals := goosLocals(w.fd.Body)
		var visit func(n ast.Node) bool
		visit = func(n ast.Node) bool {
			if is, ok := n.(*ast.IfStmt); ok {
				if roleGuard(is.Cond) {
					if is.Else != nil {
						ast.Inspect(is.Else, visit)
					}
					return false
				}
				if v, known := goosCond(is.Cond, goos, locals); known {
					if is.Init != nil {
						ast.Inspect(is.Init, visit)
					}
					if v {
						ast.Inspect(is.Body, visit)
					} else if is.Else != nil {
						ast.Inspect(is.Else, visit)
					}
					return false
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, ai := range p.nameArgs(call) {
				if v, ok := p.value(call.Args[ai], w); ok && strings.HasPrefix(v, InputPrefix) && !p.set[v] {
					names[v] = true
				}
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fd := p.funcs[fn.Name]; fd != nil {
					enqueue(fn.Name, work{fd: fd})
				}
			case *ast.SelectorExpr:
				x, ok := fn.X.(*ast.Ident)
				if !ok {
					break
				}
				if sv, ok := p.vars[x.Name]; ok {
					if fd := p.methods[sv.typ][fn.Sel.Name]; fd != nil {
						enqueue(sv.typ+"."+fn.Sel.Name+"@"+x.Name, work{fd: fd, recv: recvName(fd), fields: sv.fields})
					}
					break
				}
				if x.Name == w.recv && w.fields != nil {
					if fd := p.methods[recvType(w.fd)][fn.Sel.Name]; fd != nil {
						enqueue(recvType(w.fd)+"."+fn.Sel.Name+"@"+fmt.Sprint(w.fields), work{fd: fd, recv: recvName(fd), fields: w.fields})
					}
					break
				}
				// A method on a value not known here (a loop or parameter value):
				// every package value of the one type with that method.
				for typ, ms := range p.methods {
					fd := ms[fn.Sel.Name]
					if fd == nil {
						continue
					}
					for vn, sv := range p.vars {
						if sv.typ == typ {
							enqueue(typ+"."+fn.Sel.Name+"@"+vn, work{fd: fd, recv: recvName(fd), fields: sv.fields})
						}
					}
				}
			}
			return true
		}
		ast.Inspect(w.fd.Body, visit)
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// value evaluates a constant input name: a literal, a package constant, the
// receiver's string field, or their concatenation with +.
func (p *inputPkg) value(e ast.Expr, w work) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		return stringLit(e)
	case *ast.Ident:
		v, ok := p.consts[e.Name]
		return v, ok
	case *ast.ParenExpr:
		return p.value(e.X, w)
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		if x.Name == w.recv && w.fields != nil {
			v, ok := w.fields[e.Sel.Name]
			return v, ok
		}
		if sv, ok := p.vars[x.Name]; ok {
			v, ok := sv.fields[e.Sel.Name]
			return v, ok
		}
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, ok := p.value(e.X, w)
		if !ok {
			return "", false
		}
		r, ok := p.value(e.Y, w)
		return l + r, ok
	}
	return "", false
}

// goosLocals collects the function's "x := <runtime.GOOS condition>" variables.
func goosLocals(body *ast.BlockStmt) map[string]ast.Expr {
	out := map[string]ast.Expr{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				if _, known := goosCond(as.Rhs[i], "linux", nil); known {
					out[id.Name] = as.Rhs[i]
				}
			}
		}
		return true
	})
	return out
}

// goosCond evaluates a condition built of runtime.GOOS ==/!= "os", !, && and ||
// (and locals holding one) for goos; known is false for anything else or goos "".
func goosCond(e ast.Expr, goos string, locals map[string]ast.Expr) (v, known bool) {
	if goos == "" {
		return false, false
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return goosCond(e.X, goos, locals)
	case *ast.Ident:
		if x, ok := locals[e.Name]; ok {
			return goosCond(x, goos, nil)
		}
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			v, known := goosCond(e.X, goos, locals)
			return !v, known
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND, token.LOR:
			l, lk := goosCond(e.X, goos, locals)
			r, rk := goosCond(e.Y, goos, locals)
			if e.Op == token.LAND {
				if (lk && !l) || (rk && !r) {
					return false, true
				}
				return l && r, lk && rk
			}
			if (lk && l) || (rk && r) {
				return true, true
			}
			return l || r, lk && rk
		case token.EQL, token.NEQ:
			sel, ok := e.X.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "GOOS" {
				return false, false
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "runtime" {
				return false, false
			}
			os, ok := stringLit(e.Y)
			if !ok {
				return false, false
			}
			return (os == goos) == (e.Op == token.EQL), true
		}
	}
	return false, false
}

// roleGuard reports whether cond is os.Getenv("DSH_SCENARIO_..._ROLE") == "...":
// the branch of a role the scenario starts in another process or machine.
func roleGuard(cond ast.Expr) bool {
	be, ok := cond.(*ast.BinaryExpr)
	if !ok || be.Op != token.EQL {
		return false
	}
	call, ok := be.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Getenv" {
		return false
	}
	if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "os" {
		return false
	}
	name, ok := stringLit(call.Args[0])
	return ok && strings.HasPrefix(name, InputPrefix) && strings.HasSuffix(name, "_ROLE")
}

func paramIndex(fd *ast.FuncDecl) map[string]int {
	out := map[string]int{}
	i := 0
	for _, f := range fd.Type.Params.List {
		if len(f.Names) == 0 {
			i++
			continue
		}
		for _, n := range f.Names {
			out[n.Name] = i
			i++
		}
	}
	return out
}

func stringLit(n ast.Node) (string, bool) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}
