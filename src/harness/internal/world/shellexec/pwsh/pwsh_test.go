package pwsh_test

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/pwsh"
)

func TestMain(m *testing.M) {
	if code, ok := launch.ServeIfAsked(os.Args[1:], os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func fakeHost(goos string, env []string, files ...string) shellexec.Host {
	return shellexec.Host{GOOS: goos, Env: env, Exists: func(p string) bool { return slices.Contains(files, p) }}
}

// TestFindOrder: the configured path, then PowerShell 7's install, then pwsh
// on PATH, then Windows PowerShell 5.1 only when allowed; the edition is
// reported.
//
//dsh:test DH-T-EXEC-13
func TestFindOrder(t *testing.T) {
	win := []string{`ProgramFiles=C:\Program Files`, `SystemRoot=C:\Windows`, `Path=C:\tools`}
	const (
		install = `C:\Program Files\PowerShell\7\pwsh.exe`
		onPath  = `C:\tools\pwsh.exe`
		desktop = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	)
	for name, c := range map[string]struct {
		path    string
		allow   bool
		h       shellexec.Host
		want    string
		edition string
	}{
		"config 5.1": {desktop, false, fakeHost("windows", win, desktop, install), desktop, pwsh.Desktop},
		"install":    {"", true, fakeHost("windows", win, install, onPath, desktop), install, pwsh.Core},
		"path":       {"", true, fakeHost("windows", win, onPath, desktop), onPath, pwsh.Core},
		"5.1":        {"", true, fakeHost("windows", win, desktop), desktop, pwsh.Desktop},
		"linux":      {"", true, fakeHost("linux", []string{"PATH=/usr/bin"}, "/usr/bin/pwsh"), "/usr/bin/pwsh", pwsh.Core},
	} {
		p, err := pwsh.Find(c.path, c.allow, c.h)
		if err != nil || p.Path != c.want || p.Edition != c.edition {
			t.Errorf("%s: Find = %+v, %v; want %s (%s)", name, p, err, c.want, c.edition)
		}
	}
	if _, err := pwsh.Find("", false, fakeHost("windows", win, desktop)); herr.Kind(err) != shellexec.ReasonNotFound {
		t.Errorf("Find(5.1 not allowed) = %v, want %s", err, shellexec.ReasonNotFound)
	}
}

// TestFirstStatement: param, using and #requires open a script, by word and
// case-insensitively, after white space and a byte-order mark.
//
//dsh:test DH-T-EXEC-13
func TestFirstStatement(t *testing.T) {
	for cmd, want := range map[string]bool{
		"param($x) $x":            true,
		"  \n\tPARAM ( $x )":      true,
		"\uFEFFusing namespace X": true,
		"#Requires -Version 7":    true,
		"parameters":              false,
		"param-x":                 false,
		"Write-Output param":      false,
		"# param":                 false,
	} {
		if got := pwsh.FirstStatement(cmd); got != want {
			t.Errorf("FirstStatement(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// TestArgv: a command follows the flags and the UTF-8 preamble in one -Command
// string; a first-statement command is written, with a byte-order mark, to the
// private script file, which the command string invokes with its exit code.
//
//dsh:test DH-T-EXEC-13
func TestArgv(t *testing.T) {
	sh, err := pwsh.NewShell(pwsh.DefaultConfig(), fakeHost("linux", []string{"PATH=/usr/bin"}, "/usr/bin/pwsh"))
	if err != nil {
		t.Fatal(err)
	}
	argv, err := sh.Argv("Write-Output hi", nil)
	want := append(append([]string{"/usr/bin/pwsh"}, pwsh.Flags...), "-Command", pwsh.Preamble+"; Write-Output hi")
	if err != nil || !slices.Equal(argv, want) {
		t.Fatalf("Argv = %q, %v; want %q", argv, err, want)
	}
	dir := t.TempDir() + string(os.PathSeparator) + "it's"
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	argv, err = sh.Argv("param($x)\nexit 3", func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	script := dir + string(os.PathSeparator) + pwsh.ScriptName
	b, err := os.ReadFile(script)
	if err != nil || !bytes.Equal(b, []byte("\uFEFFparam($x)\nexit 3")) {
		t.Fatalf("script = %q, %v", b, err)
	}
	last := argv[len(argv)-1]
	if !slices.Contains(argv, "Bypass") || !strings.Contains(last, "& '"+strings.ReplaceAll(script, "'", "''")+"'") ||
		!strings.HasSuffix(last, "if ($LASTEXITCODE) { exit $LASTEXITCODE }") {
		t.Fatalf("Argv = %q", argv)
	}
}

// TestPwshExecutor: on the Windows lane pwsh runs `Write-Output hi; exit 3`:
// exit 3 and stdout tail hi (TC-06b). A first-statement command keeps its exit
// code through the script file. The row fails on Windows when pwsh is missing;
// elsewhere it runs when a pwsh is found.
//
//dsh:test DH-T-EXEC-13
func TestPwshExecutor(t *testing.T) {
	h := shellexec.LocalHost(os.Environ())
	p, err := pwsh.Find("", false, h)
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("pwsh is required on the Windows lane: %v", err)
		}
		t.Skipf("no pwsh on this lane: %v", err)
	}
	if p.Edition != pwsh.Core {
		t.Fatalf("Find = %+v, want PowerShell 7", p)
	}
	s, err := local.New(context.Background(), local.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	cfg := pwsh.Config{Path: p.Path}
	e, err := pwsh.New(cfg, h, shellexec.Options{Sandbox: s, Env: &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"Write-Output hi; exit 3", "param($x)\nWrite-Output hi\nexit 3"} {
		x, err := e.Start(context.Background(), shellexec.Request{Command: cmd, Policy: sandboxtest.Policy(sandbox.DangerFullAccess)})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		t.Cleanup(cancel)
		t.Cleanup(func() { _, _ = x.Stop(ctx, nil) })
		o, err := x.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if o.Kind != shellexec.Exited || o.ExitCode != 3 {
			t.Fatalf("%q: Outcome = %+v (stderr %q), want exited 3", cmd, o, o.Stderr.Tail)
		}
		if got := strings.TrimSpace(string(o.Stdout.Tail)); got != "hi" {
			t.Fatalf("%q: stdout tail = %q, want hi", cmd, got)
		}
		if o.Confinement.State != sandbox.StateUnconfined {
			t.Fatalf("%q: Confinement = %+v, want Unconfined", cmd, o.Confinement)
		}
	}
}
