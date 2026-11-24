//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// closureTrees are the four record states of implementation-plan Stage 1.1 step 5, in
// the order expected.golden records them.
var closureTrees = []string{"open_not_ready", "open_ready", "closed_ready", "closed_not_ready"}

var (
	m2BuildOnce sync.Once
	m2GateBin   string
	m2BuildErr  error
)

// m2ModuleRoot is tools/harness-gate, seen from tools/harness-gate/test/e2e/harness-DSH-M2.
func m2ModuleRoot() (string, error) {
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

func m2BuildGate() (string, error) {
	m2BuildOnce.Do(func() {
		root, err := m2ModuleRoot()
		if err != nil {
			m2BuildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "harness-gate-m2-e2e-")
		if err != nil {
			m2BuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		m2GateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", m2GateBin, ".")
		cmd.Dir = root
		cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
			return strings.HasPrefix(strings.ToUpper(kv), "GOWORK=")
		}), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			m2BuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return m2GateBin, m2BuildErr
}

// m2Record is a DH-S-16 record with the given status line and one scenario row; its
// steps, manual checks and owner defaults are complete so only the row and the status
// line decide the outcome.
func m2Record(statusLine, id, result string) string {
	return "# DH-S-16 evidence\n\n" + statusLine + `

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 16.1 | DH-D-BUNDLE-3 | 0123abc | |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| ` + id + ` | Linux (WSL) | guarded | required | wsl lane | 0123abc | ` + result + ` | run.txt |

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

type m2Run struct {
	tree, sub, stdout, stderr string
	code                      int
}

type m2State struct {
	index    *acceptance.Index
	record   *closure.Record
	blockers []string
	refusal  string
	closed   bool

	fixtureDir string
	hashes     map[string][32]byte
	runs       []m2Run
	transcript string
}

func (s *m2State) indexListing(id, stage string) error {
	src := fmt.Sprintf("built-through: DH-S-8\nstages:\n  %s:\n    - id: %s\n      authored: DH-S-7\n", stage, id)
	x, err := acceptance.ParseIndex(strings.NewReader(src))
	if err != nil {
		return fmt.Errorf("ParseIndex: %w", err)
	}
	if e, ok := x.Stage(stage); !ok || len(e.Scenarios) != 1 || e.Scenarios[0].ID != id {
		return fmt.Errorf("index fixture does not list %s under %s: %+v", id, stage, e)
	}
	s.index = x
	return nil
}

func (s *m2State) recordWithRow(statusLine, id, result string) error {
	rec, err := closure.ParseRecord("DH-S-16.md", strings.NewReader(m2Record(statusLine, id, result)))
	if err != nil {
		return fmt.Errorf("ParseRecord: %w", err)
	}
	s.record = rec
	s.blockers, s.refusal, s.closed = nil, "", false
	return nil
}

func (s *m2State) ready(stage string) error {
	if s.index == nil || s.record == nil {
		return errors.New("ready needs an index and a record")
	}
	s.blockers = closure.Ready(s.index, stage, s.record, false)
	return nil
}

func (s *m2State) close(stage string) error {
	if s.index == nil || s.record == nil {
		return errors.New("close needs an index and a record")
	}
	s.refusal, s.blockers = closure.Close(s.index, stage, s.record, false)
	s.closed = true
	return nil
}

func (s *m2State) noBlocker() error {
	if len(s.blockers) != 0 {
		return fmt.Errorf("blockers %q, want none", s.blockers)
	}
	return nil
}

func (s *m2State) exactlyBlockers(n int) error {
	if len(s.blockers) != n {
		return fmt.Errorf("%d blockers %q, want %d", len(s.blockers), s.blockers, n)
	}
	return nil
}

func (s *m2State) refused() error {
	if len(s.blockers) == 0 {
		return errors.New("the stage was accepted, want it refused")
	}
	return nil
}

func (s *m2State) blockerNames(text string) error {
	for _, b := range s.blockers {
		if strings.Contains(b, text) {
			return nil
		}
	}
	return fmt.Errorf("no blocker contains %q; blockers %q", text, s.blockers)
}

func (s *m2State) closeRefusal(want string) error {
	if !s.closed {
		return errors.New("the close predicate did not run")
	}
	if want == "none" {
		want = ""
	}
	switch want {
	case "", closure.NotYetClosed, closure.ClosedButNotReady:
	default:
		return fmt.Errorf("unknown expected refusal %q", want)
	}
	if s.refusal != want {
		return fmt.Errorf("refusal %q (blockers %q), want %q", s.refusal, s.blockers, want)
	}
	if (s.refusal == "") != (len(s.blockers) == 0) {
		return fmt.Errorf("refusal %q with blockers %q: a refusal must carry blockers and a pass none", s.refusal, s.blockers)
	}
	return nil
}

func hashTree(dir string) (map[string][32]byte, error) {
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = sha256.Sum256(b)
		return nil
	})
	return out, err
}

func (s *m2State) fixtureTrees(rel string) error {
	root, err := m2ModuleRoot()
	if err != nil {
		return err
	}
	s.fixtureDir = filepath.Join(root, filepath.FromSlash(rel))
	for _, tree := range closureTrees {
		for _, f := range []string{acceptance.IndexPath, closure.RecordPath(closure.DefaultStory, "DH-S-16")} {
			if _, err := os.Stat(filepath.Join(s.fixtureDir, tree, filepath.FromSlash(f))); err != nil {
				return fmt.Errorf("fixture tree %s: %w", tree, err)
			}
		}
	}
	s.hashes, err = hashTree(s.fixtureDir)
	return err
}

func (s *m2State) runSubcommands() error {
	bin, err := m2BuildGate()
	if err != nil {
		return err
	}
	var got strings.Builder
	for _, tree := range closureTrees {
		for _, sub := range []string{"ready", "close"} {
			// Each run gets its own copy so a run cannot touch the committed fixture.
			tmp, err := os.MkdirTemp("", "m2-closure-"+tree+"-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			if err := os.CopyFS(tmp, os.DirFS(filepath.Join(s.fixtureDir, tree))); err != nil {
				return fmt.Errorf("copy %s: %w", tree, err)
			}
			cmd := exec.Command(bin, sub, "--root", tmp, "--stage", "DH-S-16", "--built")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			code := 0
			if err := cmd.Run(); err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					return fmt.Errorf("run %s %s: %w", sub, tree, err)
				}
				code = exit.ExitCode()
			}
			s.runs = append(s.runs, m2Run{tree: tree, sub: sub, stdout: stdout.String(), stderr: stderr.String(), code: code})
			fmt.Fprintf(&got, "== %s: %s --built (exit %d)\n%s", tree, sub, code, stdout.String())
			if stderr.Len() > 0 {
				fmt.Fprintf(&got, "stderr: %s", stderr.String())
			}
		}
	}
	s.transcript = got.String()
	return nil
}

func (s *m2State) matchGolden(rel string) error {
	root, err := m2ModuleRoot()
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	norm := strings.ReplaceAll(string(want), "\r\n", "\n")
	if norm != s.transcript {
		return fmt.Errorf("output differs from %s\n--- got\n%s--- want\n%s", rel, s.transcript, norm)
	}
	return nil
}

func (s *m2State) exitCodes(table *godog.Table) error {
	if len(table.Rows) < 2 {
		return errors.New("exit-code table has no rows")
	}
	codes := map[string]int{}
	for _, r := range s.runs {
		codes[r.tree+"/"+r.sub] = r.code
	}
	for _, row := range table.Rows[1:] {
		tree := row.Cells[0].Value
		for i, sub := range []string{"ready", "close"} {
			want, err := strconv.Atoi(row.Cells[i+1].Value)
			if err != nil {
				return err
			}
			got, ok := codes[tree+"/"+sub]
			if !ok {
				return fmt.Errorf("%s %s did not run", sub, tree)
			}
			if got != want {
				return fmt.Errorf("%s on %s exited %d, want %d", sub, tree, got, want)
			}
		}
	}
	return nil
}

func (s *m2State) fixturesUnchanged() error {
	after, err := hashTree(s.fixtureDir)
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

func InitializeScenario_m2_entry_and_evidence_scaffolding_m2_evidence_records_and_index_skeleton(ctx *godog.ScenarioContext) {
	s := &m2State{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = m2State{}
		return c, nil
	})
	ctx.Step(`^an in-memory index listing "([^"]*)" under "([^"]*)"$`, s.indexListing)
	ctx.Step(`^an in-memory record reading "([^"]*)" with row "([^"]*)" result "([^"]*)"$`, s.recordWithRow)
	ctx.Step(`^the ready predicate evaluates "([^"]*)"$`, s.ready)
	ctx.Step(`^the close predicate evaluates "([^"]*)"$`, s.close)
	ctx.Step(`^it returns no blocker$`, s.noBlocker)
	ctx.Step(`^it returns exactly (\d+) blockers?$`, s.exactlyBlockers)
	ctx.Step(`^the stage is refused$`, s.refused)
	ctx.Step(`^the blocker names "([^"]*)"$`, s.blockerNames)
	ctx.Step(`^the close refusal is "([^"]*)"$`, s.closeRefusal)
	ctx.Step(`^the committed fixture trees under "([^"]*)"$`, s.fixtureTrees)
	ctx.Step(`^"harness-gate ready" and "harness-gate close" run with --root pointed at each tree$`, s.runSubcommands)
	ctx.Step(`^their exit codes and outputs match the committed "([^"]*)"$`, s.matchGolden)
	ctx.Step(`^the exit codes are:$`, s.exitCodes)
	ctx.Step(`^the committed fixture trees are unchanged$`, s.fixturesUnchanged)
}

func TestE2E_m2_entry_and_evidence_scaffolding_m2_evidence_records_and_index_skeleton(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_m2_entry_and_evidence_scaffolding_m2_evidence_records_and_index_skeleton,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"m2_entry_and_evidence_scaffolding_m2_evidence_records_and_index_skeleton.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
