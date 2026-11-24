//go:build e2e && windows

package e2e

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"
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
// create one, else a directory junction, which can stand only for an absolute
// link to a directory. It returns the kind it made.
func ffsMakeLink(target, link string) (string, error) {
	err := os.Symlink(target, link)
	if err == nil {
		return "symlink", nil
	}
	if !errors.Is(err, ffsErrPrivilegeNotHeld) {
		return "", err
	}
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("%w: symbolic links need a privilege this process lacks, and a junction cannot be relative (%s)", fstest.ErrLinkUnsupported, target)
	}
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%w: symbolic links need a privilege this process lacks, and a junction cannot point at a file (%s)", fstest.ErrLinkUnsupported, target)
	}
	return "junction", ffsJunction(target, link)
}

func ffsJunction(target, link string) error {
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

	sub := utf16.Encode([]rune(`\??\` + filepath.Clean(target)))
	print := utf16.Encode([]rune(filepath.Clean(target)))
	pathBytes := (len(sub) + 1 + len(print) + 1) * 2
	dataLen := ffsMountPointHeaderSize + pathBytes
	buf := make([]byte, ffsReparseHeaderSize+dataLen)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], ffsReparseTagMountPoint)
	le.PutUint16(buf[4:], uint16(dataLen))
	le.PutUint16(buf[8:], 0)
	le.PutUint16(buf[10:], uint16(len(sub)*2))
	le.PutUint16(buf[12:], uint16(len(sub)*2+2))
	le.PutUint16(buf[14:], uint16(len(print)*2))
	off := ffsReparseHeaderSize + ffsMountPointHeaderSize
	for _, c := range append(append(sub, 0), append(print, 0)...) {
		le.PutUint16(buf[off:], c)
		off += 2
	}
	var ret uint32
	if err := windows.DeviceIoControl(h, ffsFsctlSetReparsePoint, (*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil, 0, &ret, nil); err != nil {
		if errors.Is(err, ffsErrInvalidReparseData) {
			return fmt.Errorf("%w: junction %s -> %s: %v", fstest.ErrLinkUnsupported, link, target, err)
		}
		return &os.PathError{Op: "junction", Path: link, Err: err}
	}
	ok = true
	return nil
}
