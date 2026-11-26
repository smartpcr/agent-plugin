//go:build scenario

package scenarios

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestExecConfinedCommandNeverRunsWithoutBackend is the DH-E-EXEC-8 scenario
// (docs/harness/areas/execution.md; tech-spec sec. 4.5: authored in DH-S-13, run
// in DH-S-39): a confined one-shot command is confined, and once the backend is
// unusable neither a foreground nor a background call runs at all.
//
// Given one Linux machine per backend A10 ships (the bwrap= and landlock=
// machines of DH-E-SANDBOX-1), a read-only session and a marker outside the
// write roots,
// When the bash tool writes the marker; then the operator's break command makes
// the backend unusable and the same call is made in the foreground and as a
// background job; then, the backend restored, a command exits 125 printing
// launcher-like text,
// Then the marker is never created; the first result is a non-zero exit that
// carries the sandbox enforcement and a denial hint; with no backend both calls
// fail at start with no shell process on the machine; the last is an ordinary
// command that exited 125, its stderr its own.
//
// Inputs: DSH_SCENARIO_SANDBOX1_MACHINES and the layer inputs of
// sandbox_remote_test.go; DSH_SCENARIO_EXEC8_BREAK and DSH_SCENARIO_EXEC8_RESTORE,
// shell commands over {backend} that run on the machine (with sudo where the
// runbook grants it) to make the backend unusable and to restore it. The restore
// command must be idempotent and safe after a break that stopped part-way: it is
// registered before the break runs and, unless the scenario restored the backend
// itself, runs at cleanup whatever state the break left.
//
//dsh:scenario DH-E-EXEC-8
func TestExecConfinedCommandNeverRunsWithoutBackend(t *testing.T) {
	in := readSandboxInputs(t)
	brk := need(t, "DSH_SCENARIO_EXEC8_BREAK", "the command that makes {backend} unusable on its machine")
	restore := need(t, "DSH_SCENARIO_EXEC8_RESTORE", "the command that restores {backend} on its machine")
	for _, backend := range []string{"bwrap", "landlock"} {
		t.Run(backend, func(t *testing.T) {
			r := prepareRemote(t, sandboxMachine(t, backend), pureGo)
			vars := map[string]string{"backend": backend}
			marker := r.outside + "/M"
			write := "printf x > '" + marker + "'"
			m := startScriptedModel(t)
			h := r.sdk(t, r.bin, r.layer(t, in, m, "any", "exec8"), m)
			session := h.createIn(t, in.route, r.ws, "read-only")

			got := runCommands(t, h, m, session, write)
			res, ok := shellResult(t, got[0])
			if !ok || res.Outcome != "exited" || res.ExitCode == 0 {
				t.Fatalf("%q under read-only did not run to a failed exit: %s", write, got[0].Raw)
			}
			if res.Confinement.Backend != backend || res.Confinement.State == "" || res.Confinement.State == "unconfined" {
				t.Errorf("the denied write reported %s; want enforcement by %s", res.record(), backend)
			}
			if !strings.Contains(strings.ToLower(string(got[0].Raw)), "sandbox") {
				t.Errorf("the denied write carries no denial hint naming the sandbox: %s", got[0].Raw)
			}
			t.Logf("%s: denied write %s; stderr %q", backend, res.record(), res.Stderr.Tail)

			undo := expand(restore, vars)
			restored := false
			t.Cleanup(func() {
				if restored {
					return
				}
				// t.Context is already cancelled here, and a partial break still needs undoing.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if out, err := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", r.alias, undo).CombinedOutput(); err != nil {
					t.Errorf("restore %s on %s after the scenario stopped: %s: %v\n%s\nrestore it by hand before the next run", backend, r.alias, undo, err, out)
				}
			})
			r.must(t, "make "+backend+" unusable", expand(brk, vars))
			nonce := fmt.Sprintf("exec8-%s-%d", backend, os.Getpid())
			never := write + "; sleep 30 # " + nonce
			m.script(bashCall(never))
			m.script(scriptedCall{Name: "bash", Args: map[string]any{"command": never, "run_in_background": true}, Tag: "background"})
			broken := toolOutcomes(t, h.runPrompt(t, session, "Run the scripted commands.", 10*time.Minute))
			if n := m.pending(); n != 0 || len(broken) != 2 {
				t.Fatalf("the turn made %d of 2 scripted calls (%d not made)", len(broken), n)
			}
			for i, how := range []string{"foreground", "background"} {
				mustRefuse(t, broken[i], never+" ("+how+")", "sandbox/unavailable", "sandbox/launch-failed", "sandbox/exec-failed")
				if !strings.Contains(strings.ToLower(string(broken[i].Raw)), "never ran") {
					t.Errorf("the %s call with no backend does not say it never ran: %s", how, broken[i].Raw)
				}
			}
			if ps := r.must(t, "look for the shell", "pgrep -af '"+nonce+"' | grep -v pgrep || true"); strings.TrimSpace(ps) != "" {
				t.Errorf("a shell process was created with no backend:\n%s", ps)
			}

			r.must(t, "restore "+backend, undo)
			restored = true
			launcherLike := "echo 'dsh sandbox-launch: failed' >&2; exit 125"
			last := mustExit(t, runCommands(t, h, m, session, launcherLike)[0], launcherLike, 125)
			if !strings.Contains(last.Stderr.Tail, "dsh sandbox-launch: failed") || last.Error != "" {
				t.Errorf("exit 125 was not reported as the command's own: %+v", last)
			}
			if r.exists(t, marker) {
				t.Errorf("a confined command made %s", marker)
			}
		})
	}
}
