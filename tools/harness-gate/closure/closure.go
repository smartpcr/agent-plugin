package closure

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// The refusals of Close. Each names the condition, so a reviewer reads why a closing
// PR's gate failed without opening the record.
const (
	NotYetClosed      = "not-yet-closed"       // the record still reads open
	ClosedButNotReady = "closed-but-not-ready" // the record reads closed while a ready condition fails
	StatusUnreadable  = "status-invalid"       // no status line, two, or a value other than open/closed
)

var (
	shaPattern      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	scenarioPattern = regexp.MustCompile(`^DH-E-[A-Z][A-Z0-9]*-[1-9][0-9]*$`)
	decisionPattern = regexp.MustCompile(`^\(\s*[^()\s][^()]*\)$`)
)

// Ready returns every blocker of stage's record, the conditions "harness-gate ready"
// checks (implementation-plan Stage 1.1 step 1). It never reads the status line: a
// record is made ready while it reads open, and only then is its line changed.
//
// Always: every scenario the index lists under stage has a row, every row names a
// scenario ID and a known leg, and every counted row has passed. "skipped",
// "blocked", "not run", "failed" and an empty result are not passed (impl.md sec.
// 7.3; M2-C-2, M2-DEC-3). "out of scope (<decision>)" is not a blocker; "out of scope"
// without a decision is. A recorded leg (M2-DEC-4) counts when it ran, passed or
// failed.
//
// Without built (an acceptance close) every row counts, the stage must have an index
// entry and at least one row. With built (a build-stage close) only guarded rows
// count, and in addition every step has a commit SHA, every manual check its seen,
// operator, commit and label, and every owner default its confirmation (M2-C-4).
func Ready(x *acceptance.Index, stage string, rec *Record, built bool) []string {
	var blockers []string
	add := func(format string, args ...any) { blockers = append(blockers, fmt.Sprintf(format, args...)) }

	entry, indexed := x.Stage(stage)
	if !indexed && !built {
		add("%s has no entry in %s: an acceptance close needs its scenario list (a build stage closes with --built)", stage, acceptance.IndexPath)
	}
	blockers = append(blockers, scenarioBlockers(entry, rec, built)...)
	if built {
		blockers = append(blockers, builtBlockers(rec)...)
	}
	return blockers
}

func scenarioBlockers(entry acceptance.Stage, rec *Record, built bool) []string {
	var blockers []string
	add := func(format string, args ...any) { blockers = append(blockers, fmt.Sprintf(format, args...)) }
	t := rec.Scenarios
	if !t.Present {
		if len(entry.Scenarios) > 0 || !built {
			add("no %q section: the record lists no scenario", ScenariosHeading)
		}
	} else {
		for _, col := range []string{"scenario", "result"} {
			if !t.Has(col) {
				add("%s has no %q column", ScenariosHeading, col)
			}
		}
		if built && !t.Has("venue") {
			add("%s has no %q column: --built counts the guarded rows", ScenariosHeading, "venue")
		}
	}
	seen := map[string]bool{}
	counted := 0
	for _, r := range t.Rows {
		id := r.Get("scenario")
		name := rowName(r)
		if !scenarioPattern.MatchString(id) {
			add("line %d: scenario %q is not a scenario ID (DH-E-<AREA>-<n>)", r.Line, id)
			continue
		}
		seen[id] = true
		leg := strings.ToLower(r.Get("leg"))
		if leg != "" && leg != "required" && leg != "recorded" {
			add("%s: leg %q is neither required nor recorded", name, r.Get("leg"))
			continue
		}
		venue := strings.ToLower(r.Get("venue"))
		if built && venue != "guarded" {
			continue
		}
		counted++
		if why, ok := judge(r.Get("result"), leg == "recorded"); !ok {
			add("%s: %s", name, why)
		}
	}
	for _, e := range entry.Scenarios {
		if !seen[e.ID] {
			add("%s: listed under %s in %s but has no row in the record; a missing row counts as not run (M2-C-2)", e.ID, entry.ID, acceptance.IndexPath)
		}
	}
	if !built && t.Present && counted == 0 {
		add("%s has no scenario row: an acceptance close needs at least one passed scenario", ScenariosHeading)
	}
	return blockers
}

// rowName is "DH-E-X-n [variant]" for messages; the variant tells two rows of one
// scenario apart.
func rowName(r Row) string {
	name := r.Get("scenario")
	if v := r.Get("variant"); v != "" {
		name += " [" + v + "]"
	}
	if strings.EqualFold(r.Get("leg"), "recorded") {
		name += " (recorded leg)"
	}
	return name
}

// judge decides one result cell: ok, or why it is not passed.
func judge(result string, recorded bool) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(result))
	switch {
	case lower == "":
		return "no result recorded; an empty result counts as not run (M2-C-2)", false
	case lower == "passed" || strings.HasPrefix(lower, "passed ("):
		return "", true
	case strings.HasPrefix(lower, "out of scope"):
		if decisionPattern.MatchString(strings.TrimSpace(result[len("out of scope"):])) {
			return "", true
		}
		return fmt.Sprintf("result %q names no decision; only a variant out of scope by a named decision is not a blocker (M2-C-2)", result), false
	case strings.HasPrefix(lower, "skipped"):
		return fmt.Sprintf("result %q: skipped is not passed (M2-DEC-3)", result), false
	case strings.HasPrefix(lower, "not run"):
		if strings.TrimSpace(lower[len("not run"):]) == "" {
			return "result \"not run\" with no reason: not run counts as open, and its reason must be recorded (M2-C-2)", false
		}
		return fmt.Sprintf("result %q: not run counts as open (M2-C-2)", result), false
	case strings.HasPrefix(lower, "failed"):
		if recorded {
			return "", true // M2-DEC-4: a recorded leg must run; its failure is kept, not a blocker
		}
		return fmt.Sprintf("result %q: failed", result), false
	case strings.HasPrefix(lower, "blocked"):
		return fmt.Sprintf("result %q: blocked is not passed (impl.md sec. 7.3)", result), false
	case lower == "open":
		return "result \"open\": open never counts as passed (M2-C-2)", false
	}
	return fmt.Sprintf("unknown result %q; want passed, failed, not run (<reason>), skipped, blocked or out of scope (<decision>)", result), false
}

// builtBlockers are the --built conditions: steps, manual checks, owner defaults.
func builtBlockers(rec *Record) []string {
	var blockers []string
	add := func(format string, args ...any) { blockers = append(blockers, fmt.Sprintf(format, args...)) }
	need := func(t Table, heading string, cols ...string) bool {
		ok := true
		for _, c := range cols {
			if !t.Has(c) {
				add("%s has no %q column", heading, c)
				ok = false
			}
		}
		return ok
	}

	switch t := rec.Steps; {
	case !t.Present:
		add("no %q section: --built needs every step with its commit", StepsHeading)
	case !need(t, StepsHeading, "step", "commit"):
	case len(t.Rows) == 0:
		add("%s has no step row: --built needs every step with its commit", StepsHeading)
	default:
		for _, r := range t.Rows {
			name := "step " + r.Get("step")
			if d := r.Get("deliverable"); d != "" {
				name += " (" + d + ")"
			}
			if c := r.Get("commit"); c == "" {
				add("%s: no commit SHA recorded", name)
			} else if !shaPattern.MatchString(c) {
				add("%s: commit %q is not a commit SHA (7 to 40 lower-case hex digits)", name, c)
			}
		}
	}

	switch t := rec.ManualChecks; {
	case !t.Present:
		add("no %q section: --built needs every manual check recorded", ManualChecksHeading)
	case !need(t, ManualChecksHeading, "step", "check", "seen", "operator", "commit", "label"):
	default:
		for _, r := range t.Rows {
			name := fmt.Sprintf("manual check of step %s (%q)", r.Get("step"), r.Get("check"))
			for _, col := range []string{"seen", "operator"} {
				if r.Get(col) == "" {
					add("%s: no %s recorded", name, col)
				}
			}
			if c := r.Get("commit"); !shaPattern.MatchString(c) {
				if c == "" {
					add("%s: no commit recorded", name)
				} else {
					add("%s: commit %q is not a commit SHA", name, c)
				}
			}
			if l := r.Get("label"); l != "manual" {
				add("%s: label %q, want manual (impl.md sec. 7.3)", name, l)
			}
		}
	}

	switch t := rec.OwnerDefaults; {
	case !t.Present:
		add("no %q section: --built needs the stage's owner defaults confirmed (M2-C-4)", OwnerDefaultsHeading)
	case !need(t, OwnerDefaultsHeading, "default", "confirmed", "by"):
	default:
		for _, r := range t.Rows {
			name := fmt.Sprintf("owner default %q", r.Get("default"))
			if src := r.Get("raised in"); src != "" {
				name += " (" + src + ")"
			}
			switch {
			case r.Get("confirmed") == "":
				add("%s: not confirmed; impl.md sec. 9.1 requires it before the stage closes (M2-C-4)", name)
			case r.Get("by") == "":
				add("%s: the confirmation names nobody (M2-C-4)", name)
			}
		}
	}
	return blockers
}

// Close is "harness-gate close": every Ready condition and the status line reading
// closed (implementation-plan Stage 1.1 step 3). It returns "" when the stage may
// close, or the refusal (NotYetClosed, ClosedButNotReady, StatusUnreadable) and its
// blockers. An open record is refused NotYetClosed whether or not it is ready; its
// ready blockers are returned with it.
func Close(x *acceptance.Index, stage string, rec *Record, built bool) (string, []string) {
	blockers := Ready(x, stage, rec, built)
	switch rec.Status {
	case StatusMissing:
		return StatusUnreadable, append([]string{fmt.Sprintf("no %q line; want %q or %q", statusPrefix, statusPrefix+" open", statusPrefix+" closed")}, blockers...)
	case StatusInvalid:
		return StatusUnreadable, append([]string{fmt.Sprintf("the status line reads %q; want open or closed", rec.StatusValue)}, blockers...)
	case StatusOpen:
		return NotYetClosed, append([]string{"the status line reads open; the closing PR changes it to closed only after ready exits 0"}, blockers...)
	}
	if len(blockers) > 0 {
		return ClosedButNotReady, blockers
	}
	return "", nil
}
