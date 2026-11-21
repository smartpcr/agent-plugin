//go:build scenario && windows

package scenarios

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"golang.org/x/sys/windows"
)

// Windows has no SIGTERM to send: the termination signal is closing the console,
// which the runtime delivers to the program as syscall.SIGTERM (CTRL_CLOSE_EVENT).
// The launcher's tree owner starts its child without a console, so the tree's root
// is a re-exec of this test binary acting as the console host: it starts the
// binary in a hidden console of its own (inside the same Job Object, so the tree
// is still owned whole), records the binary's pid and exits with its code.
// terminate closes the binary's console from a second re-exec, since attaching
// this process to it would close this process too.

const (
	closeConsoleEnv = "DSH_SCENARIO_CLOSE_CONSOLE_OF"
	consoleHostEnv  = "DSH_SCENARIO_CONSOLE_HOST"
	consolePidEnv   = "DSH_SCENARIO_CONSOLE_PIDFILE"
)

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
	if bin := os.Getenv(consoleHostEnv); bin != "" {
		os.Exit(hostConsole(bin, os.Args[1:]))
	}
}

// hostConsole runs bin with args in a hidden console of its own -- a child of
// this process, so inside the same Job Object -- and returns its exit code; 125 is
// a failure before bin ran.
func hostConsole(bin string, args []string) int {
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, consoleHostEnv+"=") && !strings.HasPrefix(kv, consolePidEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "console host:", err)
		return 125
	}
	if f := os.Getenv(consolePidEnv); f != "" {
		_ = os.WriteFile(f, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}
	_ = cmd.Wait()
	return cmd.ProcessState.ExitCode()
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

// hostCommand runs bin with args under the console host; the pid it returns is the
// binary's, read from the file the console host writes.
func hostCommand(t *testing.T, env []string, bin string, args ...string) ([]string, []string, func(*launcher.Host) int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	env = append(append([]string(nil), env...), consoleHostEnv+"="+bin, consolePidEnv+"="+pidFile)
	pid := func(h *launcher.Host) int {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if data, err := os.ReadFile(pidFile); err == nil {
				if n, err := strconv.Atoi(string(data)); err == nil {
					return n
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("the console host recorded no pid for %s:\n%s", bin, h.Stderr.String())
			}
			<-time.After(20 * time.Millisecond)
		}
	}
	return append([]string{self}, args...), env, pid
}

// terminate sends the host the termination signal by closing its console.
func terminate(t *testing.T, h *dshHost) error {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	closer := exec.Command(self)
	closer.Env = append(os.Environ(), closeConsoleEnv+"="+strconv.Itoa(h.pid()))
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
