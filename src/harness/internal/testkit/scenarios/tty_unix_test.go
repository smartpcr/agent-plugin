//go:build scenario && (linux || darwin)

package scenarios

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func init() {
	if bin := os.Getenv(terminalHostEnv); bin != "" {
		os.Exit(hostTerminal(bin, os.Args[1:]))
	}
}

// hostTerminal runs bin with args, its standard error on a new pty and its other
// streams this process's, copies the pty to this process's standard error,
// forwards SIGTERM and SIGINT, and returns bin's exit code (128+n for signal n);
// 125 is a failure before bin ran. bin stays in this process's group and session,
// so the launcher's tree still owns it whole. With terminalInputEnv set, bin
// instead leads a session of its own whose controlling terminal is the pty (so
// /dev/tty is the pty and a typed Ctrl-C signals bin), the input file is typed
// into the pty, and bin is bound to this process (bindToHost) since the group
// kill no longer reaches it.
func hostTerminal(bin string, args []string) int {
	ptm, pts, err := openPTY()
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminal host:", err)
		return 125
	}
	input := os.Getenv(terminalInputEnv)
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, pts
	cmd.Env = withoutTerminalHost(os.Environ())
	if input != "" {
		// Ctty is a descriptor in bin: 2, its standard error, the pts.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 2}
		bindToHost(cmd.SysProcAttr)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "terminal host:", err)
		return 125
	}
	_ = pts.Close()
	if input != "" {
		done := make(chan struct{})
		defer close(done)
		go feedTerminal(input, ptm, done)
	}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		// The read ends with EIO once no process holds the pty.
		_, _ = io.Copy(os.Stderr, ptm)
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for s := range sig {
			_ = cmd.Process.Signal(s)
		}
	}()
	_ = cmd.Wait()
	// A descendant that still holds the pty does not hold the host.
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return 125
}
