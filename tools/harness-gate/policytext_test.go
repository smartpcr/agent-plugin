package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// policyRunCommit is the commit policyTextRepo makes of a fixture's text alone:
// fixed author, committer, dates and message make it the same on every host, so
// the committed run records of the fixtures can name it.
const policyRunCommit = "41101e7b57521c233f03210ab960fd3ceb665729"

var policyRunEnv = []string{
	"GIT_AUTHOR_NAME=dsh-fixture", "GIT_AUTHOR_EMAIL=fixture@dsh.invalid", "GIT_AUTHOR_DATE=2026-12-01T00:00:00Z",
	"GIT_COMMITTER_NAME=dsh-fixture", "GIT_COMMITTER_EMAIL=fixture@dsh.invalid", "GIT_COMMITTER_DATE=2026-12-01T00:00:00Z",
}

func policyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.autocrlf=false", "-c", "commit.gpgsign=false", "-C", root}, args...)...)
	cmd.Env = append(os.Environ(), policyRunEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// policyTextRepo copies fixture tree name to a temporary git checkout: the first
// commit is the run's commit, holding only the text (policyRunCommit for the
// committed text), the second adds the rest of the tree.
func policyTextRepo(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("the policy-text gate needs git: %v", err)
	}
	root := closureTree(t, filepath.Join("testdata", "policytext", name))
	policyGit(t, root, "init", "-q", "--object-format=sha1")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(PolicyTextDir))); err == nil {
		policyGit(t, root, "add", "--", PolicyTextDir)
		policyGit(t, root, "commit", "-q", "--no-verify", "-m", "DH-E-SANDBOX-7 run commit (fixture)")
	}
	policyGit(t, root, "add", "-A")
	policyGit(t, root, "commit", "-q", "--no-verify", "--allow-empty", "-m", "the rest of the fixture tree")
	return root
}

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
		root := policyTextRepo(t, tree.name)
		if tree.name == "text_with_matching_pass" {
			if run := policyGit(t, root, "rev-parse", "HEAD~1"); run != policyRunCommit {
				t.Fatalf("the fixture's run commit is %s, want %s (the run record names it)", run, policyRunCommit)
			}
		}
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

func editFile(t *testing.T, path, old, new string) {
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

func sandbox7Record(root string) string {
	return filepath.Join(root, "docs", "stories", "harness-DSH-M2", "evidence", "DH-S-17.md")
}

var sandbox7Row = regexp.MustCompile(`(?m)^\| DH-E-SANDBOX-7 \|.*$`)

// setSandbox7Row rewrites the fixture's DH-E-SANDBOX-7 row with commit and run.
func setSandbox7Row(t *testing.T, root, commit, run string) {
	t.Helper()
	b, err := os.ReadFile(sandbox7Record(root))
	if err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf("| DH-E-SANDBOX-7 | Linux (E1) | acceptance | required | E1 | %s | passed | run=%s; linux.md sha256=b6aafb9bf1f7d6f156af094486d3092854377da18ceddab7062f1f199a5bfe87 escalated=12/18 |", commit, run)
	if err := os.WriteFile(sandbox7Record(root), sandbox7Row.ReplaceAll(b, []byte(row)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRunRecord writes an intact run record runs/<id>.txt for commit.
func writeRunRecord(t *testing.T, root, id, commit string, exit int) {
	t.Helper()
	body := fmt.Sprintf("harness-gate evidence record\ncommit: %s\ntree: clean\n--- output ---\n--- end of output ---\nexit: %d\n", commit, exit)
	sum := sha256.Sum256([]byte(body))
	path := filepath.Join(root, filepath.FromSlash(PolicyRunsDir), id+".txt")
	if err := os.WriteFile(path, []byte(body+"sha256: "+hex.EncodeToString(sum[:])+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A file that is not <platform>.md, a pass below the bar and a record that has
// no DH-E-SANDBOX-7 row are refused too; a bad flag is a usage error.
func TestPolicyTextRefusals(t *testing.T) {
	root := policyTextRepo(t, "text_with_matching_pass")
	textDir := filepath.Join(root, filepath.FromSlash(PolicyTextDir))
	rec := sandbox7Record(root)
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
	editFile(t, rec, "escalated=12/18", "escalated=9/18")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, "9/18 escalations") {
		t.Errorf("below the bar: exit %d\n%s", code, stdout)
	}
	editFile(t, rec, "escalated=9/18", "escalated=10/18")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 0 {
		t.Errorf("exactly at the bar: exit %d\n%s", code, stdout)
	}
	editFile(t, rec, "run=wsl-41101e7-20261201T0000Z; ", "")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, "names no run") {
		t.Errorf("no run id: exit %d\n%s", code, stdout)
	}
	editFile(t, rec, "| DH-E-SANDBOX-7 |", "| DH-E-SANDBOX-6 |")
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, "has no DH-E-SANDBOX-7 row") {
		t.Errorf("no row: exit %d\n%s", code, stdout)
	}
	if code, _, _ := runClosureGate(t, "policy-text", "--root", root, "extra"); code != 2 {
		t.Errorf("an extra argument exits %d, want 2", code)
	}
}

// The run a passed row names must be an intact run record whose commit is the
// row's and contains the text's bytes, checked through git; anything that
// cannot be verified is refused (M2-C-6 fails closed).
func TestPolicyTextRunProvenance(t *testing.T) {
	const run = "wsl-41101e7-20261201T0000Z"
	copied := closureTree(t, filepath.Join("testdata", "policytext", "text_with_matching_pass"))
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", copied); code != 1 || !strings.Contains(stdout, "commit containment unavailable") {
		t.Errorf("a tree that is not a git checkout: exit %d, want 1 refusing on containment\n%s", code, stdout)
	}

	root := policyTextRepo(t, "text_with_matching_pass")
	runFile := filepath.Join(root, filepath.FromSlash(PolicyRunsDir), run+".txt")
	pristine, err := os.ReadFile(runFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "mktree")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	emptyTree := strings.TrimSpace(string(out))
	if err != nil || emptyTree == "" {
		t.Fatalf("git mktree: %v", err)
	}
	withoutText := policyGit(t, root, "commit-tree", emptyTree, "-m", "a commit without the text")
	noTextRun := "wsl-" + withoutText[:7] + "-20261201T0100Z"
	writeRunRecord(t, root, noTextRun, withoutText, 0)
	failedRun := "win-" + policyRunCommit[:7] + "-20261201T0200Z"
	writeRunRecord(t, root, failedRun, policyRunCommit, 1)

	for _, c := range []struct {
		name, commit, run, marker string
		tamper                    bool
	}{
		{"nonexistent run", "41101e7", "wsl-41101e7-20261202T0000Z", "no run record " + PolicyRunsDir + "/wsl-41101e7-20261202T0000Z.txt", false},
		{"not a run-record id", "41101e7", "local-1", `"local-1" is not a run-record id`, false},
		{"run id of another commit", "41101e7", "wsl-0000000-20261201T0000Z", "no run record", false},
		{"row commit is not the run's", "deadbee", run, "the row's commit deadbee is not the run's commit " + policyRunCommit, false},
		{"tampered run record", "41101e7", run, "is not intact", true},
		{"run that did not pass", policyRunCommit[:7], failedRun, "did not exit 0", false},
		{"run commit without the text", withoutText[:7], noTextRun, "commit " + withoutText + " does not contain " + PolicyTextDir + "/linux.md", false},
	} {
		setSandbox7Row(t, root, c.commit, c.run)
		body := pristine
		if c.tamper {
			body = []byte(strings.Replace(string(pristine), "12/18 escalated", "18/18 escalated", 1))
		}
		if err := os.WriteFile(runFile, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, c.marker) || !strings.Contains(stdout, "REFUSED "+PolicyTextRule) {
			t.Errorf("%s: exit %d, want 1 with %q\n%s", c.name, code, c.marker, stdout)
		}
	}
	setSandbox7Row(t, root, policyRunCommit, run)
	if err := os.WriteFile(runFile, pristine, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 0 || !strings.Contains(stdout, "run "+run+" at "+policyRunCommit) {
		t.Errorf("the full commit in the row: exit %d\n%s", code, stdout)
	}
}
