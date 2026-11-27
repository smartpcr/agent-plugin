package analyzers

import (
	"go/ast"
	"go/types"
	"slices"
	"strconv"
	"strings"
)

// ScenarioImport reports, in a test file that uses the scenario helper, an import
// of a harness-internal package other than the test kit's scenario helper and
// launcher: a scenario drives the built binary and src/sdk/..., never the
// harness's internals (architecture.md sec. 3.4 rule 5; DH-D-UTIL-8, C55).
var ScenarioImport = newAnalyzer("scenarioimport",
	"forbid a scenario file that uses internal/testkit/scenario importing other src/harness/internal/... packages (DH-D-UTIL-8)",
	ScenarioImportFindings)

// ScenarioPackage is the scenario helper; a test file importing it is a scenario
// file.
const ScenarioPackage = "internal/testkit/scenario"

// ScenarioAllowed are the harness-internal packages a scenario file may import.
var ScenarioAllowed = []string{ScenarioPackage, "internal/testkit/launcher"}

// ScenarioImportFindings is the scenarioimport predicate. The rule applies to the
// test files that import the scenario helper: the scenarios written before it
// (src/harness/internal/testkit/scenarios) move to it one by one, each leaving the
// tree clean under the check as it does (DH-ADR-TOOLCHAIN-8). This import-based
// scope is the Stage 6.8 scope amendment recorded in
// docs/stories/harness-DSH-M2/implementation-plan.md (operator decisions
// scenarioimport-scope and scenarioimport-scope-pin), so a file without the
// helper import is deliberately not checked.
func ScenarioImportFindings(filename string, file *ast.File, _ *types.Info) []Finding {
	if !isTestFile(filename) {
		return nil
	}
	internal := map[*ast.ImportSpec]string{}
	scenario := false
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
