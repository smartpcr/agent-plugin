package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/closure/expected.golden (refused under CI)")

// closureTree copies a fixture tree into a fresh temp dir so a run cannot touch testdata.
func closureTree(t *testing.T, dir string) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(dir)); err != nil {
		t.Fatalf("copy %s: %v", dir, err)
	}
	return tmp
}

func runClosureGate(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestStageClosureGolden runs ready and close over the four record states of
// implementation-plan Stage 1.1 step 5: open and not ready, open and ready, closed
// and ready, closed and not ready.
func TestStageClosureGolden(t *testing.T) {
	trees := []string{"open_not_ready", "open_ready", "closed_ready", "closed_not_ready"}
	var got strings.Builder
	for _, tree := range trees {
		for _, sub := range []string{"ready", "close"} {
			root := closureTree(t, filepath.Join("testdata", "closure", tree))
			code, stdout, stderr := runClosureGate(t, sub, "--root", root, "--stage", "DH-S-16", "--built")
			fmt.Fprintf(&got, "== %s: %s --built (exit %d)\n%s", tree, sub, code, stdout)
			if stderr != "" {
				fmt.Fprintf(&got, "stderr: %s", stderr)
			}
		}
	}
	golden := filepath.Join("testdata", "closure", "expected.golden")
	if *updateGolden {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI: regenerate the golden locally and review the diff")
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update locally to create it): %v", err)
	}
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != got.String() {
		t.Errorf("stage-closure output differs from %s\n--- got\n%s--- want\n%s", golden, got.String(), norm)
	}
}

// The exit codes the golden records, asserted directly so a regenerated golden
// cannot silently flip a refusal into a pass.
func TestStageClosureExitCodes(t *testing.T) {
	cases := []struct {
		tree, sub string
		code      int
		marker    string
	}{
		{"open_not_ready", "ready", 1, "NOT READY"},
		{"open_ready", "ready", 0, ": ready;"},
		{"closed_ready", "ready", 0, ": ready;"},
		{"closed_not_ready", "ready", 1, "NOT READY"},
		{"open_not_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"open_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"closed_ready", "close", 0, ": closed;"},
		{"closed_not_ready", "close", 1, "REFUSED " + closure.ClosedButNotReady},
	}
	for _, c := range cases {
		t.Run(c.tree+"/"+c.sub, func(t *testing.T) {
			root := closureTree(t, filepath.Join("testdata", "closure", c.tree))
			code, stdout, stderr := runClosureGate(t, c.sub, "--root", root, "--stage", "DH-S-16", "--built")
			if code != c.code || !strings.Contains(stdout, c.marker) {
				t.Errorf("exit %d, want %d; stdout must contain %q\nstdout:\n%s\nstderr:\n%s", code, c.code, c.marker, stdout, stderr)
			}
		})
	}
}

func TestStageClosureUsage(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "closure", "open_ready"))
	if code, _, _ := runClosureGate(t, "ready", "--root", root, "--stage", "S16"); code != 2 {
		t.Errorf("a bad --stage exits %d, want 2", code)
	}
	if code, stdout, _ := runClosureGate(t, "ready", "--root", root, "--stage", "DH-S-17"); code != 1 || !strings.Contains(stdout, "not found") && !strings.Contains(stdout, "no such file") && !strings.Contains(stdout, "cannot find") {
		t.Errorf("a missing record exits %d, want 1 naming it; stdout:\n%s", code, stdout)
	}
	if code, _, _ := runClosureGate(t, "records", "--root", root, "--story", "harness:NOPE"); code != 2 {
		t.Errorf("an unknown story exits %d, want 2", code)
	}
}

// TestRecordsCreated: the seven records and closure.md as Stage 1.1 created them
// hold their shape, and a record missing a sec. 7.3 field is refused.
func TestRecordsCreated(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "records", "created"))
	if code, stdout, stderr := runClosureGate(t, "records", "--root", root); code != 0 {
		t.Fatalf("records on the created tree: exit %d\n%s%s", code, stdout, stderr)
	}
	path := filepath.Join(root, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-13")))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), "### Cleanup evidence", "### Cleanup", 1)
	if broken == string(b) {
		t.Fatal("fixture DH-S-13.md has no Cleanup evidence heading")
	}
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runClosureGate(t, "records", "--root", root)
	if code != 1 || !strings.Contains(stdout, "DH-S-13") || !strings.Contains(stdout, "Cleanup evidence") {
		t.Errorf("a record missing a field: exit %d, want 1 naming DH-S-13 and the field\n%s", code, stdout)
	}

	// An open record may leave cells empty, but deleting an owed row is refused.
	path16 := filepath.Join(root, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-16")))
	b16, err := os.ReadFile(path16)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(string(b16), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "| Windows double-click |") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path16, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runClosureGate(t, "records", "--root", root)
	if code != 1 || !strings.Contains(stdout, `owner default "Windows double-click" has no row`) {
		t.Errorf("a deleted owner default: exit %d, want 1 naming it\n%s", code, stdout)
	}
}

// TestRecordsLive checks the repository's own M2 records and closure record.
func TestRecordsLive(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(closure.ClosureRecordPath(closure.DefaultStory)))); err != nil {
		t.Skipf("not run inside the repository: %v", err)
	}
	if code, stdout, stderr := runClosureGate(t, "records", "--root", root); code != 0 {
		t.Fatalf("records on the repository: exit %d\n%s%s", code, stdout, stderr)
	}
}

// TestAcceptanceIndexLiveM2Keys: the index names every stage M2 runs or authors for
// (implementation-plan Stage 1.1 step 3), and built-through has not gone backwards.
func TestAcceptanceIndexLiveM2Keys(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(acceptance.IndexPath))); err != nil {
		t.Skipf("not run inside the repository: %v", err)
	}
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"DH-S-16", "DH-S-17", "DH-S-18", "DH-S-22", "DH-S-27", "DH-S-39", "DH-S-48"} {
		if _, ok := x.Stage(s); !ok {
			t.Errorf("index has no %s key", s)
		}
	}
	n, ok := acceptance.StageNumber(x.BuiltThrough)
	if !ok || n < 8 {
		t.Errorf("built-through %q, want DH-S-8 or later", x.BuiltThrough)
	}
}
