package acceptance

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Directive marks a scenario test with the scenario it is:
//
//	//dsh:scenario DH-E-TOOLCHAIN-2
//	func TestToolchainRehearsal(t *testing.T) { ... }
//
// It belongs to the doc comment of a top-level TestXxx function of a
// *_scenario_test.go file.
const Directive = "//dsh:scenario"

// FileSuffix names the files of the scenario layer; their build constraint is
// exactly BuildTag (DH-ADR-TOOLCHAIN-4).
const (
	FileSuffix = "_scenario_test.go"
	BuildTag   = "scenario"
)

// Scenario is one authored scenario test.
type Scenario struct {
	ID   string // DH-E-...
	File string // slash path relative to the scanned root
	Test string // TestXxx
}

// ScanScenarios returns the scenario tests of every *_scenario_test.go file below the
// module directories dirs, File relative to root. testdata, vendor, hidden
// directories and nested modules are skipped. A scenario file without the exact
// build constraint, without a directive, or with a directive anywhere but on a
// TestXxx function is an error: an ID the index cannot see is never silently lost.
func ScanScenarios(root string, dirs []string) ([]Scenario, error) {
	var out []Scenario
	var errs []error
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if p != dir && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || isFile(filepath.Join(p, "go.mod"))) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), FileSuffix) {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			found, err := ParseScenarioFile(filepath.ToSlash(rel), src)
			if err != nil {
				errs = append(errs, err)
				return nil
			}
			out = append(out, found...)
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

// ParseScenarioFile returns the scenario tests src declares; name is the file's
// slash path, used in the results and errors.
func ParseScenarioFile(name string, src []byte) ([]Scenario, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if err := checkConstraint(name, f); err != nil {
		return nil, err
	}
	attached := map[*ast.Comment]string{}
	var out []Scenario
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		for _, c := range fn.Doc.List {
			id, ok := directiveID(c.Text)
			if !ok {
				continue
			}
			attached[c] = fn.Name.Name
			if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				return nil, fmt.Errorf("%s: %s %s is not on a TestXxx function", fset.Position(c.Pos()), Directive, id)
			}
			if !scenarioPattern.MatchString(id) {
				return nil, fmt.Errorf("%s: %s %q is not a scenario ID (DH-E-<AREA>-<n>)", fset.Position(c.Pos()), Directive, id)
			}
			out = append(out, Scenario{ID: id, File: name, Test: fn.Name.Name})
		}
	}
	for _, g := range f.Comments {
		for _, c := range g.List {
			if id, ok := directiveID(c.Text); ok {
				if _, on := attached[c]; !on {
					return nil, fmt.Errorf("%s: %s %s is not on a TestXxx function", fset.Position(c.Pos()), Directive, id)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no %s directive names the scenario this file authors", name, Directive)
	}
	return out, nil
}

func directiveID(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, Directive)
	if !ok || (rest != "" && rest[0] != ' ') {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// checkConstraint requires the file's build constraint to be exactly "scenario", so
// the keyless gate never compiles it into a test run (DH-ADR-TOOLCHAIN-4).
func checkConstraint(name string, f *ast.File) error {
	for _, g := range f.Comments {
		if g.Pos() > f.Package {
			break
		}
		for _, c := range g.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if expr.String() != BuildTag {
				return fmt.Errorf("%s: build constraint is %q, want exactly //go:build %s", name, expr.String(), BuildTag)
			}
			return nil
		}
	}
	return fmt.Errorf("%s: no //go:build %s constraint: the default gate would run it", name, BuildTag)
}

// Check reports every disagreement between the index and the authored scenarios:
// a scenario test whose ID is not indexed, an ID indexed more than once, an ID two
// tests claim, and an indexed ID authored at or before the index's built-through
// stage that has no scenario test.
func Check(x *Index, scenarios []Scenario) error {
	built, _ := StageNumber(x.BuiltThrough)
	where := map[string][]string{}
	var order []string
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			if _, ok := where[e.ID]; !ok {
				order = append(order, e.ID)
			}
			where[e.ID] = append(where[e.ID], s.ID)
		}
	}
	files := map[string][]string{}
	for _, s := range scenarios {
		files[s.ID] = append(files[s.ID], s.File+": "+s.Test)
	}
	var errs []error
	for _, id := range order {
		if stages := where[id]; len(stages) > 1 {
			errs = append(errs, fmt.Errorf("%s is indexed %d times (%s); a scenario runs in exactly one stage", id, len(stages), strings.Join(stages, ", ")))
		}
	}
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if len(files[id]) > 1 {
			errs = append(errs, fmt.Errorf("%s is authored by %d tests (%s)", id, len(files[id]), strings.Join(files[id], "; ")))
		}
		if _, ok := where[id]; !ok {
			errs = append(errs, fmt.Errorf("%s (%s) is not in %s", id, files[id][0], IndexPath))
		}
	}
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			n, _ := StageNumber(e.Authored)
			if n <= built && len(files[e.ID]) == 0 {
				errs = append(errs, fmt.Errorf("%s is indexed under %s, authored at %s (built through %s), and has no scenario test", e.ID, s.ID, e.Authored, x.BuiltThrough))
			}
		}
	}
	return errors.Join(errs...)
}

// The import paths a scenario authored ahead of the stages it requires may use:
// the standard library, the client-facing SDK and the test kit's scenario
// launcher. Such a scenario asserts behaviour that does not exist yet; reaching
// into the product's internal packages would let it substitute that behaviour
// (a title, a loop) for what the product boundary returns.
const (
	productModule  = "github.com/smartpcr/agent-plugin/"
	sdkPrefix      = productModule + "src/sdk/"
	launcherImport = productModule + "src/harness/internal/testkit/launcher"
)

// BoundaryImport reports whether a scenario with a requires list may import path.
func BoundaryImport(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".") || strings.HasPrefix(path, sdkPrefix) || path == launcherImport
}

// CheckBoundary reads, under root, the file of every authored scenario whose index
// entry has a requires list, and fails for each import BoundaryImport refuses. It
// then checks the index against the plan under root (CheckPlan): the built-through
// marker against impl.md's ticks, and rule 4's authoring. The scenario-index gate
// calls it, so both plan checks are part of that gate (13.6.4, 13.6.6).
func CheckBoundary(root string, x *Index, scenarios []Scenario) error {
	return errors.Join(checkImports(root, x, scenarios), CheckPlan(root, x))
}

// checkImports is the import half of CheckBoundary.
func checkImports(root string, x *Index, scenarios []Scenario) error {
	gated := map[string]bool{}
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			if len(e.Requires) > 0 {
				gated[e.ID] = true
			}
		}
	}
	var errs []error
	for _, s := range scenarios {
		if !gated[s.ID] {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(s.File)), nil, parser.ImportsOnly)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s.File, err))
				continue
			}
			if !BoundaryImport(path) {
				errs = append(errs, fmt.Errorf("%s (%s) imports %s: a scenario authored ahead of the stages it requires imports only the standard library, src/sdk/... and the test kit's launcher", s.ID, s.File, path))
			}
		}
	}
	return errors.Join(errs...)
}

// RequireEnv is the environment variable the preflight reads: the comma- or
// space-separated scenario IDs the run must execute. The preflight reads no model
// key: under decision models-signed-in-claude-copilot real-model checks use the
// user's signed-in Claude or Copilot session, never an API key (M1-G-TOOL-15). The
// session a scenario needs is its index entry's credential, checked by the sign-in
// probe (M1-G-TOOL-16; signin.go).
const RequireEnv = "DSH_REQUIRE_SCENARIOS"

// Plan is what the scenario job runs after a passing preflight, and the sign-ins
// the preflight checked for it.
type Plan struct {
	Scenarios []Scenario
	SignIns   []SignIn
}

// RunPattern is the go test -run expression that selects exactly the plan's tests.
func (p Plan) RunPattern() string {
	if len(p.Scenarios) == 0 {
		return ""
	}
	names := make([]string, 0, len(p.Scenarios))
	for _, s := range p.Scenarios {
		names = append(names, regexp.QuoteMeta(s.Test))
	}
	return "^(" + strings.Join(names, "|") + ")$"
}

// Tests returns the plan's test names in order.
func (p Plan) Tests() []string {
	names := make([]string, 0, len(p.Scenarios))
	for _, s := range p.Scenarios {
		names = append(names, s.Test)
	}
	return names
}

// SplitRequired splits a DSH_REQUIRE_SCENARIOS value on commas and white space.
func SplitRequired(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}

// Preflight is PreflightWith with no sign-in probe: a planned scenario whose
// entry needs a signed-in session fails, named, so nothing that needs a sign-in
// starts unchecked. The gate's preflight and scenarios subcommands pass the
// client status probe (StatusProbe).
func Preflight(env map[string]string, x *Index, scenarios []Scenario) (Plan, error) {
	return PreflightWith(context.Background(), env, x, scenarios, nil)
}

// PreflightWith decides whether the credentialed scenario run may start
// (DH-ADR-TOOLCHAIN-6, -12): the index must agree with the authored scenarios,
// every ID of DSH_REQUIRE_SCENARIOS must have its scenario test and every stage
// its index entry requires must be built, and then the client of every planned
// scenario whose credential is not none must be signed in on this host, checked
// once per credential through probe (M1-G-TOOL-16). No model key is read
// (models-signed-in-claude-copilot). On any failure the plan is empty, so no
// scenario is started; every reason is named, a sign-in failure with the
// scenarios and the client. An empty required list passes with an empty plan and
// probes nothing.
func PreflightWith(ctx context.Context, env map[string]string, x *Index, scenarios []Scenario, probe SignInProbe) (Plan, error) {
	var errs []error
	if err := Check(x, scenarios); err != nil {
		errs = append(errs, err)
	}
	var plan Plan
	for _, id := range SplitRequired(env[RequireEnv]) {
		i := slices.IndexFunc(scenarios, func(s Scenario) bool { return s.ID == id })
		if i < 0 {
			errs = append(errs, fmt.Errorf("required scenario %s (%s) has no scenario test", id, RequireEnv))
			continue
		}
		if !slices.ContainsFunc(plan.Scenarios, func(s Scenario) bool { return s.ID == id }) {
			if err := requiresBuilt(x, id); err != nil {
				errs = append(errs, err)
				continue
			}
			plan.Scenarios = append(plan.Scenarios, scenarios[i])
		}
	}
	signIns, err := checkSignIns(ctx, x, plan.Scenarios, probe)
	if err != nil {
		errs = append(errs, fmt.Errorf("credentials: %w", err))
	}
	if err := errors.Join(errs...); err != nil {
		return Plan{SignIns: signIns}, err
	}
	plan.SignIns = signIns
	return plan, nil
}

// requiresBuilt fails when the index entry of scenario id requires a stage after
// the index's built-through: the behaviour the scenario asserts does not exist yet.
func requiresBuilt(x *Index, id string) error {
	built, _ := StageNumber(x.BuiltThrough)
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			if e.ID != id {
				continue
			}
			var missing []string
			for _, req := range e.Requires {
				if n, _ := StageNumber(req); n > built {
					missing = append(missing, req)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("required scenario %s requires %s, not built (built through %s)", id, strings.Join(missing, ", "), x.BuiltThrough)
			}
		}
	}
	return nil
}

// Environ turns "KEY=value" pairs into the map Preflight reads.
func Environ(kv []string) map[string]string {
	env := make(map[string]string, len(kv))
	for _, pair := range kv {
		if k, v, ok := strings.Cut(pair, "="); ok && k != "" {
			env[k] = v
		}
	}
	return env
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
