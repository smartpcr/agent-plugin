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
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

var prsBg = context.Background()

// prsOuterT is the entrypoint's *testing.T, for temp dirs that outlive a step.
var prsOuterT *testing.T

const prsSession = session.ID("s-policy-e2e")

// prsRepoRoot is the repository root: this file lives in
// src/harness/test/e2e/harness-DSH-M2.
func prsRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "tools", "harness-gate", "go.mod")); err != nil {
		return "", fmt.Errorf("repository root %s: %w", root, err)
	}
	return root, nil
}

func prsPolicyDir(root string) string {
	return filepath.Join(root, "src", "harness", "internal", "world", "sandbox", "policy")
}

// prsOwner applies a component context of root, the one that dispatches or
// listens for ModeChanging.
func prsOwner(root *cordis.Context, name string) (*cordis.Context, error) {
	var octx *cordis.Context
	f, err := root.Apply(name, func(c *cordis.Context) error { octx = c; return nil })
	if err == nil {
		err = f.Wait()
	}
	return octx, err
}

type prsWorld struct {
	cleanups []func()

	root          *cordis.Context
	P, W, G, Scr  string
	wd            *workdirtest.Lookup
	resolver      *policy.Resolver
	deps          policy.Deps
	policy        sandbox.Policy
	resolveErr    error
	sessions      session.Service
	handle        session.Handle
	seen          []sandbox.ModeChange
	duringMode    []sandbox.Mode
	duringLogged  []int
	listenerErrs  []error
	fixtureTmpl   string
	rendered      map[string]string
	promptText    string
	promptHasSect bool
	repoRoot      string
	gateCodes     map[string]int
	gateOut       string
}

func (w *prsWorld) cleanup(f func()) { w.cleanups = append(w.cleanups, f) }

func (w *prsWorld) close() {
	for i := len(w.cleanups) - 1; i >= 0; i-- {
		w.cleanups[i]()
	}
	w.cleanups = nil
}

func (w *prsWorld) newRoot() {
	w.root = cordis.New()
	w.root.Loggers().ClearExporters()
	w.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	w.cleanup(w.root.Dispose)
}

// hostWorld makes real directories: W below P, a grant G and scratch.
func (w *prsWorld) hostWorld() error {
	base, err := os.MkdirTemp("", "prs-world-")
	if err != nil {
		return err
	}
	w.cleanup(func() { _ = os.RemoveAll(base) })
	w.P, w.G, w.Scr = filepath.Join(base, "proj"), filepath.Join(base, "grant"), filepath.Join(base, "scratch")
	w.W = filepath.Join(w.P, "sub")
	for _, d := range []string{w.W, w.G, w.Scr} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	w.wd = workdirtest.New()
	w.wd.Set(prsSession, w.W, w.P)
	return nil
}

func (w *prsWorld) baseDeps() (policy.Deps, error) {
	o, err := prsOwner(w.root, "world.sandbox.policy")
	if err != nil {
		return policy.Deps{}, err
	}
	scr := w.Scr
	return policy.Deps{Owner: o, Workdir: w.wd, Scratch: func(session.ID) (string, error) { return scr, nil }}, nil
}

// ---- Scenario: Resolver precedence ----

func (w *prsWorld) givenWorkspaceWriteResolver(_, _ string) error {
	w.newRoot()
	if err := w.hostWorld(); err != nil {
		return err
	}
	deps, err := w.baseDeps()
	if err != nil {
		return err
	}
	w.deps = deps
	w.resolver, err = policy.New(policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	return err
}

func (w *prsWorld) givenGrant(_ string) error {
	release, err := w.resolver.GrantRoot(prsBg, prsSession, w.G)
	if err != nil {
		return err
	}
	w.cleanup(release)
	return nil
}

func (w *prsWorld) whenResolve() error {
	w.policy, w.resolveErr = w.resolver.Resolve(prsBg, sandbox.Request{Session: prsSession})
	return nil
}

func (w *prsWorld) thenRootsExactlyPGScratch() error {
	if w.resolveErr != nil {
		return w.resolveErr
	}
	want := []sandbox.WriteRoot{
		{Path: w.P, Source: sandbox.SourceWorkspace},
		{Path: w.G, Source: sandbox.SourceGrant},
		{Path: w.Scr, Source: sandbox.SourceScratch},
	}
	if w.policy.Mode() != sandbox.WorkspaceWrite {
		return fmt.Errorf("mode = %s, want workspace-write", w.policy.Mode())
	}
	if got := w.policy.WriteRoots(); !slices.Equal(got, want) {
		return fmt.Errorf("write roots = %v, want exactly %v (W %s must not be added)", got, want, w.W)
	}
	return nil
}

func (w *prsWorld) thenExplicitReadOnlyNoRoots() error {
	p, err := w.resolver.Resolve(prsBg, sandbox.Request{Session: prsSession, Mode: sandbox.ReadOnly})
	if err != nil {
		return err
	}
	if p.Mode() != sandbox.ReadOnly || len(p.WriteRoots()) != 0 {
		return fmt.Errorf("explicit read-only = %s %v, want read-only with no roots", p.Mode(), p.WriteRoots())
	}
	return nil
}

func (w *prsWorld) thenDefaultReadOnlyNoRoots() error {
	ro, err := policy.New(policy.Config{}, w.deps)
	if err != nil {
		return err
	}
	if _, err := ro.GrantRoot(prsBg, prsSession, w.G); err != nil {
		return err
	}
	p, err := ro.Resolve(prsBg, sandbox.Request{Session: prsSession})
	if err != nil {
		return err
	}
	if p.Mode() != sandbox.ReadOnly || len(p.WriteRoots()) != 0 {
		return fmt.Errorf("default read-only = %s %v, want read-only with no roots even with a grant", p.Mode(), p.WriteRoots())
	}
	return nil
}

// ---- Scenario: SetMode is logged and announced ----

func (w *prsWorld) modeEvents() ([]policy.ModeSet, error) {
	evs, err := w.handle.Read(prsBg, 0, 0)
	if err != nil {
		return nil, err
	}
	var out []policy.ModeSet
	for _, ev := range evs {
		if ev.Type() != sandbox.ModeEventType {
			continue
		}
		m, err := policy.ModeType.As(ev)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (w *prsWorld) givenLiveSession() error {
	w.newRoot()
	if err := w.hostWorld(); err != nil {
		return err
	}
	w.sessions = session.NewService(w.root, memstore.New(), session.Config{})
	h, err := w.sessions.Create(prsBg, nil, session.CreateOptions{Header: session.Header{ID: string(prsSession)}})
	if err != nil {
		return err
	}
	w.handle = h
	w.cleanup(func() { _ = h.Close(prsBg) })
	deps, err := w.baseDeps()
	if err != nil {
		return err
	}
	deps.Sessions = w.sessions
	w.deps = deps
	w.resolver, err = policy.New(policy.Config{}, deps)
	if err != nil {
		return err
	}
	m, err := w.resolver.Mode(prsBg, prsSession)
	if err != nil {
		return err
	}
	if m != sandbox.ReadOnly {
		return fmt.Errorf("a fresh session resolves to %s, want read-only", m)
	}
	return nil
}

func (w *prsWorld) givenListener() error {
	l, err := prsOwner(w.root, "e2e.listener")
	if err != nil {
		return err
	}
	_, err = sandbox.ModeChanging.On(l, func(_ *cordis.Context, c sandbox.ModeChange) error {
		w.seen = append(w.seen, c)
		p, err := w.resolver.Resolve(prsBg, sandbox.Request{Session: prsSession})
		if err != nil {
			w.listenerErrs = append(w.listenerErrs, err)
		}
		w.duringMode = append(w.duringMode, p.Mode())
		evs, err := w.modeEvents()
		if err != nil {
			w.listenerErrs = append(w.listenerErrs, err)
		}
		w.duringLogged = append(w.duringLogged, len(evs))
		return nil
	})
	return err
}

func (w *prsWorld) whenSetMode(mode string) error {
	return w.resolver.SetMode(prsBg, prsSession, sandbox.Mode(mode))
}

func (w *prsWorld) thenOneDispatch(from, to string) error {
	want := []sandbox.ModeChange{{Session: prsSession, From: sandbox.Mode(from), To: sandbox.Mode(to)}}
	if !slices.Equal(w.seen, want) {
		return fmt.Errorf("ModeChanging dispatches = %+v, want %+v", w.seen, want)
	}
	return nil
}

func (w *prsWorld) thenOldModeDuringDispatch() error {
	if len(w.listenerErrs) > 0 {
		return errors.Join(w.listenerErrs...)
	}
	if !slices.Equal(w.duringMode, []sandbox.Mode{sandbox.ReadOnly}) {
		return fmt.Errorf("mode in force during dispatch = %v, want [read-only]", w.duringMode)
	}
	if !slices.Equal(w.duringLogged, []int{0}) {
		return fmt.Errorf("sandbox/mode events logged during dispatch = %v, want [0]", w.duringLogged)
	}
	return nil
}

func (w *prsWorld) thenOneModeEvent(mode string) error {
	evs, err := w.modeEvents()
	if err != nil {
		return err
	}
	if len(evs) != 1 {
		return fmt.Errorf("sandbox/mode events = %d (%+v), want 1", len(evs), evs)
	}
	e := evs[0]
	if e.Mode != sandbox.Mode(mode) || e.From != sandbox.ReadOnly || e.By.Principal != policy.PrincipalHarness || !e.Network.Valid() {
		return fmt.Errorf("sandbox/mode = %+v, want mode %s from read-only by %s", e, mode, policy.PrincipalHarness)
	}
	return nil
}

func (w *prsWorld) thenNextResolveUses(mode string) error {
	p, err := w.resolver.Resolve(prsBg, sandbox.Request{Session: prsSession})
	if err != nil {
		return err
	}
	if p.Mode() != sandbox.Mode(mode) || len(p.WriteRoots()) != 0 {
		return fmt.Errorf("next Resolve = %s %v, want %s with no roots", p.Mode(), p.WriteRoots(), mode)
	}
	fresh, err := policy.New(policy.Config{}, w.deps)
	if err != nil {
		return err
	}
	if m, err := fresh.Mode(prsBg, prsSession); err != nil || m != sandbox.Mode(mode) {
		return fmt.Errorf("a fresh resolver folds the log to %s, %v; want %s", m, err, mode)
	}
	return nil
}

// ---- Scenario: Renderer fills the fixture template per platform ----

// prsPlatformRoots are the fixed, platform-style roots of the render goldens
// (render_test.go), so each golden is the same on every host.
var prsPlatformRoots = map[string][3]string{
	"linux":   {"/home/dev/proj", "/srv/shared/out", "/home/dev/.dsh/scratch/s-policy"},
	"darwin":  {"/Users/dev/proj", "/Volumes/shared/out", "/Users/dev/Library/Caches/dsh/scratch/s-policy"},
	"windows": {`C:\Users\dev\proj`, `D:\shared\out`, `C:\Users\dev\AppData\Local\dsh\scratch\s-policy`},
}

const prsFullAccessToken = "<<policy.FullAccessLabel>>"

func (w *prsWorld) givenFixtureTemplate(rel string) error {
	root, err := prsRepoRoot()
	if err != nil {
		return err
	}
	w.repoRoot = root
	b, err := os.ReadFile(filepath.Join(prsPolicyDir(root), filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	w.fixtureTmpl = strings.ReplaceAll(string(b), "\r\n", "\n")
	if strings.Contains(w.fixtureTmpl, prsFullAccessToken) {
		return fmt.Errorf("the fixture template holds %s itself", prsFullAccessToken)
	}
	return nil
}

// whenRenderEachPlatform builds a resolver per platform (Config.Platform), lets
// it resolve each mode for a host session and renders what it resolved; the
// workspace-write roots are the golden's fixed platform paths, since another
// platform's paths cannot be stat-checked as grants on this host.
func (w *prsWorld) whenRenderEachPlatform(_, _, _ string) error {
	w.newRoot()
	if err := w.hostWorld(); err != nil {
		return err
	}
	w.rendered = map[string]string{}
	for _, platform := range policy.Platforms {
		deps, err := w.baseDeps()
		if err != nil {
			return err
		}
		allowed, err := policy.New(policy.Config{Platform: platform}, deps)
		if err != nil {
			return fmt.Errorf("resolver for %s: %w", platform, err)
		}
		// The golden's read-only row is a deployment with the network denied.
		denied, err := policy.New(policy.Config{Platform: platform, Network: policy.NetworkDenied}, deps)
		if err != nil {
			return fmt.Errorf("network-denied resolver for %s: %w", platform, err)
		}
		roots := prsPlatformRoots[platform]
		var b strings.Builder
		for _, m := range []sandbox.Mode{sandbox.ReadOnly, sandbox.WorkspaceWrite, sandbox.DangerFullAccess} {
			r := allowed
			if m == sandbox.ReadOnly {
				r = denied
			}
			p, err := r.Resolve(prsBg, sandbox.Request{Session: prsSession, Mode: m})
			if err != nil {
				return fmt.Errorf("%s: resolve %s: %w", platform, m, err)
			}
			res := policy.Describe(p)
			if res.Mode != m {
				return fmt.Errorf("%s: resolved %s, want %s", platform, res.Mode, m)
			}
			if m == sandbox.WorkspaceWrite {
				res.WriteRoots = []sandbox.WriteRoot{
					{Path: roots[0], Source: sandbox.SourceWorkspace},
					{Path: roots[1], Source: sandbox.SourceGrant},
					{Path: roots[2], Source: sandbox.SourceScratch},
				}
			}
			out, err := policy.Render(platform, res, w.fixtureTmpl)
			if err != nil {
				return fmt.Errorf("%s: render %s: %w", platform, m, err)
			}
			wantLabel := 0
			if m == sandbox.DangerFullAccess {
				wantLabel = 1
			}
			if n := strings.Count(out, policy.FullAccessLabel); n != wantLabel {
				return fmt.Errorf("%s: mode %s carries FullAccessLabel %d times", platform, m, n)
			}
			fmt.Fprintf(&b, "== mode %s\n%s", m, out)
		}
		w.rendered[platform] = strings.ReplaceAll(b.String(), policy.FullAccessLabel, prsFullAccessToken)
	}
	return nil
}

func (w *prsWorld) thenRenderGoldens(_ string) error {
	for _, platform := range policy.Platforms {
		file := filepath.Join(prsPolicyDir(w.repoRoot), "testdata", "render_fixture_"+platform+".golden")
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		want := strings.ReplaceAll(string(b), "\r\n", "\n")
		if got := w.rendered[platform]; got != want {
			return fmt.Errorf("%s render differs from %s\n--- got\n%s--- want\n%s", platform, file, got, want)
		}
	}
	return nil
}

// ---- Scenario: No text means no section ----

type prsView struct{ target tools.Target }

func (v prsView) Target() tools.Target                   { return v.target }
func (prsView) Tools() []*tools.Declaration              { return nil }
func (prsView) Lookup(string) (*tools.Declaration, bool) { return nil, false }
func (prsView) Release()                                 {}

func (w *prsWorld) givenNoTextDir(_ string) error {
	root, err := prsRepoRoot()
	if err != nil {
		return err
	}
	w.repoRoot = root
	if _, err := os.Stat(filepath.Join(prsPolicyDir(root), "text")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("world/sandbox/policy/text/ must not exist before Stage 7.6 (M2-C-6): %v", err)
	}
	return nil
}

func (w *prsWorld) whenPromptComposed() error {
	w.newRoot()
	if err := w.hostWorld(); err != nil {
		return err
	}
	deps, err := w.baseDeps()
	if err != nil {
		return err
	}
	r, err := policy.New(policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	if err != nil {
		return err
	}
	if p, err := r.Resolve(prsBg, sandbox.Request{Session: prsSession}); err != nil || p.TextVersion() != "" {
		return fmt.Errorf("resolved text version = %q, %v; want none", p.TextVersion(), err)
	}
	ps, err := prompt.New(w.root, scope.NewTable(w.root), prompt.Config{})
	if err != nil {
		return err
	}
	if _, err := r.RegisterPromptSection(deps.Owner, ps); err != nil {
		return err
	}
	a, err := ps.Assemble(prsBg, prompt.Input{View: prsView{tools.Target{Agent: agent.Ref{Session: prsSession, Incarnation: 1}}}})
	if err != nil {
		return err
	}
	for _, m := range a.Manifest() {
		if m.Name == prompt.SectionSandboxPolicy {
			w.promptHasSect = true
		}
	}
	w.promptText, err = ps.Render(a)
	return err
}

func (w *prsWorld) thenTextIsErrNoText() error {
	for _, platform := range policy.Platforms {
		if _, err := policy.Text(platform); !errors.Is(err, policy.ErrNoText) {
			return fmt.Errorf("Text(%s) = %v, want ErrNoText", platform, err)
		}
	}
	return nil
}

func (w *prsWorld) thenNoPolicySection() error {
	if w.promptHasSect {
		return errors.New("the prompt manifest has a sandbox policy section with no shipped text")
	}
	if strings.Contains(w.promptText, "Sandbox policy") || strings.Contains(w.promptText, policy.EscalationHint) {
		return fmt.Errorf("the prompt states a sandbox policy:\n%s", w.promptText)
	}
	return nil
}

// ---- Scenario: Policy-text gate refuses unproven text ----

// prsPolicyRunCommit is the commit the fixtures' run records name: the text
// alone, committed with fixed author, committer, dates and message
// (tools/harness-gate/policytext_test.go).
const prsPolicyRunCommit = "41101e7b57521c233f03210ab960fd3ceb665729"

const prsPolicyTextDir = "src/harness/internal/world/sandbox/policy/text"

var prsGitEnv = []string{
	"GIT_AUTHOR_NAME=dsh-fixture", "GIT_AUTHOR_EMAIL=fixture@dsh.invalid", "GIT_AUTHOR_DATE=2026-12-01T00:00:00Z",
	"GIT_COMMITTER_NAME=dsh-fixture", "GIT_COMMITTER_EMAIL=fixture@dsh.invalid", "GIT_COMMITTER_DATE=2026-12-01T00:00:00Z",
	"GIT_CONFIG_NOSYSTEM=1",
}

var prsTrees = []string{"no_text", "text_without_sandbox7", "text_with_matching_pass", "text_edited_after_pass"}

func prsGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.autocrlf=false", "-c", "commit.gpgsign=false", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), prsGitEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// prsFixtureRepo copies a fixture tree to a temporary git checkout whose first
// commit holds only the text (the run's commit) and whose second adds the rest.
func prsFixtureRepo(src string) (string, error) {
	dst := filepath.Join(prsOuterT.TempDir(), filepath.Base(src))
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		return "", err
	}
	if _, err := prsGit(dst, "init", "-q", "--object-format=sha1"); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(prsPolicyTextDir))); err == nil {
		if _, err := prsGit(dst, "add", "--", prsPolicyTextDir); err != nil {
			return "", err
		}
		if _, err := prsGit(dst, "commit", "-q", "--no-verify", "-m", "DH-E-SANDBOX-7 run commit (fixture)"); err != nil {
			return "", err
		}
	}
	if _, err := prsGit(dst, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := prsGit(dst, "commit", "-q", "--no-verify", "--allow-empty", "-m", "the rest of the fixture tree"); err != nil {
		return "", err
	}
	return dst, nil
}

func (w *prsWorld) givenFixtureTrees(rel string) error {
	root, err := prsRepoRoot()
	if err != nil {
		return err
	}
	w.repoRoot = root
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("the policy-text gate needs git: %w", err)
	}
	for _, t := range prsTrees {
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel), t)); err != nil || !fi.IsDir() {
			return fmt.Errorf("fixture tree %s is missing: %v", t, err)
		}
	}
	return nil
}

func (w *prsWorld) whenGateRunsOnEach(_ string) error {
	bin := filepath.Join(prsOuterT.TempDir(), "harness-gate")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join(w.repoRoot, "tools", "harness-gate")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building harness-gate: %v\n%s", err, out)
	}
	w.gateCodes = map[string]int{}
	var got strings.Builder
	for _, t := range prsTrees {
		repo, err := prsFixtureRepo(filepath.Join(w.repoRoot, "tools", "harness-gate", "testdata", "policytext", t))
		if err != nil {
			return err
		}
		if t == "text_with_matching_pass" {
			run, err := prsGit(repo, "rev-parse", "HEAD~1")
			if err != nil {
				return err
			}
			if run != prsPolicyRunCommit {
				return fmt.Errorf("the fixture's run commit is %s, want %s (the run record names it)", run, prsPolicyRunCommit)
			}
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin, "policy-text", "--root", repo)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				return fmt.Errorf("harness-gate policy-text on %s: %w", t, err)
			}
			code = ee.ExitCode()
		}
		w.gateCodes[t] = code
		fmt.Fprintf(&got, "== %s (exit %d)\n%s", t, code, stdout.String())
		if stderr.Len() > 0 {
			fmt.Fprintf(&got, "stderr: %s", stderr.String())
		}
	}
	w.gateOut = got.String()
	return nil
}

func (w *prsWorld) thenOnlyExitZero(a, b string) error {
	for _, t := range prsTrees {
		zero := t == a || t == b
		if (w.gateCodes[t] == 0) != zero {
			return fmt.Errorf("%s exited %d; only %s and %s may exit 0\n%s", t, w.gateCodes[t], a, b, w.gateOut)
		}
	}
	return nil
}

func (w *prsWorld) thenNonZeroNaming(a, b, rule string) error {
	sections := strings.Split(w.gateOut, "== ")
	for _, t := range []string{a, b} {
		if w.gateCodes[t] == 0 {
			return fmt.Errorf("%s exited 0, want non-zero", t)
		}
		var sec string
		for _, s := range sections {
			if strings.HasPrefix(s, t+" ") {
				sec = s
			}
		}
		if !strings.Contains(sec, "REFUSED "+rule) {
			return fmt.Errorf("%s output does not name %s:\n%s", t, rule, sec)
		}
	}
	return nil
}

func (w *prsWorld) thenGateGolden(rel string) error {
	file := filepath.Join(w.repoRoot, "tools", "harness-gate", filepath.FromSlash(rel))
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if want := strings.ReplaceAll(string(b), "\r\n", "\n"); want != w.gateOut {
		return fmt.Errorf("policy-text output differs from %s\n--- got\n%s--- want\n%s", file, w.gateOut, want)
	}
	return nil
}

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_policy_resolver(ctx *godog.ScenarioContext) {
	w := &prsWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.close()
		*w = prsWorld{}
		return c, err
	})

	ctx.Step(`^a workspace-write resolver over working directory "([^"]*)" below workspace root "([^"]*)" with scratch$`, w.givenWorkspaceWriteResolver)
	ctx.Step(`^a directory grant "([^"]*)" on the session$`, w.givenGrant)
	ctx.Step(`^Resolve runs for the session$`, w.whenResolve)
	ctx.Step(`^the write roots are exactly the workspace root, the grant and scratch, in that order$`, w.thenRootsExactlyPGScratch)
	ctx.Step(`^an explicit read-only Resolve yields no write roots$`, w.thenExplicitReadOnlyNoRoots)
	ctx.Step(`^a resolver whose default mode is read-only yields no write roots$`, w.thenDefaultReadOnlyNoRoots)

	ctx.Step(`^an in-memory live session under a read-only resolver$`, w.givenLiveSession)
	ctx.Step(`^a sandbox\.ModeChanging listener that records each dispatch$`, w.givenListener)
	ctx.Step(`^SetMode changes the session to "([^"]*)"$`, w.whenSetMode)
	ctx.Step(`^exactly one sandbox\.ModeChanging dispatch from "([^"]*)" to "([^"]*)" was observed$`, w.thenOneDispatch)
	ctx.Step(`^during the dispatch the old mode was still in force and no sandbox/mode event was logged$`, w.thenOldModeDuringDispatch)
	ctx.Step(`^exactly one sandbox/mode event to "([^"]*)" is appended to the session log$`, w.thenOneModeEvent)
	ctx.Step(`^the next Resolve uses "([^"]*)"$`, w.thenNextResolveUses)

	ctx.Step(`^the test-only fixture template "([^"]*)"$`, w.givenFixtureTemplate)
	ctx.Step(`^policy\.Render runs with the resolver of each platform "([^"]*)", "([^"]*)" and "([^"]*)"$`, w.whenRenderEachPlatform)
	ctx.Step(`^each output is byte-identical to its "([^"]*)"$`, w.thenRenderGoldens)

	ctx.Step(`^a build with no "([^"]*)" directory$`, w.givenNoTextDir)
	ctx.Step(`^the prompt builder composes a session prompt with the resolver's section registered$`, w.whenPromptComposed)
	ctx.Step(`^policy\.Text returns policy\.ErrNoText on every platform$`, w.thenTextIsErrNoText)
	ctx.Step(`^the prompt has no sandbox policy section$`, w.thenNoPolicySection)

	ctx.Step(`^the committed fixture trees under "([^"]*)"$`, w.givenFixtureTrees)
	ctx.Step(`^"harness-gate policy-text --root ([^"]*)" runs on each tree$`, w.whenGateRunsOnEach)
	ctx.Step(`^only "([^"]*)" and "([^"]*)" exit 0$`, w.thenOnlyExitZero)
	ctx.Step(`^"([^"]*)" and "([^"]*)" exit non-zero naming "([^"]*)"$`, w.thenNonZeroNaming)
	ctx.Step(`^the combined output equals "([^"]*)"$`, w.thenGateGolden)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_policy_resolver(t *testing.T) {
	prsOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_policy_resolver,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_policy_resolver.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("policy resolver e2e scenarios failed")
	}
}
