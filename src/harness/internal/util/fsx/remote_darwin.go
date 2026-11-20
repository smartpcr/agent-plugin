//go:build darwin

package fsx

import (
	"bytes"

	"golang.org/x/sys/unix"
)

var remoteTypes = map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "cifs": true, "macfuse": true, "osxfuse": true}

// IsRemote reports whether path lies on a network filesystem, where change
// notification is unreliable and a watcher should poll.
func IsRemote(path string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, err
	}
	name := st.Fstypename[:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return remoteTypes[string(name)], nil
}
