package bash_test

import (
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
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
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

// TestFindOrder: the configured path, then Git for Windows, then PATH; on
// Windows the WSL launcher in System32 or WindowsApps is never chosen.
//
//dsh:test DH-T-EXEC-13
func TestFindOrder(t *testing.T) {
	win := []string{`ProgramFiles=C:\Program Files`, `SystemRoot=C:\Windows`,
		`Path=C:\Windows\system32;C:\Users\u\AppData\Local\Microsoft\WindowsApps;C:\msys\usr\bin`}
	for name, c := range map[string]struct {
		path   string
		h      shellexec.Host
		want   string
		source string
	}{
		"linux path":  {"", fakeHost("linux", []string{"PATH=/usr/local/bin:/bin"}, "/bin/bash"), "/bin/bash", shellexec.SourcePath},
		"linux conf":  {"/opt/bash", fakeHost("linux", []string{"PATH=/bin"}, "/opt/bash", "/bin/bash"), "/opt/bash", shellexec.SourceConfig},
		"git install": {"", fakeHost("windows", win, `C:\Program Files\Git\bin\bash.exe`, `C:\msys\usr\bin\bash.exe`), `C:\Program Files\Git\bin\bash.exe`, shellexec.SourceInstall},
		"skips wsl": {"", fakeHost("windows", win, `C:\Windows\system32\bash.exe`,
			`C:\Users\u\AppData\Local\Microsoft\WindowsApps\bash.exe`, `C:\msys\usr\bin\bash.exe`), `C:\msys\usr\bin\bash.exe`, shellexec.SourcePath},
	} {
		p, err := bash.Find(c.path, c.h)
		if err != nil || p.Path != c.want || p.Source != c.source {
			t.Errorf("%s: Find = %+v, %v; want %s from %s", name, p, err, c.want, c.source)
		}
	}
	onlyWSL := fakeHost("windows", win, `C:\Windows\system32\bash.exe`, `C:\Users\u\AppData\Local\Microsoft\WindowsApps\bash.exe`)
	if p, err := bash.Find("", onlyWSL); herr.Kind(err) != shellexec.ReasonNotFound {
		t.Errorf("Find(only the WSL launcher) = %+v, %v; want %s", p, err, shellexec.ReasonNotFound)
	}
	if _, err := bash.Find("/opt/none", fakeHost("linux", nil)); herr.Kind(err) != shellexec.ReasonNotFound {
		t.Errorf("Find(missing configured) = %v, want %s", err, shellexec.ReasonNotFound)
	}
}

// TestArgv: the command is one argv element after -c; the defaults and unset
// names are the dialect's.
//
//dsh:test DH-T-EXEC-13
func TestArgv(t *testing.T) {
	sh, err := bash.NewShell(bash.Config{}, fakeHost("linux", []string{"PATH=/bin"}, "/bin/bash"))
	if err != nil {
		t.Fatal(err)
	}
	argv, err := sh.Argv("echo 'a b'; exit 3", nil)
	if err != nil || !slices.Equal(argv, []string{"/bin/bash", "-c", "echo 'a b'; exit 3"}) {
		t.Fatalf("Argv = %q, %v", argv, err)
	}
	if !slices.Contains(sh.Defaults(), "TERM=dumb") || !slices.Contains(sh.Unset(), "BASH_ENV") {
		t.Fatalf("Defaults = %q, Unset = %q", sh.Defaults(), sh.Unset())
	}
	if _, err := bash.NewShell(bash.Config{Timeout: time.Hour, MaxTimeout: time.Minute}, fakeHost("linux", nil, "/bin/bash")); herr.Kind(err) != shellexec.ReasonBadConfig {
		t.Fatalf("NewShell(timeout over the cap) = %v, want %s", err, shellexec.ReasonBadConfig)
	}
}

// TestBashExecutor: on the Linux lane, bash found by preflight runs
// `echo hi; exit 3` under a danger-full-access policy: exit 3, stdout tail hi,
// Unconfined. The row fails, never skips, on Linux when bash is missing
// (TC-06b); elsewhere it runs when a bash is found.
//
//dsh:test DH-T-EXEC-13
func TestBashExecutor(t *testing.T) {
	h := shellexec.LocalHost(os.Environ())
	if _, err := bash.Find("", h); err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("bash is required on the Linux lane: %v", err)
		}
		t.Skipf("no bash on this lane: %v", err)
	}
	s, err := local.New(context.Background(), local.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	e, err := bash.New(bash.Config{}, h, shellexec.Options{Sandbox: s, Env: &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}})
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.Start(context.Background(), shellexec.Request{Command: "echo hi; exit 3", Policy: sandboxtest.Policy(sandbox.DangerFullAccess)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Cleanup(func() { _, _ = x.Stop(ctx, nil) })
	o, err := x.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != shellexec.Exited || o.ExitCode != 3 {
		t.Fatalf("Outcome = %+v, want exited 3", o)
	}
	if got := strings.TrimSpace(string(o.Stdout.Tail)); got != "hi" {
		t.Fatalf("stdout tail = %q, want hi", got)
	}
	if o.Confinement.State != sandbox.StateUnconfined {
		t.Fatalf("Confinement = %+v, want Unconfined", o.Confinement)
	}
	if r := x.Reader(subprocess.StreamStdout); r == nil {
		t.Fatal("no background reader")
	}
}
