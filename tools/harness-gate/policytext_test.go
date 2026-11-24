package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPolicyTextGolden is the M2-C-6 gate on the four committed fixture trees of
// implementation-plan Stage 2.4: only no_text and text_with_matching_pass exit 0,
// the other two exit 1 naming M2-C-6; the output equals expected.golden.
func TestPolicyTextGolden(t *testing.T) {
	trees := []struct {
		name string
		code int
	}{
		{"no_text", 0},
		{"text_without_sandbox7", 1},
		{"text_with_matching_pass", 0},
		{"text_edited_after_pass", 1},
	}
	var got strings.Builder
	for _, tree := range trees {
		root := closureTree(t, filepath.Join("testdata", "policytext", tree.name))
		code, stdout, stderr := runClosureGate(t, "policy-text", "--root", root)
		fmt.Fprintf(&got, "== %s (exit %d)\n%s", tree.name, code, stdout)
		if stderr != "" {
			fmt.Fprintf(&got, "stderr: %s", stderr)
		}
		if code != tree.code {
			t.Errorf("%s: exit %d, want %d\n%s%s", tree.name, code, tree.code, stdout, stderr)
		}
		if refused := strings.Contains(stdout, "REFUSED "+PolicyTextRule); refused != (tree.code != 0) {
			t.Errorf("%s: a refusal naming %s is printed: %v, want %v\n%s", tree.name, PolicyTextRule, refused, tree.code != 0, stdout)
		}
	}
	golden := filepath.Join("testdata", "policytext", "expected.golden")
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
		t.Errorf("policy-text output differs from %s\n--- got\n%s--- want\n%s", golden, got.String(), norm)
	}
}

// A file that is not <platform>.md, a pass below the bar and a record that has
// no DH-E-SANDBOX-7 row are refused too; a bad flag is a usage error.
func TestPolicyTextRefusals(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "policytext", "text_with_matching_pass"))
	textDir := filepath.Join(root, filepath.FromSlash(PolicyTextDir))
	rec := filepath.Join(root, "docs", "stories", "harness-DSH-M2", "evidence", "DH-S-17.md")
	edit := func(path, old, new string) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), old) {
			t.Fatalf("%s has no %q", path, old)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(textDir, "plan9.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runClosureGate(t, "policy-text", "--root", root)
	if code != 1 || !strings.Contains(stdout, "plan9.md") || !strings.Contains(stdout, "not a platform text") || !strings.Contains(stdout, "ok: "+PolicyTextDir+"/linux.md") {
		t.Errorf("an unknown platform file: exit %d\n%s", code, stdout)
	}
	if err := os.Remove(filepath.Join(textDir, "plan9.md")); err != nil {
		t.Fatal(err)
	}
	edit(rec, "escalated=12/18", "escalated=9/18")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, "9/18 escalations") {
		t.Errorf("below the bar: exit %d\n%s", code, stdout)
	}
	edit(rec, "escalated=9/18", "escalated=10/18")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 0 {
		t.Errorf("exactly at the bar: exit %d\n%s", code, stdout)
	}
	edit(rec, "run=local-20260101T0000Z-1; ", "")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, "names no run") {
		t.Errorf("no run id: exit %d\n%s", code, stdout)
	}
	edit(rec, "| DH-E-SANDBOX-7 |", "| DH-E-SANDBOX-6 |")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, "has no DH-E-SANDBOX-7 row") {
		t.Errorf("no row: exit %d\n%s", code, stdout)
	}
	if code, _, _ := runClosureGate(t, "policy-text", "--root", root, "extra"); code != 2 {
		t.Errorf("an extra argument exits %d, want 2", code)
	}
}

// When the root is a git checkout's top level the passed row's commit must
// contain the file with the recorded bytes: the run's own commit passes, a commit
// without the file is refused.
func TestPolicyTextCommitContainment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := closureTree(t, filepath.Join("testdata", "policytext", "text_with_matching_pass"))
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=harness-gate", "-c", "user.email=harness-gate@example.invalid", "-c", "core.autocrlf=false", "-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "before the text")
	empty := git("rev-parse", "HEAD")
	git("add", "-A")
	git("commit", "-q", "-m", "the text")
	withText := git("rev-parse", "HEAD")
	rec := filepath.Join(root, "docs", "stories", "harness-DSH-M2", "evidence", "DH-S-17.md")
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		commit string
		code   int
		marker string
	}{
		{withText, 0, "commit containment: git"},
		{empty, 1, "does not contain " + PolicyTextDir + "/linux.md"},
	} {
		if err := os.WriteFile(rec, []byte(strings.Replace(string(b), "| 0123abc |", "| "+c.commit+" |", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != c.code || !strings.Contains(stdout, c.marker) {
			t.Errorf("commit %s: exit %d, want %d with %q\n%s", c.commit, code, c.code, c.marker, stdout)
		}
	}
}
