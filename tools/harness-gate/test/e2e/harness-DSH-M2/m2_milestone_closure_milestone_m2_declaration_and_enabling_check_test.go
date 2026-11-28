//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
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

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// m2dIndex lists the step 16.8 guarded scenario DH-E-STORE-1 under DH-S-16 and one
// acceptance scenario each under DH-S-17 and DH-S-18 (the closure package's own
// milestone fixture shape).
const m2dIndex = `built-through: DH-S-16
stages:
  DH-S-16:
    - id: DH-E-STORE-1
      authored: DH-S-7
      credential: none
  DH-S-17:
    - id: DH-E-SDK-1
      authored: DH-S-15
      credential: none
  DH-S-18:
    - id: DH-E-FS-2
      authored: DH-S-12
      credential: none
`

// m2dRecord is a stage record reading status, with steps, manual checks and owner
// defaults complete and the given scenario rows.
func m2dRecord(status string, rows ...string) string {
	return "# evidence\n\nClosure status: " + status + `

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 16.1 | DH-D-BUNDLE-3 | 0123abc | |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
` + strings.Join(rows, "\n") + `

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 16.3 | one headless prompt | answered | tester | 0123abc | ` + "`manual`" + ` |

## Owner defaults (M2-C-4)

| Default | Raised in | Confirmed | By | Commit |
| --- | --- | --- | --- | --- |
| ` + "`dsh headless`" + ` progress | U-K2-4 | confirmed as is | owner | 0123abc |
`
}

func m2dRow(id, result string) string {
	return "| " + id + " | Linux (WSL) | guarded | required | wsl lane | 0123abc | " + result + " | run.txt |"
}

var (
	m2dBuildOnce sync.Once
	m2dGateBin   string
	m2dBuildErr  error

	m2dDshPattern  = regexp.MustCompile(`(?i)\bdsh\b|src/harness|cmd/dsh`)
	m2dTagTrigger  = regexp.MustCompile(`^\s*(tags|tags-ignore|release|create)\s*:`)
	m2dGoreleaser  = regexp.MustCompile(`(?i)goreleaser`)
	m2dEgressLine  = regexp.MustCompile(`^([a-z][a-z0-9-]*): \[(.*)\]$`)
	m2dBaseHeader  = regexp.MustCompile(`^# base: ([0-9a-f]{40})$`)
	m2dCopyHeading = regexp.MustCompile(`^=== (\S+) ([0-9a-f]{40})$`)
)

// m2dModuleRoot is the harness-gate module, three levels above this package.
func m2dModuleRoot() (string, error) {
	mod, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(mod, "go.mod")); err != nil {
		return "", fmt.Errorf("%s is not the harness-gate module: %w", mod, err)
	}
	return mod, nil
}

func m2dRepoRoot() (string, error) {
	mod, err := m2dModuleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(mod)), nil
}

func m2dEnv() []string {
	return append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		u := strings.ToUpper(kv)
		return strings.HasPrefix(u, "GOWORK=") || strings.HasPrefix(u, "GOFLAGS=")
	}), "GOWORK=off")
}

func m2dBuildGate() (string, error) {
	m2dBuildOnce.Do(func() {
		mod, err := m2dModuleRoot()
		if err != nil {
			m2dBuildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "harness-gate-m2d-e2e-")
		if err != nil {
			m2dBuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		m2dGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", m2dGateBin, ".")
		cmd.Dir, cmd.Env = mod, m2dEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			m2dBuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return m2dGateBin, m2dBuildErr
}

type m2dRun struct {
	stdout, stderr string
	code           int
}

func m2dExec(dir, bin string, args ...string) (m2dRun, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = dir, m2dEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return m2dRun{}, fmt.Errorf("run %s %v: %w", bin, args, err)
		}
		code = exit.ExitCode()
	}
	return m2dRun{stdout: strings.ReplaceAll(stdout.String(), "\r\n", "\n"), stderr: stderr.String(), code: code}, nil
}

func m2dReadLF(root, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), err
}

func m2dBlobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

type m2dCopy struct {
	path, blob string
	content    []byte
}

type m2dState struct {
	// Milestone predicate.
	story   closure.Story
	index   *acceptance.Index
	records map[string]*closure.Record
	through string
	refused []string

	// Nothing published.
	root, goldenRel, base string
	releaseFiles          []string
	copies                map[string]m2dCopy
	release               m2dRun
	egressRun             string
}

func (s *m2dState) parse(name, src string) (*closure.Record, error) {
	return closure.ParseRecord(name, strings.NewReader(src))
}

func (s *m2dState) givenIndex() error {
	s.story = closure.Stories[closure.DefaultStory]
	// The story's real milestone rule, without the per-platform rows its committed
	// records owe, so the fixtures vary only what the scenario names.
	s.story.Obligations = nil
	m := s.story.Milestone
	if m.Name != "M2" || !slices.Equal(m.Closed, []string{"DH-S-17", "DH-S-18"}) || m.GuardedStage != "DH-S-16" {
		return fmt.Errorf("story milestone rule %+v, want M2 closing DH-S-17 and DH-S-18 with DH-S-16 guarded", m)
	}
	if want := []string{"DH-E-STORE-1", "DH-E-STORE-2", "DH-E-STORE-6", "DH-E-FS-3"}; !slices.Equal(m.Guarded, want) {
		return fmt.Errorf("guarded scenarios %q, want the step 16.8 four %q", m.Guarded, want)
	}
	x, err := acceptance.ParseIndex(strings.NewReader(m2dIndex))
	if err != nil {
		return fmt.Errorf("ParseIndex: %w", err)
	}
	s.index = x
	return nil
}

func (s *m2dState) records16(fs3 string) (*closure.Record, error) {
	return s.parse("DH-S-16.md", m2dRecord("open",
		m2dRow("DH-E-STORE-1", "passed"), m2dRow("DH-E-STORE-2", "passed"),
		m2dRow("DH-E-STORE-6", "passed"), m2dRow("DH-E-FS-3", fs3)))
}

func (s *m2dState) givenRecords(s17, s18 string) error {
	r16, err := s.records16("passed")
	if err != nil {
		return err
	}
	r17, err := s.parse("DH-S-17.md", m2dRecord(s17, m2dRow("DH-E-SDK-1", "passed")))
	if err != nil {
		return err
	}
	r18, err := s.parse("DH-S-18.md", m2dRecord(s18, m2dRow("DH-E-FS-2", "passed")))
	if err != nil {
		return err
	}
	if r17.StatusValue != s17 || r18.StatusValue != s18 {
		return fmt.Errorf("fixture status lines read %q and %q, want %q and %q", r17.StatusValue, r18.StatusValue, s17, s18)
	}
	s.records = map[string]*closure.Record{"DH-S-16": r16, "DH-S-17": r17, "DH-S-18": r18}
	return nil
}

func (s *m2dState) checkRuns() error {
	s.through, s.refused = s.story.Check(s.index, s.records)
	return nil
}

func (s *m2dState) refusedNaming(stage string) error {
	if s.through != "" {
		return fmt.Errorf("refused milestone still advances built-through to %q", s.through)
	}
	if len(s.refused) != 1 {
		return fmt.Errorf("%d refusals, want exactly 1: %q", len(s.refused), s.refused)
	}
	p := s.refused[0]
	for _, want := range []string{"M2 refused", stage + " is not closed", closure.NotYetClosed} {
		if !strings.Contains(p, want) {
			return fmt.Errorf("refusal %q lacks %q", p, want)
		}
	}
	return nil
}

func (s *m2dState) noRefusalNames(stage string) error {
	for _, p := range s.refused {
		if strings.Contains(p, stage) {
			return fmt.Errorf("refusal %q names %s, which is closed", p, stage)
		}
	}
	return nil
}

func m2dClosureDoc(decl string) []byte {
	return []byte("# closure\n\n" + decl + "\n")
}

func (s *m2dState) yesRefusedNoAllowed(yes, no string) error {
	p := s.story.DeclarationProblems("closure.md", m2dClosureDoc(yes), s.index, s.records)
	if len(p) == 0 {
		return fmt.Errorf("%q accepted while the predicate refuses M2", yes)
	}
	if !slices.ContainsFunc(p, func(x string) bool { return strings.Contains(x, "declares M2 reached") }) {
		return fmt.Errorf("declaration problems %q do not say the declaration is premature", p)
	}
	if p := s.story.DeclarationProblems("closure.md", m2dClosureDoc(no), s.index, s.records); len(p) != 0 {
		return fmt.Errorf("%q refused: %q", no, p)
	}
	return nil
}

func (s *m2dState) accepted(through string) error {
	if len(s.refused) != 0 {
		return fmt.Errorf("M2 refused: %q", s.refused)
	}
	if s.through != through {
		return fmt.Errorf("built-through advances to %q, want %q", s.through, through)
	}
	if got := s.story.Milestone.BuiltThrough(); got != through {
		return fmt.Errorf("milestone BuiltThrough %q, want %q", got, through)
	}
	return nil
}

func (s *m2dState) yesAllowedOnceAdvanced(decl, through string) error {
	// Built-through not yet advanced: yes is still premature.
	if p := s.story.DeclarationProblems("closure.md", m2dClosureDoc(decl), s.index, s.records); len(p) != 1 || !strings.Contains(p[0], "built-through") {
		return fmt.Errorf("built-through %q: declaration problems %q, want one naming built-through", s.index.BuiltThrough, p)
	}
	x, err := acceptance.ParseIndex(strings.NewReader(strings.Replace(m2dIndex, "built-through: DH-S-16", "built-through: "+through, 1)))
	if err != nil {
		return err
	}
	if p := s.story.DeclarationProblems("closure.md", m2dClosureDoc(decl), x, s.records); len(p) != 0 {
		return fmt.Errorf("built-through %s: declaration problems %q, want none", through, p)
	}
	return nil
}

func (s *m2dState) guardedNotRun(id string) error {
	if id != "DH-E-FS-3" {
		return fmt.Errorf("fixture seeds only DH-E-FS-3 as not run, got %s", id)
	}
	r16, err := s.records16("not run (no editor script on the lane)")
	if err != nil {
		return err
	}
	recs := map[string]*closure.Record{"DH-S-16": r16, "DH-S-17": s.records["DH-S-17"], "DH-S-18": s.records["DH-S-18"]}
	through, p := s.story.Check(s.index, recs)
	if through != "" || len(p) != 1 {
		return fmt.Errorf("guarded row not run: built-through %q, problems %q; want refused with one problem", through, p)
	}
	for _, want := range []string{"M2 refused", "guarded scenarios of DH-S-16", id, "not run"} {
		if !strings.Contains(p[0], want) {
			return fmt.Errorf("refusal %q lacks %q", p[0], want)
		}
	}
	return nil
}

func (s *m2dState) committedRefused(decl string) error {
	root, err := m2dRepoRoot()
	if err != nil {
		return err
	}
	bin, err := m2dBuildGate()
	if err != nil {
		return err
	}
	r, err := m2dExec(root, bin, "milestone", "--root", root)
	if err != nil {
		return err
	}
	src, err := m2dReadLF(root, closure.ClosureRecordPath(closure.DefaultStory))
	if err != nil {
		return err
	}
	d := closure.Stories[closure.DefaultStory].Milestone.ReadDeclaration(src)
	got := "M2 reached: " + d.Value
	switch {
	case r.code == 1 && strings.Contains(r.stdout, "M2: REFUSED"):
		if d.Count != 1 || got != strings.TrimSuffix(decl, ".") {
			return fmt.Errorf("milestone refused, but the closure record declares %q (%d lines), want %q", got, d.Count, decl)
		}
	case r.code == 0 && strings.Contains(r.stdout, "M2: reached"):
		return fmt.Errorf("milestone command accepts M2 on the committed records; the scenario expects %q:\n%s", decl, r.stdout)
	default:
		return fmt.Errorf("milestone exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return nil
}

// Nothing published.

func (s *m2dState) committedReleaseFiles(workflow, goreleaser string) error {
	root, err := m2dRepoRoot()
	if err != nil {
		return err
	}
	s.root, s.releaseFiles = root, []string{workflow, goreleaser}
	for _, rel := range s.releaseFiles {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("committed release file: %w", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "src", "harness", "internal", "boot", "profile")); err != nil {
		return fmt.Errorf("default profile bundles: %w", err)
	}
	return nil
}

func (s *m2dState) baseCopies(rel, base string) error {
	golden, err := m2dReadLF(s.root, rel)
	if err != nil {
		return err
	}
	s.goldenRel, s.copies = rel, map[string]m2dCopy{}
	var cur string
	for _, l := range strings.SplitAfter(string(golden), "\n") {
		if m := m2dCopyHeading.FindStringSubmatch(strings.TrimRight(l, "\n")); m != nil {
			if _, twice := s.copies[m[1]]; twice {
				return fmt.Errorf("%s holds %s twice", rel, m[1])
			}
			cur = m[1]
			s.copies[cur] = m2dCopy{path: m[1], blob: m[2]}
			continue
		}
		if cur != "" {
			c := s.copies[cur]
			c.content = append(c.content, l...)
			s.copies[cur] = c
			continue
		}
		if m := m2dBaseHeader.FindStringSubmatch(strings.TrimRight(l, "\n")); m != nil {
			s.base = m[1]
		}
	}
	if s.base != base {
		return fmt.Errorf("%s records base %q, want %s", rel, s.base, base)
	}
	for _, f := range s.releaseFiles {
		if _, ok := s.copies[f]; !ok {
			return fmt.Errorf("%s has no copy of %s", rel, f)
		}
	}
	if len(s.copies) != len(s.releaseFiles) {
		return fmt.Errorf("%s holds %d copies, want %d", rel, len(s.copies), len(s.releaseFiles))
	}
	return nil
}

func (s *m2dState) releaseCheckRuns() error {
	bin, err := m2dBuildGate()
	if err != nil {
		return err
	}
	if s.release, err = m2dExec(s.root, bin, "release", "--root", s.root); err != nil {
		return err
	}
	// The egress set computed from the shipped bundles, held to egress.golden by the
	// profile package's own test (TC-08a, NG-7).
	cmd := exec.Command("go", "test", "./internal/boot/profile", "-run", "^TestDefaultProfilesSendNothing$")
	cmd.Dir, cmd.Env = filepath.Join(s.root, "src", "harness"), m2dEnv()
	out, err := cmd.CombinedOutput()
	s.egressRun = string(out)
	if err != nil {
		return fmt.Errorf("computing the default egress set (TestDefaultProfilesSendNothing): %v\n%s", err, out)
	}
	return nil
}

func (s *m2dState) filesEqualCopies() error {
	for _, rel := range s.releaseFiles {
		c := s.copies[rel]
		if got := m2dBlobID(c.content); got != c.blob {
			return fmt.Errorf("the copy of %s hashes to %s, the golden records %s", rel, got, c.blob)
		}
		cur, err := m2dReadLF(s.root, rel)
		if err != nil {
			return err
		}
		if !bytes.Equal(cur, c.content) {
			return fmt.Errorf("%s differs from its copy at the base commit %s", rel, s.base)
		}
		cmd := exec.Command("git", "-C", s.root, "rev-parse", "--verify", "--quiet", s.base+":"+rel)
		out, err := cmd.Output()
		if err != nil {
			shallow, _ := exec.Command("git", "-C", s.root, "rev-parse", "--is-shallow-repository").Output()
			if strings.TrimSpace(string(shallow)) == "true" {
				continue // A shallow clone lacks the base; the blob recompute above still holds.
			}
			return fmt.Errorf("git rev-parse %s:%s: %v", s.base, rel, err)
		}
		if got := strings.TrimSpace(string(out)); got != c.blob {
			return fmt.Errorf("%s is blob %s at the base commit, the golden records %s", rel, got, c.blob)
		}
	}
	return nil
}

func (s *m2dState) noDshJob() error {
	for _, rel := range s.releaseFiles {
		cur, err := m2dReadLF(s.root, rel)
		if err != nil {
			return err
		}
		for i, l := range strings.Split(string(cur), "\n") {
			if m2dDshPattern.MatchString(l) {
				return fmt.Errorf("%s:%d names dsh: %q", rel, i+1, strings.TrimSpace(l))
			}
		}
	}
	workflows, err := filepath.Glob(filepath.Join(s.root, ".github", "workflows", "*.y*ml"))
	if err != nil {
		return err
	}
	if len(workflows) == 0 {
		return errors.New("no workflows found")
	}
	for _, path := range workflows {
		rel := ".github/workflows/" + filepath.Base(path)
		if slices.Contains(s.releaseFiles, rel) {
			continue
		}
		cur, err := m2dReadLF(s.root, rel)
		if err != nil {
			return err
		}
		for i, l := range strings.Split(string(cur), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if m2dTagTrigger.MatchString(l) || m2dGoreleaser.MatchString(l) {
				return fmt.Errorf("%s:%d may publish: %q", rel, i+1, strings.TrimSpace(l))
			}
		}
	}
	return nil
}

func (s *m2dState) egressEmpty(a, b string) error {
	if !strings.Contains(s.egressRun, "ok") {
		return fmt.Errorf("egress computation did not pass:\n%s", s.egressRun)
	}
	src, err := m2dReadLF(s.root, "src/harness/internal/boot/profile/testdata/egress.golden")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, l := range strings.Split(string(src), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m := m2dEgressLine.FindStringSubmatch(l)
		if m == nil {
			return fmt.Errorf("egress.golden:%d: %q is not \"<profile>: [...]\"", i+1, l)
		}
		if strings.TrimSpace(m[2]) != "" {
			return fmt.Errorf("default profile %s sends %s (NG-7)", m[1], m[2])
		}
		seen[m[1]] = true
	}
	for _, p := range []string{a, b} {
		if !seen[p] {
			return fmt.Errorf("no egress set for the default profile %s", p)
		}
	}
	return nil
}

func (s *m2dState) releaseCommandPasses() error {
	r := s.release
	if r.code != 0 {
		return fmt.Errorf("release exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{
		strings.Join(s.releaseFiles, " and ") + " equal their copies at the base commit " + s.base[:7],
		"no dsh release job",
		"is empty",
	} {
		if !strings.Contains(r.stdout, want) {
			return fmt.Errorf("release stdout %q lacks %q", r.stdout, want)
		}
	}
	return nil
}

func (s *m2dState) releaseRefusesDshJob() error {
	tmp, err := os.MkdirTemp("", "harness-gate-m2d-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	inputs := append([]string{s.goldenRel, "src/harness/internal/boot/profile/testdata/egress.golden", ".github/workflows/ci.yml"}, s.releaseFiles...)
	for _, rel := range inputs {
		b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if rel == s.releaseFiles[0] {
			b = append(b, "\n  dsh:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go -C src/harness build ./cmd/dsh\n"...)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
	bin, err := m2dBuildGate()
	if err != nil {
		return err
	}
	r, err := m2dExec(tmp, bin, "release", "--root", tmp)
	if err != nil {
		return err
	}
	if r.code != 1 || !strings.Contains(r.stdout, "FAILED") {
		return fmt.Errorf("seeded dsh job: release exit %d, stdout %q; want 1 FAILED", r.code, r.stdout)
	}
	for _, want := range []string{"differs from its copy at the base commit", "names dsh", "cmd/dsh"} {
		if !strings.Contains(r.stdout, want) {
			return fmt.Errorf("seeded dsh job: release stdout %q lacks %q", r.stdout, want)
		}
	}
	return nil
}

func InitializeScenario_m2_milestone_closure_milestone_m2_declaration_and_enabling_check(ctx *godog.ScenarioContext) {
	s := &m2dState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = m2dState{}
		return c, nil
	})
	ctx.Step(`^an index listing the step 16\.8 guarded scenarios under DH-S-16 and one scenario each under DH-S-17 and DH-S-18$`, s.givenIndex)
	ctx.Step(`^fixture records with DH-S-17 "([^"]+)", DH-S-18 "([^"]+)" and the step 16\.8 guarded rows passed$`, s.givenRecords)
	ctx.Step(`^the milestone check runs$`, s.checkRuns)
	ctx.Step(`^M2 is refused naming "([^"]+)" as not closed$`, s.refusedNaming)
	ctx.Step(`^no refusal names "([^"]+)"$`, s.noRefusalNames)
	ctx.Step(`^a closure record reading "([^"]+)" is refused while "([^"]+)" is allowed$`, s.yesRefusedNoAllowed)
	ctx.Step(`^M2 is accepted and advances built-through to "([^"]+)"$`, s.accepted)
	ctx.Step(`^a closure record reading "([^"]+)" is allowed once built-through reads "([^"]+)"$`, s.yesAllowedOnceAdvanced)
	ctx.Step(`^with the guarded row "([^"]+)" not run M2 is refused naming "[^"]+"$`, s.guardedNotRun)
	ctx.Step(`^the gate's milestone command refuses M2 on the committed records, which declare "([^"]+)"$`, s.committedRefused)

	ctx.Step(`^the committed "([^"]+)", "([^"]+)" and default profile bundles$`, s.committedReleaseFiles)
	ctx.Step(`^their base-commit copies in "([^"]+)" at base "([0-9a-f]{40})"$`, s.baseCopies)
	ctx.Step(`^the release check compares them with the story's base commit and computes the default egress set$`, s.releaseCheckRuns)
	ctx.Step(`^each release file equals its base-commit copy, whose blob ID matches the golden and git history$`, s.filesEqualCopies)
	ctx.Step(`^no release file names dsh and no other workflow runs on a tag push or runs goreleaser$`, s.noDshJob)
	ctx.Step(`^the default egress set computed from the shipped bundles is empty for every profile, "([^"]+)" and "([^"]+)" included$`, s.egressEmpty)
	ctx.Step(`^the gate's release command exits 0 reporting the release files unchanged and the egress set empty$`, s.releaseCommandPasses)
	ctx.Step(`^the gate's release command refuses a copied tree in which release\.yml gains a dsh release job$`, s.releaseRefusesDshJob)
}

func TestE2E_m2_milestone_closure_milestone_m2_declaration_and_enabling_check(t *testing.T) {
	t.Cleanup(func() {
		if m2dGateBin != "" {
			os.RemoveAll(filepath.Dir(m2dGateBin))
		}
	})
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_m2_milestone_closure_milestone_m2_declaration_and_enabling_check,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"m2_milestone_closure_milestone_m2_declaration_and_enabling_check.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("milestone M2 declaration and enabling check scenarios failed")
	}
}
