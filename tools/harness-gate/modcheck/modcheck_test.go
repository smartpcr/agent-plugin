package modcheck

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is the checkout root, seen from tools/harness-gate/modcheck.
var repoRoot = filepath.Join("..", "..", "..")

const (
	fixtureGo = `module github.com/smartpcr/agent-plugin/src/go

go 1.27.4
`
	fixtureSDK = `module github.com/smartpcr/agent-plugin/src/sdk

go 1.27.4
`
	fixtureHarness = `module github.com/smartpcr/agent-plugin/src/harness

go 1.27.4

require (
	github.com/smartpcr/agent-plugin/src/go v0.1.5-0.20261119220503-a647d4984790
	github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790
)
`
	fixtureWork = `go 1.27.4

use (
	./src/go
	./src/sdk
	./src/harness
)
`
	fixtureSum = `github.com/smartpcr/agent-plugin/src/go v0.1.5-0.20261119220503-a647d4984790 h1:+X0ZYjvm5RUr2KmO3O7ABnaCY97cmq0vqKT8KwIwjlQ=
github.com/smartpcr/agent-plugin/src/go v0.1.5-0.20261119220503-a647d4984790/go.mod h1:9fSA8rmSzLayjvwnUR39P59YsFDRRAKsiPPKeLMvziE=
github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790 h1:ljeRlsuM1I7uEd5F/5wK3FhJGv2ldFD4+/Tq69pD2W8=
github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790/go.mod h1:4ZWs5LPwRFeD4uSXHCeFrLPSYskVsh8X1oOB/0SDEbk=
`
	replaceLine = "replace github.com/smartpcr/agent-plugin/src/go => ../go"
	zeroPin     = "v0.0.0-00010101000000-000000000000"
)

func fixtureRepo() Repo {
	return Repo{
		Work: []byte(fixtureWork),
		Mods: map[string][]byte{
			"src/go":      []byte(fixtureGo),
			"src/sdk":     []byte(fixtureSDK),
			"src/harness": []byte(fixtureHarness),
		},
		HarnessSum: []byte(fixtureSum),
	}
}

func committedRepo(t *testing.T) Repo {
	t.Helper()
	repo, err := Load(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func wantFailure(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("check passed, want a failure naming %q", parts)
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("failure does not name %q:\n%v", part, err)
		}
	}
}

func TestToolchain11CommittedFilesPass(t *testing.T) {
	if err := CheckToolchain11(committedRepo(t)); err != nil {
		t.Fatal(err)
	}
	if err := CheckToolchain11(fixtureRepo()); err != nil {
		t.Fatalf("fixture repo: %v", err)
	}
}

func TestToolchain11RejectsReplace(t *testing.T) {
	repo := committedRepo(t)
	harness := string(repo.Mods["src/harness"])
	if !strings.HasSuffix(harness, "\n") {
		harness += "\n"
	}
	repo.Mods["src/harness"] = []byte(harness + replaceLine + "\n")
	line := strings.Count(harness, "\n") + 1
	wantFailure(t, CheckToolchain11(repo), "src/harness/go.mod:"+strconv.Itoa(line)+": "+replaceLine)
}

func TestToolchain11RejectsReplaceBlock(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/harness"] = []byte(fixtureHarness + "\nreplace (\n\tgithub.com/smartpcr/agent-plugin/src/sdk => ../sdk\n)\n")
	wantFailure(t, CheckToolchain11(repo), "src/harness/go.mod:11: replace github.com/smartpcr/agent-plugin/src/sdk => ../sdk")

	repo = fixtureRepo()
	repo.Work = []byte(fixtureWork + "\n" + replaceLine + "\n")
	wantFailure(t, CheckToolchain11(repo), "go.work:9: "+replaceLine)
}

func TestToolchain11RejectsSecondGoFloor(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/sdk"] = []byte(strings.Replace(fixtureSDK, "go 1.27.4", "go 1.25", 1))
	wantFailure(t, CheckToolchain11(repo), "src/sdk/go.mod:3: go 1.25", "src/go/go.mod:3: go 1.27.4")
}

func TestToolchain11RejectsFourthModule(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/extra"] = []byte("module example.com/extra\n\ngo 1.27.4\n")
	wantFailure(t, CheckToolchain11(repo), "src/extra")

	repo = fixtureRepo()
	delete(repo.Mods, "src/sdk")
	wantFailure(t, CheckToolchain11(repo), "want exactly [src/go src/harness src/sdk]")
}

func TestToolchain11RejectsWorkspaceDrift(t *testing.T) {
	repo := fixtureRepo()
	repo.Work = []byte(strings.Replace(fixtureWork, "\t./src/sdk\n", "", 1))
	wantFailure(t, CheckToolchain11(repo), "go.work uses [src/go src/harness]")
}

func TestToolchain11RejectsMissingRequire(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/harness"] = []byte(strings.Replace(fixtureHarness,
		"\tgithub.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790\n", "", 1))
	wantFailure(t, CheckToolchain11(repo), "does not require github.com/smartpcr/agent-plugin/src/sdk")
}

func TestToolchain11RejectsWrongModulePath(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/sdk"] = []byte(strings.Replace(fixtureSDK, "/src/sdk", "/sdk", 1))
	wantFailure(t, CheckToolchain11(repo), "src/sdk/go.mod: module is", "want github.com/smartpcr/agent-plugin/src/sdk")
}

func TestSDK17CommittedFilePasses(t *testing.T) {
	if err := CheckSDK17(committedRepo(t).Mods["src/sdk"]); err != nil {
		t.Fatal(err)
	}
}

func TestSDK17RejectsRequire(t *testing.T) {
	err := CheckSDK17([]byte(fixtureSDK + "\nrequire golang.org/x/text v0.14.0\n"))
	wantFailure(t, err, "src/sdk/go.mod:5: require golang.org/x/text v0.14.0")

	err = CheckSDK17([]byte(fixtureSDK + "\nrequire (\n\tgolang.org/x/text v0.14.0 // indirect\n)\n"))
	wantFailure(t, err, "src/sdk/go.mod:6: require golang.org/x/text v0.14.0")
}

func TestParseRejectsMalformed(t *testing.T) {
	if _, err := Parse("go.mod", []byte("require (\n\tx v1\n")); err == nil || !strings.Contains(err.Error(), "unterminated require block") {
		t.Errorf("unterminated block: %v", err)
	}
	if _, err := Parse("go.mod", []byte("module\n")); err == nil || !strings.Contains(err.Error(), "go.mod:1") {
		t.Errorf("bare verb: %v", err)
	}
}

func TestPinsRejectZeroPlaceholder(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/harness"] = []byte(strings.Replace(fixtureHarness, "v0.1.5-0.20261119220503-a647d4984790", zeroPin, 1))
	wantFailure(t, CheckToolchain11(repo),
		"src/harness/go.mod:6: require github.com/smartpcr/agent-plugin/src/go "+zeroPin, "names no revision")

	repo = fixtureRepo()
	repo.Mods["src/harness"] = []byte(strings.Replace(fixtureHarness, "v0.0.0-20261119220503-a647d4984790", "latest", 1))
	wantFailure(t, CheckToolchain11(repo), "src/harness/go.mod:7:", "latest is not a pseudo-version")
}

func TestPinsRequireGoSumLines(t *testing.T) {
	repo := fixtureRepo()
	var kept []string
	for _, line := range strings.SplitAfter(fixtureSum, "\n") {
		if !strings.Contains(line, "src/sdk v0.0.0-20261119220503-a647d4984790/go.mod") {
			kept = append(kept, line)
		}
	}
	repo.HarnessSum = []byte(strings.Join(kept, ""))
	wantFailure(t, CheckToolchain11(repo),
		"go.sum has no line for github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790/go.mod")

	repo.HarnessSum = nil
	wantFailure(t, CheckToolchain11(repo), "go.sum has no line for github.com/smartpcr/agent-plugin/src/go")
}

func TestParsePseudoForms(t *testing.T) {
	for version, rev := range map[string]string{
		"v0.0.0-20261119220503-a647d4984790":        "a647d4984790",
		"v0.1.5-0.20261119220503-a647d4984790":      "a647d4984790",
		"v1.2.0-rc.1.0.20261119220503-a647d4984790": "a647d4984790",
	} {
		pseudo, err := ParsePseudo(version)
		if err != nil || pseudo.Rev != rev || pseudo.Time.Format("20060102150405") != "20261119220503" {
			t.Errorf("ParsePseudo(%s) = %+v, %v", version, pseudo, err)
		}
	}
	for _, version := range []string{zeroPin, "v0.1.4", "v0.0.0-2026111922050-a647d4984790", "v0.0.0-20261119220503-A647D4984790"} {
		if _, err := ParsePseudo(version); err == nil {
			t.Errorf("ParsePseudo(%s) succeeded", version)
		}
	}
}

func TestGoModHashMatchesGoSum(t *testing.T) {
	sdkMod := "module github.com/smartpcr/agent-plugin/src/sdk\n\ngo 1.26.0\n"
	want := "h1:4ZWs5LPwRFeD4uSXHCeFrLPSYskVsh8X1oOB/0SDEbk="
	if got := GoModHash([]byte(sdkMod)); got != want {
		t.Errorf("GoModHash = %s, want %s (the go.sum line written by go get)", got, want)
	}
}

const fixtureTest = `module github.com/smartpcr/agent-plugin/src/go/test/e2e

go 1.27.4

require (
	// The Gherkin runner.
	github.com/cucumber/godog v0.16.0
	// The module under test, resolved by the replace below.
	github.com/smartpcr/agent-plugin/src/go v0.0.0-00010101000000-000000000000
)

// The suites test the working tree.
replace github.com/smartpcr/agent-plugin/src/go => ../..
`

// TestCordisModuleIsStandardLibraryOnly: the committed src/go/go.mod requires nothing,
// its Gherkin suites are the test module src/go/test/e2e, and a require added to
// src/go is named, also through CheckToolchain11 (M1-D-TOOL-4 (A), M1-G-TOOL-10;
// DH-T-TOOLCHAIN-11).
func TestCordisModuleIsStandardLibraryOnly(t *testing.T) {
	repo := committedRepo(t)
	if err := CheckCordisStdlib(repo.Mods["src/go"]); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.Tests["src/go/test/e2e"]; !ok {
		t.Errorf("Load found no test module src/go/test/e2e; tests: %v", repo.Tests)
	}
	if _, ok := repo.Mods["src/go/test/e2e"]; ok {
		t.Error("Load counts src/go/test/e2e as a product module")
	}

	wantFailure(t, CheckCordisStdlib([]byte(fixtureGo+"\nrequire github.com/cucumber/godog v0.16.0\n")),
		"src/go/go.mod:5: require github.com/cucumber/godog v0.16.0", "M1-D-TOOL-4")
	repo = fixtureRepo()
	repo.Mods["src/go"] = []byte(fixtureGo + "\nrequire (\n\tgithub.com/cucumber/godog v0.16.0\n)\n")
	wantFailure(t, CheckToolchain11(repo), "src/go/go.mod:6: require github.com/cucumber/godog v0.16.0")
}

// TestToolchain11TestModuleClass: a test module at <product>/test/e2e is outside the
// RM-1 count and may replace its own product module; it must still declare its path,
// state the go floor and replace nothing else (M1-G-TOOL-10; DH-T-TOOLCHAIN-11).
func TestToolchain11TestModuleClass(t *testing.T) {
	repo := fixtureRepo()
	repo.Tests = map[string][]byte{"src/go/test/e2e": []byte(fixtureTest)}
	if err := CheckToolchain11(repo); err != nil {
		t.Fatalf("fixture with a test module: %v", err)
	}

	repo.Tests["src/go/test/e2e"] = []byte(strings.Replace(fixtureTest, "go 1.27.4", "go 1.25", 1))
	wantFailure(t, CheckToolchain11(repo), "src/go/test/e2e/go.mod:3: go 1.25", "(RM-2)")

	repo.Tests["src/go/test/e2e"] = []byte(strings.Replace(fixtureTest, "src/go/test/e2e\n", "src/go/e2e\n", 1))
	wantFailure(t, CheckToolchain11(repo), "src/go/test/e2e/go.mod: module is", "want github.com/smartpcr/agent-plugin/src/go/test/e2e")

	repo.Tests["src/go/test/e2e"] = []byte(fixtureTest + "replace github.com/cucumber/godog => ../godog\n")
	wantFailure(t, CheckToolchain11(repo), "src/go/test/e2e/go.mod:14: replace github.com/cucumber/godog => ../godog", "may replace only its own product module")

	// Elsewhere under src/ a nested module is still a fourth module.
	repo = fixtureRepo()
	repo.Mods["src/go/e2e"] = []byte(fixtureTest)
	wantFailure(t, CheckToolchain11(repo), "src/go/e2e", "(RM-1)")
	if IsTestModule("src/go/e2e") || IsTestModule("src/other/test/e2e") || !IsTestModule("src/harness/test/e2e") {
		t.Error("IsTestModule accepts only <product module>/test/e2e")
	}
}

// TestReasonsNamePinWithoutReason is the "pin without a reason is named" scenario of
// plan stage 13.4: a direct require with no comment fails, naming the file and line;
// with a comment above it or at the end of its line it passes (M1-G-TOOL-2;
// DH-T-TOOLCHAIN-11).
func TestReasonsNamePinWithoutReason(t *testing.T) {
	const head = "module example.com/m\n\ngo 1.27.4\n\n"
	wantFailure(t, CheckReasons("scratch/go.mod", []byte(head+"require example.com/x v1.0.0\n")),
		"scratch/go.mod:5: require example.com/x v1.0.0: no reason comment")
	for name, body := range map[string]string{
		"above":       "// Needed for x.\nrequire example.com/x v1.0.0\n",
		"end of line": "require example.com/x v1.0.0 // needed for x\n",
		"in a block":  "require (\n\t// Needed for x.\n\texample.com/x v1.0.0\n)\n",
		"indirect":    "require (\n\texample.com/y v1.0.0 // indirect\n)\n",
		"indirect+":   "require example.com/x v1.0.0 // indirect; pinned with y\n",
	} {
		if err := CheckReasons("scratch/go.mod", []byte(head+body)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestReasonsCoverEveryPinVerb: replace, exclude, tool and toolchain lines always
// need a reason, a bare "// indirect" mark is not one, and neither a comment above a
// block nor one separated by a blank line counts (M1-G-TOOL-2; DH-T-TOOLCHAIN-11).
func TestReasonsCoverEveryPinVerb(t *testing.T) {
	const head = "module example.com/m\n\ngo 1.27.4\n\n"
	for body, want := range map[string]string{
		"replace example.com/x => ../x\n":                   "scratch/go.mod:5: replace example.com/x => ../x",
		"exclude example.com/x v1.0.1\n":                    "scratch/go.mod:5: exclude example.com/x v1.0.1",
		"toolchain go1.27.4\n":                              "scratch/go.mod:5: toolchain go1.27.4",
		"tool (\n\texample.com/x/cmd/x\n)\n":                "scratch/go.mod:6: tool example.com/x/cmd/x",
		"// For x.\nrequire (\n\texample.com/x v1.0.0\n)\n": "scratch/go.mod:7: require example.com/x v1.0.0",
		"// For x.\n\nrequire example.com/x v1.0.0\n":       "scratch/go.mod:7: require example.com/x v1.0.0",
		"replace example.com/x => ../x // indirect\n":       "scratch/go.mod:5: replace example.com/x => ../x",
	} {
		wantFailure(t, CheckReasons("scratch/go.mod", []byte(head+body)), want, "(RM-10, DH-ADR-TOOLCHAIN-11)")
	}
}

// TestReasonsCommittedFilesPass: every product, test and tools module of the checkout,
// and go.work, gives a reason for each pin (M1-G-TOOL-2; DH-T-TOOLCHAIN-11).
func TestReasonsCommittedFilesPass(t *testing.T) {
	repo := committedRepo(t)
	if _, ok := repo.Tools["tools/harness-gate"]; !ok {
		t.Fatalf("Load found no tools module tools/harness-gate; tools: %v", repo.Tools)
	}
	if err := CheckRepoReasons(repo); err != nil {
		t.Fatal(err)
	}
}

// TestToolsModulePinsLinters: the tools module (M1-D-TOOL-2 (a)) declares the
// staticcheck and govulncheck tool lines, pinned at the versions of plan step 13.4.1
// (M1-G-TOOL-5; DH-T-TOOLCHAIN-11).
func TestToolsModulePinsLinters(t *testing.T) {
	file, err := Parse("tools/harness-gate/go.mod", committedRepo(t).Tools["tools/harness-gate"])
	if err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, d := range file.All("tool") {
		tools = append(tools, d.Args)
	}
	required := map[string]string{}
	for _, d := range file.All("require") {
		if f := strings.Fields(d.Args); len(f) >= 2 {
			required[f[0]] = f[1]
		}
	}
	for tool, pin := range map[string][2]string{
		"honnef.co/go/tools/cmd/staticcheck": {"honnef.co/go/tools", "v0.8.2"},
		"golang.org/x/vuln/cmd/govulncheck":  {"golang.org/x/vuln", "v1.10.0"},
	} {
		if !slices.Contains(tools, tool) {
			t.Errorf("tools module has no tool line %s: %v", tool, tools)
		}
		if required[pin[0]] != pin[1] {
			t.Errorf("tools module pins %s at %q, want %s", pin[0], required[pin[0]], pin[1])
		}
	}
}
