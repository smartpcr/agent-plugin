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
// child process) are not run-time inputs. root is the directory the scenario files
// are relative to.
func Inputs(root string, scenarios []acceptance.Scenario, tests []string) (map[string][]string, error) {
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
		out[sc.Test] = p.inputsOf(sc.Test)
	}
	for _, t := range tests {
		if _, ok := out[t]; !ok {
			return nil, fmt.Errorf("no scenario test %s", t)
		}
	}
	return out, nil
}

// InputsFor returns the sorted union of the run-time inputs of the scenario tests
// that prove ids (the variant's required scenarios); an ID no scenario test proves
// is an error, as in acceptance.Preflight.
func InputsFor(root string, scenarios []acceptance.Scenario, ids []string) ([]string, error) {
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
	per, err := Inputs(root, scenarios, tests)
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
}

func loadInputPkg(dir string) (*inputPkg, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	p := &inputPkg{funcs: map[string]*ast.FuncDecl{}, consts: map[string]string{}, set: map[string]bool{}, forwards: map[string][]int{}}
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
				if d.Recv == nil && d.Body != nil {
					p.funcs[d.Name.Name] = d
				}
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if i < len(vs.Values) {
							if v, ok := stringLit(vs.Values[i]); ok {
								p.consts[n.Name] = v
							}
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

func (p *inputPkg) inputsOf(test string) []string {
	seen := map[string]bool{test: true}
	queue := []string{test}
	names := map[string]bool{}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if is, ok := n.(*ast.IfStmt); ok && roleGuard(is.Cond) {
			if is.Else != nil {
				ast.Inspect(is.Else, visit)
			}
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, ai := range p.nameArgs(call) {
			if v, ok := p.value(call.Args[ai]); ok && strings.HasPrefix(v, InputPrefix) && !p.set[v] {
				names[v] = true
			}
		}
		if id, ok := call.Fun.(*ast.Ident); ok && p.funcs[id.Name] != nil && !seen[id.Name] {
			seen[id.Name] = true
			queue = append(queue, id.Name)
		}
		return true
	}
	for len(queue) > 0 {
		fd := p.funcs[queue[0]]
		queue = queue[1:]
		ast.Inspect(fd.Body, visit)
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (p *inputPkg) value(e ast.Expr) (string, bool) {
	if v, ok := stringLit(e); ok {
		return v, true
	}
	if id, ok := e.(*ast.Ident); ok {
		v, ok := p.consts[id.Name]
		return v, ok
	}
	return "", false
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
