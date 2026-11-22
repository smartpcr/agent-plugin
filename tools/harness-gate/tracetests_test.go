package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// traceFixture writes a one-stage repository whose step requires DH-T-X-1 and
// DH-T-X-2, and a test file whose source is test.
func traceFixture(t *testing.T, test string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range map[string]string{
		acceptance.ImplPath:  "## 6\n\n### DH-S-1: One\n\n**Steps.**\n\n- [x] **1.1** A step (`DH-T-X-1`, `-2`).\n",
		acceptance.IndexPath: "built-through: DH-S-1\nstages:\n  DH-S-18: []\n",
		"src/m/x_test.go":    "package x\n\nimport \"testing\"\n\n" + test,
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTraceTestsSubcommand(t *testing.T) {
	tests := []struct {
		name, test string
		args       []string
		code       int
		want       string
	}{
		{"all tagged", "//dsh:test DH-T-X-1, DH-T-X-2\nfunc TestX(t *testing.T) {}\n", nil, 0, "harness-gate trace-tests: passed"},
		{"one missing", "//dsh:test DH-T-X-1\nfunc TestX(t *testing.T) {}\n", []string{"--stage", "DH-S-1"}, 1, "step 1.1 of DH-S-1: no test names DH-T-X-2"},
		{"bad stage", "", []string{"--stage", "S-1"}, 2, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := traceFixture(t, tt.test)
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), append([]string{"trace-tests", "--root", root}, tt.args...), &stdout, &stderr)
			if code != tt.code || !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("trace-tests %v = %d\nstdout:\n%s\nstderr:\n%s\nwant %d and %q", tt.args, code, stdout.String(), stderr.String(), tt.code, tt.want)
			}
		})
	}
	for _, args := range [][]string{{"trace-tests", "extra"}, {"trace-tests", "--bogus"}, {"trace-tests", "--root", t.TempDir()}} {
		if code := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}
