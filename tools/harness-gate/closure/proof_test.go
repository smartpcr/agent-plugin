package closure

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// proofStory is a one-stage story whose DH-S-16 record, as stageRecord writes it, is
// built (no obligations beyond the package Ready).
var proofStory = Story{ID: "test:proof", Stages: []string{"DH-S-16"}, BuildStages: []string{"DH-S-16"}}

func closureTable(t *testing.T, rows string, held string) *Record {
	t.Helper()
	src := "# closure\n\n## Deliverables\n\n" +
		"| Deliverable | Priority | Built in | State | Finishing stage or held-open item | Proving scenarios |\n" +
		"| --- | --- | --- | --- | --- | --- |\n" + rows + "\n\n## Held-open items\n\n" +
		"| Item | Deliverable | What waits | State | Closes at |\n| --- | --- | --- | --- | --- |\n" + held + "\n"
	rec, err := ParseDocument("closure.md", strings.NewReader(src), DeliverablesHeading, HeldOpenHeading)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// Scenario: Done needs every proof -- an in-memory closure table marking CLI-2 done
// while DH-E-CLI-1 is unrun is refused naming CLI-2 and DH-E-CLI-1 (M2-C-3).
func TestDoneNeedsEveryProof(t *testing.T) {
	x := index(t, store1Index)
	recs := map[string]*Record{"DH-S-16": record(t, stageRecord("closed", row("DH-E-STORE-1", "passed")))}
	c := closureTable(t, "| DH-D-CLI-2 | P0 | DH-S-16 | done | DH-S-16 (local tests) | DH-E-CLI-1, DH-E-STORE-1 |", "")
	p := ProofProblems(proofStory, x, c, recs)
	if len(p) != 1 || !containsAll(p[0], "DH-D-CLI-2", "DH-E-CLI-1", "unrun", "M2-C-3") {
		t.Fatalf("problems %q, want one naming DH-D-CLI-2 and DH-E-CLI-1 unrun", p)
	}
	// A row that did not run is no proof either, whatever the stage record's status.
	recs["DH-S-16"] = record(t, stageRecord("closed", row("DH-E-STORE-1", "passed"), "| DH-E-CLI-1 | Linux (WSL) | later | required | | | not run (S27) | |"))
	if p := ProofProblems(proofStory, x, c, recs); len(p) != 1 || !containsAll(p[0], "DH-D-CLI-2", "DH-E-CLI-1") {
		t.Errorf("later row only: problems %q", p)
	}
	recs["DH-S-16"] = record(t, stageRecord("closed", row("DH-E-STORE-1", "passed"), row("DH-E-CLI-1", "not run (no runner)")))
	if p := ProofProblems(proofStory, x, c, recs); !slices.ContainsFunc(p, func(s string) bool { return containsAll(s, "DH-D-CLI-2", "DH-E-CLI-1", "not run") }) {
		t.Errorf("not-run row: problems %q", p)
	}
}

// Done holds only with every proof; removing any one of them is refused naming it.
func TestDoneHoldsOnlyWithEveryProof(t *testing.T) {
	x := index(t, store1Index)
	const done = "| DH-D-CLI-9 | P0 | DH-S-16 | done | DH-S-16 (local tests); H-21 | DH-E-STORE-1 |"
	const h21Closed = "| H-21 | DH-D-BOOT-6 | the boot preview | closed | DH-S-16 |"
	closed := func() map[string]*Record {
		return map[string]*Record{"DH-S-16": record(t, stageRecord("closed", row("DH-E-STORE-1", "passed")))}
	}
	if p := ProofProblems(proofStory, x, closureTable(t, done, h21Closed), closed()); len(p) != 0 {
		t.Fatalf("every proof present: problems %q", p)
	}
	for _, tc := range []struct {
		name string
		recs map[string]*Record
		held string
		want []string
	}{
		{"stage open", map[string]*Record{"DH-S-16": record(t, stageRecord("open", row("DH-E-STORE-1", "passed")))}, h21Closed, []string{"DH-D-CLI-9", "DH-S-16 is not closed", NotYetClosed}},
		{"scenario failed", map[string]*Record{"DH-S-16": record(t, stageRecord("closed", row("DH-E-STORE-1", "failed")))}, h21Closed, []string{"DH-D-CLI-9", "DH-E-STORE-1", "failed"}},
		{"skipped", map[string]*Record{"DH-S-16": record(t, stageRecord("closed", row("DH-E-STORE-1", "passed"), row("DH-E-STORE-1", "skipped")))}, h21Closed, []string{"DH-E-STORE-1", "skipped"}},
		{"no evidence", map[string]*Record{"DH-S-16": record(t, stageRecord("closed", "| DH-E-STORE-1 | Linux (WSL) | guarded | required | wsl lane | 0123abc | passed | |"))}, h21Closed, []string{"DH-E-STORE-1", "artifacts"}},
		{"held open", closed(), "| H-21 | DH-D-BOOT-6 | the boot preview | open | DH-S-16 |", []string{"DH-D-CLI-9", "H-21", "not closed"}},
		{"held row missing", closed(), "", []string{"DH-D-CLI-9", "H-21", "has no row"}},
		{"no record", map[string]*Record{}, h21Closed, []string{"DH-D-CLI-9", "no evidence record"}},
	} {
		p := ProofProblems(proofStory, x, closureTable(t, done, tc.held), tc.recs)
		if !slices.ContainsFunc(p, func(s string) bool { return containsAll(s, tc.want...) }) {
			t.Errorf("%s: problems %q name no %q", tc.name, p, tc.want)
		}
	}
}

// Built needs the Built-in stage to pass ready --built; not built claims nothing.
func TestBuiltNeedsReadyBuilt(t *testing.T) {
	x := index(t, store1Index)
	const built = "| DH-D-BUNDLE-3 | P0 | DH-S-16 | built | DH-S-22 | DH-E-BUNDLE-1 |"
	open := map[string]*Record{"DH-S-16": record(t, stageRecord("open", row("DH-E-STORE-1", "passed")))}
	if p := ProofProblems(proofStory, x, closureTable(t, built, ""), open); len(p) != 0 {
		t.Fatalf("built stage (open record): problems %q", p)
	}
	unready := map[string]*Record{"DH-S-16": record(t, strings.Replace(stageRecord("open", row("DH-E-STORE-1", "passed")), "| 16.1 | DH-D-BUNDLE-3 | 0123abc |", "| 16.1 | DH-D-BUNDLE-3 | |", 1))}
	p := ProofProblems(proofStory, x, closureTable(t, built, ""), unready)
	if len(p) != 1 || !containsAll(p[0], "DH-D-BUNDLE-3 reads built", "DH-S-16 is not built", "step 16.1") {
		t.Errorf("unready stage: problems %q", p)
	}
	if p := ProofProblems(proofStory, x, closureTable(t, strings.Replace(built, "| built |", "| not built |", 1), ""), unready); len(p) != 0 {
		t.Errorf("not built row: problems %q", p)
	}
	if p := ProofProblems(proofStory, x, closureTable(t, strings.Replace(built, "| DH-S-16 | built |", "| DH-S-17 | built |", 1), ""), open); len(p) != 1 || !containsAll(p[0], "DH-S-17 is not a build stage") {
		t.Errorf("acceptance stage as Built in: problems %q", p)
	}
}

// m2Fixture is the story's shape (its BuiltIn and Finishing maps) without the rows its
// committed records owe, so in-memory stage records judge only what each case varies.
func m2Fixture() Story {
	s := Stories[DefaultStory]
	s.Obligations = nil
	return s
}

// fixtureRecord is a build-stage record that passes ready --built (ready true) or lacks
// a step commit (ready false), reading status.
func fixtureRecord(t *testing.T, status string, ready bool) *Record {
	t.Helper()
	src := stageRecord(status, row("DH-E-STORE-1", "passed"))
	if !ready {
		src = strings.Replace(src, "| 16.1 | DH-D-BUNDLE-3 | 0123abc |", "| 16.1 | DH-D-BUNDLE-3 | |", 1)
	}
	return record(t, src)
}

// A done-candidate's finishing stage is the story's, not the cell's: clearing or
// substituting the finishing column, or clearing the proofs, never skips the check that
// DH-S-12 has closed; once DH-S-12 closes, the row as recorded holds.
func TestDoneNeedsTheStorysFinishingStage(t *testing.T) {
	s, x := m2Fixture(), index(t, store1Index)
	const recorded = "| DH-D-HUMAN-4 | P1 | DH-S-12 | done | DH-S-12 (local tests) | DH-T-HUMAN-7 (no DH-E scenario) |"
	const cleared = "| DH-D-HUMAN-4 | P1 | DH-S-12 | done |  | DH-T-HUMAN-7 (no DH-E scenario) |"
	const substituted = "| DH-D-HUMAN-4 | P1 | DH-S-12 | done | DH-S-14 (local tests) | DH-T-HUMAN-7 (no DH-E scenario) |"
	const noProof = "| DH-D-HUMAN-4 | P1 | DH-S-12 | done | DH-S-12 (local tests) | |"
	// DH-S-12 is built but open; DH-S-14, the substitute, is closed.
	open := map[string]*Record{"DH-S-12": fixtureRecord(t, "open", true), "DH-S-14": fixtureRecord(t, "closed", true)}
	closed := map[string]*Record{"DH-S-12": fixtureRecord(t, "closed", true), "DH-S-14": fixtureRecord(t, "closed", true)}
	for _, tc := range []struct {
		name, row string
		recs      map[string]*Record
		want      [][]string // each a problem's parts; none means no problem
	}{
		{"cleared, S12 open", cleared, open, [][]string{{"DH-D-HUMAN-4", "does not name DH-S-12"}, {"DH-D-HUMAN-4", "DH-S-12 is not closed"}}},
		{"substituted, S12 open", substituted, open, [][]string{{"DH-D-HUMAN-4", "does not name DH-S-12"}, {"DH-D-HUMAN-4", "DH-S-12 is not closed"}}},
		{"recorded, S12 open", recorded, open, [][]string{{"DH-D-HUMAN-4", "DH-S-12 is not closed", NotYetClosed}}},
		{"no proof, S12 closed", noProof, closed, [][]string{{"DH-D-HUMAN-4", "names no proving scenario or local test"}}},
		{"substituted, S12 closed", substituted, closed, [][]string{{"DH-D-HUMAN-4", "does not name DH-S-12"}}},
		{"recorded, S12 closed", recorded, closed, nil},
	} {
		p := ProofProblems(s, x, closureTable(t, tc.row, ""), tc.recs)
		if len(p) != len(tc.want) {
			t.Errorf("%s: problems %q, want %d", tc.name, p, len(tc.want))
		}
		for _, w := range tc.want {
			if !slices.ContainsFunc(p, func(s string) bool { return containsAll(s, w...) }) {
				t.Errorf("%s: problems %q name no %q", tc.name, p, w)
			}
		}
	}
}

// Built in is the deliverable's own stage: HOST-1 marked built in DH-S-12 (ready) is
// refused and judged against DH-S-15; once DH-S-15 is ready only the wrong cell is
// refused, and the row as tech-spec sec. 3.2 writes it holds.
func TestBuiltInCannotBeSubstituted(t *testing.T) {
	s, x := m2Fixture(), index(t, store1Index)
	const substituted = "| DH-D-HOST-1 | P0 | DH-S-12 | built | DH-S-22 | DH-E-HOST-2 |"
	const correct = "| DH-D-HOST-1 | P0 | DH-S-15 | built | DH-S-22 | DH-E-HOST-2 |"
	unready := map[string]*Record{"DH-S-12": fixtureRecord(t, "open", true), "DH-S-15": fixtureRecord(t, "open", false)}
	ready := map[string]*Record{"DH-S-12": fixtureRecord(t, "open", true), "DH-S-15": fixtureRecord(t, "open", true)}
	for _, tc := range []struct {
		name, row string
		recs      map[string]*Record
		want      [][]string
	}{
		{"substituted, S15 unready", substituted, unready, [][]string{{"DH-D-HOST-1", `Built in reads "DH-S-12"`, "DH-S-15"}, {"DH-D-HOST-1 reads built", "DH-S-15 is not built"}}},
		{"correct, S15 unready", correct, unready, [][]string{{"DH-D-HOST-1 reads built", "DH-S-15 is not built"}}},
		{"substituted, S15 ready", substituted, ready, [][]string{{"DH-D-HOST-1", `Built in reads "DH-S-12"`, "DH-S-15"}}},
		{"correct, S15 ready", correct, ready, nil},
	} {
		p := ProofProblems(s, x, closureTable(t, tc.row, ""), tc.recs)
		if len(p) != len(tc.want) {
			t.Errorf("%s: problems %q, want %d", tc.name, p, len(tc.want))
		}
		for _, w := range tc.want {
			if !slices.ContainsFunc(p, func(s string) bool { return containsAll(s, w...) }) {
				t.Errorf("%s: problems %q name no %q", tc.name, p, w)
			}
		}
	}
	m2 := Stories[DefaultStory]
	if len(m2.BuiltIn) != len(m2.Deliverables) {
		t.Errorf("BuiltIn has %d deliverables; the story has %d", len(m2.BuiltIn), len(m2.Deliverables))
	}
	for id := range m2.BuiltIn {
		if _, ok := m2.Deliverables[id]; !ok {
			t.Errorf("BuiltIn names %s, not a deliverable", id)
		}
	}
	for _, id := range m2.DoneCandidates {
		if _, ok := m2.Finishing[id]; !ok {
			t.Errorf("done-candidate %s has no finishing stage", id)
		}
	}
}

// Scenario: Counts match tech-spec -- the committed closure.md has the 35 deliverables,
// at most 11 done, every row not done names its tech-spec sec. 3.2 stage or H-item, the
// held-open items in their M2 states, and every built or done state holds against the
// committed stage records (M2-I-5, M2-DOD-6).
func TestCommittedClosureRecordMatchesTechSpec(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	s := Stories[DefaultStory]
	src, err := os.ReadFile(filepath.Join(append([]string{root}, append(evidencePath, "closure.md")...)...))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ParseDocument("closure.md", strings.NewReader(string(src)), DeliverablesHeading, HeldOpenHeading)
	if err != nil {
		t.Fatal(err)
	}
	if p := ClosureProblems(s, rec); len(p) != 0 {
		t.Errorf("closure problems: %q", p)
	}
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(root)
	if p := CheckRecords(abs, s, x); len(p) != 0 {
		t.Errorf("records: %q", p)
	}
	// tech-spec sec. 3.2 "Stays built until": the 24 deliverables and what keeps each.
	staysBuilt := map[string][]string{}
	for until, ids := range map[string][]string{
		"DH-S-22": {"BUNDLE-2", "BUNDLE-3", "BUNDLE-6", "HOST-1", "HOST-2"},
		"DH-S-27": {"CLI-2", "CLI-4", "CLI-5"},
		"DH-S-28": {"FS-4"},
		"DH-S-32": {"CONTEXT-2"},
		"DH-S-39": {"SANDBOX-2", "SANDBOX-3", "EXEC-4", "EXEC-8", "EXEC-9", "HOST-3", "SDK-3", "UTIL-8"},
		"DH-S-48": {"BUNDLE-4", "BUNDLE-5", "API-1"},
	} {
		for _, id := range ids {
			staysBuilt["DH-D-"+id] = []string{until}
		}
	}
	staysBuilt["DH-D-SDK-4"] = []string{"H-1", "H-2", "H-3"}
	staysBuilt["DH-D-SDK-5"] = []string{"H-4"}
	staysBuilt["DH-D-UTIL-7"] = []string{"H-10"}
	if len(staysBuilt) != 24 {
		t.Fatalf("tech-spec table has %d stays-built deliverables; want 24", len(staysBuilt))
	}
	d := rec.Extra[DeliverablesHeading]
	done := 0
	for _, r := range d.Rows {
		id, state, fin := r.Get("deliverable"), r.Get("state"), r.Get("finishing stage or held-open item")
		if state == "done" {
			done++
		}
		if want, ok := staysBuilt[id]; ok {
			if state == "done" {
				t.Errorf("%s reads done; tech-spec sec. 3.2 keeps it built", id)
			}
			for _, w := range want {
				if !slices.Contains(append(stageIDPattern.FindAllString(fin, -1), heldOpenPattern.FindAllString(fin, -1)...), w) {
					t.Errorf("%s: finishing %q does not name %s (tech-spec sec. 3.2)", id, fin, w)
				}
			}
		} else if !slices.Contains(s.DoneCandidates, id) {
			t.Errorf("%s is neither a done-candidate nor in the stays-built table", id)
		}
	}
	if len(d.Rows) != 35 || done > 11 {
		t.Errorf("%d deliverable rows, %d done; want 35 and at most 11", len(d.Rows), done)
	}
	held := map[string]string{}
	for _, r := range rec.Extra[HeldOpenHeading].Rows {
		held[r.Get("item")] = r.Get("state")
	}
	for item, want := range map[string]string{"H-1": "open", "H-2": "open", "H-3": "open", "H-4": "open", "H-10": "open", "H-21": "closed"} {
		if held[item] != want {
			t.Errorf("held-open item %s reads %q; want %q", item, held[item], want)
		}
	}
}
