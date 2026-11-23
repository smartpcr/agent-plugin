package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
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

// traceGateFixture is traceFixture plus a stub tools/harness-trace whose verify
// passes, an impl.md whose later stage DH-S-16 names DH-T-X-2 (so a doc.go may defer
// it there), and files (slash paths; "src/m/x_test.go" is the test file).
func traceGateFixture(t *testing.T, files map[string]string) *Config {
	t.Helper()
	root := traceFixture(t, files["src/m/x_test.go"])
	extra := map[string]string{
		acceptance.ImplPath: "## 6\n\n### DH-S-1: One\n\n**Steps.**\n\n- [x] **1.1** A step (`DH-T-X-1`, `-2`).\n\n" +
			"### DH-S-16: Later\n\n**Steps.**\n\n- [ ] **16.1** Something (`DH-T-X-2`).\n",
		"tools/harness-trace/go.mod":  "module example.com/harness-trace\n\ngo 1.21\n",
		"tools/harness-trace/main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"verify: ok\") }\n",
	}
	for rel, src := range files {
		if rel != "src/m/x_test.go" {
			extra[rel] = src
		}
	}
	for rel, src := range extra {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	return &Config{Root: root, Go: gobin, Environ: os.Environ()}
}

// TestTraceGateRunsTraceTests: the trace gate runs the trace-tests check through
// built-through (step 18.1.4, M1-G-CLOSE-9). A required ID that no test names, or
// one whose deferral impl.md does not back, fails it with the subcommand's message
// even though harness-trace verify passes; every ID tagged, or deferred to a later
// stage that names it, passes.
func TestTraceGateRunsTraceTests(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string // "" passes
	}{
		{"one missing", map[string]string{
			"src/m/x_test.go": "//dsh:test DH-T-X-1\nfunc TestX(t *testing.T) {}\n",
		}, "harness-gate trace-tests: FAILED:\nstep 1.1 of DH-S-1: no test names DH-T-X-2"},
		{"unbacked deferral", map[string]string{
			"src/m/x_test.go": "//dsh:test DH-T-X-2\nfunc TestX(t *testing.T) {}\n",
			"src/m/doc.go":    "//dsh:test DH-T-X-1 later DH-S-16\npackage x\n",
		}, "step 1.1 of DH-S-1: no test names DH-T-X-1"},
		{"all tagged", map[string]string{
			"src/m/x_test.go": "//dsh:test DH-T-X-1, DH-T-X-2\nfunc TestX(t *testing.T) {}\n",
		}, ""},
		{"one deferred", map[string]string{
			"src/m/x_test.go": "//dsh:test DH-T-X-1\nfunc TestX(t *testing.T) {}\n",
			"src/m/doc.go":    "//dsh:test DH-T-X-2 later DH-S-16\npackage x\n",
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := traceGateFixture(t, tt.files)
			var out bytes.Buffer
			err := cfg.traceGate(context.Background(), &out)
			if !strings.Contains(out.String(), "trace: verify: ok") {
				t.Errorf("trace gate did not run harness-trace verify:\n%s", out.String())
			}
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("trace gate = %v, want a pass\n%s", err, out.String())
			case tt.want == "" && (!strings.Contains(out.String(), "trace-tests: DH-S-1..DH-S-1: required 2,") || !strings.Contains(out.String(), "missing 0\n")):
				t.Errorf("trace gate printed no trace-tests totals:\n%s", out.String())
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("trace gate = %v, want a failure containing %q\n%s", err, tt.want, out.String())
			}
		})
	}
}
