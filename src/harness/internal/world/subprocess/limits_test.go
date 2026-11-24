package subprocess_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// The Limits half of DH-T-EXEC-17 (19.4.5): a tree's memory and process-count
// caps are applied by the job object and by a control group whose controllers
// are delegated, where memory.max and pids.max then hold them, and are reported
// unavailable on the process-group rung. No cap asked is reported none. The
// outcome is logged as the evidence line.
//
//dsh:test DH-T-EXEC-17
func TestLimitsAreAppliedByTheOwnerOrReportedUnavailable(t *testing.T) {
	const mem, procs = 256 << 20, 64
	spec := subprocesstest.Spec(t, subprocesstest.RoleStubborn)
	spec.Limits = subprocess.Limits{MemoryBytes: mem, Processes: procs}
	p := subprocesstest.Start(t, spec)
	r := p.Limits()
	t.Logf("owner %s; limits %s", p.Owner(), r)
	switch p.Owner().Kind {
	case subprocess.JobObject:
		if r.State != subprocess.LimitsEnforced {
			t.Fatalf("job limits = %s; want enforced", r)
		}
	case subprocess.ControlGroup:
		if r.State != subprocess.LimitsEnforced {
			// Controllers not delegated to the parent: said, never silent.
			if r.Detail == "" || r.State == subprocess.LimitsNone {
				t.Fatalf("control-group limits = %s; want enforced, or why not", r)
			}
			break
		}
		dir := childGroup(t, p.Pid())
		for file, want := range map[string]int{"memory.max": mem, "pids.max": procs} {
			b, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(want) {
				t.Fatalf("%s/%s = %q (%v); want %d", dir, file, b, err, want)
			}
			t.Logf("%s = %s", file, strings.TrimSpace(string(b)))
		}
	case subprocess.ProcessGroup:
		if r.State != subprocess.LimitsUnavailable || r.Detail == "" {
			t.Fatalf("process-group limits = %s; want unavailable, with why", r)
		}
	}
	plain := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleExit))
	if got := plain.Limits(); got.State != subprocess.LimitsNone {
		t.Fatalf("limits with none asked = %s; want none", got)
	}
}

// On Windows the job's active-process limit holds: a child capped at one process
// cannot start its grandchild.
//
//dsh:test DH-T-EXEC-17
func TestJobProcessLimitStopsAGrandchild(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the job object's active-process limit is Windows'; pids.max counts threads on Linux")
	}
	spec := subprocesstest.Spec(t, subprocesstest.RoleTree)
	spec.Limits = subprocess.Limits{Processes: 1}
	p := subprocesstest.Start(t, spec)
	if err := p.Wait(); err == nil || p.ExitCode() != 2 {
		t.Fatalf("Wait = %v, exit %d; want the tree child to fail starting its grandchild", err, p.ExitCode())
	}
}

// childGroup is the control group directory of pid.
func childGroup(t *testing.T, pid int) string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+rel))
		}
	}
	t.Fatalf("no cgroup v2 entry for %d", pid)
	return ""
}
