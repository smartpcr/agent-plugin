//go:build unix

package fsx

import (
	"os"

	"golang.org/x/sys/unix"
)

func identity(path string) (FileID, int64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return FileID{}, 0, &os.PathError{Op: "identity", Path: path, Err: err}
	}
	return FileID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, st.Ctim.Nano(), nil
}
