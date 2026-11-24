package closure

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// Story is what the gate knows of one story's evidence: its stages and the bounds its
// closure record must reproduce (tech-spec sec. 3.2; M2-I-5).
type Story struct {
	ID             string
	Stages         []string
	BuildStages    []string // the stages that close with --built; the others are acceptance stages
	Deliverables   int      // rows of the closure record's deliverable table
	P0, P1         int
	DoneCandidates []string // the only deliverables that may read done
	HeldOpen       []string // the H-items the closure record must list
}

// Stories are the stories whose evidence the gate checks.
var Stories = map[string]Story{
	"harness:DSH-M2": {
		ID:           "harness:DSH-M2",
		Stages:       []string{"DH-S-12", "DH-S-13", "DH-S-14", "DH-S-15", "DH-S-16", "DH-S-17", "DH-S-18"},
		BuildStages:  []string{"DH-S-12", "DH-S-13", "DH-S-14", "DH-S-15", "DH-S-16"},
		Deliverables: 35, P0: 30, P1: 5,
		DoneCandidates: []string{
			"DH-D-VIEW-10", "DH-D-SANDBOX-1", "DH-D-HUMAN-4", "DH-D-FS-6", "DH-D-SANDBOX-4", "DH-D-EXEC-3",
			"DH-D-BUNDLE-1", "DH-D-SDK-6", "DH-D-CLI-9", "DH-D-SDK-8", "DH-D-VIEW-4",
		},
		HeldOpen: []string{"H-1", "H-2", "H-3", "H-4", "H-10", "H-21"},
	},
}

// DefaultStory is the story of --story when it is not given.
const DefaultStory = "harness:DSH-M2"

// EvidenceDir is the evidence directory of story, relative to the repository root:
// docs/stories/<story with ":" as "-">/evidence.
func EvidenceDir(story string) string {
	return "docs/stories/" + strings.ReplaceAll(story, ":", "-") + "/evidence"
}

// RecordPath is the evidence record of stage, relative to the repository root.
func RecordPath(story, stage string) string { return EvidenceDir(story) + "/" + stage + ".md" }

// ClosureRecordPath is the story's closure record, relative to the repository root.
func ClosureRecordPath(story string) string { return EvidenceDir(story) + "/closure.md" }

// ReadRecord reads the evidence record of stage under root; the record's Path is the
// slash-separated path relative to root.
func ReadRecord(root, story, stage string) (*Record, error) {
	rel := RecordPath(story, stage)
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("no evidence record %s: %w", rel, err)
	}
	defer f.Close()
	return ParseRecord(rel, f)
}

// Fields713 are the headings of the eight fields of impl.md sec. 7.3, in order.
var Fields713 = []string{
	"### Source and build identity",
	"### Environment and fixture",
	"### Exact tests and commands",
	"### Run identifiers",
	"### Results",
	"### Manual checks",
	"### Cleanup evidence",
	"### Open items",
}

// ShapeProblems returns what is wrong with the form of a stage record: the status
// line (open or closed, nothing else), the eight impl.md sec. 7.3 fields and the
// gate's four tables. It does not judge the content; Ready and Close do.
func ShapeProblems(rec *Record) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, rec.Path+": "+fmt.Sprintf(format, args...)) }
	switch rec.Status {
	case StatusMissing:
		add("no %q line", statusPrefix)
	case StatusInvalid:
		add("status line reads %q; want open or closed", rec.StatusValue)
	}
	for _, h := range Fields713 {
		if !slices.Contains(rec.Headings, h) {
			add("no impl.md sec. 7.3 field %q", h)
		}
	}
	for _, s := range []struct {
		heading string
		t       Table
		cols    []string
	}{
		{StepsHeading, rec.Steps, []string{"step", "deliverable", "commit"}},
		{ScenariosHeading, rec.Scenarios, []string{"scenario", "variant", "venue", "leg", "environment", "commit", "result", "artifacts"}},
		{ManualChecksHeading, rec.ManualChecks, []string{"step", "check", "seen", "operator", "commit", "label"}},
		{OwnerDefaultsHeading, rec.OwnerDefaults, []string{"default", "raised in", "confirmed", "by", "commit"}},
	} {
		if !s.t.Present {
			add("no %q section", s.heading)
			continue
		}
		for _, c := range s.cols {
			if !s.t.Has(c) {
				add("%s has no %q column", s.heading, c)
			}
		}
	}
	return out
}

// The tables of the closure record.
const (
	DeliverablesHeading = "## Deliverables"
	HeldOpenHeading     = "## Held-open items"
)

var (
	deliverablePattern = regexp.MustCompile(`^DH-D-[A-Z][A-Z0-9]*-[1-9][0-9]*$`)
	heldOpenPattern    = regexp.MustCompile(`\bH-[1-9][0-9]*\b`)
	stageRefPattern    = regexp.MustCompile(`\b(DH-S-[1-9][0-9]*|S[1-9][0-9]*|step [1-9][0-9]*\.[1-9][0-9]*)\b`)
)

// ClosureStates are the values of a deliverable's state: not built until its stage
// builds it, then built, then done. Built and done are never merged (M2-C-3).
var ClosureStates = []string{"not built", "built", "done"}

// ClosureProblems checks the story's closure record (M2-DOD-6; M2-I-5): one row per
// deliverable with the story's P0/P1 counts, a known state, only done-candidates
// done, a finishing stage or held-open item on every row not done, and a row for
// every held-open item.
func ClosureProblems(s Story, rec *Record) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, rec.Path+": "+fmt.Sprintf(format, args...)) }
	d := rec.Extra[DeliverablesHeading]
	if d == nil || !d.Present {
		add("no %q section", DeliverablesHeading)
	} else {
		for _, c := range []string{"deliverable", "priority", "state", "finishing stage or held-open item", "proving scenarios"} {
			if !d.Has(c) {
				add("%s has no %q column", DeliverablesHeading, c)
			}
		}
		var p0, p1 int
		seen := map[string]bool{}
		for _, r := range d.Rows {
			id := r.Get("deliverable")
			if !deliverablePattern.MatchString(id) {
				add("line %d: deliverable %q is not a deliverable ID (DH-D-<AREA>-<n>)", r.Line, id)
				continue
			}
			if seen[id] {
				add("%s is listed twice", id)
			}
			seen[id] = true
			switch r.Get("priority") {
			case "P0":
				p0++
			case "P1":
				p1++
			default:
				add("%s: priority %q, want P0 or P1", id, r.Get("priority"))
			}
			state := r.Get("state")
			if !slices.Contains(ClosureStates, state) {
				add("%s: state %q, want one of %s", id, state, strings.Join(ClosureStates, ", "))
			}
			if state == "done" && !slices.Contains(s.DoneCandidates, id) {
				add("%s reads done but is not a done-candidate of %s (tech-spec sec. 3.2)", id, s.ID)
			}
			if fin := r.Get("finishing stage or held-open item"); state != "done" && !stageRefPattern.MatchString(fin) && !heldOpenPattern.MatchString(fin) {
				add("%s: %q names no finishing stage or held-open item", id, fin)
			}
		}
		if len(seen) != s.Deliverables || p0 != s.P0 || p1 != s.P1 {
			add("%s has %d deliverables (%d P0, %d P1); %s has %d (%d P0, %d P1)", DeliverablesHeading, len(seen), p0, p1, s.ID, s.Deliverables, s.P0, s.P1)
		}
		for _, id := range s.DoneCandidates {
			if !seen[id] {
				add("done-candidate %s has no row", id)
			}
		}
	}
	h := rec.Extra[HeldOpenHeading]
	if h == nil || !h.Present {
		add("no %q section", HeldOpenHeading)
		return out
	}
	if !h.Has("item") {
		add("%s has no %q column", HeldOpenHeading, "item")
	}
	items := map[string]bool{}
	for _, r := range h.Rows {
		items[r.Get("item")] = true
	}
	for _, id := range s.HeldOpen {
		if !items[id] {
			add("held-open item %s has no row in %s", id, HeldOpenHeading)
		}
	}
	return out
}

// CheckRecords is "harness-gate records": every stage record of story exists and has
// its shape; a record reading closed must also pass Close (so a legitimate close keeps
// the check green and a premature one does not); the closure record has its rows.
// A build stage's close runs with --built.
func CheckRecords(root string, s Story, x *acceptance.Index) []string {
	var out []string
	for _, stage := range s.Stages {
		rec, err := ReadRecord(root, s.ID, stage)
		if err != nil {
			out = append(out, err.Error())
			continue
		}
		out = append(out, ShapeProblems(rec)...)
		if rec.Status == StatusClosed {
			if refusal, blockers := Close(x, stage, rec, slices.Contains(s.BuildStages, stage)); refusal != "" {
				for _, b := range blockers {
					out = append(out, fmt.Sprintf("%s: reads closed but close refuses it (%s): %s", rec.Path, refusal, b))
				}
			}
		}
	}
	rel := ClosureRecordPath(s.ID)
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return append(out, fmt.Sprintf("no closure record %s: %v", rel, err))
	}
	defer f.Close()
	rec, err := ParseDocument(rel, f, DeliverablesHeading, HeldOpenHeading)
	if err != nil {
		return append(out, err.Error())
	}
	return append(out, ClosureProblems(s, rec)...)
}
