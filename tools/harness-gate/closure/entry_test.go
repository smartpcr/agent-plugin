package closure

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var m2Entry = Entries["harness:DSH-M2"]

func entryRecord(t *testing.T, stage, status string) *Record {
	t.Helper()
	src := fmt.Sprintf("# %s evidence\n\nClosure status: %s\n\nHistory: the **Closure status: closed** of an old stage is prose, not the line.\n", stage, status)
	rec, err := ParseRecord(RecordPath("harness:DSH-M1", stage), strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	return rec
}

// m1ClosureRecord lists every M1 deliverable, the first done and the rest built with
// a reason, plus the extra rows given.
func m1ClosureRecord(t *testing.T, extra ...string) *Record {
	t.Helper()
	var b strings.Builder
	b.WriteString("# harness:DSH-M1 closure record\n\n## Deliverables\n\n| Deliverable | State | Reason |\n| --- | --- | --- |\n")
	for i, id := range m2Entry.Deliverables {
		if i == 0 {
			fmt.Fprintf(&b, "| %s | done | |\n", id)
			continue
		}
		fmt.Fprintf(&b, "| %s | built | done when its DH-E passes in DH-S-17 |\n", id)
	}
	for _, r := range extra {
		b.WriteString(r + "\n")
	}
	rec, err := ParseDocument(ClosureRecordPath("harness:DSH-M1"), strings.NewReader(b.String()), DeliverablesHeading)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	return rec
}

func closedInputs(t *testing.T) EntryInputs {
	t.Helper()
	in := EntryInputs{Records: map[string]*Record{}, Closure: m1ClosureRecord(t), BuiltThrough: "DH-S-11"}
	for _, s := range m2Entry.PriorStages {
		in.Records[s] = entryRecord(t, s, "closed at 1a2b3c4")
	}
	return in
}

// Scenario "Entry refused while an M1 record is open" (implementation-plan Stage 1.2).
func TestEntryRefusedWhileAnM1RecordIsOpen(t *testing.T) {
	in := closedInputs(t)
	in.Records["DH-S-11"] = entryRecord(t, "DH-S-11", "open")
	got := m2Entry.Check(in)
	if len(got) != 1 || !strings.HasPrefix(got[0], "DH-S-11: ") || !strings.Contains(got[0], `"Closure status: open"`) {
		t.Fatalf("one open record: got %q, want one condition naming DH-S-11", got)
	}
	if got := m2Entry.Check(closedInputs(t)); len(got) != 0 {
		t.Fatalf("all eleven closed, a closure record and built-through DH-S-11: got %q, want a pass", got)
	}
}

func TestEntryNamesEachUnmetCondition(t *testing.T) {
	in := EntryInputs{Records: map[string]*Record{}, BuiltThrough: "DH-S-8"}
	for _, s := range m2Entry.PriorStages[:9] {
		in.Records[s] = entryRecord(t, s, "open")
	}
	missingLine, err := ParseRecord("x.md", strings.NewReader("# DH-S-10\n"))
	if err != nil {
		t.Fatal(err)
	}
	in.Records["DH-S-10"] = missingLine
	got := m2Entry.Check(in)
	if len(got) != 13 {
		t.Fatalf("got %d conditions, want 13 (nine open, one without a status line, one missing, the closure record, built-through):\n%s", len(got), strings.Join(got, "\n"))
	}
	for i, s := range m2Entry.PriorStages {
		if !strings.HasPrefix(got[i], s+": ") {
			t.Errorf("condition %d = %q, want it to name %s", i, got[i], s)
		}
	}
	for i, want := range []string{"has no \"Closure status:\" line", "no evidence record docs/stories/harness-DSH-M1/evidence/DH-S-11.md", "no harness:DSH-M1 closure record docs/stories/harness-DSH-M1/evidence/closure.md", "built-through in test/harness/acceptance-index.yaml reads DH-S-8; want DH-S-11 or later"} {
		if !strings.Contains(got[9+i], want) {
			t.Errorf("condition %d = %q, want it to contain %q", 9+i, got[9+i], want)
		}
	}
}

func TestEntryStatusLineMustReadClosed(t *testing.T) {
	for _, status := range []string{"closed at main", "done"} {
		in := closedInputs(t)
		in.Records["DH-S-3"] = entryRecord(t, "DH-S-3", status)
		if got := m2Entry.Check(in); len(got) != 1 || !strings.HasPrefix(got[0], "DH-S-3: ") {
			t.Errorf("status %q: got %q, want one condition naming DH-S-3", status, got)
		}
	}
}

func TestEntryBuiltThroughAtLeast(t *testing.T) {
	for bt, ok := range map[string]bool{"DH-S-10": false, "DH-S-11": true, "DH-S-12": true, "": false} {
		in := closedInputs(t)
		in.BuiltThrough = bt
		if got := m2Entry.Check(in); (len(got) == 0) != ok {
			t.Errorf("built-through %q: got %q, want pass=%v", bt, got, ok)
		}
	}
}

func TestEntryClosureRecordContent(t *testing.T) {
	cases := map[string]struct {
		rec  func(t *testing.T) *Record
		want []string
	}{
		"no deliverables table": {
			rec: func(t *testing.T) *Record {
				r, err := ParseDocument("c.md", strings.NewReader("# closure\n"), DeliverablesHeading)
				if err != nil {
					t.Fatal(err)
				}
				return r
			},
			want: []string{`no "## Deliverables" section`},
		},
		"bad rows": {
			rec: func(t *testing.T) *Record {
				return m1ClosureRecord(t,
					"| DH-D-TOOLCHAIN-1 | built | twice |",
					"| DH-D-VIEW-10 | done | |",
					"| DH-D-BOGUS | done | |",
					"| DH-D-CONTEXT-1 | open | |",
				)
			},
			want: []string{"DH-D-TOOLCHAIN-1 is listed twice", "DH-D-VIEW-10 is not a deliverable of harness:DSH-M1", `deliverable "DH-D-BOGUS" is not a deliverable ID`, `DH-D-CONTEXT-1: state "open"`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			in := closedInputs(t)
			in.Closure = c.rec(t)
			got := strings.Join(m2Entry.Check(in), "\n")
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want a condition containing %q; got:\n%s", w, got)
				}
			}
		})
	}
	t.Run("built without reason and missing rows", func(t *testing.T) {
		src := "## Deliverables\n\n| Deliverable | State |\n| --- | --- |\n| DH-D-TOOLCHAIN-1 | built |\n"
		rec, err := ParseDocument("c.md", strings.NewReader(src), DeliverablesHeading)
		if err != nil {
			t.Fatal(err)
		}
		in := closedInputs(t)
		in.Closure = rec
		got := m2Entry.Check(in)
		joined := strings.Join(got, "\n")
		for _, w := range []string{`has no "reason" column`, "DH-D-TOOLCHAIN-1 reads built but gives no reason", "deliverable DH-D-CONTEXT-1 has no row"} {
			if !strings.Contains(joined, w) {
				t.Errorf("want %q; got:\n%s", w, joined)
			}
		}
		if n := strings.Count(joined, "has no row"); n != len(m2Entry.Deliverables)-1 {
			t.Errorf("%d missing-row conditions, want %d", n, len(m2Entry.Deliverables)-1)
		}
	})
}

// The deliverable list is impl.md steps 1.1 to 11.7: each step's first DH-D link.
func TestM1DeliverablesMatchImpl(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "harness", "impl.md"))
	if err != nil {
		t.Skipf("impl.md not readable from the tools module: %v", err)
	}
	step := regexp.MustCompile(`^- \[[ x]\] \*\*([0-9]+)\.[0-9]+\*\*\s+(?:Part of )?\[?(DH-D-[A-Z0-9]+-[0-9]+)`)
	var want []string
	for _, line := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		m := step.FindStringSubmatch(line)
		if m == nil || len(m[1]) > 2 || (len(m[1]) == 2 && m[1] > "11") {
			continue
		}
		if !slices.Contains(want, m[2]) {
			want = append(want, m[2])
		}
	}
	if !slices.Equal(want, m2Entry.Deliverables) {
		t.Errorf("m1Deliverables differ from impl.md steps 1.1 to 11.7:\n got %q\nwant %q", m2Entry.Deliverables, want)
	}
	if len(want) != 99 {
		t.Errorf("impl.md steps 1.1 to 11.7 name %d deliverables, the M1 tech-spec 99", len(want))
	}
}
