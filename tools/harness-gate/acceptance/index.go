// Package acceptance keeps the authored real-environment scenarios (DH-E-*) and the
// acceptance index test/harness/acceptance-index.yaml in agreement, and decides the
// preflight of the credentialed scenario workflow (DH-ADR-TOOLCHAIN-6, -12; QG-10).
//
// The index is keyed by the stage that executes a scenario. Each entry lists the
// authored scenario IDs with the stage that authors them:
//
//	built-through: DH-S-1
//	stages:
//	  DH-S-17:
//	    - id: DH-E-TOOLCHAIN-1
//	      authored: DH-S-11
//	  DH-S-18:
//	    - id: DH-E-TOOLCHAIN-2
//	      authored: DH-S-1
//	  DH-S-22: []
//
// built-through is the last stage built at the commit: every indexed scenario
// authored at or before it must have its scenario test. The file is read by a strict
// line parser that accepts exactly this shape (comments and blank lines aside), so
// the gate needs no YAML dependency.
package acceptance

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// IndexPath is the acceptance index, relative to the repository root.
const IndexPath = "test/harness/acceptance-index.yaml"

var (
	stagePattern    = regexp.MustCompile(`^DH-S-([1-9][0-9]*)$`)
	scenarioPattern = regexp.MustCompile(`^DH-E-[A-Z][A-Z0-9]*-[1-9][0-9]*$`)
)

// Entry is one indexed scenario: its ID and the stage that authors its test.
type Entry struct {
	ID       string
	Authored string
}

// Stage is one executing stage of the index and the scenarios it runs.
type Stage struct {
	ID        string
	Scenarios []Entry
}

// Index is the parsed acceptance index, stages in file order.
type Index struct {
	BuiltThrough string
	Stages       []Stage
}

// Stage returns the entry of the executing stage id.
func (x *Index) Stage(id string) (Stage, bool) {
	for _, s := range x.Stages {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}

// Required returns the scenario IDs the acceptance of stage runs, the value the
// scenario workflow sets DSH_REQUIRE_SCENARIOS to. An empty stage requires none; a
// stage the index does not list is an error, never an empty list.
func (x *Index) Required(stage string) ([]string, error) {
	if stage == "" {
		return nil, nil
	}
	s, ok := x.Stage(stage)
	if !ok {
		return nil, fmt.Errorf("stage %s has no entry in %s", stage, IndexPath)
	}
	ids := make([]string, 0, len(s.Scenarios))
	for _, e := range s.Scenarios {
		ids = append(ids, e.ID)
	}
	return ids, nil
}

// StageNumber returns n of "DH-S-n".
func StageNumber(stage string) (int, bool) {
	m := stagePattern.FindStringSubmatch(stage)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// ReadIndex reads the acceptance index of the repository at root.
func ReadIndex(root string) (*Index, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(IndexPath)))
	if err != nil {
		return nil, fmt.Errorf("%s not found under %s: the acceptance index cannot be checked: %w", IndexPath, root, err)
	}
	defer f.Close()
	x, err := ParseIndex(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", IndexPath, err)
	}
	return x, nil
}

// ParseIndex parses an acceptance index; see the package documentation for the one
// accepted shape. Every error names its line.
func ParseIndex(r io.Reader) (*Index, error) {
	x := &Index{}
	var (
		inStages bool
		stage    *Stage
		entry    *Entry
		seen     = map[string]bool{}
		lineNo   int
	)
	closeEntry := func() error {
		if entry != nil && entry.Authored == "" {
			return fmt.Errorf("line %d: %s has no authored stage", lineNo, entry.ID)
		}
		entry = nil
		return nil
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), " \t\r")
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimRight(line[:i], " \t")
		}
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return nil, fmt.Errorf("line %d: tab in indentation", lineNo)
		}
		indent := len(line) - len(trimmed)
		key, value, hasColon := strings.Cut(trimmed, ":")
		value = strings.TrimSpace(value)
		switch {
		case indent == 0 && key == "built-through" && hasColon:
			if x.BuiltThrough != "" {
				return nil, fmt.Errorf("line %d: built-through given twice", lineNo)
			}
			if _, ok := StageNumber(value); !ok {
				return nil, fmt.Errorf("line %d: built-through %q is not a stage ID (DH-S-n)", lineNo, value)
			}
			x.BuiltThrough = value
		case indent == 0 && key == "stages" && hasColon && value == "":
			if inStages {
				return nil, fmt.Errorf("line %d: stages given twice", lineNo)
			}
			inStages = true
		case indent == 2 && inStages && hasColon && (value == "" || value == "[]"):
			if err := closeEntry(); err != nil {
				return nil, err
			}
			if _, ok := StageNumber(key); !ok {
				return nil, fmt.Errorf("line %d: %q is not a stage ID (DH-S-n)", lineNo, key)
			}
			if seen[key] {
				return nil, fmt.Errorf("line %d: stage %s listed twice", lineNo, key)
			}
			seen[key] = true
			x.Stages = append(x.Stages, Stage{ID: key})
			stage = &x.Stages[len(x.Stages)-1]
			if value == "[]" {
				stage = nil
			}
		case indent == 4 && stage != nil && strings.HasPrefix(trimmed, "- "):
			if err := closeEntry(); err != nil {
				return nil, err
			}
			k, v, ok := strings.Cut(strings.TrimPrefix(trimmed, "- "), ":")
			v = strings.TrimSpace(v)
			if !ok || k != "id" || !scenarioPattern.MatchString(v) {
				return nil, fmt.Errorf("line %d: want \"- id: DH-E-<AREA>-<n>\", got %q", lineNo, trimmed)
			}
			stage.Scenarios = append(stage.Scenarios, Entry{ID: v})
			entry = &stage.Scenarios[len(stage.Scenarios)-1]
		case indent == 6 && entry != nil && key == "authored" && hasColon:
			if entry.Authored != "" {
				return nil, fmt.Errorf("line %d: %s has two authored stages", lineNo, entry.ID)
			}
			if _, ok := StageNumber(value); !ok {
				return nil, fmt.Errorf("line %d: authored %q is not a stage ID (DH-S-n)", lineNo, value)
			}
			entry.Authored = value
		default:
			return nil, fmt.Errorf("line %d: unexpected %q", lineNo, trimmed)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := closeEntry(); err != nil {
		return nil, err
	}
	if x.BuiltThrough == "" {
		return nil, fmt.Errorf("no built-through stage")
	}
	if !inStages {
		return nil, fmt.Errorf("no stages")
	}
	return x, nil
}
