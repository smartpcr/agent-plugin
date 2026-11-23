//go:build linux

package subprocess

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// groupRungEnv, set in the environment of a re-executed test binary, makes that
// process own its trees with a process group, as a host whose control group is not
// writable does. It has no DSH_ prefix because BuildEnv removes every DSH_* name.
const groupRungEnv = "SUBPROCESS_TEST_GROUP_RUNG"

// init runs before TestMain, so the forced rung holds before any start.
func init() {
	if os.Getenv(groupRungEnv) != "" {
		cgroupOnce.Do(func() { cgroupWhy = "forced by " + groupRungEnv })
	}
}

// The process-group rung asks for the parent-death signal and starts from the
// locked thread, on its own and as the control group's fallback.
//
//dsh:test DH-T-NODEPROC-7
func TestGroupRungAsksForTheDeathSignal(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	build := func() *exec.Cmd {
		return &exec.Cmd{Path: exe, Args: []string{exe}, Env: []string{"SUBPROCESSTEST_ROLE=exit"}}
	}
	o := &groupOwner{}
	cmd, err := o.start(build)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	if a := cmd.SysProcAttr; a == nil || !a.Setpgid || a.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("SysProcAttr = %+v; want its own group and SIGKILL on the host's death", a)
	}
	if _, err := o.start(func() *exec.Cmd { return &exec.Cmd{Path: "/nonexistent/program"} }); err == nil {
		t.Fatal("start of a missing program succeeded")
	}
}
