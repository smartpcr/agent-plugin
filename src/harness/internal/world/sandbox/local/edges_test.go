package local_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// fakeBwrapEnv makes the re-executed test binary act as bwrap: it skips the
// bubblewrap arguments up to "--" and then, by mode, serves the stage that
// follows as-is ("serve"), serves it rewritten into an unconfined plan
// ("unconfined"), or exits before the stage starts ("dies").
const fakeBwrapEnv = "LOCALTEST_FAKE_BWRAP"

// fakeBwrap is the fake bwrap's main. It confines nothing: the stage it serves
// runs in the fake's own process, with the channel the starter handed it.
func fakeBwrap(mode string, args []string) int {
	i := slices.Index(args, "--")
	if i < 0 || len(args) < i+3 || args[i+2] != launch.Mode {
		return 2
	}
	stage := args[i+3:]
	switch mode {
	case "serve":
	case "unconfined":
		stage = unconfinedPlan(stage)
	default:
		return 3
	}
	code, _ := launch.ServeIfAsked(append([]string{launch.Mode}, stage...), os.Stderr)
	return code
}

// unconfinedPlan rewrites the stage arguments of a confined plan into an
// unconfined one: kind unconfined, backend none, no canary, roots or home.
func unconfinedPlan(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return append(out, args[i:]...)
		case "--kind":
			out, i = append(out, "--kind", string(launch.Unconfined)), i+1
		case "--backend":
			out, i = append(out, "--backend", string(sandbox.BackendNone)), i+1
		case "--canary", "--write", "--home":
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out
}

// installFakeBwrap puts a copy of the test binary named bwrap in a fresh
// directory and makes that directory the whole PATH, so the real lookup finds it
// whether or not bubblewrap is installed. It returns the fake's path.
func installFakeBwrap(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "bwrap"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.Link(exe, path); err != nil {
		copyFile(t, exe, path)
	}
	t.Setenv("PATH", dir)
	return path
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// hostFake is a passing backend that also answers as a HostBackend, with a fixed
// prefix or prefix error for every start.
type hostFake struct {
	*fake
	prefix []string
	err    error
}

func (h *hostFake) ProbeOn(ctx context.Context, _ local.Host, canary string) (local.Probe, error) {
	return h.Probe(ctx, canary)
}

func (h *hostFake) Prefix(local.Host, sandbox.Policy, string) ([]string, error) {
	return h.prefix, h.err
}

// blocking is a backend whose probe returns, without error, only once its
// context ends.
type blocking struct{}

func (blocking) Name() sandbox.Backend { return sandbox.BackendBwrap }
func (blocking) Custom() bool          { return false }
func (blocking) Probe(ctx context.Context, _ string) (local.Probe, error) {
	<-ctx.Done()
	return local.Probe{Canary: launch.CanaryRefused}, nil
}

// brokenKernel cannot report its Landlock version.
type brokenKernel struct{}

func (brokenKernel) LandlockABI() (int, error) { return 0, errors.New("no landlock syscall") }

// resolvedTempDir is a fresh directory named as it resolves, so a path below it
// has one form only.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// canaries lists the start canaries left in dir.
func canaries(t *testing.T, dir string) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dir, "start-*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

// TestConfigValidateRefusesMalformed: a negative time limit, a backend that does
// not confine, and a canary directory or harness home that is not absolute and
// clean are each refused sandbox/unavailable, and New refuses them before
// probing anything.
//
//dsh:test DH-T-SANDBOX-7
func TestConfigValidateRefusesMalformed(t *testing.T) {
	abs := t.TempDir()
	for _, tc := range []struct {
		name string
		cfg  local.Config
	}{
		{"a negative probe timeout", local.Config{ProbeTimeout: -time.Second}},
		{"a negative launch timeout", local.Config{LaunchTimeout: -time.Second}},
		{"backend none", local.Config{Backends: []sandbox.Backend{sandbox.BackendNone}}},
		{"an unknown backend", local.Config{Backends: []sandbox.Backend{"chroot"}}},
		{"a relative canary directory", local.Config{CanaryDir: "canaries"}},
		{"an unclean canary directory", local.Config{CanaryDir: abs + string(filepath.Separator) + "." + string(filepath.Separator) + "c"}},
		{"a relative harness home", local.Config{Home: "home"}},
		{"an unclean harness home", local.Config{Home: abs + string(filepath.Separator) + ".." + string(filepath.Separator) + "h"}},
	} {
		if err := tc.cfg.Validate(); herr.Kind(err) != sandbox.CodeUnavailable {
			t.Errorf("%s: Validate = %v, want %s", tc.name, err, sandbox.CodeUnavailable)
		}
		f := passing(sandbox.BackendBwrap)
		if s, err := local.New(context.Background(), tc.cfg, []local.Backend{f}); herr.Kind(err) != sandbox.CodeUnavailable || s != nil || f.calls != 0 {
			t.Errorf("%s: New = %v, %v after %d probes; want %s and no probe", tc.name, s, err, f.calls, sandbox.CodeUnavailable)
		}
	}
	if err := (local.Config{}).Validate(); err != nil {
		t.Errorf("the zero Config is refused: %v", err)
	}
}

// TestNewRefusesInvalidChainEntries: a nil chain entry, or one named none or an
// unknown backend, is not a confining backend and New refuses the chain.
//
//dsh:test DH-T-SANDBOX-7
func TestNewRefusesInvalidChainEntries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chain []local.Backend
	}{
		{"a nil entry", []local.Backend{passing(sandbox.BackendBwrap), nil}},
		{"an entry named none", []local.Backend{passing(sandbox.BackendNone)}},
		{"an entry with an unknown name", []local.Backend{passing("chroot")}},
	} {
		if s, err := local.New(context.Background(), local.Config{}, tc.chain); herr.Kind(err) != sandbox.CodeUnavailable || s != nil {
			t.Errorf("%s: New = %v, %v; want %s", tc.name, s, err, sandbox.CodeUnavailable)
		}
	}
}

// TestNewFailsWithoutATempDir: with no configured canary directory and a system
// temp directory that does not exist, New cannot create its canary directory and
// fails sandbox/unavailable.
//
//dsh:test DH-T-SANDBOX-7
func TestNewFailsWithoutATempDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, missing)
	}
	s, err := local.New(context.Background(), local.Config{}, []local.Backend{passing(sandbox.BackendBwrap)})
	if !herr.Has(err, sandbox.CodeUnavailable) || s != nil || !strings.Contains(err.Error(), "canary directory") {
		t.Fatalf("New = %v, %v; want %s creating the canary directory", s, err, sandbox.CodeUnavailable)
	}
}

// TestCloseKeepsAConfiguredCanaryDir: Close removes only a canary directory New
// created; a configured one is the operator's and stays.
//
//dsh:test DH-T-SANDBOX-7
func TestCloseKeepsAConfiguredCanaryDir(t *testing.T) {
	dir := t.TempDir()
	s, err := local.New(context.Background(), local.Config{CanaryDir: dir}, []local.Backend{passing(sandbox.BackendBwrap)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("the configured canary directory is gone after Close: %v", err)
	}
}

// TestMissingCanaryDirFailsProbeAndPrepare: a configured canary directory that
// does not exist fails every probe (no canary can be created), so nothing is
// available; one that disappears after a passing probe refuses the confined
// start sandbox/unavailable rather than running it without a canary.
//
//dsh:test DH-T-SANDBOX-7
func TestMissingCanaryDirFailsProbeAndPrepare(t *testing.T) {
	f := passing(sandbox.BackendBwrap, sandbox.Effects...)
	s, err := local.New(context.Background(), local.Config{CanaryDir: filepath.Join(t.TempDir(), "missing")}, []local.Backend{f})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if st := s.Describe(context.Background()); len(st) != 1 || st[0].Available || !strings.Contains(st[0].Reason, "create a canary") || f.calls != 0 {
		t.Fatalf("Describe = %+v after %d probes; want bwrap unavailable before its probe ran", st, f.calls)
	}

	dir := t.TempDir()
	gone, err := local.New(context.Background(), local.Config{CanaryDir: dir}, []local.Backend{passing(sandbox.BackendBwrap, sandbox.Effects...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gone.Close() })
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if l, err := gone.Prepare(context.Background(), sandboxtest.Policy(sandbox.ReadOnly), command(t)); !herr.Has(err, sandbox.CodeUnavailable) || l.Valid() {
		t.Fatalf("Prepare without a canary directory = %v, %v; want %s", l, err, sandbox.CodeUnavailable)
	}
}

// TestProbeAbandonedOrPastItsLimit: a probe whose caller had already given up is
// reported abandoned, and one that outlives ProbeTimeout is reported past its
// time limit; neither is available, even though both probes returned a refused
// canary and no error.
//
//dsh:test DH-T-SANDBOX-7
func TestProbeAbandonedOrPastItsLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := local.New(ctx, local.Config{}, []local.Backend{passing(sandbox.BackendBwrap, sandbox.Effects...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if st := s.Describe(context.Background()); len(st) != 1 || st[0].Available || !strings.Contains(st[0].Reason, "abandoned") {
		t.Fatalf("Describe after a cancelled New = %+v; want the probe abandoned", st)
	}

	slow, err := local.New(context.Background(), local.Config{ProbeTimeout: time.Millisecond}, []local.Backend{blocking{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slow.Close() })
	if st := slow.Describe(context.Background()); len(st) != 1 || st[0].Available || !strings.Contains(st[0].Reason, "time limit") {
		t.Fatalf("Describe with a probe past its limit = %+v; want it past its time limit", st)
	}
}

// TestPrepareRefusesAnEndedContext: Prepare under a context that has already
// ended prepares nothing and fails sandbox/unavailable.
//
//dsh:test DH-T-SANDBOX-7
func TestPrepareRefusesAnEndedContext(t *testing.T) {
	s := newService(t, passing(sandbox.BackendBwrap, sandbox.Effects...))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, m := range []sandbox.Mode{sandbox.ReadOnly, sandbox.DangerFullAccess} {
		if l, err := s.Prepare(ctx, sandboxtest.Policy(m), command(t)); !herr.Has(err, sandbox.CodeUnavailable) || l.Valid() {
			t.Errorf("%s: Prepare after the context ended = %v, %v; want %s", m, l, err, sandbox.CodeUnavailable)
		}
	}
}

// TestStartOfAnotherCommandIsRefused: a plan prepared for one command refuses a
// stage that reports ready for another: sandbox/launch-failed, and neither
// command runs.
//
//dsh:test DH-T-SANDBOX-3
func TestStartOfAnotherCommandIsRefused(t *testing.T) {
	s := newService(t)
	prepared, other := command(t), command(t)
	l, err := s.Prepare(context.Background(), sandboxtest.Policy(sandbox.DangerFullAccess), prepared)
	if err != nil {
		t.Fatal(err)
	}
	p, err := start(t, l, other)
	if p != nil || herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("Start of another command = %v, %v; want no handle and %s", p, err, sandbox.CodeLaunchFailed)
	}
	if exists(t, prepared[2]) || exists(t, other[2]) {
		t.Fatal("a command ran although the started command is not the prepared one")
	}
}

// TestPrepareReleasesTheCanaryWhenThePrefixFails: a host backend that cannot
// give a start's outside confiner, or gives one whose program is not an absolute
// path, refuses the start, and the start's canary is removed either way.
//
//dsh:test DH-T-SANDBOX-4
func TestPrepareReleasesTheCanaryWhenThePrefixFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []string
		err    error
		want   herr.Reason
	}{
		{"the prefix fails", nil, herr.New(sandbox.CodeUnavailable, "no confiner for this start"), sandbox.CodeUnavailable},
		{"the prefix program is relative", []string{"bwrap", "--"}, nil, sandbox.CodeLaunchFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			b := &hostFake{fake: passing(sandbox.BackendBwrap, sandbox.Effects...), prefix: tc.prefix, err: tc.err}
			s, err := local.New(context.Background(), local.Config{CanaryDir: dir}, []local.Backend{b})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			if st := s.Describe(context.Background()); len(st) != 1 || !st[0].Available {
				t.Fatalf("Describe = %+v; want the host backend available", st)
			}
			l, err := s.Prepare(context.Background(), sandboxtest.Policy(sandbox.ReadOnly), command(t))
			if !herr.Has(err, tc.want) || l.Valid() {
				t.Fatalf("Prepare = %v, %v; want %s", l, err, tc.want)
			}
			if left := canaries(t, dir); len(left) != 0 {
				t.Fatalf("the refused start left its canary: %q", left)
			}
		})
	}
}

// TestProtectRefusesWhatCannotBeResolved: a harness home or a write root that
// cannot be resolved (a component that is not a directory on POSIX, an invalid
// name on Windows) cannot be judged, so the confined start is refused
// sandbox/unavailable rather than risk making the harness writable.
//
//dsh:test DH-T-SANDBOX-13
func TestProtectRefusesWhatCannotBeResolved(t *testing.T) {
	bad := unresolvable(t)
	homeless, err := local.New(context.Background(), local.Config{Home: bad}, []local.Backend{passing(sandbox.BackendBwrap, sandbox.Effects...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = homeless.Close() })
	s := newService(t, passing(sandbox.BackendBwrap, sandbox.Effects...))
	for _, tc := range []struct {
		name string
		s    *local.Service
		root string
	}{
		{"an unresolvable harness home", homeless, t.TempDir()},
		{"an unresolvable write root", s, bad},
	} {
		_, _, err := local.PreparedPrefix(context.Background(), tc.s, sandboxtest.Policy(sandbox.WorkspaceWrite, tc.root), command(t))
		if !herr.Has(err, sandbox.CodeUnavailable) || !strings.Contains(err.Error(), "cannot resolve") {
			t.Errorf("%s: Prepare = %v; want %s, cannot resolve", tc.name, err, sandbox.CodeUnavailable)
		}
	}
}

// TestProtectJudgesRootsNotCreatedYet: a write root that does not exist yet is
// judged by its nearest existing ancestor, resolved, with the rest appended: one
// below an unrelated directory is prepared with the root as written, and one
// below the harness home's config directory is refused.
//
//dsh:test DH-T-SANDBOX-13
func TestProtectJudgesRootsNotCreatedYet(t *testing.T) {
	base := resolvedTempDir(t)
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := local.New(context.Background(), local.Config{Home: home}, []local.Backend{passing(sandbox.BackendBwrap, sandbox.Effects...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	fresh := filepath.Join(base, "work", "new", "deeper")
	plan, _, err := local.PreparedPrefix(context.Background(), s, sandboxtest.Policy(sandbox.WorkspaceWrite, fresh), command(t))
	if err != nil || !slices.Equal(plan.Write, []string{fresh}) {
		t.Fatalf("Prepare of a root not created yet = %v (write %q), %v; want it prepared with write [%s]", plan, plan.Write, err, fresh)
	}
	inHome := filepath.Join(home, "config", "new", "deeper")
	if _, _, err := local.PreparedPrefix(context.Background(), s, sandboxtest.Policy(sandbox.WorkspaceWrite, inHome), command(t)); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("Prepare of a root not created yet inside the home = %v; want %s", err, sandbox.CodeUnavailable)
	}
}

// TestLandlockWithoutAVersionOrABinary: the Landlock backend is built-in and
// cannot probe without the host; a kernel whose version cannot be read fails the
// probe and the rule set sandbox/unavailable; and with Landlock present but no
// known harness binary no probe stage can run, also sandbox/unavailable.
//
//dsh:test DH-T-SANDBOX-4
func TestLandlockWithoutAVersionOrABinary(t *testing.T) {
	broken := local.Landlock(brokenKernel{})
	if broken.Name() != sandbox.BackendLandlock || broken.Custom() {
		t.Fatalf("Landlock = %s (custom %v); want built-in landlock", broken.Name(), broken.Custom())
	}
	if _, err := broken.Probe(context.Background(), "canary"); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Errorf("Probe without the host = %v; want %s", err, sandbox.CodeUnavailable)
	}
	if _, err := broken.ProbeOn(context.Background(), local.Host{}, "canary"); !herr.Has(err, sandbox.CodeUnavailable) || !strings.Contains(err.Error(), "no landlock syscall") {
		t.Errorf("ProbeOn on a kernel without a version = %v; want %s wrapping the kernel's error", err, sandbox.CodeUnavailable)
	}
	if _, err := broken.Rules(sandboxtest.Policy(sandbox.ReadOnly), ""); !herr.Has(err, sandbox.CodeUnavailable) {
		t.Errorf("Rules on a kernel without a version = %v; want %s", err, sandbox.CodeUnavailable)
	}
	if _, err := local.Landlock(fakeKernel(3)).ProbeOn(context.Background(), local.Host{}, "canary"); herr.Kind(err) != sandbox.CodeUnavailable || !strings.Contains(err.Error(), "harness binary is unknown") {
		t.Errorf("ProbeOn with no harness binary = %v; want %s, harness binary unknown", err, sandbox.CodeUnavailable)
	}
}

// TestBwrapLookupOnPath: the bubblewrap backend looks bwrap up on PATH. With
// none there its prefix and probe fail sandbox/unavailable, "bwrap not found";
// with one there the prefix starts with its absolute path and ends in "--".
//
//dsh:test DH-T-SANDBOX-4
func TestBwrapLookupOnPath(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		b, ok := local.Bwrap().(local.HostBackend)
		if !ok {
			t.Fatal("the bubblewrap backend does not probe on the host")
		}
		if b.Name() != sandbox.BackendBwrap || b.Custom() {
			t.Fatalf("Bwrap = %s (custom %v); want built-in bwrap", b.Name(), b.Custom())
		}
		if _, err := b.Prefix(local.Host{}, sandbox.Policy{}, ""); !herr.Has(err, sandbox.CodeUnavailable) || !strings.Contains(err.Error(), "bwrap not found") {
			t.Errorf("Prefix = %v; want %s, bwrap not found", err, sandbox.CodeUnavailable)
		}
		if _, err := b.ProbeOn(context.Background(), local.Host{}, ""); !herr.Has(err, sandbox.CodeUnavailable) {
			t.Errorf("ProbeOn = %v; want %s", err, sandbox.CodeUnavailable)
		}
	})
	t.Run("present", func(t *testing.T) {
		dir := t.TempDir()
		name := "bwrap"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		bwrap := filepath.Join(dir, name)
		if err := os.WriteFile(bwrap, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		b := local.Bwrap().(local.HostBackend)
		exe := command(t)[0]
		canary := filepath.Join(t.TempDir(), "canary")
		prefix, err := b.Prefix(local.Host{Exe: exe}, sandboxtest.Policy(sandbox.ReadOnly), canary)
		if err != nil {
			t.Fatal(err)
		}
		if prefix[0] != bwrap || prefix[len(prefix)-1] != "--" || !slices.Contains(prefix, canary) || !slices.Contains(prefix, exe) {
			t.Fatalf("prefix = %q; want %s ... --, binding the canary and the binary", prefix, bwrap)
		}
	})
}

// TestBwrapProbeNeedsAnAbsoluteBinary: with bwrap found but no absolute harness
// binary, the probe's bwrap argv cannot be built, so the probe fails
// sandbox/launch-failed and no probe stage starts.
//
//dsh:test DH-T-SANDBOX-4
func TestBwrapProbeNeedsAnAbsoluteBinary(t *testing.T) {
	stage := &refusingStage{governed: sandbox.Effects}
	b := local.NewBwrap(func() (string, error) { return fixture("bwrap"), nil }, stage.run).(local.HostBackend)
	if _, err := b.ProbeOn(context.Background(), local.Host{}, fixture("canary")); herr.Kind(err) != sandbox.CodeLaunchFailed {
		t.Fatalf("ProbeOn with no harness binary = %v; want %s", err, sandbox.CodeLaunchFailed)
	}
	if len(stage.plans) != 0 {
		t.Fatalf("a probe stage started without its bwrap argv: %+v", stage.plans)
	}
}

// TestRealProbeStageUnderAFakeBwrap: the real probe stage runs as the command of
// the bwrap found on PATH, here a fake that confines nothing. Its ready decides
// the probe: a stage that tried its canary and could write it is a failed
// confinement; a stage that reports another backend than its plan, or ends
// before ready, leaves bubblewrap unavailable. No command runs in any case.
//
//dsh:test DH-T-SANDBOX-4
func TestRealProbeStageUnderAFakeBwrap(t *testing.T) {
	installFakeBwrap(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, tc := range []struct {
		mode, canary, reason string
	}{
		{"serve", string(launch.CanaryWritten), "confinement failed"},
		{"unconfined", "", "reports backend none"},
		{"dies", "", "ended before ready"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			env := []string{reexecEnv + "=1", fakeBwrapEnv + "=" + tc.mode}
			if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
				env = append(env, subprocesstest.CoverDirEnv+"="+dir)
			}
			local.SetProbeEnv(t, env...)
			s := newService(t, local.Bwrap())
			st := s.Describe(context.Background())
			if len(st) != 1 || st[0].Backend != sandbox.BackendBwrap || st[0].Available || st[0].Canary != tc.canary || !strings.Contains(st[0].Reason, tc.reason) {
				t.Fatalf("Describe = %+v; want bwrap unavailable, canary %q, reason containing %q", st, tc.canary, tc.reason)
			}
		})
	}
}
