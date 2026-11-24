package closure

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

const store1Index = `built-through: DH-S-8
stages:
  DH-S-16:
    - id: DH-E-STORE-1
      authored: DH-S-7
`

func index(t *testing.T, src string) *acceptance.Index {
	t.Helper()
	x, err := acceptance.ParseIndex(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	return x
}

func record(t *testing.T, src string) *Record {
	t.Helper()
	rec, err := ParseRecord("DH-S-16.md", strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	return rec
}

// stageRecord is a DH-S-16 record with the given status and scenario rows, and steps,
// manual checks and owner defaults complete.
func stageRecord(status string, rows ...string) string {
	return "# DH-S-16 evidence\n\nClosure status: " + status + `

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 16.1 | DH-D-BUNDLE-3 | 0123abc | |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
` + strings.Join(rows, "\n") + `

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 16.3 | one headless prompt | answered | tester | 0123abc | ` + "`manual`" + ` |

## Owner defaults (M2-C-4)

| Default | Raised in | Confirmed | By | Commit |
| --- | --- | --- | --- | --- |
| ` + "`dsh headless`" + ` progress | U-K2-4 | confirmed as is | owner | 0123abc |
`
}

func row(id, result string) string {
	return "| " + id + " | Linux (WSL) | guarded | required | wsl lane | 0123abc | " + result + " | run.txt |"
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// Scenario: Ready accepts an open record whose rows passed (implementation-plan
// Stage 1.1; e2e "Ready ignores the status line and checks every row").
func TestReadyAcceptsOpenRecordWhoseRowsPassed(t *testing.T) {
	x := index(t, store1Index)
	for _, built := range []bool{false, true} {
		if b := Ready(x, "DH-S-16", record(t, stageRecord("open", row("DH-E-STORE-1", "passed"))), built); len(b) != 0 {
			t.Errorf("built=%v: passed row: blockers %q, want none", built, b)
		}
		b := Ready(x, "DH-S-16", record(t, stageRecord("open", row("DH-E-STORE-1", "not run (bwrap preflight failed)"))), built)
		if len(b) != 1 || !containsAll(b[0], "DH-E-STORE-1", "not run") {
			t.Errorf("built=%v: not-run row: blockers %q, want one naming DH-E-STORE-1 and not run", built, b)
		}
	}
	// The status line is never read by ready: a missing or garbled line changes nothing.
	for _, status := range []string{"closed", "half-closed"} {
		if b := Ready(x, "DH-S-16", record(t, stageRecord(status, row("DH-E-STORE-1", "passed"))), false); len(b) != 0 {
			t.Errorf("status %q: blockers %q, want none", status, b)
		}
	}
}

// Scenario: Close needs ready plus the status transition.
func TestCloseNeedsReadyPlusTransition(t *testing.T) {
	x := index(t, store1Index)
	for _, tc := range []struct {
		name, status, result, want string
	}{
		{"open/not-ready", "open", "not run (no runner)", NotYetClosed},
		{"open/ready", "open", "passed", NotYetClosed},
		{"closed/ready", "closed", "passed", ""},
		{"closed-at-sha/ready", "closed at 0123abcd", "passed", ""},
		{"closed/not-ready", "closed", "not run (no runner)", ClosedButNotReady},
		{"garbled/ready", "done", "passed", StatusUnreadable},
	} {
		refusal, blockers := Close(x, "DH-S-16", record(t, stageRecord(tc.status, row("DH-E-STORE-1", tc.result))), false)
		if refusal != tc.want {
			t.Errorf("%s: refusal %q (%q), want %q", tc.name, refusal, blockers, tc.want)
		}
		if (refusal == "") != (len(blockers) == 0) {
			t.Errorf("%s: refusal %q with blockers %q", tc.name, refusal, blockers)
		}
	}
	// No status line at all is refused, never read as open or closed.
	if refusal, _ := Close(x, "DH-S-16", record(t, strings.Replace(stageRecord("open", row("DH-E-STORE-1", "passed")), "Closure status: open", "", 1)), false); refusal != StatusUnreadable {
		t.Errorf("no status line: refusal %q, want %q", refusal, StatusUnreadable)
	}
}

// Scenario: Skipped counts as not passed (M2-C-2, M2-DEC-3).
func TestSkippedIsNotPassed(t *testing.T) {
	x := index(t, store1Index)
	b := Ready(x, "DH-S-16", record(t, stageRecord("open", row("DH-E-STORE-1", "passed"), row("DH-E-FS-3", "skipped"))), false)
	if len(b) != 1 || !containsAll(b[0], "DH-E-FS-3", "skipped is not passed") {
		t.Errorf("blockers %q, want one naming DH-E-FS-3 with \"skipped is not passed\"", b)
	}
}

func TestResultsThatAreNotPassed(t *testing.T) {
	x := index(t, store1Index)
	for result, want := range map[string]string{
		"":                   "no result recorded",
		"not run":            "no reason",
		"failed (exit 1)":    "failed",
		"blocked (no fleet)": "blocked is not passed",
		"open":               "open never counts as passed",
		"green":              "unknown result",
		"out of scope":       "names no decision",
		"out of scope ()":    "names no decision",
		"Skipped (no bwrap)": "skipped is not passed",
		"passed but flaky":   "unknown result",
	} {
		b := Ready(x, "DH-S-16", record(t, stageRecord("open", row("DH-E-STORE-1", result))), false)
		if len(b) != 1 || !strings.Contains(b[0], want) {
			t.Errorf("result %q: blockers %q, want one containing %q", result, b, want)
		}
	}
}

// Scenario: Out of scope by decision is not the same as not run.
func TestOutOfScopeNeedsDecision(t *testing.T) {
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-18:\n    - id: DH-E-STORAGE-6\n      authored: DH-S-6\n")
	rows := func(macOS string) string {
		return stageRecord("open",
			"| DH-E-STORAGE-6 | Windows | acceptance | required | windows-latest | 0123abc | passed | run.txt |",
			"| DH-E-STORAGE-6 | Linux (WSL) | acceptance | required | wsl lane | 0123abc | passed | run.txt |",
			"| DH-E-STORAGE-6 | macOS | acceptance | required | | | "+macOS+" | |")
	}
	if b := Ready(x, "DH-S-18", record(t, rows("out of scope (platforms-windows-wsl)")), false); len(b) != 0 {
		t.Errorf("decided out of scope: blockers %q, want none", b)
	}
	b := Ready(x, "DH-S-18", record(t, rows("out of scope")), false)
	if len(b) != 1 || !containsAll(b[0], "DH-E-STORAGE-6 [macOS]", "names no decision") {
		t.Errorf("undecided out of scope: blockers %q, want one naming the macOS row", b)
	}
}

func TestIndexedScenarioWithoutRowBlocks(t *testing.T) {
	x := index(t, store1Index+"    - id: DH-E-STORE-2\n      authored: DH-S-7\n")
	for _, built := range []bool{false, true} {
		b := Ready(x, "DH-S-16", record(t, stageRecord("open", row("DH-E-STORE-1", "passed"))), built)
		if len(b) != 1 || !containsAll(b[0], "DH-E-STORE-2", "no row") {
			t.Errorf("built=%v: blockers %q, want one naming DH-E-STORE-2", built, b)
		}
	}
}

func TestAcceptanceCloseNeedsIndexEntryAndRows(t *testing.T) {
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-17: []\n")
	b := Ready(x, "DH-S-17", record(t, stageRecord("open")), false)
	if len(b) != 1 || !strings.Contains(b[0], "no scenario row") {
		t.Errorf("no rows: blockers %q", b)
	}
	b = Ready(x, "DH-S-16", record(t, stageRecord("open", row("DH-E-STORE-1", "passed"))), false)
	if len(b) != 1 || !strings.Contains(b[0], "no entry in test/harness/acceptance-index.yaml") {
		t.Errorf("unindexed stage: blockers %q", b)
	}
	// A build stage without an index entry closes with --built.
	if b := Ready(x, "DH-S-16", record(t, stageRecord("open")), true); len(b) != 0 {
		t.Errorf("--built without index entry: blockers %q, want none", b)
	}
}

// A recorded leg (M2-DEC-4) must run; its failure is kept and is not a blocker.
func TestRecordedLeg(t *testing.T) {
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-17:\n    - id: DH-E-LLM-1\n      authored: DH-S-9\n")
	rec := func(copilot string) *Record {
		return record(t, stageRecord("open",
			"| DH-E-LLM-1 | claude-code | acceptance | required | E1 | 0123abc | passed | export.json |",
			"| DH-E-LLM-1 | copilot-cli | acceptance | recorded | E1 | 0123abc | "+copilot+" | export.json |"))
	}
	if b := Ready(x, "DH-S-17", rec("failed (route refused)"), false); len(b) != 0 {
		t.Errorf("recorded failure: blockers %q, want none", b)
	}
	if b := Ready(x, "DH-S-17", rec("not run (not signed in)"), false); len(b) != 1 || !containsAll(b[0], "copilot-cli", "recorded leg", "not run") {
		t.Errorf("recorded not run: blockers %q", b)
	}
}

// --built counts only the guarded rows: acceptance rows of a build stage run later.
func TestBuiltCountsGuardedRowsOnly(t *testing.T) {
	x := index(t, store1Index)
	src := stageRecord("open", row("DH-E-STORE-1", "passed"),
		"| DH-E-FS-1 | Windows | acceptance | required | | | not run (runs in DH-S-18) | |")
	if b := Ready(x, "DH-S-16", record(t, src), true); len(b) != 0 {
		t.Errorf("--built: blockers %q, want none", b)
	}
	if b := Ready(x, "DH-S-16", record(t, src), false); len(b) != 1 || !strings.Contains(b[0], "DH-E-FS-1") {
		t.Errorf("acceptance: blockers %q, want one naming DH-E-FS-1", b)
	}
}

// Scenario: Built-mode ready requires a commit per step and every owner default.
func TestBuiltNeedsStepsManualChecksAndOwnerDefaults(t *testing.T) {
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-16: []\n")
	src := `# DH-S-12 evidence

Closure status: open

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 12.1 | DH-D-VIEW-10 | 0123abc | |
| 12.2 | DH-D-FS-4 | | |
| 12.3 | DH-D-SANDBOX-1 | HEAD | |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 12.2 | symlink write denied | denied | | 0123abc | manual |
| 12.2 | fence held during swap | held | tester | 0123abc | auto |

## Owner defaults (M2-C-4)

| Default | Raised in | Confirmed | By | Commit |
| --- | --- | --- | --- | --- |
| Full-access label | I2 Q3 | confirmed as is | owner | 0123abc |
| UTF-8-only text tools | core §6.3 | | | |
`
	b := Ready(x, "DH-S-12", record(t, src), true)
	want := [][]string{
		{"step 12.2 (DH-D-FS-4)", "no commit SHA"},
		{"step 12.3 (DH-D-SANDBOX-1)", `"HEAD" is not a commit SHA`},
		{"symlink write denied", "no operator"},
		{"fence held during swap", `label "auto"`},
		{"UTF-8-only text tools", "M2-C-4"},
	}
	if len(b) != len(want) {
		t.Fatalf("blockers %q, want %d", b, len(want))
	}
	for i, w := range want {
		if !containsAll(b[i], w...) {
			t.Errorf("blocker %d = %q, want %q", i, b[i], w)
		}
	}
	// Without --built the build-stage tables are not read.
	if b := Ready(x, "DH-S-16", record(t, strings.Replace(src, "| --- | --- | --- | --- | --- | --- | --- | --- |", "| --- | --- | --- | --- | --- | --- | --- | --- |\n"+row("DH-E-STORE-1", "passed"), 1)), false); len(b) != 0 {
		t.Errorf("acceptance mode read the build tables: %q", b)
	}
}

func TestBuiltNeedsTheSections(t *testing.T) {
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-16: []\n")
	b := Ready(x, "DH-S-12", record(t, "# DH-S-12 evidence\n\nClosure status: open\n"), true)
	for _, h := range []string{StepsHeading, ManualChecksHeading, OwnerDefaultsHeading} {
		if !slices.ContainsFunc(b, func(s string) bool { return strings.Contains(s, h) }) {
			t.Errorf("blockers %q name no missing %q", b, h)
		}
	}
}

func TestParseRecord(t *testing.T) {
	rec := record(t, "# R\n\nClosure status: open\n\n```\nClosure status: closed\n## Steps\n```\n\n## Scenarios\n\n"+
		"| Scenario | Result | Note |\n| --- | --- | --- |\n| `DH-E-X-1` | passed | a \\| b |\n| DH-E-X-2 | failed |\n\nprose\n| DH-E-X-3 | passed | not read |\n")
	if rec.Status != StatusOpen || rec.StatusLine != 3 {
		t.Errorf("status %v at line %d; a fenced status line must not count", rec.Status, rec.StatusLine)
	}
	if rec.Steps.Present {
		t.Error("a fenced heading opened the Steps section")
	}
	if len(rec.Scenarios.Rows) != 2 {
		t.Fatalf("rows %+v, want 2 (the table ends at the prose)", rec.Scenarios.Rows)
	}
	if r := rec.Scenarios.Rows[0]; r.Get("scenario") != "DH-E-X-1" || r.Get("NOTE") != "a | b" {
		t.Errorf("row 0 = %+v", r.Cells)
	}
	if rec := record(t, "Closure status: open\nClosure status: closed\n"); rec.Status != StatusInvalid {
		t.Errorf("two status lines: %v, want invalid", rec.Status)
	}
	if _, err := ParseRecord("x.md", strings.NewReader("## Steps\n| A |\n| --- |\n| 1 | 2 |\n")); err == nil {
		t.Error("a row wider than its header parsed")
	}
	if _, err := ParseRecord("x.md", strings.NewReader("## Steps\n## Steps\n")); err == nil {
		t.Error("a section given twice parsed")
	}
}

// Indexed guarded scenarios cannot leave judging by a venue edit, and an unknown venue
// is refused in both modes.
func TestVenueCannotBypassJudging(t *testing.T) {
	x := index(t, store1Index)
	moved := "| DH-E-STORE-1 | Linux (WSL) | acceptance | required | wsl lane | 0123abc | failed (exit 1) | run.txt |"
	b := Ready(x, "DH-S-16", record(t, stageRecord("open", moved)), true)
	if len(b) != 1 || !containsAll(b[0], "DH-E-STORE-1", "runs guarded", `"acceptance"`) {
		t.Errorf("indexed row moved to acceptance: blockers %q, want one naming the venue", b)
	}
	typo := "| DH-E-STORE-1 | Linux (WSL) | guardd | required | wsl lane | 0123abc | not run (x) | run.txt |"
	for _, built := range []bool{false, true} {
		b := Ready(x, "DH-S-16", record(t, stageRecord("open", typo)), built)
		if !slices.ContainsFunc(b, func(s string) bool { return containsAll(s, `venue "guardd"`) }) {
			t.Errorf("built=%v: misspelt venue: blockers %q", built, b)
		}
	}
}

// A row that ran needs its environment, commit and artifacts (M2-DEC-5); a named
// out-of-scope row does not.
func TestExecutedRowsNeedEvidence(t *testing.T) {
	x := index(t, store1Index)
	bare := "| DH-E-STORE-1 | Linux (WSL) | guarded | required | | | passed | |"
	b := Ready(x, "DH-S-16", record(t, stageRecord("open", bare)), false)
	want := []string{"no environment", "no commit", "no artifacts"}
	if len(b) != len(want) {
		t.Fatalf("passed row without evidence: blockers %q, want %d", b, len(want))
	}
	for i, w := range want {
		if !containsAll(b[i], "DH-E-STORE-1", w) {
			t.Errorf("blocker %d = %q, want %q", i, b[i], w)
		}
	}
	badSHA := "| DH-E-STORE-1 | Linux (WSL) | guarded | required | wsl lane | HEAD | passed | run.txt |"
	if b := Ready(x, "DH-S-16", record(t, stageRecord("open", badSHA)), false); len(b) != 1 || !strings.Contains(b[0], `"HEAD" is not a commit SHA`) {
		t.Errorf("passed row at HEAD: blockers %q", b)
	}
	x17 := index(t, "built-through: DH-S-8\nstages:\n  DH-S-17:\n    - id: DH-E-LLM-1\n      authored: DH-S-9\n")
	rec := record(t, stageRecord("open",
		"| DH-E-LLM-1 | claude-code | acceptance | required | E1 | 0123abc | passed | export.json |",
		"| DH-E-LLM-1 | copilot-cli | acceptance | recorded | E1 | 0123abc | failed (route refused) | |"))
	if b := Ready(x17, "DH-S-17", rec, false); len(b) != 1 || !containsAll(b[0], "copilot-cli", "no artifacts") {
		t.Errorf("recorded failure without artifacts: blockers %q", b)
	}
}

// Deleting an owed row is a blocker: the stage's obligations are fixed when Stage 1.1
// creates the record, so a deleted owner default cannot remove its own blocker.
func TestStoryReadyRefusesDeletedObligations(t *testing.T) {
	s := Stories[DefaultStory]
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-16:\n    - id: DH-E-STORE-1\n      authored: DH-S-7\n")
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "records", "created", "docs", "stories", "harness-DSH-M2", "evidence", "DH-S-16.md"))
	if err != nil {
		t.Fatal(err)
	}
	full := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parse := func(src string) *Record { return record(t, src) }
	if m := s.Obligations["DH-S-16"].Missing(parse(full), true); len(m) != 0 {
		t.Fatalf("the created record misses owed rows: %q", m)
	}
	var kept []string
	for _, line := range strings.Split(full, "\n") {
		switch {
		case strings.HasPrefix(line, "| 16.10 |"),
			strings.HasPrefix(line, "| 16.4 | Ctrl-C"),
			strings.HasPrefix(line, "| Windows double-click |"),
			strings.HasPrefix(line, "| DH-E-STORE-2 |"):
			continue
		case strings.HasPrefix(line, "| DH-E-FS-3 | Windows |"):
			line = strings.Replace(line, "| guarded |", "| later |", 1)
		}
		kept = append(kept, line)
	}
	b := s.Ready(x, "DH-S-16", parse(strings.Join(kept, "\n")), true)
	for _, w := range [][]string{
		{"step 16.10 has no row"},
		{"step 16.4: manual check \"Ctrl-C during a tool call\" has no row"},
		{`owner default "Windows double-click" has no row`, "M2-C-4"},
		{"DH-E-STORE-2 [Linux (WSL)]", "has no row"},
		{"DH-E-FS-3 [Windows] (venue guarded", "has no row"},
	} {
		if !slices.ContainsFunc(b, func(s string) bool { return containsAll(s, w...) }) {
			t.Errorf("blockers name no %q:\n%s", w, strings.Join(b, "\n"))
		}
	}
	// Without --built only the scenario rows are owed.
	if m := s.Obligations["DH-S-16"].Missing(parse(strings.Join(kept, "\n")), false); len(m) != 2 {
		t.Errorf("acceptance-mode obligations %q, want the two scenario rows", m)
	}
}

// A data row in the separator's place is refused, never consumed: a failed row
// followed by a passed one cannot be reduced to the passed one.
func TestParseRecordRefusesMissingSeparator(t *testing.T) {
	for _, src := range []string{
		"## Scenarios\n| Scenario | Result |\n| DH-E-STORE-1 | failed |\n| DH-E-STORE-1 | passed |\n",
		"## Scenarios\n| Scenario | Result |\n| --- |\n| DH-E-STORE-1 | passed |\n",
		"## Scenarios\n| Scenario | Result |\n| --- | - |\n",
	} {
		if _, err := ParseRecord("x.md", strings.NewReader(src)); err == nil || !strings.Contains(err.Error(), "separator") {
			t.Errorf("%q: err %v, want a separator error", src, err)
		}
	}
	if _, err := ParseRecord("x.md", strings.NewReader("## Scenarios\n| Scenario | Result |\n| :--- | ---: |\n")); err != nil {
		t.Errorf("aligned separator: %v", err)
	}
}

func readFixture(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", "testdata"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

var evidencePath = []string{"docs", "stories", "harness-DSH-M2", "evidence"}

// A build stage is judged with --built whatever the caller asks: DH-S-16 with every
// scenario row passed but a step without its commit stays not ready without --built.
func TestStoryReadyJudgesBuildStageInItsMode(t *testing.T) {
	s := Stories[DefaultStory]
	x := index(t, "built-through: DH-S-8\nstages:\n  DH-S-16:\n    - id: DH-E-STORE-1\n      authored: DH-S-7\n    - id: DH-E-STORE-6\n      authored: DH-S-7\n")
	src := readFixture(t, append([]string{"closure", "closed_ready"}, append(evidencePath, "DH-S-16.md")...)...)
	if b := s.Ready(x, "DH-S-16", record(t, src), true); len(b) != 0 {
		t.Fatalf("closed_ready with --built: %q", b)
	}
	broken := strings.Replace(src, "| 16.1 | DH-D-BUNDLE-3 | 0a1b2c3 |", "| 16.1 | DH-D-BUNDLE-3 | |", 1)
	if broken == src {
		t.Fatal("fixture has no step 16.1 row")
	}
	b := s.Ready(x, "DH-S-16", record(t, broken), false)
	for _, w := range [][]string{{"DH-S-16 is a build stage", "--built"}, {"step 16.1", "no commit SHA"}} {
		if !slices.ContainsFunc(b, func(s string) bool { return containsAll(s, w...) }) {
			t.Errorf("blockers name no %q:\n%s", w, strings.Join(b, "\n"))
		}
	}
	if b := s.Ready(x, "DH-S-17", record(t, src), true); !slices.ContainsFunc(b, func(s string) bool { return containsAll(s, "DH-S-17 is an acceptance stage") }) {
		t.Errorf("an acceptance stage with --built: blockers %q name no mode mismatch", b)
	}
}

// Manual checks are owed by identity: a copy of one check of step 12.2 does not stand
// in for the other.
func TestManualCheckCannotBeSubstituted(t *testing.T) {
	src := readFixture(t, append([]string{"records", "created"}, append(evidencePath, "DH-S-12.md")...)...)
	const first = "| 12.2 | the write tool asked for a path outside the roots through a symbolic link: the denial (scratch directory) |"
	const swap = "| 12.2 | a directory swapped for a link during a write: the fence held (scratch directory) |"
	if !strings.Contains(src, first) || !strings.Contains(src, swap) {
		t.Fatal("fixture DH-S-12 lacks its two 12.2 checks")
	}
	m := Stories[DefaultStory].Obligations["DH-S-12"].Missing(record(t, strings.Replace(src, swap, first, 1)), true)
	for _, w := range [][]string{{"a directory swapped for a link", "has no row"}, {"symbolic link", "listed twice"}} {
		if !slices.ContainsFunc(m, func(s string) bool { return containsAll(s, w...) }) {
			t.Errorf("missing %q names no %q", m, w)
		}
	}
}

// The closure record carries every deliverable of tech-spec sec. 3.2 by ID: replacing
// one with an unknown ID of the same priority and state is refused.
func TestClosureRecordNeedsEveryDeliverableID(t *testing.T) {
	s := Stories[DefaultStory]
	src := readFixture(t, append([]string{"records", "created"}, append(evidencePath, "closure.md")...)...)
	parse := func(src string) *Record {
		rec, err := ParseDocument("closure.md", strings.NewReader(src), DeliverablesHeading, HeldOpenHeading)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	if p := ClosureProblems(s, parse(src)); len(p) != 0 {
		t.Fatalf("created closure record: %q", p)
	}
	broken := strings.Replace(src, "| DH-D-FS-4 |", "| DH-D-FS-999 |", 1)
	if broken == src {
		t.Fatal("fixture closure.md has no DH-D-FS-4 row")
	}
	p := ClosureProblems(s, parse(broken))
	for _, w := range []string{"DH-D-FS-999 is not a deliverable", "deliverable DH-D-FS-4 has no row"} {
		if !slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("problems %q name no %q", p, w)
		}
	}
	if p := ClosureProblems(s, parse(strings.Replace(src, "| DH-D-SDK-8 | P1 |", "| DH-D-SDK-8 | P0 |", 1))); !slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, `DH-D-SDK-8: priority "P0"`) }) {
		t.Errorf("a changed priority: problems %q", p)
	}
}
