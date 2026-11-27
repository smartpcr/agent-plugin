//go:build replay

package replay

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// boundary is one parser of untrusted bytes named by DH-ADR-TOOLCHAIN-3 and the
// fuzz target that covers it.
type boundary struct {
	what   string // the ADR's name for the boundary
	module string // the module directory under src
	pkg    string // the package directory in the module
	target string // the fuzz target
}

// boundaries are the parsers of untrusted bytes in this tree (DH-ADR-TOOLCHAIN-3).
// The ADR's MCP frames and shell-command splitting have no parser here yet; the
// stage that adds one adds its row.
var boundaries = []boundary{
	{"session log lines", "harness", "internal/session/store/sqlite", "FuzzParseExport"},
	{"configuration files", "harness", "internal/boot/yamlx", "FuzzParse"},
	{"tool-call JSON from the model", "harness", "internal/schema", "FuzzValidate"},
	{"streamed model responses", "harness", "internal/llm/transport", "FuzzSSE"},
	{"streamed model responses (Anthropic events)", "harness", "internal/llm/wire/anthropic", "FuzzSSE"},
	{"JSON-RPC frames", "sdk", "jsonrpc", "FuzzDecode"},
	{"patch text", "harness", "internal/util/textdiff/patch", "FuzzParse"},
}

// srcDir is the repository's src directory, from this package's directory.
const srcDir = "../../.."

// Each listed boundary parser has a fuzz target, its committed inputs under
// testdata/fuzz/<target> are valid corpus files, and plain go test (no -fuzz)
// runs every one of them as a passing case.
//
//dsh:test DH-T-TOOLCHAIN-3
func TestBoundaryParsersRunTheirCommittedCrashers(t *testing.T) {
	for _, b := range boundaries {
		t.Run(b.what, func(t *testing.T) {
			dir := filepath.Join(srcDir, b.module, filepath.FromSlash(b.pkg))
			if !declaresFuzz(t, dir, b.target) {
				t.Fatalf("%s: no func %s(f *testing.F) in %s", b.what, b.target, dir)
			}
			corpus := filepath.Join(dir, "testdata", "fuzz", b.target)
			entries, err := os.ReadDir(corpus)
			if err != nil || len(entries) == 0 {
				t.Fatalf("%s: no committed crasher under %s (%v)", b.what, corpus, err)
			}
			cmd := exec.Command("go", "test", "-run", "^"+b.target+"$", "-v", ".")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go test -run %s in %s: %v\n%s", b.target, dir, err, out)
			}
			for _, e := range entries {
				data, err := os.ReadFile(filepath.Join(corpus, e.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.HasPrefix(data, []byte("go test fuzz v1\n")) {
					t.Errorf("%s: %s is not a corpus file", b.what, e.Name())
				}
				pass := regexp.MustCompile(`(?m)^\s*--- PASS: ` + regexp.QuoteMeta(b.target+"/"+e.Name()) + ` `)
				if !pass.Match(out) {
					t.Errorf("%s: plain go test did not run %s as a passing case:\n%s", b.what, e.Name(), out)
				}
			}
		})
	}
}

// declaresFuzz reports whether a _test.go file in dir declares fuzz target name.
func declaresFuzz(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "\nfunc "+name+"(f *testing.F) {") {
			return true
		}
	}
	return false
}
