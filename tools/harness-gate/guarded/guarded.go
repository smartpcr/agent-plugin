// Package guarded plans the guarded run of a build stage's real-environment
// scenarios on one gate lane (harness:DSH-M2 step 16.8, implementation-plan Stage
// 6.12; M2-C-1, M2-EN-6): which of the stage's indexed scenarios the lane runs and
// which it records out of scope by a named decision, the run-time inputs each
// planned scenario reads, and the check that a planned scenario's index entry
// requires nothing past built-through but the stage being run. It starts nothing;
// "harness-gate guarded" (guardedrun.go) runs the plan.
package guarded

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// Lane is a gate lane (tools/harness-gate/lanes).
type Lane string

const (
	// WSL is lanes/wsl.sh: Linux under WSL.
	WSL Lane = "wsl"
	// Windows is lanes/win.ps1: Windows native.
	Windows Lane = "windows"
)

// ParseLane reads a --lane value.
func ParseLane(s string) (Lane, error) {
	switch l := Lane(strings.ToLower(strings.TrimSpace(s))); l {
	case WSL, Windows:
		return l, nil
	}
	return "", fmt.Errorf("lane %q is not %s or %s", s, WSL, Windows)
}

// Variant is the lane's Variant cell in a stage record's Scenarios table.
func (l Lane) Variant() string {
	if l == Windows {
		return "Windows"
	}
	return "Linux (WSL)"
}

// LinuxOnly maps each scenario that runs on the Linux lane only to the decision
// its Windows row names (impl.md 16.8: DH-E-STORE-2 needs the Claude route and
// bwrap; a Windows STORE-2 belongs to DH-S-27). The text is one of
// closure.ScopeDecisions.
var LinuxOnly = map[string]string{
	"DH-E-STORE-2": "impl.md 16.8 Linux only; TC-19s2",
}

// Excluded is a required scenario the lane does not run, and the decision that
// puts it out of scope there.
type Excluded struct {
	ID       string
	Decision string
}

// Result is the excluded row's Result cell.
func (e Excluded) Result() string { return "out of scope (" + e.Decision + ")" }

// Select splits the stage's required IDs into those lane runs, in order, and
// those it records out of scope.
func Select(lane Lane, ids []string) (run []string, out []Excluded) {
	for _, id := range ids {
		if d, ok := LinuxOnly[id]; ok && lane != WSL {
			out = append(out, Excluded{ID: id, Decision: d})
			continue
		}
		run = append(run, id)
	}
	return run, out
}

// RequiresBuilt fails, naming each, a planned scenario whose index entry requires
// a stage past built-through other than stage itself. A guarded run executes
// inside its build stage, which is not built through until its guarded rows pass
// (impl.md sec. 7.4), so the executing stage is the one requirement it may name
// unbuilt; every other one must be built.
func RequiresBuilt(x *acceptance.Index, stage string, ids []string) error {
	built, _ := acceptance.StageNumber(x.BuiltThrough)
	var errs []error
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			if !slices.Contains(ids, e.ID) {
				continue
			}
			var missing []string
			for _, req := range e.Requires {
				if n, _ := acceptance.StageNumber(req); n > built && req != stage {
					missing = append(missing, req)
				}
			}
			if len(missing) > 0 {
				errs = append(errs, fmt.Errorf("%s requires %s, not built (built through %s)", e.ID, strings.Join(missing, ", "), x.BuiltThrough))
			}
		}
	}
	return errors.Join(errs...)
}

// routeInputs maps a signed-in route variable of the scenario package
// (signedin_routes_test.go) to the infix of its DSH_SCENARIO_<infix>_MODEL and
// _EFFORT inputs.
var routeInputs = map[string]string{
	"claudeCodeRoute": "CLAUDE_CODE",
	"copilotCLIRoute": "COPILOT_CLI",
}

// Inputs returns, sorted, the run-time inputs the body of test in the Go source
// src reads: the name of every need(t, "NAME", ...) call and, for each signed-in
// route whose model or effort the body reads, DSH_SCENARIO_<route>_MODEL or
// _EFFORT. A scenario reads its inputs first and fails on a missing one, so the
// guarded preflight reads the same names and names every missing one before any
// scenario starts.
func Inputs(name string, src []byte, test string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var body *ast.BlockStmt
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == test {
			body = fn.Body
		}
	}
	if body == nil {
		return nil, fmt.Errorf("%s: no func %s", name, test)
	}
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == "need" && len(call.Args) >= 2 {
				if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil {
						add(v)
					}
				}
			}
		case *ast.SelectorExpr:
			if recv, ok := fun.X.(*ast.Ident); ok {
				if infix, ok := routeInputs[recv.Name]; ok && (fun.Sel.Name == "model" || fun.Sel.Name == "effort") {
					add("DSH_SCENARIO_" + infix + "_" + strings.ToUpper(fun.Sel.Name))
				}
			}
		}
		return true
	})
	slices.Sort(names)
	return names, nil
}

// Missing returns the inputs that lookup reports unset or blank.
func Missing(inputs []string, lookup func(string) (string, bool)) []string {
	var out []string
	for _, n := range inputs {
		if v, ok := lookup(n); !ok || strings.TrimSpace(v) == "" {
			out = append(out, n)
		}
	}
	return out
}

// Programs maps a run-time input that names a program the scenario starts
// directly (exec, not a shell line) to the lane service it stands for; the
// preflight fails naming that service when the program does not resolve
// (e2e-scenarios.md Phase 7: a missing editor save script fails naming
// service:git-and-editor).
var Programs = map[string]string{
	"DSH_SCENARIO_FS3_EDITOR": "service:git-and-editor",
}

// SignIn maps a scenario whose route is the user's signed-in client to the
// credential the guarded preflight probes before any scenario starts
// (e2e-scenarios.md Phase 7, WSL lane: claude signed in). Its index entry reads
// credential none: the keyless scenario workflow never plans it.
var SignIn = map[string]acceptance.Credential{
	"DH-E-STORE-2": acceptance.CredentialClaude,
}
