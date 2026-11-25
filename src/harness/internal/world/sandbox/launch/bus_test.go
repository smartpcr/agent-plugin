package launch

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// TestBusProbeCountsTheUnknownReachable: the bus observation shows the session
// services unreachable only when every place it knows of is shown absent; an
// address it cannot judge, or a dial that fails for another reason, leaves
// delegated-execution not governed (DH-ADR-SANDBOX-3, I2 Q4).
//
//dsh:test DH-T-SANDBOX-13
func TestBusProbeCountsTheUnknownReachable(t *testing.T) {
	gone := func(string) error { return &os.PathError{Op: "dial", Path: "x", Err: syscall.ENOENT} }
	for _, tc := range []struct {
		name    string
		address string
		runtime string
		dial    func(string) error
		want    bool
	}{
		{"nothing anywhere", "", "", gone, true},
		{"a unix path that is gone", "unix:path=/run/user/1/bus,guid=ab", "", gone, true},
		{"refused and not a directory", "unix:path=/a;unix:path=/b", "", func(p string) error {
			if p == "/a" {
				return syscall.ECONNREFUSED
			}
			return syscall.ENOTDIR
		}, true},
		{"runtime=yes with a runtime directory", "unix:runtime=yes", "/run/user/1", gone, true},
		{"runtime=yes without one", "unix:runtime=yes", "", gone, false},
		{"tcp", "tcp:host=127.0.0.1,port=4000", "", gone, false},
		{"nonce-tcp after a unix path", "unix:path=/a;nonce-tcp:host=localhost,port=1", "", gone, false},
		{"unixexec", "unixexec:path=/usr/bin/dbus-daemon", "", gone, false},
		{"autolaunch", "autolaunch:", "", gone, false},
		{"systemd activation", "systemd:", "", gone, false},
		{"abstract", "unix:abstract=/tmp/dbus-x", "", gone, false},
		{"tmpdir", "unix:tmpdir=/tmp", "", gone, false},
		{"a relative path", "unix:path=bus", "", gone, false},
		{"a malformed escape", "unix:path=/a%zz", "", gone, false},
		{"a truncated escape", "unix:path=/a%2", "", gone, false},
		{"a key with no value", "unix:path", "", gone, false},
		{"a bus that answers", "", "", func(string) error { return nil }, false},
		{"a dial that timed out", "", "", func(string) error { return os.ErrDeadlineExceeded }, false},
		{"a dial that was denied", "", "", func(string) error { return syscall.EACCES }, false},
		{"an unclassified dial error", "", "", func(string) error { return errors.New("odd") }, false},
	} {
		b := busProbe{address: tc.address, runtimeDir: tc.runtime, dirs: []string{"/run/user/1"}, dial: tc.dial}
		if got := b.unreachable(); got != tc.want {
			t.Errorf("%s: unreachable = %v, want %v", tc.name, got, tc.want)
		}
	}

	var dialed []string
	b := busProbe{address: "unix:path=/run/a%20b%2cc,guid=1", dirs: []string{"/r"}, dial: func(p string) error {
		dialed = append(dialed, p)
		return syscall.ENOENT
	}}
	if !b.unreachable() || len(dialed) != 3 || dialed[0] != "/run/a b,c" {
		t.Errorf("escaped address dialed %q; want the decoded path first, then the runtime directory's two sockets", dialed)
	}
}
