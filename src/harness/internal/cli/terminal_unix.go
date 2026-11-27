//go:build linux || darwin

package cli

import "golang.org/x/sys/unix"

// isTTY reports whether fd is a terminal: one with termios.
func isTTY(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), ioctlGetTermios)
	return err == nil
}

// hideEcho switches echo off on the terminal fd, keeping line input so Enter ends
// the line, and returns the function that puts the terminal back as it was. ok is
// false when fd is not a terminal.
func hideEcho(fd uintptr) (restore func() error, ok bool, err error) {
	saved, err := unix.IoctlGetTermios(int(fd), ioctlGetTermios)
	if err != nil {
		return nil, false, nil
	}
	quiet := *saved
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON
	if err := unix.IoctlSetTermios(int(fd), ioctlSetTermios, &quiet); err != nil {
		return nil, true, err
	}
	return func() error { return unix.IoctlSetTermios(int(fd), ioctlSetTermios, saved) }, true, nil
}
