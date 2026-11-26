//go:build e2e

package e2e

// Step definitions of
// dh_s_13_sandbox_launch_linux_backends_and_shell_tools_dh_s_13_scenarios_and_built_record.feature:
// the nine real-environment scenarios DH-S-13 authors compile under the scenario
// tag and are indexed once under their executing stages (M2-DOD-5), and the
// stage's record lists the fake-backend tests of every security control with
// results while both local lanes pass every gate (impl.md DH-S-13 exit evidence).
// Proof: service:go-toolchain -- the real go toolchain, the gate's own acceptance
// package and "records" subcommand, the record's security-control tests run live,
// and the lanes' --evidence records verified byte for byte (SHA-256 trailer).

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

var s13rOuterT *testing.T

const (
	s13rCommitEnv     = "DSH_E2E_DHS13_COMMIT"
	s13rCommitSubject = `^\[impl\] DH-S-13 Scenarios and Built Record`
	s13rRunsDir       = "docs/stories/harness-DSH-M1/evidence/runs"
	s13rEvidenceDir   = "docs/stories/harness-DSH-M2/evidence"
	s13rStageRecord   = s13rEvidenceDir + "/DH-S-13.md"
	s13rTableHeader   = "| Control | Test | Windows | Linux (WSL) |"
	s13rHookSuffix    = "[testhook]"
	s13rBothBuilds    = "(both builds)"
)

// s13rTestDirs are the packages (under src/harness) whose tests the record's
// security-control commands run.
var s13rTestDirs = []string{"internal/world/sandbox", "internal/world/shellexec", "internal/world/subprocess"}

type s13rWant struct {
	id, file, stage string
	requires        []string
}

type s13rEntry struct {
	Stage    string   `json:"stage"`
	ID       string   `json:"id"`
	Authored string   `json:"authored"`
	Requires []string `json:"requires"`
}

type s13rScenario struct {
	ID   string `json:"id"`
	File string `json:"file"`
	Test string `json:"test"`
}

type s13rReport struct {
	Entries     []s13rEntry    `json:"entries"`
	Scenarios   []s13rScenario `json:"scenarios"`
	CheckErr    string         `json:"check_err"`
	BoundaryErr string         `json:"boundary_err"`
}

type s13rLane struct {
	name, path string
	data       []byte
}

// s13rControl is one row of a fake-backend control table of the record: its
// tests and, per lane column, each test's result.
type s13rControl struct {
	table, control string
	tests          []string
	results        map[string][]string // column -> one result per test
	line           int
}

type s13rState struct {
	root   string
	dir    string
	wants  []s13rWant
	vetOut string
	vetErr error
	report s13rReport

	merged, commit string
	record         string
	text           string
	controls       []s13rControl
	tables         int
	pins           []s13rPin
	liveLanes      []*s13rLiveLane
	lanes          map[string]*s13rLane
	lanesErr       error

	gateBudget time.Duration
}

// s13rPin is one row of the feature's pinned control list.
type s13rPin struct {
	control, test, build string
}

func (p s13rPin) builds() []string {
	switch p.build {
	case "both":
		return []string{"default", "testhook"}
	case "testhook":
		return []string{"testhook"}
	}
	return []string{"default"}
}

// s13rGateProgram is the gate's scenario-index check (acceptance.Check and
// acceptance.CheckBoundary over the index and the scanned scenario tests of the
// three modules, as tools/harness-gate's scenario-index gate runs them).
const s13rGateProgram = `package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

type entry struct {
	Stage    string   ` + "`json:\"stage\"`" + `
	ID       string   ` + "`json:\"id\"`" + `
	Authored string   ` + "`json:\"authored\"`" + `
	Requires []string ` + "`json:\"requires\"`" + `
}

type scenario struct {
	ID   string ` + "`json:\"id\"`" + `
	File string ` + "`json:\"file\"`" + `
	Test string ` + "`json:\"test\"`" + `
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	root := os.Args[1]
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		panic(err)
	}
	var mods []string
	for _, m := range []string{"src/go", "src/sdk", "src/harness"} {
		mods = append(mods, filepath.Join(root, filepath.FromSlash(m)))
	}
	sc, err := acceptance.ScanScenarios(root, mods)
	if err != nil {
		panic(err)
	}
	var es []entry
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			es = append(es, entry{s.ID, e.ID, e.Authored, e.Requires})
		}
	}
	var ss []scenario
	for _, s := range sc {
		ss = append(ss, scenario{s.ID, s.File, s.Test})
	}
	out := map[string]any{
		"entries":      es,
		"scenarios":    ss,
		"check_err":    errString(acceptance.Check(x, sc)),
		"boundary_err": errString(acceptance.CheckBoundary(root, x, sc)),
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`

func s13rIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func s13rRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if s13rIsRegular(filepath.Join(dir, "go.work")) && s13rIsRegular(filepath.Join(dir, "test", "harness", "acceptance-index.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func s13rCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func s13rSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *s13rState) ensureRoot() error {
	if s.root != "" {
		return nil
	}
	root, err := s13rRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

// ---- Scenario: Authored S13 scenarios compile and are indexed ----

func (s *s13rState) givenScenarioFiles(dir, index string, table *godog.Table) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	if !s13rIsRegular(filepath.Join(s.root, filepath.FromSlash(index))) {
		return fmt.Errorf("%s is missing", index)
	}
	s.dir = dir
	if len(table.Rows) < 2 {
		return errors.New("the table lists no scenario")
	}
	head := map[string]int{}
	for i, c := range table.Rows[0].Cells {
		head[c.Value] = i
	}
	for _, col := range []string{"id", "file", "stage", "requires"} {
		if _, ok := head[col]; !ok {
			return fmt.Errorf("the table has no %q column", col)
		}
	}
	for _, r := range table.Rows[1:] {
		cell := func(c string) string { return strings.TrimSpace(r.Cells[head[c]].Value) }
		s.wants = append(s.wants, s13rWant{id: cell("id"), file: cell("file"), stage: cell("stage"), requires: s13rSplit(cell("requires"))})
	}
	if len(s.wants) != 9 {
		return fmt.Errorf("DH-S-13 authors nine scenarios, the table lists %d", len(s.wants))
	}
	for _, w := range s.wants {
		p := filepath.Join(s.root, filepath.FromSlash(dir), w.file)
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s: %v", w.id, err)
		}
		if !regexp.MustCompile(`(?m)^//dsh:scenario\s+` + regexp.QuoteMeta(w.id) + `\s*$`).Match(data) {
			return fmt.Errorf("%s does not carry //dsh:scenario %s", w.file, w.id)
		}
	}
	return nil
}

func (s *s13rState) runVet(command string) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	s.vetOut, s.vetErr = s13rCommand(s.root, []string{"GOFLAGS="}, "go", args[1:]...)
	return nil
}

func (s *s13rState) runIndexCheck() error {
	tmp := s13rOuterT.TempDir()
	gate := filepath.Join(s.root, "tools", "harness-gate")
	gomod := fmt.Sprintf("module dsh.e2e/s13rindex\n\ngo 1.27.4\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
		strconv.Quote(filepath.ToSlash(gate)))
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
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(s13rGateProgram), 0o644); err != nil {
		return err
	}
	out, err := s13rCommand(tmp, []string{"GOWORK=off", "GOFLAGS=-mod=mod"}, "go", "run", ".", s.root)
	if err != nil {
		return fmt.Errorf("the gate's scenario-index check: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &s.report); err != nil {
		return fmt.Errorf("scenario-index check output: %v\n%s", err, out)
	}
	return nil
}

func (s *s13rState) vetExits0() error {
	if s.vetErr != nil {
		return fmt.Errorf("vet -tags scenario: %v\n%s", s.vetErr, s.vetOut)
	}
	return nil
}

func (s *s13rState) indexCheckPasses() error {
	if s.report.CheckErr != "" {
		return fmt.Errorf("acceptance.Check: %s", s.report.CheckErr)
	}
	if s.report.BoundaryErr != "" {
		return fmt.Errorf("acceptance.CheckBoundary: %s", s.report.BoundaryErr)
	}
	return nil
}

func (s *s13rState) bindings(id string) []s13rScenario {
	var out []s13rScenario
	for _, sc := range s.report.Scenarios {
		if sc.ID == id {
			out = append(out, sc)
		}
	}
	return out
}

func (s *s13rState) boundOnceInFile(tag string) error {
	tests := map[string]string{}
	for _, w := range s.wants {
		bs := s.bindings(w.id)
		if len(bs) != 1 {
			return fmt.Errorf("%s is bound %d times: %+v", w.id, len(bs), bs)
		}
		want := s.dir + "/" + w.file
		if bs[0].File != want || !strings.HasPrefix(bs[0].Test, "Test") {
			return fmt.Errorf("%s bound as %+v, want a TestXxx in %s", w.id, bs[0], want)
		}
		if other, dup := tests[bs[0].Test]; dup {
			return fmt.Errorf("%s and %s are bound by the same test %s", other, w.id, bs[0].Test)
		}
		tests[bs[0].Test] = w.id
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(s.root, filepath.FromSlash(want)), nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		tagged := false
		for _, cg := range f.Comments {
			if cg.Pos() >= f.Package {
				break
			}
			for _, c := range cg.List {
				if strings.TrimSpace(c.Text) == "//go:build "+tag {
					tagged = true
				}
			}
		}
		if !tagged {
			return fmt.Errorf("%s is not built only under //go:build %s", want, tag)
		}
	}
	return nil
}

func (s *s13rState) indexedOnce(authored string) error {
	for _, w := range s.wants {
		var es []s13rEntry
		for _, e := range s.report.Entries {
			if e.ID == w.id {
				es = append(es, e)
			}
		}
		if len(es) != 1 {
			return fmt.Errorf("%s has %d index entries: %+v", w.id, len(es), es)
		}
		e := es[0]
		if e.Stage != w.stage || e.Authored != authored || !slices.Equal(e.Requires, w.requires) {
			return fmt.Errorf("%s entry %+v, want under %s authored %s requiring %v", w.id, e, w.stage, authored, w.requires)
		}
	}
	return nil
}

func (s *s13rState) defaultRunNone() error {
	harness := filepath.Join(s.root, "src", "harness")
	pkg := "./" + strings.TrimPrefix(s.dir, "src/harness/")
	plain, err := s13rCommand(harness, []string{"GOFLAGS="}, "go", "test", "-tags=", "-list", ".", "-count=1", pkg)
	if err != nil {
		return fmt.Errorf("go test -tags= -list: %v\n%s", err, plain)
	}
	tagged, err := s13rCommand(harness, []string{"GOFLAGS="}, "go", "test", "-tags", "scenario", "-list", ".", "-count=1", pkg)
	if err != nil {
		return fmt.Errorf("go test -tags scenario -list: %v\n%s", err, tagged)
	}
	for _, w := range s.wants {
		name := s.bindings(w.id)[0].Test
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*$`)
		if re.MatchString(plain) {
			return fmt.Errorf("the default run includes %s (%s):\n%s", name, w.id, plain)
		}
		if !re.MatchString(tagged) {
			return fmt.Errorf("-tags scenario does not list %s (%s):\n%s", name, w.id, tagged)
		}
	}
	return nil
}

// ---- Scenario: S13 gate green on both lanes ----

var s13rHex = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *s13rState) revParse(ref string) (string, error) {
	out, err := s13rCommand(s.root, nil, "git", "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %v\n%s", ref, err, out)
	}
	sha := strings.TrimSpace(out)
	if !s13rHex.MatchString(sha) {
		return "", fmt.Errorf("unexpected commit %q", sha)
	}
	return sha, nil
}

func (s *s13rState) isAncestor(a, b string) bool {
	_, err := s13rCommand(s.root, nil, "git", "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func (s *s13rState) givenMergedCommit(record string) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	out, err := s13rCommand(s.root, nil, "git", "log", "--format=%H", "-E", "--grep", s13rCommitSubject, "-n", "1", "HEAD")
	if err != nil {
		return fmt.Errorf("git log --grep: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("no commit %q on this checkout's history", s13rCommitSubject)
	}
	if s.merged, err = s.revParse(strings.TrimSpace(out)); err != nil {
		return err
	}
	s.commit = s.merged
	if ref := strings.TrimSpace(os.Getenv(s13rCommitEnv)); ref != "" {
		if s.commit, err = s.revParse(ref); err != nil {
			return err
		}
		if !s.isAncestor(s.merged, s.commit) {
			return fmt.Errorf("%s=%s does not hold the merged commit %s", s13rCommitEnv, s.commit[:7], s.merged[:7])
		}
	}
	if !s.isAncestor(s.commit, "HEAD") {
		return fmt.Errorf("%s is not on this checkout's history", s.commit)
	}
	s.record = record
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(record)))
	if err != nil {
		return err
	}
	s.text = strings.ReplaceAll(string(data), "\r\n", "\n")
	return s.parseControls()
}

func s13rCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	var out []string
	for _, c := range strings.Split(line, "|") {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

// s13rSplitResults splits "passed, skipped (Windows row, DH-D-SANDBOX-4)" at the
// commas outside parentheses.
func s13rSplitResults(cell string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range cell {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(cell[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(cell[start:]))
}

var s13rBackticked = regexp.MustCompile("`([^`]+)`")

// parseControls reads every table of the record with the control header whose
// lead paragraph says it runs on fake backends.
func (s *s13rState) parseControls() error {
	lines := strings.Split(s.text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != s13rTableHeader {
			continue
		}
		lead := ""
		for j := i - 1; j >= 0; j-- {
			if t := strings.TrimSpace(lines[j]); t != "" {
				for ; j >= 0 && strings.TrimSpace(lines[j]) != ""; j-- {
					lead = strings.TrimSpace(lines[j]) + " " + lead
				}
				break
			}
		}
		if !strings.Contains(strings.ToLower(lead), "fake") {
			continue
		}
		s.tables++
		cols := s13rCells(l)
		for k := i + 2; k < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k]), "|"); k++ {
			cells := s13rCells(lines[k])
			if len(cells) != len(cols) {
				return fmt.Errorf("%s:%d has %d cells, want %d", s.record, k+1, len(cells), len(cols))
			}
			c := s13rControl{table: lead, control: cells[0], results: map[string][]string{}, line: k + 1}
			for _, m := range s13rBackticked.FindAllStringSubmatch(cells[1], -1) {
				c.tests = append(c.tests, m[1])
			}
			for _, col := range []int{2, 3} {
				c.results[cols[col]] = s13rSplitResults(cells[col])
			}
			s.controls = append(s.controls, c)
		}
	}
	return nil
}

// s13rResult returns the result of the i-th test of a row in a column: one result
// for every test, or one per test.
func (c s13rControl) result(col string, i int) string {
	rs := c.results[col]
	if len(rs) == 1 {
		return rs[0]
	}
	if i < len(rs) {
		return rs[i]
	}
	return ""
}

var s13rResultRe = regexp.MustCompile(`^(passed|failed|skipped \(.+\)|not run \(.+\)|out of scope \(.+\)|passed \(both builds\))$`)

func (s *s13rState) lanesRan(win, wsl string) error {
	s.lanes = map[string]*s13rLane{}
	var missing []string
	for _, script := range []string{win, wsl} {
		lane := strings.TrimSuffix(strings.TrimSuffix(script, ".ps1"), ".sh")
		data, err := os.ReadFile(filepath.Join(s.root, "tools", "harness-gate", "lanes", script))
		if err != nil {
			return fmt.Errorf("lane script %s: %v", script, err)
		}
		if !bytes.Contains(data, []byte("--evidence")) || !bytes.Contains(data, []byte(lane+"-")) {
			return fmt.Errorf("lane script %s does not write a %s-<sha7> evidence record", script, lane)
		}
		pattern := filepath.Join(s.root, filepath.FromSlash(s13rRunsDir), lane+"-"+s.commit[:7]+"-*Z.txt")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			missing = append(missing, fmt.Sprintf("no %s lane record of %s (%s): run tools/harness-gate/lanes/%s on that commit and commit its record",
				lane, s.commit[:7], filepath.ToSlash(pattern), script))
			continue
		}
		slices.Sort(matches)
		p := matches[len(matches)-1]
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		s.lanes[lane] = &s13rLane{name: lane, path: filepath.ToSlash(rel), data: b}
	}
	if len(missing) > 0 {
		// Kept for the lane Then steps, so the record's control steps still run.
		s.lanesErr = errors.New(strings.Join(missing, "\n"))
	}
	return nil
}

// s13rCommandTests returns the test names of every -run '^(...)$' in the record's
// "Exact tests and commands" section.
func (s *s13rState) commandTests() []string {
	_, sec, _ := strings.Cut(s.text, "### Exact tests and commands")
	sec, _, _ = strings.Cut(sec, "\n### ")
	var out []string
	for _, m := range regexp.MustCompile(`-run '\^\(([^)]*)\)\$'`).FindAllStringSubmatch(sec, -1) {
		out = append(out, strings.Split(m[1], "|")...)
	}
	return out
}

func (s *s13rState) everyCommandTestListed() error {
	if s.tables < 2 {
		return fmt.Errorf("%s has %d fake-backend control tables (%q), want the security controls and the launch-authorization controls", s.record, s.tables, s13rTableHeader)
	}
	listed := map[string]bool{}
	for _, c := range s.controls {
		for _, t := range c.tests {
			listed[strings.TrimSuffix(t, s13rHookSuffix)] = true
		}
	}
	names := s.commandTests()
	if len(names) == 0 {
		return fmt.Errorf("%s names no -run '^(...)$' security-control command", s.record)
	}
	for _, n := range names {
		if !listed[n] {
			return fmt.Errorf("%s runs %s in its commands but no fake-backend control row lists it", s.record, n)
		}
	}
	return nil
}

func (s *s13rState) testFuncs() (map[string]bool, error) {
	funcs := map[string]bool{}
	re := regexp.MustCompile(`(?m)^func (Test\w+)\(\w+ \*testing\.T\)`)
	for _, d := range s13rTestDirs {
		root := filepath.Join(s.root, "src", "harness", filepath.FromSlash(d))
		err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, m := range re.FindAllSubmatch(b, -1) {
				funcs[string(m[1])] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return funcs, nil
}

func s13rHeader(data []byte) map[string]string {
	h := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if line == "--- output ---" {
			break
		}
		if k, v, ok := strings.Cut(line, ": "); ok {
			h[k] = v
		}
	}
	return h
}

func (s *s13rState) laneRecordIntact(lane, goos string) error {
	if s.lanesErr != nil {
		return s.lanesErr
	}
	l, ok := s.lanes[lane]
	if !ok {
		return fmt.Errorf("no %s lane record", lane)
	}
	data := bytes.ReplaceAll(l.data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("harness-gate evidence record\n")) {
		return fmt.Errorf("%s is not a harness-gate evidence record", l.path)
	}
	body := bytes.TrimSuffix(data, []byte("\n"))
	i := bytes.LastIndexByte(body, '\n')
	if i < 0 {
		return fmt.Errorf("%s has no sha256 trailer", l.path)
	}
	last := string(body[i+1:])
	sum := sha256.Sum256(data[:i+1])
	if last != "sha256: "+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%s: trailer %q does not match the record's SHA-256 %s", l.path, last, hex.EncodeToString(sum[:]))
	}
	h := s13rHeader(data)
	if h["commit"] != s.commit {
		return fmt.Errorf("%s records commit %q, want %s", l.path, h["commit"], s.commit)
	}
	if !strings.HasPrefix(h["tree"], "clean") {
		return fmt.Errorf("%s: tree %q is not clean", l.path, h["tree"])
	}
	if !strings.HasPrefix(h["goos/goarch"], goos+"/") {
		return fmt.Errorf("%s ran on %q, want %s", l.path, h["goos/goarch"], goos)
	}
	if !strings.Contains(h["command"], "--evidence") || !strings.Contains(filepath.ToSlash(h["command"]), "/"+filepath.Base(l.path)) {
		return fmt.Errorf("%s: command %q did not write this record", l.path, h["command"])
	}
	return nil
}

var (
	s13rGateLine = regexp.MustCompile(`(?m)^harness-gate: gate (\d+)/(\d+) (\S+) --`)
	s13rPassLine = regexp.MustCompile(`(?m)^harness-gate: PASS (\S+) \(`)
	s13rFailLine = regexp.MustCompile(`(?m)^harness-gate: FAIL`)
	s13rAllLine  = regexp.MustCompile(`(?m)^harness-gate: PASSED all (\d+) gates$`)
)

func s13rTrailerField(text, key string) string {
	_, after, ok := strings.Cut(text, "--- end of output ---\n")
	if !ok {
		return ""
	}
	for _, line := range strings.Split(after, "\n") {
		if v, ok := strings.CutPrefix(line, key+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (s *s13rState) everyGatePasses() error {
	if s.lanesErr != nil {
		return s.lanesErr
	}
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		text := strings.ReplaceAll(string(l.data), "\r\n", "\n")
		gates := s13rGateLine.FindAllStringSubmatch(text, -1)
		if len(gates) == 0 {
			return fmt.Errorf("%s runs no gate", l.path)
		}
		total := gates[0][2]
		passed := map[string]bool{}
		for _, m := range s13rPassLine.FindAllStringSubmatch(text, -1) {
			passed[m[1]] = true
		}
		for i, g := range gates {
			if g[1] != strconv.Itoa(i+1) || g[2] != total {
				return fmt.Errorf("%s: gate line %q out of order", l.path, g[0])
			}
			if !passed[g[3]] {
				return fmt.Errorf("%s: gate %s did not pass", l.path, g[3])
			}
		}
		if strconv.Itoa(len(gates)) != total {
			return fmt.Errorf("%s ran %d of %s gates", l.path, len(gates), total)
		}
		if s13rFailLine.MatchString(text) {
			return fmt.Errorf("%s reports a failed gate", l.path)
		}
		if all := s13rAllLine.FindStringSubmatch(text); all == nil || all[1] != total {
			return fmt.Errorf("%s does not report PASSED all %s gates", l.path, total)
		}
		if h := s13rTrailerField(text, "exit"); h != "0" {
			return fmt.Errorf("%s exits %q, want 0", l.path, h)
		}
	}
	return nil
}

func (s *s13rState) recordNamesLanes() error {
	if s.lanesErr != nil {
		return s.lanesErr
	}
	scripts := map[string]string{"win": "win.ps1", "wsl": "wsl.sh"}
	for _, lane := range []string{"win", "wsl"} {
		base := filepath.Base(s.lanes[lane].path)
		found := false
		for _, line := range strings.Split(s.text, "\n") {
			if strings.Contains(line, scripts[lane]) && strings.Contains(line, base) &&
				strings.Contains(line, s.commit[:7]) && strings.Contains(strings.ToLower(line), "exit 0") {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s has no line naming %s, its record %s, the commit %s and exit 0", s.record, scripts[lane], base, s.commit[:7])
		}
	}
	return nil
}

var s13rRecordsOK = regexp.MustCompile(`(?m)^harness-gate records: harness:DSH-M2: ([0-9]+) stage records and ` +
	regexp.QuoteMeta(s13rEvidenceDir+"/closure.md") + ` hold their shape\s*$`)

// gateRecordsAccepts: "harness-gate records" accepts the story's records, and
// DH-S-13.md is one it examines -- the same run without it fails naming it.
func (s *s13rState) gateRecordsAccepts() error {
	gate := filepath.Join(s.root, "tools", "harness-gate")
	env := []string{"GOWORK=off", "GOFLAGS="}
	out, err := s13rCommand(gate, env, "go", "run", ".", "records", "--root", s.root)
	if err != nil {
		return fmt.Errorf("harness-gate records: %v\n%s", err, out)
	}
	m := s13rRecordsOK.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("harness-gate records did not report the harness:DSH-M2 records holding their shape:\n%s", out)
	}
	if n, _ := strconv.Atoi(m[1]); n < 1 {
		return fmt.Errorf("harness-gate records examined %d stage records:\n%s", n, out)
	}
	probe := s13rOuterT.TempDir()
	if err := s13rCopyFile(filepath.Join(s.root, "test", "harness", "acceptance-index.yaml"), filepath.Join(probe, "test", "harness", "acceptance-index.yaml")); err != nil {
		return err
	}
	src := filepath.Join(s.root, filepath.FromSlash(s13rEvidenceDir))
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.Type().IsRegular() || e.Name() == filepath.Base(s13rStageRecord) {
			continue
		}
		if err := s13rCopyFile(filepath.Join(src, e.Name()), filepath.Join(probe, filepath.FromSlash(s13rEvidenceDir), e.Name())); err != nil {
			return err
		}
	}
	out, err = s13rCommand(gate, env, "go", "run", ".", "records", "--root", probe)
	if err == nil {
		return fmt.Errorf("harness-gate records accepted a tree without %s:\n%s", s13rStageRecord, out)
	}
	if !strings.Contains(out, "no evidence record "+s13rStageRecord) {
		return fmt.Errorf("harness-gate records without %s failed without naming it: %v\n%s", s13rStageRecord, err, out)
	}
	return nil
}

func s13rCopyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}

// ---- the pinned security controls, run live ----

func (s *s13rState) givenPins(table *godog.Table) error {
	if len(table.Rows) < 2 {
		return errors.New("the control table lists no test")
	}
	head := map[string]int{}
	for i, c := range table.Rows[0].Cells {
		head[c.Value] = i
	}
	for _, col := range []string{"control", "test", "build"} {
		if _, ok := head[col]; !ok {
			return fmt.Errorf("the control table has no %q column", col)
		}
	}
	seen := map[string]bool{}
	for _, r := range table.Rows[1:] {
		cell := func(c string) string { return strings.TrimSpace(r.Cells[head[c]].Value) }
		p := s13rPin{control: cell("control"), test: cell("test"), build: cell("build")}
		if p.control == "" || !strings.HasPrefix(p.test, "Test") || !slices.Contains([]string{"default", "testhook", "both"}, p.build) {
			return fmt.Errorf("malformed control row %+v", p)
		}
		if seen[p.test] {
			return fmt.Errorf("%s is pinned twice", p.test)
		}
		seen[p.test] = true
		s.pins = append(s.pins, p)
	}
	return nil
}

// s13rBuildArgs are the -tags flag and the packages of a build.
func s13rBuildArgs(build string) (string, []string) {
	if build == "testhook" {
		return "-tags=testhook", []string{"./internal/world/sandbox/launch/"}
	}
	var pkgs []string
	for _, d := range s13rTestDirs {
		pkgs = append(pkgs, "./"+d+"/...")
	}
	return "-tags=", pkgs
}

var s13rListed = regexp.MustCompile(`(?m)^(Test\w+)\s*$`)

// s13rLiveLane is one lane this scenario runs live: this host's, and the other
// one reachable from it (the WSL lane from a Windows host). Each runs the pinned
// tests and harness-gate in its own checkout of the lanes' commit.
type s13rLiveLane struct {
	name, column string
	checkout     string // in the lane's own path form
	command      func(ctx context.Context, rel string, env []string, name string, args ...string) *exec.Cmd
	remove       func() error

	live         map[string]map[string]string // build -> test -> PASS, FAIL, SKIP, NONE (built, no result) or ABSENT (not built)
	out          string
	err          error
	gateOut      string
	gateErr      error
	gateTimedOut bool
}

func (l *s13rLiveLane) run(rel string, env []string, name string, args ...string) (string, error) {
	out, err := l.command(context.Background(), rel, env, name, args...).CombinedOutput()
	return strings.ReplaceAll(string(out), "\r\n", "\n"), err
}

const (
	s13rWSLDistroEnv = "DSH_E2E_DHS13_WSL_DISTRO"
	s13rWSLUserEnv   = "DSH_E2E_DHS13_WSL_USER"
	s13rWSLGoEnv     = "DSH_E2E_DHS13_WSL_GO"
)

// s13rWSLSetup checks the lanes' commit out on the WSL ext4 file system (as
// lanes/wsl.sh requires) from the Windows repository, and prints the directory.
// It is under $HOME, not /tmp: a systemd distro that idles out while the host
// lane runs may clear /tmp when it starts again.
const s13rWSLSetup = `set -eu
mkdir -p "$HOME/.cache/dsh-e2e"
d=$(mktemp -d "$HOME/.cache/dsh-e2e/dhs13e2e.XXXXXX")
trap 'rm -rf "$d"' ERR
git init -q "$d/r"
git -C "$d/r" fetch -q --depth 1 "$1" "$2"
git -C "$d/r" -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
printf '%s\n' "$d"`

// s13rWSLCheckoutDir returns the last non-empty line of out that names a
// checkout directory made by s13rWSLSetup, or "" when there is none.
func s13rWSLCheckoutDir(out string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "/") && strings.Contains(l, "/.cache/dsh-e2e/dhs13e2e.") {
			return l
		}
	}
	return ""
}

func s13rWSLBase() []string {
	var a []string
	if d := strings.TrimSpace(os.Getenv(s13rWSLDistroEnv)); d != "" {
		a = append(a, "-d", d)
	}
	if u := strings.TrimSpace(os.Getenv(s13rWSLUserEnv)); u != "" {
		a = append(a, "-u", u)
	}
	return a
}

// ensureLanes makes the two live lanes' checkouts of the lanes' commit: a git
// worktree on this host, and an ext4 checkout inside WSL. A host that reaches no
// second lane fails the scenario: it is "on both lanes".
func (s *s13rState) ensureLanes() error {
	if s.liveLanes != nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("this host is %s: the Windows lane cannot run live from it; run the scenario on a Windows host with WSL", runtime.GOOS)
	}
	tmp := s13rOuterT.TempDir()
	wt := filepath.Join(tmp, "wt")
	if out, err := s13rCommand(s.root, nil, "git", "worktree", "add", "--detach", wt, s.commit); err != nil {
		return fmt.Errorf("git worktree add %s: %v\n%s", s.commit[:7], err, out)
	}
	host := &s13rLiveLane{name: "win (this host)", column: "Windows", checkout: wt}
	host.command = func(ctx context.Context, rel string, env []string, name string, args ...string) *exec.Cmd {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = filepath.Join(wt, filepath.FromSlash(rel))
		c.Env = append(os.Environ(), env...)
		return c
	}
	host.remove = func() error {
		out, err := s13rCommand(s.root, nil, "git", "worktree", "remove", "--force", wt)
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
	s.liveLanes = []*s13rLiveLane{host}

	if _, err := exec.LookPath("wsl.exe"); err != nil {
		return fmt.Errorf("the WSL lane is not reachable from this host: %v", err)
	}
	common, err := s13rCommand(s.root, nil, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("git rev-parse --git-common-dir: %v\n%s", err, common)
	}
	base := s13rWSLBase()
	src, err := s13rCommand(s.root, nil, "wsl.exe", append(base, "-e", "wslpath", "-a", strings.TrimSpace(common))...)
	if err != nil {
		return fmt.Errorf("the WSL lane is not reachable from this host (wslpath): %v\n%s", err, src)
	}
	// The setup script's stdout carries only the directory; wsl.exe and git
	// notices go to stderr and must not be mistaken for it.
	setup := exec.Command("wsl.exe", append(base, "-e", "bash", "-c", s13rWSLSetup, "_", strings.TrimSpace(src), s.commit)...)
	setup.Dir = s.root
	var setupOut, setupErr bytes.Buffer
	setup.Stdout, setup.Stderr = &setupOut, &setupErr
	if err := setup.Run(); err != nil {
		return fmt.Errorf("checking %s out inside WSL: %v\n%s%s", s.commit[:7], err, setupOut.String(), setupErr.String())
	}
	dir := s13rWSLCheckoutDir(setupOut.String())
	if dir == "" {
		return fmt.Errorf("checking %s out inside WSL printed no directory:\nstdout:\n%s\nstderr:\n%s", s.commit[:7], setupOut.String(), setupErr.String())
	}
	goBin := "/usr/local/go/bin/go"
	if v := strings.TrimSpace(os.Getenv(s13rWSLGoEnv)); v != "" {
		goBin = v
	}
	wslPath := path.Dir(goBin) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	wsl := &s13rLiveLane{name: "wsl", column: "Linux (WSL)", checkout: dir + "/r"}
	wsl.command = func(ctx context.Context, rel string, env []string, name string, args ...string) *exec.Cmd {
		if name == "go" {
			name = goBin
		}
		a := append(append([]string{}, base...), "--cd", path.Join(wsl.checkout, rel), "-e", "env", "PATH="+wslPath)
		a = append(append(append(a, env...), name), args...)
		return exec.CommandContext(ctx, "wsl.exe", a...)
	}
	wsl.remove = func() error {
		out, err := s13rCommand(s.root, nil, "wsl.exe", append(base, "-e", "rm", "-rf", dir)...)
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
	s.liveLanes = append(s.liveLanes, wsl)
	return nil
}

func (s *s13rState) removeLanes() {
	for _, l := range s.liveLanes {
		if err := l.remove(); err != nil {
			s13rOuterT.Logf("removing the %s lane's checkout %s: %v", l.name, l.checkout, err)
		}
	}
	s.liveLanes = nil
}

// runPinned runs every pinned test live on both lanes in each build it names,
// whatever the record says, and keeps each one's outcome: ABSENT when the build
// of that lane's OS does not compile it (go test -list does not list it).
func (s *s13rState) runPinned() error {
	if err := s.ensureLanes(); err != nil {
		return err
	}
	env := []string{"GOFLAGS="}
	for _, l := range s.liveLanes {
		l.live = map[string]map[string]string{}
		for _, b := range []string{"default", "testhook"} {
			var names []string
			for _, p := range s.pins {
				if slices.Contains(p.builds(), b) {
					names = append(names, p.test)
				}
			}
			if len(names) == 0 {
				continue
			}
			slices.Sort(names)
			tags, pkgs := s13rBuildArgs(b)
			list, err := l.run("src/harness", env, "go", append([]string{"test", tags, "-list", "."}, pkgs...)...)
			if err != nil {
				return fmt.Errorf("%s lane: go test %s -list (%s build): %v\n%s", l.name, tags, b, err, list)
			}
			listed := map[string]bool{}
			for _, m := range s13rListed.FindAllStringSubmatch(list, -1) {
				listed[m[1]] = true
			}
			out, err := l.run("src/harness", env, "go", append([]string{"test", tags, "-count=1", "-v", "-run", "^(" + strings.Join(names, "|") + ")$"}, pkgs...)...)
			l.out += "=== " + l.name + " lane, " + b + " build ===\n" + out + "\n"
			if err != nil && l.err == nil {
				l.err = fmt.Errorf("%s lane, %s build: %v", l.name, b, err)
			}
			got := map[string]string{}
			for _, n := range names {
				got[n] = "NONE"
				if !listed[n] {
					got[n] = "ABSENT"
					continue
				}
				for _, o := range []string{"PASS", "FAIL", "SKIP"} {
					if regexp.MustCompile(`(?m)^--- ` + o + `: ` + regexp.QuoteMeta(n) + ` \(`).MatchString(out) {
						got[n] = o
						break
					}
				}
			}
			l.live[b] = got
		}
	}
	return nil
}

func (s *s13rState) recordRow(test string) (s13rControl, int, bool) {
	for _, c := range s.controls {
		for i, t := range c.tests {
			if strings.TrimSuffix(t, s13rHookSuffix) == test {
				return c, i, true
			}
		}
	}
	return s13rControl{}, 0, false
}

var s13rColumns = []string{"Windows", "Linux (WSL)"}

// pinnedListed: the record has a fake-backend row for every pinned test, with a
// recorded result on both lanes, none failed, and every pinned control passed on
// at least one lane.
func (s *s13rState) pinnedListed() error {
	funcs, err := s.testFuncs()
	if err != nil {
		return err
	}
	passed := map[string]bool{}
	var bad []string
	for _, p := range s.pins {
		if !funcs[p.test] {
			bad = append(bad, fmt.Sprintf("%s (%s) is no test under src/harness/%v", p.test, p.control, s13rTestDirs))
			continue
		}
		row, i, ok := s.recordRow(p.test)
		if !ok {
			bad = append(bad, fmt.Sprintf("%s (%s) is in no fake-backend control table of %s", p.test, p.control, s.record))
			continue
		}
		if hook := strings.HasSuffix(row.tests[i], s13rHookSuffix); hook != (p.build == "testhook") {
			bad = append(bad, fmt.Sprintf("%s:%d names %s, want the %s build", s.record, row.line, row.tests[i], p.build))
		}
		for _, col := range s13rColumns {
			rs := row.results[col]
			if len(rs) != 1 && len(rs) != len(row.tests) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s has %d results for %d tests", s.record, row.line, col, len(rs), len(row.tests)))
				continue
			}
			r := row.result(col, i)
			switch {
			case !s13rResultRe.MatchString(r):
				bad = append(bad, fmt.Sprintf("%s:%d: %s result %q of %s is not a recorded result", s.record, row.line, col, r, p.test))
			case r == "failed":
				bad = append(bad, fmt.Sprintf("%s:%d: %s failed on %s", s.record, row.line, p.test, col))
			case strings.HasPrefix(r, "passed"):
				passed[p.control] = true
			}
		}
	}
	for _, p := range s.pins {
		if !passed[p.control] {
			bad = append(bad, fmt.Sprintf("control %q: no test of it passed on either lane in %s", p.control, s.record))
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(slices.Compact(bad), "\n"))
	}
	return nil
}

// s13rWantLive maps a recorded result to the live outcome it claims on this OS.
func s13rWantLive(r string) string {
	switch {
	case strings.HasPrefix(r, "passed"):
		return "PASS"
	case strings.HasPrefix(r, "skipped ("):
		return "SKIP"
	case strings.HasPrefix(r, "not run ("):
		return "ABSENT"
	}
	return ""
}

func (s *s13rState) liveOutput(msg string) error {
	for _, l := range s.liveLanes {
		out := l.out
		if len(out) > 8000 {
			out = "...\n" + out[len(out)-8000:]
		}
		msg += "\n" + out
	}
	return errors.New(msg)
}

// outcomesMatch: on each lane, every pinned test's live outcome is what the
// record claims in that lane's column, and no go test run of the lane failed.
func (s *s13rState) outcomesMatch() error {
	var bad []string
	failed := false
	for _, l := range s.liveLanes {
		for _, p := range s.pins {
			row, i, ok := s.recordRow(p.test)
			if !ok {
				bad = append(bad, p.test+": not in the record")
				continue
			}
			r := row.result(l.column, i)
			want := s13rWantLive(r)
			for _, b := range p.builds() {
				got := l.live[b][p.test]
				failed = failed || got == "FAIL"
				if want == "" || got != want {
					bad = append(bad, fmt.Sprintf("%s [%s lane, %s build] (%s): recorded %q on %s, live %s", p.test, l.name, b, p.control, r, l.column, got))
				}
			}
		}
		if l.err != nil {
			bad = append(bad, l.err.Error())
			failed = true
		}
	}
	if len(s.liveLanes) < 2 {
		bad = append(bad, fmt.Sprintf("%d of 2 lanes ran the pinned tests live", len(s.liveLanes)))
	}
	if len(bad) == 0 {
		return nil
	}
	if failed {
		return s.liveOutput(strings.Join(bad, "\n"))
	}
	return errors.New(strings.Join(bad, "\n"))
}

// everyPinPassedLive: a skip or a build that leaves a test out proves nothing, so
// each pinned test must PASS live, in every build it names, on at least one lane.
func (s *s13rState) everyPinPassedLive() error {
	var bad []string
	for _, p := range s.pins {
		for _, b := range p.builds() {
			var seen []string
			pass := false
			for _, l := range s.liveLanes {
				got := l.live[b][p.test]
				seen = append(seen, l.name+"="+got)
				pass = pass || got == "PASS"
			}
			if !pass {
				bad = append(bad, fmt.Sprintf("%s [%s build] (%s) passed live on no lane: %s", p.test, b, p.control, strings.Join(seen, ", ")))
			}
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "\n"))
	}
	return nil
}

// ---- harness-gate, live on both lanes ----

const (
	s13rBudgetEnv = "DSH_E2E_DHS13_GATE_BUDGET"
	// s13rReserve is the part of the go test deadline the gate run leaves to the
	// steps after it and the tests after this one in the package.
	s13rReserve = 5 * time.Minute
	// s13rNoDeadline bounds the run when go test has no deadline (-timeout 0).
	s13rNoDeadline = 3 * time.Hour
)

// s13rGateBudget is the time the live gate run may take: DSH_E2E_DHS13_GATE_BUDGET
// when set, else all the go test deadline leaves after s13rReserve; never past
// that deadline, so a run that cannot finish fails the step instead of panicking
// the package. A run that does not finish within it fails: every gate must pass.
func s13rGateBudget() (time.Duration, error) {
	budget, set := s13rNoDeadline, false
	if v := strings.TrimSpace(os.Getenv(s13rBudgetEnv)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("%s=%q is not a positive duration", s13rBudgetEnv, v)
		}
		budget, set = d, true
	}
	if dl, ok := s13rOuterT.Deadline(); ok {
		left := time.Until(dl) - s13rReserve
		if left <= 0 {
			return 0, fmt.Errorf("go test leaves %s, under the %s reserve: no time to run every gate on both lanes; run with a larger -timeout", time.Until(dl).Round(time.Second), s13rReserve)
		}
		if !set || left < budget {
			budget = left
		}
	}
	return budget, nil
}

// s13rStopTree ends the gate and the go commands under it.
func s13rStopTree(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
	}
	return p.Signal(os.Interrupt)
}

// liveGate builds harness-gate at the lanes' commit in each lane's checkout and
// runs its whole gate list there (as the lane scripts do, without --evidence),
// both lanes at once, until it ends or the budget runs out.
func (s *s13rState) liveGate() error {
	b, err := s13rGateBudget()
	if err != nil {
		return err
	}
	s.gateBudget = b
	if err := s.ensureLanes(); err != nil {
		return err
	}
	env := []string{"GOWORK=off", "GOFLAGS="}
	var wg sync.WaitGroup
	for _, l := range s.liveLanes {
		bin := "../harness-gate"
		if l.column == "Windows" {
			bin += ".exe"
		}
		if out, err := l.run("tools/harness-gate", env, "go", "build", "-o", "../../"+bin, "."); err != nil {
			return fmt.Errorf("%s lane: building harness-gate at %s: %v\n%s", l.name, s.commit[:7], err, out)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var cmd *exec.Cmd
			var ctx context.Context
			var cancel context.CancelFunc
			if l.column == "Windows" {
				ctx, cancel = context.WithTimeout(context.Background(), s.gateBudget)
				cmd = l.command(ctx, "", env, filepath.Join(l.checkout, "..", "harness-gate.exe"), "--root", l.checkout)
				cmd.Cancel = func() error { return s13rStopTree(cmd.Process) }
			} else {
				// timeout(1) interrupts the gate inside WSL; the context only backs it up.
				ctx, cancel = context.WithTimeout(context.Background(), s.gateBudget+2*time.Minute)
				secs := strconv.Itoa(int(s.gateBudget.Seconds()))
				cmd = l.command(ctx, "", env, "timeout", "--signal=INT", "--kill-after=30", secs, path.Join(path.Dir(l.checkout), "harness-gate"), "--root", l.checkout)
			}
			defer cancel()
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			cmd.WaitDelay = 30 * time.Second
			l.gateErr = cmd.Run()
			l.gateOut = strings.ReplaceAll(buf.String(), "\r\n", "\n")
			var ee *exec.ExitError
			l.gateTimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded) ||
				(l.column != "Windows" && errors.As(l.gateErr, &ee) && ee.ExitCode() == 124)
		}()
	}
	wg.Wait()
	return nil
}

// liveGatePassed: on each lane the live run ran every gate of its list to the
// end, each one passed, and it exited 0 with "PASSED all N gates". A run the
// budget cut short proves nothing about the gates it did not reach, so it fails.
func (s *s13rState) liveGatePassed() error {
	if len(s.liveLanes) < 2 {
		return fmt.Errorf("%d of 2 lanes ran harness-gate live", len(s.liveLanes))
	}
	var bad []string
	for _, l := range s.liveLanes {
		if err := s.laneGatePassed(l); err != nil {
			bad = append(bad, err.Error())
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "\n\n"))
	}
	return nil
}

func (s *s13rState) laneGatePassed(l *s13rLiveLane) error {
	where := fmt.Sprintf("%s lane: harness-gate --root <checkout of %s>", l.name, s.commit[:7])
	gates := s13rGateLine.FindAllStringSubmatch(l.gateOut, -1)
	if len(gates) == 0 {
		return fmt.Errorf("%s started no gate (%v):\n%s", where, l.gateErr, l.gateOut)
	}
	if s13rFailLine.MatchString(l.gateOut) {
		return fmt.Errorf("%s failed a gate:\n%s", where, l.gateOut)
	}
	passed := map[string]bool{}
	for _, m := range s13rPassLine.FindAllStringSubmatch(l.gateOut, -1) {
		passed[m[1]] = true
	}
	last := gates[len(gates)-1]
	if l.gateTimedOut {
		return fmt.Errorf("%s: the %s budget (%s, or the go test -timeout) ran out inside gate %s/%s %s; the gates it did not reach are unproven:\n%s", where, s.gateBudget, s13rBudgetEnv, last[1], last[2], last[3], l.gateOut)
	}
	if l.gateErr != nil {
		return fmt.Errorf("%s exited %v:\n%s", where, l.gateErr, l.gateOut)
	}
	n, err := strconv.Atoi(gates[0][2])
	if err != nil || len(gates) != n {
		return fmt.Errorf("%s ran %d gates of a list of %s:\n%s", where, len(gates), gates[0][2], l.gateOut)
	}
	for i, g := range gates {
		if g[1] != strconv.Itoa(i+1) || g[2] != gates[0][2] || !passed[g[3]] {
			return fmt.Errorf("%s: gate %s/%s %s did not pass:\n%s", where, g[1], g[2], g[3], l.gateOut)
		}
	}
	if all := s13rAllLine.FindStringSubmatch(l.gateOut); all == nil || all[1] != gates[0][2] {
		return fmt.Errorf("%s ended without PASSED all %s gates:\n%s", where, gates[0][2], l.gateOut)
	}
	s13rOuterT.Logf("%s: all %d gates passed live", where, n)
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_dh_s_13_scenarios_and_built_record(ctx *godog.ScenarioContext) {
	s := &s13rState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s13rState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.removeLanes()
		return c, nil
	})

	ctx.Step(`^the nine DH-S-13 scenario files in "([^"]*)" and "([^"]*)"$`, s.givenScenarioFiles)
	ctx.Step(`^"(go -C src/harness vet -tags scenario \./internal/testkit/scenarios/\.\.\.)" runs$`, s.runVet)
	ctx.Step(`^the gate's scenario-index check runs on the checkout$`, s.runIndexCheck)
	ctx.Step(`^vet exits 0$`, s.vetExits0)
	ctx.Step(`^the gate's scenario-index check passes$`, s.indexCheckPasses)
	ctx.Step(`^each ID is bound by exactly one scenario test in its named file, built only under the "([^"]*)" tag$`, s.boundOnceInFile)
	ctx.Step(`^each ID appears exactly once in the index, under its executing stage, authored "([^"]*)" with its requires$`, s.indexedOnce)
	ctx.Step(`^the default untagged test run lists none of the nine scenario tests$`, s.defaultRunNone)

	ctx.Step(`^the merged commit of stage DH-S-13 and its record "([^"]*)"$`, s.givenMergedCommit)
	ctx.Step(`^the security controls of steps 13\.2 to 13\.7 and their fake-backend tests$`, s.givenPins)
	ctx.Step(`^the pinned security-control tests run live on both lanes in their builds$`, s.runPinned)
	ctx.Step(`^harness-gate runs live on both lanes at the lanes' commit within its budget$`, s.liveGate)
	ctx.Step(`^the lane records harness-gate wrote with "([^"]*)" and "([^"]*)" for the lanes' commit are read$`, s.lanesRan)
	ctx.Step(`^every pinned test is listed in a fake-backend control table of the record with a result on both lanes and none failed$`, s.pinnedListed)
	ctx.Step(`^the record lists every security-control test of its commands in a fake-backend table$`, s.everyCommandTestListed)
	ctx.Step(`^every pinned test's live outcome on each lane matches the record's result for that lane$`, s.outcomesMatch)
	ctx.Step(`^every pinned test passed live on at least one lane in every build it names$`, s.everyPinPassedLive)
	ctx.Step(`^on each lane the live harness-gate run ran every gate of its list and every gate passed$`, s.liveGatePassed)
	ctx.Step(`^the "([^"]*)" lane record is an intact clean-tree evidence record of that commit on "([^"]*)"$`, s.laneRecordIntact)
	ctx.Step(`^every gate of each lane record passes and the record exits 0$`, s.everyGatePasses)
	ctx.Step(`^the record names both lane results with the commit SHA$`, s.recordNamesLanes)
	ctx.Step(`^"harness-gate records" accepts the shape of every DH-S-13 record of the story$`, s.gateRecordsAccepts)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_dh_s_13_scenarios_and_built_record(t *testing.T) {
	s13rOuterT = t
	defer func() { s13rOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "dh_s_13_sandbox_launch_linux_backends_and_shell_tools_dh_s_13_scenarios_and_built_record",
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_dh_s_13_scenarios_and_built_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_dh_s_13_scenarios_and_built_record.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
