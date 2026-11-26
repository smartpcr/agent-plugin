//go:build scenario

package scenarios

import (
	"os"
	"strings"
	"testing"
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

// withoutTerminalHost is environ without terminalHostEnv, for the binary itself.
func withoutTerminalHost(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, terminalHostEnv+"=") {
			out = append(out, kv)
		}
	}
	return out
}
