package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// fixture is an absolute path of this lane for a golden token.
func fixture(name string) string {
	if runtime.GOOS == "windows" {
		return `C:\fixture\` + name
	}
	return "/fixture/" + name
}

// fakeKernel reports a fixed Landlock version.
type fakeKernel int

func (k fakeKernel) LandlockABI() (int, error) { return int(k), nil }

// TestBwrapArgvFromPolicy: a workspace-write policy with roots {P, G} and its
// scratch directory gives exactly the committed argv (TC-04a). Fixture paths are
// written as tokens so the golden is the same on every lane.
//
//dsh:test DH-T-SANDBOX-4
func TestBwrapArgvFromPolicy(t *testing.T) {
	tokens := map[string]string{}
	path := func(token, name string) string {
		p := fixture(name)
		tokens[p] = token
		return p
	}
	p := issue(t, mint.Spec{Mode: string(sandbox.WorkspaceWrite), Roots: []mint.Root{
		{Path: path("<P>", "project"), Source: string(sandbox.SourceWorkingDirectory)},
		{Path: path("<G>", "granted"), Source: string(sandbox.SourceGrant)},
		{Path: path("<S>", "scratch"), Source: string(sandbox.SourceScratch)},
	}})
	argv, err := local.BwrapArgv(local.BwrapArgs{
		Bwrap:  path("<BWRAP>", "bwrap"),
		Host:   local.Host{Exe: path("<DSH>", "dsh"), Home: path("<HOME>", "home"), RuntimeDir: path("<RUNTIME>", "run")},
		Canary: path("<CANARY>", "canary"),
		Policy: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, a := range argv {
		if tok, ok := tokens[a]; ok {
			a = tok
		}
		sb.WriteString(a + "\n")
	}
	golden.Assert(t, "bwrap_argv", sb.String())

	inside := filepath.Join(fixture("home"), "work")
	nested, err := local.BwrapArgv(local.BwrapArgs{
		Bwrap: fixture("bwrap"), Host: local.Host{Exe: fixture("dsh"), Home: fixture("home")}, Canary: fixture("canary"),
		Policy: sandboxtest.Policy(sandbox.WorkspaceWrite, inside),
	})
	if err != nil {
		t.Fatal(err)
	}
	home := slices.Index(nested, "--ro-bind-try")
	if home < 0 || !slices.Equal(nested[home+3:home+6], []string{"--bind", inside, inside}) {
		t.Errorf("a root inside the home is not bound again after it: %q", nested)
	}
	ro, err := local.BwrapArgv(local.BwrapArgs{
		Bwrap: fixture("bwrap"), Host: local.Host{Exe: fixture("dsh")}, Canary: fixture("canary"),
		Policy: sandboxtest.Policy(sandbox.ReadOnly),
	})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(ro, "/tmp"); i > 0 && ro[i-1] == "--tmpfs" {
		t.Errorf("read-only got a private writable /tmp; the host /tmp must stay read-only: %q", ro)
	}
	for _, bad := range []local.BwrapArgs{
		{Bwrap: "bwrap", Host: local.Host{Exe: fixture("dsh")}, Canary: fixture("canary"), Policy: p},
		{Bwrap: fixture("bwrap"), Host: local.Host{Exe: fixture("dsh")}, Canary: fixture("canary"), Policy: sandboxtest.Policy(sandbox.DangerFullAccess)},
	} {
		if _, err := local.BwrapArgv(bad); herr.Kind(err) != sandbox.CodeLaunchFailed {
			t.Errorf("BwrapArgv(%+v) = %v, want %s", bad, err, sandbox.CodeLaunchFailed)
		}
	}
}

// TestLandlockRulesFromPolicy: on a fake kernel reporting version 3, the Landlock
// backend grants writes only beneath {P, G, scratch}, and fs-write -- which needs
// the reference version's device-ioctl kind -- is listed missing; the start is
// partial with fs-write, network and delegated-execution missing (TC-04a, I2 Q4).
//
//dsh:test DH-T-SANDBOX-4
func TestLandlockRulesFromPolicy(t *testing.T) {
	base := t.TempDir()
	P, G, S := filepath.Join(base, "project"), filepath.Join(base, "granted"), filepath.Join(base, "scratch")
	home := filepath.Join(base, "home")
	p := issue(t, mint.Spec{Mode: string(sandbox.WorkspaceWrite), Roots: []mint.Root{
		{Path: P, Source: string(sandbox.SourceWorkingDirectory)},
		{Path: G, Source: string(sandbox.SourceGrant)},
		{Path: S, Source: string(sandbox.SourceScratch)},
	}})
	rs, err := local.NewLandlock(fakeKernel(3), nil).Rules(p, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{P, filepath.Join(G, "a", "b"), filepath.Join(S, "tmp")} {
		if !rs.Writable(path) {
			t.Errorf("%s is beneath a write root but not writable", path)
		}
	}
	for _, path := range []string{base, filepath.Join(home, "config.json"), filepath.Join(base, "projectx"), filepath.Join(base, "other", "f")} {
		if rs.Writable(path) {
			t.Errorf("%s is outside {P, G, scratch} but writable", path)
		}
	}
	var granted []string
	for _, r := range rs.Rules {
		if r.Dir && r.Access&launch.AccessWriteFile != 0 {
			granted = append(granted, r.Path)
		}
	}
	if !slices.Equal(granted, []string{P, G, S}) || rs.Handled&launch.AccessIoctlDev != 0 || rs.Handled&launch.AccessTruncate == 0 {
		t.Errorf("rules grant writes beneath %v with handled %#x; want [P G S], truncate handled, no device ioctl", granted, rs.Handled)
	}
	if !slices.Equal(rs.Missing, []sandbox.Effect{sandbox.EffectFSWrite}) {
		t.Errorf("Missing = %v, want [fs-write]: version 3 lacks the reference set's device ioctl", rs.Missing)
	}
	c := sandbox.Enforce(p, sandbox.Evidence{Backend: sandbox.BackendLandlock, Probe: rs.Governed, Start: rs.Governed})
	want := []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectNetwork, sandbox.EffectDelegatedExecution}
	if c.State != sandbox.StatePartial || !slices.Equal(c.Missing, want) {
		t.Errorf("Enforce = %+v, want Partial missing %v", c, want)
	}
	if _, err := local.NewLandlock(fakeKernel(0), nil).Rules(p, home); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Errorf("Rules on a kernel without Landlock = %v, want %s", err, sandbox.CodeUnavailable)
	}
}

// refusingStage is a probe stage that refused its canary and governed governed;
// it records the plans and prefixes it was started with.
type refusingStage struct {
	governed []sandbox.Effect
	plans    []launch.Plan
	prefixes [][]string
}

func (r *refusingStage) run(_ context.Context, _ local.Host, plan launch.Plan, _ sandbox.Policy, prefix []string) (local.Probe, error) {
	r.plans, r.prefixes = append(r.plans, plan), append(r.prefixes, prefix)
	return local.Probe{Canary: launch.CanaryRefused, Governed: r.governed}, nil
}

// TestSelectionOrder: with bwrap failing its probe and Landlock passing, the
// service selects landlock and prepares a self-confining plan carrying the write
// roots; with both failing a confined policy is refused sandbox/unavailable
// (TC-04a).
//
//dsh:test DH-T-SANDBOX-7
func TestSelectionOrder(t *testing.T) {
	missing := func() (string, error) { return "", herr.New(sandbox.CodeUnavailable, "bwrap not found") }
	stage := &refusingStage{governed: []sandbox.Effect{sandbox.EffectHarnessHome}}
	s := newService(t, local.NewBwrap(missing, stage.run), local.NewLandlock(fakeKernel(3), stage.run))
	st := s.Describe(context.Background())
	if len(st) != 2 || st[0].Available || !strings.Contains(st[0].Reason, "bwrap not found") || !st[1].Available || st[1].Backend != sandbox.BackendLandlock {
		t.Fatalf("Describe = %+v; want bwrap unavailable, landlock available", st)
	}
	if len(stage.plans) != 1 || stage.plans[0].Backend != sandbox.BackendLandlock || stage.prefixes[0] != nil {
		t.Fatalf("probe stages = %+v, prefixes %q; want one self-confining landlock stage", stage.plans, stage.prefixes)
	}
	root := t.TempDir()
	plan, prefix, err := local.PreparedPrefix(context.Background(), s, sandboxtest.Policy(sandbox.WorkspaceWrite, root), command(t))
	if err != nil || plan.Backend != sandbox.BackendLandlock || prefix != nil || !slices.Equal(plan.Write, []string{root}) {
		t.Fatalf("Prepare = %v (write %v), prefix %q, %v; want landlock, no prefix, write [%s]", plan, plan.Write, prefix, err, root)
	}

	ran := &refusingStage{}
	none := newService(t, local.NewBwrap(missing, ran.run), local.NewLandlock(fakeKernel(0), ran.run))
	if l, err := none.Prepare(context.Background(), sandboxtest.Policy(sandbox.ReadOnly), command(t)); herr.Kind(err) != sandbox.CodeUnavailable || l.Valid() {
		t.Fatalf("Prepare with both failing = %v, %v; want %s", l, err, sandbox.CodeUnavailable)
	}
	if len(ran.plans) != 0 {
		t.Fatalf("a probe stage ran although neither backend is present: %+v", ran.plans)
	}
}

// TestBwrapIsFoundOnceAndWrapsTheStage: bwrap is looked up once however often
// the chain is probed, and a start under it runs its stage under the policy's
// bwrap argv, ending in "--".
//
//dsh:test DH-T-SANDBOX-4
func TestBwrapIsFoundOnceAndWrapsTheStage(t *testing.T) {
	finds := 0
	bwrap := fixture("bwrap")
	find := func() (string, error) { finds++; return bwrap, nil }
	stage := &refusingStage{governed: sandbox.Effects}
	s := newService(t, local.NewBwrap(find, stage.run))
	s.Reprobe(context.Background())
	root := t.TempDir()
	plan, prefix, err := local.PreparedPrefix(context.Background(), s, sandboxtest.Policy(sandbox.WorkspaceWrite, root), command(t))
	if err != nil || plan.Backend != sandbox.BackendBwrap {
		t.Fatalf("Prepare = %v, %v", plan, err)
	}
	if finds != 1 {
		t.Errorf("bwrap was looked up %d times, want once", finds)
	}
	if len(prefix) == 0 || prefix[0] != bwrap || prefix[len(prefix)-1] != "--" || !slices.Contains(prefix, root) || !slices.Contains(prefix, plan.Canary) {
		t.Errorf("prefix = %q; want bwrap ... --, binding the root and the canary", prefix)
	}
	for i, p := range stage.prefixes {
		if len(p) == 0 || p[0] != bwrap || !slices.Contains(p, "--unshare-net") || stage.plans[i].Backend != sandbox.BackendBwrap {
			t.Errorf("probe %d ran under %q; want the read-only, network-denied bwrap argv", i, p)
		}
	}
	if _, err := local.NewBwrap(func() (string, error) { return "", errors.New("bwrap not found") }, stage.run).Probe(context.Background(), plan.Canary); err == nil {
		t.Error("Probe without the host passed")
	}
}

// TestPrepareKeepsTheHarnessUnwritable: Prepare refuses a policy that would let
// the command write the running harness binary or any part of the harness home,
// unless the backend keeps it read-only inside the root (DH-ADR-SANDBOX-5 (a),
// DH-T-SANDBOX-13): Landlock refuses a root holding the binary or the home, which
// bubblewrap overlays read-only; under both, the session scratch and a plugin's
// state directory are the only roots inside the home.
//
//dsh:test DH-T-SANDBOX-13
func TestPrepareKeepsTheHarnessUnwritable(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := func(parts ...string) string {
		p := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	state, attached, scratch, config := dir("plugins", "p1", "state"), dir("plugins", "attached", "s1", "p2", "state", "db"), dir("scratch", "s1"), dir("config")
	elsewhere := t.TempDir()
	stage := &refusingStage{governed: sandbox.Effects}
	service := func(b local.Backend) *local.Service {
		s, err := local.New(context.Background(), local.Config{Home: home}, []local.Backend{b})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	landlock := service(local.NewLandlock(fakeKernel(5), stage.run))
	bwrap := service(local.NewBwrap(func() (string, error) { return fixture("bwrap"), nil }, stage.run))
	for _, tc := range []struct {
		name                string
		root                string
		source              sandbox.RootSource
		landlockOK, bwrapOK bool
	}{
		{"an unrelated root", elsewhere, sandbox.SourceWorkingDirectory, true, true},
		{"a root holding the home", base, sandbox.SourceWorkingDirectory, false, true},
		{"a root holding the binary", filepath.Dir(exe), sandbox.SourceGrant, false, true},
		{"the home itself", home, sandbox.SourceGrant, false, false},
		{"a config directory of the home", config, sandbox.SourceGrant, false, false},
		{"a plugin's state directory", state, sandbox.SourceHelper, true, true},
		{"below an attached plugin's state directory", attached, sandbox.SourceHelper, true, true},
		{"the session scratch", scratch, sandbox.SourceScratch, true, true},
		{"a scratch-shaped root that is not the scratch", scratch, sandbox.SourceWorkspace, false, false},
	} {
		p := issue(t, mint.Spec{Mode: string(sandbox.WorkspaceWrite), Roots: []mint.Root{{Path: tc.root, Source: string(tc.source)}}})
		for _, b := range []struct {
			name sandbox.Backend
			s    *local.Service
			ok   bool
		}{{sandbox.BackendLandlock, landlock, tc.landlockOK}, {sandbox.BackendBwrap, bwrap, tc.bwrapOK}} {
			plan, _, err := local.PreparedPrefix(context.Background(), b.s, p, command(t))
			if b.ok && err != nil {
				t.Errorf("%s under %s: Prepare = %v; want it prepared", tc.name, b.name, err)
			}
			if !b.ok && herr.Kind(err) != sandbox.CodeUnavailable {
				t.Errorf("%s under %s: Prepare = %v, %v; want %s", tc.name, b.name, plan, err, sandbox.CodeUnavailable)
			}
		}
	}
}

// TestPrepareSeesThroughSymlinkedAncestors pins that the refusal compares the
// directories a backend will actually open (DH-ADR-SANDBOX-5 (a),
// DH-T-SANDBOX-13): a root reached through a symbolic link in an ancestor is
// judged by where it resolves, so /alias/config with /alias pointing at the
// home is the home's config directory.
//
//dsh:test DH-T-SANDBOX-13
func TestPrepareSeesThroughSymlinkedAncestors(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	for _, d := range []string{filepath.Join(home, "config"), filepath.Join(home, "plugins", "p1", "state")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	links := t.TempDir()
	link := func(name, target string) string {
		l := filepath.Join(links, name)
		if err := os.Symlink(target, l); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		return l
	}
	toHome, toBase, toExeParent, toPlugins := link("h", home), link("b", filepath.Dir(base)), link("e", filepath.Dir(filepath.Dir(exe))), link("p", filepath.Join(home, "plugins"))
	stage := &refusingStage{governed: sandbox.Effects}
	service := func(b local.Backend) *local.Service {
		s, err := local.New(context.Background(), local.Config{Home: home}, []local.Backend{b})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	landlock := service(local.NewLandlock(fakeKernel(5), stage.run))
	bwrap := service(local.NewBwrap(func() (string, error) { return fixture("bwrap"), nil }, stage.run))
	for _, tc := range []struct {
		name                string
		root                string
		landlockOK, bwrapOK bool
	}{
		{"a config directory reached through a link to the home", filepath.Join(toHome, "config"), false, false},
		{"a root reached through a link that holds the home", filepath.Join(toBase, filepath.Base(base)), false, true},
		{"a root reached through a link that holds the binary", filepath.Join(toExeParent, filepath.Base(filepath.Dir(exe))), false, true},
		{"a plugin's state directory reached through a link", filepath.Join(toPlugins, "p1", "state"), true, true},
	} {
		p := issue(t, mint.Spec{Mode: string(sandbox.WorkspaceWrite), Roots: []mint.Root{{Path: tc.root, Source: string(sandbox.SourceGrant)}}})
		for _, b := range []struct {
			name sandbox.Backend
			s    *local.Service
			ok   bool
		}{{sandbox.BackendLandlock, landlock, tc.landlockOK}, {sandbox.BackendBwrap, bwrap, tc.bwrapOK}} {
			plan, _, err := local.PreparedPrefix(context.Background(), b.s, p, command(t))
			if b.ok && err != nil {
				t.Errorf("%s under %s: Prepare = %v; want it prepared", tc.name, b.name, err)
			}
			if !b.ok && herr.Kind(err) != sandbox.CodeUnavailable {
				t.Errorf("%s under %s: Prepare = %v, %v; want %s", tc.name, b.name, plan, err, sandbox.CodeUnavailable)
			}
		}
	}
}
