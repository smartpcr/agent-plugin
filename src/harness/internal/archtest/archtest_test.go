package archtest

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
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
	imports := func(p Package, imps ...string) Package { p.Imports = imps; return p }
	dsh := imports(pkg(HarnessModule+"/cmd/dsh", "main", false, "main.go"), "time/tzdata")
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
			dsh,
			imports(pkg(VocabPackage, "vocab", false, "a.go"), "encoding/json", SchemaPackage, "github.com/google/jsonschema-go/jsonschema"),
			pkg(HarnessModule+"/test/e2e", "e2e", false),
			pkg(SDKModule+"/protocol", "protocol", true, "a.go"),
			pkg(HarnessModule+"/elsewhere", "elsewhere", true, "a.go"),
		}, 0},
		{"harness public package", HarnessModule, []Package{pkg(HarnessModule+"/engine", "engine", false, "a.go")}, 1},
		{"harness non-main under cmd", HarnessModule, []Package{pkg(HarnessModule+"/cmd/lib", "lib", false, "a.go")}, 1},
		{"harness internalx is not internal", HarnessModule, []Package{pkg(HarnessModule+"/internalx", "x", false, "a.go")}, 1},
		{"vocab imports an area package", HarnessModule, []Package{
			imports(pkg(VocabPackage, "vocab", false, "a.go"), SchemaPackage, HarnessModule+"/internal/session", HarnessModule+"/internal/llm"),
		}, 2},
		{"vocab imports another module", HarnessModule, []Package{imports(pkg(VocabPackage, "vocab", false, "a.go"), GoModule+"/cordis")}, 1},
		{"cmd/dsh without tzdata", HarnessModule, []Package{pkg(HarnessModule+"/cmd/dsh", "main", false, "main.go")}, 1},
		{"yaml and expr through their owners", HarnessModule, []Package{
			imports(pkg(YAMLXPackage, "yamlx", false, "a.go"), YAMLModule),
			imports(pkg(ExprPackage, "expr", false, "a.go"), ExprModule, ExprModule+"/ast"),
			imports(pkg(YAMLModule, "yaml", true, "a.go"), YAMLModule+"/internal/libyaml"),
		}, 0},
		{"yaml and expr elsewhere", HarnessModule, []Package{
			imports(pkg(HarnessModule+"/internal/boot/config", "config", false, "a.go"), YAMLModule, ExprModule+"/vm"),
		}, 2},
		{"session packages within their bounds", HarnessModule, []Package{
			imports(pkg(EventPackage, "event", false, "a.go"), VocabPackage, SchemaPackage, "encoding/json"),
			imports(pkg(ScopePackage, "scope", false, "a.go"), CordisPackage, "sync"),
			imports(pkg(StorePackage, "store", false, "a.go"), EventPackage, SchemaPackage),
			imports(pkg(MemstorePackage, "memstore", false, "a.go"), EventPackage, StorePackage),
			imports(pkg(StoretestPackage, "storetest", false, "a.go"), EventPackage, StorePackage, VocabPackage, "testing"),
		}, 0},
		{"session packages out of bounds", HarnessModule, []Package{
			imports(pkg(EventPackage, "event", false, "a.go"), StorePackage),
			imports(pkg(ScopePackage, "scope", false, "a.go"), EventPackage),
			imports(pkg(StorePackage, "store", false, "a.go"), MemstorePackage),
			imports(pkg(HarnessModule+"/internal/engine", "engine", false, "a.go"), StoretestPackage),
		}, 5},
		{"spawning and leaf packages within their bounds", HarnessModule, []Package{
			imports(pkg(SubprocessPackage, "subprocess", false, "a.go"), ExecPackage, HerrPackage, "golang.org/x/sys/windows"),
			imports(pkg(SubprocesstestPackage, "subprocesstest", false, "a.go"), SubprocessPackage, "testing"),
			imports(pkg(HerrPackage, "herr", false, "a.go"), "errors", "fmt", "log/slog"),
			imports(pkg(IdentPackage, "ident", false, "a.go"), "crypto/rand", "encoding/hex"),
		}, 0},
		{"spawning and leaf packages out of bounds", HarnessModule, []Package{
			imports(pkg(HarnessModule+"/internal/tool/shell", "shell", false, "a.go"), ExecPackage),
			imports(pkg(HarnessModule+"/internal/engine", "engine", false, "a.go"), SubprocesstestPackage),
			imports(pkg(HerrPackage, "herr", false, "a.go"), "github.com/pkg/errors"),
			imports(pkg(IdentPackage, "ident", false, "a.go"), "github.com/google/uuid"),
		}, 4},
		{"the llm listener and meter edges", HarnessModule, []Package{
			imports(pkg(LLMRetryPackage, "retry", false, "a.go"), LLMPackage, AgentPackage),
			imports(pkg(LLMMeterPackage, "meter", false, "a.go"), VocabPackage, ViewsPackage),
		}, 0},
		{"the llm edges are not widened", HarnessModule, []Package{
			imports(pkg(LLMRetryPackage, "retry", false, "a.go"), ViewsPackage, HumanPackage),
			imports(pkg(LLMMeterPackage, "meter", false, "a.go"), AgentPackage),
			imports(pkg(LLMPackage, "llm", false, "a.go"), AgentPackage, ViewsPackage),
		}, 5},
		{"the test kit within its bounds", HarnessModule, []Package{
			imports(pkg(TestkitPackage+"/launcher", "launcher", false, "a.go"), TestkitPackage, TestkitPackage+"/temphome", SubprocessPackage),
			imports(pkg(HarnessModule+"/internal/testkitx", "testkitx", false, "a.go"), HarnessModule+"/internal/testkitx/y"),
		}, 0},
		{"the test kit out of bounds", HarnessModule, []Package{
			imports(pkg(HarnessModule+"/internal/boot", "boot", false, "a.go"), TestkitPackage+"/clock"),
			imports(pkg(HarnessModule+"/cmd/dsh", "main", false, "main.go"), TestkitPackage, "time/tzdata"),
		}, 2},
		{"the tools packages within their bounds", HarnessModule, []Package{
			imports(pkg(ToolsPackage, "tools", false, "a.go"), SchemaPackage, VocabPackage, ScopePackage, CordisPackage),
			imports(pkg(ToolsPackage+"/prompt", "prompt", false, "a.go"), ToolsPackage, ToolsCorePackage),
			imports(pkg(ToolsEndpointPackage+"/script", "script", false, "a.go"), ToolsEndpointPackage),
			imports(pkg(HarnessModule+"/internal/toolsx", "toolsx", false, "a.go"), HarnessModule+"/internal/toolsx/core"),
		}, 0},
		{"the tools packages out of bounds", HarnessModule, []Package{
			imports(pkg(ToolsPackage, "tools", false, "a.go"), LLMProviderPackage+"/deepseek", LLMWirePackage, VocabPackage),
			imports(pkg(HarnessModule+"/internal/agent/driver", "driver", false, "a.go"), ToolsPackage, ToolsCorePackage),
			imports(pkg(HarnessModule+"/cmd/dsh", "main", false, "main.go"), ToolsEndpointPackage+"/mcp", "time/tzdata"),
		}, 4},
		{"the context packages within their bounds", HarnessModule, []Package{
			imports(pkg(ContextPackage+"/request", "request", false, "a.go"), AgentPackage, VocabPackage, EventPackage, SurfacePackage, CordisPackage),
			imports(pkg(HarnessModule+"/internal/contextx", "contextx", false, "a.go"), DriverPackage),
		}, 1},
		{"the context packages out of bounds", HarnessModule, []Package{
			pkg(ContextPackage, "context", false, "a.go"),
			imports(pkg(ContextPackage+"/request", "request", false, "a.go"), DriverPackage, LLMProviderPackage+"/deepseek", LLMWirePackage),
			imports(pkg(ContextPackage+"/clock", "clock", false, "a.go"), LLMTransportPackage),
		}, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := check(tc.module, tc.pkgs...); len(got) != tc.bad {
				t.Fatalf("violations = %q, want %d", got, tc.bad)
			}
		})
	}
}

// TestRequestTypes proves the TC-05 predicate finds a request type under either
// shape and ignores what is not one.
func TestRequestTypes(t *testing.T) {
	file := func(src string) *fstest.MapFile { return &fstest.MapFile{Data: []byte("package vocab\n" + src)} }
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		found int
	}{
		{"clean", fstest.MapFS{"a.go": file("type Message struct{}\ntype Controls struct{}\ntype Meta struct{ Session string }")}, 0},
		{"named Request", fstest.MapFS{"a.go": file("type Request struct{}")}, 1},
		{"named Request, not a struct", fstest.MapFS{"a.go": file("type Request = string")}, 1},
		{"messages and controls", fstest.MapFS{"a.go": file("type Call struct{ M []Message; C Controls }")}, 1},
		{"pointers and qualified names", fstest.MapFS{"a.go": file("type Call struct{ M *[]vocab.Message; C *vocab.Controls }")}, 1},
		{"nested in a function", fstest.MapFS{"a.go": file("func f() { type q struct{ M []Message; C Controls }; _ = q{} }")}, 1},
		{"messages alone", fstest.MapFS{"a.go": file("type Turn struct{ M []Message }")}, 0},
		{"an array is not a slice", fstest.MapFS{"a.go": file("type Pair struct{ M [2]Message; C Controls }")}, 0},
		{"test files are skipped", fstest.MapFS{"a.go": file(""), "a_test.go": file("type Request struct{}")}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := RequestTypes(tc.files)
			if err != nil || len(found) != tc.found {
				t.Fatalf("RequestTypes = %q, %v; want %d", found, err, tc.found)
			}
		})
	}
	if _, err := RequestTypes(fstest.MapFS{"a.go": file("type (")}); err == nil {
		t.Fatal("a file that does not parse was accepted")
	}
	if _, err := RequestTypes(fstest.MapFS{}); err == nil {
		t.Fatal("an empty tree passed")
	}
}

// TestNoSpawnOutsideSubprocess is the source half of onespawn over the real
// harness module: no non-test file outside internal/world/subprocess starts a
// program (DH-D-EXEC-1).
func TestNoSpawnOutsideSubprocess(t *testing.T) {
	found, err := Spawns(os.DirFS(moduleDirs[HarnessModule]))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

// TestSpawns proves the predicate finds every way to start a program and honours
// its exemptions.
func TestSpawns(t *testing.T) {
	file := func(src string) *fstest.MapFile { return &fstest.MapFile{Data: []byte("package p\n" + src)} }
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		found int
	}{
		{"clean", fstest.MapFS{"a/a.go": file(`import "os"; var _ = os.Getpid`)}, 0},
		{"os.StartProcess", fstest.MapFS{"a/a.go": file(`import "os"; var _ = os.StartProcess`)}, 1},
		{"renamed syscall", fstest.MapFS{"a/a.go": file(`import sc "syscall"; func f() { sc.ForkExec("", nil, nil); sc.Exec("", nil, nil) }`)}, 2},
		{"x/sys", fstest.MapFS{"a/a.go": file(`import ("golang.org/x/sys/unix"; w "golang.org/x/sys/windows"); var _, _ = unix.Exec, w.CreateProcess`)}, 2},
		{"os/exec", fstest.MapFS{"a/a.go": file(`import "os/exec"; var _ = exec.Command`)}, 1},
		{"dot import", fstest.MapFS{"a/a.go": file(`import . "os/exec"; var _ = Command`)}, 1},
		{"a local named os", fstest.MapFS{"a/a.go": file(`type t struct{}; func (t) StartProcess() {}; func f() { var os t; os.StartProcess() }`)}, 0},
		{"the owner is exempt", fstest.MapFS{"a/a.go": file(""), SpawnOwner + "/s.go": file(`import "os/exec"; var _ = exec.Command`), SpawnOwner + "/subprocesstest/t.go": file(`import "os"; var _ = os.StartProcess`)}, 0},
		{"tests and testdata are exempt", fstest.MapFS{"a/a.go": file(""), "a/a_test.go": file(`import "os"; var _ = os.StartProcess`), "a/testdata/x.go": file(`import "os"; var _ = os.StartProcess`)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := Spawns(tc.files)
			if err != nil || len(found) != tc.found {
				t.Fatalf("Spawns = %q, %v; want %d", found, err, tc.found)
			}
		})
	}
	if _, err := Spawns(fstest.MapFS{}); err == nil {
		t.Fatal("an empty tree passed")
	}
}

// TestNoHTTPClientOutsideEgress is DH-T-UNDICI-8 and the source half of
// DH-T-HOST-19 over the real harness module: no non-test file outside
// internal/util/egress and internal/testkit builds an HTTP client or sends through
// the default one.
//
//dsh:test DH-T-UNDICI-8, DH-T-HOST-19
func TestNoHTTPClientOutsideEgress(t *testing.T) {
	found, err := HTTPClients(os.DirFS(moduleDirs[HarnessModule]))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

// TestNoTestSeamOutsideTests is the seam rule of DH-T-HOST-19 over the real harness
// module: llm/transport.Over is used only by tests and internal/testkit, and
// egress.Over also by internal/llm/transport.
//
//dsh:test DH-T-HOST-19
func TestNoTestSeamOutsideTests(t *testing.T) {
	found, err := SeamCalls(os.DirFS(moduleDirs[HarnessModule]))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

// seededLines returns "path:line" of every line of the tree marked "// seeded:".
func seededLines(t *testing.T, fsys fs.FS) map[string]bool {
	t.Helper()
	want := map[string]bool{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "// seeded:") {
				want[fmt.Sprintf("%s:%d", p, i+1)] = true
			}
		}
		return nil
	})
	if err != nil || len(want) == 0 {
		t.Fatalf("seeded tree: %v, %d marked lines", err, len(want))
	}
	return want
}

// TestSeededTreeFailsTheEgressRules is the stage scenario "A client literal outside
// egress fails the gate": over testdata/egress, a seeded harness tree, both rules
// report exactly the lines marked "// seeded:" -- among them a file building
// &http.Client{} and a non-test call to transport.Over -- and nothing in the
// owner, the test kit or a test file.
//
//dsh:test DH-T-UNDICI-8, DH-T-HOST-19
func TestSeededTreeFailsTheEgressRules(t *testing.T) {
	tree := os.DirFS(filepath.Join("testdata", "egress"))
	want := seededLines(t, tree)
	clients, err := HTTPClients(tree)
	if err != nil {
		t.Fatal(err)
	}
	seams, err := SeamCalls(tree)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range append(clients, seams...) {
		parts := strings.SplitN(f, ":", 3)
		got[parts[0]+":"+parts[1]] = true
	}
	for line := range want {
		if !got[line] {
			t.Errorf("%s is seeded but not reported", line)
		}
	}
	for line := range got {
		if !want[line] {
			t.Errorf("%s is reported but not seeded", line)
		}
	}
	for _, needle := range []string{
		"internal/sender/client.go:18:14: a net/http.Client value builds an HTTP client outside " + EgressOwner,
		"internal/sender/seam.go:9:6: " + LLMTransportPackage + ".Over is a test seam: only tests and " + TestkitDir + " may use it",
	} {
		if !slices.Contains(append(clients, seams...), needle) {
			t.Errorf("findings lack %q:\n%s", needle, strings.Join(append(clients, seams...), "\n"))
		}
	}
}

// TestEgressRulesExemptions proves the two predicates honour their exemptions and
// refuse a tree they cannot check.
//
//dsh:test DH-T-UNDICI-8
func TestEgressRulesExemptions(t *testing.T) {
	file := func(src string) *fstest.MapFile { return &fstest.MapFile{Data: []byte("package p\n" + src)} }
	const tp = `"` + LLMTransportPackage + `"`
	for _, tc := range []struct {
		name           string
		files          fstest.MapFS
		clients, seams int
	}{
		{"clean", fstest.MapFS{"a/a.go": file(`import "net/http"; func f(c *http.Client, r *http.Request) { c.Do(r) }`)}, 0, 0},
		{"no net/http import", fstest.MapFS{"a/a.go": file(`type http struct{ Client int }; var _ = http{}.Client`)}, 0, 0},
		{"blank import", fstest.MapFS{"a/a.go": file(`import _ "net/http"`)}, 0, 0},
		{"renamed net/http", fstest.MapFS{"a/a.go": file(`import h "net/http"; var _, _ = h.Client{}, h.DefaultClient`)}, 2, 0},
		{"a pointer conversion is a reference", fstest.MapFS{"a/a.go": file(`import "net/http"; var _ = (*http.Transport)(nil)`)}, 0, 0},
		{"owner and test kit are exempt", fstest.MapFS{
			"a/a.go":                      file(""),
			EgressOwner + "/e.go":         file(`import "net/http"; var _ = &http.Client{}`),
			TestkitDir + "/model/m.go":    file(`import ("net/http"; "` + LLMTransportPackage + `"); var _, _ = http.DefaultTransport, transport.Over`),
			TestkitDir + "/clock/c.go":    file(`import "net/http"; var _ = http.Get`),
			"internal/llm/transport/t.go": file(`import ("net/http"; "` + EgressPackage + `"); func Over(rt http.RoundTripper) *http.Client { return egress.Over(rt) }`),
		}, 0, 0},
		{"tests and testdata are exempt", fstest.MapFS{"a/a.go": file(""), "a/a_test.go": file(`import ("net/http"; ` + tp + `); var _, _ = http.Client{}, transport.Over`), "a/testdata/x.go": file(`import "net/http"; var _ = http.Get`)}, 0, 0},
		{"egress.Over outside llm/transport", fstest.MapFS{"internal/llm/provider/p.go": file(`import "` + EgressPackage + `"; var _ = egress.Over(nil)`)}, 0, 1},
		{"a lookalike directory is not llm/transport", fstest.MapFS{"internal/llm/transportx/p.go": file(`import "` + EgressPackage + `"; var _ = egress.Over(nil)`)}, 0, 1},
		{"dot imports", fstest.MapFS{"a/a.go": file(`import (. "net/http"; . ` + tp + `); var _ = Client{}`)}, 1, 1},
		{"own package calls its seam", fstest.MapFS{"internal/llm/transport/t.go": file(`func Over() {}; func f() { Over() }`)}, 0, 1},
		{"another function of the seam's package", fstest.MapFS{"internal/llm/transport/t.go": file(`import ` + tp + `; var _ = transport.New`)}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients, err := HTTPClients(tc.files)
			if err != nil || len(clients) != tc.clients {
				t.Errorf("HTTPClients = %q, %v; want %d", clients, err, tc.clients)
			}
			seams, err := SeamCalls(tc.files)
			if err != nil || len(seams) != tc.seams {
				t.Errorf("SeamCalls = %q, %v; want %d", seams, err, tc.seams)
			}
		})
	}
	for _, check := range []func(fs.FS) ([]string, error){HTTPClients, SeamCalls} {
		if _, err := check(fstest.MapFS{}); err == nil {
			t.Error("an empty tree passed")
		}
		if _, err := check(fstest.MapFS{"a/a.go": file("var (")}); err == nil {
			t.Error("a file that does not parse was accepted")
		}
	}
}
