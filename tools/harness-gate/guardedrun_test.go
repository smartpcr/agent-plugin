package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/guarded"
)

// TestGuardedRows: the record's Scenarios-table rows come from the scenario
// helper's report, not the exit status; a planned ID with no row is not run, and
// the Windows lane's excluded STORE-2 row carries its decision.
func TestGuardedRows(t *testing.T) {
	report := filepath.Join(t.TempDir(), "report.jsonl")
	lines := `{"kind":"scenario","id":"DH-E-STORE-1","result":"passed","os":"windows"}
not a row
{"kind":"scenario","id":"DH-E-STORE-6","result":"passed"}
{"kind":"scenario","id":"DH-E-STORE-6","result":"failed"}
{"kind":"other","id":"DH-E-FS-3","result":"passed"}
`
	if err := os.WriteFile(report, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	run, out := guarded.Select(guarded.Windows, []string{"DH-E-STORE-1", "DH-E-STORE-2", "DH-E-STORE-6", "DH-E-FS-3"})
	g := &guardedRun{lane: guarded.Windows, commit: "0123456789abcdef", out: out}
	for _, id := range run {
		g.plan.Scenarios = append(g.plan.Scenarios, acceptance.Scenario{ID: id})
	}
	var w bytes.Buffer
	g.rows(&w, reportResults(report))
	want := strings.Join([]string{
		"row: | DH-E-STORE-1 | Windows | guarded | required | scratch home | 0123456 | passed | |",
		"row: | DH-E-STORE-6 | Windows | guarded | required | scratch home | 0123456 | failed | |",
		"row: | DH-E-FS-3 | Windows | guarded | required | scratch home | 0123456 | not run (no report row) | |",
		"row: | DH-E-STORE-2 | Windows | guarded | required | scratch home | 0123456 | out of scope (impl.md 16.8 Linux only; TC-19s2) | |",
	}, "\n") + "\n"
	if w.String() != want {
		t.Errorf("rows:\n%s\nwant:\n%s", w.String(), want)
	}
}

// TestGuardedEvidenceLines: the record keeps each scenario's "evidence:" lines,
// named by the test that logged them, and nothing else of the test output.
func TestGuardedEvidenceLines(t *testing.T) {
	out := "=== RUN   TestDeletedSessionLeavesNothingBehind\r\n" +
		"    store_deletion_scenario_test.go:90: evidence: home /tmp/h, before the deletions: 2 files: a (1), b (2)\r\n" +
		"    store_deletion_scenario_test.go:91: kept the home at /tmp/h\n" +
		"=== RUN   TestTwoProcessesEditOneFile\n" +
		"    fs_two_process_scenario_test.go:214: evidence: file f.txt before x, after y, last acknowledged edit a0999\n" +
		"--- PASS: TestTwoProcessesEditOneFile (1.00s)\n"
	got := evidenceLines(out)
	want := []string{
		"TestDeletedSessionLeavesNothingBehind: home /tmp/h, before the deletions: 2 files: a (1), b (2)",
		"TestTwoProcessesEditOneFile: file f.txt before x, after y, last acknowledged edit a0999",
	}
	if !slices.Equal(got, want) {
		t.Errorf("evidence lines:\n%q\nwant:\n%q", got, want)
	}
}

// TestGuardedBuildLoadsReplay: the guarded build of dsh carries the testhook tag
// and loads a layer inserting the keyless replay provider (llm.replay): the
// committed authored recording unregistered-tool plays to a run result with no
// key.
func TestGuardedBuildLoadsReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds dsh")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "dsh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", guardedBuildArgs(bin)...)
	build.Dir = filepath.Join(root, "src", "harness")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", guardedBuildArgs(bin), err, out)
	}
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	tagged := false
	for _, s := range info.Settings {
		tagged = tagged || (s.Key == "-tags" && slices.Contains(strings.Split(s.Value, ","), "testhook"))
	}
	if !tagged {
		t.Fatalf("the guarded build lacks -tags=testhook: %v", info.Settings)
	}

	scenario := filepath.ToSlash(filepath.Join(root, "src", "harness", "test", "replay", "testdata", "unregistered-tool"))
	home, ws := filepath.Join(dir, "home"), filepath.Join(dir, "workspace")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	headless := func() (string, string, error) {
		cmd := exec.CommandContext(ctx, bin, "--home", home, "headless", "--json", "write a note")
		cmd.Dir = ws
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	// With no route the run is refused, but boot has created the home private to
	// its owner, which this module cannot do itself.
	if _, stderr, err := headless(); err == nil || !strings.Contains(stderr, "no model route") {
		t.Fatalf("with no layer: %v, want no model route:\n%s", err, stderr)
	}
	layerDir := filepath.Join(home, "config", "profiles", "headless")
	if err := os.MkdirAll(layerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	layer := "- insert:\n    - id: replay\n      name: llm.replay\n      config:\n" +
		"        scenario: \"" + scenario + "\"\n        mode: replay\n        route: \"replay\"\n        model: \"replay\"\n"
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte(layer), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := headless()
	if err != nil {
		t.Fatalf("the replayed run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, `"type":"run/result"`) || !strings.Contains(last, `"reason":"completed"`) {
		t.Fatalf("the last line is not a completed run result: %s\nstderr:\n%s", last, stderr)
	}
}

func TestGuardedUsage(t *testing.T) {
	for _, args := range [][]string{
		{"guarded", "--lane", "macos", "--evidence", "x.txt"},
		{"guarded", "--lane", "wsl"},
		{"guarded", "--lane", "wsl", "--stage", "S16", "--evidence", "x.txt"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stderr.Len() == 0 {
			t.Errorf("%q: exit %d, stderr %q; want 2 with a reason", args, code, stderr.String())
		}
	}
}
