//go:build scenario

package scenarios

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// TestOneLaunchCommandAcrossStops is the DH-E-CLI-1 scenario
// (docs/harness/areas/app-cli.md; DH-D-CLI-2, -4, -5; tech-spec sec. 4.5:
// authored in DH-S-16, run in DH-S-27 step 27.8, guarded: a stop can lose work):
// one launch command, a second launch, Ctrl-C and a crash, on Linux, macOS and
// Windows.
//
// Given the built dsh on a machine with a real default browser and a terminal the
// scenario drives, a scratch home whose web profile layer (DSH_SCENARIO_CLI1_LAYER)
// selects the replay provider, and a fixture session whose turn runs a shell
// command sleeping two minutes (DSH_SCENARIO_CLI1_TURN starts it),
// When (1) dsh web runs; (2) dsh web runs again, then with --tls-cert a --tls-key b,
// then with --port 0; (3) the sleeping turn starts and Ctrl-C is pressed once,
// then twice after the window; (4) the host and turn start again and the host is
// sent the termination signal; (5) the host and turn start again, the host is
// killed, then dsh web, dsh status and dsh --safe web run,
// Then (1) the terminal shows exactly one launch link and the browser the UI; (2)
// the second run opens a tab, prints no link and exits 0, the third exits 6
// naming the running host's pid, the fourth is a second host; (3) one press stops
// nothing, the double press ends the host with 130 and the log holds turn/end
// with the interrupt cause; (4) the host exits 0 in its deadline; (5) the next
// start prints the unclean-exit line and the safe-mode hint, the open tab is not
// served meanwhile, the session's interrupted turn is closed, safe mode reaches
// the UI, and no host remains.
// Cleanup: dsh stop --yes; the home is removed by scenario.Run.
//
// Inputs: DSH_SCENARIO_CLI1_LAYER, a layer template file ({workspace});
// DSH_SCENARIO_CLI1_BROWSER, the browser driver (host_web_test.go browserState)
// over the browser "default"; DSH_SCENARIO_CLI1_TURN, a command over {home} and
// {origin} that starts the fixture session's sleeping turn in the running host
// and prints the session id last; on Windows DSH_SCENARIO_TERMINAL_KEYS, a
// command over {pid} (the terminal host) and {keys} ("ctrl-c", "ctrl-c ctrl-c")
// pressing them on its terminal.
//
// Every web host runs on the scenario's own terminal
// (startDshOnInteractiveTerminal), dsh's controlling terminal, so on POSIX step
// (3) types Ctrl-C into it and the pty raises SIGINT for dsh. A pseudoconsole
// raises no console event for a typed Ctrl-C, so on Windows the operator command
// presses it.
//
//dsh:scenario DH-E-CLI-1
func TestOneLaunchCommandAcrossStops(t *testing.T) {
	inputs := []string{"DSH_SCENARIO_CLI1_LAYER", "DSH_SCENARIO_CLI1_BROWSER", "DSH_SCENARIO_CLI1_TURN"}
	if runtime.GOOS == "windows" {
		inputs = append(inputs, "DSH_SCENARIO_TERMINAL_KEYS")
	}
	scenario.Run(t, "DH-E-CLI-1", scenario.Needs{
		Bin: true, Deadline: 45 * time.Minute, Env: inputs,
	}, func(s *scenario.S) {
		profileLayer(t, s.Home, "web", layerInput(t, "DSH_SCENARIO_CLI1_LAYER", "a replay layer", map[string]string{"workspace": s.Workspace}))
		browser := newBrowserDriver(t, "DSH_SCENARIO_CLI1_BROWSER")
		env := []string{"DSH_HOME=" + s.Home}
		typers := map[*dshHost]func(string){}
		web := func(args ...string) (*dshHost, string) {
			h, k := startDshOnInteractiveTerminal(t, env, s.Bin, append([]string{"--home", s.Home}, append(args, "web")...)...)
			typers[h] = k
			return h, h.waitFor(t, 2*time.Minute, "a launch link", func(out string) string { return launchLink.FindString(plainText(out)) })
		}
		turn := func(link string) string {
			return lastLine(operatorStep(t, "DSH_SCENARIO_CLI1_TURN", "starts the sleeping turn", map[string]string{"home": s.Home, "origin": originOf(link)}))
		}
		// ctrlC presses Ctrl-C presses times in a row on h's terminal.

		ctrlC := func(h *dshHost, presses int) {
			if runtime.GOOS == "windows" {
				operatorStep(t, "DSH_SCENARIO_TERMINAL_KEYS", "types keys into a terminal", map[string]string{"pid": strconv.Itoa(h.Process.Pid()), "keys": strings.TrimSpace(strings.Repeat("ctrl-c ", presses))})
				return
			}
			typers[h](strings.Repeat("\x03", presses))
		}
		s.Cleanup(func() { _, _ = launcher.Run(t, time.Minute, s.Program(env, "--home", s.Home, "stop", "--yes")) })

		h, link := web()
		if n := len(launchLink.FindAllString(plainText(h.output()), -1)); n != 1 {
			t.Errorf("(1) the terminal shows %d launch links, want 1", n)
		}
		tabs := browser.step(t, "default", "tabs", "")

		r := dshIn(t, s, time.Minute, env, "web")
		if r.code != 0 || launchLink.MatchString(r.stdout+r.stderr) {
			t.Errorf("(2) the second dsh web exited %d with output %q, want 0 and no link", r.code, r.stdout+r.stderr)
		}
		if got := browser.step(t, "default", "tabs", ""); len(got.Tabs) != len(tabs.Tabs)+1 {
			t.Errorf("(2) the browser has %d tabs after the second launch, want %d", len(got.Tabs), len(tabs.Tabs)+1)
		}
		pid := strconv.Itoa(oneRecord(t, s.Home).PID)
		if r := dshIn(t, s, time.Minute, env, "web", "--tls-cert", "a", "--tls-key", "b"); r.code != 6 || !strings.Contains(r.stdout+r.stderr, pid) {
			t.Errorf("(2) dsh web --tls-cert exited %d naming %q, want 6 naming pid %s", r.code, r.stdout+r.stderr, pid)
		}
		other, _ := web("--port", "0")
		if n := len(runtimeRecords(t, s.Home)); n != 2 {
			t.Errorf("(2) %d hosts run after dsh web --port 0, want 2", n)
		}
		if err := terminate(t, other); err != nil || !other.await(time.Minute) {
			t.Errorf("(2) the second host did not stop: %v", err)
		}

		session := turn(link)
		ctrlC(h, 1)
		if h.await(5 * time.Second) {
			t.Fatalf("(3) one Ctrl-C ended the host (exit %d)", h.code())
		}
		ctrlC(h, 2)
		if !h.await(30*time.Second) || h.code() != 130 {
			t.Errorf("(3) the double Ctrl-C left exit %d, want 130", h.code())
		}

		h, link = web()
		turn(link)
		if err := terminate(t, h); err != nil || !h.await(time.Minute) || h.code() != 0 {
			t.Errorf("(4) the terminated host exited %d (%v), want 0 in its deadline", h.code(), err)
		}

		h, link = web()
		turn(link)
		_ = h.Process.Kill()
		h.await(time.Minute)
		if st := browser.step(t, "default", "current", ""); st.Status == 200 {
			t.Errorf("(5) the open tab is still served after the kill: %s", st.URL)
		}
		h, _ = web()
		if out := strings.ToLower(plainText(h.output())); !strings.Contains(out, "unclean") || !strings.Contains(out, "--safe") {
			t.Errorf("(5) the start after the kill printed no unclean-exit line and safe-mode hint:\n%s", out)
		}
		if r := dshIn(t, s, time.Minute, env, "status"); r.code != 0 {
			t.Errorf("(5) dsh status exited %d:\n%s", r.code, r.stderr)
		}
		_ = terminate(t, h)
		h.await(time.Minute)
		safe, link := web("--safe")
		if st := browser.step(t, "default", "open", link); st.Status != 200 {
			t.Errorf("(5) safe mode did not reach the UI: %d", st.Status)
		}
		_ = terminate(t, safe)
		safe.await(time.Minute)

		var interrupted, closed bool
		for _, e := range ofType(storedHistory(t, s, s.Home, session), "turn/end") {
			var end wireTurnEnd
			decode(t, e, &end)
			interrupted = interrupted || strings.Contains(end.Reason.Cause, "interrupt")
			closed = closed || end.Reason.Kind == "interrupted"
		}
		if !interrupted || !closed {
			t.Errorf("the session log holds an interrupt-caused turn/end %t and a closed interrupted turn %t, want both", interrupted, closed)
		}
		if recs := runtimeRecords(t, s.Home); len(recs) != 0 {
			t.Errorf("a host remains: %+v", recs)
		}
	})
}
