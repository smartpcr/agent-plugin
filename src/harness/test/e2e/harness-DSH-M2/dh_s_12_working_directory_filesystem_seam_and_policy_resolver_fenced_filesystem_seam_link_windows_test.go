//go:build e2e && windows

package e2e

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"
	//lint:ignore typeassert DeviceIoControl(FSCTL_SET_REPARSE_POINT) takes the reparse buffer as a *byte made with unsafe.Pointer
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
)

const (
	ffsFsctlSetReparsePoint  = 0x000900A4
	ffsReparseTagMountPoint  = 0xA0000003
	ffsReparseHeaderSize     = 8
	ffsMountPointHeaderSize  = 8
	ffsErrPrivilegeNotHeld   = windows.Errno(1314)
	ffsErrInvalidReparseData = windows.Errno(4392)
)

// ffsMakeLink makes link point at target: a symbolic link when the process may
// create one, else a reparse point fs/local resolves as a link (it never lets
// the kernel follow one): a directory junction for an absolute link to a
// directory, and otherwise a mount point carrying target as given (relative, or
// a file), which needs no privilege. It returns the kind it made.
func ffsMakeLink(target, link string) (string, error) {
	err := os.Symlink(target, link)
	if err == nil {
		return "symlink", nil
	}
	if !errors.Is(err, ffsErrPrivilegeNotHeld) {
		return "", err
	}
	if fi, err := os.Stat(target); filepath.IsAbs(target) && (err != nil || fi.IsDir()) {
		return "junction", ffsMountPoint(`\??\`+filepath.Clean(target), filepath.Clean(target), link)
	}
	sub := target
	if filepath.IsAbs(target) {
		sub = `\??\` + filepath.Clean(target)
	}
	return "mount-point(" + target + ")", ffsMountPoint(sub, target, link)
}

// ffsMountPoint makes link a directory carrying a mount-point reparse point
// with the substitute name sub and the print name print.
func ffsMountPoint(sub, print, link string) error {
	if err := os.Mkdir(link, 0o755); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(link)
		}
	}()
	p, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return &os.PathError{Op: "junction", Path: link, Err: err}
	}
	defer windows.CloseHandle(h)

	sub16 := utf16.Encode([]rune(sub))
	print16 := utf16.Encode([]rune(print))
	pathBytes := (len(sub16) + 1 + len(print16) + 1) * 2
	dataLen := ffsMountPointHeaderSize + pathBytes
	buf := make([]byte, ffsReparseHeaderSize+dataLen)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], ffsReparseTagMountPoint)
	le.PutUint16(buf[4:], uint16(dataLen))
	le.PutUint16(buf[8:], 0)
	le.PutUint16(buf[10:], uint16(len(sub16)*2))
	le.PutUint16(buf[12:], uint16(len(sub16)*2+2))
	le.PutUint16(buf[14:], uint16(len(print16)*2))
	off := ffsReparseHeaderSize + ffsMountPointHeaderSize
	for _, c := range append(append(sub16, 0), append(print16, 0)...) {
		le.PutUint16(buf[off:], c)
		off += 2
	}
	var ret uint32
	if err := windows.DeviceIoControl(h, ffsFsctlSetReparsePoint, (*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil, 0, &ret, nil); err != nil {
		if errors.Is(err, ffsErrInvalidReparseData) {
			return fmt.Errorf("%w: mount point %s -> %s: %v", fstest.ErrLinkUnsupported, link, print, err)
		}
		return &os.PathError{Op: "mount point", Path: link, Err: err}
	}
	ok = true
	return nil
}
