//go:build linux

package launch

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// busDialLimit bounds each connection attempt to a candidate bus socket.
const busDialLimit = 200 * time.Millisecond

// sessionBusUnreachable reports that the stage can reach none of the user's
// session services that start programs on request: the D-Bus session bus (named
// by DBUS_SESSION_BUS_ADDRESS, or $XDG_RUNTIME_DIR/bus) and the systemd user
// manager ($XDG_RUNTIME_DIR/systemd/private), looked up under both the
// environment's runtime directory and /run/user/<uid>. An abstract socket address
// cannot be shut out by a mount and counts as reachable.
func sessionBusUnreachable() bool {
	var paths []string
	for _, part := range strings.Split(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), ";") {
		for _, kv := range strings.Split(strings.TrimPrefix(part, "unix:"), ",") {
			switch {
			case strings.HasPrefix(kv, "abstract="):
				return false
			case strings.HasPrefix(kv, "path="):
				paths = append(paths, strings.TrimPrefix(kv, "path="))
			}
		}
	}
	dirs := []string{"/run/user/" + strconv.Itoa(os.Getuid())}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && filepath.IsAbs(d) {
		dirs = append(dirs, d)
	}
	for _, d := range dirs {
		paths = append(paths, filepath.Join(d, "bus"), filepath.Join(d, "systemd", "private"))
	}
	for _, p := range paths {
		c, err := net.DialTimeout("unix", p, busDialLimit)
		if err == nil {
			_ = c.Close()
			return false
		}
	}
	return true
}
