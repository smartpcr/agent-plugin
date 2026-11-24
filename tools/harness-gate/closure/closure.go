package closure

import (
	"fmt"
	"regexp"
	"slices"
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
// 7.3; M2-C-2, M2-DEC-3). "out of scope (<decision>)" is not a blocker when the
// decision is one of ScopeDecisions and applies to the row's variant; "out of scope"
// without a decision, or with an unknown or inapplicable one, is. A recorded leg
// (M2-DEC-4) counts when it ran, passed or failed.
//
// Every row's venue is guarded, acceptance or later, and a row that ran (passed or
// failed) records its environment, commit and artifacts (M2-DEC-5). With built, a row
// of an index-listed ID whose venue is not guarded is a blocker, not skipped.
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
	listed := map[string]bool{}
	for _, e := range entry.Scenarios {
		listed[e.ID] = true
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
		if t.Has("venue") && !slices.Contains(Venues, venue) {
			add("%s: venue %q is not one of %s; its result cannot be counted", name, r.Get("venue"), strings.Join(Venues, ", "))
			continue
		}
		if built && venue != "guarded" {
			if listed[id] {
				// The index lists the stage's guarded scenarios; another venue would leave
				// the row's result unjudged while it still covers the index entry.
				add("%s: listed under %s in %s, which a build stage runs guarded, but its venue reads %q", name, entry.ID, acceptance.IndexPath, r.Get("venue"))
			}
			continue
		}
		counted++
		result := r.Get("result")
		if why, ok := judge(result, r, leg == "recorded"); !ok {
			add("%s: %s", name, why)
		}
		if executed(result) {
			blockers = append(blockers, evidenceBlockers(name, r)...)
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

// Venues are the values of a scenario row's Venue column: run inside a build stage on
// scratch homes, at an acceptance stage, or authored here and run by a later stage.
var Venues = []string{"guarded", "acceptance", "later"}

// executed reports whether a result says the scenario ran: passed or failed. Those
// rows need their evidence; "out of scope (<decision>)" and the open results do not.
func executed(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	return lower == "passed" || strings.HasPrefix(lower, "passed (") || strings.HasPrefix(lower, "failed")
}

// evidenceBlockers are the evidence a row that ran must carry: the environment, the
// commit it ran at and its artifacts (M2-DEC-5: "an evidence record row with commit,
// environment and artifacts"). A recorded leg's failure is kept, so it needs
// them too.
func evidenceBlockers(name string, r Row) []string {
	var out []string
	if r.Get("environment") == "" {
		out = append(out, fmt.Sprintf("%s: ran but records no environment", name))
	}
	switch c := r.Get("commit"); {
	case c == "":
		out = append(out, fmt.Sprintf("%s: ran but records no commit", name))
	case !shaPattern.MatchString(c):
		out = append(out, fmt.Sprintf("%s: commit %q is not a commit SHA (7 to 40 lower-case hex digits)", name, c))
	}
	if r.Get("artifacts") == "" {
		out = append(out, fmt.Sprintf("%s: ran but records no artifacts", name))
	}
	return out
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

// ScopeDecision is a named decision that puts some variants of some scenarios out of
// scope (M2-C-2). Applies says whether it covers a row's scenario and variant; a
// decision never covers a variant it was not made for.
type ScopeDecision struct {
	ID      string
	Applies func(scenario, variant string) bool
}

// ScopeDecisions are the decisions "out of scope (<decision>)" may name. Any other
// text in the parentheses is a blocker: an invented reason waives nothing.
var ScopeDecisions = []ScopeDecision{
	{
		// Owner decision (M1 OWNER-1, M2-EN-2, Q m2-macos-rows): the gate is Windows
		// plus Linux under WSL; macOS rows only.
		ID: "platforms-windows-wsl",
		Applies: func(_, variant string) bool {
			return strings.Contains(strings.ToLower(variant), "macos")
		},
	},
	{
		// e2e-scenarios.md, Windows lane: DH-E-STORE-2 is Linux only (impl.md 16.8).
		ID: "impl.md 16.8 Linux only; TC-19s2",
		Applies: func(scenario, variant string) bool {
			return scenario == "DH-E-STORE-2" && strings.Contains(strings.ToLower(variant), "windows")
		},
	},
}

// scopeDecision checks the decision named by an "out of scope (<decision>)" result
// against ScopeDecisions and the row's scenario and variant.
func scopeDecision(result string, r Row) (string, bool) {
	named := strings.TrimSpace(result[len("out of scope"):])
	if !decisionPattern.MatchString(named) {
		return fmt.Sprintf("result %q names no decision; only a variant out of scope by a named decision is not a blocker (M2-C-2)", result), false
	}
	id := strings.TrimSpace(named[1 : len(named)-1])
	id = strings.TrimSpace(strings.Trim(id, "`"))
	known := make([]string, 0, len(ScopeDecisions))
	for _, d := range ScopeDecisions {
		known = append(known, d.ID)
		if !strings.EqualFold(d.ID, id) {
			continue
		}
		variant := r.Get("variant")
		if d.Applies(r.Get("scenario"), variant) {
			return "", true
		}
		return fmt.Sprintf("result %q: decision %q does not put variant %q of %s out of scope (M2-C-2)", result, d.ID, variant, r.Get("scenario")), false
	}
	return fmt.Sprintf("result %q names unknown decision %q; known: %s (M2-C-2)", result, id, strings.Join(known, ", ")), false
}

// judge decides one result cell of row r: ok, or why it is not passed.
func judge(result string, r Row, recorded bool) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(result))
	switch {
	case lower == "":
		return "no result recorded; an empty result counts as not run (M2-C-2)", false
	case lower == "passed" || strings.HasPrefix(lower, "passed ("):
		return "", true
	case strings.HasPrefix(lower, "out of scope"):
		return scopeDecision(strings.TrimSpace(result), r)
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
	return closeOn(rec, Ready(x, stage, rec, built))
}

// closeOn adds the status line to the ready blockers of rec.
func closeOn(rec *Record, blockers []string) (string, []string) {
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
