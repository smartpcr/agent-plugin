//go:build e2e

package e2e

// Step definitions of
// dh_s_16_profiles_launchers_and_evidence_runners_later_stage_scenario_authoring.feature:
// the 17 later-run scenarios of tech-spec sec. 4.5 plus DH-E-SDK-1 compile under
// the scenario tag for each lane's GOOS (proof: service:go-toolchain), and the
// committed acceptance index places each of the 17 once under its executing stage,
// none under DH-S-16..18 (proof: golden, the index read by the gate's own
// acceptance.ReadIndex and projected onto a committed golden).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

var s611OuterT *testing.T

type s611Want struct{ id, file string }

type s611Entry struct {
	Stage    string `json:"stage"`
	ID       string `json:"id"`
	Authored string `json:"authored"`
}

type s611Report struct {
	Entries []s611Entry `json:"entries"`
}

type s611State struct {
	root   string
	dir    string
	wants  []s611Want
	goos   []string
	vetErr map[string]string

	index    string
	golden   []byte
	goldenID []string
	goldenSt map[string]string
	entries  []s611Entry
}

// s611IndexProgram reads the index with the gate's acceptance.ReadIndex, the
// reader of the scenario-index gate, and prints every entry with its stage.
const s611IndexProgram = `package main

import (
	"encoding/json"
	"os"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

type entry struct {
	Stage    string ` + "`json:\"stage\"`" + `
	ID       string ` + "`json:\"id\"`" + `
	Authored string ` + "`json:\"authored\"`" + `
}

func main() {
	x, err := acceptance.ReadIndex(os.Args[1])
	if err != nil {
		panic(err)
	}
	es := []entry{}
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			es = append(es, entry{s.ID, e.ID, e.Authored})
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"entries": es}); err != nil {
		panic(err)
	}
}
`

var s611IDRe = regexp.MustCompile(`DH-E-[A-Z]+-[0-9]+`)

func (s *s611State) ensureRoot() error {
	if s.root != "" {
		return nil
	}
	root, err := s15rRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

func (s *s611State) path(rel string) string { return filepath.Join(s.root, filepath.FromSlash(rel)) }

// s611SpecIDs returns the IDs of the table rows of tech-spec sec. 4.5 (the prose
// below the table names scenarios authored elsewhere and is not read).
func s611SpecIDs(spec string) ([]string, error) {
	data, err := os.ReadFile(spec)
	if err != nil {
		return nil, err
	}
	in := false
	var ids []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "### 4.5 "):
			in = true
			continue
		case in && strings.HasPrefix(line, "#"):
			in = false
		}
		if in && strings.HasPrefix(line, "|") {
			cells := strings.Split(line, "|")
			if len(cells) > 1 {
				ids = append(ids, s611IDRe.FindAllString(cells[1], -1)...)
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s: no sec. 4.5 table", spec)
	}
	slices.Sort(ids)
	return ids, nil
}

func s611CheckSpecSet(got []string, spec []string, what string) error {
	if len(spec) != 17 {
		return fmt.Errorf("tech-spec sec. 4.5 lists %d IDs, want 17: %v", len(spec), spec)
	}
	g := slices.Clone(got)
	slices.Sort(g)
	if !slices.Equal(slices.Compact(g), spec) || len(g) != len(got) {
		return fmt.Errorf("%s %v, tech-spec sec. 4.5 %v", what, got, spec)
	}
	return nil
}

// ---- Scenario: Later-run scenarios compile ----

func (s *s611State) givenFiles(dir string, table *godog.Table) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	s.dir = dir
	if len(table.Rows) < 2 {
		return errors.New("the table lists no scenario")
	}
	for _, r := range table.Rows[1:] {
		s.wants = append(s.wants, s611Want{strings.TrimSpace(r.Cells[0].Value), strings.TrimSpace(r.Cells[1].Value)})
	}
	return nil
}

func (s *s611State) tableIsSpec(spec, extra string) error {
	ids, err := s611SpecIDs(s.path(spec))
	if err != nil {
		return err
	}
	var got []string
	seenExtra := false
	for _, w := range s.wants {
		if w.id == extra {
			if seenExtra || w.file != "sdk_stdio_scenario_test.go" {
				return fmt.Errorf("%s listed twice or not in sdk_stdio_scenario_test.go: %+v", extra, w)
			}
			seenExtra = true
			continue
		}
		got = append(got, w.id)
	}
	if !seenExtra {
		return fmt.Errorf("the table does not list %s", extra)
	}
	return s611CheckSpecSet(got, ids, "the table lists")
}

func (s *s611State) filesCarryDirective(directive, tag string) error {
	for _, w := range s.wants {
		p := filepath.Join(s.path(s.dir), w.file)
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s: %v", w.id, err)
		}
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(directive) + `\s+` + regexp.QuoteMeta(w.id) + `\s*$`)
		if n := len(re.FindAll(data, -1)); n != 1 {
			return fmt.Errorf("%s carries %s %s %d times, want once", w.file, directive, w.id, n)
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, data, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		if got := s15rBuildLine(f); got != "//go:build "+tag {
			return fmt.Errorf("%s has build line %q, want //go:build %s", w.file, got, tag)
		}
	}
	return nil
}

func (s *s611State) runVet(command, a, b string) error {
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	s.goos = []string{a, b}
	s.vetErr = map[string]string{}
	for _, goos := range s.goos {
		if out, err := s15rCommand(s.root, s15rGOOSEnv(goos), "go", args[1:]...); err != nil {
			s.vetErr[goos] = fmt.Sprintf("%v\n%s", err, out)
		}
	}
	return nil
}

func (s *s611State) vetExits0() error {
	if len(s.goos) == 0 {
		return errors.New("vet did not run")
	}
	for _, goos := range s.goos {
		if msg, bad := s.vetErr[goos]; bad {
			return fmt.Errorf("vet -tags scenario on %s: %s", goos, msg)
		}
	}
	return nil
}

// ---- Scenario: Index count ----

func (s *s611State) givenIndex(index string) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	if !s15rIsRegular(s.path(index)) {
		return fmt.Errorf("%s is missing", index)
	}
	s.index = index
	return nil
}

func (s *s611State) givenGolden(golden, spec string) error {
	data, err := os.ReadFile(filepath.FromSlash(golden))
	if err != nil {
		return err
	}
	s.golden = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	s.goldenSt = map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(s.golden), "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || !strings.HasPrefix(f[2], "authored=") {
			return fmt.Errorf("%s: malformed line %q", golden, line)
		}
		s.goldenID = append(s.goldenID, f[0])
		s.goldenSt[f[0]] = f[1]
	}
	ids, err := s611SpecIDs(s.path(spec))
	if err != nil {
		return err
	}
	return s611CheckSpecSet(s.goldenID, ids, "the golden lists")
}

func (s *s611State) countIDs() error {
	tmp := s611OuterT.TempDir()
	gate := s.path("tools/harness-gate")
	goVersion, err := s15rGoDirective(filepath.Join(gate, "go.mod"))
	if err != nil {
		return err
	}
	gomod := fmt.Sprintf("module dsh.e2e/s611index\n\ngo %s\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
		goVersion, strconv.Quote(filepath.ToSlash(gate)))
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0o644); err != nil {
		return err
	}
	sum, err := os.ReadFile(filepath.Join(gate, "go.sum"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.sum"), sum, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(s611IndexProgram), 0o644); err != nil {
		return err
	}
	out, err := s15rCommand(tmp, []string{"GOWORK=off", "GOFLAGS=-mod=mod"}, "go", "run", ".", s.root)
	if err != nil {
		return fmt.Errorf("acceptance.ReadIndex: %v\n%s", err, out)
	}
	var r s611Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return fmt.Errorf("index reader output: %v\n%s", err, out)
	}
	if len(r.Entries) == 0 {
		return fmt.Errorf("%s has no entry", s.index)
	}
	s.entries = r.Entries
	return nil
}

func (s *s611State) of(id string) []s611Entry {
	var out []s611Entry
	for _, e := range s.entries {
		if e.ID == id {
			out = append(out, e)
		}
	}
	return out
}

func (s *s611State) eachOnce() error {
	for _, id := range s.goldenID {
		if es := s.of(id); len(es) != 1 {
			return fmt.Errorf("%s has %d index entries, want 1: %+v", id, len(es), es)
		}
	}
	return nil
}

func (s *s611State) underStatedStage() error {
	for _, id := range s.goldenID {
		if es := s.of(id); len(es) != 1 || es[0].Stage != s.goldenSt[id] {
			return fmt.Errorf("%s indexed as %+v, want under %s", id, es, s.goldenSt[id])
		}
	}
	return nil
}

func (s *s611State) matchesGolden() error {
	var b strings.Builder
	ids := slices.Clone(s.goldenID)
	slices.Sort(ids)
	for _, id := range ids {
		for _, e := range s.of(id) {
			fmt.Fprintf(&b, "%s %s authored=%s\n", e.ID, e.Stage, e.Authored)
		}
	}
	if got := b.String(); got != string(s.golden) {
		return fmt.Errorf("index projection differs from the golden:\n--- got\n%s--- want\n%s", got, s.golden)
	}
	return nil
}

func (s *s611State) noneUnder(a, b, c string) error {
	for _, e := range s.entries {
		if (e.Stage == a || e.Stage == b || e.Stage == c) && s.goldenSt[e.ID] != "" {
			return fmt.Errorf("%s is indexed under %s", e.ID, e.Stage)
		}
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_later_stage_scenario_authoring(ctx *godog.ScenarioContext) {
	s := &s611State{}
	ctx.Step(`^the later-run scenario files in "([^"]*)"$`, s.givenFiles)
	ctx.Step(`^the table lists the 17 IDs of tech-spec sec\. 4\.5 in "([^"]*)" plus "([^"]*)"$`, s.tableIsSpec)
	ctx.Step(`^each file carries its "([^"]*)" directives and is built only under the "([^"]*)" tag$`, s.filesCarryDirective)
	ctx.Step(`^"([^"]*)" runs for "([^"]*)" and "([^"]*)"$`, s.runVet)
	ctx.Step(`^vet exits 0 for each$`, s.vetExits0)
	ctx.Step(`^the committed "([^"]*)"$`, s.givenIndex)
	ctx.Step(`^the golden "([^"]*)" of the 17 IDs of tech-spec sec\. 4\.5 in "([^"]*)"$`, s.givenGolden)
	ctx.Step(`^the index check counts the 17 IDs$`, s.countIDs)
	ctx.Step(`^each ID appears exactly once in the index$`, s.eachOnce)
	ctx.Step(`^each ID is under the stage the golden states$`, s.underStatedStage)
	ctx.Step(`^the projection of the index onto the 17 IDs matches the golden byte for byte$`, s.matchesGolden)
	ctx.Step(`^none of the 17 IDs is under "([^"]*)", "([^"]*)" or "([^"]*)"$`, s.noneUnder)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_later_stage_scenario_authoring(t *testing.T) {
	s611OuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_later_stage_scenario_authoring,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_later_stage_scenario_authoring.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
