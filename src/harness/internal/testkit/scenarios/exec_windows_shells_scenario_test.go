//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestExecWindowsShellsQuoteAndResolve is the DH-E-EXEC-7 scenario
// (docs/harness/areas/execution.md; tech-spec sec. 4.5: authored in DH-S-13, run
// in DH-S-39): on Windows every command string reaches the real shells as
// written, a batch file starts only when allowed and with plain arguments, and
// bash never falls back to WSL.
//
// Given Windows with pwsh 7, Windows PowerShell 5.1, Git for Windows, WSL and
// Node's npm.cmd on PATH,
// When a table of strings (spaces, double and single quotes, trailing
// backslashes, %NAME%, &, ^, non-ASCII, and one of 40,000 characters) is printed
// back through the pwsh tool on each edition and the bash tool on Git's bash;
// the bash tool runs with no Git for Windows it can find (an empty %ProgramFiles%
// and a PATH holding no bash but the WSL launcher's; see withoutBash); npm is started by bare name
// through the process package by default, with opt-in and a plain argument and
// with opt-in and a % argument; and non-ASCII text is written to the standard
// input of Windows PowerShell 5.1 started after its input code page is set to
// UTF-8,
// Then every string within the limit prints back exactly through all three
// shells and the over-long one is refused with no command run; without Git the
// bash call answers unavailable and names no WSL program; npm is refused with
// subprocess/batch-refused, runs with opt-in, and is refused with
// subprocess/bad-argument for the % argument. Whether the 5.1 input arrived
// intact is logged for debt L:pwsh-local.7 and asserts nothing.
//
// Inputs: DSH_SCENARIO_EXEC7_LAYER, a layer over {endpoint}, {shell} and
// {program} registering DSH_SCENARIO_EXEC7_ROUTE on {endpoint} with a
// scenario-local key and enabling the {shell} tool (bash or pwsh) on {program}
// (empty: resolved on PATH); DSH_SCENARIO_EXEC7_MODEL; DSH_SCENARIO_EXEC7_PWSH7,
// DSH_SCENARIO_EXEC7_PWSH51 and DSH_SCENARIO_EXEC7_GITBASH, the three programs.
// Temp files are removed with the test's directories.
//
//dsh:scenario DH-E-EXEC-7
func TestExecWindowsShellsQuoteAndResolve(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Fatalf("DH-E-EXEC-7 runs on Windows, not %s", runtime.GOOS)
	}
	layerT := need(t, "DSH_SCENARIO_EXEC7_LAYER", "a layer over {endpoint}, {shell} and {program} enabling the {shell} tool on {program}")
	route := &protocol.ModelRoute{
		Provider: need(t, "DSH_SCENARIO_EXEC7_ROUTE", "the route the layer registers on the scripted endpoint"),
		Model:    need(t, "DSH_SCENARIO_EXEC7_MODEL", "a model of that route"),
	}
	bin := dshBinary(t)
	start := func(t *testing.T, shell, program string, env []string) (*sdkHost, *scriptedModel, string) {
		m := startScriptedModel(t)
		layer := writeLayer(t, expand(layerT, map[string]string{"endpoint": m.URL, "shell": shell, "program": program}))
		h := startSDK(t, bin, env, []string{"--layer", layer})
		return h, m, h.createIn(t, route, t.TempDir(), "danger-full-access")
	}
	table := []string{
		"a b  c", `say "hi" to "them"`, "it's 'quoted'", `C:\dir with space\`, `trail\\`,
		"%PATH% and %USERPROFILE%", "a & b && c", "a ^ b ^^ c", "héllo wörld 日本語 ✓",
	}
	long := strings.Repeat("x", 40000)
	shells := []struct{ name, tool, program string }{
		{"pwsh7", "pwsh", need(t, "DSH_SCENARIO_EXEC7_PWSH7", "the pwsh 7 program")},
		{"pwsh51", "pwsh", need(t, "DSH_SCENARIO_EXEC7_PWSH51", "the Windows PowerShell 5.1 program")},
		{"gitbash", "bash", need(t, "DSH_SCENARIO_EXEC7_GITBASH", "Git for Windows' bash.exe")},
	}
	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			h, m, session := start(t, sh.tool, sh.program, nil)
			for _, s := range append(table, long) {
				m.script(scriptedCall{Name: sh.tool, Args: map[string]any{"command": echoBack(sh.tool, s)}, Tag: s[:min(len(s), 16)]})
			}
			got := toolOutcomes(t, h.runPrompt(t, session, "Run the scripted commands.", 10*time.Minute))
			if n := m.pending(); n != 0 || len(got) != len(table)+1 {
				t.Fatalf("the turn made %d of %d scripted calls (%d not made)", len(got), len(table)+1, n)
			}
			for i, s := range table {
				res := mustExit(t, got[i], echoBack(sh.tool, s), 0)
				if res.Stdout.Tail != s {
					t.Errorf("%s printed %q back as %q", sh.name, s, res.Stdout.Tail)
				}
			}
			if res, ok := shellResult(t, got[len(table)]); (ok && res.Outcome == "exited") || !got[len(table)].IsError {
				t.Errorf("the 40,000-character command was not refused before it ran: %.400s", got[len(table)].Raw)
			}
		})
	}
	t.Run("bash-without-git", func(t *testing.T) {
		env := withoutBash(t, t.TempDir())
		h, m, session := start(t, "bash", "", env)
		m.script(bashCall("echo reached"))
		got := toolOutcomes(t, h.runPrompt(t, session, "Run the scripted command.", 10*time.Minute))
		if len(got) != 1 {
			t.Fatalf("the turn made %d calls; want the one scripted bash call", len(got))
		}
		raw := strings.ToLower(string(got[0].Raw))
		if res, ok := shellResult(t, got[0]); (ok && res.Outcome == "exited") || !strings.Contains(raw, "unavailable") {
			t.Errorf("bash without Git for Windows did not answer unavailable: %s", got[0].Raw)
		}
		if strings.Contains(raw, "wsl") || strings.Contains(raw, `system32\\bash.exe`) {
			t.Errorf("bash without Git for Windows named the WSL launcher: %s", got[0].Raw)
		}
	})
	t.Run("npm", func(t *testing.T) {
		env := subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{})
		prog, err := subprocess.LookupProgram("npm", env)
		if err != nil || !prog.Batch {
			t.Fatalf("npm resolved to %+v, %v; want npm.cmd, a batch file", prog, err)
		}
		run := func(policy subprocess.BatchPolicy, arg string) (subprocess.Result, error) {
			return subprocess.Run(t.Context(), subprocess.Spec{
				Argv: []string{prog.Path, arg}, Env: env, Batch: policy,
				Launch: sandbox.Trusted("DH-E-EXEC-7 starts npm --version from the scenario"),
			}, subprocess.RunOptions{Timeout: 2 * time.Minute})
		}
		if res, err := run(subprocess.Refuse, "--version"); !herr.Has(err, subprocess.ReasonBatchRefused) || res.Started {
			t.Errorf("npm by default: started %v, %v; want subprocess/batch-refused", res.Started, err)
		}
		if res, err := run(subprocess.AllowChecked, "--version"); err != nil || res.ExitCode != 0 {
			t.Errorf("npm --version with opt-in: exit %d, %v\n%s", res.ExitCode, err, res.Stderr.Bytes)
		}
		if res, err := run(subprocess.AllowChecked, "--version%PATH%"); !herr.Has(err, subprocess.ReasonBadArgument) || res.Started {
			t.Errorf("npm with a %% argument: started %v, %v; want subprocess/bad-argument", res.Started, err)
		}
	})
	t.Run("pwsh51-stdin", func(t *testing.T) {
		const text = "héllo wörld 日本語 ✓"
		cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		line := `chcp 65001 >nul & "` + shells[1].program + `" -NoProfile -NonInteractive -Command "[Console]::OutputEncoding=[Text.Encoding]::UTF8; [Console]::Out.Write([Console]::In.ReadToEnd())"`
		res, err := subprocess.Run(t.Context(), subprocess.Spec{
			Argv: []string{cmd, "/d", "/s", "/c", line}, Env: subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{}),
			Stdin:  subprocess.StdinData([]byte(text)),
			Launch: sandbox.Trusted("DH-E-EXEC-7 reads Windows PowerShell 5.1's standard input back"),
		}, subprocess.RunOptions{Timeout: 2 * time.Minute})
		if err != nil {
			t.Fatalf("Windows PowerShell 5.1 with the UTF-8 input code page: %v\n%s", err, res.Stderr.Bytes)
		}
		got := strings.TrimRight(string(res.Stdout.Bytes), "\r\n")
		t.Logf("L:pwsh-local.7: 5.1 standard input after chcp 65001 arrived intact: %v (%q, want %q)", got == text, got, text)
	})
}

// echoBack is a command of tool's dialect that writes s and nothing else.
func echoBack(tool, s string) string {
	if tool == "pwsh" {
		return "[Console]::Out.Write('" + strings.ReplaceAll(s, "'", "''") + "')"
	}
	return "printf '%s' '" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// withoutBash is the environment entries under which the bash tool can find no
// bash but the WSL launcher, without uninstalling anything: every source bash.Find
// consults on Windows is closed. %ProgramFiles% (and %ProgramW6432%) name empty,
// whose Git\bin\bash.exe is absent, and PATH loses every directory holding a bash
// program except %SystemRoot%\System32 and WindowsApps, the WSL launcher's, which
// stay so that falling back to WSL is observable. The fixture is checked with
// bash.Find over the same environment before it is used: a bash it still resolves
// fails the scenario as a fixture fault, not as a product result.
func withoutBash(t *testing.T, empty string) []string {
	t.Helper()
	launcherDir := func(d string) bool {
		d = strings.ToUpper(filepath.Clean(d))
		if root := os.Getenv("SystemRoot"); root != "" && d == strings.ToUpper(filepath.Join(root, "System32")) {
			return true
		}
		return slices.Contains(strings.Split(d, `\`), "WINDOWSAPPS")
	}
	holdsBash := func(d string) string {
		for _, ext := range []string{"", ".exe", ".com", ".bat", ".cmd"} {
			if p := filepath.Join(d, "bash"+ext); fileExists(p) {
				return p
			}
		}
		return ""
	}
	var keep []string
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" && (launcherDir(d) || holdsBash(d) == "") {
			keep = append(keep, d)
		}
	}
	entries := []string{"PATH=" + strings.Join(keep, string(os.PathListSeparator)), "ProgramFiles=" + empty, "ProgramW6432=" + empty}
	host := shellexec.LocalHost(subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Entries: entries}))
	if p, err := bash.Find("", host); !herr.Has(err, shellexec.ReasonNotFound) {
		t.Fatalf("fixture: bash still resolves to %+v (%v) with Git for Windows hidden", p, err)
	}
	return entries
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
