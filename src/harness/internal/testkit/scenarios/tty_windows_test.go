//go:build scenario && windows

package scenarios

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unicode/utf16"
	//lint:ignore typeassert UpdateProcThreadAttribute takes the pseudo console handle as an unsafe.Pointer and STARTUPINFOEX needs its own size; package windows has no typed wrapper for them
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	// The console host and the console closer (terminate_windows_test.go) carry
	// terminalHostEnv on to the terminal host they start; they are not it.
	if bin := os.Getenv(terminalHostEnv); bin != "" && os.Getenv(consoleHostEnv) == "" && os.Getenv(closeConsoleEnv) == "" {
		os.Exit(hostTerminal(bin, os.Args[1:]))
	}
}

// hostTerminal runs bin with args attached to a new pseudoconsole -- a child of
// this process, so inside the same Job Object -- copies what the pseudoconsole
// renders to this process's standard error, closes the pseudoconsole on the
// termination signal (bin receives CTRL_CLOSE_EVENT, its SIGTERM) and returns
// bin's exit code; 125 is a failure before bin ran.
func hostTerminal(bin string, args []string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "terminal host:", err)
		return 125
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	var pc windows.Handle
	// Wide enough that a launch link is never wrapped.
	if err := windows.CreatePseudoConsole(windows.Coord{X: 512, Y: 64}, windows.Handle(inR.Fd()), windows.Handle(outW.Fd()), 0, &pc); err != nil {
		return fail(err)
	}
	_, _ = inR.Close(), outW.Close() // the pseudoconsole holds its own copies
	defer inW.Close()
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(os.Stderr, outR)
	}()
	closed := false
	closePC := func() {
		if !closed {
			closed = true
			windows.ClosePseudoConsole(pc)
		}
	}
	defer closePC()

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// The attribute's value is the pseudoconsole handle itself.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&pc)), unsafe.Sizeof(pc)); err != nil {
		return fail(err)
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// Null standard handles with STARTF_USESTDHANDLES, so bin's streams are the
	// pseudoconsole's and not this process's redirected pipes.
	si.Flags = windows.STARTF_USESTDHANDLES
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{bin}, args...)))
	if err != nil {
		return fail(err)
	}
	var block []uint16
	for _, kv := range withoutTerminalHost(os.Environ()) {
		block = append(append(block, utf16.Encode([]rune(kv))...), 0)
	}
	block = append(block, 0)
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, line, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &block[0], nil, &si.StartupInfo, &pi); err != nil {
		return fail(err)
	}
	defer windows.CloseHandle(pi.Process)
	_ = windows.CloseHandle(pi.Thread)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	exited := make(chan struct{})
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		close(exited)
	}()
	select {
	case <-exited:
	case <-sig:
		closePC()
		<-exited
	}
	var code uint32
	if err := windows.GetExitCodeProcess(pi.Process, &code); err != nil {
		return fail(err)
	}
	closePC()
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
	}
	return int(code)
}
