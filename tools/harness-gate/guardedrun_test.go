package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
