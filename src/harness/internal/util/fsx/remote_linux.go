//go:build linux

package fsx

import "golang.org/x/sys/unix"

// remoteMagic are the statfs types of network filesystems, plus 9p (WSL's view of
// Windows drives), on all of which inotify misses changes made elsewhere.
var remoteMagic = map[int64]bool{
	0x6969:     true, // NFS
	0x517b:     true, // SMB
	0xff534d42: true, // CIFS
	0xfe534d42: true, // SMB2
	0x01021997: true, // 9p
	0x5346414f: true, // AFS
	0x73757245: true, // Coda
	0x564c:     true, // NCP
	0x47504653: true, // GPFS
	0x0bd00bd0: true, // Lustre
	0x65735546: true, // FUSE (sshfs and kin)
}

// IsRemote reports whether path lies on a network filesystem, where change
// notification is unreliable and a watcher should poll.
func IsRemote(path string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, err
	}
	return remoteMagic[int64(st.Type)], nil
}
