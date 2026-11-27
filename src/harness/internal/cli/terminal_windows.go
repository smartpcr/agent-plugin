package cli

import "golang.org/x/sys/windows"

// hideEcho switches echo off on the console input handle fd, keeping line input
// so Enter ends the line, and returns the function that puts the console mode
// back as it was. ok is false when fd is not a console.
func hideEcho(fd uintptr) (restore func() error, ok bool, err error) {
	h := windows.Handle(fd)
	var saved uint32
	if err := windows.GetConsoleMode(h, &saved); err != nil {
		return nil, false, nil
	}
	quiet := saved&^windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(h, quiet); err != nil {
		return nil, true, err
	}
	return func() error { return windows.SetConsoleMode(h, saved) }, true, nil
}
