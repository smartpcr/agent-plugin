//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Windows has no SIGTERM to send: the termination signal is closing the console,
// which the runtime delivers to the program as syscall.SIGTERM (CTRL_CLOSE_EVENT).
// As in testkit/scenarios/terminate_windows_test.go, dsh runs in a hidden
// console of its own, and a re-exec of this test binary attaches to that console
// and closes its window, since attaching this process would close it too.

const closeConsoleEnv = "DSH_CLI_TEST_CLOSE_CONSOLE_OF"

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	user32           = windows.NewLazySystemDLL("user32.dll")
	freeConsole      = kernel32.NewProc("FreeConsole")
	attachConsole    = kernel32.NewProc("AttachConsole")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	postMessage      = user32.NewProc("PostMessageW")
)

const (
	wmClose            = 0x0010
	statusControlCExit = 0xC000013A
)

func init() {
	if pid := os.Getenv(closeConsoleEnv); pid != "" {
		if err := closeConsoleOf(pid); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

// stoppable starts cmd in a hidden console of its own.
func stoppable(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
}

// closeConsoleOf attaches to pid's console and closes its window.
func closeConsoleOf(pid string) error {
	n, err := strconv.ParseUint(pid, 10, 32)
	if err != nil {
		return err
	}
	_, _, _ = freeConsole.Call()
	// The close reaches this process too once it is attached; a notified SIGTERM
	// lets it detach and exit 0 instead of being ended by the event.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM, os.Interrupt)
	if r, _, err := attachConsole.Call(uintptr(n)); r == 0 {
		return fmt.Errorf("attach to the console of %s: %w", pid, err)
	}
	defer freeConsole.Call()
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return fmt.Errorf("process %s has no console window", pid)
	}
	if r, _, err := postMessage.Call(hwnd, wmClose, 0, 0); r == 0 {
		return fmt.Errorf("close the console of %s: %w", pid, err)
	}
	return nil
}

// terminateProcess sends cmd the termination signal by closing its console.
func terminateProcess(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	closer := exec.Command(self)
	closer.Env = append(os.Environ(), closeConsoleEnv+"="+strconv.Itoa(cmd.Process.Pid))
	if out, err := closer.CombinedOutput(); err != nil {
		// STATUS_CONTROL_C_EXIT: the closer was ended by the very close it posted,
		// which therefore reached the console.
		var exit *exec.ExitError
		if errors.As(err, &exit) && uint32(exit.ExitCode()) == statusControlCExit {
			return nil
		}
		return fmt.Errorf("closing the console: %v: %s", err, out)
	}
	return nil
}
