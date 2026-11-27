//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
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

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

const (
	s16mStory = "harness:DSH-M2"
	s16mStage = "DH-S-16"
)

var (
	s16mSHA        = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	s16mStatusLine = regexp.MustCompile(`(?m)^Closure status:[^\n]*$`)

	s16mBuildOnce sync.Once
	s16mGateBin   string
	s16mGateDir   string
	s16mBuildErr  error
)

// s16mModuleRoot is tools/harness-gate, seen from tools/harness-gate/test/e2e/harness-DSH-M2.
func s16mModuleRoot() (string, error) {
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

func s16mRepoRoot() (string, error) {
	mod, err := s16mModuleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(mod)), nil
}

func s16mGitEnv() []string {
	return append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		u := strings.ToUpper(kv)
		return strings.HasPrefix(u, "GOWORK=") || strings.HasPrefix(u, "GOFLAGS=")
	}), "GOWORK=off")
}

func s16mBuildGate() (string, error) {
	s16mBuildOnce.Do(func() {
		root, err := s16mModuleRoot()
		if err != nil {
			s16mBuildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "harness-gate-s16m-e2e-")
		if err != nil {
			s16mBuildErr = err
			return
		}
		s16mGateDir = dir
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		s16mGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", s16mGateBin, ".")
		cmd.Dir = root
		cmd.Env = s16mGitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			s16mBuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return s16mGateBin, s16mBuildErr
}

// s16mShowHEAD is the committed text of a repository path, line endings normalised.
func s16mShowHEAD(repo, rel string) (string, error) {
	cmd := exec.Command("git", "show", "HEAD:"+rel)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git show HEAD:%s: %v\n%s", rel, err, stderr.String())
	}
	return strings.ReplaceAll(stdout.String(), "\r\n", "\n"), nil
}

type s16mRun struct {
	stdout, stderr string
	code           int
}

func s16mExec(bin string, args ...string) (s16mRun, error) {
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return s16mRun{}, fmt.Errorf("run %v: %w", args, err)
		}
		code = exit.ExitCode()
	}
	return s16mRun{stdout: strings.ReplaceAll(stdout.String(), "\r\n", "\n"), stderr: stderr.String(), code: code}, nil
}

// s16mScratchRoot writes a repository root holding only the given files.
func s16mScratchRoot(files map[string]string) (string, error) {
	dir, err := os.MkdirTemp("", "harness-gate-s16m-root-")
	if err != nil {
		return "", err
	}
	for rel, text := range files {
		to := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(to, []byte(text), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

type s16mState struct {
	repo    string
	record  string // repository path of the DH-S-16 record
	text    string // the record as committed at HEAD
	index   string // the acceptance index as committed at HEAD
	run     s16mRun
	ran     string // the command the last run was
	scratch []string

	fixture string
	hashes  map[string][32]byte

	records map[string]string // stage -> committed record text
	parsed  *acceptance.Index
	idxErr  error
}

func (s *s16mState) cleanup() {
	for _, d := range s.scratch {
		_ = os.RemoveAll(d)
	}
	s.scratch = nil
}

func (s *s16mState) ensureRepo() error {
	if s.repo != "" {
		return nil
	}
	repo, err := s16mRepoRoot()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(acceptance.IndexPath))); err != nil {
		return fmt.Errorf("repository root %s: %w", repo, err)
	}
	s.repo = repo
	return nil
}

// runOn runs the built gate's subcommand for DH-S-16 --built on a scratch root
// holding the committed index and the given record text.
func (s *s16mState) runOn(sub, record string) error {
	bin, err := s16mBuildGate()
	if err != nil {
		return err
	}
	root, err := s16mScratchRoot(map[string]string{acceptance.IndexPath: s.index, s.record: record})
	if err != nil {
		return err
	}
	s.scratch = append(s.scratch, root)
	s.run, err = s16mExec(bin, sub, "--stage", s16mStage, "--built", "--story", s16mStory, "--root", root)
	s.ran = sub
	return err
}

// ---- Scenario: S16 ready then closed ----

func (s *s16mState) committedRecord(record string) error {
	if err := s.ensureRepo(); err != nil {
		return err
	}
	if want := closure.RecordPath(s16mStory, s16mStage); record != want {
		return fmt.Errorf("record %q is not the DH-S-16 record %q", record, want)
	}
	text, err := s16mShowHEAD(s.repo, record)
	if err != nil {
		return err
	}
	index, err := s16mShowHEAD(s.repo, acceptance.IndexPath)
	if err != nil {
		return err
	}
	if n := len(s16mStatusLine.FindAllString(text, -1)); n != 1 {
		return fmt.Errorf("the committed %s has %d status lines, want 1", record, n)
	}
	s.record, s.text, s.index = record, text, index
	return nil
}

func (s *s16mState) readyRuns(command string) error {
	if command != "harness-gate ready --stage DH-S-16 --built" {
		return fmt.Errorf("unexpected command %q", command)
	}
	// ready runs before the status transition, so its input is the pre-transition
	// record (status open) whatever HEAD reads, including after the closing PR lands.
	open := s16mStatusLine.ReplaceAllLiteralString(s.text, "Closure status: open")
	return s.runOn("ready", open)
}

func (s *s16mState) exits0Reporting(what string) error {
	if s.run.code != 0 {
		return fmt.Errorf("harness-gate %s --stage %s --built exits %d, want 0 (the record is not %s; the owed items are the blockers below)\nstdout:\n%s\nstderr:\n%s",
			s.ran, s16mStage, s.run.code, what, s.run.stdout, s.run.stderr)
	}
	head := fmt.Sprintf("harness-gate %s: %s (%s, built): %s", s.ran, s16mStage, s.record, what)
	if !strings.HasPrefix(s.run.stdout, head) {
		return fmt.Errorf("stdout does not start %q:\n%s", head, s.run.stdout)
	}
	if strings.Contains(s.run.stdout, "\n  - ") {
		return fmt.Errorf("a passing run printed blockers:\n%s", s.run.stdout)
	}
	return nil
}

func (s *s16mState) guardedRowsPassed(a, b, c, d string) error {
	rec, err := closure.ParseRecord(s.record, strings.NewReader(s.text))
	if err != nil {
		return fmt.Errorf("parse %s: %w", s.record, err)
	}
	if !rec.Scenarios.Present {
		return fmt.Errorf("%s has no Scenarios table", s.record)
	}
	var problems []string
	for _, id := range []string{a, b, c, d} {
		counted := 0
		for _, row := range rec.Scenarios.Rows {
			if row.Get("scenario") != id || row.Get("venue") != "guarded" {
				continue
			}
			result := row.Get("result")
			if strings.HasPrefix(result, "out of scope (") {
				continue
			}
			counted++
			if result != "passed" {
				problems = append(problems, fmt.Sprintf("%s [%s] (line %d): result %q, want passed", id, row.Get("variant"), row.Line, result))
			}
			if !s16mSHA.MatchString(row.Get("commit")) {
				problems = append(problems, fmt.Sprintf("%s [%s] (line %d): commit %q is not a SHA", id, row.Get("variant"), row.Line, row.Get("commit")))
			}
		}
		if counted == 0 {
			problems = append(problems, id+": no in-scope guarded row")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("guarded rows of %s not passed:\n  %s", s.record, strings.Join(problems, "\n  "))
	}
	return nil
}

func (s *s16mState) closeRuns(status, command string) error {
	if command != "harness-gate close --stage DH-S-16 --built" {
		return fmt.Errorf("unexpected command %q", command)
	}
	if status != "Closure status: closed" {
		return fmt.Errorf("unexpected status line %q", status)
	}
	// The closing PR's only change is the status line; a record already closed
	// at HEAD is the input as committed.
	closed := s16mStatusLine.ReplaceAllLiteralString(s.text, status)
	if strings.Contains(s.text, "\nClosure status: closed") {
		closed = s.text
	}
	return s.runOn("close", closed)
}

func (s *s16mState) fixtureTree(rel string) error {
	root, err := s16mModuleRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	for _, p := range []string{closure.RecordPath(s16mStory, s16mStage), acceptance.IndexPath} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			return fmt.Errorf("fixture tree %s: %w", rel, err)
		}
	}
	h, err := hashTree(dir)
	if err != nil {
		return err
	}
	s.fixture, s.hashes = dir, h
	s.record = closure.RecordPath(s16mStory, s16mStage)
	return nil
}

func (s *s16mState) readyRunsOnFixture(command string) error {
	if command != "harness-gate ready --stage DH-S-16 --built" {
		return fmt.Errorf("unexpected command %q", command)
	}
	bin, err := s16mBuildGate()
	if err != nil {
		return err
	}
	s.run, err = s16mExec(bin, "ready", "--stage", s16mStage, "--built", "--root", s.fixture)
	s.ran = "ready"
	return err
}

func (s *s16mState) exits1Naming(row, state string) error {
	if s.run.code != 1 {
		return fmt.Errorf("ready on the fixture exits %d, want 1 (blockers)\nstdout:\n%s\nstderr:\n%s", s.run.code, s.run.stdout, s.run.stderr)
	}
	head := fmt.Sprintf("harness-gate ready: %s (%s, built): NOT READY", s16mStage, s.record)
	if !strings.HasPrefix(s.run.stdout, head) {
		return fmt.Errorf("stdout does not start %q:\n%s", head, s.run.stdout)
	}
	for _, line := range strings.Split(s.run.stdout, "\n") {
		if b, ok := strings.CutPrefix(line, "  - "+row+": "); ok && strings.Contains(b, state) {
			return nil
		}
	}
	return fmt.Errorf("no blocker names %q as %q:\n%s", row, state, s.run.stdout)
}

// s16mGoldenSection is the body under "== <title>" in a golden of "== " sections.
func s16mGoldenSection(text, title string) (string, bool) {
	var b strings.Builder
	in, found := false, false
	sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(text, "\r\n", "\n")))
	for sc.Scan() {
		line := sc.Text()
		if t, ok := strings.CutPrefix(line, "== "); ok {
			in = t == title
			found = found || in
			continue
		}
		if in {
			b.WriteString(line + "\n")
		}
	}
	return b.String(), found
}

func (s *s16mState) stdoutEqualsSection(title, golden string) error {
	root, err := s16mModuleRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(golden)))
	if err != nil {
		return err
	}
	want, ok := s16mGoldenSection(string(data), title)
	if !ok {
		return fmt.Errorf("%s has no section %q", golden, title)
	}
	if s.run.stdout != want {
		return fmt.Errorf("stdout differs from %s section %q\n--- got\n%s--- want\n%s", golden, title, s.run.stdout, want)
	}
	return nil
}

func (s *s16mState) fixtureUnchanged() error {
	after, err := hashTree(s.fixture)
	if err != nil {
		return err
	}
	if len(after) != len(s.hashes) {
		return fmt.Errorf("fixture file count changed: %d -> %d", len(s.hashes), len(after))
	}
	for k, v := range s.hashes {
		if after[k] != v {
			return fmt.Errorf("fixture %s changed", k)
		}
	}
	return nil
}

// ---- Scenario: Built-through advanced ----

func (s *s16mState) committedIndexAndRecords(a, b, c, d, e string) error {
	if err := s.ensureRepo(); err != nil {
		return err
	}
	index, err := s16mShowHEAD(s.repo, acceptance.IndexPath)
	if err != nil {
		return err
	}
	s.index = index
	s.records = map[string]string{}
	story := closure.Stories[s16mStory]
	for _, stage := range []string{a, b, c, d, e} {
		if built, known := story.Built(stage); !known || !built {
			return fmt.Errorf("%s is not a build stage of %s", stage, s16mStory)
		}
		text, err := s16mShowHEAD(s.repo, closure.RecordPath(s16mStory, stage))
		if err != nil {
			return err
		}
		s.records[stage] = text
	}
	return nil
}

func (s *s16mState) indexCheckRuns() error {
	s.parsed, s.idxErr = acceptance.ParseIndex(strings.NewReader(s.index))
	return nil
}

func (s *s16mState) builtThroughReads(want string) error {
	if s.idxErr != nil {
		return fmt.Errorf("the committed %s does not parse: %w", acceptance.IndexPath, s.idxErr)
	}
	if s.parsed.BuiltThrough != want {
		return fmt.Errorf("built-through in the committed %s reads %q, want %q", acceptance.IndexPath, s.parsed.BuiltThrough, want)
	}
	return nil
}

// s16mStepProblems names every step of a record without a commit SHA.
func s16mStepProblems(stage, text string) ([]string, error) {
	path := closure.RecordPath(s16mStory, stage)
	rec, err := closure.ParseRecord(path, strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if !rec.Steps.Present || !rec.Steps.Has("commit") {
		return []string{stage + ": no Steps table with a Commit column"}, nil
	}
	if len(rec.Steps.Rows) == 0 {
		return []string{stage + ": the Steps table has no rows"}, nil
	}
	var out []string
	for _, row := range rec.Steps.Rows {
		if c := row.Get("commit"); !s16mSHA.MatchString(c) {
			out = append(out, fmt.Sprintf("%s step %s (line %d): commit %q is not a SHA", stage, row.Get("step"), row.Line, c))
		}
	}
	return out, nil
}

func (s *s16mState) everyStepHasSHA() error {
	var all []string
	for _, stage := range slices.Sorted(maps.Keys(s.records)) {
		p, err := s16mStepProblems(stage, s.records[stage])
		if err != nil {
			return err
		}
		all = append(all, p...)
	}
	if len(all) > 0 {
		return fmt.Errorf("steps without a commit SHA:\n  %s", strings.Join(all, "\n  "))
	}
	return nil
}

var s16mFirstStepRow = regexp.MustCompile(`(?m)^(\| 16\.1 \|[^|\n]*\|)[^|\n]*(\|)`)

func (s *s16mState) stepCheckIsLive() error {
	text, ok := s.records[s16mStage]
	if !ok {
		return errors.New("no DH-S-16 record was read")
	}
	if !s16mFirstStepRow.MatchString(text) {
		return errors.New("the DH-S-16 record has no step 16.1 row")
	}
	broken := s16mFirstStepRow.ReplaceAllString(text, "$1  $2")
	p, err := s16mStepProblems(s16mStage, broken)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(p, func(m string) bool { return strings.Contains(m, "step 16.1 ") }) {
		return fmt.Errorf("the step check does not name step 16.1 without its commit: %q", p)
	}
	// The gate's own --built check agrees.
	x, err := acceptance.ParseIndex(strings.NewReader(s.index))
	if err != nil {
		return err
	}
	rec, err := closure.ParseRecord(closure.RecordPath(s16mStory, s16mStage), strings.NewReader(broken))
	if err != nil {
		return err
	}
	blockers := closure.Stories[s16mStory].Ready(x, s16mStage, rec, true)
	if !slices.ContainsFunc(blockers, func(b string) bool { return strings.HasPrefix(b, "step 16.1 ") && strings.Contains(b, "no commit SHA") }) {
		return fmt.Errorf("ready --built does not name step 16.1 without its commit: %q", blockers)
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_dh_s_16_manual_checks_and_built_record(ctx *godog.ScenarioContext) {
	s := &s16mState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s16mState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return c, nil
	})
	ctx.Step(`^the committed "([^"]*)" and acceptance index$`, s.committedRecord)
	ctx.Step(`^"([^"]*)" runs on it before the status transition$`, s.readyRuns)
	ctx.Step(`^the run exits 0 reporting the record (ready|closed)$`, s.exits0Reporting)
	ctx.Step(`^the guarded rows of "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" all read "passed" with a commit SHA$`, s.guardedRowsPassed)
	ctx.Step(`^the status line reads "([^"]*)" and "([^"]*)" runs on it$`, s.closeRuns)
	ctx.Step(`^the committed fixture tree "([^"]*)"$`, s.fixtureTree)
	ctx.Step(`^"([^"]*)" runs on the fixture tree$`, s.readyRunsOnFixture)
	ctx.Step(`^the run exits 1 naming "([^"]*)" as "([^"]*)"$`, s.exits1Naming)
	ctx.Step(`^its stdout equals the "([^"]*)" section of "([^"]*)"$`, s.stdoutEqualsSection)
	ctx.Step(`^the committed fixture tree is unchanged$`, s.fixtureUnchanged)
	ctx.Step(`^the committed acceptance index and the evidence records of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, s.committedIndexAndRecords)
	ctx.Step(`^the index check runs$`, s.indexCheckRuns)
	ctx.Step(`^built-through reads "([^"]*)"$`, s.builtThroughReads)
	ctx.Step(`^every step of each of those records carries a commit SHA$`, s.everyStepHasSHA)
	ctx.Step(`^the check is live: a record whose first step loses its commit is named by the step check$`, s.stepCheckIsLive)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_dh_s_16_manual_checks_and_built_record(t *testing.T) {
	t.Cleanup(func() {
		if s16mGateDir != "" {
			_ = os.RemoveAll(s16mGateDir)
		}
	})
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_dh_s_16_manual_checks_and_built_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_dh_s_16_manual_checks_and_built_record.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
