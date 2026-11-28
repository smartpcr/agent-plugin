package closure

import (
	"slices"
	"strings"
	"testing"
)

// milestoneIndex lists the step 16.8 guarded scenarios and one acceptance scenario for
// each of S17 and S18.
const milestoneIndex = `built-through: DH-S-16
stages:
  DH-S-16:
    - id: DH-E-STORE-1
      authored: DH-S-7
      credential: none
  DH-S-17:
    - id: DH-E-SDK-1
      authored: DH-S-15
      credential: none
  DH-S-18:
    - id: DH-E-FS-2
      authored: DH-S-12
      credential: none
`

// milestoneRecords are the S16, S17 and S18 records of the milestone fixtures: the four
// guarded rows passed or one not run, S17 and S18 reading the given statuses with their
// one row passed.
func milestoneRecords(t *testing.T, guardedPassed bool, s17, s18 string) map[string]*Record {
	t.Helper()
	fs3 := "passed"
	if !guardedPassed {
		fs3 = "not run (no editor script on the lane)"
	}
	return map[string]*Record{
		"DH-S-16": record(t, stageRecord("open", row("DH-E-STORE-1", "passed"), row("DH-E-STORE-2", "passed"), row("DH-E-STORE-6", "passed"), row("DH-E-FS-3", fs3))),
		"DH-S-17": record(t, stageRecord(s17, row("DH-E-SDK-1", "passed"))),
		"DH-S-18": record(t, stageRecord(s18, row("DH-E-FS-2", "passed"))),
	}
}

// Scenario: Milestone predicate -- S17 closed and S18 open refuses M2 naming DH-S-18;
// with both closed and the step 16.8 guarded rows passed M2 is accepted and advances
// built-through to DH-S-18; a guarded row not run refuses it even with both closed.
func TestMilestonePredicate(t *testing.T) {
	s, x := m2Fixture(), index(t, milestoneIndex)
	for _, tc := range []struct {
		name          string
		guarded       bool
		s17, s18      string
		want          [][]string // each a problem's parts; none means accepted
		wantNotNaming string
	}{
		{"S17 closed, S18 open", true, "closed", "open", [][]string{{"M2 refused", "DH-S-18 is not closed", NotYetClosed}}, "DH-S-17"},
		{"both open", true, "open", "open", [][]string{{"DH-S-17 is not closed"}, {"DH-S-18 is not closed"}}, ""},
		{"both closed, a guarded row not run", false, "closed", "closed", [][]string{{"M2 refused", "guarded scenarios of DH-S-16", "DH-E-FS-3", "not run"}}, ""},
		{"both closed, 16.8 passed", true, "closed", "closed", nil, ""},
	} {
		through, p := s.Check(x, milestoneRecords(t, tc.guarded, tc.s17, tc.s18))
		if len(p) != len(tc.want) {
			t.Errorf("%s: problems %q, want %d", tc.name, p, len(tc.want))
		}
		for _, w := range tc.want {
			if !slices.ContainsFunc(p, func(s string) bool { return containsAll(s, w...) }) {
				t.Errorf("%s: problems %q name no %q", tc.name, p, w)
			}
		}
		if tc.wantNotNaming != "" && slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, tc.wantNotNaming) }) {
			t.Errorf("%s: problems %q name %s, which is closed", tc.name, p, tc.wantNotNaming)
		}
		if want := map[bool]string{true: "DH-S-18", false: ""}[tc.want == nil]; through != want {
			t.Errorf("%s: built-through %q, want %q", tc.name, through, want)
		}
	}
	// A missing guarded row is not run (M2-C-2): deleting DH-E-STORE-2 refuses M2.
	recs := milestoneRecords(t, true, "closed", "closed")
	recs["DH-S-16"] = record(t, stageRecord("open", row("DH-E-STORE-1", "passed"), row("DH-E-STORE-6", "passed"), row("DH-E-FS-3", "passed")))
	if _, p := s.Check(x, recs); len(p) != 1 || !containsAll(p[0], "DH-E-STORE-2", "has no row") {
		t.Errorf("missing guarded row: problems %q, want one naming DH-E-STORE-2", p)
	}
	delete(recs, "DH-S-18")
	if _, p := s.Check(x, recs); !slices.ContainsFunc(p, func(s string) bool { return containsAll(s, "DH-S-18", "no evidence record") }) {
		t.Errorf("no S18 record: problems %q", p)
	}
}

// The closure record's declaration: one line, yes or no; yes only while the predicate
// holds and built-through has reached DH-S-18.
func TestMilestoneDeclaration(t *testing.T) {
	s, x := m2Fixture(), index(t, milestoneIndex)
	refused := milestoneRecords(t, true, "closed", "open")
	accepted := milestoneRecords(t, true, "closed", "closed")
	advanced := index(t, strings.Replace(milestoneIndex, "built-through: DH-S-16", "built-through: DH-S-18", 1))
	doc := func(lines ...string) []byte {
		return []byte("# closure\n\n" + strings.Join(lines, "\n") + "\n\n```text\nM2 reached: yes\n```\n")
	}
	for _, tc := range []struct {
		name string
		src  []byte
		recs map[string]*Record
		want []string // parts of the one problem; nil means none
	}{
		{"no while refused", doc("M2 reached: no."), refused, nil},
		{"no while accepted", doc("M2 reached: no"), accepted, nil},
		{"yes while refused", doc("M2 reached: yes"), refused, []string{"closure.md:3", "declares M2 reached", "DH-S-18 is not closed"}},
		{"yes, built-through not advanced", doc("M2 reached: yes"), accepted, []string{"built-through", `"DH-S-16"`, "DH-S-18"}},
		{"missing", doc(), accepted, []string{`no "M2 reached:" line`}},
		{"twice", doc("M2 reached: no", "M2 reached: no"), accepted, []string{`2 "M2 reached:" lines`}},
		{"garbled", doc("M2 reached: soon"), accepted, []string{`"soon"`, "want yes or no"}},
	} {
		p := s.DeclarationProblems("closure.md", tc.src, x, tc.recs)
		if tc.want == nil {
			if len(p) != 0 {
				t.Errorf("%s: problems %q, want none", tc.name, p)
			}
			continue
		}
		if len(p) != 1 || !containsAll(p[0], tc.want...) {
			t.Errorf("%s: problems %q, want one naming %q", tc.name, p, tc.want)
		}
	}
	if p := s.DeclarationProblems("closure.md", doc("M2 reached: yes"), advanced, accepted); len(p) != 0 {
		t.Errorf("yes, accepted and advanced: problems %q, want none", p)
	}
}
