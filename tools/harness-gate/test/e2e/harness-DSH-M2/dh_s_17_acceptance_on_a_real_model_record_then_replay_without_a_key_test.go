//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
)

const (
	s17rStage     = "DH-S-17"
	s17rScenario  = "DH-E-TOOLCHAIN-1"
	s17rTest      = "TestRecordThenReplayWithoutAKey"
	s17rRecordRel = "docs/stories/harness-DSH-M2/evidence/DH-S-17.md"
	s17rIndexRel  = "test/harness/acceptance-index.yaml"
)

// s17rRouteInputs are the inputs TestRecordThenReplayWithoutAKey needs from the S17
// machine; with all three set the scenario records on the signed-in route.
var s17rRouteInputs = []string{"DSH_SCENARIO_TOOLCHAIN1_DIR", "DSH_SCENARIO_TOOLCHAIN1_RECORD", "DSH_SCENARIO_TOOLCHAIN1_REPLAY"}

var (
	s17rSHA      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	s17rChecksum = regexp.MustCompile(`(?:^|[^0-9a-fA-F])([0-9a-f]{64})(?:$|[^0-9a-fA-F])`)
	s17rModel    = regexp.MustCompile(`(?i)\bmodel(?:[ _-]?id)?\s*[:= ]\s*([A-Za-z0-9][\w.:/@-]*)`)
	s17rRunID    = regexp.MustCompile(`(?i)\brun[ _-]?id\s*[:= ]\s*([A-Za-z0-9][\w.:/@-]*)`)

	s17rBuildOnce sync.Once
	s17rGateBin   string
	s17rBuildErr  error
)

// s17rRow is one Scenarios row of the record, keyed by lower-case column name.
type s17rRow map[string]string

func (r s17rRow) passed() bool {
	l := strings.ToLower(r["result"])
	return l == "passed" || strings.HasPrefix(l, "passed (")
}

// s17rFields are the M2-DEC-5 identities a passed row must carry.
type s17rFields struct{ commit, checksum, model, runID string }

func s17rFieldsOf(r s17rRow) s17rFields {
	f := s17rFields{}
	if c := r["commit"]; s17rSHA.MatchString(c) {
		f.commit = c
	}
	a := r["artifacts"]
	if m := s17rChecksum.FindStringSubmatch(a); m != nil {
		f.checksum = m[1]
	}
	f.model = s17rIdentity(s17rModel, a)
	f.runID = s17rIdentity(s17rRunID, a)
	return f
}

// s17rIdentity is the first value pattern names in s that is not the word "id"
// itself ("model id" with nothing after it names no model).
func s17rIdentity(pattern *regexp.Regexp, s string) string {
	for _, m := range pattern.FindAllStringSubmatch(s, -1) {
		if v := strings.TrimRight(m[1], ".;,"); v != "" && !strings.EqualFold(v, "id") {
			return v
		}
	}
	return ""
}

func (f s17rFields) missing() []string {
	var out []string
	for _, p := range []struct{ name, v string }{
		{"commit", f.commit}, {"binary checksum", f.checksum}, {"model id", f.model}, {"run id", f.runID},
	} {
		if p.v == "" {
			out = append(out, p.name)
		}
	}
	return out
}

type s17rState struct {
	repo, module string

	record string // the record text under test, LF line endings
	row    s17rRow
	root   string // the tree ready runs on

	live         bool
	testOut      string
	testCode     int
	reportPath   string
	stdout, errs string
	code         int
}

func s17rModuleRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	if !bytes.Contains(data, []byte("module github.com/smartpcr/agent-plugin/tools/harness-gate")) {
		return "", fmt.Errorf("%s is not the harness-gate module", root)
	}
	return root, nil
}

// s17rEnv is the environment with GOWORK=off and no inherited GOFLAGS, plus set
// (a later entry wins).
func s17rEnv(set ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		u := strings.ToUpper(kv)
		return strings.HasPrefix(u, "GOWORK=") || strings.HasPrefix(u, "GOFLAGS=")
	})
	return append(append(env, "GOWORK=off"), set...)
}

func s17rBuildGate(module string) (string, error) {
	s17rBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "harness-gate-s17r-e2e-")
		if err != nil {
			s17rBuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		s17rGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", s17rGateBin, ".")
		cmd.Dir = module
		cmd.Env = s17rEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			s17rBuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return s17rGateBin, s17rBuildErr
}

func s17rShowHEAD(repo, rel string) (string, error) {
	cmd := exec.Command("git", "show", "HEAD:"+rel)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git show HEAD:%s: %v\n%s", rel, err, stderr.String())
	}
	return strings.ReplaceAll(stdout.String(), "\r\n", "\n"), nil
}

// s17rSection is the body of the "## <heading>" section of md, up to the next "## ".
func s17rSection(md, heading string) (string, bool) {
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != heading {
			continue
		}
		var body []string
		for _, b := range lines[i+1:] {
			if strings.HasPrefix(b, "## ") {
				break
			}
			body = append(body, b)
		}
		return strings.Join(body, "\n"), true
	}
	return "", false
}

func s17rCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimSuffix(strings.TrimPrefix(t, "|"), "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// s17rRows parses the first table of the "## Scenarios" section of md.
func s17rRows(md string) ([]string, []s17rRow, error) {
	body, ok := s17rSection(md, "## Scenarios")
	if !ok {
		return nil, nil, errors.New(`the record has no "## Scenarios" section`)
	}
	var header []string
	var rows []s17rRow
	for _, l := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "|") {
			if header != nil {
				break
			}
			continue
		}
		cells := s17rCells(l)
		switch {
		case header == nil:
			for _, c := range cells {
				header = append(header, strings.ToLower(c))
			}
		case strings.Trim(strings.Join(cells, ""), "-: ") == "":
		default:
			if len(cells) != len(header) {
				return nil, nil, fmt.Errorf("row %q has %d cells, the header %d", l, len(cells), len(header))
			}
			r := s17rRow{}
			for i, h := range header {
				r[h] = cells[i]
			}
			rows = append(rows, r)
		}
	}
	if header == nil {
		return nil, nil, errors.New(`the "## Scenarios" section has no table`)
	}
	return header, rows, nil
}

func s17rRequiredRow(md string) (s17rRow, error) {
	_, rows, err := s17rRows(md)
	if err != nil {
		return nil, err
	}
	var found []s17rRow
	for _, r := range rows {
		if r["scenario"] == s17rScenario && strings.EqualFold(r["leg"], "required") {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("the record has %d required %s rows, want 1", len(found), s17rScenario)
	}
	return found[0], nil
}

func (s *s17rState) committedRecord(rel, scenario string) error {
	if rel != s17rRecordRel || scenario != s17rScenario {
		return fmt.Errorf("this stage reads %s row of %s, not %s of %s", s17rScenario, s17rRecordRel, scenario, rel)
	}
	md, err := s17rShowHEAD(s.repo, rel)
	if err != nil {
		return err
	}
	disk, err := os.ReadFile(filepath.Join(s.repo, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if strings.ReplaceAll(string(disk), "\r\n", "\n") != md {
		return fmt.Errorf("%s differs from its committed text; the evidence is the committed record", rel)
	}
	s.record, s.root = md, s.repo
	s.row, err = s17rRequiredRow(md)
	return err
}

func (s *s17rState) committedRecordOpen(rel, status string) error {
	if err := s.committedRecord(rel, s17rScenario); err != nil {
		return err
	}
	var lines []string
	for _, l := range strings.Split(s.record, "\n") {
		if strings.HasPrefix(l, "Closure status:") {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) != 1 || lines[0] != status {
		return fmt.Errorf("status lines %q, want exactly %q", lines, status)
	}
	return nil
}

// runScenarioTest runs the real DH-E-TOOLCHAIN-1 scenario test of src/harness. On a
// host without the route inputs they are blanked, so an operator's partial shell
// cannot half-run it; the test must then fail closed.
func (s *s17rState) runScenarioTest() error {
	s.live = true
	for _, name := range s17rRouteInputs {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			s.live = false
		}
	}
	dir, err := os.MkdirTemp("", "s17r-report-")
	if err != nil {
		return err
	}
	s.reportPath = filepath.Join(dir, "scenario-report.jsonl")
	set := []string{"DSH_SCENARIO_REPORT=" + s.reportPath, "DSH_REQUIRE_SCENARIOS=" + s17rScenario}
	if !s.live {
		for _, name := range s17rRouteInputs {
			set = append(set, name+"=")
		}
	}
	cmd := exec.Command("go", "-C", filepath.Join(s.repo, "src", "harness"), "test", "-tags", "scenario", "-count=1", "-v",
		"-run", "^"+s17rTest+"$", "./internal/testkit/scenarios/")
	cmd.Env = s17rEnv(set...)
	out, err := cmd.CombinedOutput()
	s.testOut, s.testCode = string(out), 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		s.testCode = exit.ExitCode()
	case err != nil:
		return fmt.Errorf("go test: %v\n%s", err, out)
	}
	if !strings.Contains(s.testOut, "=== RUN   "+s17rTest) {
		return fmt.Errorf("%s did not run (exit %d):\n%s", s17rTest, s.testCode, s.testOut)
	}
	return nil
}

func (s *s17rState) testResult(input string) error {
	if s.live {
		if s.testCode != 0 || !strings.Contains(s.testOut, "--- PASS: "+s17rTest) {
			return fmt.Errorf("on the signed-in route %s must pass; exit %d:\n%s", s17rTest, s.testCode, s.testOut)
		}
		return nil
	}
	if s.testCode == 0 || !strings.Contains(s.testOut, "--- FAIL: "+s17rTest) {
		return fmt.Errorf("without the route inputs %s must fail closed; exit %d:\n%s", s17rTest, s.testCode, s.testOut)
	}
	if !strings.Contains(s.testOut, input+" is not set") {
		return fmt.Errorf("the failure does not name the missing %s:\n%s", input, s.testOut)
	}
	return nil
}

func (s *s17rState) neverSkipped() error {
	if strings.Contains(s.testOut, "--- SKIP: "+s17rTest) {
		return fmt.Errorf("%s was skipped; a scenario that cannot run fails (DH-O-15):\n%s", s17rTest, s.testOut)
	}
	return nil
}

func (s *s17rState) reportRow(id string) error {
	f, err := os.Open(s.reportPath)
	if err != nil {
		return fmt.Errorf("the scenario report %s: %v", s.reportPath, err)
	}
	defer f.Close()
	var rows []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return fmt.Errorf("report line %q: %v", sc.Text(), err)
		}
		if r["kind"] == "scenario" && r["id"] == id {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 {
		return fmt.Errorf("the report has %d %s rows, want 1", len(rows), id)
	}
	want := "failed"
	if s.live {
		want = "passed"
	}
	if got := rows[0]["result"]; got != want {
		return fmt.Errorf("report row %v reads %v, want %s", rows[0], got, want)
	}
	if !s.live {
		if reason, _ := rows[0]["reason"].(string); !strings.Contains(reason, "DSH_SCENARIO_TOOLCHAIN1_") {
			return fmt.Errorf("report row %v names no missing route input", rows[0])
		}
	}
	return nil
}

func (s *s17rState) requiredAcceptanceRow(variant string) error {
	r := s.row
	if r["variant"] != variant || !strings.EqualFold(r["venue"], "acceptance") || !strings.EqualFold(r["leg"], "required") {
		return fmt.Errorf("row %v: want variant %q, venue acceptance, leg required", r, variant)
	}
	if !strings.HasPrefix(r["environment"], "E1") {
		return fmt.Errorf("row %v: the environment is not E1", r)
	}
	return nil
}

func (s *s17rState) passedOrOwed(heading string) error {
	r := s.row
	if r.passed() {
		if miss := s17rFieldsOf(r).missing(); len(miss) > 0 {
			return fmt.Errorf("row reads passed but lacks %s (M2-DEC-5): %v", strings.Join(miss, ", "), r)
		}
		a := strings.ToLower(r["artifacts"])
		if !strings.Contains(a, "executed") && !strings.Contains(a, "equals") {
			return fmt.Errorf("row reads passed but its Artifacts carry no replay result (an \"executed\" report row or the \"equals\" check): %q", r["artifacts"])
		}
		return nil
	}
	res := strings.TrimSpace(r["result"])
	if !strings.HasPrefix(strings.ToLower(res), "not run (") || !strings.HasSuffix(res, ")") || len(strings.TrimSpace(res[len("not run ("):len(res)-1])) == 0 {
		return fmt.Errorf("row result %q is neither passed nor \"not run (<reason>)\"", res)
	}
	body, ok := s17rSection(s.record, heading)
	if !ok {
		return fmt.Errorf("the row is owed but the record has no %q section", heading)
	}
	if !strings.Contains(body, "What clears it") || strings.Count(body, "\n|") < 3 {
		return fmt.Errorf("the %q section has no blocker table naming what clears it:\n%s", heading, body)
	}
	return nil
}

func (s *s17rState) runReady() error {
	bin, err := s17rBuildGate(s.module)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "ready", "--stage", s17rStage, "--root", s.root)
	cmd.Env = s17rEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	s.stdout, s.errs, s.code = strings.ReplaceAll(stdout.String(), "\r\n", "\n"), stderr.String(), 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		s.code = exit.ExitCode()
	case err != nil:
		return err
	}
	if s.code == 2 {
		return fmt.Errorf("ready: usage or setup error:\n%s%s", s.stdout, s.errs)
	}
	return nil
}

func (s *s17rState) runReadyOn(_ string) error { return s.runReady() }

// toolchainBlockers are ready's blocker lines about the TOOLCHAIN-1 row.
func (s *s17rState) toolchainBlockers() []string {
	var out []string
	for _, l := range strings.Split(s.stdout, "\n") {
		if strings.HasPrefix(l, "  - "+s17rScenario+" ") || strings.HasPrefix(l, "  - "+s17rScenario+":") {
			out = append(out, l)
		}
	}
	return out
}

func (s *s17rState) readyCountsOnlyPassed() error {
	if err := s.runReady(); err != nil {
		return err
	}
	blocked := s.toolchainBlockers()
	if s.row.passed() {
		if len(blocked) > 0 {
			return fmt.Errorf("the row reads passed but ready blocks it: %q", blocked)
		}
		return nil
	}
	want := fmt.Sprintf("  - %s [%s]: result %q: not run counts as open", s17rScenario, s.row["variant"], s.row["result"])
	if s.code != 1 || !slices.ContainsFunc(blocked, func(l string) bool { return strings.HasPrefix(l, want) }) {
		return fmt.Errorf("ready (exit %d) does not hold the owed row open with %q:\n%s", s.code, want, s.stdout)
	}
	return nil
}

func (s *s17rState) countedOnlyWhenComplete() error {
	f := s17rFieldsOf(s.row)
	complete := s.row.passed() && len(f.missing()) == 0
	blocked := s.toolchainBlockers()
	switch {
	case complete && len(blocked) > 0:
		return fmt.Errorf("the row is complete but ready blocks it: %q", blocked)
	case !complete && len(blocked) == 0:
		return fmt.Errorf("ready counts the row as passed while it lacks %s or does not read passed: %v", strings.Join(f.missing(), ", "), s.row)
	case !complete && s.code != 1:
		return fmt.Errorf("ready exits %d on an incomplete row, want 1", s.code)
	}
	return nil
}

// copyWithRow writes the committed index and a copy of the committed record whose
// TOOLCHAIN-1 row reads passed with commit and artifacts into a scratch tree.
func (s *s17rState) copyWithRow(scenario, commit, artifacts string) error {
	if scenario != s17rScenario {
		return fmt.Errorf("this stage edits %s, not %s", s17rScenario, scenario)
	}
	md, err := s17rShowHEAD(s.repo, s17rRecordRel)
	if err != nil {
		return err
	}
	index, err := s17rShowHEAD(s.repo, s17rIndexRel)
	if err != nil {
		return err
	}
	header, _, err := s17rRows(md)
	if err != nil {
		return err
	}
	lines := strings.Split(md, "\n")
	edited := 0
	for i, l := range lines {
		cells := s17rCells(l)
		if !strings.HasPrefix(strings.TrimSpace(l), "|") || len(cells) != len(header) || cells[0] != s17rScenario {
			continue
		}
		for j, h := range header {
			switch h {
			case "environment":
				cells[j] = "E1 wsl:DEVBOX7/Ubuntu-24.04"
			case "commit":
				cells[j] = commit
			case "result":
				cells[j] = "passed"
			case "artifacts":
				cells[j] = artifacts
			}
		}
		lines[i] = "| " + strings.Join(cells, " | ") + " |"
		edited++
	}
	if edited != 1 {
		return fmt.Errorf("edited %d %s rows, want 1", edited, s17rScenario)
	}
	md = strings.Join(lines, "\n")
	root, err := os.MkdirTemp("", "s17r-tree-")
	if err != nil {
		return err
	}
	for rel, text := range map[string]string{s17rRecordRel: md, s17rIndexRel: index} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return err
		}
	}
	s.root, s.record = root, md
	s.row, err = s17rRequiredRow(md)
	return err
}

func (s *s17rState) exitsOneForOthers() error {
	if s.code != 1 || !strings.Contains(s.stdout, "NOT READY") {
		return fmt.Errorf("ready exit %d, want 1 for the other open rows:\n%s", s.code, s.stdout)
	}
	return nil
}

func (s *s17rState) noBlockerOf(id string) error {
	if id != s17rScenario {
		return fmt.Errorf("unexpected scenario %s", id)
	}
	if b := s.toolchainBlockers(); len(b) > 0 {
		return fmt.Errorf("ready still names %s: %q", id, b)
	}
	return nil
}

func (s *s17rState) namesBlocker(line string) error {
	if !slices.ContainsFunc(strings.Split(s.stdout, "\n"), func(l string) bool { return strings.HasPrefix(l, "  - "+line) }) {
		return fmt.Errorf("ready (exit %d) does not name %q:\n%s", s.code, line, s.stdout)
	}
	if s.code != 1 {
		return fmt.Errorf("ready exit %d, want 1", s.code)
	}
	return nil
}

func (s *s17rState) carriesAll() error {
	if miss := s17rFieldsOf(s.row).missing(); len(miss) > 0 {
		return fmt.Errorf("the row lacks %s: %v", strings.Join(miss, ", "), s.row)
	}
	return nil
}

func (s *s17rState) lacks(what string) error {
	miss := s17rFieldsOf(s.row).missing()
	if !slices.Equal(miss, []string{what}) {
		return fmt.Errorf("the row lacks %q, want exactly [%s]: %v", miss, what, s.row)
	}
	return nil
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key(ctx *godog.ScenarioContext) {
	s := &s17rState{}
	ctx.Before(func(c2 context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s17rState{}
		module, err := s17rModuleRoot()
		if err != nil {
			return c2, err
		}
		s.module, s.repo = module, filepath.Dir(filepath.Dir(module))
		return c2, nil
	})
	ctx.After(func(c2 context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.root != "" && s.root != s.repo {
			_ = os.RemoveAll(s.root)
		}
		if s.reportPath != "" {
			_ = os.RemoveAll(filepath.Dir(s.reportPath))
		}
		return c2, err
	})

	ctx.Step(`^the committed record "([^"]*)" with its "([^"]*)" row$`, s.committedRecord)
	ctx.Step(`^the committed record "([^"]*)" still reading "([^"]*)"$`, s.committedRecordOpen)
	ctx.Step(`^TestRecordThenReplayWithoutAKey runs with the route inputs this host holds$`, s.runScenarioTest)
	ctx.Step(`^the test passes when the route inputs are set, and otherwise fails naming "([^"]*)"$`, s.testResult)
	ctx.Step(`^the test is never reported skipped$`, s.neverSkipped)
	ctx.Step(`^its scenario report row for "([^"]*)" reads the same result$`, s.reportRow)
	ctx.Step(`^the committed row is a required acceptance row of variant "([^"]*)"$`, s.requiredAcceptanceRow)
	ctx.Step(`^the committed row reads passed with its replay result in Artifacts, or reads not run with a reason and a "([^"]*)" section naming what clears it$`,
		func(name string) error { return s.passedOrOwed("## " + name) })
	ctx.Step(`^"harness-gate ready --stage DH-S-17" counts the committed row as passed only when it reads passed$`, s.readyCountsOnlyPassed)
	ctx.Step(`^"harness-gate ready --stage DH-S-17" reads the "([^"]*)" row of the checkout$`, s.runReadyOn)
	ctx.Step(`^the row is counted as passed only when its commit, binary checksum, model id and run id are all present$`, s.countedOnlyWhenComplete)
	ctx.Step(`^a copy of the committed index and record whose "([^"]*)" row reads passed at commit "([^"]*)" with artifacts "([^"]*)"$`, s.copyWithRow)
	ctx.Step(`^"harness-gate ready --stage DH-S-17" runs on the copy$`, s.runReady)
	ctx.Step(`^it exits 1 for the other open rows$`, s.exitsOneForOthers)
	ctx.Step(`^it names no blocker of "([^"]*)"$`, s.noBlockerOf)
	ctx.Step(`^it names the blocker "(.*)"$`, s.namesBlocker)
	ctx.Step(`^the copied row carries a commit, a binary checksum, a model id and a run id$`, s.carriesAll)
	ctx.Step(`^the copied row lacks its (commit|binary checksum|model id|run id)$`, s.lacks)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the record-then-replay e2e scenarios failed")
	}
}
