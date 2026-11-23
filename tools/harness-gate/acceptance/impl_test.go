package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// planStage renders one build stage section: ticks[i] says whether step n.(i+1)
// is ticked, and each step builds DH-D-A-<n><i+1>.
func planStage(n int, paired string, ticks ...bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<a id=\"dh-s-%d\"></a>\n### DH-S-%d: Stage %d\n\n", n, n, n)
	if paired != "" {
		fmt.Fprintf(&b, "**Kind and priority.** Build, P0. Paired acceptance stage: [%s](#%s).\n\n", paired, strings.ToLower(paired))
	}
	b.WriteString("**Steps.**\n\n")
	for i, ticked := range ticks {
		box := " "
		if ticked {
			box = "x"
		}
		fmt.Fprintf(&b, "- [%s] **%d.%d** [DH-D-A-%d%d](areas/a.md#dh-d-a-%d%d) A step.\n", box, n, i+1, n, i+1, n, i+1)
	}
	b.WriteString("\n**Manual checks.** None.\n\n")
	return b.String()
}

func mustParseImpl(t *testing.T, src string) *ImplPlan {
	t.Helper()
	p, err := ParseImpl(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseImpl: %v", err)
	}
	return p
}

// tickPlan is DH-S-1..9 where every step through DH-S-7 is ticked and DH-S-8's
// step 8.3 is not.
func tickPlan() string {
	var b strings.Builder
	b.WriteString("# plan\n\n## 6. The stages\n\n")
	for n := 1; n <= 7; n++ {
		b.WriteString(planStage(n, "DH-S-17", true, true))
	}
	b.WriteString(planStage(8, "DH-S-17", true, true, false, true))
	b.WriteString(planStage(9, "DH-S-17", false))
	b.WriteString("## 7. Acceptance\n")
	return b.String()
}

func indexAt(t *testing.T, stage string) *Index {
	t.Helper()
	return mustParse(t, "built-through: "+stage+"\nstages:\n  DH-S-17: []\n")
}

func TestParseImpl(t *testing.T) {
	p := mustParseImpl(t, tickPlan())
	if len(p.Stages) != 9 {
		t.Fatalf("%d stages", len(p.Stages))
	}
	s, ok := p.Stage("DH-S-8")
	if !ok || s.Paired != "DH-S-17" || len(s.Steps) != 4 || s.Steps[2].ID != "8.3" || s.Steps[2].Ticked || !s.Steps[3].Ticked {
		t.Fatalf("DH-S-8 = %+v", s)
	}
	if d := s.Steps[0].Deliverables; len(d) != 1 || d[0] != (DeliverableLink{ID: "DH-D-A-81", File: "areas/a.md", Anchor: "dh-d-a-81"}) {
		t.Errorf("8.1 links %+v", d)
	}
	if got, ok := p.DeliverableStage("DH-D-A-82"); !ok || got != "DH-S-8" {
		t.Errorf("DeliverableStage(DH-D-A-82) = %q, %v", got, ok)
	}
	if _, ok := p.DeliverableStage("DH-D-NONE-1"); ok {
		t.Error("DeliverableStage(DH-D-NONE-1) found a stage")
	}
	if _, ok := p.Stage("DH-S-10"); ok {
		t.Error("Stage(DH-S-10) found a section")
	}
	for src, want := range map[string]string{
		"## 6\n":                         "no \"### DH-S-n:\" stage section",
		"### DH-S-1: a\n### DH-S-1: b\n": "line 2: stage DH-S-1 has two sections",
	} {
		if _, err := ParseImpl(strings.NewReader(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseImpl(%q) = %v, want %q", src, err, want)
		}
	}
	if _, err := ReadImpl(t.TempDir()); err == nil || !strings.Contains(err.Error(), ImplPath) {
		t.Errorf("ReadImpl on an empty tree = %v", err)
	}
	if _, err := ReadImpl(writeTree(t, map[string]string{ImplPath: "nothing\n"})); err == nil || !strings.Contains(err.Error(), ImplPath+": no") {
		t.Errorf("ReadImpl on a plan without stages = %v", err)
	}
}

func TestBuiltThroughMatchesTicks(t *testing.T) {
	p := mustParseImpl(t, tickPlan())
	if err := CheckBuiltThrough(indexAt(t, "DH-S-7"), p); err != nil {
		t.Errorf("CheckBuiltThrough(DH-S-7) = %v, want a pass", err)
	}
	// A marker behind the ticks names the stage it should be raised to.
	if err := CheckBuiltThrough(indexAt(t, "DH-S-6"), p); err == nil || !strings.Contains(err.Error(), "every step of DH-S-1..DH-S-7 is ticked") || !strings.Contains(err.Error(), "raise the marker to DH-S-7") {
		t.Errorf("CheckBuiltThrough(DH-S-6) = %v, want the marker raised to DH-S-7", err)
	}
	// Every stage ticked through the last section: the marker equals the last.
	all := mustParseImpl(t, "## 6\n\n"+planStage(1, "", true)+planStage(2, "", true))
	if err := CheckBuiltThrough(indexAt(t, "DH-S-2"), all); err != nil {
		t.Errorf("CheckBuiltThrough on a fully ticked plan = %v", err)
	}
	if err := CheckBuiltThrough(&Index{BuiltThrough: "seven"}, p); err == nil {
		t.Error("CheckBuiltThrough with a bad marker passed")
	}
}

func TestBuiltThroughAheadOfTicksFails(t *testing.T) {
	p := mustParseImpl(t, tickPlan())
	err := CheckBuiltThrough(indexAt(t, "DH-S-8"), p)
	if err == nil || !strings.Contains(err.Error(), "built-through DH-S-8, but step 8.3 of DH-S-8 is not ticked") {
		t.Fatalf("CheckBuiltThrough(DH-S-8) = %v, want step 8.3 named", err)
	}
	if err := CheckBuiltThrough(indexAt(t, "DH-S-12"), p); err == nil || !strings.Contains(err.Error(), "step 8.3 of DH-S-8") {
		t.Errorf("CheckBuiltThrough(DH-S-12) = %v, want the first offending step, 8.3", err)
	}
	gap := mustParseImpl(t, "## 6\n\n"+planStage(1, "", true)+planStage(3, "", true))
	if err := CheckBuiltThrough(indexAt(t, "DH-S-3"), gap); err == nil || !strings.Contains(err.Error(), "DH-S-2 has no section") {
		t.Errorf("CheckBuiltThrough over a missing stage = %v", err)
	}
	empty := mustParseImpl(t, "## 6\n\n"+planStage(1, "")+planStage(2, "", true))
	if err := CheckBuiltThrough(indexAt(t, "DH-S-2"), empty); err == nil || !strings.Contains(err.Error(), "DH-S-1 has no steps") {
		t.Errorf("CheckBuiltThrough over a stage without steps = %v", err)
	}
}

// rule4Plan: DH-S-6 builds DH-D-A-61 and -62 and pairs with DH-S-18; DH-S-10
// builds DH-D-A-101 and pairs with DH-S-17. Sec. 7.5 places DH-E-EXEC-10 (proves
// DH-D-A-61, an EXEC-10-like scenario) and DH-E-LIB-1 (a library requirement) in
// DH-S-18, DH-E-HUMAN-1 (proves DH-D-A-101, a HUMAN-1-like scenario moved later)
// in DH-S-22, and DH-E-TOOLS-1 (proves DH-D-A-101) in DH-S-17.
func rule4Plan() string {
	return "# plan\n\n## 6. The stages\n\n" +
		planStage(6, "DH-S-18", true, true) +
		planStage(10, "DH-S-17", true) +
		"## 7. Acceptance\n\n### 7.5 Index of all scenarios\n\n" +
		"| Stage | Count | Scenarios |\n|---|---|---|\n" +
		"| [DH-S-17](#dh-s-17) | 1 | [DH-E-TOOLS-1](areas/tools.md#dh-e-tools-1) |\n" +
		"| [DH-S-18](#dh-s-18) | 4 | [DH-E-EXEC-10](areas/execution.md#dh-e-exec-10), [DH-E-STORAGE-6](areas/storage.md#dh-e-storage-6), [DH-E-LIB-1](libs/lib.md), [DH-E-ODD-1](areas/odd.md) |\n" +
		"| [DH-S-22](#dh-s-22) | 1 | [DH-E-HUMAN-1](areas/interaction-credentials.md#dh-e-human-1) |\n" +
		"\n## 8. Known limits\n"
}

var rule4Proves = map[string][]string{
	"DH-E-EXEC-10":   {"DH-D-A-61"},
	"DH-E-STORAGE-6": {"DH-D-A-62", "DH-ADR-STORAGE-8"},
	"DH-E-LIB-1":     {"DH-LIB-X-R1", "DH-ADR-X-1"},
	"DH-E-ODD-1":     {"DH-D-A-61", "DH-D-NOWHERE-1"},
	"DH-E-HUMAN-1":   {"DH-D-A-101"},
	"DH-E-TOOLS-1":   {"DH-D-A-101"},
}

func TestRule4FlagsUnauthoredPairedScenario(t *testing.T) {
	p := mustParseImpl(t, rule4Plan())
	if len(p.Placements) != 3 || p.Placements[1].Stage != "DH-S-18" || len(p.Placements[1].Scenarios) != 4 {
		t.Fatalf("placements = %+v", p.Placements)
	}
	x := mustParse(t, "built-through: DH-S-10\nstages:\n  DH-S-17:\n    - id: DH-E-TOOLS-1\n      authored: DH-S-10\n  DH-S-18: []\n")
	err := CheckRule4Authoring(x, p, rule4Proves)
	if err == nil {
		t.Fatal("CheckRule4Authoring passed with DH-E-EXEC-10 and DH-E-STORAGE-6 unindexed")
	}
	for _, want := range []string{
		"DH-E-EXEC-10 is owed by DH-S-6 (rule 4): its proves list (DH-D-A-61) is built through DH-S-6, docs/harness/impl.md sec. 7.5 places it in DH-S-18, the paired acceptance stage of DH-S-6, and test/harness/acceptance-index.yaml does not index it",
		"DH-E-STORAGE-6 is owed by DH-S-6",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CheckRule4Authoring = %v, want %q", err, want)
		}
	}
	for _, not := range []string{"DH-E-LIB-1", "DH-E-ODD-1", "DH-E-TOOLS-1", "DH-E-HUMAN-1"} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("CheckRule4Authoring = %v: %s is not owed", err, not)
		}
	}
	// Indexing both makes the check pass.
	both := mustParse(t, "built-through: DH-S-10\nstages:\n  DH-S-17:\n    - id: DH-E-TOOLS-1\n      authored: DH-S-10\n  DH-S-18:\n"+
		"    - id: DH-E-EXEC-10\n      authored: DH-S-6\n    - id: DH-E-STORAGE-6\n      authored: DH-S-6\n")
	if err := CheckRule4Authoring(both, p, rule4Proves); err != nil {
		t.Errorf("CheckRule4Authoring with both indexed = %v", err)
	}
	// Before DH-S-6 is built nothing is owed yet.
	if err := CheckRule4Authoring(indexAt(t, "DH-S-5"), p, rule4Proves); err != nil {
		t.Errorf("CheckRule4Authoring built through DH-S-5 = %v", err)
	}
	if err := CheckRule4Authoring(&Index{BuiltThrough: "x"}, p, rule4Proves); err == nil {
		t.Error("CheckRule4Authoring with a bad marker passed")
	}
}

func TestRule4IgnoresScenarioMovedLater(t *testing.T) {
	p := mustParseImpl(t, rule4Plan())
	// Built through DH-S-10: DH-E-HUMAN-1 proves only DH-D-A-101 of DH-S-10, whose
	// paired stage is DH-S-17, but sec. 7.5 moved it to DH-S-22: DH-S-10 does not
	// owe it, so it may stay unindexed. DH-E-TOOLS-1, placed in DH-S-17, is owed.
	x := mustParse(t, "built-through: DH-S-10\nstages:\n  DH-S-18:\n"+
		"    - id: DH-E-EXEC-10\n      authored: DH-S-6\n    - id: DH-E-STORAGE-6\n      authored: DH-S-6\n")
	err := CheckRule4Authoring(x, p, rule4Proves)
	if err == nil || !strings.Contains(err.Error(), "DH-E-TOOLS-1 is owed by DH-S-10") {
		t.Fatalf("CheckRule4Authoring = %v, want DH-E-TOOLS-1 owed", err)
	}
	if strings.Contains(err.Error(), "DH-E-HUMAN-1") {
		t.Errorf("CheckRule4Authoring = %v: DH-E-HUMAN-1 runs in DH-S-22 and is not owed by DH-S-10", err)
	}
}

// TestCheckPlanRunsInCheckBoundary: the scenario-index gate's CheckBoundary runs
// both plan checks (13.6.4, 13.6.6), and a root without impl.md or the ledger fails
// rather than skips.
func TestCheckPlanRunsInCheckBoundary(t *testing.T) {
	ledger := `{"kind":"scenario","go":"DH-E-EXEC-10","proves":["DH-D-A-61"]}` + "\n"
	root := writeTree(t, map[string]string{ImplPath: rule4Plan(), LedgerPath: ledger})
	ahead := mustParse(t, "built-through: DH-S-7\nstages:\n  DH-S-18: []\n")
	err := CheckBoundary(root, ahead, nil)
	if err == nil || !strings.Contains(err.Error(), "built-through DH-S-7, but DH-S-1 has no section") || !strings.Contains(err.Error(), "DH-E-EXEC-10 is owed by DH-S-6") {
		t.Fatalf("CheckBoundary = %v, want the marker drift and the rule-4 omission named", err)
	}
	full := "## 6\n\n"
	for n := 1; n <= 6; n++ {
		full += planStage(n, "DH-S-18", true)
	}
	full += "## 7\n\n### 7.5 Index\n\n| [DH-S-18](#dh-s-18) | 1 | [DH-E-EXEC-10](x.md) |\n"
	good := writeTree(t, map[string]string{ImplPath: full, LedgerPath: `{"kind":"scenario","go":"DH-E-EXEC-10","proves":["DH-D-A-61"]}` + "\n"})
	x := mustParse(t, "built-through: DH-S-6\nstages:\n  DH-S-18:\n    - id: DH-E-EXEC-10\n      authored: DH-S-6\n")
	if err := CheckBoundary(good, x, nil); err != nil {
		t.Errorf("CheckBoundary on an agreeing plan = %v", err)
	}
	if err := CheckPlan(t.TempDir(), x); err == nil || !strings.Contains(err.Error(), ImplPath) {
		t.Errorf("CheckPlan without impl.md = %v, want it named", err)
	}
	if err := CheckPlan(writeTree(t, map[string]string{ImplPath: full}), x); err == nil || !strings.Contains(err.Error(), LedgerPath) {
		t.Errorf("CheckPlan without the ledger = %v, want it named", err)
	}
}

func TestReadLedgerScenarios(t *testing.T) {
	root := writeTree(t, map[string]string{LedgerPath: `{"kind":"deliverable","go":"DH-D-A-1","proof":["DH-T-A-1"]}

{"kind":"scenario","go":"DH-E-A-1","proves":["DH-D-A-1","DH-ADR-A-1"]}
{"unit":"UP-X","disposition":"carried"}
`})
	got, err := ReadLedgerScenarios(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Join(got["DH-E-A-1"], ",") != "DH-D-A-1,DH-ADR-A-1" {
		t.Errorf("ReadLedgerScenarios = %v", got)
	}
	if _, err := ReadLedgerScenarios(t.TempDir()); err == nil || !strings.Contains(err.Error(), LedgerPath) {
		t.Errorf("ReadLedgerScenarios on an empty tree = %v", err)
	}
	bad := writeTree(t, map[string]string{LedgerPath: "{\n"})
	if _, err := ReadLedgerScenarios(bad); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("ReadLedgerScenarios on a broken row = %v", err)
	}
}
