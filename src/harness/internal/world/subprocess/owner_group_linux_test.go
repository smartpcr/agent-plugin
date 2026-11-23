//go:build linux

package subprocess_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// groupRungEnv forces a re-executed host onto the process-group rung
// (owner_group_linux_internal_test.go).
const groupRungEnv = "SUBPROCESS_TEST_GROUP_RUNG"

// deathWait is how long a killed host's direct child may outlive it.
const deathWait = time.Second

// A host on the process-group rung that is killed without warning takes its
// direct child with it within a second: the child carries the parent-death signal
// (DH-T-NODEPROC-7, Linux half; M1-G-BOOT-5). The host is a re-executed copy of
// this binary forced onto the group rung, whatever this process's own rung is;
// only the host's pid is killed, never a group, so nothing but the death signal
// can end the child.
//
//dsh:test DH-T-NODEPROC-7
func TestKilledHostTakesItsDirectChild(t *testing.T) {
	spec := subprocesstest.Spec(t, subprocesstest.RoleHost)
	spec.Env = append(spec.Env, groupRungEnv+"=1")
	pr, pw := io.Pipe()
	spec.Stdout = pw
	host := subprocesstest.Start(t, spec)
	t.Cleanup(func() { _ = pr.Close() })
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("host said %q: %v", line, err)
	}
	var child int
	var kind string
	if _, err := fmt.Sscanf(line, "host %d %s", &child, &kind); err != nil {
		t.Fatalf("host said %q: %v", line, err)
	}
	// A child that survives a failure here is not left behind.
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if kind != string(subprocess.ProcessGroup) {
		t.Fatalf("the host owns its child with %s; want the forced process group", kind)
	}
	if !subprocesstest.Alive(child) {
		t.Fatalf("child %d is not running before the host is killed", child)
	}

	if err := syscall.Kill(host.Pid(), syscall.SIGKILL); err != nil {
		t.Fatalf("kill host %d: %v", host.Pid(), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), deathWait)
	defer cancel()
	if err := procwait.Poll(ctx, func() bool { return gone(child) }); err != nil {
		t.Fatalf("child %d still runs %v after its host was killed (state %s): %v", child, deathWait, state(child), err)
	}
	if werr := host.Wait(); werr == nil {
		t.Fatal("the killed host reported success")
	}
}

// gone reports that pid has exited: it no longer exists, or it is a zombie
// waiting for whichever process reaps orphans.
func gone(pid int) bool {
	if !subprocesstest.Alive(pid) {
		return true
	}
	s := state(pid)
	return s == "Z" || s == "X" || s == ""
}

// state is the process state letter of /proc/<pid>/stat, "" when it cannot be
// read.
func state(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	// The command name is in parentheses and may hold spaces; the state follows.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return ""
	}
	return s[i+2 : i+3]
}
