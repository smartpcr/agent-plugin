//go:build e2e

package e2e

import (
	"bufio"
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
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

var m1eEntry = closure.Entries["harness:DSH-M2"]

var (
	m1eBuildOnce sync.Once
	m1eGateBin   string
	m1eBuildErr  error
)

// m1eModuleRoot is tools/harness-gate, seen from tools/harness-gate/test/e2e/harness-DSH-M2.
func m1eModuleRoot() (string, error) {
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

func m1eBuildGate() (string, error) {
	m1eBuildOnce.Do(func() {
		root, err := m1eModuleRoot()
		if err != nil {
			m1eBuildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "harness-gate-m1e-e2e-")
		if err != nil {
			m1eBuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		m1eGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", m1eGateBin, ".")
		cmd.Dir = root
		cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
			return strings.HasPrefix(strings.ToUpper(kv), "GOWORK=")
		}), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			m1eBuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return m1eGateBin, m1eBuildErr
}

type m1eRun struct {
	stdout, stderr string
	code           int
}

func m1eExec(bin string, args ...string) (m1eRun, error) {
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return m1eRun{}, fmt.Errorf("run %v: %w", args, err)
		}
		code = exit.ExitCode()
	}
	return m1eRun{stdout: stdout.String(), stderr: stderr.String(), code: code}, nil
}

// m1eBlockers are the "  - " lines of an entry run's stdout.
func m1eBlockers(stdout string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		if b, ok := strings.CutPrefix(line, "  - "); ok {
			out = append(out, b)
		}
	}
	return out
}

type m1eState struct {
	in     closure.EntryInputs
	unmet  []string
	ran    bool
	trees  map[string]string
	hashes map[string][32]byte
	runs   map[string]m1eRun
	repo   string
	live   m1eRun
}

func (s *m1eState) records(openStage, statusLine string) error {
	if openStage != "none" && !slices.Contains(m1eEntry.PriorStages, openStage) {
		return fmt.Errorf("%s is not an M1 stage", openStage)
	}
	s.in = closure.EntryInputs{Records: map[string]*closure.Record{}}
	s.unmet, s.ran = nil, false
	for _, stage := range m1eEntry.PriorStages {
		line := "Closure status: closed at 1a2b3c4"
		if stage == openStage {
			line = statusLine
		}
		rel := closure.RecordPath(m1eEntry.Prior, stage)
		rec, err := closure.ParseDocument(rel, strings.NewReader("# "+stage+" evidence\n\n"+line+"\n"))
		if err != nil {
			return fmt.Errorf("ParseDocument %s: %w", stage, err)
		}
		s.in.Records[stage] = rec
	}
	if len(s.in.Records) != 11 {
		return fmt.Errorf("%d records, want eleven", len(s.in.Records))
	}
	return nil
}

func (s *m1eState) closureAllDone() error {
	var b strings.Builder
	b.WriteString("# harness:DSH-M1 closure record\n\n" + closure.DeliverablesHeading + "\n\n| Deliverable | State | Reason |\n| --- | --- | --- |\n")
	for _, id := range m1eEntry.Deliverables {
		fmt.Fprintf(&b, "| %s | done | passed at the closure commit |\n", id)
	}
	rec, err := closure.ParseDocument(closure.ClosureRecordPath(m1eEntry.Prior), strings.NewReader(b.String()), closure.DeliverablesHeading)
	if err != nil {
		return fmt.Errorf("ParseDocument closure: %w", err)
	}
	s.in.Closure = rec
	return nil
}

func (s *m1eState) noClosure() error {
	s.in.Closure = nil
	return nil
}

func (s *m1eState) builtThrough(stage string) error {
	s.in.BuiltThrough, s.in.IndexMissing = stage, false
	return nil
}

func (s *m1eState) predicateRuns() error {
	s.unmet, s.ran = m1eEntry.Check(s.in), true
	return nil
}

func (s *m1eState) passes() error {
	if !s.ran {
		return errors.New("the entry predicate did not run")
	}
	if len(s.unmet) != 0 {
		return fmt.Errorf("unmet %q, want none", s.unmet)
	}
	return nil
}

func (s *m1eState) failsWith(n int) error {
	if !s.ran {
		return errors.New("the entry predicate did not run")
	}
	if len(s.unmet) != n {
		return fmt.Errorf("%d unmet %q, want %d", len(s.unmet), s.unmet, n)
	}
	return nil
}

func (s *m1eState) unmetNames(text string) error {
	for _, u := range s.unmet {
		if strings.Contains(u, text) {
			return nil
		}
	}
	return fmt.Errorf("no unmet condition contains %q; unmet %q", text, s.unmet)
}

func (s *m1eState) unmetNotNames(text string) error {
	for _, u := range s.unmet {
		if strings.Contains(u, text) {
			return fmt.Errorf("unmet condition %q contains %q", u, text)
		}
	}
	return nil
}

func (s *m1eState) fixtureTrees(open, closed string) error {
	root, err := m1eModuleRoot()
	if err != nil {
		return err
	}
	s.trees = map[string]string{}
	s.hashes = map[string][32]byte{}
	for _, rel := range []string{open, closed} {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		for _, stage := range m1eEntry.PriorStages {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(closure.RecordPath(m1eEntry.Prior, stage)))); err != nil {
				return fmt.Errorf("fixture tree %s: %w", rel, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(acceptance.IndexPath))); err != nil {
			return fmt.Errorf("fixture tree %s: %w", rel, err)
		}
		h, err := hashTree(dir)
		if err != nil {
			return err
		}
		name := filepath.Base(filepath.FromSlash(rel))
		s.trees[name] = dir
		for k, v := range h {
			s.hashes[name+"/"+k] = v
		}
	}
	return nil
}

func (s *m1eState) runOnTrees() error {
	bin, err := m1eBuildGate()
	if err != nil {
		return err
	}
	s.runs = map[string]m1eRun{}
	for name, dir := range s.trees {
		r, err := m1eExec(bin, "entry", "--story", "harness:DSH-M2", "--root", dir)
		if err != nil {
			return err
		}
		s.runs[name] = r
	}
	return nil
}

func (s *m1eState) run(name string) (m1eRun, error) {
	r, ok := s.runs[name]
	if !ok {
		return r, fmt.Errorf("no entry run on %s", name)
	}
	return r, nil
}

func (s *m1eState) exitsNonZero(name string) error {
	r, err := s.run(name)
	if err != nil {
		return err
	}
	if r.code == 0 {
		return fmt.Errorf("entry on %s exited 0, want non-zero\n%s", name, r.stdout)
	}
	if r.code != 1 {
		return fmt.Errorf("entry on %s exited %d (a usage or read error), want 1 (unmet)\nstderr: %s", name, r.code, r.stderr)
	}
	return nil
}

func (s *m1eState) exitsCode(name string, want int) error {
	r, err := s.run(name)
	if err != nil {
		return err
	}
	if r.code != want {
		return fmt.Errorf("entry on %s exited %d, want %d\nstdout: %s\nstderr: %s", name, r.code, want, r.stdout, r.stderr)
	}
	return nil
}

func (s *m1eState) stdoutGolden(name, rel string) error {
	r, err := s.run(name)
	if err != nil {
		return err
	}
	root, err := m1eModuleRoot()
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	got := strings.ReplaceAll(r.stdout, "\r\n", "\n")
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != got {
		return fmt.Errorf("stdout of %s differs from %s\n--- got\n%s--- want\n%s", name, rel, got, norm)
	}
	return nil
}

func (s *m1eState) namesAllOpen(name, bt string) error {
	r, err := s.run(name)
	if err != nil {
		return err
	}
	bs := m1eBlockers(r.stdout)
	for _, stage := range m1eEntry.PriorStages {
		want := stage + ": " + closure.RecordPath(m1eEntry.Prior, stage) + ` reads "Closure status: open"; want closed`
		if !slices.Contains(bs, want) {
			return fmt.Errorf("no blocker %q in %q", want, bs)
		}
	}
	want := "built-through in " + acceptance.IndexPath + " reads " + bt + "; want DH-S-11 or later"
	if !slices.Contains(bs, want) {
		return fmt.Errorf("no blocker %q in %q", want, bs)
	}
	return nil
}

func (s *m1eState) stdoutReports(name, text string) error {
	r, err := s.run(name)
	if err != nil {
		return err
	}
	if !strings.Contains(r.stdout, text) {
		return fmt.Errorf("stdout of %s does not contain %q:\n%s", name, text, r.stdout)
	}
	if bs := m1eBlockers(r.stdout); len(bs) != 0 {
		return fmt.Errorf("a passing run printed blockers %q", bs)
	}
	return nil
}

func (s *m1eState) fixturesUnchanged() error {
	after := map[string][32]byte{}
	for name, dir := range s.trees {
		h, err := hashTree(dir)
		if err != nil {
			return err
		}
		for k, v := range h {
			after[name+"/"+k] = v
		}
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

func (s *m1eState) liveRepo() error {
	root, err := m1eModuleRoot()
	if err != nil {
		return err
	}
	repo := filepath.Dir(filepath.Dir(root))
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(closure.EvidenceDir(m1eEntry.Prior)))); err != nil {
		return fmt.Errorf("live M1 evidence: %w", err)
	}
	s.repo = repo
	return nil
}

func (s *m1eState) runLive() error {
	bin, err := m1eBuildGate()
	if err != nil {
		return err
	}
	s.live, err = m1eExec(bin, "entry", "--root", s.repo)
	return err
}

var (
	m1eStatusLine = regexp.MustCompile(`^Closure status:\s*(.*?)\s*$`)
	m1eClosedAt   = regexp.MustCompile(`^closed at [0-9a-f]{7,40}$`)
	m1eBuiltLine  = regexp.MustCompile(`^built-through:\s*DH-S-(\d+)\s*$`)
)

// m1eOracle reads the live records and index without the gate's parser: which stages
// are not closed, whether the closure record exists, and the built-through number
// (-1 when the index is absent or carries no built-through line).
type m1eOracle struct {
	notClosed     []string
	closureExists bool
	built         int
}

func m1eReadOracle(repo string) (m1eOracle, error) {
	o := m1eOracle{built: -1}
	for _, stage := range m1eEntry.PriorStages {
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(closure.RecordPath(m1eEntry.Prior, stage))))
		if errors.Is(err, os.ErrNotExist) {
			o.notClosed = append(o.notClosed, stage)
			continue
		}
		if err != nil {
			return o, err
		}
		closed := false
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if m := m1eStatusLine.FindStringSubmatch(sc.Text()); m != nil {
				closed = m[1] == "closed" || m1eClosedAt.MatchString(m[1])
				break
			}
		}
		if !closed {
			o.notClosed = append(o.notClosed, stage)
		}
	}
	_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(closure.ClosureRecordPath(m1eEntry.Prior))))
	o.closureExists = err == nil
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(acceptance.IndexPath)))
	if err == nil {
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if m := m1eBuiltLine.FindStringSubmatch(line); m != nil {
				o.built, _ = strconv.Atoi(m[1])
				break
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return o, err
	}
	return o, nil
}

func (s *m1eState) liveIff() error {
	o, err := m1eReadOracle(s.repo)
	if err != nil {
		return err
	}
	in, err := closure.ReadEntryInputs(s.repo, m1eEntry)
	if err != nil {
		return fmt.Errorf("ReadEntryInputs: %w", err)
	}
	contentOK := len(m1eEntry.Check(in)) == 0
	allMet := len(o.notClosed) == 0 && o.closureExists && o.built >= 11 && contentOK
	switch {
	case s.live.code == 2:
		return fmt.Errorf("live entry exited 2 (usage or read error): %s", s.live.stderr)
	case allMet && s.live.code != 0:
		return fmt.Errorf("every live condition holds but entry exited %d:\n%s", s.live.code, s.live.stdout)
	case !allMet && s.live.code == 0:
		return fmt.Errorf("live conditions unmet (open %v, closure %v, built-through %d) but entry exited 0", o.notClosed, o.closureExists, o.built)
	case !allMet && s.live.code != 1:
		return fmt.Errorf("live entry exited %d, want 1", s.live.code)
	}
	return nil
}

func (s *m1eState) liveExact() error {
	o, err := m1eReadOracle(s.repo)
	if err != nil {
		return err
	}
	got := m1eBlockers(s.live.stdout)
	in, err := closure.ReadEntryInputs(s.repo, m1eEntry)
	if err != nil {
		return fmt.Errorf("ReadEntryInputs: %w", err)
	}
	if want := m1eEntry.Check(in); !slices.Equal(got, want) {
		return fmt.Errorf("CLI blockers %q differ from the in-process check %q", got, want)
	}
	if s.live.code == 0 {
		if len(got) != 0 {
			return fmt.Errorf("a passing run printed blockers %q", got)
		}
		return nil
	}
	if !strings.Contains(s.live.stdout, fmt.Sprintf(", %d blocker", len(got))) {
		return fmt.Errorf("head line does not count %d blockers:\n%s", len(got), s.live.stdout)
	}
	// Every stage blocker names a stage the oracle reads as not closed, and vice versa.
	var named []string
	for _, b := range got {
		for _, stage := range m1eEntry.PriorStages {
			if strings.HasPrefix(b, stage+": ") {
				named = append(named, stage)
			}
		}
	}
	if !slices.Equal(named, o.notClosed) {
		return fmt.Errorf("blockers name stages %v, live records leave %v not closed", named, o.notClosed)
	}
	closureMissing := slices.ContainsFunc(got, func(b string) bool { return strings.HasPrefix(b, "no "+m1eEntry.Prior+" closure record") })
	if closureMissing == o.closureExists {
		return fmt.Errorf("closure record exists=%v but a missing-closure blocker is present=%v", o.closureExists, closureMissing)
	}
	builtBlocker := slices.ContainsFunc(got, func(b string) bool { return strings.Contains(b, "built-through") })
	if builtBlocker != (o.built < 11) {
		return fmt.Errorf("built-through reads %d but a built-through blocker is present=%v", o.built, builtBlocker)
	}
	if o.built >= 0 && o.built < 11 {
		want := fmt.Sprintf("built-through in %s reads DH-S-%d; want DH-S-11 or later", acceptance.IndexPath, o.built)
		if !slices.Contains(got, want) {
			return fmt.Errorf("no blocker %q in %q", want, got)
		}
	}
	for _, b := range got {
		isStage := slices.ContainsFunc(m1eEntry.PriorStages, func(st string) bool { return strings.HasPrefix(b, st+": ") })
		isClosure := strings.Contains(b, closure.ClosureRecordPath(m1eEntry.Prior))
		isBuilt := strings.Contains(b, "built-through")
		if !isStage && !isClosure && !isBuilt {
			return fmt.Errorf("blocker %q names no entry condition", b)
		}
	}
	return nil
}

func InitializeScenario_m2_entry_and_evidence_scaffolding_m1_closure_entry_check(ctx *godog.ScenarioContext) {
	s := &m1eState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = m1eState{}
		return c, nil
	})
	ctx.Step(`^eleven in-memory M1 evidence records of which "([^"]*)" reads "([^"]*)"$`, s.records)
	ctx.Step(`^an in-memory M1 closure record listing every M1 deliverable as done$`, s.closureAllDone)
	ctx.Step(`^no in-memory M1 closure record$`, s.noClosure)
	ctx.Step(`^an in-memory built-through of "([^"]*)"$`, s.builtThrough)
	ctx.Step(`^the entry predicate runs$`, s.predicateRuns)
	ctx.Step(`^it passes$`, s.passes)
	ctx.Step(`^it fails with exactly (\d+) unmet conditions?$`, s.failsWith)
	ctx.Step(`^an unmet condition names "([^"]*)"$`, s.unmetNames)
	ctx.Step(`^no unmet condition names "([^"]*)"$`, s.unmetNotNames)
	ctx.Step(`^the committed fixture trees "([^"]*)" and "([^"]*)"$`, s.fixtureTrees)
	ctx.Step(`^"harness-gate entry --story harness:DSH-M2 --root <tree>" runs on each tree$`, s.runOnTrees)
	ctx.Step(`^the run on "([^"]*)" exits non-zero$`, s.exitsNonZero)
	ctx.Step(`^the run on "([^"]*)" exits (\d+)$`, s.exitsCode)
	ctx.Step(`^the stdout of "([^"]*)" equals the committed "([^"]*)"$`, s.stdoutGolden)
	ctx.Step(`^the stdout of "([^"]*)" names all eleven M1 records as open and built-through "([^"]*)"$`, s.namesAllOpen)
	ctx.Step(`^the stdout of "([^"]*)" reports "([^"]*)"$`, s.stdoutReports)
	ctx.Step(`^the committed entry fixture trees are unchanged$`, s.fixturesUnchanged)
	ctx.Step(`^the repository's live M1 evidence records and acceptance index$`, s.liveRepo)
	ctx.Step(`^"harness-gate entry" runs against the repository root$`, s.runLive)
	ctx.Step(`^it exits 0 if and only if every live condition holds$`, s.liveIff)
	ctx.Step(`^its blockers name exactly the unmet live conditions$`, s.liveExact)
}

func TestE2E_m2_entry_and_evidence_scaffolding_m1_closure_entry_check(t *testing.T) {
	t.Cleanup(func() {
		if m1eGateBin != "" {
			_ = os.RemoveAll(filepath.Dir(m1eGateBin))
		}
	})
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_m2_entry_and_evidence_scaffolding_m1_closure_entry_check,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"m2_entry_and_evidence_scaffolding_m1_closure_entry_check.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
