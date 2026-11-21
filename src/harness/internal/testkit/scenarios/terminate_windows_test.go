//go:build scenario && windows

package scenarios

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Windows has no SIGTERM to send: the termination signal is closing the console,
// which the runtime delivers to the program as syscall.SIGTERM (CTRL_CLOSE_EVENT).
// ownConsole gives the child a hidden console of its own, and terminate closes it
// from a re-exec of this test binary, since attaching this process to the child's
// console would close this process with it.

const closeConsoleEnv = "DSH_SCENARIO_CLOSE_CONSOLE_OF"

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	user32           = windows.NewLazySystemDLL("user32.dll")
	freeConsole      = kernel32.NewProc("FreeConsole")
	attachConsole    = kernel32.NewProc("AttachConsole")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	postMessage      = user32.NewProc("PostMessageW")
)

const wmClose = 0x0010

func init() {
	pid := os.Getenv(closeConsoleEnv)
	if pid == "" {
		return
	}
	if err := closeConsoleOf(pid); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

// closeConsoleOf attaches to pid's console and closes its window.
func closeConsoleOf(pid string) error {
	n, err := strconv.ParseUint(pid, 10, 32)
	if err != nil {
		return err
	}
	_, _, _ = freeConsole.Call()
	if r, _, err := attachConsole.Call(uintptr(n)); r == 0 {
		return fmt.Errorf("attach to the console of %s: %w", pid, err)
	}
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return fmt.Errorf("process %s has no console window", pid)
	}
	if r, _, err := postMessage.Call(hwnd, wmClose, 0, 0); r == 0 {
		return fmt.Errorf("close the console of %s: %w", pid, err)
	}
	return nil
}

// ownConsole starts cmd in a hidden console of its own, so terminate can close it.
func ownConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_CONSOLE
	cmd.SysProcAttr.HideWindow = true
}

// terminate sends cmd, started after ownConsole, the termination signal by closing
// its console.
func terminate(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	closer := exec.Command(self)
	closer.Env = append(os.Environ(), closeConsoleEnv+"="+strconv.Itoa(cmd.Process.Pid))
	if out, err := closer.CombinedOutput(); err != nil {
		return fmt.Errorf("closing the console: %v: %s", err, out)
	}
	return nil
}
