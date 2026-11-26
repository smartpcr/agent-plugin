//go:build scenario

package scenarios

import (
	"slices"
	"strings"
	"testing"
)

// TestSandboxLinuxBackendsConfineShellOnRealMachines is the DH-E-SANDBOX-1
// scenario (e2e-scenarios.md Phase 9; docs/harness/areas/sandbox-ssh.md): on
// three real Linux machines -- (a) bwrap with unprivileged user namespaces, (b)
// no bwrap and Landlock ABI >= 5, (c) neither -- "dsh sandbox status" names the
// backend the machine has, and the shell tool's commands run confined by it.
//
// Given each machine with the binary built from this checkout, a workspace W
// that is a git root, an extra write root X and a directory O outside both,
// When a workspace-write session runs, through the bash tool, a write inside W
// and X, a write in O, the removal of a file beside W, a child shell writing O,
// "exit 0/1/2/125", a command printing "bwrap: fatal" and "dsh sandbox-launch:
// failed" to stderr and a listing of its open descriptors,
// Then on (a) every result reports bwrap and full; on (b) landlock with full or
// partial naming every missing effect; the outside writes and the removal fail
// and leave O and the file beside W unchanged; each exit code and the stderr
// text reach the caller as the command's own; the command holds descriptors 0,
// 1 and 2 only; on (c) status reports Unconfined and, under require: full, the
// start is refused with sandbox/unavailable or sandbox/enforcement-insufficient
// and the marker is never made; and with dsh itself inside a write root (b) the
// start is refused with sandbox/launcher-writable.
//
// Inputs: DSH_SCENARIO_SANDBOX1_MACHINES (bwrap=, landlock= and unconfined=
// items) and the layer inputs of sandbox_remote_test.go. The reported
// enforcement of each machine and the denial's stderr are logged for the
// evidence record (debt row sandbox.3).
//
//dsh:scenario DH-E-SANDBOX-1
func TestSandboxLinuxBackendsConfineShellOnRealMachines(t *testing.T) {
	in := readSandboxInputs(t)
	cases := []struct {
		role, status, backend string
		states                []string
	}{
		{"bwrap", "bwrap", "bwrap", []string{"full"}},
		{"landlock", "landlock", "landlock", []string{"full", "partial"}},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			r := prepareRemote(t, sandboxMachine(t, c.role), pureGo)
			if st := r.status(t); !strings.Contains(strings.ToLower(st), c.status) {
				t.Errorf("status on the %s machine does not name %s:\n%s", c.role, c.status, st)
			}
			sandbox1Confined(t, in, r, c.backend, c.states)
		})
	}
	t.Run("unconfined", func(t *testing.T) {
		r := prepareRemote(t, sandboxMachine(t, "unconfined"), pureGo)
		if st := r.status(t); !strings.Contains(st, "Unconfined") {
			t.Errorf("status on the unconfined machine does not read Unconfined:\n%s", st)
		}
		m := startScriptedModel(t)
		h := r.sdk(t, r.bin, r.layer(t, in, m, "full", "full"), m)
		marker := r.ws + "/M"
		cmd := "touch '" + marker + "'"
		got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "workspace-write"), cmd)
		mustRefuse(t, got[0], cmd, "sandbox/unavailable", "sandbox/enforcement-insufficient")
		if r.exists(t, marker) {
			t.Errorf("the refused command made %s", marker)
		}
		t.Logf("unconfined: refused %s", got[0].Raw)
	})
	t.Run("launcher-writable", func(t *testing.T) {
		r := prepareRemote(t, sandboxMachine(t, "landlock"), pureGo)
		inside := r.ws + "/dsh"
		r.must(t, "copy dsh into the workspace", "cp '"+r.bin+"' '"+inside+"'")
		m := startScriptedModel(t)
		h := r.sdk(t, inside, r.layer(t, in, m, "any", "writable"), m)
		marker := r.ws + "/M"
		cmd := "touch '" + marker + "'"
		got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "workspace-write"), cmd)
		mustRefuse(t, got[0], cmd, "sandbox/launcher-writable")
		if r.exists(t, marker) {
			t.Errorf("the refused command made %s", marker)
		}
	})
}

// sandbox1Confined runs the confined commands of DH-E-SANDBOX-1 on r and checks
// each result reports backend in one of states.
func sandbox1Confined(t *testing.T, in sandboxInputs, r *remoteDsh, backend string, states []string) {
	t.Helper()
	m := startScriptedModel(t)
	h := r.sdk(t, r.bin, r.layer(t, in, m, "any", "confined"), m)
	session := h.createIn(t, in.route, r.ws, "workspace-write")
	o := r.outside
	type step struct {
		cmd  string
		exit int // -1: any non-zero
	}
	steps := []step{
		{"printf ok > '" + r.ws + "/ok' && printf ok > '" + r.extra + "/ok'", 0},
		{"printf x > '" + o + "/direct'", -1},
		{"rm -f '" + r.beside + "'", -1},
		{"sh -c \"printf x > '" + o + "/child'\"", -1},
		{"exit 0", 0},
		{"exit 1", 1},
		{"exit 2", 2},
		{"exit 125", 125},
		{"echo 'bwrap: fatal' >&2; echo 'dsh sandbox-launch: failed' >&2; exit 3", 3},
		{"ls /proc/self/fd", 0},
	}
	cmds := make([]string, len(steps))
	for i, s := range steps {
		cmds[i] = s.cmd
	}
	got := runCommands(t, h, m, session, cmds...)
	for i, s := range steps {
		res, ok := shellResult(t, got[i])
		if !ok || res.Outcome != "exited" {
			t.Errorf("%q did not run to an exit: %s", s.cmd, got[i].Raw)
			continue
		}
		switch {
		case s.exit >= 0 && res.ExitCode != s.exit:
			t.Errorf("%q exited %d, want %d: %s", s.cmd, res.ExitCode, s.exit, got[i].Raw)
		case s.exit < 0 && res.ExitCode == 0:
			t.Errorf("%q succeeded outside the write roots: %s", s.cmd, got[i].Raw)
		}
		c := res.Confinement
		if c.Backend != backend || !slices.Contains(states, c.State) {
			t.Errorf("%q reported %s; want backend %s in state %v", s.cmd, res.record(), backend, states)
		}
		if c.State == "partial" && len(c.Missing) == 0 {
			t.Errorf("%q reported partial without naming a missing effect", s.cmd)
		}
		if i == 1 {
			t.Logf("%s: enforcement %s; denial stderr %q", r.alias, res.record(), res.Stderr.Tail)
		}
		if i == 8 && (!strings.Contains(res.Stderr.Tail, "bwrap: fatal") || !strings.Contains(res.Stderr.Tail, "dsh sandbox-launch: failed")) {
			t.Errorf("the command's own stderr did not reach the caller: %q", res.Stderr.Tail)
		}
		if i == 9 {
			for _, fd := range strings.Fields(res.Stdout.Tail) {
				// 3 is the descriptor ls opens on /proc/self/fd itself.
				if fd != "0" && fd != "1" && fd != "2" && fd != "3" {
					t.Errorf("the command holds descriptor %s: %q", fd, res.Stdout.Tail)
				}
			}
		}
	}
	for _, p := range []string{o + "/direct", o + "/child"} {
		if r.exists(t, p) {
			t.Errorf("a confined command made %s outside the write roots", p)
		}
	}
	if !r.exists(t, r.beside) {
		t.Errorf("a confined command removed %s beside the workspace", r.beside)
	}
	if !r.exists(t, r.ws+"/ok") || !r.exists(t, r.extra+"/ok") {
		t.Error("the writes inside the write roots did not land")
	}
}
