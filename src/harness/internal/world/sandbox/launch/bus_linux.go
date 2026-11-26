//go:build linux

package launch

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// busDialLimit bounds each connection attempt to a candidate bus socket.
const busDialLimit = 200 * time.Millisecond

// dialUnix connects to the Unix socket at path and closes the connection.
func dialUnix(path string) error {
	c, err := net.DialTimeout("unix", path, busDialLimit)
	if err != nil {
		return err
	}
	return c.Close()
}

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
