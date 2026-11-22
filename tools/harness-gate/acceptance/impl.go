package acceptance

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ImplPath is the implementation plan, relative to the repository root, and
// LedgerPath the trace ledger whose scenario rows carry the proves lists.
const (
	ImplPath   = "docs/harness/impl.md"
	LedgerPath = "docs/harness/trace/ledger.jsonl"
)

// specDir is the directory the plan's relative links resolve against.
const specDir = "docs/harness"

// ImplStep is one step of a stage: "- [x] **8.3** [DH-D-VIEW-3](areas/...) ...".
type ImplStep struct {
	ID     string // "8.3"
	Stage  string // "DH-S-8"
	Ticked bool
	Text   string // the step's line and its indented continuation lines
	Line   int
	// Deliverables are the step's links to a deliverable anchor (dh-d-...), in
	// order; the first is the deliverable the step builds.
	Deliverables []DeliverableLink
}

// DeliverableLink is a link from a step to a deliverable of a specification.
type DeliverableLink struct {
	ID     string // DH-D-VIEW-3
	File   string // slash path relative to docs/harness, e.g. areas/session-views.md
	Anchor string // dh-d-view-3
}

// ImplStage is one "### DH-S-n:" section of impl.md.
type ImplStage struct {
	ID     string
	Paired string // "Paired acceptance stage: [DH-S-n]"; empty for an acceptance stage
	Steps  []ImplStep
	Text   string // the whole section, heading included
	Line   int
}

// Placement is one row of impl.md sec. 7.5: the stage a scenario runs in.
type Placement struct {
	Stage     string
	Scenarios []string
}

// ImplPlan is the parsed implementation plan.
type ImplPlan struct {
	Stages     []ImplStage
	Placements []Placement
}

// Stage returns the section of stage id.
func (p *ImplPlan) Stage(id string) (*ImplStage, bool) {
	for i := range p.Stages {
		if p.Stages[i].ID == id {
			return &p.Stages[i], true
		}
	}
	return nil, false
}

// DeliverableStage returns the stage whose step first builds deliverable id (the
// step's first deliverable link), and false when no step builds it.
func (p *ImplPlan) DeliverableStage(id string) (string, bool) {
	for _, s := range p.Stages {
		for _, st := range s.Steps {
			if len(st.Deliverables) > 0 && st.Deliverables[0].ID == id {
				return s.ID, true
			}
		}
	}
	return "", false
}

var (
	stageHeading   = regexp.MustCompile(`^### (DH-S-[1-9][0-9]*):`)
	stepLine       = regexp.MustCompile(`^- \[([ xX])\] \*\*([0-9]+\.[0-9]+[a-z]?)\*\*`)
	pairedPattern  = regexp.MustCompile(`Paired acceptance stage: \[(DH-S-[1-9][0-9]*)\]`)
	deliverableRef = regexp.MustCompile(`\]\(([^)#\s]+\.md)#(dh-d-[a-z0-9]+-[0-9]+)\)`)
	placementRow   = regexp.MustCompile(`^\| \[(DH-S-[1-9][0-9]*)\]\([^)]*\)[^|]*\|\s*[0-9]+\s*\|(.*)\|\s*$`)
	scenarioRef    = regexp.MustCompile(`\[(DH-E-[A-Z][A-Z0-9]*-[1-9][0-9]*)\]`)
)

// ReadImpl reads and parses the implementation plan of the repository at root.
func ReadImpl(root string) (*ImplPlan, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(ImplPath)))
	if err != nil {
		return nil, fmt.Errorf("%s not found under %s: %w", ImplPath, root, err)
	}
	defer f.Close()
	p, err := ParseImpl(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ImplPath, err)
	}
	return p, nil
}

// ParseImpl parses the stage sections (their steps, ticks, deliverable links and
// paired acceptance stage) and the scenario index of sec. 7.5. A stage section runs
// from its "### DH-S-n:" heading to the next heading of level 2 or 3.
func ParseImpl(r io.Reader) (*ImplPlan, error) {
	p := &ImplPlan{}
	var (
		stage   *ImplStage
		text    strings.Builder
		step    *ImplStep
		inIndex bool
		lineNo  int
	)
	closeStage := func() {
		if stage != nil {
			stage.Text = text.String()
			p.Stages = append(p.Stages, *stage)
		}
		stage, step = nil, nil
		text.Reset()
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			closeStage()
			inIndex = strings.HasPrefix(line, "### 7.5 ")
			if m := stageHeading.FindStringSubmatch(line); m != nil {
				if _, dup := p.Stage(m[1]); dup {
					return nil, fmt.Errorf("line %d: stage %s has two sections", lineNo, m[1])
				}
				stage = &ImplStage{ID: m[1], Line: lineNo}
			}
		}
		if inIndex {
			if m := placementRow.FindStringSubmatch(line); m != nil {
				var ids []string
				for _, s := range scenarioRef.FindAllStringSubmatch(m[2], -1) {
					ids = append(ids, s[1])
				}
				p.Placements = append(p.Placements, Placement{Stage: m[1], Scenarios: ids})
			}
			continue
		}
		if stage == nil {
			continue
		}
		text.WriteString(line)
		text.WriteByte('\n')
		if stage.Paired == "" {
			if m := pairedPattern.FindStringSubmatch(line); m != nil {
				stage.Paired = m[1]
			}
		}
		if m := stepLine.FindStringSubmatch(line); m != nil {
			stage.Steps = append(stage.Steps, ImplStep{ID: m[2], Stage: stage.ID, Ticked: m[1] != " ", Text: line, Line: lineNo})
			step = &stage.Steps[len(stage.Steps)-1]
		} else if step != nil && strings.TrimSpace(line) != "" && (line[0] == ' ' || line[0] == '\t') {
			step.Text += "\n" + line
		} else {
			step = nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	closeStage()
	for i := range p.Stages {
		for j := range p.Stages[i].Steps {
			st := &p.Stages[i].Steps[j]
			for _, m := range deliverableRef.FindAllStringSubmatch(st.Text, -1) {
				st.Deliverables = append(st.Deliverables, DeliverableLink{ID: strings.ToUpper(m[2]), File: m[1], Anchor: m[2]})
			}
		}
	}
	if len(p.Stages) == 0 {
		return nil, errors.New("no \"### DH-S-n:\" stage section")
	}
	return p, nil
}

// CheckBuiltThrough requires the index's built-through marker to equal the largest
// n for which every step of DH-S-1..n is ticked in impl.md (M1-G-CLOSE-3). A marker
// ahead of the ticks names the first unticked step at or before it; a marker behind
// them names the stage it should be raised to.
func CheckBuiltThrough(x *Index, p *ImplPlan) error {
	marker, ok := StageNumber(x.BuiltThrough)
	if !ok {
		return fmt.Errorf("built-through %q is not a stage ID", x.BuiltThrough)
	}
	largest := 0
	var first string // the first reason the run of ticked stages stops
	for n := 1; ; n++ {
		id := fmt.Sprintf("DH-S-%d", n)
		s, ok := p.Stage(id)
		if !ok {
			first = fmt.Sprintf("%s has no section in %s", id, ImplPath)
			break
		}
		if len(s.Steps) == 0 {
			first = fmt.Sprintf("%s has no steps in %s", id, ImplPath)
			break
		}
		if i := slices.IndexFunc(s.Steps, func(st ImplStep) bool { return !st.Ticked }); i >= 0 {
			first = fmt.Sprintf("step %s of %s is not ticked (%s line %d)", s.Steps[i].ID, id, ImplPath, s.Steps[i].Line)
			break
		}
		largest = n
	}
	switch {
	case marker > largest:
		return fmt.Errorf("%s says built-through %s, but %s", IndexPath, x.BuiltThrough, first)
	case marker < largest:
		return fmt.Errorf("%s says built-through %s, but every step of DH-S-1..DH-S-%d is ticked in %s: raise the marker to DH-S-%d", IndexPath, x.BuiltThrough, largest, ImplPath, largest)
	}
	return nil
}

// ReadLedgerScenarios returns the proves list of every scenario row of the ledger
// under root, keyed by scenario ID.
func ReadLedgerScenarios(root string) (map[string][]string, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(LedgerPath)))
	if err != nil {
		return nil, fmt.Errorf("%s not found under %s: %w", LedgerPath, root, err)
	}
	defer f.Close()
	out, err := ParseLedgerScenarios(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", LedgerPath, err)
	}
	return out, nil
}

// ParseLedgerScenarios reads the rows of kind "scenario" of a JSON-lines ledger.
func ParseLedgerScenarios(r io.Reader) (map[string][]string, error) {
	out := map[string][]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row struct {
			Kind   string   `json:"kind"`
			Go     string   `json:"go"`
			Proves []string `json:"proves"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if row.Kind == "scenario" {
			out[row.Go] = row.Proves
		}
	}
	return out, sc.Err()
}

// CheckRule4Authoring checks rule 4 of impl.md sec. 1.4 for every built stage k
// (DH-S-1..built-through; M1-G-CLOSE-8): a scenario whose ledger proves list names
// deliverables that are all built by stages up to k, the last of them by k, and that
// impl.md sec. 7.5 places in k's paired acceptance stage, is owed by k and must be
// in the acceptance index. A scenario sec. 7.5 places in a later stage than k's
// paired one is not owed by k, and a scenario whose proves list names no
// deliverable a step builds (a library requirement, a decision) is placed by the
// deliverables that use it and is outside this check.
func CheckRule4Authoring(x *Index, p *ImplPlan, proves map[string][]string) error {
	built, ok := StageNumber(x.BuiltThrough)
	if !ok {
		return fmt.Errorf("built-through %q is not a stage ID", x.BuiltThrough)
	}
	indexed := map[string]bool{}
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			indexed[e.ID] = true
		}
	}
	var errs []error
	for _, pl := range p.Placements {
		for _, id := range pl.Scenarios {
			owner, ok := ownerStage(p, proves[id])
			if !ok {
				continue
			}
			n, _ := StageNumber(owner)
			if n > built {
				continue
			}
			s, _ := p.Stage(owner)
			if s.Paired != pl.Stage || indexed[id] {
				continue
			}
			errs = append(errs, fmt.Errorf("%s is owed by %s (rule 4): its proves list (%s) is built through %s, %s sec. 7.5 places it in %s, the paired acceptance stage of %s, and %s does not index it",
				id, owner, strings.Join(proves[id], ", "), owner, ImplPath, pl.Stage, owner, IndexPath))
		}
	}
	return errors.Join(errs...)
}

// ownerStage returns the last stage that builds a deliverable of proves, and false
// when proves names no deliverable or one that no step builds.
func ownerStage(p *ImplPlan, proves []string) (string, bool) {
	owner, last := "", 0
	for _, d := range proves {
		if !strings.HasPrefix(d, "DH-D-") {
			continue
		}
		s, ok := p.DeliverableStage(d)
		if !ok {
			return "", false
		}
		if n, _ := StageNumber(s); n > last {
			owner, last = s, n
		}
	}
	return owner, owner != ""
}
