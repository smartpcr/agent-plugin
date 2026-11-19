package modcheck

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is the checkout root, seen from tools/harness-gate/modcheck.
var repoRoot = filepath.Join("..", "..", "..")

const (
	fixtureGo = `module github.com/smartpcr/agent-plugin/src/go

go 1.26.0
`
	fixtureSDK = `module github.com/smartpcr/agent-plugin/src/sdk

go 1.26.0
`
	fixtureHarness = `module github.com/smartpcr/agent-plugin/src/harness

go 1.26.0

require (
	github.com/smartpcr/agent-plugin/src/go v0.0.0-00010101000000-000000000000
	github.com/smartpcr/agent-plugin/src/sdk v0.0.0-00010101000000-000000000000
)
`
	fixtureWork = `go 1.26.0

use (
	./src/go
	./src/sdk
	./src/harness
)
`
	replaceLine = "replace github.com/smartpcr/agent-plugin/src/go => ../go"
)

func fixtureRepo() Repo {
	return Repo{
		Work: []byte(fixtureWork),
		Mods: map[string][]byte{
			"src/go":      []byte(fixtureGo),
			"src/sdk":     []byte(fixtureSDK),
			"src/harness": []byte(fixtureHarness),
		},
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
	repo.Mods["src/sdk"] = []byte(strings.Replace(fixtureSDK, "go 1.26.0", "go 1.25", 1))
	wantFailure(t, CheckToolchain11(repo), "src/sdk/go.mod:3: go 1.25", "src/go/go.mod:3: go 1.26.0")
}

func TestToolchain11RejectsFourthModule(t *testing.T) {
	repo := fixtureRepo()
	repo.Mods["src/extra"] = []byte("module example.com/extra\n\ngo 1.26.0\n")
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
		"\tgithub.com/smartpcr/agent-plugin/src/sdk v0.0.0-00010101000000-000000000000\n", "", 1))
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
