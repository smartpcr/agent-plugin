package closure

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

var (
	proofScenarioPattern = regexp.MustCompile(`\bDH-E-[A-Z][A-Z0-9]*-[1-9][0-9]*\b`)
	stageIDPattern       = regexp.MustCompile(`\bDH-S-[1-9][0-9]*\b`)
)

// ProofProblems is the closure predicate (M2-C-3, M2-DEC-5): what a closure record's
// states claim is checked against the stage records, so built and done are never
// merged and neither is claimed ahead of its evidence. records holds the story's stage
// records by stage ID; a stage with no record is not built and not closed.
//
//   - A row reading built or done needs its "Built in" stage to be a build stage of s
//     whose record passes ready --built.
//   - A row reading done also needs every proving DH-E scenario to have a counted row
//     in some stage record (venue other than later), every such row judged passed
//     (or a recorded leg, M2-DEC-4) with its evidence, and at least one that passed;
//     every stage its finishing column names to read closed and pass close (local
//     tests are recorded only by their stage's closure); and every held-open item that
//     column names to read closed in the held-open table.
//
// Shape problems (unknown IDs, counts, done on a non-candidate) are ClosureProblems'.
func ProofProblems(s Story, x *acceptance.Index, rec *Record, records map[string]*Record) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, rec.Path+": "+fmt.Sprintf(format, args...)) }
	d := rec.Extra[DeliverablesHeading]
	if d == nil || !d.Present {
		return nil
	}
	if !d.Has("built in") {
		add("%s has no %q column: a built or done row cannot be checked against its stage", DeliverablesHeading, "built in")
	}
	held := map[string]string{}
	if h := rec.Extra[HeldOpenHeading]; h != nil {
		for _, r := range h.Rows {
			held[r.Get("item")] = strings.ToLower(r.Get("state"))
		}
	}
	builtMemo := map[string][]string{}
	notBuilt := func(stage string) []string {
		if b, ok := builtMemo[stage]; ok {
			return b
		}
		var b []string
		switch r := records[stage]; {
		case !slices.Contains(s.BuildStages, stage):
			b = []string{fmt.Sprintf("%s is not a build stage of %s", stage, s.ID)}
		case r == nil:
			b = []string{"no evidence record " + RecordPath(s.ID, stage)}
		default:
			b = s.Ready(x, stage, r, true)
		}
		builtMemo[stage] = b
		return b
	}
	for _, r := range d.Rows {
		id, state := r.Get("deliverable"), r.Get("state")
		if state != "built" && state != "done" {
			continue
		}
		if d.Has("built in") {
			stage := r.Get("built in")
			if b := notBuilt(stage); len(b) > 0 {
				add("%s reads %s but %s is not built (harness-gate ready --stage %s --built): %s", id, state, stage, stage, summarize(b))
			}
		}
		if state != "done" {
			continue
		}
		for _, sc := range proofScenarioPattern.FindAllString(r.Get("proving scenarios"), -1) {
			if why := scenarioUnproven(s, records, sc); why != "" {
				add("%s reads done but proving scenario %s %s (M2-C-3)", id, sc, why)
			}
		}
		fin := r.Get("finishing stage or held-open item")
		for _, stage := range stageIDPattern.FindAllString(fin, -1) {
			sr := records[stage]
			if sr == nil {
				add("%s reads done but its finishing stage %s has no evidence record in %s (M2-C-3)", id, stage, s.ID)
				continue
			}
			built, _ := s.Built(stage)
			if refusal, b := s.Close(x, stage, sr, built); refusal != "" {
				add("%s reads done but its finishing stage %s is not closed (%s): %s (M2-C-3)", id, stage, refusal, summarize(b))
			}
		}
		for _, h := range heldOpenPattern.FindAllString(fin, -1) {
			if st, ok := held[h]; !ok {
				add("%s reads done but held-open item %s has no row in %s (M2-C-3)", id, h, HeldOpenHeading)
			} else if st != "closed" {
				add("%s reads done but held-open item %s reads %q, not closed (M2-C-3)", id, h, st)
			}
		}
	}
	return out
}

// scenarioUnproven says why scenario has not passed across the story's stage records,
// or "" when it has.
func scenarioUnproven(s Story, records map[string]*Record, scenario string) string {
	rows, passed := 0, 0
	for _, stage := range s.Stages {
		rec := records[stage]
		if rec == nil {
			continue
		}
		for _, r := range rec.Scenarios.Rows {
			if r.Get("scenario") != scenario || strings.EqualFold(r.Get("venue"), "later") {
				continue
			}
			rows++
			result := r.Get("result")
			name := rowName(r)
			if why, ok := judge(result, r, strings.EqualFold(r.Get("leg"), "recorded")); !ok {
				return fmt.Sprintf("has not passed: %s in %s: %s", name, rec.Path, why)
			}
			if executed(result) {
				if ev := evidenceBlockers(name, r); len(ev) > 0 {
					return fmt.Sprintf("has no evidence in %s: %s", rec.Path, ev[0])
				}
				if lower := strings.ToLower(strings.TrimSpace(result)); lower == "passed" || strings.HasPrefix(lower, "passed (") {
					passed++
				}
			}
		}
	}
	switch {
	case rows == 0:
		return fmt.Sprintf("is unrun: no row in any evidence record of %s", s.ID)
	case passed == 0:
		return "has no row that passed"
	}
	return ""
}

func summarize(b []string) string {
	if len(b) == 1 {
		return b[0]
	}
	return fmt.Sprintf("%s (and %d more)", b[0], len(b)-1)
}
