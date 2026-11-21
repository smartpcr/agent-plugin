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
	{HarnessModule, "only internal/session/store/sqlite imports the SQLite driver (DH-D-STORE-2, DH-ADR-SQLITE-1)", onlyImporter(SQLiteModule, SQLitePackage)},
	{HarnessModule, "internal/session/event imports no harness package but vocab and schema (DH-D-SESSION-1)", importsOnly(EventPackage, VocabPackage, SchemaPackage)},
	{HarnessModule, "internal/session/scope imports nothing of the modules but cordis (DH-D-SESSION-8)", importsOnly(ScopePackage, CordisPackage)},
	{HarnessModule, "internal/session/store imports no harness package but schema and event (DH-D-STORE-1)", importsOnly(StorePackage, SchemaPackage, EventPackage)},
	{HarnessModule, "no non-test package imports memstore or storetest (DH-D-STORE-1)", noImporter(MemstorePackage, StoretestPackage)},
	{HarnessModule, "internal/session/surface imports no harness package but event, vocab and schema (DH-D-SESSION-3)", importsOnly(SurfacePackage, EventPackage, VocabPackage, SchemaPackage)},
	{HarnessModule, "nothing outside internal/agent/driver imports it (DH-T-AGENT-27)", onlyImporter(DriverPackage, DriverPackage)},
	{HarnessModule, "internal/session and its packages import no llm runtime, agent or interaction registry (DH-D-LLM-1, DH-D-AGENT-1)", importsNone(SessionPackage, LLMPackage, AgentPackage, HumanPackage)},
	{HarnessModule, "internal/llm imports no agent, interaction or view package: the runtime holds no session writer (DH-D-LLM-1)", importsNone(LLMPackage, AgentPackage, HumanPackage, ViewsPackage)},
	{HarnessModule, "only internal/world/subprocess imports os/exec: the one place a program starts (DH-D-EXEC-1, onespawn)", onlyImporter(ExecPackage, SubprocessPackage)},
	{HarnessModule, "no non-test package imports subprocesstest (DH-D-EXEC-1)", noImporter(SubprocesstestPackage)},
	{HarnessModule, "internal/util/herr and internal/util/ident import the standard library only (RM-6, DH-D-UTIL-5)", stdlibOnly(HerrPackage, IdentPackage)},
	{HarnessModule, "internal/util/egress imports no harness package but herr, and of the modules only cordis: the permit is an interface, not sharing's type (DH-ADR-UTIL-2)", importsOnly(EgressPackage, HerrPackage, CordisPackage)},
	{HarnessModule, "only tests and the test kit's own packages import internal/testkit (DH-T-UTIL-11, DH-ADR-UTIL-6)", importedOnlyWithin(TestkitPackage)},
}

// Packages the rules name.
const (
	VocabPackage  = HarnessModule + "/internal/llm/vocab"
	SchemaPackage = HarnessModule + "/internal/schema"
	YAMLXPackage  = HarnessModule + "/internal/boot/yamlx"
	ExprPackage   = HarnessModule + "/internal/boot/expr"

	CordisPackage    = GoModule + "/cordis"
	EventPackage     = HarnessModule + "/internal/session/event"
	ScopePackage     = HarnessModule + "/internal/session/scope"
	StorePackage     = HarnessModule + "/internal/session/store"
	MemstorePackage  = StorePackage + "/memstore"
	StoretestPackage = StorePackage + "/storetest"
	SQLitePackage    = StorePackage + "/sqlite"
	SurfacePackage   = HarnessModule + "/internal/session/surface"
	SessionPackage   = HarnessModule + "/internal/session"
	ViewsPackage     = SessionPackage + "/views"
	LLMPackage       = HarnessModule + "/internal/llm"
	AgentPackage     = HarnessModule + "/internal/agent"
	DriverPackage    = AgentPackage + "/driver"
	HumanPackage     = HarnessModule + "/internal/human"

	SubprocessPackage     = HarnessModule + "/internal/world/subprocess"
	SubprocesstestPackage = SubprocessPackage + "/subprocesstest"
	HerrPackage           = HarnessModule + "/internal/util/herr"
	IdentPackage          = HarnessModule + "/internal/util/ident"
	EgressPackage         = HarnessModule + "/internal/util/egress"
	TestkitPackage        = HarnessModule + "/internal/testkit"
	ExecPackage           = "os/exec"
)

// Third-party modules with a single importer.
const (
	YAMLModule   = "go.yaml.in/yaml/v4"
	ExprModule   = "github.com/expr-lang/expr"
	SQLiteModule = "modernc.org/sqlite"
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

// importsNone fails every package at or below under that directly imports one of
// forbidden (matched exactly, so a parent does not forbid its children).
func importsNone(under string, forbidden ...string) func([]Package) []string {
	return func(pkgs []Package) []string {
		var bad []string
		for _, p := range pkgs {
			if !within(p.ImportPath, under) {
				continue
			}
			for _, imp := range p.Imports {
				if slices.Contains(forbidden, imp) {
					bad = append(bad, fmt.Sprintf("%s imports %s", p.ImportPath, imp))
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

// noImporter fails every listed package that directly imports one of pkgs. The
// list excludes test files, so tests may import them.
func noImporter(pkgs ...string) func([]Package) []string {
	return func(list []Package) []string {
		var bad []string
		for _, p := range list {
			for _, imp := range p.Imports {
				if slices.Contains(pkgs, imp) {
					bad = append(bad, fmt.Sprintf("%s imports %s; only tests may", p.ImportPath, imp))
				}
			}
		}
		return bad
	}
}

// importedOnlyWithin fails every listed package outside under that directly imports
// a package at or below under. The list excludes test files, so tests may.
func importedOnlyWithin(under string) func([]Package) []string {
	return func(pkgs []Package) []string {
		var bad []string
		for _, p := range pkgs {
			if within(p.ImportPath, under) {
				continue
			}
			for _, imp := range p.Imports {
				if within(imp, under) {
					bad = append(bad, fmt.Sprintf("%s imports %s; only tests and %s may", p.ImportPath, imp, under))
				}
			}
		}
		return bad
	}
}

// stdlibOnly fails each of pkgs that directly imports a package outside the standard
// library, whose import paths have no dot in their first element.
func stdlibOnly(pkgs ...string) func([]Package) []string {
	return func(list []Package) []string {
		var bad []string
		for _, p := range list {
			if !slices.Contains(pkgs, p.ImportPath) {
				continue
			}
			for _, imp := range p.Imports {
				first, _, _ := strings.Cut(imp, "/")
				if strings.Contains(first, ".") {
					bad = append(bad, fmt.Sprintf("%s imports %s; it may import the standard library only", p.ImportPath, imp))
				}
			}
		}
		return bad
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
