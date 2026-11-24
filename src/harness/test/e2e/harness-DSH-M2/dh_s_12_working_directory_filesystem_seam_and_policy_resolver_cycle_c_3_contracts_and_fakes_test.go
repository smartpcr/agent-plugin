//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/archtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// c3SuiteT is the *testing.T of the entrypoint. fstest.Run needs a real
// *testing.T, and testing.T.Run may be called from another goroutine while the
// parent waits on a godog scenario subtest.
var c3SuiteT *testing.T

// c3HarnessDir is the src/harness module, relative to this package.
var c3HarnessDir = filepath.Join("..", "..", "..")

type c3Subtest struct {
	name            string
	failed, skipped bool
}

type c3World struct {
	policy   sandbox.Policy
	mem      *fstest.MemFS
	path     string
	writeErr error

	newSubject func(t *testing.T) fstest.Subject
	runOK      bool
	mu         sync.Mutex
	subtests   []c3Subtest

	fixtureDir string
	violations map[string][]string // rule name -> messages
}

func (w *c3World) givenZeroPolicy(decl string) error {
	if decl != "var p sandbox.Policy" {
		return fmt.Errorf("unexpected declaration %q", decl)
	}
	var p sandbox.Policy
	w.policy = p
	if !w.policy.IsZero() {
		return errors.New("a declared Policy is not the zero value")
	}
	return nil
}

func (w *c3World) givenMemFS() error {
	w.mem = fstest.NewMemFS()
	w.path = filepath.Join(os.TempDir(), "dsh-c3-e2e", "ws", "f.txt")
	return nil
}

func (w *c3World) whenZeroPolicyWrite() error {
	_, w.writeErr = w.mem.Write(context.Background(), fs.Ref{Path: w.path}, []byte("x"), fs.NoGuard, w.policy)
	return nil
}

func c3WantCode(what string, err error, want string) error {
	if err == nil {
		return fmt.Errorf("%s: got nil error, want code %q", what, want)
	}
	if got := herr.Kind(err); string(got) != want {
		return fmt.Errorf("%s: error %v has code %q, want %q", what, err, got, want)
	}
	return nil
}

func (w *c3World) thenWriteCode(want string) error {
	return c3WantCode("write with the zero policy", w.writeErr, want)
}

func (w *c3World) thenFenceWrite(want string) error {
	return c3WantCode("fence write", fs.Fence(w.policy, fs.OpWrite, w.path), want)
}

func (w *c3World) thenFenceRead(want string) error {
	return c3WantCode("fence read", fs.Fence(w.policy, fs.OpRead, w.path), want)
}

func (w *c3World) thenNeitherAllowedNorDenied() error {
	if w.writeErr == nil {
		return errors.New("the zero policy allowed the write")
	}
	if herr.Kind(w.writeErr) == fs.CodeSandboxDenied {
		return errors.New("the zero policy was treated as a denying policy (fs/sandbox-denied)")
	}
	in, err := w.mem.Stat(context.Background(), fs.Ref{Path: w.path})
	if err != nil {
		return fmt.Errorf("stat after refused write: %v", err)
	}
	if in.Exists {
		return errors.New("the zero policy created the file")
	}
	return nil
}

func (w *c3World) givenProviderFactory(expr string) error {
	if expr != "fstest.NewMemFS()" {
		return fmt.Errorf("unexpected provider %q", expr)
	}
	w.newSubject = func(t *testing.T) fstest.Subject {
		m := fstest.NewMemFS()
		t.Cleanup(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.subtests = append(w.subtests, c3Subtest{name: t.Name(), failed: t.Failed(), skipped: t.Skipped()})
		})
		return fstest.Subject{FS: m, Symlink: m.Symlink}
	}
	return nil
}

func (w *c3World) whenRun(fn string) error {
	if fn != "fstest.Run" {
		return fmt.Errorf("unexpected runner %q", fn)
	}
	if c3SuiteT == nil {
		return errors.New("no suite *testing.T to run the conformance suite on")
	}
	w.runOK = c3SuiteT.Run("fstest-conformance-MemFS", func(t *testing.T) {
		fstest.Run(t, w.newSubject)
	})
	return nil
}

func (w *c3World) thenAllPass() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.subtests) == 0 {
		return errors.New("fstest.Run executed no subtests")
	}
	var bad []string
	for _, s := range w.subtests {
		if s.failed {
			bad = append(bad, s.name+" FAILED")
		}
		if s.skipped {
			bad = append(bad, s.name+" SKIPPED")
		}
	}
	if len(bad) > 0 || !w.runOK {
		return fmt.Errorf("conformance run ok=%v; %d of %d subtests not passing: %v", w.runOK, len(bad), len(w.subtests), bad)
	}
	return nil
}

func (w *c3World) thenIncluded(tbl *godog.Table) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, row := range tbl.Rows {
		if i == 0 {
			continue
		}
		name := row.Cells[0].Value
		if !slices.ContainsFunc(w.subtests, func(s c3Subtest) bool {
			return strings.HasSuffix(s.name, "/"+name) && !s.failed && !s.skipped
		}) {
			return fmt.Errorf("subtest %q did not run and pass", name)
		}
	}
	return nil
}

func c3Dirs() (ws, out string) {
	base := filepath.Join(os.TempDir(), "dsh-c3-e2e-direct")
	return filepath.Join(base, "ws"), filepath.Join(base, "out")
}

func (w *c3World) thenOutsideDenied(want string) error {
	m := fstest.NewMemFS()
	ws, out := c3Dirs()
	pol := sandboxtest.Policy(sandbox.WorkspaceWrite, ws)
	target := filepath.Join(out, "escape.txt")
	_, err := m.Write(context.Background(), fs.Ref{Path: target}, []byte("x"), fs.NoGuard, pol)
	if e := c3WantCode("write outside the root", err, want); e != nil {
		return e
	}
	if in, _ := m.Stat(context.Background(), fs.Ref{Path: target}); in.Exists {
		return errors.New("a refused write changed the filesystem")
	}
	return nil
}

func (w *c3World) thenStaleRefused(want string) error {
	ctx := context.Background()
	m := fstest.NewMemFS()
	ws, _ := c3Dirs()
	full := sandboxtest.Policy(sandbox.DangerFullAccess)
	pol := sandboxtest.Policy(sandbox.WorkspaceWrite, ws)
	p := fs.Ref{Path: filepath.Join(ws, "f.txt")}
	if _, err := m.Write(ctx, p, []byte("v1"), fs.NoGuard, full); err != nil {
		return fmt.Errorf("seed v1: %v", err)
	}
	_, v1, err := m.Open(ctx, p)
	if err != nil {
		return fmt.Errorf("open v1: %v", err)
	}
	if _, err := m.Write(ctx, p, []byte("v2"), fs.NoGuard, full); err != nil {
		return fmt.Errorf("seed v2: %v", err)
	}
	_, err = m.Write(ctx, p, []byte("v3"), fs.ReplaceIfVersion{Version: v1.Version}, pol)
	if e := c3WantCode("stale write", err, want); e != nil {
		return e
	}
	rc, _, err := m.Open(ctx, p)
	if err != nil {
		return fmt.Errorf("reopen: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "v2" {
		return fmt.Errorf("content after stale write = %q, want v2", b)
	}
	return nil
}

func c3GoList(dir string, env ...string) ([]archtest.Package, error) {
	tool, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("the go toolchain is required: %v", err)
	}
	cmd := exec.Command(tool, "list", "-deps", "-json", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %v\n%s", dir, err, stderr.String())
	}
	return archtest.Parse(bytes.NewReader(out))
}

func c3RunRules(pkgs []archtest.Package) map[string][]string {
	v := map[string][]string{}
	for _, r := range archtest.Rules {
		if r.Module != archtest.HarnessModule {
			continue
		}
		if bad := r.Check(pkgs); len(bad) > 0 {
			v[r.Name] = append(v[r.Name], bad...)
		}
	}
	return v
}

func (w *c3World) givenFixture(rel, importer, imported string) error {
	w.fixtureDir = filepath.Join(c3HarnessDir, "internal", "archtest", filepath.FromSlash(rel))
	src := filepath.Join(w.fixtureDir, "internal", filepath.FromSlash(importer), filepath.Base(importer)+".go")
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read committed fixture: %v", err)
	}
	if !strings.Contains(string(b), `"`+archtest.HarnessModule+"/internal/"+imported+`"`) {
		return fmt.Errorf("fixture %s does not seed the import of %s", src, imported)
	}
	return nil
}

func (w *c3World) whenRulesOverFixture() error {
	pkgs, err := c3GoList(w.fixtureDir, "GOWORK=off")
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return errors.New("go list loaded no packages from the fixture")
	}
	w.violations = c3RunRules(pkgs)
	return nil
}

func (w *c3World) whenRulesOverRealTree() error {
	pkgs, err := c3GoList(c3HarnessDir)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(pkgs, func(p archtest.Package) bool { return p.ImportPath == archtest.WorkdirPackage }) {
		return errors.New("the real tree listing lacks internal/session/workdir")
	}
	w.violations = c3RunRules(pkgs)
	return nil
}

func (w *c3World) thenFailNaming(msg string) error {
	for _, msgs := range w.violations {
		for _, m := range msgs {
			if strings.HasSuffix(m, msg) && strings.HasPrefix(m, archtest.HarnessModule+"/") {
				return nil
			}
		}
	}
	return fmt.Errorf("no violation names %q; got %v", msg, w.violations)
}

func (w *c3World) thenReportedByRule(fragment string) error {
	want := archtest.WorkdirPackage + " imports " + archtest.FSLocalPackage
	for name, msgs := range w.violations {
		if strings.Contains(name, fragment) && slices.Contains(msgs, want) {
			return nil
		}
	}
	return fmt.Errorf("no rule containing %q reported %q; got %v", fragment, want, w.violations)
}

func (w *c3World) thenNoViolations() error {
	if len(w.violations) > 0 {
		return fmt.Errorf("real tree violates archtest rules: %v", w.violations)
	}
	return nil
}

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_contracts_and_fakes(ctx *godog.ScenarioContext) {
	w := &c3World{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = c3World{}
		return c, nil
	})

	ctx.Step(`^a zero-value sandbox policy "([^"]*)"$`, w.givenZeroPolicy)
	ctx.Step(`^a fresh in-memory filesystem fake$`, w.givenMemFS)
	ctx.Step(`^the zero policy is passed to the fake seam's write check$`, w.whenZeroPolicyWrite)
	ctx.Step(`^the write returns an herr error with code "([^"]*)"$`, w.thenWriteCode)
	ctx.Step(`^the fence check for a write with the zero policy returns code "([^"]*)"$`, w.thenFenceWrite)
	ctx.Step(`^the fence check for a read with the zero policy returns code "([^"]*)"$`, w.thenFenceRead)
	ctx.Step(`^the zero policy neither allowed nor silently denied the write$`, w.thenNeitherAllowedNorDenied)

	ctx.Step(`^a provider factory returning "([^"]*)"$`, w.givenProviderFactory)
	ctx.Step(`^"([^"]*)" executes against the provider$`, w.whenRun)
	ctx.Step(`^every conformance subtest passes and none is skipped$`, w.thenAllPass)
	ctx.Step(`^the conformance run included the subtests:$`, w.thenIncluded)
	ctx.Step(`^a write outside the workspace-write root on the fake is refused with code "([^"]*)"$`, w.thenOutsideDenied)
	ctx.Step(`^a write guarded by a stale version on the fake is refused with code "([^"]*)"$`, w.thenStaleRefused)

	ctx.Step(`^the committed archtest fixture "([^"]*)" where "([^"]*)" imports "([^"]*)"$`, w.givenFixture)
	ctx.Step(`^the archtest rules run over the fixture loaded by the go toolchain$`, w.whenRulesOverFixture)
	ctx.Step(`^the rules fail naming the import "([^"]*)"$`, w.thenFailNaming)
	ctx.Step(`^the violation is reported by the rule "([^"]*)"$`, w.thenReportedByRule)
	ctx.Step(`^the archtest rules run over the real harness tree loaded by the go toolchain$`, w.whenRulesOverRealTree)
	ctx.Step(`^the real tree has no violations$`, w.thenNoViolations)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_contracts_and_fakes(t *testing.T) {
	c3SuiteT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_contracts_and_fakes,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_contracts_and_fakes.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
