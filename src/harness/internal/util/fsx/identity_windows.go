//go:build windows

package fsx

import (
	"os"

	"golang.org/x/sys/windows"
)

// identity opens path for attributes only (directories too, through backup
// semantics) and reads its volume serial number and file index.
func identity(path string) (FileID, int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FileID{}, 0, err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return FileID{}, 0, &os.PathError{Op: "identity", Path: path, Err: err}
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return FileID{}, 0, &os.PathError{Op: "identity", Path: path, Err: err}
	}
	return FileID{
		Dev: uint64(info.VolumeSerialNumber),
		Ino: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
	}, 0, nil
}
