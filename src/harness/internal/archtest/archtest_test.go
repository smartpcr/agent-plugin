package archtest

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// moduleDirs maps each module path to its directory, relative to this package.
var moduleDirs = map[string]string{
	GoModule:      filepath.Join("..", "..", "..", "go"),
	SDKModule:     filepath.Join("..", "..", "..", "sdk"),
	HarnessModule: filepath.Join("..", ".."),
}

// goList runs `go list -deps -json ./...` in the module's directory.
func goList(t *testing.T, module string) []Package {
	t.Helper()
	tool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is required: %v", err)
	}
	cmd := exec.Command(tool, "list", "-deps", "-json", "./...")
	cmd.Dir = moduleDirs[module]
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list in %s: %v\n%s", cmd.Dir, err, stderr.String())
	}
	pkgs, err := Parse(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

// TestImportDirection runs every rule against the real modules. The src/sdk rule is
// the stage scenario "src/sdk imports nothing of the product" (DH-S-4 exit evidence).
func TestImportDirection(t *testing.T) {
	listed := map[string][]Package{}
	for _, r := range Rules {
		t.Run(r.Name, func(t *testing.T) {
			pkgs, ok := listed[r.Module]
			if !ok {
				pkgs = goList(t, r.Module)
				listed[r.Module] = pkgs
			}
			own := 0
			for _, p := range pkgs {
				if within(p.ImportPath, r.Module) {
					own++
				}
			}
			if own == 0 {
				t.Fatalf("go list found no package of %s", r.Module)
			}
			for _, v := range r.Check(pkgs) {
				t.Error(v)
			}
		})
	}
}

func TestParse(t *testing.T) {
	pkgs, err := Parse(strings.NewReader(`{"ImportPath":"fmt","Standard":true}
{"ImportPath":"` + SDKModule + `/jsonrpc","Name":"jsonrpc","GoFiles":["a.go"],"Module":{"Path":"` + SDKModule + `"}}`))
	if err != nil || len(pkgs) != 2 || !pkgs[0].Standard || pkgs[1].Module.Path != SDKModule {
		t.Fatalf("Parse = %+v, %v", pkgs, err)
	}
	if _, err := Parse(strings.NewReader(`{"ImportPath":`)); err == nil {
		t.Fatal("Parse accepted truncated output")
	}
}

func TestRulesCatchViolations(t *testing.T) {
	pkg := func(path, name string, depOnly bool, files ...string) Package {
		return Package{ImportPath: path, Name: name, DepOnly: depOnly, GoFiles: files}
	}
	check := func(module string, pkgs ...Package) []string {
		var all []string
		for _, r := range Rules {
			if r.Module == module {
				all = append(all, r.Check(pkgs)...)
			}
		}
		return all
	}
	for _, tc := range []struct {
		name   string
		module string
		pkgs   []Package
		bad    int
	}{
		{"sdk clean", SDKModule, []Package{pkg("fmt", "fmt", true), pkg(SDKModule+"/jsonrpc", "jsonrpc", false, "a.go")}, 0},
		{"sdk imports harness", SDKModule, []Package{pkg(HarnessModule+"/internal/schema", "schema", true, "a.go")}, 1},
		{"sdk imports go", SDKModule, []Package{pkg(GoModule+"/cordis", "cordis", true, "a.go")}, 1},
		{"sdk lookalike prefix is fine", SDKModule, []Package{pkg(GoModule+"x/y", "y", true, "a.go")}, 0},
		{"go imports sdk and harness", GoModule, []Package{pkg(SDKModule, "sdk", true), pkg(HarnessModule+"/cmd/dsh", "main", true)}, 2},
		{"harness layout clean", HarnessModule, []Package{
			pkg(HarnessModule+"/internal", "internal", false, "doc.go"),
			pkg(HarnessModule+"/internal/schema", "schema", false, "a.go"),
			pkg(HarnessModule+"/cmd/dsh", "main", false, "main.go"),
			pkg(HarnessModule+"/test/e2e", "e2e", false),
			pkg(SDKModule+"/protocol", "protocol", true, "a.go"),
			pkg(HarnessModule+"/elsewhere", "elsewhere", true, "a.go"),
		}, 0},
		{"harness public package", HarnessModule, []Package{pkg(HarnessModule+"/engine", "engine", false, "a.go")}, 1},
		{"harness non-main under cmd", HarnessModule, []Package{pkg(HarnessModule+"/cmd/lib", "lib", false, "a.go")}, 1},
		{"harness internalx is not internal", HarnessModule, []Package{pkg(HarnessModule+"/internalx", "x", false, "a.go")}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := check(tc.module, tc.pkgs...); len(got) != tc.bad {
				t.Fatalf("violations = %q, want %d", got, tc.bad)
			}
		})
	}
}
