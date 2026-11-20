// Package archtest holds the import-direction rules of the three modules (RM-3,
// RM-5) and checks them against `go list -deps -json` output. The rules live here;
// archtest_test.go runs the go tool, because only tests may spawn processes. Later
// stages extend Rules.
package archtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Root is the module path prefix shared by the three modules.
const Root = "github.com/smartpcr/agent-plugin/src/"

// Module paths of the three modules.
const (
	GoModule      = Root + "go"
	SDKModule     = Root + "sdk"
	HarnessModule = Root + "harness"
)

// Package is the part of one `go list -json` record the rules read.
type Package struct {
	ImportPath string
	Name       string
	Standard   bool
	DepOnly    bool
	GoFiles    []string
	Imports    []string
	Module     *struct{ Path string }
}

// Parse reads the stream of JSON objects that `go list -json` prints.
func Parse(r io.Reader) ([]Package, error) {
	dec := json.NewDecoder(r)
	var pkgs []Package
	for {
		var p Package
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("archtest: go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// Rule is one check over the package list of one module.
type Rule struct {
	// Module is the module whose `go list -deps -json ./...` output the rule reads.
	Module string
	// Name says what the rule asserts.
	Name string
	// Check returns one message per violation.
	Check func(pkgs []Package) []string
}

// Rules is the rule table.
var Rules = []Rule{
	{SDKModule, "src/sdk imports neither src/go nor src/harness", noneUnder(GoModule, HarnessModule)},
	{GoModule, "src/go imports neither src/sdk nor src/harness", noneUnder(SDKModule, HarnessModule)},
	{HarnessModule, "harness implementation packages are under internal/", implementationInternal},
	{HarnessModule, "internal/llm/vocab imports no harness package but internal/schema (DH-T-LLM-1)", importsOnly(VocabPackage, SchemaPackage)},
	{HarnessModule, "cmd/dsh embeds the zone database through time/tzdata (DH-T-TEMPORAL-5)", mustImport(HarnessModule+"/cmd/dsh", "time/tzdata")},
	{HarnessModule, "only internal/boot/yamlx imports the YAML module (DH-T-YAML-1)", onlyImporter(YAMLModule, YAMLXPackage)},
	{HarnessModule, "only internal/boot/expr imports the expression module (DH-D-LOADER-5)", onlyImporter(ExprModule, ExprPackage)},
}

// Packages the rules name.
const (
	VocabPackage  = HarnessModule + "/internal/llm/vocab"
	SchemaPackage = HarnessModule + "/internal/schema"
	YAMLXPackage  = HarnessModule + "/internal/boot/yamlx"
	ExprPackage   = HarnessModule + "/internal/boot/expr"
)

// Third-party modules with a single importer.
const (
	YAMLModule = "go.yaml.in/yaml/v4"
	ExprModule = "github.com/expr-lang/expr"
)

// onlyImporter fails every harness package other than owner that directly imports a
// package of module.
func onlyImporter(module, owner string) func([]Package) []string {
	return func(pkgs []Package) []string {
		var bad []string
		for _, p := range pkgs {
			if p.ImportPath == owner || !within(p.ImportPath, HarnessModule) {
				continue
			}
			for _, imp := range p.Imports {
				if within(imp, module) {
					bad = append(bad, fmt.Sprintf("%s imports %s; only %s may", p.ImportPath, imp, owner))
				}
			}
		}
		return bad
	}
}

// importsOnly fails pkg when it directly imports a package of the three modules
// other than allowed. Standard and third-party imports are not its concern.
func importsOnly(pkg string, allowed ...string) func([]Package) []string {
	return func(pkgs []Package) []string {
		var bad []string
		for _, p := range pkgs {
			if p.ImportPath != pkg {
				continue
			}
			for _, imp := range p.Imports {
				if strings.HasPrefix(imp, Root) && !slices.Contains(allowed, imp) {
					bad = append(bad, fmt.Sprintf("%s imports %s", pkg, imp))
				}
			}
		}
		return bad
	}
}

// mustImport fails pkg, when it is listed, unless it directly imports imp.
func mustImport(pkg, imp string) func([]Package) []string {
	return func(pkgs []Package) []string {
		for _, p := range pkgs {
			if p.ImportPath == pkg && !slices.Contains(p.Imports, imp) {
				return []string{fmt.Sprintf("%s does not import %s", pkg, imp)}
			}
		}
		return nil
	}
}

// within reports whether path is module or a package below it.
func within(path, module string) bool {
	return path == module || strings.HasPrefix(path, module+"/")
}

// noneUnder fails every package, dependency or not, whose path is under one of mods.
func noneUnder(mods ...string) func([]Package) []string {
	return func(pkgs []Package) []string {
		var bad []string
		for _, p := range pkgs {
			for _, m := range mods {
				if within(p.ImportPath, m) {
					bad = append(bad, fmt.Sprintf("depends on %s", p.ImportPath))
				}
			}
		}
		return bad
	}
}

// implementationInternal fails every non-test package of the harness module that is
// neither under internal/ nor a main package under cmd/.
func implementationInternal(pkgs []Package) []string {
	var bad []string
	for _, p := range pkgs {
		if p.DepOnly || !within(p.ImportPath, HarnessModule) || len(p.GoFiles) == 0 {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, HarnessModule), "/")
		if rel == "internal" || strings.HasPrefix(rel, "internal/") {
			continue
		}
		if p.Name == "main" && strings.HasPrefix(rel, "cmd/") {
			continue
		}
		bad = append(bad, fmt.Sprintf("%s is an implementation package outside internal/", p.ImportPath))
	}
	return bad
}
