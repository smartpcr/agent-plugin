package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// Scenario "Entry subcommand over immutable fixtures" (implementation-plan Stage 1.2):
// m1_open exits 1 with the committed golden as its output, m1_closed exits 0.
func TestEntryGolden(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "entry", "m1_open"))
	code, stdout, stderr := runClosureGate(t, "entry", "--story", "harness:DSH-M2", "--root", root)
	if code != 1 || stderr != "" {
		t.Errorf("m1_open: exit %d, want 1; stderr:\n%s", code, stderr)
	}
	golden := filepath.Join("testdata", "entry", "m1_open.golden")
	if *updateGolden {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI: regenerate the golden locally and review the diff")
		}
		if err := os.WriteFile(golden, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update locally to create it): %v", err)
	}
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != stdout {
		t.Errorf("entry output differs from %s\n--- got\n%s--- want\n%s", golden, stdout, norm)
	}
	for _, s := range closure.Entries["harness:DSH-M2"].PriorStages {
		if !strings.Contains(stdout, "  - "+s+": ") {
			t.Errorf("m1_open: output does not name %s", s)
		}
	}
	if !strings.Contains(stdout, "reads DH-S-8; want DH-S-11") {
		t.Errorf("m1_open: output does not name built-through DH-S-8")
	}

	root = closureTree(t, filepath.Join("testdata", "entry", "m1_closed"))
	code, stdout, stderr = runClosureGate(t, "entry", "--story", "harness:DSH-M2", "--root", root)
	if code != 0 || !strings.Contains(stdout, "harness:DSH-M1 is closed") {
		t.Errorf("m1_closed: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestEntryUsage(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "entry", "m1_closed"))
	if code, _, stderr := runClosureGate(t, "entry", "--root", root, "--story", "harness:DSH-M1"); code != 2 || !strings.Contains(stderr, "no entry condition") {
		t.Errorf("a story without an entry condition exits %d, want 2; stderr:\n%s", code, stderr)
	}
	if code, _, _ := runClosureGate(t, "entry", "--root", root, "extra"); code != 2 {
		t.Errorf("an extra argument exits %d, want 2", code)
	}
	broken := closureTree(t, filepath.Join("testdata", "entry", "m1_closed"))
	if err := os.WriteFile(filepath.Join(broken, "test", "harness", "acceptance-index.yaml"), []byte("built-through: S11\nstages:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runClosureGate(t, "entry", "--root", broken); code != 2 || !strings.Contains(stderr, "acceptance-index.yaml") {
		t.Errorf("an unreadable index exits %d, want 2 naming it; stderr:\n%s", code, stderr)
	}
}

// A root with no M1 records, no closure record and no acceptance index names all
// thirteen missing conditions and exits 1; the missing index does not hide the rest.
func TestEntryNamesEveryMissingInput(t *testing.T) {
	code, stdout, stderr := runClosureGate(t, "entry", "--story", "harness:DSH-M2", "--root", t.TempDir())
	if code != 1 || stderr != "" {
		t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr)
	}
	lines := conditionLines(stdout)
	if len(lines) != 13 {
		t.Fatalf("%d conditions, want 13 (eleven records, the closure record, the index):\n%s", len(lines), stdout)
	}
	for i, s := range closure.Entries["harness:DSH-M2"].PriorStages {
		if want := s + ": no evidence record "; !strings.HasPrefix(lines[i], want) {
			t.Errorf("condition %d = %q, want prefix %q", i, lines[i], want)
		}
	}
	if !strings.HasPrefix(lines[11], "no harness:DSH-M1 closure record ") {
		t.Errorf("condition 11 = %q, want the missing closure record", lines[11])
	}
	if !strings.HasPrefix(lines[12], "no acceptance index test/harness/acceptance-index.yaml") {
		t.Errorf("condition 12 = %q, want the missing index", lines[12])
	}
	assertEntryAgrees(t, t.TempDir())
}

// Scenario "Live entry result agrees with live records" (implementation-plan Stage
// 1.2): whatever the state of the committed M1 records, closure record and index,
// entry passes if and only if all eleven read closed, the closure record lists every
// deliverable done or built with a reason and built-through is at least DH-S-11, and
// otherwise names exactly the unmet conditions. The oracle (entryOracle) reads those
// files with its own parsing; the command's output is only compared against it.
func TestEntryLiveAgreesWithRecords(t *testing.T) {
	code, stdout := assertEntryAgrees(t, filepath.Join("..", ".."))
	t.Logf("live entry (exit %d):\n%s", code, stdout)
}

// The oracle itself is exercised on states the live repository is not in yet: the
// committed fixtures and a closed tree with a damaged closure record and index.
func TestEntryOracleOverOtherStates(t *testing.T) {
	for _, tree := range []string{"m1_open", "m1_closed"} {
		t.Run(tree, func(t *testing.T) { assertEntryAgrees(t, closureTree(t, filepath.Join("testdata", "entry", tree))) })
	}
	t.Run("damaged", func(t *testing.T) {
		root := closureTree(t, filepath.Join("testdata", "entry", "m1_closed"))
		ev := filepath.Join(root, filepath.FromSlash(closure.EvidenceDir("harness:DSH-M1")))
		src, err := os.ReadFile(filepath.Join(ev, "closure.md"))
		if err != nil {
			t.Fatal(err)
		}
		doc := strings.Replace(string(src), "| DH-D-LOADER-1 | built | done when its DH-E scenario passes in DH-S-17 or DH-S-18 |", "| DH-D-LOADER-1 | built | |", 1)
		doc = strings.Replace(doc, "| DH-D-AGENT-2 | built |", "| DH-D-AGENT-2 | open |", 1)
		doc = strings.Replace(doc, "| DH-D-CONTEXT-1 | built | done when its DH-E scenario passes in DH-S-17 or DH-S-18 |\n", "| DH-D-VIEW-10 | done | |\n", 1)
		if doc == string(src) {
			t.Fatal("the damage was not applied")
		}
		if err := os.WriteFile(filepath.Join(ev, "closure.md"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(ev, "DH-S-5.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "test", "harness", "acceptance-index.yaml")); err != nil {
			t.Fatal(err)
		}
		code, stdout := assertEntryAgrees(t, root)
		if code != 1 || len(conditionLines(stdout)) != 6 {
			t.Errorf("exit %d with %d conditions, want 1 with 6 (DH-S-5, four closure rows, the index):\n%s", code, len(conditionLines(stdout)), stdout)
		}
	})
}

// expectedEntry is what the oracle derives from the files under a root.
type expectedEntry struct {
	notClosed      []string // stages whose record is missing or does not read closed
	closureMissing bool
	closureIssues  []string // one token per closure-record problem, found in its line
	indexMissing   bool
	builtThroughLo bool
}

func (x expectedEntry) met() bool {
	return len(x.notClosed) == 0 && !x.closureMissing && len(x.closureIssues) == 0 && !x.indexMissing && !x.builtThroughLo
}

// assertEntryAgrees runs entry on root and checks its exit code and every condition
// line against entryOracle.
func assertEntryAgrees(t *testing.T, root string) (int, string) {
	t.Helper()
	want := entryOracle(t, root)
	code, stdout, stderr := runClosureGate(t, "entry", "--root", root)
	if code == 2 {
		t.Fatalf("entry exits 2: %s", stderr)
	}
	if (code == 0) != want.met() {
		t.Errorf("entry exits %d but the oracle finds the conditions met=%v\nstdout:\n%s", code, want.met(), stdout)
	}
	prior := closure.Entries["harness:DSH-M2"].Prior
	closurePath := closure.ClosureRecordPath(prior)
	var stages, closureLines []string
	var closureMissing, indexMissing, low bool
	for _, l := range conditionLines(stdout) {
		switch {
		case strings.HasPrefix(l, "DH-S-"):
			stages = append(stages, l[:strings.Index(l, ":")])
		case strings.HasPrefix(l, "no "+prior+" closure record "):
			closureMissing = true
		case strings.HasPrefix(l, closurePath+": "):
			closureLines = append(closureLines, l)
		case strings.HasPrefix(l, "no acceptance index "):
			indexMissing = true
		case strings.HasPrefix(l, "built-through in "):
			low = true
		default:
			t.Errorf("unexpected condition %q", l)
		}
	}
	if strings.Join(stages, ",") != strings.Join(want.notClosed, ",") {
		t.Errorf("records named %v, the oracle finds not closed %v", stages, want.notClosed)
	}
	if closureMissing != want.closureMissing || indexMissing != want.indexMissing || low != want.builtThroughLo {
		t.Errorf("named closure-missing=%v index-missing=%v built-through-low=%v; oracle %v %v %v",
			closureMissing, indexMissing, low, want.closureMissing, want.indexMissing, want.builtThroughLo)
	}
	if len(closureLines) != len(want.closureIssues) {
		t.Errorf("%d closure-record conditions, the oracle finds %d %v:\n%s", len(closureLines), len(want.closureIssues), want.closureIssues, strings.Join(closureLines, "\n"))
	}
	for _, tok := range want.closureIssues {
		if !slices.ContainsFunc(closureLines, func(l string) bool { return strings.Contains(l, tok) }) {
			t.Errorf("no closure-record condition names %s", tok)
		}
	}
	return code, stdout
}

func conditionLines(stdout string) []string {
	var out []string
	for _, l := range strings.Split(stdout, "\n") {
		if v, ok := strings.CutPrefix(l, "  - "); ok {
			out = append(out, v)
		}
	}
	return out
}

// entryOracle reads the M1 records, closure record and index under root without the
// closure or acceptance packages: the first "Closure status:" line outside a code
// fence, the "## Deliverables" table, and the top-level built-through line.
func entryOracle(t *testing.T, root string) expectedEntry {
	t.Helper()
	e := closure.Entries["harness:DSH-M2"]
	var x expectedEntry
	ev := filepath.Join(root, "docs", "stories", "harness-DSH-M1", "evidence")
	for _, s := range e.PriorStages {
		if !liveStatusClosed(t, filepath.Join(ev, s+".md")) {
			x.notClosed = append(x.notClosed, s)
		}
	}
	if src, err := os.ReadFile(filepath.Join(ev, "closure.md")); err != nil {
		x.closureMissing = true
	} else {
		x.closureIssues = oracleClosureIssues(string(src), e.Deliverables)
	}
	src, err := os.ReadFile(filepath.Join(root, "test", "harness", "acceptance-index.yaml"))
	if err != nil {
		x.indexMissing = true
		return x
	}
	bt := -1
	for _, line := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		if v, ok := strings.CutPrefix(line, "built-through: DH-S-"); ok {
			n, err := strconv.Atoi(strings.Fields(v)[0])
			if err != nil {
				t.Fatalf("built-through %q: %v", line, err)
			}
			bt = n
		}
	}
	if bt < 0 {
		t.Fatal("no built-through line")
	}
	x.builtThroughLo = bt < 11
	return x
}

var oracleDeliverableID = regexp.MustCompile(`^DH-D-[A-Z][A-Z0-9]*-[1-9][0-9]*$`)

// oracleClosureIssues returns one token per problem of the "## Deliverables" table:
// the table or a column missing, or a deliverable ID whose row is malformed, repeated,
// unknown, in a state other than done or built, built without a reason, or absent.
func oracleClosureIssues(src string, deliverables []string) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	start := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == "## Deliverables" })
	if start < 0 {
		return []string{`"## Deliverables"`}
	}
	cells := func(l string) []string {
		parts := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		for i, p := range parts {
			parts[i] = strings.Trim(strings.TrimSpace(p), "`")
		}
		return parts
	}
	var header []string
	var rows [][]string
	for _, l := range lines[start+1:] {
		tl := strings.TrimSpace(l)
		if strings.HasPrefix(tl, "#") {
			break
		}
		if !strings.HasPrefix(tl, "|") {
			if header != nil {
				break
			}
			continue
		}
		switch {
		case header == nil:
			header = cells(tl)
			for i := range header {
				header[i] = strings.ToLower(header[i])
			}
		case strings.HasPrefix(strings.ReplaceAll(tl, " ", ""), "|---"):
		default:
			rows = append(rows, cells(tl))
		}
	}
	var issues []string
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, c := range []string{"deliverable", "state", "reason"} {
		if _, ok := col[c]; !ok {
			issues = append(issues, `"`+c+`"`)
		}
	}
	get := func(r []string, c string) string {
		if i, ok := col[c]; ok && i < len(r) {
			return r[i]
		}
		return ""
	}
	seen := map[string]bool{}
	for _, r := range rows {
		id := get(r, "deliverable")
		if !oracleDeliverableID.MatchString(id) {
			issues = append(issues, `"`+id+`"`)
			continue
		}
		if seen[id] || !slices.Contains(deliverables, id) {
			issues = append(issues, id)
		}
		seen[id] = true
		switch state := get(r, "state"); {
		case state != "done" && state != "built":
			issues = append(issues, id)
		case state == "built" && get(r, "reason") == "":
			issues = append(issues, id)
		}
	}
	for _, id := range deliverables {
		if !seen[id] {
			issues = append(issues, id)
		}
	}
	return issues
}

// liveStatusClosed reads the first line of path that starts "Closure status:" outside
// a code fence; a missing file is not closed.
func liveStatusClosed(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	inFence := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \r")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if v, ok := strings.CutPrefix(line, "Closure status:"); ok && !inFence {
			v = strings.TrimSpace(v)
			return v == "closed" || regexp.MustCompile(`^closed at [0-9a-f]{7,40}$`).MatchString(v)
		}
	}
	return false
}
