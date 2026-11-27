//go:build e2e

package e2e

import (
	"bytes"
	"context"
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

const s17wStage = "DH-S-17"

var (
	// s17wKeyMarker is the model-key name pattern of the harness-gate workflow lint
	// (scenarios_test.go modelKeyRefs): the variables the signed-in routes scrub and
	// the API-key names of every provider the harness has known.
	s17wKeyMarker  = regexp.MustCompile(`(?i)[a-z0-9_]*(api_key|auth_token|oauth_token|gh_token|github_token|anthropic|openai|deepseek)[a-z0-9_]*`)
	s17wScenarioID = regexp.MustCompile(`^DH-E-[A-Z]+-[0-9]+$`)

	s17wBuildOnce sync.Once
	s17wGateBin   string
	s17wBuildErr  error
)

type s17wState struct {
	repo, module string

	workflowRel, workflow string
	refs                  []string
	job                   string

	indexRel string
	stdout   string
	stderr   string
	code     int
}

func s17wModuleRoot() (string, error) {
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

func s17wGoEnv() []string {
	return append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		u := strings.ToUpper(kv)
		return strings.HasPrefix(u, "GOWORK=") || strings.HasPrefix(u, "GOFLAGS=")
	}), "GOWORK=off")
}

func s17wBuildGate(module string) (string, error) {
	s17wBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "harness-gate-s17w-e2e-")
		if err != nil {
			s17wBuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		s17wGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", s17wGateBin, ".")
		cmd.Dir = module
		cmd.Env = s17wGoEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			s17wBuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return s17wGateBin, s17wBuildErr
}

// s17wShowHEAD is the committed text of a repository path, line endings normalised.
func s17wShowHEAD(repo, rel string) (string, error) {
	cmd := exec.Command("git", "show", "HEAD:"+rel)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git show HEAD:%s: %v\n%s", rel, err, stderr.String())
	}
	return strings.ReplaceAll(stdout.String(), "\r\n", "\n"), nil
}

func s17wReadLF(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

// s17wModelKeyRefs mirrors the harness-gate workflow lint: every secret reference,
// and every non-comment line naming a model key.
func s17wModelKeyRefs(workflow string) []string {
	var refs []string
	for i, l := range strings.Split(workflow, "\n") {
		if j := strings.Index(l, "#"); j >= 0 && (j == 0 || l[j-1] == ' ') {
			l = l[:j]
		}
		if strings.Contains(l, "secrets.") || strings.Contains(l, "secrets[") {
			refs = append(refs, fmt.Sprintf("line %d: secret reference: %s", i+1, strings.TrimSpace(l)))
			continue
		}
		if m := s17wKeyMarker.FindString(l); m != "" {
			refs = append(refs, fmt.Sprintf("line %d: names a model key (%s): %s", i+1, m, strings.TrimSpace(l)))
		}
	}
	return refs
}

// s17wJob is the body of a top-level job of a workflow (the lines after "  name:"
// up to the next job), trimmed to end with one newline as the golden is.
func s17wJob(workflow, name string) (string, error) {
	_, body, ok := strings.Cut(workflow, "\n  "+name+":\n")
	if !ok {
		return "", fmt.Errorf("workflow has no %s job", name)
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if len(l) > 2 && strings.HasPrefix(l, "  ") && l[2] != ' ' && l[2] != '#' {
			body = strings.Join(lines[:i], "\n")
			break
		}
	}
	return strings.TrimRight(body, "\n") + "\n", nil
}

func s17wJobField(job, field string) string {
	for _, l := range strings.Split(job, "\n") {
		if v, ok := strings.CutPrefix(l, "    "+field+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// s17wTechSpecS17IDs are the scenario IDs of tech-spec sec. 4.2 rows 5-18.
func s17wTechSpecS17IDs(spec string) ([]string, error) {
	var ids []string
	for _, l := range strings.Split(spec, "\n") {
		cells := strings.Split(l, "|")
		if len(cells) < 4 {
			continue
		}
		row := strings.TrimSpace(cells[1])
		if row != "5-16" && row != "17" && row != "18" {
			continue
		}
		if strings.TrimSpace(cells[3]) != "S17" {
			return nil, fmt.Errorf("tech-spec row %s is not an S17 row: %q", row, l)
		}
		for _, n := range strings.Split(cells[2], ",") {
			n = strings.TrimSpace(n)
			if !strings.HasPrefix(n, "DH-E-") {
				n = "DH-E-" + n
			}
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("tech-spec sec. 4.2 has no rows 5-18")
	}
	return ids, nil
}

func (s *s17wState) committedWorkflow(rel string) error {
	text, err := s17wShowHEAD(s.repo, rel)
	if err != nil {
		return err
	}
	worktree, err := s17wReadLF(filepath.Join(s.repo, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if worktree != text {
		return fmt.Errorf("%s differs from its committed text; the scenario proves the committed file", rel)
	}
	s.workflowRel, s.workflow = rel, text
	return nil
}

func (s *s17wState) seedKey(job, env, secret string) error {
	marker := "\n  " + job + ":\n"
	i := strings.Index(s.workflow, marker)
	if i < 0 {
		return fmt.Errorf("workflow has no %s job", job)
	}
	anchor := "\n    timeout-minutes:"
	j := strings.Index(s.workflow[i:], anchor)
	if j < 0 {
		return fmt.Errorf("%s job has no timeout-minutes line to seed after", job)
	}
	at := i + j + 1
	end := strings.Index(s.workflow[at:], "\n")
	at += end + 1
	seed := "    env:\n      " + env + ": placeholder\n      LAB: ${{ secrets." + secret + " }}\n"
	s.workflow = s.workflow[:at] + seed + s.workflow[at:]
	return nil
}

func (s *s17wState) replaceRunsOn(job, value string) error {
	body, err := s17wJob(s.workflow, job)
	if err != nil {
		return err
	}
	old := s17wJobField(body, "runs-on")
	if old == "" {
		return fmt.Errorf("%s job has no runs-on", job)
	}
	replaced := strings.Replace(body, "    runs-on: "+old+"\n", "    runs-on: "+value+"\n", 1)
	s.workflow = strings.Replace(s.workflow, "\n  "+job+":\n"+strings.TrimRight(body, "\n"), "\n  "+job+":\n"+strings.TrimRight(replaced, "\n"), 1)
	return nil
}

func (s *s17wState) lintScans() error {
	s.refs = s17wModelKeyRefs(s.workflow)
	job, err := s17wJob(s.workflow, "s17")
	if err != nil {
		return err
	}
	s.job = job
	return nil
}

// gateLintTestsPass runs the lint tests of the harness-gate module itself on the
// committed workflow: the seeded-key test of the lint and the s17 job check.
func (s *s17wState) gateLintTestsPass() error {
	cmd := exec.Command("go", "test", "-run", "^(TestModelKeyRefsFindsSeededKeys|TestS17JobIsKeylessAndSelfHosted)$", ".")
	cmd.Dir = s.module
	cmd.Env = s17wGoEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("harness-gate workflow lint tests failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("ok")) {
		return fmt.Errorf("harness-gate workflow lint tests reported no result:\n%s", out)
	}
	return nil
}

func (s *s17wState) noModelKeyRefs() error {
	if len(s.refs) > 0 {
		return fmt.Errorf("%s names a model key or reads a secret:\n%s", s.workflowRel, strings.Join(s.refs, "\n"))
	}
	if strings.Contains(s.job, "secrets") {
		return errors.New("s17 job mentions secrets")
	}
	return nil
}

func (s *s17wState) lintReports(n int) error {
	if len(s.refs) != n {
		return fmt.Errorf("lint reported %d references, want %d: %q", len(s.refs), n, s.refs)
	}
	return nil
}

func s17wSelfHosted(runsOn string) bool {
	v, ok := strings.CutPrefix(runsOn, "[")
	if !ok {
		return false
	}
	v, ok = strings.CutSuffix(v, "]")
	if !ok {
		return false
	}
	labels := strings.Split(v, ",")
	return len(labels) > 1 && strings.TrimSpace(labels[0]) == "self-hosted"
}

func (s *s17wState) runsOnSelfHosted(job string) error {
	body, err := s17wJob(s.workflow, job)
	if err != nil {
		return err
	}
	runsOn := s17wJobField(body, "runs-on")
	if !s17wSelfHosted(runsOn) {
		return fmt.Errorf("%s runs-on = %q, want a self-hosted label list (M2-EN-8)", job, runsOn)
	}
	if !strings.HasPrefix(runsOn, "[self-hosted, linux, ") {
		return fmt.Errorf("%s runs-on = %q, want a self-hosted Linux label list (E1)", job, runsOn)
	}
	return nil
}

func (s *s17wState) runsOnNotSelfHosted(job string) error {
	body, err := s17wJob(s.workflow, job)
	if err != nil {
		return err
	}
	if runsOn := s17wJobField(body, "runs-on"); s17wSelfHosted(runsOn) {
		return fmt.Errorf("%s runs-on = %q was accepted as self-hosted", job, runsOn)
	}
	return nil
}

func (s *s17wState) dispatchOnly(job, stage string) error {
	body, err := s17wJob(s.workflow, job)
	if err != nil {
		return err
	}
	want := "github.event_name == 'workflow_dispatch' && inputs.stage == '" + stage + "'"
	if got := s17wJobField(body, "if"); got != want {
		return fmt.Errorf("%s if = %q, want %q", job, got, want)
	}
	return nil
}

func (s *s17wState) golden(rel string) (string, error) {
	return s17wReadLF(filepath.Join(s.module, filepath.FromSlash(rel)))
}

func (s *s17wState) jobEqualsGolden(job, rel string) error {
	want, err := s.golden(rel)
	if err != nil {
		return err
	}
	body, err := s17wJob(s.workflow, job)
	if err != nil {
		return err
	}
	if body != want {
		return fmt.Errorf("%s job differs from %s:\n--- got\n%s\n--- want\n%s", job, rel, body, want)
	}
	return nil
}

func (s *s17wState) jobDiffersFromGolden(job, rel string) error {
	if err := s.jobEqualsGolden(job, rel); err == nil {
		return fmt.Errorf("seeded %s job still equals %s", job, rel)
	}
	return nil
}

func (s *s17wState) committedIndex(rel string) error {
	text, err := s17wShowHEAD(s.repo, rel)
	if err != nil {
		return err
	}
	if !strings.Contains(text, s17wStage) {
		return fmt.Errorf("committed %s has no %s entry", rel, s17wStage)
	}
	s.indexRel = rel
	return nil
}

func (s *s17wState) requireRuns(stage string) error {
	bin, err := s17wBuildGate(s.module)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "require", "--root", s.repo, "--stage", stage)
	cmd.Dir = s.module
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	s.code = 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		s.code = exit.ExitCode()
	} else if err != nil {
		return err
	}
	s.stdout = strings.ReplaceAll(stdout.String(), "\r\n", "\n")
	s.stderr = stderr.String()
	return nil
}

func (s *s17wState) exits(code int) error {
	if s.code != code {
		return fmt.Errorf("exit code %d, want %d (stderr: %s)", s.code, code, s.stderr)
	}
	return nil
}

func (s *s17wState) printedIDs() []string {
	line := strings.TrimSpace(s.stdout)
	if line == "" {
		return nil
	}
	return strings.Split(line, ",")
}

func (s *s17wState) printsDistinct(n int) error {
	ids := s.printedIDs()
	if len(ids) != n {
		return fmt.Errorf("printed %d IDs, want %d: %q", len(ids), n, ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !s17wScenarioID.MatchString(id) {
			return fmt.Errorf("printed %q, not a scenario ID", id)
		}
		if seen[id] {
			return fmt.Errorf("printed %s twice", id)
		}
		seen[id] = true
	}
	return nil
}

func (s *s17wState) matchesTechSpec() error {
	spec, err := s17wShowHEAD(s.repo, "docs/stories/harness-DSH-M2/tech-spec.md")
	if err != nil {
		return err
	}
	want, err := s17wTechSpecS17IDs(spec)
	if err != nil {
		return err
	}
	got := slices.Clone(s.printedIDs())
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("require printed %q, tech-spec sec. 4.2 rows 5-18 list %q", got, want)
	}
	return nil
}

func (s *s17wState) outputEqualsGolden(rel string) error {
	want, err := s.golden(rel)
	if err != nil {
		return err
	}
	if s.stdout != want {
		return fmt.Errorf("require output %q differs from %s %q", s.stdout, rel, want)
	}
	return nil
}

func (s *s17wState) stdoutEmpty() error {
	if s.stdout != "" {
		return fmt.Errorf("stdout = %q, want nothing", s.stdout)
	}
	return nil
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_signed_in_route_runner_and_s17_workflow(ctx *godog.ScenarioContext) {
	s := &s17wState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		module, err := s17wModuleRoot()
		if err != nil {
			return c, err
		}
		*s = s17wState{module: module, repo: filepath.Dir(filepath.Dir(module))}
		return c, nil
	})

	ctx.Step(`^the committed workflow "([^"]+)"$`, s.committedWorkflow)
	ctx.Step(`^the "([^"]+)" job is seeded with env "([^"]+)" read from secret "([^"]+)"$`, s.seedKey)
	ctx.Step(`^the "([^"]+)" job's runs-on is replaced by "([^"]+)"$`, s.replaceRunsOn)
	ctx.Step(`^the workflow lint in tools/harness-gate scans it$`, s.lintScans)
	ctx.Step(`^the harness-gate workflow lint tests pass on the committed workflow$`, s.gateLintTestsPass)
	ctx.Step(`^no input, env or secret reference names a model key$`, s.noModelKeyRefs)
	ctx.Step(`^the lint reports (\d+) model-key references$`, s.lintReports)
	ctx.Step(`^the "([^"]+)" job's runs-on is a self-hosted label list$`, s.runsOnSelfHosted)
	ctx.Step(`^the "([^"]+)" job's runs-on is not a self-hosted label list$`, s.runsOnNotSelfHosted)
	ctx.Step(`^the "([^"]+)" job runs only on workflow_dispatch of stage "([^"]+)"$`, s.dispatchOnly)
	ctx.Step(`^the "([^"]+)" job equals the committed golden "([^"]+)"$`, s.jobEqualsGolden)
	ctx.Step(`^the "([^"]+)" job differs from the committed golden "([^"]+)"$`, s.jobDiffersFromGolden)

	ctx.Step(`^the committed index "([^"]+)"$`, s.committedIndex)
	ctx.Step(`^"harness-gate require --stage ([^"]+)" runs$`, s.requireRuns)
	ctx.Step(`^it exits (\d+)$`, s.exits)
	ctx.Step(`^it prints exactly (\d+) distinct scenario IDs$`, s.printsDistinct)
	ctx.Step(`^the printed IDs are the S17 IDs of tech-spec sec\. 4\.2 rows 5-18$`, s.matchesTechSpec)
	ctx.Step(`^the output equals the committed golden "([^"]+)"$`, s.outputEqualsGolden)
	ctx.Step(`^it prints nothing on stdout$`, s.stdoutEmpty)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_signed_in_route_runner_and_s17_workflow(t *testing.T) {
	t.Cleanup(func() {
		if s17wGateBin != "" {
			os.RemoveAll(filepath.Dir(s17wGateBin))
		}
	})
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_signed_in_route_runner_and_s17_workflow,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_signed_in_route_runner_and_s17_workflow.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("dh-s-17 signed-in route runner and s17 workflow e2e scenarios failed")
	}
}
