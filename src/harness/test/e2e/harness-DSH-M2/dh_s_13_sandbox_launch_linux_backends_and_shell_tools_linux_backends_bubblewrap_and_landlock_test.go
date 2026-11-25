//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// lblGoldenRoot is the harness module root, relative to this package directory.
const lblGoldenRoot = "../../.."

// lblSession is the one session the fake resolver issues policies for.
const lblSession = session.ID("lbl-e2e")

// lblRelative marks a bwrap program given as a relative path in the feature.
const lblRelative = "relative:"

// lblFixture is an absolute fixture path of this lane for token name. The
// golden is written in tokens, so the same argv matches on every lane.
func lblFixture(name string) string {
	if runtime.GOOS == "windows" {
		return `C:\fixture\` + strings.ToLower(name)
	}
	return "/fixture/" + strings.ToLower(name)
}

// lblKernel is a fake kernel reporting a fixed Landlock interface version.
type lblKernel int

func (k lblKernel) LandlockABI() (int, error) { return int(k), nil }

// lblBackend is a fake chain entry whose start-up probe fails with err or
// passes governing every effect.
type lblBackend struct {
	name sandbox.Backend
	err  error
}

func (b *lblBackend) Name() sandbox.Backend { return b.name }
func (b *lblBackend) Custom() bool          { return false }
func (b *lblBackend) Probe(context.Context, string) (local.Probe, error) {
	if b.err != nil {
		return local.Probe{}, b.err
	}
	return local.Probe{Canary: launch.CanaryRefused, Governed: slices.Clone(sandbox.Effects)}, nil
}

type lblWorld struct {
	dir string

	// paths maps a feature token (P, G, S, HOME, ...) to its path; tokens maps
	// a fixture path back to "<TOKEN>" for the golden.
	paths  map[string]string
	tokens map[string]string

	policy sandbox.Policy
	args   local.BwrapArgs
	argv   []string

	kernel lblKernel
	rules  launch.Ruleset

	chain []local.Backend
	svc   *local.Service

	err error
}

func (w *lblWorld) setup() error {
	if w.dir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "lbl-e2e-")
	if err != nil {
		return err
	}
	w.dir = dir
	w.paths = map[string]string{}
	w.tokens = map[string]string{}
	return nil
}

func (w *lblWorld) cleanup() {
	if w.svc != nil {
		_ = w.svc.Close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

func lblList(csv string) []string {
	out := []string{}
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func lblEffects(csv string) []sandbox.Effect {
	out := []sandbox.Effect{}
	for _, s := range lblList(csv) {
		out = append(out, sandbox.Effect(s))
	}
	return out
}

// fixturePath names token with a fixture path and remembers it for the golden.
func (w *lblWorld) fixturePath(token string) string {
	p := lblFixture(token)
	w.paths[token] = p
	w.tokens[p] = "<" + token + ">"
	return p
}

// realPath names token with a directory created under the scenario's directory.
func (w *lblWorld) realPath(token string) (string, error) {
	p := filepath.Join(w.dir, token)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	w.paths[token] = p
	return p, nil
}

// resolve issues a policy of mode through the fake resolver: the session's
// project root P, a grant G and the session scratch S, each with its source.
func (w *lblWorld) resolve(mode string, project, grant, scratch string) error {
	lookup := workdirtest.New()
	lookup.Set(lblSession, project, project)
	res := &sandboxtest.Resolver{Workdir: lookup, Scratch: scratch}
	if _, err := res.GrantRoot(context.Background(), lblSession, grant); err != nil {
		return fmt.Errorf("GrantRoot: %w", err)
	}
	p, err := res.Resolve(context.Background(), sandbox.Request{Session: lblSession, Mode: sandbox.Mode(mode)})
	if err != nil {
		return fmt.Errorf("Resolve(%s): %w", mode, err)
	}
	if p.Mode() != sandbox.Mode(mode) {
		return fmt.Errorf("resolved a %s policy; want %s", p.Mode(), mode)
	}
	if p.Mode() == sandbox.WorkspaceWrite {
		want := []sandbox.WriteRoot{
			{Path: project, Source: sandbox.SourceWorkingDirectory},
			{Path: grant, Source: sandbox.SourceGrant},
			{Path: scratch, Source: sandbox.SourceScratch},
		}
		if got := p.WriteRoots(); !slices.Equal(got, want) {
			return fmt.Errorf("WriteRoots = %+v; want %+v", got, want)
		}
		if p.Network() {
			return errors.New("the resolved workspace-write policy allows the network")
		}
	}
	w.policy = p
	return nil
}

func (w *lblWorld) givenFixturePolicy(mode, p, g, s string) error {
	if err := w.setup(); err != nil {
		return err
	}
	return w.resolve(mode, w.fixturePath(p), w.fixturePath(g), w.fixturePath(s))
}

func (w *lblWorld) givenRealPolicy(mode, p, g, s string) error {
	if err := w.setup(); err != nil {
		return err
	}
	var roots [3]string
	for i, tok := range []string{p, g, s} {
		path, err := w.realPath(tok)
		if err != nil {
			return err
		}
		roots[i] = path
	}
	return w.resolve(mode, roots[0], roots[1], roots[2])
}

func (w *lblWorld) givenBwrapHost(bwrap, exe, home, rundir, canary string) error {
	if err := w.setup(); err != nil {
		return err
	}
	prog := ""
	if rel, ok := strings.CutPrefix(bwrap, lblRelative); ok {
		prog = rel
	} else {
		prog = w.fixturePath(bwrap)
	}
	w.args = local.BwrapArgs{
		Bwrap:  prog,
		Host:   local.Host{Exe: w.fixturePath(exe), Home: w.fixturePath(home), RuntimeDir: w.fixturePath(rundir)},
		Canary: w.fixturePath(canary),
		Policy: w.policy,
	}
	return nil
}

func (w *lblWorld) whenBwrapArgv() error {
	w.argv, w.err = local.BwrapArgv(w.args)
	return nil
}

func (w *lblWorld) thenGolden(rel string) error {
	if w.err != nil {
		return fmt.Errorf("BwrapArgv: %w", w.err)
	}
	want, err := os.ReadFile(filepath.Join(lblGoldenRoot, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read the committed golden: %w", err)
	}
	var sb strings.Builder
	for _, a := range w.argv {
		if tok, ok := w.tokens[a]; ok {
			a = tok
		}
		sb.WriteString(a + "\n")
	}
	got := sb.String()
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); got != norm {
		return fmt.Errorf("bwrap argv differs from %s\n--- got ---\n%s--- want ---\n%s", rel, got, norm)
	}
	// Building twice gives the same argv: the function is pure.
	again, err := local.BwrapArgv(w.args)
	if err != nil || !slices.Equal(again, w.argv) {
		return fmt.Errorf("a second build gave %q, %v; want the same argv", again, err)
	}
	return nil
}

func (w *lblWorld) thenArgvShape(last string) error {
	if w.err != nil {
		return fmt.Errorf("BwrapArgv: %w", w.err)
	}
	if len(w.argv) == 0 || w.argv[0] != w.args.Bwrap || w.argv[len(w.argv)-1] != last {
		return fmt.Errorf("argv = %q; want %s ... %s", w.argv, w.args.Bwrap, last)
	}
	if !slices.Contains(w.argv, "--unshare-net") {
		return fmt.Errorf("argv = %q; want --unshare-net for a policy that denies the network", w.argv)
	}
	return nil
}

func (w *lblWorld) thenBindsWritable(csv string) error {
	var got []string
	for i := 0; i+2 < len(w.argv); i++ {
		if w.argv[i] == "--bind" {
			if w.argv[i+1] != w.argv[i+2] {
				return fmt.Errorf("--bind %s %s maps a root elsewhere", w.argv[i+1], w.argv[i+2])
			}
			got = append(got, w.argv[i+1])
		}
	}
	var want []string
	for _, tok := range lblList(csv) {
		want = append(want, w.paths[tok])
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("writable binds = %q; want %q", got, want)
	}
	return nil
}

func (w *lblWorld) thenBwrapRefused(reason string) error {
	if w.argv != nil {
		return fmt.Errorf("BwrapArgv returned argv %q with its refusal", w.argv)
	}
	if got := herr.Kind(w.err); got != herr.Reason(reason) {
		return fmt.Errorf("BwrapArgv = %v (reason %q); want %q", w.err, got, reason)
	}
	return nil
}

func (w *lblWorld) givenKernel(abi int) error {
	w.kernel = lblKernel(abi)
	return nil
}

func (w *lblWorld) whenLandlockRules(home string) error {
	h, err := w.realPath(home)
	if err != nil {
		return err
	}
	w.rules, w.err = local.Landlock(w.kernel).Rules(w.policy, h)
	return nil
}

func (w *lblWorld) rulesOK() error {
	if w.err != nil {
		return fmt.Errorf("Rules: %w", w.err)
	}
	return nil
}

func (w *lblWorld) thenWritesOnlyBeneath(csv string) error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	if w.rules.ABI != int(w.kernel) || w.rules.Version != min(int(w.kernel), launch.LandlockReference) {
		return fmt.Errorf("rule set ABI %d version %d; want ABI %d", w.rules.ABI, w.rules.Version, w.kernel)
	}
	if w.rules.Handled != launch.LandlockHandled(int(w.kernel)) {
		return fmt.Errorf("handled %#x; want %#x", w.rules.Handled, launch.LandlockHandled(int(w.kernel)))
	}
	var granted []string
	for _, r := range w.rules.Rules {
		if r.Access&(launch.AccessWriteFile|launch.AccessMakeReg) == 0 {
			continue
		}
		if !r.Dir {
			if r.Path != "/dev/null" {
				return fmt.Errorf("a file rule grants writes on %s; only /dev/null may", r.Path)
			}
			continue
		}
		granted = append(granted, r.Path)
	}
	var want []string
	for _, tok := range lblList(csv) {
		want = append(want, w.paths[tok])
	}
	if !slices.Equal(granted, want) {
		return fmt.Errorf("directory rules grant writes beneath %q; want exactly %q", granted, want)
	}
	return nil
}

// tablePath resolves a feature path: its first segment is a token when one is
// known, else the whole path lies under the scenario's directory.
func (w *lblWorld) tablePath(rel string) string {
	parts := strings.Split(rel, "/")
	if p, ok := w.paths[parts[0]]; ok {
		return filepath.Join(append([]string{p}, parts[1:]...)...)
	}
	return filepath.Join(append([]string{w.dir}, parts...)...)
}

func (w *lblWorld) tablePaths(t *godog.Table) ([]string, error) {
	if len(t.Rows) < 2 || len(t.Rows[0].Cells) != 1 || t.Rows[0].Cells[0].Value != "path" {
		return nil, errors.New("want a one-column table headed path")
	}
	var out []string
	for _, row := range t.Rows[1:] {
		out = append(out, w.tablePath(row.Cells[0].Value))
	}
	return out, nil
}

func (w *lblWorld) thenWritable(t *godog.Table) error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	paths, err := w.tablePaths(t)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if !w.rules.Writable(p) {
			return fmt.Errorf("%s is beneath a write root but not writable", p)
		}
	}
	return nil
}

func (w *lblWorld) thenNotWritable(t *godog.Table) error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	paths, err := w.tablePaths(t)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if w.rules.Writable(p) {
			return fmt.Errorf("%s is outside every write root but writable", p)
		}
	}
	return nil
}

func (w *lblWorld) thenHandledTruncateNoIoctl() error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	if w.rules.Handled&launch.AccessTruncate == 0 || w.rules.Handled&launch.AccessIoctlDev != 0 {
		return fmt.Errorf("handled %#x; want truncate handled and device ioctl not", w.rules.Handled)
	}
	return nil
}

func (w *lblWorld) thenMissing(csv string) error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	if want := lblEffects(csv); !slices.Equal(w.rules.Missing, want) && (len(want) != 0 || len(w.rules.Missing) != 0) {
		return fmt.Errorf("Missing = %v; want %v", w.rules.Missing, want)
	}
	return nil
}

func (w *lblWorld) thenGoverns(csv string) error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	if want := lblEffects(csv); !slices.Equal(w.rules.Governed, want) && (len(want) != 0 || len(w.rules.Governed) != 0) {
		return fmt.Errorf("Governed = %v; want %v", w.rules.Governed, want)
	}
	return nil
}

func (w *lblWorld) thenEnforce(state, csv string) error {
	if err := w.rulesOK(); err != nil {
		return err
	}
	c := sandbox.Enforce(w.policy, sandbox.Evidence{Backend: sandbox.BackendLandlock, Probe: w.rules.Governed, Start: w.rules.Governed})
	if string(c.State) != state || !slices.Equal(c.Missing, lblEffects(csv)) || c.Backend != sandbox.BackendLandlock {
		return fmt.Errorf("Enforce = %+v; want %s under landlock missing %v", c, state, lblEffects(csv))
	}
	return nil
}

func (w *lblWorld) thenRulesRefused(reason string) error {
	if got := herr.Kind(w.err); got != herr.Reason(reason) {
		return fmt.Errorf("Rules = %v (reason %q); want %q", w.err, got, reason)
	}
	if len(w.rules.Rules) != 0 {
		return fmt.Errorf("Rules returned %d rules with its refusal", len(w.rules.Rules))
	}
	return nil
}

func lblFailing(name, reason string) local.Backend {
	return &lblBackend{name: sandbox.Backend(name), err: herr.New(sandbox.CodeUnavailable, reason)}
}

func (w *lblWorld) givenChainFailPass(failing, reason, passing string) error {
	w.chain = []local.Backend{lblFailing(failing, reason), &lblBackend{name: sandbox.Backend(passing)}}
	return nil
}

func (w *lblWorld) givenChainPassPass(first, second string) error {
	w.chain = []local.Backend{&lblBackend{name: sandbox.Backend(first)}, &lblBackend{name: sandbox.Backend(second)}}
	return nil
}

func (w *lblWorld) givenChainFailRealLandlock(failing, reason string, abi int) error {
	w.chain = []local.Backend{lblFailing(failing, reason), local.Landlock(lblKernel(abi))}
	return nil
}

func (w *lblWorld) whenServiceProbes() error {
	if err := w.setup(); err != nil {
		return err
	}
	svc, err := local.New(context.Background(), local.Config{}, w.chain)
	if err != nil {
		return fmt.Errorf("local.New: %w", err)
	}
	w.svc = svc
	if got := svc.Probes(); got != 1 {
		return fmt.Errorf("the chain was probed %d times at start-up; want once", got)
	}
	st := svc.Describe(context.Background())
	if len(st) != len(w.chain) {
		return fmt.Errorf("Describe = %+v; want one status per chain entry", st)
	}
	for i, s := range st {
		if s.Backend != w.chain[i].Name() {
			return fmt.Errorf("status %d is %s; want chain order %s", i, s.Backend, w.chain[i].Name())
		}
	}
	return nil
}

func (w *lblWorld) status(name string) (sandbox.Status, error) {
	for _, s := range w.svc.Describe(context.Background()) {
		if s.Backend == sandbox.Backend(name) {
			return s, nil
		}
	}
	return sandbox.Status{}, fmt.Errorf("no status for backend %s", name)
}

func (w *lblWorld) thenUnavailable(name, reason string) error {
	s, err := w.status(name)
	if err != nil {
		return err
	}
	if s.Available || !strings.Contains(s.Reason, reason) {
		return fmt.Errorf("%s status = %+v; want unavailable because %q", name, s, reason)
	}
	return nil
}

func (w *lblWorld) thenAvailable(name string) error {
	s, err := w.status(name)
	if err != nil {
		return err
	}
	if !s.Available || s.Canary != string(launch.CanaryRefused) {
		return fmt.Errorf("%s status = %+v; want available with the canary refused", name, s)
	}
	return nil
}

func (w *lblWorld) prepare(mode string) (sandbox.Launch, error) {
	exe, err := os.Executable()
	if err != nil {
		return sandbox.Launch{}, err
	}
	var p sandbox.Policy
	if sandbox.Mode(mode) == sandbox.WorkspaceWrite {
		root, err := w.realPath("workspace")
		if err != nil {
			return sandbox.Launch{}, err
		}
		p = sandboxtest.Policy(sandbox.WorkspaceWrite, root)
	} else {
		p = sandboxtest.Policy(sandbox.Mode(mode))
	}
	return w.svc.Prepare(context.Background(), p, []string{exe})
}

func (w *lblWorld) thenPreparedUnder(mode, kind, backend string) error {
	l, err := w.prepare(mode)
	if err != nil {
		return fmt.Errorf("Prepare(%s): %w", mode, err)
	}
	if l.Stage() != nil {
		defer func() { _ = l.Stage().Release(context.Background()) }()
	}
	want := kind + " plan, backend " + backend
	if !l.Valid() || l.Stage() == nil || l.Reason() != want || l.Policy().Mode() != sandbox.Mode(mode) {
		return fmt.Errorf("Prepare(%s) = %v (reason %q); want a staged %q", mode, l, l.Reason(), want)
	}
	return nil
}

func (w *lblWorld) thenConfinedUnder(mode, backend string) error {
	return w.thenPreparedUnder(mode, string(launch.Confined), backend)
}

func (w *lblWorld) thenUnconfinedUnder(mode, backend string) error {
	return w.thenPreparedUnder(mode, string(launch.Unconfined), backend)
}

func (w *lblWorld) thenPrepareRefused(mode, reason string) error {
	l, err := w.prepare(mode)
	if got := herr.Kind(err); got != herr.Reason(reason) {
		return fmt.Errorf("Prepare(%s) = %v, %v (reason %q); want %q", mode, l, err, got, reason)
	}
	if l.Valid() || l.Stage() != nil {
		return fmt.Errorf("Prepare(%s) returned plan %v with its refusal", mode, l)
	}
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_linux_backends_bubblewrap_and_landlock(ctx *godog.ScenarioContext) {
	w := &lblWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = lblWorld{}
		return c, w.setup()
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, err
	})

	ctx.Step(`^a "([^"]*)" policy resolved with project root "([^"]*)", grant "([^"]*)" and scratch "([^"]*)"$`, w.givenFixturePolicy)
	ctx.Step(`^the bwrap host "([^"]*)", harness binary "([^"]*)", harness home "([^"]*)", runtime directory "([^"]*)" and canary "([^"]*)"$`, w.givenBwrapHost)
	ctx.Step(`^the bwrap backend builds the argv for the policy$`, w.whenBwrapArgv)
	ctx.Step(`^the argv, with fixture paths written as tokens, equals the committed golden "([^"]*)"$`, w.thenGolden)
	ctx.Step(`^the argv ends with "([^"]*)" and unshares the network$`, w.thenArgvShape)
	ctx.Step(`^the argv binds writable exactly "([^"]*)"$`, w.thenBindsWritable)
	ctx.Step(`^building the argv is refused "([^"]*)"$`, w.thenBwrapRefused)

	ctx.Step(`^a fake kernel reporting Landlock ABI (\d+)$`, func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		return w.givenKernel(n)
	})
	ctx.Step(`^a "([^"]*)" policy over real directories with project root "([^"]*)", grant "([^"]*)" and scratch "([^"]*)"$`, w.givenRealPolicy)
	ctx.Step(`^the Landlock backend builds the rule set for the policy with harness home "([^"]*)"$`, w.whenLandlockRules)
	ctx.Step(`^writes are granted only beneath "([^"]*)"$`, w.thenWritesOnlyBeneath)
	ctx.Step(`^these paths are writable:$`, w.thenWritable)
	ctx.Step(`^these paths are not writable:$`, w.thenNotWritable)
	ctx.Step(`^the rule set handles truncate but not device ioctl$`, w.thenHandledTruncateNoIoctl)
	ctx.Step(`^the rule set's Missing is exactly "([^"]*)"$`, w.thenMissing)
	ctx.Step(`^the rule set governs exactly "([^"]*)"$`, w.thenGoverns)
	ctx.Step(`^enforcing the policy on that evidence is "([^"]*)" missing exactly "([^"]*)"$`, w.thenEnforce)
	ctx.Step(`^building the rule set is refused "([^"]*)"$`, w.thenRulesRefused)

	ctx.Step(`^a sandbox chain of a "([^"]*)" backend whose probe fails with "([^"]*)" and a "([^"]*)" backend whose probe passes$`, w.givenChainFailPass)
	ctx.Step(`^a sandbox chain of a "([^"]*)" backend whose probe passes and a "([^"]*)" backend whose probe passes$`, w.givenChainPassPass)
	ctx.Step(`^a sandbox chain of a "([^"]*)" backend whose probe fails with "([^"]*)" and the real Landlock backend on a fake kernel reporting ABI (\d+)$`, func(name, reason, abi string) error {
		n, err := strconv.Atoi(abi)
		if err != nil {
			return err
		}
		return w.givenChainFailRealLandlock(name, reason, n)
	})
	ctx.Step(`^the service probes the chain at start-up$`, w.whenServiceProbes)
	ctx.Step(`^the service reports "([^"]*)" unavailable because "([^"]*)"$`, w.thenUnavailable)
	ctx.Step(`^the service reports "([^"]*)" available$`, w.thenAvailable)
	ctx.Step(`^Prepare for a "([^"]*)" policy gives a confined plan under backend "([^"]*)"$`, w.thenConfinedUnder)
	ctx.Step(`^Prepare for a "([^"]*)" policy gives an unconfined plan under backend "([^"]*)"$`, w.thenUnconfinedUnder)
	ctx.Step(`^Prepare for a "([^"]*)" policy is refused "([^"]*)" with no plan$`, w.thenPrepareRefused)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_linux_backends_bubblewrap_and_landlock(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_linux_backends_bubblewrap_and_landlock,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_linux_backends_bubblewrap_and_landlock.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
