//go:build scenario

package scenarios

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A listening host prints a launch link only on its own terminal: host.launch
// gives it no print channel when standard error is not a character device, since a
// redirected stream persists the credential (launch/component.go terminal). The
// launcher captures output through pipes, so a scenario that reads a printed link
// runs the binary on a real terminal: the tree's root is a re-exec of this test
// binary acting as the terminal host, which starts the binary on a pseudo-terminal
// it owns (a pty on POSIX, a pseudoconsole on Windows) and copies what the
// terminal shows to its own standard error, where the launcher captures it. The
// credential still reaches only a terminal and the test that owns it.

// terminalHostEnv names the binary the terminal host runs; the host's arguments
// are the binary's.
const terminalHostEnv = "DSH_SCENARIO_TERMINAL_HOST"

// startDshOnTerminal is startDsh with bin on a terminal of its own: its standard
// error (on Windows all three streams) is the terminal, and what the terminal
// shows is in the host's Stderr. terminate reaches bin through the terminal host,
// which forwards the termination signal (on Windows it closes the pseudoconsole)
// and exits with bin's code; pid is the terminal host's.
func startDshOnTerminal(t *testing.T, env []string, bin string, args ...string) *dshHost {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return startDsh(t, append(append([]string(nil), env...), terminalHostEnv+"="+bin), self, args...)
}

// terminalInputEnv names the interactive terminal host's input channel: a file
// whose appended bytes the host types into the terminal. With it set, the
// terminal is bin's controlling terminal (on POSIX bin leads a session of its
// own whose controlling terminal is the pty; on Windows the pseudoconsole is
// bin's console) and bin's standard output stays the host's, apart from the
// terminal.
const terminalInputEnv = "DSH_SCENARIO_TERMINAL_INPUT"

// startDshOnInteractiveTerminal is startDshOnTerminal with bin's controlling
// terminal owned by the scenario: type sends keys to it as typed ("\r" is
// Enter, "\x03" is Ctrl-C), what it shows is in the host's Stderr and bin's
// standard output, the result stream, is in the host's Stdout alone.
func startDshOnInteractiveTerminal(t *testing.T, env []string, bin string, args ...string) (h *dshHost, typeKeys func(keys string)) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "terminal-input")
	if err := os.WriteFile(in, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h = startDshOnTerminal(t, append(append([]string(nil), env...), terminalInputEnv+"="+in), bin, args...)
	return h, func(keys string) {
		t.Helper()
		f, err := os.OpenFile(in, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			_, err = f.WriteString(keys)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			t.Fatalf("type %q on the terminal: %v", keys, err)
		}
	}
}

// feedTerminal copies what is appended to the file at path into w, the
// terminal's input, until done is closed or w fails.
func feedTerminal(path string, w io.Writer, done <-chan struct{}) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminal host:", err)
		return
	}
	defer f.Close()
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			continue
		}
		if err != nil && err != io.EOF {
			return
		}
		select {
		case <-done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// withoutTerminalHost is environ without terminalHostEnv and terminalInputEnv,
// for the binary itself.
func withoutTerminalHost(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, terminalHostEnv+"=") && !strings.HasPrefix(kv, terminalInputEnv+"=") {
			out = append(out, kv)
		}
	}
	return out
}
