//go:build linux

package subprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Only a failure that comes from placing the child in its control group may fall
// back to a process group; a failure of the program or its directory is the
// caller's and is never retried. EACCES and EPERM are blamed on the group only
// when the program and the directory are themselves usable.
//
//dsh:test DH-T-EXEC-4
func TestCgroupStartErrorBlamesOnlyTheGroup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	usable := &exec.Cmd{Path: exe}
	inDir := &exec.Cmd{Path: filepath.Base(exe), Dir: filepath.Dir(exe)}
	for _, c := range []struct {
		name string
		cmd  *exec.Cmd
		err  error
		want bool
	}{
		{"not an errno", usable, errors.New("exec: not started"), false},
		{"ENOSYS", usable, syscall.ENOSYS, true},
		{"EOPNOTSUPP", usable, fmt.Errorf("fork/exec: %w", syscall.EOPNOTSUPP), true},
		{"EBUSY", usable, syscall.EBUSY, true},
		{"E2BIG", usable, syscall.E2BIG, true},
		{"ENOENT", usable, syscall.ENOENT, false},
		{"EACCES, usable program", usable, syscall.EACCES, true},
		{"EPERM, usable program found in its directory", inDir, syscall.EPERM, true},
		{"EACCES, program not executable", &exec.Cmd{Path: plain}, syscall.EACCES, false},
		{"EACCES, program is a directory", &exec.Cmd{Path: dir}, syscall.EACCES, false},
		{"EACCES, program missing", &exec.Cmd{Path: filepath.Join(dir, "missing")}, syscall.EACCES, false},
		{"EACCES, directory missing", &exec.Cmd{Path: exe, Dir: filepath.Join(dir, "missing")}, syscall.EACCES, false},
		{"EACCES, directory is a file", &exec.Cmd{Path: exe, Dir: plain}, syscall.EACCES, false},
	} {
		if got := cgroupStartError(c.cmd, c.err); got != c.want {
			t.Errorf("%s: cgroupStartError = %v; want %v", c.name, got, c.want)
		}
	}
}

// When a tree's control group cannot be made, the child still starts, owned by
// a process group, and the owner reports the fallback with its cause; every
// operation then goes to the process group.
//
//dsh:test DH-T-EXEC-4
func TestCgroupOwnerFallsBackToAProcessGroup(t *testing.T) {
	probeCgroup()
	saved := cgroupBase
	t.Cleanup(func() { cgroupBase = saved })
	cgroupBase = filepath.Join(t.TempDir(), "no-such-group")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := &cgroupOwner{}
	cmd, err := o.start(func() *exec.Cmd {
		return &exec.Cmd{Path: exe, Args: []string{exe, "0"}, Env: []string{"SUBPROCESSTEST_ROLE=exit"}}
	})
	if err != nil {
		t.Fatalf("start with no usable group: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the fallen-back child failed: %v", err)
	}
	r := o.report()
	if r.Kind != ProcessGroup || r.Exact || !strings.Contains(r.Fallback, fallbackNoCgroup) || !strings.Contains(r.Fallback, "no such file") {
		t.Fatalf("report = %+v; want a process group naming the fallback and its cause", r)
	}
	if o.dir != "" || o.group.pid != cmd.Process.Pid {
		t.Fatalf("owner dir %q, group pid %d; want no group and the child's process group %d", o.dir, o.group.pid, cmd.Process.Pid)
	}
	if err := o.polite(); err != nil {
		t.Errorf("polite to an exited group = %v; want nil", err)
	}
	if err := o.kill(); err != nil {
		t.Errorf("kill of an exited group = %v; want nil", err)
	}
	if empty, err := o.empty(); !empty || err != nil {
		t.Errorf("empty = %v, %v; want true", empty, err)
	}
	if d := o.describe(); !strings.Contains(d, fmt.Sprintf("process group %d", cmd.Process.Pid)) {
		t.Errorf("describe = %q; want the process group", d)
	}
	o.release()
}

// A process group that never started holds nothing: signals are no-ops and it
// is empty.
func TestUnstartedProcessGroupIsEmpty(t *testing.T) {
	var o groupOwner
	if err := o.polite(); err != nil {
		t.Errorf("polite = %v", err)
	}
	if err := o.kill(); err != nil {
		t.Errorf("kill = %v", err)
	}
	if empty, err := o.empty(); !empty || err != nil {
		t.Errorf("empty = %v, %v; want true", empty, err)
	}
	if n := windowsLineLength([]string{"a", "b"}); n != 0 {
		t.Errorf("windowsLineLength on Unix = %d; want 0", n)
	}
}
