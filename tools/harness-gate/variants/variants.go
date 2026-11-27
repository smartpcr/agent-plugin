// Package variants reads the E2 variant fleet of the S18 acceptance,
// docs/stories/harness-DSH-M2/evidence/variants.md (implementation-plan Stage
// 8.1; impl.md sec. 7.1 E2; tech-spec M2-EN-2, M2-EN-3), and cross-references it
// with the DH-S-18 entry of the acceptance index: every scenario of the entry has
// at least one in-scope variant or an owner decision removing it (M2-DOD-4).
// Each s18 job of harness-scenarios.yml runs one variant, and takes its
// DSH_REQUIRE_SCENARIOS from Require: the entry filtered to that variant.
//
// The file has two tables the gate reads:
//
//	## Variants
//	| Variant | Platform | Runner | Provider | Service | Preflight | Scenarios | Scope |
//	## Owner removals
//	| Scenario | Decision |
//
// Service lists one or more `service:*` names joined by " + "; Scenarios lists
// DH-E IDs separated by commas; Scope reads "in scope" or "out of scope
// (<decision>)" with a decision of closure.ScopeDecisions that covers the row.
package variants

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/sandboxstatus"
)

// The headings of the two tables, and the stage the fleet serves.
const (
	VariantsHeading = "## Variants"
	RemovalsHeading = "## Owner removals"
	Stage           = "DH-S-18"
	InScope         = "in scope"
	// MacOSScope is the scope of every macOS row (owner decision M1 OWNER-1).
	MacOSScope = "out of scope (platforms-windows-wsl)"
)

// Path is the variants file of story, relative to the repository root.
func Path(story string) string { return closure.EvidenceDir(story) + "/variants.md" }

// Services are the service names a variant may give (architecture.md, provided
// services and gate-host services tables).
var Services = []string{
	"service:filesystem", "service:os-process", "service:go-toolchain",
	"service:model-route-claude-code", "service:model-route-copilot",
	"service:linux-sandbox-bwrap", "service:linux-landlock-only", "service:linux-unconfined",
	"service:linux-systemd-cgroup", "service:windows-host", "service:git-and-editor",
	"service:vm-power-cut", "service:ack-recorder",
}

var (
	labelPattern    = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*(:[a-z0-9]+(-[a-z0-9]+)*)?$`)
	hostedRunners   = []string{"ubuntu-latest", "windows-latest"}
	selfHostedLabel = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// Variant is one row of the Variants table.
type Variant struct {
	Line      int
	Label     string
	Platform  string
	Runner    string
	Provider  string
	Services  []string
	Preflight string
	Scenarios []string
	Scope     string
}

// InScope reports whether the variant must run.
func (v Variant) InScope() bool { return v.Scope == InScope }

// RunsOn is the runner of the variant as the JSON value of a job's runs-on: a
// hosted image name, or the self-hosted labels as an array.
func (v Variant) RunsOn() string {
	if slices.Contains(hostedRunners, v.Runner) {
		return `"` + v.Runner + `"`
	}
	return `["` + strings.Join(splitList(v.Runner, ","), `","`) + `"]`
}

// Removal is one row of the Owner removals table: a scenario taken out of S18 by
// an owner decision.
type Removal struct {
	Line     int
	Scenario string
	Decision string
}

// Fleet is the parsed variants file.
type Fleet struct {
	Path     string
	Variants []Variant
	Removals []Removal
}

// Read reads the variants file of story under root.
func Read(root, story string) (*Fleet, error) {
	rel := Path(story)
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	defer f.Close()
	return Parse(rel, f)
}

// Parse reads a variants file. It fails on a table it cannot read or one that is
// missing or lacks a column; every content problem is reported by Check.
func Parse(path string, r io.Reader) (*Fleet, error) {
	rec, err := closure.ParseDocument(path, r, VariantsHeading, RemovalsHeading)
	if err != nil {
		return nil, err
	}
	need := map[string][]string{
		VariantsHeading: {"variant", "platform", "runner", "provider", "service", "preflight", "scenarios", "scope"},
		RemovalsHeading: {"scenario", "decision"},
	}
	for _, h := range []string{VariantsHeading, RemovalsHeading} {
		t := rec.Extra[h]
		if !t.Present {
			return nil, fmt.Errorf("%s: no %q table", path, h)
		}
		for _, c := range need[h] {
			if !t.Has(c) {
				return nil, fmt.Errorf("%s: the %q table has no %q column", path, h, c)
			}
		}
	}
	f := &Fleet{Path: path}
	for _, row := range rec.Extra[VariantsHeading].Rows {
		var services []string
		for _, s := range splitList(row.Get("service"), "+") {
			services = append(services, strings.Trim(s, "`"))
		}
		f.Variants = append(f.Variants, Variant{
			Line:      row.Line,
			Label:     row.Get("variant"),
			Platform:  row.Get("platform"),
			Runner:    row.Get("runner"),
			Provider:  row.Get("provider"),
			Services:  services,
			Preflight: row.Get("preflight"),
			Scenarios: splitList(row.Get("scenarios"), ","),
			Scope:     row.Get("scope"),
		})
	}
	for _, row := range rec.Extra[RemovalsHeading].Rows {
		f.Removals = append(f.Removals, Removal{Line: row.Line, Scenario: row.Get("scenario"), Decision: row.Get("decision")})
	}
	return f, nil
}

// Variant returns the variant labelled label.
func (f *Fleet) Variant(label string) (Variant, bool) {
	i := slices.IndexFunc(f.Variants, func(v Variant) bool { return v.Label == label })
	if i < 0 {
		return Variant{}, false
	}
	return f.Variants[i], true
}

// Check cross-references the fleet with stage's entry of the index and returns
// every problem, each naming its line: an empty result means every scenario of
// the entry has an in-scope variant or an owner removal, and every row is well
// formed.
func (f *Fleet) Check(x *acceptance.Index, stage string) []string {
	var problems []string
	add := func(line int, format string, a ...any) {
		problems = append(problems, fmt.Sprintf("%s:%d: %s", f.Path, line, fmt.Sprintf(format, a...)))
	}
	ids, err := x.Required(stage)
	if err != nil {
		return []string{err.Error()}
	}
	if len(ids) == 0 {
		return []string{fmt.Sprintf("stage %s lists no scenario in %s", stage, acceptance.IndexPath)}
	}
	seen := map[string]int{}
	for _, v := range f.Variants {
		if !labelPattern.MatchString(v.Label) {
			add(v.Line, "variant %q is not a label like linux-e2:landlock", v.Label)
		}
		if prev, dup := seen[v.Label]; dup {
			add(v.Line, "variant %s given twice (first at line %d)", v.Label, prev)
		}
		seen[v.Label] = v.Line
		if v.Platform == "" {
			add(v.Line, "variant %s names no platform", v.Label)
		}
		if len(v.Scenarios) == 0 {
			add(v.Line, "variant %s lists no scenario", v.Label)
		}
		for _, id := range v.Scenarios {
			if !slices.Contains(ids, id) {
				add(v.Line, "variant %s lists %s, which is not in the %s entry of %s", v.Label, id, stage, acceptance.IndexPath)
			}
		}
		if isMacOS(v) && v.Scope != MacOSScope {
			add(v.Line, "variant %s is a macOS row; it must read %q (M2-EN-2)", v.Label, MacOSScope)
			continue
		}
		if v.InScope() {
			for _, p := range checkInScope(v) {
				add(v.Line, "variant %s: %s", v.Label, p)
			}
			continue
		}
		if p := checkOutOfScope(v); p != "" {
			add(v.Line, "variant %s: %s", v.Label, p)
		}
	}
	removed := map[string]bool{}
	for _, r := range f.Removals {
		if p := checkRemoval(r, ids); p != "" {
			add(r.Line, "%s", p)
			continue
		}
		removed[r.Scenario] = true
	}
	for _, id := range ids {
		running := f.inScopeFor(id)
		switch {
		case len(running) == 0 && !removed[id]:
			problems = append(problems, fmt.Sprintf("%s: %s of %s has no in-scope variant and no owner decision removing it (M2-DOD-4)", f.Path, id, stage))
		case len(running) > 0 && removed[id]:
			problems = append(problems, fmt.Sprintf("%s: %s is removed by an owner decision but still runs on %s", f.Path, id, strings.Join(running, ", ")))
		}
	}
	return problems
}

func isMacOS(v Variant) bool {
	return strings.Contains(strings.ToLower(v.Label+" "+v.Platform), "macos")
}

func checkInScope(v Variant) []string {
	var p []string
	if !validRunner(v.Runner) {
		p = append(p, fmt.Sprintf("runner %q is not ubuntu-latest, windows-latest or self-hosted, <linux|windows>, <label>", v.Runner))
	}
	if v.Provider == "" {
		p = append(p, "names no provider (Q m2-e2-fleet)")
	}
	if len(v.Services) == 0 {
		p = append(p, "names no service:* (architecture.md provided services)")
	}
	for _, s := range v.Services {
		if !slices.Contains(Services, s) {
			p = append(p, fmt.Sprintf("service %q is not a service of architecture.md", s))
		}
		if e, ok := sandboxstatus.ExpectationOf(s); ok && v.Preflight != e.Text {
			p = append(p, fmt.Sprintf("names %s, whose preflight is %q, not %q", s, e.Text, v.Preflight))
		}
	}
	if v.Preflight == "" {
		p = append(p, "names no preflight")
	}
	return p
}

func checkOutOfScope(v Variant) string {
	named, ok := strings.CutPrefix(v.Scope, "out of scope (")
	if !ok || !strings.HasSuffix(named, ")") {
		return fmt.Sprintf("scope %q is neither %q nor \"out of scope (<decision>)\"", v.Scope, InScope)
	}
	id := strings.Trim(strings.TrimSpace(strings.TrimSuffix(named, ")")), "`")
	for _, d := range closure.ScopeDecisions {
		if !strings.EqualFold(d.ID, id) {
			continue
		}
		for _, s := range v.Scenarios {
			if !d.Applies(s, v.Platform) {
				return fmt.Sprintf("decision %q does not put %s on %q out of scope (M2-C-2)", d.ID, s, v.Platform)
			}
		}
		return ""
	}
	return fmt.Sprintf("scope names unknown decision %q (M2-C-2)", id)
}

// checkRemoval accepts a removal only when a known decision covers the whole
// scenario, on every platform; no decision does today.
func checkRemoval(r Removal, ids []string) string {
	if !slices.Contains(ids, r.Scenario) {
		return fmt.Sprintf("removal of %s, which is not an S18 scenario", r.Scenario)
	}
	id := strings.Trim(r.Decision, "`")
	for _, d := range closure.ScopeDecisions {
		if strings.EqualFold(d.ID, id) && d.Applies(r.Scenario, "") {
			return ""
		}
	}
	return fmt.Sprintf("removal of %s names %q, which is not an owner decision removing that scenario (M2-DOD-4)", r.Scenario, r.Decision)
}

func validRunner(r string) bool {
	if slices.Contains(hostedRunners, r) {
		return true
	}
	labels := splitList(r, ",")
	if len(labels) < 3 || labels[0] != "self-hosted" || (labels[1] != "linux" && labels[1] != "windows") {
		return false
	}
	for _, l := range labels[2:] {
		if !selfHostedLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// inScopeFor returns the in-scope variants listing id.
func (f *Fleet) inScopeFor(id string) []string {
	var out []string
	for _, v := range f.Variants {
		if v.InScope() && slices.Contains(v.Scenarios, id) {
			out = append(out, v.Label)
		}
	}
	return out
}

// Require returns the scenario IDs of stage's entry that variant label runs, in
// index order: the DSH_REQUIRE_SCENARIOS of its s18 job. The fleet must pass
// Check first; an out-of-scope or unknown variant is an error.
func (f *Fleet) Require(x *acceptance.Index, stage, label string) ([]string, error) {
	if p := f.Check(x, stage); len(p) > 0 {
		return nil, fmt.Errorf("the variant list does not cover %s:\n  %s", stage, strings.Join(p, "\n  "))
	}
	v, ok := f.Variant(label)
	if !ok {
		return nil, fmt.Errorf("%s lists no variant %q", f.Path, label)
	}
	if !v.InScope() {
		return nil, fmt.Errorf("variant %s is %s; it runs no scenario", label, v.Scope)
	}
	ids, _ := x.Required(stage)
	var out []string
	for _, id := range ids {
		if slices.Contains(v.Scenarios, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Coverage renders, per scenario of stage in index order, the variants that run
// it and those out of scope with their decision: the golden of the cross-reference.
func (f *Fleet) Coverage(x *acceptance.Index, stage string) string {
	ids, _ := x.Required(stage)
	in := 0
	for _, v := range f.Variants {
		if v.InScope() {
			in++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d scenarios, %d variants (%d in scope, %d out of scope), %d owner removals\n",
		stage, len(ids), len(f.Variants), in, len(f.Variants)-in, len(f.Removals))
	for _, id := range ids {
		var out []string
		for _, v := range f.Variants {
			if !v.InScope() && slices.Contains(v.Scenarios, id) {
				out = append(out, v.Label+" "+v.Scope)
			}
		}
		fmt.Fprintf(&b, "%s\n  runs on: %s\n", id, orNone(f.inScopeFor(id)))
		if len(out) > 0 {
			fmt.Fprintf(&b, "  not run: %s\n", strings.Join(out, "; "))
		}
	}
	for _, r := range f.Removals {
		fmt.Fprintf(&b, "removed: %s (%s)\n", r.Scenario, r.Decision)
	}
	return b.String()
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func splitList(s, sep string) []string {
	var out []string
	for _, item := range strings.Split(s, sep) {
		if item = strings.TrimSpace(item); item != "" && item != "-" {
			out = append(out, item)
		}
	}
	return out
}
