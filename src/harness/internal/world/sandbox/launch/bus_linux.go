//go:build linux

package launch

import (
	"os"
	"path/filepath"
	"strconv"
)

// sessionBusUnreachable reports that the stage can reach none of the user's
// session services that start programs on request (busProbe): the address in
// DBUS_SESSION_BUS_ADDRESS, and bus and systemd/private under both
// /run/user/<uid> and $XDG_RUNTIME_DIR.
func sessionBusUnreachable() bool {
	b := busProbe{address: os.Getenv("DBUS_SESSION_BUS_ADDRESS"), dial: dialUnix}
	b.dirs = []string{"/run/user/" + strconv.Itoa(os.Getuid())}
	if d := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		b.runtimeDir = d
		b.dirs = append(b.dirs, d)
	}
	return b.unreachable()
}
