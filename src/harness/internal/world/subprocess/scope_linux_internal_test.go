//go:build linux

package subprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// fakeHost replaces this process's view of its own control group for one test:
// own is what ownCgroupDir returns (unwritable at first) until a fake manager
// moves the process, and
// the kernel placement of a child in a faked group is skipped (the directory is
// no control group). The real choice is made and restored around it.
type fakeHost struct {
	own  atomic.Pointer[string]
	root string
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	probeCgroup()
	real := TreeOwner()
	base, why, scope := cgroupBase, cgroupWhy, cgroupScope
	savedOwn, savedStart := ownCgroupDir, startIntoGroup
	t.Cleanup(func() {
		cgroupBase, cgroupWhy, cgroupScope = base, why, scope
		ownCgroupDir, startIntoGroup = savedOwn, savedStart
		cgroupOnce = sync.Once{}
		cgroupOnce.Do(func() {})
		chooseOnce = sync.Once{}
		chooseOnce.Do(func() {})
		chosen = real
		announceOnce = sync.Once{}
	})
	cgroupBase, cgroupWhy, cgroupScope = "", "", ""
	cgroupOnce, chooseOnce, announceOnce = sync.Once{}, sync.Once{}, sync.Once{}
	h := &fakeHost{root: t.TempDir()}
	h.own.Store(ptr(h.unwritable()))
	ownCgroupDir = func() (string, error) { return *h.own.Load(), nil }
	startIntoGroup = func(cmd *exec.Cmd) error {
		a := *cmd.SysProcAttr
		a.UseCgroupFD, a.CgroupFD = false, 0
		cmd.SysProcAttr = &a
		return startLocked(cmd)
	}
	return h
}

// unwritable is an own control group no new group can be made in.
func (h *fakeHost) unwritable() string { return filepath.Join(h.root, "session.scope", "missing") }

// fakeManager is a user service manager: it grants a scope by making its
// directory and moving the host there, refuses with err, or never answers.
type fakeManager struct {
	host  *fakeHost
	calls atomic.Int32
	err   error
	hang  bool
	// during runs while the request is under way.
	during func(ctx context.Context, unit string)
}

func (m *fakeManager) DelegateScope(ctx context.Context, unit string, pid int) error {
	m.calls.Add(1)
	if pid != os.Getpid() {
		return errors.New("the scope must hold the host itself")
	}
	if m.during != nil {
		m.during(ctx, unit)
	}
	switch {
	case m.hang:
		<-ctx.Done()
		return ctx.Err()
	case m.err != nil:
		return m.err
	}
	dir := filepath.Join(m.host.root, "user@1000.service", unit)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	m.host.own.Store(&dir)
	return nil
}

var fakeLaunch = sandbox.Trusted("subprocess test: the test binary re-executed")

// startStubborn starts the test binary as a stubborn child through Start and
// registers its stop.
func startStubborn(t *testing.T) *Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Start(context.Background(), Spec{Argv: []string{exe}, Env: Env{"SUBPROCESSTEST_ROLE=stubborn"}, Launch: fakeLaunch})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.StopTree(ctx, 0); err != nil {
			t.Errorf("StopTree: %v", err)
		}
	})
	return p
}

// Scenario "One scope request, then exact groups" (19.4.1), granted half: with
// an unwritable own group, the provider start asks the fake user manager once;
// three commands then start, each owned by a control group inside the granted
// scope, and no further request is made. The request's own launch, which names
// the unit, is not held up by the choice it makes, and a start that comes during
// the request waits for it and gets the granted rung.
//
//dsh:test DH-T-EXEC-4
func TestScopeRequestedOnceThenGroupsInsideIt(t *testing.T) {
	h := newFakeHost(t)
	h.own.Store(ptr(h.unwritable()))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	during := make(chan *Process, 1)
	m := &fakeManager{host: h, during: func(ctx context.Context, unit string) {
		// What the real manager does: run a program that names the unit.
		res, err := Run(ctx, Spec{Argv: []string{exe, unit}, Env: Env{"SUBPROCESSTEST_ROLE=exit"}, Launch: fakeLaunch}, RunOptions{})
		if err != nil || res.Owner.Kind != ProcessGroup {
			t.Errorf("the request's own launch: %v, owner %s; want it run on a process group", err, res.Owner)
		}
		// A start that comes now waits for the choice; the request goes on once
		// it is waiting.
		waiting := make(chan struct{}, 1)
		waitingForScope = func() { waiting <- struct{}{} }
		t.Cleanup(func() { waitingForScope = nil })
		done := make(chan struct{})
		go func() {
			defer close(done)
			during <- startStubborn(t)
		}()
		t.Cleanup(func() { <-done })
		select {
		case <-waiting:
		case <-ctx.Done():
			t.Errorf("no start waited for the scope request: %v", ctx.Err())
		}
	}}
	var announced atomic.Int32
	reg, _, err := OpenRegistry(context.Background(), ProviderOptions{RunDir: t.TempDir(), UserManager: m, Announce: func(Owner) { announced.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	scope := *h.own.Load()
	if got := DelegatedScope(); got == "" || got != filepath.Base(scope) {
		t.Fatalf("DelegatedScope = %q; want the granted unit %s", got, filepath.Base(scope))
	}
	if o := TreeOwner(); o.Kind != ControlGroup || !o.Exact || o.Fallback != "" {
		t.Fatalf("TreeOwner = %s; want an exact control group", o)
	}
	procs := []*Process{<-during}
	for range 3 {
		procs = append(procs, startStubborn(t))
	}
	for _, p := range procs {
		reg.Add(p)
		dir := ownerCgroup(p.owner)
		if p.Owner().Kind != ControlGroup || filepath.Dir(dir) != scope {
			t.Errorf("pid %d: owner %s in %q; want a control group inside %s", p.Pid(), p.Owner(), dir, scope)
		}
	}
	if err := reg.OwnerRecordErr(); err != nil {
		t.Fatal(err)
	}
	if n := m.calls.Load(); n != 1 {
		t.Fatalf("scope requests = %d; want exactly one", n)
	}
	if _, _, err := OpenRegistry(context.Background(), ProviderOptions{UserManager: m}); err != nil || m.calls.Load() != 1 {
		t.Fatalf("a second provider start: %v, %d requests; want no new request", err, m.calls.Load())
	}
	if n := announced.Load(); n != 0 {
		t.Fatalf("announcements = %d; an exact owner is not a fallback", n)
	}
}

// The other half: a manager that does not answer within the deadline, or
// refuses, leaves the process-group rung; it is announced once, reported with
// its cause on every handle, and never asked again.
//
//dsh:test DH-T-EXEC-4
func TestScopeTimeoutOrRefusalLeavesTheGroupRung(t *testing.T) {
	for _, c := range []struct {
		name string
		m    func() *fakeManager
		want string
	}{
		{"timeout", func() *fakeManager { return &fakeManager{hang: true} }, "did not grant a delegated scope within 100ms"},
		{"refusal", func() *fakeManager { return &fakeManager{err: errors.New("Failed to connect to bus: No medium found")} },
			"refused a delegated scope (U-K1-1): Failed to connect to bus"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newFakeHost(t)
			h.own.Store(ptr(h.unwritable()))
			m := c.m()
			m.host = h
			var announced []Owner
			o := ProviderOptions{UserManager: m, ScopeTimeout: 100 * time.Millisecond, Announce: func(o Owner) { announced = append(announced, o) }}
			begin := time.Now()
			if _, _, err := OpenRegistry(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(begin); d > 5*time.Second {
				t.Fatalf("the provider start took %s; the request is bounded", d)
			}
			if _, _, err := OpenRegistry(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				p := startStubborn(t)
				if got := p.Owner(); got.Kind != ProcessGroup || got.Exact || !strings.Contains(got.Fallback, c.want) {
					t.Errorf("owner = %s; want a process group naming %q", got, c.want)
				}
			}
			if n := m.calls.Load(); n != 1 {
				t.Errorf("scope requests = %d; want one", n)
			}
			if len(announced) != 1 || announced[0].Kind != ProcessGroup || !strings.Contains(announced[0].Fallback, c.want) {
				t.Errorf("announcements = %v; want the process-group fallback once", announced)
			}
			if DelegatedScope() != "" {
				t.Errorf("DelegatedScope = %q; want none", DelegatedScope())
			}
		})
	}
}

// A writable own group needs no scope: the manager is not asked.
//
//dsh:test DH-T-EXEC-4
func TestWritableOwnGroupAsksNoScope(t *testing.T) {
	h := newFakeHost(t)
	own := filepath.Join(h.root, "own")
	if err := os.Mkdir(own, 0o755); err != nil {
		t.Fatal(err)
	}
	h.own.Store(&own)
	m := &fakeManager{host: h}
	if _, _, err := OpenRegistry(context.Background(), ProviderOptions{UserManager: m}); err != nil {
		t.Fatal(err)
	}
	p := startStubborn(t)
	if m.calls.Load() != 0 || p.Owner().Kind != ControlGroup || filepath.Dir(ownerCgroup(p.owner)) != own || DelegatedScope() != "" {
		t.Fatalf("requests %d, owner %s in %q, scope %q; want no request and a group in the own group", m.calls.Load(), p.Owner(), ownerCgroup(p.owner), DelegatedScope())
	}
}

// The real manager, with no user bus to reach, fails promptly as a refusal and
// leaves this process where it is.
func TestSystemdUserManagerWithoutABus(t *testing.T) {
	run, err := exec.LookPath("systemd-run")
	if err != nil {
		t.Skip("no systemd-run on PATH")
	}
	env := Env{"PATH=" + filepath.Dir(run) + ":/usr/bin:/bin", "XDG_RUNTIME_DIR=" + t.TempDir(), "DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(t.TempDir(), "no-bus")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = SystemdUserManager{Env: env}.DelegateScope(ctx, "dsh-test-never.scope", os.Getpid())
	if err == nil || ctx.Err() != nil {
		t.Fatalf("DelegateScope without a bus = %v (ctx %v); want a prompt refusal", err, ctx.Err())
	}
	if b, _ := os.ReadFile("/proc/self/cgroup"); strings.Contains(string(b), "dsh-test-never") {
		t.Fatalf("this process moved: %s", b)
	}
}

func ptr(s string) *string { return &s }
