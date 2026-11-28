//go:build scenario

package scenarios

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The Landlock stage scenarios of docs/harness/libs/node-addon-system.md
// (DH-E-NATIVESYSTEM-1 to -4; e2e-scenarios.md Phase 9), on the real machines of
// DH-E-SANDBOX-1 (sandbox_remote_test.go) and its Landlock variants. The stage is
// reached only through the product: a workspace-write session's bash tool, whose
// result carries the confinement the start recorded.

// native1Planted is a credential-looking variable set on the host process; the
// scrubbed environment of a confined command must not carry it.
const native1Planted = "ANTHROPIC_API_KEY=dsh-scenario-planted-key"

// native1 runs the checks of DH-E-NATIVESYSTEM-1 on r and returns their record:
// one line per command, "<exit kind>, <state>, <backend>", in order.
func native1(t *testing.T, in sandboxInputs, r *remoteDsh) []string {
	t.Helper()
	setuid := need(t, "DSH_SCENARIO_NATIVESYSTEM1_SETUID", "a command template over {path} that runs a set-user-ID helper present on every Landlock machine and has it try to write {path}")
	r.mustVerdict(t, "landlock", "Full")
	m := startScriptedModel(t)
	h := r.sdk(t, r.bin, r.layer(t, in, m, "any", "native1"), m, native1Planted)
	d := r.outside + "/D"
	cmds := []string{
		"printf ok > '" + r.ws + "/ok'",
		"printf x > '" + d + "'",
		"sh -c \"printf x > '" + d + "'\"",
		expand(setuid, map[string]string{"path": d}),
		// Landlock denies ptrace access to processes outside the domain, so
		// /proc/$PPID/exe is unreadable here; comm needs no ptrace check.
		"echo pid=$$; echo parent=$(cat /proc/$PPID/comm); env; grep SigIgn /proc/self/status",
	}
	got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "workspace-write"), cmds...)
	var rec []string
	for i, c := range cmds {
		res, ok := shellResult(t, got[i])
		if !ok || res.Outcome != "exited" {
			t.Errorf("%q did not run to an exit: %s", c, got[i].Raw)
			rec = append(rec, "no exit")
			continue
		}
		if res.Confinement.State != "full" || res.Confinement.Backend != "landlock" {
			t.Errorf("%q recorded %s; want confined, landlock, full", c, res.record())
		}
		kind := "ok"
		if res.ExitCode != 0 {
			kind = "failed"
		}
		if want := map[bool]string{true: "ok", false: "failed"}[i == 0 || i == 4]; kind != want {
			t.Errorf("%q %s (exit %d), want %s: %s", c, kind, res.ExitCode, want, got[i].Raw)
		}
		rec = append(rec, fmt.Sprintf("%s, %s, %s", kind, res.Confinement.State, res.Confinement.Backend))
		if i == 1 {
			t.Logf("%s: denied write %s; stderr %q", r.alias, res.record(), res.Stderr.Tail)
		}
		if i == 4 {
			native1Process(t, r, h, m, in, res.Stdout.Tail)
		}
	}
	if r.exists(t, d) {
		t.Errorf("a confined command made %s outside the write root", d)
	}
	if !r.exists(t, r.ws+"/ok") {
		t.Errorf("the write of %s/ok did not land", r.ws)
	}
	return rec
}

// native1Process checks the process report of command (5): the command is the
// process the seam started (its parent is the host itself, no wrapper between),
// its environment is the scrubbed one, and its ignored signals equal those of a
// direct (unconfined) start of the same command.
func native1Process(t *testing.T, r *remoteDsh, h *sdkHost, m *scriptedModel, in sandboxInputs, report string) {
	t.Helper()
	if comm := hostComm(r.bin); !slices.Contains(strings.Split(report, "\n"), "parent="+comm) {
		t.Errorf("the confined command's parent is not the host %s (comm %q; a wrapper stayed between): %q", r.bin, comm, report)
	}
	if strings.Contains(report, "dsh-scenario-planted-key") {
		t.Errorf("the confined command's environment carries the planted credential: %q", report)
	}
	cmd := "grep SigIgn /proc/self/status"
	direct := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "danger-full-access"), cmd)
	res := mustExit(t, direct[0], cmd, 0)
	want, got := sigIgn(res.Stdout.Tail), sigIgn(report)
	t.Logf("%s: ignored signals confined %q, direct %q", r.alias, got, want)
	if want == "" || got != want {
		t.Errorf("ignored signals confined %q, direct start %q; want equal", got, want)
	}
}

// hostComm is the kernel's comm of a process executing bin: the base name of
// the executed file, cut to TASK_COMM_LEN-1 (15) bytes.
func hostComm(bin string) string {
	c := bin[strings.LastIndex(bin, "/")+1:]
	if len(c) > 15 {
		c = c[:15]
	}
	return c
}

func sigIgn(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "SigIgn:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TestNativeSystemLandlockConfinesOnCurrentKernel is the DH-E-NATIVESYSTEM-1
// scenario: on the landlock machine (Landlock ABI >= 5, no bwrap) the pure-Go
// release build confines a shell, its child and a set-user-ID helper.
//
// Given the CGO_ENABLED=0 binary, a workspace W and a path D outside it,
// When a workspace-write session runs (1) a write of W/ok, (2) a write of D, (3)
// a shell starting a shell that writes D, (4) a set-user-ID helper writing D and
// (5) a report of the process, its environment and its ignored signals,
// Then (1) succeeds, (2) to (4) fail and D does not exist, each start records
// "confined, landlock, full", the command's parent is the host itself, the
// planted credential is absent and the ignored signals equal a direct start's.
//
// Inputs: the landlock= machine of DSH_SCENARIO_SANDBOX1_MACHINES, the layer
// inputs of sandbox_remote_test.go and DSH_SCENARIO_NATIVESYSTEM1_SETUID.
//
//dsh:scenario DH-E-NATIVESYSTEM-1
func TestNativeSystemLandlockConfinesOnCurrentKernel(t *testing.T) {
	in := readSandboxInputs(t)
	r := prepareRemote(t, sandboxMachine(t, "landlock"), pureGo)
	t.Logf("record: %q", native1(t, in, r))
}

// TestNativeSystemOlderKernelReportsPartial is the DH-E-NATIVESYSTEM-2
// scenario: on a machine whose kernel's Landlock ABI is 1 to 4 every start is
// recorded partial, still confines, and a child's own result is never read as a
// launcher failure.
//
// Given the pure-Go binary on the landlock-abi1-4 machine and a workspace W,
// When commands exit 0, 1, 2 and 125, one prints "landlock-run: fatal" and "dsh
// sandbox-launch: failed" to stderr and exits 1, and one writes outside W,
// Then every start records "confined, landlock, partial" naming what is missing,
// each exit status and stderr text reach the caller unchanged as the command's
// own, and the outside write fails and leaves no file.
//
// Inputs: the landlock-abi1-4= machine of DSH_SCENARIO_SANDBOX1_MACHINES and the
// layer inputs of sandbox_remote_test.go; the kernel version is logged.
//
//dsh:scenario DH-E-NATIVESYSTEM-2
func TestNativeSystemOlderKernelReportsPartial(t *testing.T) {
	in := readSandboxInputs(t)
	r := prepareRemote(t, sandboxMachine(t, "landlock-abi1-4"), pureGo)
	r.mustVerdict(t, "landlock", "Partial")
	m := startScriptedModel(t)
	h := r.sdk(t, r.bin, r.layer(t, in, m, "any", "native2"), m)
	outside := r.outside + "/written"
	fatal := "echo 'landlock-run: fatal' >&2; echo 'dsh sandbox-launch: failed' >&2; exit 1"
	cmds := []string{"exit 0", "exit 1", "exit 2", "exit 125", fatal, "printf x > '" + outside + "'"}
	got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "workspace-write"), cmds...)
	for i, c := range cmds {
		code := []int{0, 1, 2, 125, 1, -1}[i]
		res, ok := shellResult(t, got[i])
		if !ok || res.Outcome != "exited" {
			t.Errorf("%q was not reported as the command's own exit: %s", c, got[i].Raw)
			continue
		}
		if code >= 0 && res.ExitCode != code {
			t.Errorf("%q exited %d, want %d unchanged", c, res.ExitCode, code)
		}
		if code < 0 && res.ExitCode == 0 {
			t.Errorf("%q succeeded outside the write root", c)
		}
		if res.Confinement.State != "partial" || res.Confinement.Backend != "landlock" || len(res.Confinement.Missing) == 0 {
			t.Errorf("%q recorded %s; want confined, landlock, partial with the missing effects named", c, res.record())
		}
		if c == fatal && (!strings.Contains(res.Stderr.Tail, "landlock-run: fatal") || !strings.Contains(res.Stderr.Tail, "dsh sandbox-launch: failed")) {
			t.Errorf("the command's stderr did not reach the caller unchanged: %q", res.Stderr.Tail)
		}
		if code < 0 {
			t.Logf("%s: denied write %s; stderr %q", r.alias, res.record(), res.Stderr.Tail)
		}
	}
	if r.exists(t, outside) {
		t.Errorf("the outside write left %s", outside)
	}
}

// TestNativeSystemNoLandlockRefusesBeforeExec is the DH-E-NATIVESYSTEM-3
// scenario: without a usable Landlock, or with a grant path that does not exist,
// the start is refused with its reason and the command never runs.
//
// Given (a) the unconfined machine (Landlock disabled at boot) and (b) the
// landlock machine with an extra write root that does not exist, and a marker M,
// When status runs and a workspace-write command that would create M starts
// under require: full,
// Then on (a) status reports the Landlock backend unusable; on both the start is
// refused with an error naming the reason, no confined record is reported and M
// does not exist.
//
// Inputs: the unconfined= and landlock= machines of
// DSH_SCENARIO_SANDBOX1_MACHINES and the layer inputs of sandbox_remote_test.go.
//
//dsh:scenario DH-E-NATIVESYSTEM-3
func TestNativeSystemNoLandlockRefusesBeforeExec(t *testing.T) {
	in := readSandboxInputs(t)
	for _, c := range []struct{ role, name string }{{"unconfined", "disabled"}, {"landlock", "missing-grant"}} {
		t.Run(c.name, func(t *testing.T) {
			r := prepareRemote(t, sandboxMachine(t, c.role), pureGo)
			if c.role == "unconfined" {
				st := r.status(t)
				t.Logf("%s: enforcement %s", r.alias, readVerdict(st))
				mustStatusUnconfined(t, st)
			} else {
				r.must(t, "remove the extra write root", "rmdir '"+r.extra+"'")
			}
			m := startScriptedModel(t)
			h := r.sdk(t, r.bin, r.layer(t, in, m, "full", "native3"), m)
			marker := r.ws + "/M"
			cmd := "touch '" + marker + "'"
			got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "workspace-write"), cmd)
			mustRefuse(t, got[0], cmd, "sandbox/unavailable", "sandbox/enforcement-insufficient", "sandbox/invalid-root", "sandbox/launch-failed")
			if r.exists(t, marker) {
				t.Errorf("the refused command made %s", marker)
			}
			t.Logf("%s: refused %s", c.name, got[0].Raw)
		})
	}
}

// TestNativeSystemSameInBuildModesAndOnMusl is the DH-E-NATIVESYSTEM-4
// scenario: the checks of DH-E-NATIVESYSTEM-1 give the same record with the
// pure-Go release build and a cgo build with the race detector on a glibc
// machine, and with the pure-Go build on a musl machine.
//
// Given the landlock machine, the musl machine and two builds of one commit,
// When NATIVESYSTEM-1's checks run with each build on the landlock machine and
// with the pure-Go build on the musl machine,
// Then the three records are equal and the pure-Go binary starts on musl with
// no added library.
//
// Inputs: the landlock= and musl= machines of DSH_SCENARIO_SANDBOX1_MACHINES and
// the inputs of DH-E-NATIVESYSTEM-1. The race build needs a Linux controller of
// the landlock machine's architecture with a C toolchain.
//
//dsh:scenario DH-E-NATIVESYSTEM-4
func TestNativeSystemSameInBuildModesAndOnMusl(t *testing.T) {
	in := readSandboxInputs(t)
	records := map[string][]string{}
	t.Run("pure-go", func(t *testing.T) {
		records["pure-go"] = native1(t, in, prepareRemote(t, sandboxMachine(t, "landlock"), pureGo))
	})
	t.Run("cgo-race", func(t *testing.T) {
		race := func(t *testing.T, arch string) string {
			t.Helper()
			if runtime.GOOS != "linux" || runtime.GOARCH != arch {
				t.Fatalf("the cgo -race build needs a linux/%s controller; this is %s/%s", arch, runtime.GOOS, runtime.GOARCH)
			}
			t.Setenv("CGO_ENABLED", "1")
			t.Setenv("GOFLAGS", "-race")
			return dshBinary(t)
		}
		records["cgo-race"] = native1(t, in, prepareRemote(t, sandboxMachine(t, "landlock"), race))
	})
	t.Run("musl", func(t *testing.T) {
		r := prepareRemote(t, sandboxMachine(t, "musl"), pureGo)
		if out, err := r.ssh(t, "ldd '"+r.bin+"' 2>&1", ""); err == nil && !strings.Contains(out, "not a dynamic") && !strings.Contains(out, "statically") {
			t.Errorf("the pure-Go binary links libraries on musl:\n%s", out)
		}
		records["musl"] = native1(t, in, r)
	})
	base := records["pure-go"]
	for _, k := range []string{"cgo-race", "musl"} {
		if got := records[k]; base == nil || !slices.Equal(got, base) {
			t.Errorf("record of %s %q, pure-go %q; want equal", k, got, base)
		}
	}
}
