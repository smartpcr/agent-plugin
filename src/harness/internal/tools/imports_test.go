package tools_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The contract package imports no approval service, no tool-pipeline
// implementation and no llm provider, wire or transport package: the pipeline
// maps tools.Subject onto the approval service's subject, so the contract stays
// free of it (stage 16.2.2). internal/archtest checks the transitive rule; this
// checks the contract's own files.
//
//dsh:test DH-T-TOOLS-18
func TestContractImportsNoProviderNorApproval(t *testing.T) {
	const mod = "github.com/smartpcr/agent-plugin/src/harness/internal/"
	forbidden := []string{"human/approval", "tools/core", "tools/endpoint", "llm/provider", "llm/wire", "llm/transport"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		ast, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		n++
		for _, imp := range ast.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if p == mod+bad || strings.HasPrefix(p, mod+bad+"/") {
					t.Errorf("%s imports %s", f, p)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no contract files found")
	}
}
