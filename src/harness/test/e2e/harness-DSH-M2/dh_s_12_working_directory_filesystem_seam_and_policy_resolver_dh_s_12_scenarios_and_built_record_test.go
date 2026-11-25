//go:build e2e

package e2e

// Step definitions of
// dh_s_12_working_directory_filesystem_seam_and_policy_resolver_dh_s_12_scenarios_and_built_record.feature:
// the five real-environment scenarios DH-S-12 authors compile under the scenario tag
// and are indexed once under their executing stage (M2-DOD-5), and the stage's
// merged commit passed every gate on both local lanes, as its evidence record says
// (M2-DOD-1). Proof: service:go-toolchain -- the real go toolchain, the gate's own
// acceptance package and its "records" subcommand, and the lanes' --evidence
// records of the merged commit, verified byte for byte (SHA-256 trailer).

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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

var s12rOuterT *testing.T

// s12rCommitEnv names the merged commit of DH-S-12 explicitly; when unset it is
// found on the checkout's history by s12rCommitSubject.
const (
	s12rCommitEnv     = "DSH_E2E_DHS12_COMMIT"
	s12rCommitSubject = `^\[impl\] DH-S-12 Scenarios and Built Record`
	s12rRunsDir       = "docs/stories/harness-DSH-M1/evidence/runs"
)

type s12rWant struct {
	id, file, stage string
	requires        []string
}

type s12rEntry struct {
	Stage    string   `json:"stage"`
	ID       string   `json:"id"`
	Authored string   `json:"authored"`
	Requires []string `json:"requires"`
}

type s12rScenario struct {
	ID   string `json:"id"`
	File string `json:"file"`
	Test string `json:"test"`
}

type s12rReport struct {
	BuiltThrough string         `json:"built_through"`
	Entries      []s12rEntry    `json:"entries"`
	Scenarios    []s12rScenario `json:"scenarios"`
	CheckErr     string         `json:"check_err"`
	BoundaryErr  string         `json:"boundary_err"`
}

type s12rLane struct {
	name, path string
	data       []byte
}

type s12rState struct {
	root   string
	dir    string
	wants  []s12rWant
	vetOut string
	vetErr error
	report s12rReport

	commit string
	lanes  map[string]*s12rLane
}

// s12rGateProgram is the gate's scenario-index check (acceptance.Check and
// acceptance.CheckBoundary over the index and the scanned scenario tests of the
// three modules, as tools/harness-gate's scenario-index gate runs them), printed as
// JSON with what it read.
const s12rGateProgram = `package main

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
		"built_through": x.BuiltThrough,
		"entries":       es,
		"scenarios":     ss,
		"check_err":     errString(acceptance.Check(x, sc)),
		"boundary_err":  errString(acceptance.CheckBoundary(root, x, sc)),
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`

func s12rIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func s12rRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if s12rIsRegular(filepath.Join(dir, "go.work")) && s12rIsRegular(filepath.Join(dir, "test", "harness", "acceptance-index.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func s12rCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func s12rSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *s12rState) ensureRoot() error {
	if s.root != "" {
		return nil
	}
	root, err := s12rRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

// ---- Scenario: New scenarios compile and are indexed ----

func (s *s12rState) givenScenarioFiles(dir, index string, table *godog.Table) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	if !s12rIsRegular(filepath.Join(s.root, filepath.FromSlash(index))) {
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
		s.wants = append(s.wants, s12rWant{id: cell("id"), file: cell("file"), stage: cell("stage"), requires: s12rSplit(cell("requires"))})
	}
	if len(s.wants) != 5 {
		return fmt.Errorf("DH-S-12 authors five scenarios, the table lists %d", len(s.wants))
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

func (s *s12rState) runVet(command string) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	s.vetOut, s.vetErr = s12rCommand(s.root, []string{"GOFLAGS="}, "go", args[1:]...)
	return nil
}

func (s *s12rState) runIndexCheck() error {
	tmp := s12rOuterT.TempDir()
	gate := filepath.Join(s.root, "tools", "harness-gate")
	gomod := fmt.Sprintf("module dsh.e2e/s12rindex\n\ngo 1.27.4\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
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
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(s12rGateProgram), 0o644); err != nil {
		return err
	}
	out, err := s12rCommand(tmp, []string{"GOWORK=off", "GOFLAGS=-mod=mod"}, "go", "run", ".", s.root)
	if err != nil {
		return fmt.Errorf("the gate's scenario-index check: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &s.report); err != nil {
		return fmt.Errorf("scenario-index check output: %v\n%s", err, out)
	}
	return nil
}

func (s *s12rState) vetExits0() error {
	if s.vetErr != nil {
		return fmt.Errorf("vet -tags scenario: %v\n%s", s.vetErr, s.vetOut)
	}
	return nil
}

func (s *s12rState) indexCheckPasses() error {
	if s.report.CheckErr != "" {
		return fmt.Errorf("acceptance.Check: %s", s.report.CheckErr)
	}
	if s.report.BoundaryErr != "" {
		return fmt.Errorf("acceptance.CheckBoundary: %s", s.report.BoundaryErr)
	}
	return nil
}

func (s *s12rState) bindings(id string) []s12rScenario {
	var out []s12rScenario
	for _, sc := range s.report.Scenarios {
		if sc.ID == id {
			out = append(out, sc)
		}
	}
	return out
}

func (s *s12rState) boundOnceInFile(tag string) error {
	for _, w := range s.wants {
		bs := s.bindings(w.id)
		if len(bs) != 1 {
			return fmt.Errorf("%s is bound %d times: %+v", w.id, len(bs), bs)
		}
		want := s.dir + "/" + w.file
		if bs[0].File != want || !strings.HasPrefix(bs[0].Test, "Test") {
			return fmt.Errorf("%s bound as %+v, want a TestXxx in %s", w.id, bs[0], want)
		}
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

func (s *s12rState) indexedOnce(authored string) error {
	for _, w := range s.wants {
		var es []s12rEntry
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

// defaultRunNone lists the scenario package's tests with the gate's explicit empty
// tag list (none of the five is compiled in) and with -tags scenario (each is).
func (s *s12rState) defaultRunNone() error {
	harness := filepath.Join(s.root, "src", "harness")
	pkg := "./" + strings.TrimPrefix(s.dir, "src/harness/")
	plain, err := s12rCommand(harness, []string{"GOFLAGS="}, "go", "test", "-tags=", "-list", ".", "-count=1", pkg)
	if err != nil {
		return fmt.Errorf("go test -tags= -list: %v\n%s", err, plain)
	}
	tagged, err := s12rCommand(harness, []string{"GOFLAGS="}, "go", "test", "-tags", "scenario", "-list", ".", "-count=1", pkg)
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

// ---- Scenario: S12 gate green on both lanes ----

var s12rHex = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *s12rState) givenMergedCommit() error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	ref := strings.TrimSpace(os.Getenv(s12rCommitEnv))
	if ref == "" {
		out, err := s12rCommand(s.root, nil, "git", "log", "--format=%H", "-E", "--grep", s12rCommitSubject, "-n", "1", "HEAD")
		if err != nil {
			return fmt.Errorf("git log --grep: %v\n%s", err, out)
		}
		ref = strings.TrimSpace(out)
		if ref == "" {
			return fmt.Errorf("no commit %q on this checkout's history; set %s", s12rCommitSubject, s12rCommitEnv)
		}
	}
	out, err := s12rCommand(s.root, nil, "git", "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("git rev-parse %s: %v\n%s", ref, err, out)
	}
	s.commit = strings.TrimSpace(out)
	if !s12rHex.MatchString(s.commit) {
		return fmt.Errorf("unexpected commit %q", s.commit)
	}
	if out, err := s12rCommand(s.root, nil, "git", "merge-base", "--is-ancestor", s.commit, "HEAD"); err != nil {
		return fmt.Errorf("%s is not on this checkout's history: %v\n%s", s.commit, err, out)
	}
	return nil
}

// lanesRan finds the --evidence record each lane script wrote for the merged
// commit (lanes/win.ps1 and lanes/wsl.sh name it <lane>-<sha7>-<UTC stamp>.txt
// under s12rRunsDir); the latest one of each lane is the lane's result.
func (s *s12rState) lanesRan(win, wsl string) error {
	s.lanes = map[string]*s12rLane{}
	for _, script := range []string{win, wsl} {
		lane := strings.TrimSuffix(strings.TrimSuffix(script, ".ps1"), ".sh")
		src := filepath.Join(s.root, "tools", "harness-gate", "lanes", script)
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("lane script %s: %v", script, err)
		}
		if !bytes.Contains(data, []byte("--evidence")) || !bytes.Contains(data, []byte(lane+"-")) {
			return fmt.Errorf("lane script %s does not write a %s-<sha7> evidence record", script, lane)
		}
		pattern := filepath.Join(s.root, filepath.FromSlash(s12rRunsDir), lane+"-"+s.commit[:7]+"-*Z.txt")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return fmt.Errorf("no %s lane record of %s (%s): run tools/harness-gate/lanes/%s on that commit and commit its record", lane, s.commit[:7], filepath.ToSlash(pattern), script)
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
		s.lanes[lane] = &s12rLane{name: lane, path: filepath.ToSlash(rel), data: b}
	}
	return nil
}

// s12rHeader returns the "key: value" lines of the record before "--- output ---".
func s12rHeader(data []byte) map[string]string {
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

func (s *s12rState) laneRecordIntact(lane, goos string) error {
	l, ok := s.lanes[lane]
	if !ok {
		return fmt.Errorf("no %s lane record", lane)
	}
	data := bytes.ReplaceAll(l.data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("harness-gate evidence record\n")) {
		return fmt.Errorf("%s is not a harness-gate evidence record", l.path)
	}
	// The trailer is the SHA-256 of every byte before it (evidence.go): the record
	// is the lane's own output, not edited since.
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
	h := s12rHeader(data)
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
	s12rGateLine = regexp.MustCompile(`(?m)^harness-gate: gate (\d+)/(\d+) (\S+) --`)
	s12rPassLine = regexp.MustCompile(`(?m)^harness-gate: PASS (\S+) \(`)
	s12rFailLine = regexp.MustCompile(`(?m)^harness-gate: FAIL`)
	s12rAllLine  = regexp.MustCompile(`(?m)^harness-gate: PASSED all (\d+) gates$`)
)

func (s *s12rState) everyGatePasses() error {
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		text := strings.ReplaceAll(string(l.data), "\r\n", "\n")
		gates := s12rGateLine.FindAllStringSubmatch(text, -1)
		if len(gates) == 0 {
			return fmt.Errorf("%s runs no gate", l.path)
		}
		total := gates[0][2]
		passed := map[string]bool{}
		for _, m := range s12rPassLine.FindAllStringSubmatch(text, -1) {
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
		if s12rFailLine.MatchString(text) {
			return fmt.Errorf("%s reports a failed gate", l.path)
		}
		all := s12rAllLine.FindStringSubmatch(text)
		if all == nil || all[1] != total {
			return fmt.Errorf("%s does not report PASSED all %s gates", l.path, total)
		}
		if h := s12rTrailerField(text, "exit"); h != "0" {
			return fmt.Errorf("%s exits %q, want 0", l.path, h)
		}
	}
	return nil
}

func s12rTrailerField(text, key string) string {
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

// evidenceRecordsLanes requires the stage record to name, on one line each, the
// lane script, its record file and the merged commit, with a passing result.
func (s *s12rState) evidenceRecordsLanes(record string) error {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(record)))
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.Contains(text, s.commit[:7]) {
		return fmt.Errorf("%s does not name the merged commit %s", record, s.commit[:7])
	}
	scripts := map[string]string{"win": "win.ps1", "wsl": "wsl.sh"}
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		base := filepath.Base(l.path)
		found := false
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, scripts[lane]) && strings.Contains(line, base) &&
				strings.Contains(line, s.commit[:7]) && strings.Contains(strings.ToLower(line), "exit 0") {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s has no line naming %s, its record %s, the commit %s and exit 0", record, scripts[lane], base, s.commit[:7])
		}
		target := filepath.Join(filepath.Dir(filepath.Join(s.root, filepath.FromSlash(record))), "..", "..", "harness-DSH-M1", "evidence", "runs", base)
		if !s12rIsRegular(target) {
			return fmt.Errorf("%s names %s, which is not under %s", record, base, s12rRunsDir)
		}
	}
	return nil
}

// s12rEvidenceDir and s12rStageRecord are the story's evidence directory and the
// DH-S-12 stage record "harness-gate records" checks (closure.RecordPath).
const (
	s12rEvidenceDir = "docs/stories/harness-DSH-M2/evidence"
	s12rStageRecord = s12rEvidenceDir + "/DH-S-12.md"
)

var s12rRecordsOK = regexp.MustCompile(`(?m)^harness-gate records: harness:DSH-M2: ([0-9]+) stage records and ` +
	regexp.QuoteMeta(s12rEvidenceDir+"/closure.md") + ` hold their shape\s*$`)

// gateRecordsAccepts: "harness-gate records" over the checkout accepts the story's
// stage records and closure record, and DH-S-12.md is one of the records it examines
// -- the same run over a copy of the evidence directory without DH-S-12.md fails
// naming it. The subcommand reads stage and closure records only; the lane run
// records under s12rRunsDir are verified by the earlier steps of this scenario.
func (s *s12rState) gateRecordsAccepts() error {
	gate := filepath.Join(s.root, "tools", "harness-gate")
	env := []string{"GOWORK=off", "GOFLAGS="}
	if !s12rIsRegular(filepath.Join(s.root, filepath.FromSlash(s12rStageRecord))) {
		return fmt.Errorf("%s is missing", s12rStageRecord)
	}
	out, err := s12rCommand(gate, env, "go", "run", ".", "records", "--root", s.root)
	if err != nil {
		return fmt.Errorf("harness-gate records: %v\n%s", err, out)
	}
	m := s12rRecordsOK.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("harness-gate records did not report the harness:DSH-M2 stage records and closure record holding their shape:\n%s", out)
	}
	if n, _ := strconv.Atoi(m[1]); n < 1 {
		return fmt.Errorf("harness-gate records examined %d stage records:\n%s", n, out)
	}

	probe := s12rOuterT.TempDir()
	if err := s12rCopyFile(filepath.Join(s.root, "test", "harness", "acceptance-index.yaml"), filepath.Join(probe, "test", "harness", "acceptance-index.yaml")); err != nil {
		return err
	}
	src := filepath.Join(s.root, filepath.FromSlash(s12rEvidenceDir))
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.Type().IsRegular() || e.Name() == filepath.Base(s12rStageRecord) {
			continue
		}
		if err := s12rCopyFile(filepath.Join(src, e.Name()), filepath.Join(probe, filepath.FromSlash(s12rEvidenceDir), e.Name())); err != nil {
			return err
		}
	}
	out, err = s12rCommand(gate, env, "go", "run", ".", "records", "--root", probe)
	if err == nil {
		return fmt.Errorf("harness-gate records accepted a tree without %s, so it does not examine that record:\n%s", s12rStageRecord, out)
	}
	if !strings.Contains(out, "no evidence record "+s12rStageRecord) {
		return fmt.Errorf("harness-gate records without %s failed without naming it: %v\n%s", s12rStageRecord, err, out)
	}
	return nil
}

func s12rCopyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_dh_s_12_scenarios_and_built_record(ctx *godog.ScenarioContext) {
	s := &s12rState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s12rState{}
		return c, nil
	})

	ctx.Step(`^the five DH-S-12 scenario files in "([^"]*)" and "([^"]*)"$`, s.givenScenarioFiles)
	ctx.Step(`^"(go -C src/harness vet -tags scenario \./internal/testkit/scenarios/\.\.\.)" runs$`, s.runVet)
	ctx.Step(`^the gate's scenario-index check runs on the checkout$`, s.runIndexCheck)
	ctx.Step(`^vet exits 0$`, s.vetExits0)
	ctx.Step(`^the gate's scenario-index check passes$`, s.indexCheckPasses)
	ctx.Step(`^each ID is bound by exactly one scenario test in its named file, built only under the "([^"]*)" tag$`, s.boundOnceInFile)
	ctx.Step(`^each ID appears exactly once in the index, under its executing stage, authored "([^"]*)" with its requires$`, s.indexedOnce)
	ctx.Step(`^the default untagged test run lists none of the five scenario tests$`, s.defaultRunNone)

	ctx.Step(`^the merged commit of stage DH-S-12$`, s.givenMergedCommit)
	ctx.Step(`^harness-gate runs on "([^"]*)" and "([^"]*)" for that commit$`, s.lanesRan)
	ctx.Step(`^the "([^"]*)" lane record is an intact clean-tree evidence record of that commit on "([^"]*)"$`, s.laneRecordIntact)
	ctx.Step(`^every gate of each lane record passes and the record exits 0$`, s.everyGatePasses)
	ctx.Step(`^"([^"]*)" records both lane results with the commit SHA$`, s.evidenceRecordsLanes)
	ctx.Step(`^"harness-gate records" accepts the shape of every DH-S-12 record of the story$`, s.gateRecordsAccepts)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_dh_s_12_scenarios_and_built_record(t *testing.T) {
	s12rOuterT = t
	defer func() { s12rOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "dh_s_12_working_directory_filesystem_seam_and_policy_resolver_dh_s_12_scenarios_and_built_record",
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_dh_s_12_scenarios_and_built_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_dh_s_12_scenarios_and_built_record.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
