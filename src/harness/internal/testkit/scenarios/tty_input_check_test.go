//go:build scenario

package scenarios

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"testing"
	"time"
)

// terminalProbeEnv makes this test binary the probe the interactive terminal
// host runs: it reads its answer as headless does (internal/cli/headless
// tty_*.go), from the controlling terminal and not standard input.
const terminalProbeEnv = "DSH_SCENARIO_TERMINAL_PROBE"

func init() {
	if os.Getenv(terminalProbeEnv) != "" && os.Getenv(terminalHostEnv) == "" {
		os.Exit(terminalProbe())
	}
}

// terminalProbe asks on standard error, reads one line from the controlling
// terminal and writes it to standard output, then exits 130 on Ctrl-C (3 when
// none comes); 125 is no controlling terminal.
func terminalProbe() int {
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	tty, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe: no terminal:", err)
		return 125
	}
	fmt.Fprint(os.Stderr, "probe: approve? [y] allow once, [n] deny: ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	fmt.Fprintf(os.Stdout, "answer=%s\n", strings.TrimSpace(line))
	fmt.Fprintln(os.Stderr, "probe: waiting for ctrl-c")
	select {
	case <-sig:
		return 130
	case <-time.After(time.Minute):
		return 3
	}
}

// The interactive terminal host gives its binary a controlling terminal the
// scenario types into: the answer is read from that terminal, a typed Ctrl-C
// interrupts the binary (on POSIX), and standard output holds only what the binary wrote
// there, the terminal's prompt kept apart.
func TestInteractiveTerminalTakesTypedInput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h, typeKeys := startDshOnInteractiveTerminal(t, []string{terminalProbeEnv + "=1"}, self)
	shows := func(text string) func(string) string {
		return func(string) string {
			if strings.Contains(plainText(h.Stderr.String()), text) {
				return text
			}
			return ""
		}
	}
	h.waitFor(t, time.Minute, "the probe's prompt", shows("approve?"))
	typeKeys("y\r")
	h.waitFor(t, time.Minute, "the probe's wait for ctrl-c", shows("waiting for ctrl-c"))
	// A pseudoconsole turns no typed Ctrl-C into a console event for its
	// processes, so on Windows the probe is ended by the termination signal,
	// closing the pseudoconsole, which also ends it (STATUS_CONTROL_C_EXIT).
	want := 130
	if runtime.GOOS == "windows" {
		want = 0xC000013A
		if err := terminate(t, h); err != nil {
			t.Fatal(err)
		}
	} else {
		typeKeys("\x03")
	}
	if !h.await(time.Minute) {
		t.Fatalf("the probe did not exit:\n%s", h.output())
	}
	if h.code() != want {
		t.Errorf("the probe exited %d, want %d:\n%s", h.code(), want, h.output())
	}
	if got := strings.TrimSpace(h.Stdout.String()); got != "answer=y" {
		t.Errorf("standard output holds %q, want the typed answer only", got)
	}
}
