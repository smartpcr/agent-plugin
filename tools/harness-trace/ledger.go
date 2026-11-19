package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Row is one line of a fragment. A row does one of three things: gives an upstream
// unit its disposition ("unit"), declares an upstream unit the inventory could not find
// mechanically ("define"), or declares a Go-side item ("go").
type Row struct {
	Unit          string   `json:"unit,omitempty"`
	Disposition   string   `json:"disposition,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Links         []string `json:"links,omitempty"`
	Spec          string   `json:"spec,omitempty"`
	Justification string   `json:"justification,omitempty"`
	Stage         string   `json:"stage,omitempty"`
	Class         string   `json:"class,omitempty"` // carried debt only: platform-limit, scope-boundary or unresolved
	Area          string   `json:"area,omitempty"`    // decision agents: the upstream path the decision is about
	Summary       string   `json:"summary,omitempty"` // decision agents: the decision in one or two sentences

	Define   string `json:"define,omitempty"`
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Evidence string `json:"evidence,omitempty"`

	Go       string   `json:"go,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Outcomes []string `json:"outcomes,omitempty"`
	Proof    []string `json:"proof,omitempty"`
	Proves   []string `json:"proves,omitempty"`
	Deps     []string `json:"deps,omitempty"`
	Serves   []string `json:"serves,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Choice   string   `json:"choice,omitempty"`
	Module   string   `json:"module,omitempty"`
	Version  string   `json:"version,omitempty"`
	License  string   `json:"license,omitempty"`
	Verified string   `json:"verified,omitempty"`

	Agent string `json:"agent,omitempty"`
	Wave  int    `json:"wave,omitempty"`
}

// Ledger is the merged view of every fragment.
type Ledger struct {
	Units   map[string]Row // winning disposition per upstream unit
	Go      map[string]Row // Go-side items by ID
	Defined map[string]Row // upstream units declared by agents
}

const leadKey, leadWave = "LEAD", 99

var (
	dispositionsUnit = map[string]bool{"ported": true, "replaced": true, "merged": true, "dropped": true, "not-applicable": true}
	dispositionsDebt = map[string]bool{"resolved": true, "not-applicable": true, "carried": true}
	goKinds          = map[string]bool{"deliverable": true, "test": true, "scenario": true, "decision": true, "library": true}
	libChoices       = map[string]bool{"stdlib": true, "module": true, "own": true, "dropped": true, "toolchain": true, "open": true}
	debtClasses      = map[string]bool{"platform-limit": true, "scope-boundary": true, "unresolved": true}
	reGoID           = regexp.MustCompile(`^DH-(?:O-\d+|S-\d+|ADR-(?:[A-Z0-9]+-)?\d+|[DTE]-[A-Z0-9]+-\d+|LIB-[A-Z0-9]+(?:-R\d+)?)$`)
	reAnchor         = regexp.MustCompile(`<a id="([^"]+)">`)
	reMdLink         = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	reStageHead      = regexp.MustCompile(`^#{2,4} .*\b(DH-S-\d+)\b`)
	reChecklist      = regexp.MustCompile(`^\s*- \[[ xX]\] `)
)

func unitType(id string) string {
	parts := strings.SplitN(id, "-", 3)
	if len(parts) < 3 || parts[0] != "UP" {
		return ""
	}
	return parts[1]
}

// runMerge reads every fragment and resolves one winning row per upstream unit. A row
// from a later wave overrides a row from an earlier wave, which is how an area
// specification refines a foundation agent's first classification.
func runMerge(c config, write bool) (*Ledger, []string, error) {
	cl, err := loadClusters(c)
	if err != nil {
		return nil, nil, err
	}
	led := &Ledger{Units: map[string]Row{}, Go: map[string]Row{}, Defined: map[string]Row{}}
	var problems []string
	files, _ := filepath.Glob(filepath.Join(c.trace, "fragments", "*.jsonl"))
	sort.Strings(files)
	for _, f := range files {
		key := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		wave := leadWave
		if key != leadKey {
			a := cl.agent(key)
			if a == nil {
				problems = append(problems, fmt.Sprintf("fragment %s does not belong to a known agent", filepath.Base(f)))
				continue
			}
			wave = a.Wave
		}
		rows, bad, err := readJSONL[Row](f)
		if err != nil {
			return nil, nil, err
		}
		for _, b := range bad {
			problems = append(problems, "unparseable row "+b)
		}
		for _, r := range rows {
			r.Agent, r.Wave = key, wave
			used := false
			if r.Define != "" {
				used = true
				if prev, dup := led.Defined[r.Define]; dup && prev.Agent != key {
					problems = append(problems, fmt.Sprintf("unit %s is defined by %s and %s", r.Define, prev.Agent, key))
				}
				led.Defined[r.Define] = r
				if r.Disposition != "" && r.Unit == "" {
					r.Unit = r.Define
				}
			}
			if r.Unit != "" {
				used = true
				prev, has := led.Units[r.Unit]
				switch {
				case !has || r.Wave > prev.Wave:
					led.Units[r.Unit] = r
				case r.Wave == prev.Wave && prev.Agent != key:
					problems = append(problems, fmt.Sprintf("unit %s has rows from %s and %s in the same wave", r.Unit, prev.Agent, key))
				case r.Wave == prev.Wave:
					problems = append(problems, fmt.Sprintf("unit %s has two rows in %s", r.Unit, filepath.Base(f)))
					led.Units[r.Unit] = r
				}
			}
			if r.Go != "" {
				used = true
				if prev, dup := led.Go[r.Go]; dup {
					problems = append(problems, fmt.Sprintf("Go item %s is declared twice (%s, %s)", r.Go, prev.Agent, key))
				}
				led.Go[r.Go] = r
			}
			if !used {
				problems = append(problems, fmt.Sprintf("%s: row has none of unit, define, go", filepath.Base(f)))
			}
		}
	}
	if write {
		var out []Row
		for _, k := range sortedKeys(led.Defined) {
			d := led.Defined[k]
			out = append(out, Row{Define: d.Define, Type: d.Type, Title: d.Title, Evidence: d.Evidence, Agent: d.Agent, Wave: d.Wave})
		}
		for _, k := range sortedKeys(led.Units) {
			r := led.Units[k]
			r.Define, r.Type, r.Title, r.Evidence, r.Go = "", "", "", "", ""
			out = append(out, r)
		}
		for _, k := range sortedKeys(led.Go) {
			r := led.Go[k]
			r.Unit, r.Define, r.Disposition = "", "", ""
			out = append(out, r)
		}
		if err := writeJSONL(filepath.Join(c.trace, "ledger.jsonl"), out); err != nil {
			return nil, nil, err
		}
		fmt.Printf("merged %d fragments: %d unit rows, %d Go items, %d agent-defined units, %d problems\n",
			len(files), len(led.Units), len(led.Go), len(led.Defined), len(problems))
	}
	return led, problems, nil
}

// docSet is every Markdown document under docs/harness that is checked.
type docSet struct {
	text    map[string]string          // relative path -> content
	anchors map[string]map[string]bool // relative path -> anchor ids
	all     map[string]bool            // every anchor id, lower case
}

func loadDocs(c config) (*docSet, error) {
	ds := &docSet{text: map[string]string{}, anchors: map[string]map[string]bool{}, all: map[string]bool{}}
	err := filepath.WalkDir(c.docs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || strings.HasPrefix(d.Name(), "_") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(c.docs, p)
		rel = slash(rel)
		ds.text[rel] = string(b)
		ds.anchors[rel] = map[string]bool{}
		for _, m := range reAnchor.FindAllStringSubmatch(string(b), -1) {
			ds.anchors[rel][strings.ToLower(m[1])] = true
			ds.all[strings.ToLower(m[1])] = true
		}
		return nil
	})
	return ds, err
}

// mentioned reports whether an ID appears as a whole token in any document.
func (ds *docSet) mentioned(id string) bool {
	for _, t := range ds.text {
		if containsID(t, id) {
			return true
		}
	}
	return false
}

func containsID(text, id string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], id)
		if i < 0 {
			return false
		}
		end := from + i + len(id)
		if end == len(text) || !isIDChar(text[end]) {
			return true
		}
		from = end
	}
}

func isIDChar(b byte) bool {
	return b == '-' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

type report struct {
	fails map[string][]string
	warns map[string][]string
	order []string
}

func (r *report) fail(cat, msg string) {
	if _, ok := r.fails[cat]; !ok {
		r.order = append(r.order, cat)
	}
	r.fails[cat] = append(r.fails[cat], msg)
}

func (r *report) warn(cat, msg string) { r.warns[cat] = append(r.warns[cat], msg) }

// softWhilePartial are the checks that can only pass once every wave has finished.
var softWhilePartial = map[string]bool{
	"unit has no disposition": true, "deliverable is not scheduled in a stage": true,
	"outcome has no deliverable": true, "stage problems": true,
}

func runVerify(c config, partial bool) (bool, error) {
	cl, err := loadClusters(c)
	if err != nil {
		return false, err
	}
	units, err := loadInventory(c)
	if err != nil {
		return false, err
	}
	led, problems, err := runMerge(c, false)
	if err != nil {
		return false, err
	}
	ds, err := loadDocs(c)
	if err != nil {
		return false, err
	}
	owner, unassigned, err := assignUnits(cl, units)
	if err != nil {
		return false, err
	}
	rep := &report{fails: map[string][]string{}, warns: map[string][]string{}}
	for _, p := range problems {
		rep.fail("fragment problems", p)
	}
	for _, id := range unassigned {
		rep.fail("unit has no owner", id)
	}

	known := map[string]Unit{}
	for _, u := range units {
		known[u.ID] = u
	}
	for id, d := range led.Defined {
		t := d.Type
		if (t != "DEBT" && t != "DEC") || !strings.HasPrefix(id, "UP-"+t+"-found."+d.Agent+".") {
			rep.fail("bad agent-defined unit", fmt.Sprintf("%s (%s): must be UP-DEBT-found.%s.<n> or UP-DEC-found.%s.<n>", id, d.Agent, d.Agent, d.Agent))
		}
		if strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Evidence) == "" {
			rep.fail("bad agent-defined unit", id+": title and evidence are required")
		}
		known[id] = Unit{ID: id, Type: t, Title: d.Title}
		owner[id] = d.Agent
	}

	defined := func(id string) bool {
		if _, ok := led.Go[id]; ok {
			return true
		}
		if ds.all[strings.ToLower(id)] {
			return true
		}
		return strings.Contains(id, "-R") && strings.HasPrefix(id, "DH-LIB-") && ds.mentioned(id)
	}
	checkRefs := func(where string, ids []string) {
		for _, id := range ids {
			if !reGoID.MatchString(id) {
				rep.fail("malformed ID", where+" -> "+id)
			} else if !defined(id) {
				rep.fail("link to an ID that is not defined", where+" -> "+id)
			}
		}
	}
	specExists := func(where, spec string) {
		file := strings.SplitN(spec, "#", 2)[0]
		if file == "" {
			rep.fail("row has no spec document", where)
		} else if _, ok := ds.text[file]; !ok {
			rep.fail("spec document does not exist", where+" -> "+spec)
		}
	}

	// Every upstream unit carries a disposition that is valid for its type.
	dispCount := map[string]map[string]int{}
	missingBy := map[string]int{}
	for _, id := range sortedKeys(known) {
		u := known[id]
		r, ok := led.Units[id]
		if !ok {
			rep.fail("unit has no disposition", fmt.Sprintf("%s (owner %s)", id, owner[id]))
			missingBy[owner[id]]++
			continue
		}
		if dispCount[u.Type] == nil {
			dispCount[u.Type] = map[string]int{}
		}
		dispCount[u.Type][r.Disposition]++
		where := fmt.Sprintf("%s [%s]", id, r.Agent)
		valid := dispositionsUnit
		if u.Type == "DEBT" {
			valid = dispositionsDebt
		}
		if !valid[r.Disposition] {
			rep.fail("disposition not allowed for the unit type", fmt.Sprintf("%s: %q", where, r.Disposition))
		}
		if len(strings.TrimSpace(r.Reason)) < 20 {
			rep.fail("disposition has no real reason", where)
		}
		specExists(where, r.Spec)
		checkRefs(where, r.Links)
		switch r.Disposition {
		case "ported", "replaced", "merged", "resolved":
			if len(r.Links) == 0 {
				rep.fail("disposition links to no decision, deliverable or library", where)
			}
		case "dropped":
			hasADR := false
			for _, l := range r.Links {
				hasADR = hasADR || strings.HasPrefix(l, "DH-ADR-")
			}
			if !hasADR {
				rep.fail("dropped unit links to no decision record", where)
			}
		case "carried":
			switch {
			case len(strings.TrimSpace(r.Justification)) < 20 || !strings.HasPrefix(r.Stage, "DH-S-") || r.Stage == "DH-S-0":
				rep.fail("carried debt without justification and owning stage", where)
			case !debtClasses[r.Class]:
				rep.fail("carried debt without a class (platform-limit, scope-boundary, unresolved)", fmt.Sprintf("%s: %q", where, r.Class))
			default:
				hasADR := false
				for _, l := range r.Links {
					hasADR = hasADR || strings.HasPrefix(l, "DH-ADR-")
				}
				if r.Class == "scope-boundary" && !hasADR {
					rep.fail("scope-boundary debt links to no decision record", where)
				}
				rep.warn("tech debt carried forward: "+r.Class, where+": "+r.Justification)
			}
		}
	}
	for id, r := range led.Units {
		if _, ok := known[id]; !ok {
			rep.fail("row for a unit that is not in the inventory", fmt.Sprintf("%s [%s]", id, r.Agent))
		}
	}

	// Go-side items are well formed and anchored in their document.
	plan := ds.text["implementation-plan.md"]
	outcomeUsed := map[string]bool{}
	for _, id := range sortedKeys(led.Go) {
		g := led.Go[id]
		where := fmt.Sprintf("%s [%s]", id, g.Agent)
		if !reGoID.MatchString(id) {
			rep.fail("malformed ID", where)
		}
		if !goKinds[g.Kind] {
			rep.fail("Go item has an unknown kind", fmt.Sprintf("%s: %q", where, g.Kind))
		}
		specExists(where, g.Spec)
		file := strings.SplitN(g.Spec, "#", 2)[0]
		if t, ok := ds.text[file]; ok && !containsID(t, id) && !ds.anchors[file][strings.ToLower(id)] {
			rep.fail("Go item is not written in its spec document", where+" -> "+file)
		}
		checkRefs(where, g.Outcomes)
		checkRefs(where, g.Proof)
		checkRefs(where, g.Proves)
		checkRefs(where, g.Deps)
		checkRefs(where, g.Serves)
		switch g.Kind {
		case "deliverable":
			if len(g.Outcomes) == 0 {
				rep.fail("deliverable serves no outcome", where)
			}
			if len(g.Proof) == 0 {
				rep.fail("deliverable has no proof", where)
			}
			for _, o := range g.Outcomes {
				outcomeUsed[o] = true
			}
			if !containsID(plan, id) {
				rep.fail("deliverable is not scheduled in a stage", where)
			}
		case "test", "scenario":
			if len(g.Proves) == 0 {
				rep.fail("test proves nothing", where)
			}
		case "library":
			if !libChoices[g.Choice] {
				rep.fail("library has an unknown choice", fmt.Sprintf("%s: %q", where, g.Choice))
			}
			if g.Choice == "module" && (g.Module == "" || g.Version == "" || g.License == "" || g.Verified == "") {
				rep.fail("library module is not verified (module, version, license, verified)", where)
			}
			if g.Choice == "open" {
				rep.warn("library choice still open", where+": "+g.Title)
			}
		}
	}
	for a := range ds.anchors["design.md"] {
		if id := strings.ToUpper(a); strings.HasPrefix(id, "DH-O-") && !outcomeUsed[id] {
			rep.fail("outcome has no deliverable", id)
		}
	}

	// Stages stay small enough to review.
	if plan == "" {
		rep.fail("stage problems", "implementation-plan.md does not exist")
	} else {
		stage, steps := "", 0
		flush := func() {
			if stage == "" {
				return
			}
			if steps == 0 || steps > 20 {
				rep.fail("stage problems", fmt.Sprintf("%s has %d checklist steps (must be 1 to 20)", stage, steps))
			}
		}
		for _, l := range strings.Split(plan, "\n") {
			if strings.HasPrefix(l, "#") {
				flush()
				stage, steps = "", 0
				if m := reStageHead.FindStringSubmatch(l); m != nil {
					stage = m[1]
				}
			} else if stage != "" && reChecklist.MatchString(l) {
				steps++
			}
		}
		flush()
	}

	// Relative links between documents resolve.
	for _, rel := range sortedKeys(ds.text) {
		for _, m := range reMdLink.FindAllStringSubmatch(ds.text[rel], -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "<") {
				continue
			}
			parts := strings.SplitN(target, "#", 2)
			file := rel
			if parts[0] != "" {
				file = slash(filepath.Join(filepath.Dir(rel), parts[0]))
				if _, err := os.Stat(filepath.Join(c.docs, filepath.FromSlash(file))); err != nil {
					rep.fail("broken link", rel+" -> "+target)
					continue
				}
			}
			if len(parts) == 2 && strings.HasPrefix(strings.ToLower(parts[1]), "dh-") {
				if a, ok := ds.anchors[file]; ok && !a[strings.ToLower(parts[1])] {
					rep.fail("broken link", rel+" -> "+target+" (no such anchor)")
				}
			}
		}
	}

	// Report.
	total, done := len(known), len(known)-len(rep.fails["unit has no disposition"])
	fmt.Printf("units: %d, with a disposition: %d (%.1f%%)\n", total, done, 100*float64(done)/float64(max(total, 1)))
	fmt.Printf("%-8s %6s  %s\n", "type", "units", "dispositions")
	typeTotal := map[string]int{}
	for _, u := range known {
		typeTotal[u.Type]++
	}
	for _, t := range unitTypes {
		var parts []string
		for _, d := range sortedKeys(dispCount[t]) {
			parts = append(parts, fmt.Sprintf("%s=%d", d, dispCount[t][d]))
		}
		fmt.Printf("%-8s %6d  %s\n", t, typeTotal[t], strings.Join(parts, " "))
	}
	kinds := map[string]int{}
	for _, g := range led.Go {
		kinds[g.Kind]++
	}
	fmt.Printf("Go items: %v\n", kinds)
	if len(missingBy) > 0 {
		var parts []string
		for _, k := range sortedKeys(missingBy) {
			parts = append(parts, fmt.Sprintf("%s=%d", k, missingBy[k]))
		}
		fmt.Println("units still open by owner:", strings.Join(parts, " "))
	}
	for _, cat := range sortedKeys(rep.warns) {
		fmt.Printf("WARN %s: %d\n", cat, len(rep.warns[cat]))
		for i, m := range rep.warns[cat] {
			if i == 15 {
				fmt.Printf("  ... and %d more\n", len(rep.warns[cat])-15)
				break
			}
			fmt.Println("  -", m)
		}
	}
	ok := true
	for _, cat := range rep.order {
		soft := partial && softWhilePartial[cat]
		label := "FAIL"
		if soft {
			label = "OPEN"
		} else {
			ok = false
		}
		fmt.Printf("%s %s: %d\n", label, cat, len(rep.fails[cat]))
		limit := 25
		if soft {
			limit = 5
		}
		for i, m := range rep.fails[cat] {
			if i == limit {
				fmt.Printf("  ... and %d more\n", len(rep.fails[cat])-limit)
				break
			}
			fmt.Println("  -", m)
		}
	}
	if ok {
		fmt.Println("verify: PASS")
	} else {
		fmt.Println("verify: FAIL")
	}
	return ok, nil
}
