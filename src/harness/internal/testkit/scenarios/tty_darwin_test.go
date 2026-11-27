//go:build scenario

package scenarios

import (
	"bytes"
	"os"
	"syscall"
	//lint:ignore typeassert the TIOCPTYGNAME ioctl writes the pts name through a raw pointer; package unix has no wrapper for it
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pty pair (grantpt, unlockpt and ptsname through their
// ioctls); neither end becomes this process's controlling terminal.
func openPTY() (ptm, pts *os.File, err error) {
	ptm, err = os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(ptm.Fd())
	var name [128]byte
	err = unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0)
	if err == nil {
		err = unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0)
	}
	if err == nil {
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); e != 0 {
			err = e
		}
	}
	if err == nil {
		n := bytes.IndexByte(name[:], 0)
		if n < 0 {
			n = len(name)
		}
		pts, err = os.OpenFile(string(name[:n]), os.O_RDWR|unix.O_NOCTTY, 0)
	}
	if err != nil {
		_ = ptm.Close()
		return nil, nil, err
	}
	return ptm, pts, nil
}

// bindToHost is a no-op: darwin has no parent-death signal, so an interactive
// bin outlives a killed terminal host until its terminal closes (the pty
// hangup on the host's exit signals it).
func bindToHost(*syscall.SysProcAttr) {}
