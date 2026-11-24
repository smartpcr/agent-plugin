package subprocesstest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// reap kills pid, a child this process started and released, and reaps it, so
// no process or zombie outlives the test.
func reap(t *testing.T, pid int) {
	t.Helper()
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Kill()
	_, _ = p.Wait()
	if !Exited(pid) {
		t.Errorf("pid %d still running after its kill", pid)
	}
}

// RoleDial connects to the address it is given, says its pid on the connection
// and on standard output, and parks with the connection open; it fails without
// a network and an address, and when nothing listens.
func TestDialRoleConnectsAndSaysItsPid(t *testing.T) {
	noPark(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var out strings.Builder
	if code := run(RoleDial, []string{"tcp", ln.Addr().String()}, &out); code != 0 {
		t.Fatalf("dial role exited %d", code)
	}
	want := fmt.Sprintf("dialed %d\n", os.Getpid())
	if out.String() != want {
		t.Fatalf("dial role printed %q; want %q", out.String(), want)
	}
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if got, err := bufio.NewReader(c).ReadString('\n'); err != nil || got != want {
		t.Fatalf("the connection said %q (%v); want %q", got, err, want)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closed.Close() })
	gone := closed.Addr().String()
	_ = closed.Close() // nothing listens there now
	for _, args := range [][]string{nil, {"tcp"}, {"tcp", gone}} {
		if code := run(RoleDial, args, io.Discard); code != 2 {
			t.Errorf("dial %q exited %d; want 2", args, code)
		}
	}
}

// RoleOrphan starts a stubborn grandchild with no stream tying it to the child,
// reports it and exits at once, leaving it running.
func TestOrphanRoleLeavesItsGrandchildRunning(t *testing.T) {
	var out strings.Builder
	if code := run(RoleOrphan, nil, &out); code != 0 {
		t.Fatalf("orphan role exited %d", code)
	}
	var g int
	if _, err := fmt.Sscanf(out.String(), "orphan %d\n", &g); err != nil || g <= 0 {
		t.Fatalf("orphan role printed %q (%v)", out.String(), err)
	}
	t.Cleanup(func() { reap(t, g) })
	if !Alive(g) {
		t.Fatalf("orphaned grandchild %d is not running", g)
	}
}

// RoleDetached starts a stubborn grandchild that detaches itself, checks that it
// did, reports it once it is ready and exits, leaving it running. On Unix the
// check fails for a process that shares this process's session.
func TestDetachedRoleReportsADetachedGrandchild(t *testing.T) {
	var out strings.Builder
	if code := run(RoleDetached, nil, &out); code != 0 {
		t.Fatalf("detached role exited %d", code)
	}
	var g int
	if _, err := fmt.Sscanf(out.String(), "detached %d\n", &g); err != nil || g <= 0 {
		t.Fatalf("detached role printed %q (%v)", out.String(), err)
	}
	t.Cleanup(func() { reap(t, g) })
	if !Alive(g) {
		t.Fatalf("detached grandchild %d is not running", g)
	}
	if err := checkDetached(g); err != nil {
		t.Fatalf("the grandchild is not detached: %v", err)
	}
	// Windows keeps no queryable process group, so there it has nothing to check.
	if err := checkDetached(os.Getpid()); (err == nil) != (runtime.GOOS == "windows") {
		t.Fatalf("checkDetached of this process = %v", err)
	}
}

// RoleChannel writes its text to the channel whose number it is given and
// closes it, so the reader sees the text and then end of file.
func TestChannelRoleWritesAndCloses(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	fd, err := dupFile(w)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if code := run(RoleChannel, []string{strconv.FormatUint(uint64(fd), 10), "status: ready"}, io.Discard); code != 0 {
		t.Fatalf("channel role exited %d", code)
	}
	b, err := io.ReadAll(r)
	if err != nil || string(b) != "status: ready" {
		t.Fatalf("the channel carried %q (%v); want the text, then end of file", b, err)
	}
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// A role whose output cannot be written fails with code 2: RoleWrite, RoleFlood
// (after it reported its tree, whose grandchild the test then reaps; it fails
// before it would park) and
// RoleChannel given a channel it cannot write.
func TestRolesFailWhenTheirOutputFails(t *testing.T) {
	var errOut strings.Builder
	saved := stderr
	stderr = &errOut
	t.Cleanup(func() { stderr = saved })
	if code := run(RoleWrite, []string{"stdout", "6869"}, failingWriter{}); code != 2 {
		t.Errorf("write to a failing stream exited %d; want 2", code)
	}
	if code := run(RoleFlood, []string{"3"}, failingWriter{}); code != 2 {
		t.Errorf("flood to a failing stream exited %d; want 2", code)
	}
	var self, g int
	if _, err := fmt.Sscanf(errOut.String(), "tree %d %d\n", &self, &g); err != nil || g <= 0 {
		t.Fatalf("flood said %q on stderr (%v); want its tree before the write", errOut.String(), err)
	}
	reap(t, g)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	fd, err := dupFile(r)
	if err != nil {
		t.Fatal(err)
	}
	if code := run(RoleChannel, []string{strconv.FormatUint(uint64(fd), 10), "x"}, io.Discard); code != 2 {
		t.Errorf("channel on a read end exited %d; want 2", code)
	}
}
