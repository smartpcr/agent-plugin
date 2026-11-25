package launch

import (
	"errors"
	"net"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// busDialLimit bounds each connection attempt to a candidate bus socket.
const busDialLimit = 200 * time.Millisecond

// busProbe is the stage's look for a same-user service that starts programs on
// request: the D-Bus session bus and the systemd user manager. It shows them
// unreachable only when every place it knows of is shown absent; anything it
// cannot judge counts as reachable, so delegated-execution stays not governed
// (DH-ADR-SANDBOX-3: unknown is not governed; I2 Q4).
type busProbe struct {
	// address is DBUS_SESSION_BUS_ADDRESS, empty when unset.
	address string
	// runtimeDir is XDG_RUNTIME_DIR when it is absolute, else empty; it is what
	// a unix:runtime=yes address names.
	runtimeDir string
	// dirs are the runtime directories looked in for bus and systemd/private.
	dirs []string
	// dial connects to the Unix socket at path.
	dial func(path string) error
	// unixTable reads /proc/net/unix. It is set when the stage shares the host
	// network namespace, and so its abstract Unix socket namespace: a bus there
	// (dbus-launch's unix:abstract=...) is reachable even when the address is
	// unset or scrubbed. Nil when the stage has a network namespace of its own.
	unixTable func() ([]byte, error)
}

// unreachable reports that the probe saw no session service. It is false when
// the address names a transport other than a Unix path (tcp:, nonce-tcp:,
// unixexec:, launchd:, systemd:, autolaunch:, an abstract socket), is malformed,
// or when any dial fails for a reason other than the socket's absence (a time
// out, a permission denial): only "no such file", "not a directory" and
// "connection refused" show a place empty. When the stage shares the host's
// abstract socket namespace it is also false when /proc/net/unix lists any
// abstract socket or cannot be read: the probe cannot tell a bus from another
// service there, so it counts every one reachable.
func (b busProbe) unreachable() bool {
	paths, ok := b.addressPaths()
	if !ok {
		return false
	}
	if b.unixTable != nil {
		t, err := b.unixTable()
		if err != nil || hasAbstractSocket(t) {
			return false
		}
	}
	for _, d := range b.dirs {
		paths = append(paths, path.Join(d, "bus"), path.Join(d, "systemd", "private"))
	}
	for _, s := range paths {
		if !absent(b.dial(s)) {
			return false
		}
	}
	return true
}

// addressPaths are the Unix socket paths the bus address names; ok is false
// when any of its entries is something the probe cannot show absent.
func (b busProbe) addressPaths() (paths []string, ok bool) {
	for _, entry := range strings.Split(b.address, ";") {
		if entry == "" {
			continue
		}
		transport, rest, found := strings.Cut(entry, ":")
		if !found || transport != "unix" {
			return nil, false
		}
		var p string
		for _, kv := range strings.Split(rest, ",") {
			k, v, found := strings.Cut(kv, "=")
			if !found {
				return nil, false
			}
			v, err := unescapeBusValue(v)
			if err != nil {
				return nil, false
			}
			switch k {
			case "path":
				p = v
			case "runtime":
				if v != "yes" || b.runtimeDir == "" {
					return nil, false
				}
				p = path.Join(b.runtimeDir, "bus")
			case "guid":
			default: // abstract=, dir=, tmpdir= and anything newer
				return nil, false
			}
		}
		if !path.IsAbs(p) {
			return nil, false
		}
		paths = append(paths, p)
	}
	return paths, true
}

// hasAbstractSocket reports that a /proc/net/unix table lists a socket in the
// abstract namespace: its Path column, the eighth, begins with "@". A table
// without its header line is not one the probe can judge, and counts as listing
// one.
func hasAbstractSocket(table []byte) bool {
	lines := strings.Split(string(table), "\n")
	if !strings.HasPrefix(lines[0], "Num") {
		return true
	}
	for _, l := range lines[1:] {
		if f := strings.Fields(l); len(f) >= 8 && strings.HasPrefix(f[7], "@") {
			return true
		}
	}
	return false
}

// unescapeBusValue decodes a D-Bus address value: bytes outside
// [-0-9A-Za-z_/.\*] are written %XX (D-Bus specification, "Server Addresses").
func unescapeBusValue(v string) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			sb.WriteByte(v[i])
			continue
		}
		if i+2 >= len(v) {
			return "", errors.New("truncated escape")
		}
		n, err := strconv.ParseUint(v[i+1:i+3], 16, 8)
		if err != nil {
			return "", err
		}
		sb.WriteByte(byte(n))
		i += 2
	}
	return sb.String(), nil
}

// absent reports a dial error that shows no service listens at the path.
func absent(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ECONNREFUSED)
}

// dialUnix connects to the Unix socket at path and closes the connection.
func dialUnix(path string) error {
	c, err := net.DialTimeout("unix", path, busDialLimit)
	if err != nil {
		return err
	}
	return c.Close()
}
