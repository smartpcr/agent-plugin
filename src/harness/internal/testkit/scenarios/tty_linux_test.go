//go:build scenario

package scenarios

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pty pair; neither end becomes this process's controlling
// terminal.
func openPTY() (ptm, pts *os.File, err error) {
	ptm, err = os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(ptm.Fd())
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err == nil {
		err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0)
	}
	if err == nil {
		pts, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	}
	if err != nil {
		_ = ptm.Close()
		return nil, nil, err
	}
	return ptm, pts, nil
}

// bindToHost kills bin when the terminal host dies, so a session of its own
// does not outlive the launcher's kill of the host's group.
func bindToHost(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
