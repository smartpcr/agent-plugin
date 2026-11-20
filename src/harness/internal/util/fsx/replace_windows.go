//go:build windows

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func moveFlags(replace, durable bool) uint32 {
	var flags uint32
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if durable {
		flags |= windows.MOVEFILE_WRITE_THROUGH
	}
	return flags
}

// fileRenameInfo is FILE_RENAME_INFO as SetFileInformationByHandle takes it with
// the FileRenameInfoEx class: Flags replaces the ReplaceIfExists boolean.
type fileRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

const (
	renameFlagReplace = 0x1 // FILE_RENAME_FLAG_REPLACE_IF_EXISTS
	renameFlagPOSIX   = 0x2 // FILE_RENAME_FLAG_POSIX_SEMANTICS
)

// posixRename renames with POSIX semantics (Windows 10 1709 and later, NTFS): the
// name is swapped even while readers hold the old file open with delete sharing,
// which is how every reader through Open holds it. When durable, the file's
// buffers, its new name included, are flushed before returning.
func posixRename(from, to string, replace, durable bool) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	name, err := windows.UTF16FromString(abs)
	if err != nil {
		return err
	}
	access := uint32(windows.DELETE | windows.SYNCHRONIZE)
	if durable {
		access |= windows.GENERIC_WRITE // FlushFileBuffers needs it
	}
	open := func() (windows.Handle, error) {
		return windows.CreateFile(src, access,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	h, err := open()
	if durable && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// A read-only file cannot be opened for writing; its content was synced
		// before it was closed, so only the name's flush is forgone.
		durable, access = false, windows.DELETE|windows.SYNCHRONIZE
		h, err = open()
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	size := int(unsafe.Offsetof(fileRenameInfo{}.FileName)) + 2*len(name)
	buf := make([]uint64, (size+7)/8) // 8-byte aligned, as the handle field needs
	info := (*fileRenameInfo)(unsafe.Pointer(&buf[0]))
	info.Flags = renameFlagPOSIX
	if replace {
		info.Flags |= renameFlagReplace
	}
	info.FileNameLength = uint32(2 * (len(name) - 1)) // bytes, without the NUL
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	if err := windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, (*byte)(unsafe.Pointer(info)), uint32(size)); err != nil {
		return err
	}
	if durable {
		return windows.FlushFileBuffers(h)
	}
	return nil
}

// posixUnsupported reports the errors of a filesystem or Windows version without
// POSIX rename semantics.
func posixUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION)
}

// move publishes from as to, retrying while another program blocks it: POSIX
// rename where the filesystem has it, else MoveFileEx.
func move(from, to string, replace, durable bool) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return retryInUse(func() error {
		if usePOSIXRename {
			err := posixRename(from, to, replace, durable)
			if !posixUnsupported(err) {
				return err
			}
		}
		return windows.MoveFileEx(src, dst, moveFlags(replace, durable))
	})
}

// usePOSIXRename is cleared by tests to take the MoveFileEx path.
var usePOSIXRename = true

func renameReplace(stage, target string, durable bool) error {
	return move(stage, target, true, durable)
}

func renameNoReplace(stage, target string, durable bool) error {
	return move(stage, target, false, durable)
}

// syncDir is a no-op: MOVEFILE_WRITE_THROUGH already made the rename durable, and
// Windows cannot flush a directory handle.
func syncDir(string) error { return nil }

func syncRootDir(*os.Root, string) error { return nil }

// copyACL gives to the discretionary access-control list of from, so replacing a
// file does not silently widen or narrow who may read it (DH-LIB-NODEFS-T3).
func copyACL(from, to string) error {
	sd, err := windows.GetNamedSecurityInfo(from, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if control, _, err := sd.Control(); err == nil && control&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
}

func isInUse(err error) bool {
	var errno windows.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case windows.ERROR_SHARING_VIOLATION, windows.ERROR_ACCESS_DENIED, windows.ERROR_LOCK_VIOLATION:
		return true
	}
	return false
}

func isCrossDevice(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
