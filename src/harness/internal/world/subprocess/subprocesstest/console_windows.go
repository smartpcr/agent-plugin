//go:build windows

package subprocesstest

import "golang.org/x/sys/windows"

var getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// consoleWindow reports whether this process has a console window; a child
// created with CREATE_NO_WINDOW has a console and no window.
func consoleWindow() bool {
	h, _, _ := getConsoleWindow.Call()
	return h != 0
}
