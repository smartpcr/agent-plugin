//go:build windows

package local_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
)

const (
	fsctlSetReparsePoint    = 0x000900A4
	ioReparseTagMountPoint  = 0xA0000003
	reparseHeaderSize       = 8
	mountPointHeaderSize    = 8
	errorPrivilegeNotHeld   = windows.Errno(1314)
	errorInvalidReparseData = windows.Errno(4392)
)

// makeLink makes link point at target: a symbolic link when the process may
// create one (Developer Mode or the privilege), else a directory junction,
// which can stand only for an absolute link to a directory (architecture.md
// "Gate-local services"). It returns which kind it made.
func makeLink(target, link string) (string, error) {
	err := os.Symlink(target, link)
	if err == nil {
		return "symlink", nil
	}
	if !errors.Is(err, errorPrivilegeNotHeld) {
		return "", err
	}
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("%w: symbolic links need a privilege this process lacks, and a junction cannot be relative (%s)", fstest.ErrLinkUnsupported, target)
	}
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%w: symbolic links need a privilege this process lacks, and a junction cannot point at a file (%s)", fstest.ErrLinkUnsupported, target)
	}
	return "junction", junction(target, link)
}

// junction makes link a directory junction (a mount point reparse point) to
// the absolute directory target, which need not exist.
func junction(target, link string) error {
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
	dataLen := mountPointHeaderSize + pathBytes
	buf := make([]byte, reparseHeaderSize+dataLen)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], ioReparseTagMountPoint)
	le.PutUint16(buf[4:], uint16(dataLen))
	le.PutUint16(buf[8:], 0)                     // substitute name offset
	le.PutUint16(buf[10:], uint16(len(sub)*2))   // substitute name length
	le.PutUint16(buf[12:], uint16(len(sub)*2+2)) // print name offset
	le.PutUint16(buf[14:], uint16(len(print)*2)) // print name length
	off := reparseHeaderSize + mountPointHeaderSize
	for _, c := range append(append(sub, 0), append(print, 0)...) {
		le.PutUint16(buf[off:], c)
		off += 2
	}
	var ret uint32
	if err := windows.DeviceIoControl(h, fsctlSetReparsePoint, &buf[0], uint32(len(buf)), nil, 0, &ret, nil); err != nil {
		if errors.Is(err, errorInvalidReparseData) {
			return fmt.Errorf("%w: junction %s -> %s: %v", fstest.ErrLinkUnsupported, link, target, err)
		}
		return &os.PathError{Op: "junction", Path: link, Err: err}
	}
	ok = true
	return nil
}
