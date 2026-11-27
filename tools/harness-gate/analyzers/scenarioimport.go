package analyzers

import (
	"go/ast"
	"go/build/constraint"
	"go/types"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ScenarioImport reports, in a scenario test file, an import of a
// harness-internal package other than the test kit's scenario helper and
// launcher: a scenario drives the built binary and src/sdk/..., never the
// harness's internals (architecture.md sec. 3.4 rule 5; DH-D-UTIL-8, C55).
var ScenarioImport = newAnalyzer("scenarioimport",
	"forbid scenario files importing src/harness/internal/... except the test kit scenario helper and launcher (DH-D-UTIL-8)",
	ScenarioImportFindings)

// ScenarioPackage is the scenario helper; a test file importing it is a scenario
// file.
const ScenarioPackage = "internal/testkit/scenario"

// ScenariosDir is the scenario package; every test file in it is a scenario
// file.
const ScenariosDir = "internal/testkit/scenarios/"

// ScenarioAllowed are the harness-internal packages a scenario file may import.
var ScenarioAllowed = []string{ScenarioPackage, "internal/testkit/launcher"}

// ScenarioLegacy are the scenario files written before the helper that still
// import harness internals. Stage 6.8 retargets scenarios without rewriting
// their bodies (architecture.md I-2), so each is exempt by name until it moves
// to the helper and leaves this list (DH-ADR-TOOLCHAIN-8); the list only
// shrinks, and any other scenario file is checked.
var ScenarioLegacy = []string{
	"boot_two_process_scenario_test.go",
	"durable_replace_scenario_test.go",
	"exec_windows_shells_scenario_test.go",
	"host_report_scenario_test.go",
	"loader_edit_scenario_test.go",
	"realenv_check_test.go",
	"search_scenario_test.go",
	"storage_image_scenario_test.go",
	"storage_spill_scenario_test.go",
	"storage_workspace_scenario_test.go",
	"store_crash_recovery_scenario_test.go",
	"store_deletion_scenario_test.go",
	"store_follow_request_scenario_test.go",
	"store_two_process_scenario_test.go",
	"takeover_check_test.go",
	"toolchain_rehearsal_scenario_test.go",
	"watch_scenario_test.go",
}

// ScenarioImportFindings is the scenarioimport predicate. A scenario file is a
// test file that imports the scenario helper, carries the scenario build tag,
// or lies in the scenario package (ScenariosDir); a ScenarioLegacy file there
// is exempt.
func ScenarioImportFindings(filename string, file *ast.File, _ *types.Info) []Finding {
	if !isTestFile(filename) {
		return nil
	}
	slash := filepath.ToSlash(filename)
	inDir := strings.Contains("/"+slash, "/"+ScenariosDir)
	if inDir && slices.Contains(ScenarioLegacy, path.Base(slash)) {
		return nil
	}
	internal := map[*ast.ImportSpec]string{}
	scenario := inDir || scenarioTagged(file)
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if rel, ok := harnessInternal(p); ok {
			internal[imp] = rel
			scenario = scenario || rel == ScenarioPackage
		}
	}
	if !scenario {
		return nil
	}
	var out []Finding
	for _, imp := range file.Imports {
		if rel, ok := internal[imp]; ok && !slices.Contains(ScenarioAllowed, rel) {
			out = append(out, Finding{Pos: imp.Pos(), Message: "scenario file imports " + rel + ": a scenario imports only " + strings.Join(ScenarioAllowed, " and ") + ", the standard library and src/sdk/... (DH-D-UTIL-8)"})
		}
	}
	return out
}

// scenarioTagged reports whether file's //go:build line names the scenario tag.
func scenarioTagged(file *ast.File) bool {
	for _, g := range file.Comments {
		if g.Pos() >= file.Package {
			break
		}
		for _, c := range g.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				continue
			}
			named := false
			expr.Eval(func(tag string) bool {
				named = named || tag == "scenario"
				return true
			})
			if named {
				return true
			}
		}
	}
	return false
}

// harnessInternal is p relative to the harness module when p is one of its
// internal packages ("internal/..."): a path of the harness module itself
// (".../src/harness/internal/...") or a bare one ("internal/...").
func harnessInternal(p string) (string, bool) {
	const mod = "/src/harness/"
	if i := strings.Index(p, mod+"internal/"); i >= 0 {
		return p[i+len(mod):], true
	}
	if strings.HasPrefix(p, "internal/") {
		return p, true
	}
	return "", false
}
