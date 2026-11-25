//go:build linux

package launch

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TestSessionBusObservedForReal: the observer the stage runs, with real Unix
// sockets: a listening socket is reachable (also through an escaped address), a
// missing path and a socket nobody listens on are absent, a socket in a
// directory the stage cannot search is unknown, and a tcp: address is never
// shown absent.
//
//dsh:test DH-T-SANDBOX-13
func TestSessionBusObservedForReal(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "b us")
	ln, err := net.Listen("unix", live)
	if err != nil {
		t.Skipf("no Unix sockets here: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	stale := filepath.Join(dir, "stale")
	sl, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	sl.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = sl.Close() })
	_ = sl.Close()

	probe := func(address string, dirs ...string) bool {
		return busProbe{address: address, dirs: dirs, dial: dialUnix}.unreachable()
	}
	if probe("unix:path=" + filepath.Join(dir, "b%20us")) {
		t.Error("a listening bus behind an escaped address was shown unreachable")
	}
	if !probe("unix:path="+filepath.Join(dir, "missing")+";unix:path="+stale, filepath.Join(dir, "nothing")) {
		t.Error("a missing path and a socket with no listener were not shown absent")
	}
	if !probe("", dir) {
		t.Error("a runtime directory with no bus or systemd/private socket was not shown absent")
	}
	if probe("unix:path="+live, dir) {
		t.Error("a listening bus was shown unreachable")
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		if probe("unix:path=" + filepath.Join(locked, "bus")) {
			t.Error("a socket the stage may not search for was shown absent")
		}
	}

	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "tcp:host=127.0.0.1,port=1")
	if sessionBusUnreachable() {
		t.Error("a tcp: session bus was shown unreachable")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+live)
	if sessionBusUnreachable() {
		t.Error("the listening session bus named by the environment was shown unreachable")
	}
}
