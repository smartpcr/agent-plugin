//go:build e2e

package e2e

// Step definitions of
// dh_s_15_listener_sign_in_stdio_and_sdk_server_dh_s_15_scenarios_and_built_record.feature:
// the five HOST scenarios DH-S-15 authors compile under the scenario tag on every
// GOOS, are indexed once under their executing stage and never reach the Go client
// src/sdk/sdk (M2-DOD-5); and the stage's merged commit leaves src/sdk/protocol
// unchanged (TC-12g) and passed every gate on both local lanes, as its committed
// --evidence records and its evidence record say (M2-DOD-1). Proof:
// service:go-toolchain -- the real go toolchain, the gate's own acceptance package,
// and the lanes' records verified byte for byte (SHA-256 trailer). The lanes are
// not run here: they run outside the test (Full harness gate policy) and their
// committed records are the evidence.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
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

var s15rOuterT *testing.T

const (
	s15rCommitEnv     = "DSH_E2E_DHS15_COMMIT"
	s15rCommitSubject = `^\[impl\] DH-S-15 Scenarios and Built Record`
	s15rRunsDir       = "docs/stories/harness-DSH-M1/evidence/runs"
	s15rIndex         = "test/harness/acceptance-index.yaml"
)

type s15rWant struct {
	id, file, stage string
	requires        []string
}

type s15rEntry struct {
	Stage    string   `json:"stage"`
	ID       string   `json:"id"`
	Authored string   `json:"authored"`
	Requires []string `json:"requires"`
}

type s15rScenario struct {
	ID   string `json:"id"`
	File string `json:"file"`
	Test string `json:"test"`
}

type s15rReport struct {
	Entries     []s15rEntry    `json:"entries"`
	Scenarios   []s15rScenario `json:"scenarios"`
	CheckErr    string         `json:"check_err"`
	BoundaryErr string         `json:"boundary_err"`
}

type s15rLane struct {
	path string
	data []byte
}

type s15rState struct {
	root   string
	dir    string
	wants  []s15rWant
	goos   []string
	vetErr map[string]string
	report s15rReport

	commit string
	lanes  map[string]*s15rLane
}

// s15rGateProgram is the gate's scenario-index check (acceptance.Check and
// acceptance.CheckBoundary over the index and the scanned scenario tests of the
// three modules, as tools/harness-gate's scenario-index gate runs them), printed as
// JSON with what it read.
const s15rGateProgram = `package main

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

func s15rIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func s15rRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if s15rIsRegular(filepath.Join(dir, "go.work")) && s15rIsRegular(filepath.Join(dir, filepath.FromSlash(s15rIndex))) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func s15rCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func s15rSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *s15rState) ensureRoot() error {
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

func (s *s15rState) pkg() string { return "./" + strings.TrimPrefix(s.dir, "src/harness/") }

// s15rGOOSEnv cross-compiles for goos without cgo, so every platform's files of the
// package type-check on this host.
func s15rGOOSEnv(goos string) []string {
	return []string{"GOFLAGS=", "GOOS=" + goos, "GOARCH=amd64", "CGO_ENABLED=0"}
}

// ---- Scenario: Authored S15 scenarios compile and are indexed ----

func (s *s15rState) givenHostFiles(dir, index string, table *godog.Table) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	if !s15rIsRegular(filepath.Join(s.root, filepath.FromSlash(index))) {
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
		s.wants = append(s.wants, s15rWant{id: cell("id"), file: cell("file"), stage: cell("stage"), requires: s15rSplit(cell("requires"))})
	}
	if len(s.wants) != 5 {
		return fmt.Errorf("DH-S-15 authors five HOST scenarios, the table lists %d", len(s.wants))
	}
	for _, w := range s.wants {
		data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(dir), w.file))
		if err != nil {
			return fmt.Errorf("%s: %v", w.id, err)
		}
		if !regexp.MustCompile(`(?m)^//dsh:scenario\s+` + regexp.QuoteMeta(w.id) + `\s*$`).Match(data) {
			return fmt.Errorf("%s does not carry //dsh:scenario %s", w.file, w.id)
		}
	}
	return nil
}

func (s *s15rState) runVet(command, a, b, c string) error {
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	s.goos = []string{a, b, c}
	s.vetErr = map[string]string{}
	for _, goos := range s.goos {
		if out, err := s15rCommand(s.root, s15rGOOSEnv(goos), "go", args[1:]...); err != nil {
			s.vetErr[goos] = fmt.Sprintf("%v\n%s", err, out)
		}
	}
	return nil
}

func (s *s15rState) vetExits0() error {
	for _, goos := range s.goos {
		if msg, bad := s.vetErr[goos]; bad {
			return fmt.Errorf("vet -tags scenario on %s: %s", goos, msg)
		}
	}
	return nil
}

// s15rGoDirective returns the `go` directive of a go.mod so the temporary
// module tracks the repository's toolchain pin instead of a copied literal.
func s15rGoDirective(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: no go directive", path)
}

func (s *s15rState) runIndexCheck() error {
	tmp := s15rOuterT.TempDir()
	gate := filepath.Join(s.root, "tools", "harness-gate")
	goVersion, err := s15rGoDirective(filepath.Join(gate, "go.mod"))
	if err != nil {
		return err
	}
	gomod := fmt.Sprintf("module dsh.e2e/s15rindex\n\ngo %s\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
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
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(s15rGateProgram), 0o644); err != nil {
		return err
	}
	out, err := s15rCommand(tmp, []string{"GOWORK=off", "GOFLAGS=-mod=mod"}, "go", "run", ".", s.root)
	if err != nil {
		return fmt.Errorf("the gate's scenario-index check: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &s.report); err != nil {
		return fmt.Errorf("scenario-index check output: %v\n%s", err, out)
	}
	return nil
}

func (s *s15rState) indexCheckPasses() error {
	if s.report.CheckErr != "" {
		return fmt.Errorf("acceptance.Check: %s", s.report.CheckErr)
	}
	if s.report.BoundaryErr != "" {
		return fmt.Errorf("acceptance.CheckBoundary: %s", s.report.BoundaryErr)
	}
	return nil
}

func (s *s15rState) bindings(id string) []s15rScenario {
	var out []s15rScenario
	for _, sc := range s.report.Scenarios {
		if sc.ID == id {
			out = append(out, sc)
		}
	}
	return out
}

func (s *s15rState) parse(file string, mode parser.Mode) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), filepath.Join(s.root, filepath.FromSlash(s.dir), file), nil, mode)
}

// s15rBuildLine returns the file's //go:build line (before the package clause).
func s15rBuildLine(f *ast.File) string {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if t := strings.TrimSpace(c.Text); strings.HasPrefix(t, "//go:build ") {
				return t
			}
		}
	}
	return ""
}

func (s *s15rState) boundOnceInFile(tag string) error {
	for _, w := range s.wants {
		bs := s.bindings(w.id)
		if len(bs) != 1 {
			return fmt.Errorf("%s is bound %d times: %+v", w.id, len(bs), bs)
		}
		want := s.dir + "/" + w.file
		if bs[0].File != want || !strings.HasPrefix(bs[0].Test, "Test") {
			return fmt.Errorf("%s bound as %+v, want a TestXxx in %s", w.id, bs[0], want)
		}
		f, err := s.parse(w.file, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		if got := s15rBuildLine(f); got != "//go:build "+tag {
			return fmt.Errorf("%s has build line %q, want //go:build %s", want, got, tag)
		}
	}
	return nil
}

func (s *s15rState) indexedOnce(authored string) error {
	for _, w := range s.wants {
		var es []s15rEntry
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
func (s *s15rState) defaultRunNone() error {
	harness := filepath.Join(s.root, "src", "harness")
	plain, err := s15rCommand(harness, []string{"GOFLAGS="}, "go", "test", "-tags=", "-list", ".", s.pkg())
	if err != nil {
		return fmt.Errorf("go test -tags= -list: %v\n%s", err, plain)
	}
	tagged, err := s15rCommand(harness, []string{"GOFLAGS="}, "go", "test", "-tags", "scenario", "-list", ".", s.pkg())
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

func (s *s15rState) noFileImports(path string) error {
	for _, w := range s.wants {
		f, err := s.parse(w.file, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == path || strings.HasPrefix(p, path+"/") {
				return fmt.Errorf("%s (%s) imports %s (M2-DOD-5)", w.file, w.id, p)
			}
		}
	}
	return nil
}

// noDependency: the package's test build under tag, on each GOOS of the vet step,
// reaches path through no import at all, the shared helpers included.
func (s *s15rState) noDependency(tag, path string) error {
	harness := filepath.Join(s.root, "src", "harness")
	for _, goos := range s.goos {
		out, err := s15rCommand(harness, s15rGOOSEnv(goos), "go", "list", "-deps", "-test", "-tags", tag, s.pkg())
		if err != nil {
			return fmt.Errorf("go list -deps on %s: %v\n%s", goos, err, out)
		}
		deps := strings.Fields(out)
		if !slices.Contains(deps, "github.com/smartpcr/agent-plugin/src/sdk/jsonrpc") {
			return fmt.Errorf("on %s the scenario package does not reach src/sdk/jsonrpc, so the list is not of the HOST scenarios' build:\n%s", goos, out)
		}
		for _, d := range deps {
			if d == path || strings.HasPrefix(d, path+"/") {
				return fmt.Errorf("on %s the scenario package depends on %s (M2-DOD-5)", goos, d)
			}
		}
	}
	return nil
}

// ---- Scenario: S15 gate green on both lanes ----

var s15rHex = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *s15rState) givenMergedCommit() error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	ref := strings.TrimSpace(os.Getenv(s15rCommitEnv))
	if ref == "" {
		out, err := s15rCommand(s.root, nil, "git", "log", "--format=%H", "-E", "--grep", s15rCommitSubject, "-n", "1", "HEAD")
		if err != nil {
			return fmt.Errorf("git log --grep: %v\n%s", err, out)
		}
		ref = strings.TrimSpace(out)
		if ref == "" {
			return fmt.Errorf("no commit %q on this checkout's history; set %s", s15rCommitSubject, s15rCommitEnv)
		}
	}
	out, err := s15rCommand(s.root, nil, "git", "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("git rev-parse %s: %v\n%s", ref, err, out)
	}
	s.commit = strings.TrimSpace(out)
	if !s15rHex.MatchString(s.commit) {
		return fmt.Errorf("unexpected commit %q", s.commit)
	}
	if out, err := s15rCommand(s.root, nil, "git", "merge-base", "--is-ancestor", s.commit, "HEAD"); err != nil {
		return fmt.Errorf("%s is not on this checkout's history: %v\n%s", s.commit, err, out)
	}
	return nil
}

// changedUnder lists the files under path that commit changes against its first
// parent (all of them for a root commit).
func (s *s15rState) changedUnder(commit, path string) ([]string, error) {
	out, err := s15rCommand(s.root, nil, "git", "diff-tree", "--root", "-r", "-m", "--first-parent", "--no-commit-id", "--name-only", commit, "--", path)
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s -- %s: %v\n%s", commit, path, err, out)
	}
	return strings.Fields(out), nil
}

func (s *s15rState) leavesUnchanged(path string) error {
	changed, err := s.changedUnder(s.commit, path)
	if err != nil {
		return err
	}
	if len(changed) != 0 {
		return fmt.Errorf("%s changes %s (TC-12g): %v", s.commit[:7], path, changed)
	}
	return nil
}

func (s *s15rState) latestChangeRefused(path string) error {
	out, err := s15rCommand(s.root, nil, "git", "log", "-n", "1", "--format=%H", "HEAD", "--", path)
	if err != nil {
		return fmt.Errorf("git log -- %s: %v\n%s", path, err, out)
	}
	c := strings.TrimSpace(out)
	if c == "" {
		return fmt.Errorf("no commit on this history changes %s", path)
	}
	changed, err := s.changedUnder(c, path)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		return fmt.Errorf("the check passes %s, which changes %s", c[:7], path)
	}
	return nil
}

// lanesRan finds the --evidence record each lane script wrote for the merged
// commit (lanes/win.ps1 and lanes/wsl.sh name it <lane>-<sha7>-<UTC stamp>.txt
// under s15rRunsDir); the latest one of each lane is the lane's result.
func (s *s15rState) lanesRan(win, wsl string) error {
	s.lanes = map[string]*s15rLane{}
	for _, script := range []string{win, wsl} {
		lane := strings.TrimSuffix(strings.TrimSuffix(script, ".ps1"), ".sh")
		data, err := os.ReadFile(filepath.Join(s.root, "tools", "harness-gate", "lanes", script))
		if err != nil {
			return fmt.Errorf("lane script %s: %v", script, err)
		}
		if !bytes.Contains(data, []byte("--evidence")) || !bytes.Contains(data, []byte(lane+"-")) {
			return fmt.Errorf("lane script %s does not write a %s-<sha7> evidence record", script, lane)
		}
		pattern := filepath.Join(s.root, filepath.FromSlash(s15rRunsDir), lane+"-"+s.commit[:7]+"-*Z.txt")
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
		s.lanes[lane] = &s15rLane{path: filepath.ToSlash(rel), data: b}
	}
	return nil
}

// s15rHeader returns the "key: value" lines of the record before "--- output ---".
func s15rHeader(data []byte) map[string]string {
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

func (s *s15rState) laneRecordIntact(lane, goos string) error {
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
	sum := sha256.Sum256(data[:i+1])
	if last := string(body[i+1:]); last != "sha256: "+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%s: trailer %q does not match the record's SHA-256 %s", l.path, last, hex.EncodeToString(sum[:]))
	}
	h := s15rHeader(data)
	if h["commit"] != s.commit {
		return fmt.Errorf("%s records commit %q, want %s", l.path, h["commit"], s.commit)
	}
	if !strings.HasPrefix(h["tree"], "clean") {
		return fmt.Errorf("%s: tree %q is not clean", l.path, h["tree"])
	}
	if !strings.HasPrefix(h["goos/goarch"], goos+"/") {
		return fmt.Errorf("%s ran on %q, want %s", l.path, h["goos/goarch"], goos)
	}
	if !strings.Contains(h["command"], "--evidence") || !strings.HasSuffix(filepath.ToSlash(h["command"]), "/"+filepath.Base(l.path)) {
		return fmt.Errorf("%s: command %q did not write this record", l.path, h["command"])
	}
	return nil
}

var (
	s15rGateLine = regexp.MustCompile(`(?m)^harness-gate: gate (\d+)/(\d+) (\S+) --`)
	s15rPassLine = regexp.MustCompile(`(?m)^harness-gate: PASS (\S+) \(`)
	s15rFailLine = regexp.MustCompile(`(?m)^harness-gate: FAIL`)
	s15rAllLine  = regexp.MustCompile(`(?m)^harness-gate: PASSED all (\d+) gates$`)
)

// s15rGates returns the record's text, the gates it ran in order, and the ones it passed.
func s15rGates(l *s15rLane) (string, [][]string, map[string]bool) {
	text := strings.ReplaceAll(string(l.data), "\r\n", "\n")
	passed := map[string]bool{}
	for _, m := range s15rPassLine.FindAllStringSubmatch(text, -1) {
		passed[m[1]] = true
	}
	return text, s15rGateLine.FindAllStringSubmatch(text, -1), passed
}

func s15rTrailerField(text, key string) string {
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

func (s *s15rState) everyGatePasses() error {
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		text, gates, passed := s15rGates(l)
		if len(gates) == 0 {
			return fmt.Errorf("%s runs no gate", l.path)
		}
		total := gates[0][2]
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
		if s15rFailLine.MatchString(text) {
			return fmt.Errorf("%s reports a failed gate", l.path)
		}
		if all := s15rAllLine.FindStringSubmatch(text); all == nil || all[1] != total {
			return fmt.Errorf("%s does not report PASSED all %s gates", l.path, total)
		}
		if v := s15rTrailerField(text, "exit"); v != "0" {
			return fmt.Errorf("%s exits %q, want 0", l.path, v)
		}
	}
	return nil
}

// gateAmongPassing: the named gate is one the lane ran and passed, so a gate list
// that drops it cannot pass this scenario.
func (s *s15rState) gateAmongPassing(name string) error {
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		_, gates, passed := s15rGates(l)
		ran := slices.ContainsFunc(gates, func(g []string) bool { return g[3] == name })
		if !ran || !passed[name] {
			return fmt.Errorf("%s: gate %s ran %v, passed %v", l.path, name, ran, passed[name])
		}
	}
	return nil
}

// evidenceRecordsLanes requires the stage record to name, on one line each, the
// lane script, its record file and the merged commit, with a passing result.
func (s *s15rState) evidenceRecordsLanes(record string) error {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(record)))
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	scripts := map[string]string{"win": "win.ps1", "wsl": "wsl.sh"}
	for _, lane := range []string{"win", "wsl"} {
		base := filepath.Base(s.lanes[lane].path)
		found := slices.ContainsFunc(strings.Split(text, "\n"), func(line string) bool {
			return strings.Contains(line, scripts[lane]) && strings.Contains(line, base) &&
				strings.Contains(line, s.commit[:7]) && strings.Contains(strings.ToLower(line), "exit 0")
		})
		if !found {
			return fmt.Errorf("%s has no line naming %s, its record %s, the commit %s and exit 0", record, scripts[lane], base, s.commit[:7])
		}
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_dh_s_15_scenarios_and_built_record(ctx *godog.ScenarioContext) {
	s := &s15rState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s15rState{}
		return c, nil
	})

	ctx.Step(`^the five DH-S-15 HOST files in "([^"]*)" and "([^"]*)"$`, s.givenHostFiles)
	ctx.Step(`^"(go -C src/harness vet -tags scenario \./internal/testkit/scenarios/\.\.\.)" runs for "([^"]*)", "([^"]*)" and "([^"]*)"$`, s.runVet)
	ctx.Step(`^the gate's scenario-index check runs on the checkout$`, s.runIndexCheck)
	ctx.Step(`^vet exits 0 for each$`, s.vetExits0)
	ctx.Step(`^the gate's scenario-index check passes$`, s.indexCheckPasses)
	ctx.Step(`^each ID is bound by exactly one scenario test in its named file, built only under the "([^"]*)" tag$`, s.boundOnceInFile)
	ctx.Step(`^each ID appears exactly once in the index, under its executing stage, authored "([^"]*)" with its requires$`, s.indexedOnce)
	ctx.Step(`^the default untagged test run lists none of the five scenario tests$`, s.defaultRunNone)
	ctx.Step(`^none of the five files imports "([^"]*)"$`, s.noFileImports)
	ctx.Step(`^the scenario package under the "([^"]*)" tag does not depend on "([^"]*)" for each$`, s.noDependency)

	ctx.Step(`^the merged commit of stage DH-S-15$`, s.givenMergedCommit)
	ctx.Step(`^the merged commit leaves "([^"]*)" unchanged$`, s.leavesUnchanged)
	ctx.Step(`^the check is live: the latest commit that changed "([^"]*)" is refused$`, s.latestChangeRefused)
	ctx.Step(`^harness-gate runs on "([^"]*)" and "([^"]*)" for that commit$`, s.lanesRan)
	ctx.Step(`^the "([^"]*)" lane record is an intact clean-tree evidence record of that commit on "([^"]*)"$`, s.laneRecordIntact)
	ctx.Step(`^every gate of each lane record passes and the record exits 0$`, s.everyGatePasses)
	ctx.Step(`^the "([^"]*)" gate is among the passing gates of each lane record$`, s.gateAmongPassing)
	ctx.Step(`^"([^"]*)" records both lane results with the commit SHA$`, s.evidenceRecordsLanes)
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_dh_s_15_scenarios_and_built_record(t *testing.T) {
	s15rOuterT = t
	defer func() { s15rOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "dh_s_15_listener_sign_in_stdio_and_sdk_server_dh_s_15_scenarios_and_built_record",
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_dh_s_15_scenarios_and_built_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_dh_s_15_scenarios_and_built_record.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
