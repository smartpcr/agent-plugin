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
	"time"
)

// policyRunCommit is the commit policyTextRepo makes of a fixture's text alone:
// fixed author, committer, dates and message make it the same on every host, so
// the committed run records of the fixtures can name it.
const policyRunCommit = "41101e7b57521c233f03210ab960fd3ceb665729"

// policyFixtureRun is the run the passed rows of the fixtures name; its record is
// the producer's own output (TestPolicyTextFixtureRecordIsProduced).
const (
	policyFixtureRun  = "s17-41101e7-20261201T0000Z"
	policyFixtureText = "b6aafb9bf1f7d6f156af094486d3092854377da18ceddab7062f1f199a5bfe87"
)

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

// setSandbox7Row rewrites the DH-E-SANDBOX-7 row with a pass of the linux text
// with sha at commit by run.
func setSandbox7Row(t *testing.T, root, commit, run, sha string, escalated int) {
	t.Helper()
	b, err := os.ReadFile(sandbox7Record(root))
	if err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf("| DH-E-SANDBOX-7 | Linux (E1) | acceptance | required | E1 | %s | passed | run=%s; linux.md sha256=%s escalated=%d/18 |", commit, run, sha, escalated)
	if !sandbox7Row.Match(b) {
		t.Fatalf("%s has no DH-E-SANDBOX-7 row", sandbox7Record(root))
	}
	if err := os.WriteFile(sandbox7Record(root), sandbox7Row.ReplaceAll(b, []byte(row)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRecordPath(root, id string) string {
	return filepath.Join(root, filepath.FromSlash(PolicyRunsDir), id+".txt")
}

// writeAcceptanceRecord writes runs/<id>.txt as formatSandbox7Record, the
// producer's formatter, writes it for the linux text.
func writeAcceptanceRecord(t *testing.T, root, id, commit, sha string, escalated int) {
	t.Helper()
	text := sandbox7Text{Path: PolicyTextDir + "/linux.md", SHA256: sha, Escalated: escalated, Of: PolicyTextEscalations}
	body := formatSandbox7Record(commit, "", time.Date(2026, 12, 1, 0, 30, 0, 0, time.UTC), []sandbox7Text{text})
	if err := os.WriteFile(runRecordPath(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resealed is record body with old replaced by new and its sha256 line redone:
// an intact record of other content.
func resealed(t *testing.T, body, old, new string) string {
	t.Helper()
	if !strings.Contains(body, old) {
		t.Fatalf("the record has no %q", old)
	}
	body = strings.Replace(body, old, new, 1)
	body = body[:strings.LastIndex(body, "sha256: ")]
	sum := sha256.Sum256([]byte(body))
	return body + "sha256: " + hex.EncodeToString(sum[:]) + "\n"
}

// The committed fixture run record is byte for byte what the producer's
// formatter writes for the fixture's run, so the fixtures carry no hand-made
// record format.
func TestPolicyTextFixtureRecordIsProduced(t *testing.T) {
	text := sandbox7Text{Path: PolicyTextDir + "/linux.md", SHA256: policyFixtureText, Escalated: 12, Of: PolicyTextEscalations}
	want := formatSandbox7Record(policyRunCommit, "fixture", time.Date(2026, 12, 1, 0, 30, 0, 0, time.UTC), []sandbox7Text{text})
	for _, tree := range []string{"text_with_matching_pass", "text_edited_after_pass"} {
		path := runRecordPath(filepath.Join("testdata", "policytext", tree), policyFixtureRun)
		if *updateGolden && os.Getenv("CI") == "" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if norm := strings.ReplaceAll(string(got), "\r\n", "\n"); norm != want {
			t.Errorf("%s is not the producer's record (run with -update locally)\n--- got\n%s--- want\n%s", path, norm, want)
		}
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
	writeAcceptanceRecord(t, root, policyFixtureRun, policyRunCommit, policyFixtureText, 10)
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 0 {
		t.Errorf("exactly at the bar: exit %d\n%s", code, stdout)
	}
	editFile(t, rec, "run="+policyFixtureRun+"; ", "")
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

// The run a passed row names must be an intact, passed DH-E-SANDBOX-7
// acceptance record on a clean tree, recording the file's bytes and the row's
// count, whose commit is the row's and contains the text, checked through git;
// anything that cannot be verified is refused (M2-C-6 fails closed).
func TestPolicyTextRunProvenance(t *testing.T) {
	const run = policyFixtureRun
	copied := closureTree(t, filepath.Join("testdata", "policytext", "text_with_matching_pass"))
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", copied); code != 1 || !strings.Contains(stdout, "commit containment unavailable") {
		t.Errorf("a tree that is not a git checkout: exit %d, want 1 refusing on containment\n%s", code, stdout)
	}

	root := policyTextRepo(t, "text_with_matching_pass")
	pristine, err := os.ReadFile(runRecordPath(root, run))
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
	noTextRun := "s17-" + withoutText[:7] + "-20261201T0100Z"
	writeAcceptanceRecord(t, root, noTextRun, withoutText, policyFixtureText, 12)
	failedRun := "s17-" + policyRunCommit[:7] + "-20261201T0200Z"
	writeAcceptanceRecord(t, root, failedRun, policyRunCommit, policyFixtureText, 9)
	quality := "harness-gate evidence record\ncommit: " + policyRunCommit + "\ntree: " + CleanTreeField + "\n--- output ---\n--- end of output ---\nexit: 0\n"

	for _, c := range []struct {
		name, commit, run, marker string
		record                    string // the body of runs/<run>.txt, pristine when ""
	}{
		{"nonexistent run", "41101e7", "s17-41101e7-20261202T0000Z", "no run record " + PolicyRunsDir + "/s17-41101e7-20261202T0000Z.txt", ""},
		{"not a run-record id", "41101e7", "local-1", `"local-1" is not a run-record id`, ""},
		{"run id of another commit", "41101e7", "s17-0000000-20261201T0000Z", "no run record", ""},
		{"row commit is not the run's", "deadbee", run, "the row's commit deadbee is not the run's commit " + policyRunCommit, ""},
		{"tampered run record", "41101e7", run, "is not intact", strings.Replace(string(pristine), "escalated=12/18", "escalated=18/18", 1)},
		{"record of a dirty tree", "41101e7", run, `was not taken on a clean tree (tree: "dirty")`, resealed(t, string(pristine), "tree: "+CleanTreeField, "tree: dirty")},
		{"record of another scenario", "41101e7", run, `is of scenario "DH-E-SANDBOX-6"`, resealed(t, string(pristine), "scenario: DH-E-SANDBOX-7", "scenario: DH-E-SANDBOX-6")},
		{"record of other text bytes", "41101e7", run, "records " + PolicyTextDir + "/linux.md with sha256 " + strings.Repeat("0", 64) + ", not the file's", resealed(t, string(pristine), policyFixtureText, strings.Repeat("0", 64))},
		{"record of another count", "41101e7", run, "records 11/18 escalations for " + PolicyTextDir + "/linux.md, the row 12/18", resealed(t, string(pristine), "escalated=12/18", "escalated=11/18")},
		{"record without the text", "41101e7", run, "does not record " + PolicyTextDir + "/linux.md", resealed(t, string(pristine), PolicyTextDir+"/linux.md", PolicyTextDir+"/darwin.md")},
		{"quality-gate evidence record", "41101e7", run, "is a quality-gate evidence record (harness-gate --evidence), not a DH-E-SANDBOX-7 acceptance record", resealed(t, quality+"sha256: \n", "exit: 0", "exit: 0")},
		{"run that did not pass", policyRunCommit[:7], failedRun, `is "failed", not passed`, ""},
		{"run commit without the text", withoutText[:7], noTextRun, "commit " + withoutText + " does not contain " + PolicyTextDir + "/linux.md", ""},
	} {
		setSandbox7Row(t, root, c.commit, c.run, policyFixtureText, 12)
		body := string(pristine)
		if c.record != "" {
			body = c.record
		}
		if err := os.WriteFile(runRecordPath(root, run), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 1 || !strings.Contains(stdout, c.marker) || !strings.Contains(stdout, "REFUSED "+PolicyTextRule) {
			t.Errorf("%s: exit %d, want 1 with %q\n%s", c.name, code, c.marker, stdout)
		}
	}
	setSandbox7Row(t, root, policyRunCommit, run, policyFixtureText, 12)
	if err := os.WriteFile(runRecordPath(root, run), pristine, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runClosureGate(t, "policy-text", "--root", root); code != 0 || !strings.Contains(stdout, "run "+run+" at "+policyRunCommit) {
		t.Errorf("the full commit in the row: exit %d\n%s", code, stdout)
	}
}

// sandbox7RecordRun runs "harness-gate sandbox7-record" on root and returns the
// run id it names, "" when it wrote none.
func sandbox7RecordRun(t *testing.T, root string, wantCode int, args ...string) (id, stderr string) {
	t.Helper()
	code, stdout, stderr := runClosureGate(t, append([]string{"sandbox7-record", "--root", root}, args...)...)
	if code != wantCode {
		t.Fatalf("sandbox7-record %v: exit %d, want %d\n%s%s", args, code, wantCode, stdout, stderr)
	}
	if m := regexp.MustCompile(`as run=(\S+)\)`).FindStringSubmatch(stdout); m != nil {
		return m[1], stderr
	}
	return "", stderr
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// The producer's record is the consumer's proof, with no gate run in between:
// the first text is recorded on its commit by "harness-gate sandbox7-record"
// and admitted once the row names that run; an edit is refused until the edited
// commit is recorded passed again; a failed run never admits a text.
func TestSandbox7RecordFlows(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "policytext", "text_without_sandbox7"))
	policyGit(t, root, "init", "-q", "--object-format=sha1")
	commitAll := func(msg string) string {
		policyGit(t, root, "add", "-A")
		policyGit(t, root, "commit", "-q", "--no-verify", "-m", msg)
		return policyGit(t, root, "rev-parse", "HEAD")
	}
	gate := func(want int, marker string) {
		t.Helper()
		code, stdout, _ := runClosureGate(t, "policy-text", "--root", root)
		if code != want || !strings.Contains(stdout, marker) {
			t.Fatalf("policy-text: exit %d, want %d with %q\n%s", code, want, marker, stdout)
		}
	}
	text := filepath.Join(root, filepath.FromSlash(PolicyTextDir), "linux.md")

	// First text: committed on the branch, refused, recorded, admitted.
	first := commitAll("the first shipped text")
	gate(1, "no passed DH-E-SANDBOX-7 row")
	stray := filepath.Join(root, "stray.txt")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr := sandbox7RecordRun(t, root, 2, "--escalated", "linux=12"); !strings.Contains(stderr, "is not clean") || !strings.Contains(stderr, "stray.txt") {
		t.Errorf("a dirty tree is recorded: %s", stderr)
	}
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	if _, stderr := sandbox7RecordRun(t, root, 2, "--escalated", "darwin=12"); !strings.Contains(stderr, "no escalation count for "+PolicyTextDir+"/linux.md") {
		t.Errorf("a missing count is recorded: %s", stderr)
	}
	id, _ := sandbox7RecordRun(t, root, 0, "--escalated", "linux=12", "--ci-run", "4242")
	if !strings.HasPrefix(id, "s17-"+first[:7]+"-") {
		t.Fatalf("run id %q is not of commit %s", id, first)
	}
	rec, err := os.ReadFile(runRecordPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"commit: " + first + "\n", "tree: " + CleanTreeField + "\n", "ci run: 4242\n", "result: passed\n"} {
		if !strings.Contains(string(rec), want) {
			t.Errorf("the record has no %q:\n%s", want, rec)
		}
	}
	gate(1, "no passed DH-E-SANDBOX-7 row")
	setSandbox7Row(t, root, first[:7], id, sha256Hex(t, text), 12)
	gate(0, "run "+id+" at "+first)
	commitAll("DH-E-SANDBOX-7 passed on the first text")
	gate(0, "run "+id+" at "+first)

	// Re-proved edit: the edit is refused until its own commit is recorded.
	editFile(t, text, "\n", "\nAsk before writing outside the workspace.\n")
	edited := commitAll("edit the text")
	gate(1, "(edited after the run)")
	id2, _ := sandbox7RecordRun(t, root, 0, "--escalated", "linux=11")
	if !strings.HasPrefix(id2, "s17-"+edited[:7]+"-") {
		t.Fatalf("run id %q is not of commit %s", id2, edited)
	}
	gate(1, "(edited after the run)")
	setSandbox7Row(t, root, edited[:7], id2, sha256Hex(t, text), 11)
	gate(0, "run "+id2+" at "+edited)
	setSandbox7Row(t, root, edited[:7], id, sha256Hex(t, text), 11)
	gate(1, "the row's commit "+edited[:7]+" is not the run's commit "+first)
	setSandbox7Row(t, root, edited[:7], id2, sha256Hex(t, text), 11)
	commitAll("DH-E-SANDBOX-7 passed on the edited text")

	// A failed run is recorded, exits 1 and admits nothing.
	editFile(t, text, "\n", "\nNever escalate.\n")
	weak := commitAll("a weaker text")
	id3, _ := sandbox7RecordRun(t, root, 1, "--escalated", "linux=9")
	if id3 == "" {
		t.Fatal("a failed run is not recorded")
	}
	setSandbox7Row(t, root, weak[:7], id3, sha256Hex(t, text), 12)
	gate(1, `is "failed", not passed`)
}

// The shards of one run are recorded from their own report files, each platform
// once: the three files of one commit are joined, a stale or second report of
// a platform and a report of more than one line are refused, and counts from a
// go test log that does not show the scenario passed record nothing.
func TestSandbox7RecordReadsShardReports(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "policytext", "text_without_sandbox7"))
	textDir := filepath.Join(root, filepath.FromSlash(PolicyTextDir))
	linux, err := os.ReadFile(filepath.Join(textDir, "linux.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(textDir, "darwin.md"), linux, 0o644); err != nil {
		t.Fatal(err)
	}
	policyGit(t, root, "init", "-q", "--object-format=sha1")
	policyGit(t, root, "add", "-A")
	policyGit(t, root, "commit", "-q", "--no-verify", "-m", "two shipped texts")

	out := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(out, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	linuxR := write("report-linux.txt", "linux=12\n")
	darwinR := write("report-darwin.txt", "darwin=11\r\n")
	staleR := write("report.txt", "linux=9,darwin=9\n")
	pasted := write("pasted.txt", "linux=12\ndarwin=11\n")
	passLog := write("pass.txt", "=== RUN   "+sandbox7Test+"\n--- PASS: "+sandbox7Test+" (1.00s)\nPASS\nok  \tscenarios\t1.0s\n")
	shardLog := write("shard.txt", "--- PASS: "+sandbox7Test+" (1.00s)\n    --- PASS: "+sandbox7Test+"/darwin (1.00s)\nok  \tscenarios\t1.0s\n")
	failLog := write("fail.txt", "--- FAIL: "+sandbox7Test+" (1.00s)\n    --- FAIL: "+sandbox7Test+"/linux/task07/run1 (1.00s)\nFAIL\nFAIL\tscenarios\t1.0s\n")
	emptyLog := write("empty.txt", "no test ran\n")

	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"stale aggregate", []string{"--report", linuxR, "--report", darwinR, "--report", staleR}, "names linux twice"},
		{"pasted lines", []string{"--report", pasted}, "want one line"},
		{"both sources", []string{"--escalated", "linux=12", "--report", darwinR}, "one of the two"},
		{"no source", nil, "one of the two"},
		{"failed scenario", []string{"--report", linuxR, "--report", darwinR, "--go-test-log", passLog, "--go-test-log", failLog}, "the scenario failed"},
		{"no passed run", []string{"--report", linuxR, "--report", darwinR, "--go-test-log", emptyLog}, "no passed " + sandbox7Test},
	} {
		if id, stderr := sandbox7RecordRun(t, root, 2, c.args...); id != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: run %q, stderr %q, want %q", c.name, id, stderr, c.want)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(PolicyRunsDir))); len(entries) != 0 {
		t.Fatalf("a refused input wrote %d records", len(entries))
	}

	id, _ := sandbox7RecordRun(t, root, 0, "--report", linuxR, "--report", darwinR, "--go-test-log", passLog, "--go-test-log", shardLog)
	rec, err := os.ReadFile(runRecordPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/linux.md sha256=", " escalated=12/18\n", "/darwin.md sha256=", " escalated=11/18\n", "result: passed\n"} {
		if !strings.Contains(string(rec), want) {
			t.Errorf("the record has no %q:\n%s", want, rec)
		}
	}
}
