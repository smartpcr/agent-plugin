package closure

import (
	"bufio"
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// Milestone is the rule under which a story's closure record may declare its milestone
// reached (impl.md sec. 3.3, the milestone's row; implementation-plan Stage 9.2).
type Milestone struct {
	Name         string   // "M2": the closure record's line reads "<Name> reached: yes|no"
	Closed       []string // the stages whose records must read closed and pass close
	GuardedStage string   // the build stage whose record holds the guarded rows
	Guarded      []string // the guarded scenarios that must have passed there
}

// DeclarationPrefix is the start of the closure record's declaration line.
func (m Milestone) DeclarationPrefix() string { return m.Name + " reached:" }

// BuiltThrough is the built-through a reached milestone advances the index to: the last
// stage it closes, by the index rule that a stage raises built-through when it closes.
func (m Milestone) BuiltThrough() string {
	last, best := "", -1
	for _, st := range m.Closed {
		if n, ok := acceptance.StageNumber(st); ok && n > best {
			last, best = st, n
		}
	}
	return last
}

// Check is the milestone predicate: every stage of m.Closed has a record that reads
// closed and passes close in its mode, m.GuardedStage's record has every scenario row
// its Obligations owe (each platform's row), and every guarded scenario has a passed
// row of venue guarded there (with its evidence), as do the scenarios the index lists
// under that stage. It returns the built-through the milestone advances to
// when it is reached, or the problems that refuse it. records holds the story's stage
// records by stage ID; a stage with no record is neither closed nor guarded.
func (s Story) Check(x *acceptance.Index, records map[string]*Record) (string, []string) {
	m := s.Milestone
	if m.Name == "" {
		return "", []string{s.ID + " has no milestone rule"}
	}
	var out []string
	for _, stage := range m.Closed {
		rec := records[stage]
		if rec == nil {
			out = append(out, fmt.Sprintf("%s refused: %s has no evidence record %s", m.Name, stage, RecordPath(s.ID, stage)))
			continue
		}
		built, _ := s.Built(stage)
		if refusal, b := s.Close(x, stage, rec, built); refusal != "" {
			out = append(out, fmt.Sprintf("%s refused: %s is not closed (%s): %s", m.Name, stage, refusal, summarize(b)))
		}
	}
	if m.GuardedStage != "" {
		entry := acceptance.Stage{ID: m.GuardedStage}
		for _, id := range m.Guarded {
			entry.Scenarios = append(entry.Scenarios, acceptance.Entry{ID: id})
		}
		if listed, ok := x.Stage(m.GuardedStage); ok {
			for _, e := range listed.Scenarios {
				if !slices.Contains(m.Guarded, e.ID) {
					entry.Scenarios = append(entry.Scenarios, e)
				}
			}
		}
		if rec := records[m.GuardedStage]; rec == nil {
			out = append(out, fmt.Sprintf("%s refused: the guarded scenarios have no evidence record %s", m.Name, RecordPath(s.ID, m.GuardedStage)))
		} else {
			// The owed scenario rows (one per scenario and platform) as well as coverage by
			// scenario ID: a deleted Windows row is not hidden by its passed Linux row. Only
			// the scenario rows are read (built false), not the stage's build tables.
			blockers := Obligations{Scenarios: s.Obligations[m.GuardedStage].Scenarios}.Missing(rec, false)
			blockers = append(blockers, scenarioBlockers(entry, rec, true)...)
			for _, b := range blockers {
				out = append(out, fmt.Sprintf("%s refused: guarded scenarios of %s: %s", m.Name, m.GuardedStage, b))
			}
		}
	}
	if len(out) > 0 {
		return "", out
	}
	return m.BuiltThrough(), nil
}

// Declaration is a closure record's milestone declaration line.
type Declaration struct {
	Value string // "yes" or "no" when valid; the raw value otherwise
	Line  int    // 0 when there is no line
	Count int    // the number of declaration lines outside code fences
}

// ReadDeclaration finds m's declaration line in a closure record's source, outside code
// fences.
func (m Milestone) ReadDeclaration(src []byte) Declaration {
	var d Declaration
	prefix := m.DeclarationPrefix()
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	inFence := false
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		v, ok := strings.CutPrefix(line, prefix)
		if inFence || !ok {
			continue
		}
		d.Count++
		if d.Count == 1 {
			// The line is prose: one closing full stop is not part of the value.
			d.Value, d.Line = strings.TrimSuffix(strings.TrimSpace(v), "."), n
		}
	}
	return d
}

// DeclarationProblems checks the closure record's declaration: exactly one line reading
// yes or no; yes only while the milestone predicate holds and the index's built-through
// has reached the stage the milestone advances it to. No is always allowed: declaring is
// the closing step, never forced.
func (s Story) DeclarationProblems(path string, src []byte, x *acceptance.Index, records map[string]*Record) []string {
	m := s.Milestone
	if m.Name == "" {
		return nil
	}
	d := m.ReadDeclaration(src)
	switch {
	case d.Count == 0:
		return []string{fmt.Sprintf("%s: no %q line (yes or no)", path, m.DeclarationPrefix())}
	case d.Count > 1:
		return []string{fmt.Sprintf("%s: %d %q lines; want exactly one", path, d.Count, m.DeclarationPrefix())}
	case d.Value == "no":
		return nil
	case d.Value != "yes":
		return []string{fmt.Sprintf("%s:%d: %s %q; want yes or no", path, d.Line, m.DeclarationPrefix(), d.Value)}
	}
	through, problems := s.Check(x, records)
	var out []string
	for _, p := range problems {
		out = append(out, fmt.Sprintf("%s:%d: declares %s reached, but %s", path, d.Line, m.Name, p))
	}
	if through != "" {
		have, okHave := acceptance.StageNumber(x.BuiltThrough)
		want, _ := acceptance.StageNumber(through)
		if !okHave || have < want {
			out = append(out, fmt.Sprintf("%s:%d: declares %s reached, but built-through in %s reads %q; the milestone advances it to %s", path, d.Line, m.Name, acceptance.IndexPath, x.BuiltThrough, through))
		}
	}
	return out
}
