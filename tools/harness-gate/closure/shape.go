package closure

import (
	"bytes"
	"fmt"
	"maps"
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
	BuildStages    []string          // the stages that close with --built; the others are acceptance stages
	Deliverables   map[string]string // every deliverable of the closure record, by ID, with its priority
	P0, P1         int
	DoneCandidates []string               // the only deliverables that may read done
	BuiltIn        map[string]string      // the stage that builds each deliverable (tech-spec sec. 3.2)
	Finishing      map[string]string      // the stage whose closure proves each done-candidate
	HeldOpen       []string               // the H-items the closure record must list
	Obligations    map[string]Obligations // the rows each stage record owes
	Milestone      Milestone              // when the closure record may declare the milestone reached
}

// Built reports whether stage closes with --built: a stage of the story is a build
// stage exactly when BuildStages lists it, whatever the caller asked for.
func (s Story) Built(stage string) (built, known bool) {
	return slices.Contains(s.BuildStages, stage), slices.Contains(s.Stages, stage)
}

// ModeProblem names the mismatch when built is not the mode stage closes in, or "".
func (s Story) ModeProblem(stage string, built bool) string {
	want, known := s.Built(stage)
	switch {
	case !known || want == built:
		return ""
	case want:
		return fmt.Sprintf("%s is a build stage of %s: it closes with --built (steps, manual checks, owner defaults and guarded rows)", stage, s.ID)
	default:
		return fmt.Sprintf("%s is an acceptance stage of %s: it closes without --built", stage, s.ID)
	}
}

// Ready is the package Ready plus the rows stage's record owes (Obligations): a
// deleted step, manual check, owner default or scenario row is a blocker. A stage of
// the story is judged in its own mode; asking for the other mode is itself a blocker,
// so a build stage judged without --built can never skip its build tables.
func (s Story) Ready(x *acceptance.Index, stage string, rec *Record, built bool) []string {
	var blockers []string
	if p := s.ModeProblem(stage, built); p != "" {
		blockers = append(blockers, p)
		built, _ = s.Built(stage)
	}
	blockers = append(blockers, Ready(x, stage, rec, built)...)
	if ob, ok := s.Obligations[stage]; ok {
		blockers = append(blockers, ob.Missing(rec, built)...)
	}
	return blockers
}

// Close is the package Close over Story.Ready.
func (s Story) Close(x *acceptance.Index, stage string, rec *Record, built bool) (string, []string) {
	return closeOn(rec, s.Ready(x, stage, rec, built))
}

// Stories are the stories whose evidence the gate checks.
var Stories = map[string]Story{
	"harness:DSH-M2": {
		ID:           "harness:DSH-M2",
		Stages:       []string{"DH-S-12", "DH-S-13", "DH-S-14", "DH-S-15", "DH-S-16", "DH-S-17", "DH-S-18"},
		BuildStages:  []string{"DH-S-12", "DH-S-13", "DH-S-14", "DH-S-15", "DH-S-16"},
		Deliverables: m2Deliverables, P0: 30, P1: 5,
		DoneCandidates: []string{
			"DH-D-VIEW-10", "DH-D-SANDBOX-1", "DH-D-HUMAN-4", "DH-D-FS-6", "DH-D-SANDBOX-4", "DH-D-EXEC-3",
			"DH-D-BUNDLE-1", "DH-D-SDK-6", "DH-D-CLI-9", "DH-D-SDK-8", "DH-D-VIEW-4",
		},
		HeldOpen:    []string{"H-1", "H-2", "H-3", "H-4", "H-10", "H-21"},
		BuiltIn:     m2BuiltIn,
		Finishing:   m2Finishing,
		Obligations: m2Obligations,
		// impl.md sec. 3.3, M2 row: S17 and S18 closed, and the four step 16.8 guarded
		// scenarios (implementation-plan Stage 6.12) passed in the DH-S-16 record.
		Milestone: Milestone{
			Name:         "M2",
			Closed:       []string{"DH-S-17", "DH-S-18"},
			GuardedStage: "DH-S-16",
			Guarded:      []string{"DH-E-STORE-1", "DH-E-STORE-2", "DH-E-STORE-6", "DH-E-FS-3"},
		},
	},
}

// m2BuiltIn is the stage that builds each deliverable: the "Delivers" column of
// tech-spec sec. 3.2.
var m2BuiltIn = func() map[string]string {
	out := map[string]string{}
	for stage, ids := range map[string][]string{
		"DH-S-12": {"VIEW-10", "FS-4", "SANDBOX-1", "HUMAN-4", "FS-6"},
		"DH-S-13": {"SANDBOX-2", "SANDBOX-3", "SANDBOX-4", "EXEC-3", "EXEC-4", "EXEC-8", "EXEC-9", "CONTEXT-2"},
		"DH-S-14": {"BUNDLE-1", "BUNDLE-2", "BUNDLE-4", "BUNDLE-5", "BUNDLE-6"},
		"DH-S-15": {"HOST-1", "HOST-2", "HOST-3", "API-1", "SDK-3", "SDK-4", "SDK-5", "SDK-6"},
		"DH-S-16": {"BUNDLE-3", "CLI-2", "CLI-4", "CLI-5", "CLI-9", "SDK-8", "UTIL-7", "UTIL-8", "VIEW-4"},
	} {
		for _, id := range ids {
			out["DH-D-"+id] = stage
		}
	}
	return out
}()

// m2Finishing is the stage whose closure proves each done-candidate: the stage that
// runs its proving scenario, or for one proved by local tests only, the stage that
// builds it (implementation-plan steps 6.12, 7.x and 8.x).
var m2Finishing = map[string]string{
	"DH-D-VIEW-10": "DH-S-18", "DH-D-SANDBOX-1": "DH-S-17", "DH-D-HUMAN-4": "DH-S-12", "DH-D-FS-6": "DH-S-18",
	"DH-D-SANDBOX-4": "DH-S-18", "DH-D-EXEC-3": "DH-S-13", "DH-D-BUNDLE-1": "DH-S-14", "DH-D-SDK-6": "DH-S-17",
	"DH-D-CLI-9": "DH-S-16", "DH-D-SDK-8": "DH-S-17", "DH-D-VIEW-4": "DH-S-17",
}

// m2Deliverables are the 35 deliverables of tech-spec sec. 3.2, with their priority.
var m2Deliverables = map[string]string{
	"DH-D-VIEW-10": "P0", "DH-D-FS-4": "P0", "DH-D-SANDBOX-1": "P0", "DH-D-HUMAN-4": "P1", "DH-D-FS-6": "P0",
	"DH-D-SANDBOX-2": "P0", "DH-D-SANDBOX-3": "P0", "DH-D-SANDBOX-4": "P0", "DH-D-EXEC-3": "P0", "DH-D-EXEC-4": "P0",
	"DH-D-EXEC-8": "P0", "DH-D-EXEC-9": "P0", "DH-D-CONTEXT-2": "P0", "DH-D-BUNDLE-1": "P0", "DH-D-BUNDLE-2": "P0",
	"DH-D-BUNDLE-4": "P0", "DH-D-BUNDLE-5": "P0", "DH-D-BUNDLE-6": "P0", "DH-D-HOST-1": "P0", "DH-D-HOST-2": "P0",
	"DH-D-HOST-3": "P0", "DH-D-API-1": "P0", "DH-D-SDK-3": "P0", "DH-D-SDK-4": "P0", "DH-D-SDK-5": "P0",
	"DH-D-SDK-6": "P0", "DH-D-BUNDLE-3": "P0", "DH-D-CLI-2": "P0", "DH-D-CLI-4": "P0", "DH-D-CLI-5": "P0",
	"DH-D-CLI-9": "P0", "DH-D-SDK-8": "P1", "DH-D-UTIL-7": "P1", "DH-D-UTIL-8": "P1", "DH-D-VIEW-4": "P1",
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
			want, owed := s.Deliverables[id]
			if !owed {
				add("%s is not a deliverable of %s (tech-spec sec. 3.2)", id, s.ID)
			} else if got := r.Get("priority"); got != want {
				add("%s: priority %q, tech-spec sec. 3.2 gives %s", id, got, want)
			}
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
		if len(seen) != len(s.Deliverables) || p0 != s.P0 || p1 != s.P1 {
			add("%s has %d deliverables (%d P0, %d P1); %s has %d (%d P0, %d P1)", DeliverablesHeading, len(seen), p0, p1, s.ID, len(s.Deliverables), s.P0, s.P1)
		}
		for _, id := range slices.Sorted(maps.Keys(s.Deliverables)) {
			if !seen[id] {
				add("deliverable %s has no row (tech-spec sec. 3.2)", id)
			}
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
// the check green and a premature one does not); the closure record has its rows,
// and its built and done states hold against the stage records (ProofProblems), and its
// milestone declaration reads yes only while the milestone predicate holds
// (DeclarationProblems). A build stage's close runs with --built.
func CheckRecords(root string, s Story, x *acceptance.Index) []string {
	var out []string
	records := map[string]*Record{}
	for _, stage := range s.Stages {
		rec, err := ReadRecord(root, s.ID, stage)
		if err != nil {
			out = append(out, err.Error())
			continue
		}
		records[stage] = rec
		out = append(out, ShapeProblems(rec)...)
		// An open record may have empty cells, but never fewer rows than it owes.
		for _, m := range s.Obligations[stage].Missing(rec, true) {
			out = append(out, rec.Path+": "+m)
		}
		if rec.Status == StatusClosed {
			if refusal, blockers := s.Close(x, stage, rec, slices.Contains(s.BuildStages, stage)); refusal != "" {
				for _, b := range blockers {
					out = append(out, fmt.Sprintf("%s: reads closed but close refuses it (%s): %s", rec.Path, refusal, b))
				}
			}
		}
	}
	rel := ClosureRecordPath(s.ID)
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return append(out, fmt.Sprintf("no closure record %s: %v", rel, err))
	}
	rec, err := ParseDocument(rel, bytes.NewReader(src), DeliverablesHeading, HeldOpenHeading)
	if err != nil {
		return append(out, err.Error())
	}
	out = append(out, ClosureProblems(s, rec)...)
	out = append(out, ProofProblems(s, x, rec, records)...)
	return append(out, s.DeclarationProblems(rel, src, x, records)...)
}
