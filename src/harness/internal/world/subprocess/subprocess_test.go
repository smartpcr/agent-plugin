package subprocess_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

func TestMain(m *testing.M) { subprocesstest.Main(m) }

const secretName = "DSH_DEEPSEEK_API_KEY"

func TestArgumentsArriveLiterally(t *testing.T) {
	args := []string{"a; echo pwned", `"quoted" \back\`, "", "$(whoami)", "with space", "%PATH%"}
	out := subprocesstest.Output(t, subprocesstest.Spec(t, subprocesstest.RoleArgs, args...))
	var got []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("child output %q: %v", out, err)
	}
	if strings.Join(got, "\x00") != strings.Join(args, "\x00") || len(got) != len(args) {
		t.Fatalf("child saw %q, want %q", got, args)
	}
}

// TC-06c, SP-11: a secret in the host environment never reaches the child.
func TestChildEnvironmentHoldsNoSecret(t *testing.T) {
	t.Setenv(secretName, "sk-dummy-never-leaks")
	t.Setenv("SERVICE_TOKEN", "dummy")
	t.Setenv("PLAIN_SETTING", "kept")
	probe := []string{secretName, "SERVICE_TOKEN", "PLAIN_SETTING"}

	t.Run("explicit list", func(t *testing.T) {
		spec := subprocesstest.Spec(t, subprocesstest.RoleEnvProbe, probe...)
		out := subprocesstest.Output(t, spec)
		want := secretName + "=absent\nSERVICE_TOKEN=absent\nPLAIN_SETTING=absent\n"
		if normal(out) != want {
			t.Fatalf("child reported %q, want %q", out, want)
		}
	})
	t.Run("built from the host", func(t *testing.T) {
		spec := subprocesstest.Spec(t, subprocesstest.RoleEnvProbe, probe...)
		spec.Env = subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Entries: spec.Env})
		out := subprocesstest.Output(t, spec)
		want := secretName + "=absent\nSERVICE_TOKEN=absent\nPLAIN_SETTING=present\n"
		if normal(out) != want {
			t.Fatalf("child reported %q, want %q", out, want)
		}
	})
}

func normal(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// TC-06c: StopTree stops a child and a grandchild that ignores the polite stop,
// and returns only after both are gone.
func TestStopTreeStopsAndAwaitsTheTree(t *testing.T) {
	p, child, grandchild := subprocesstest.Tree(t)
	if !subprocesstest.Alive(child) || !subprocesstest.Alive(grandchild) {
		t.Fatalf("tree not running: child %d alive=%v, grandchild %d alive=%v",
			child, subprocesstest.Alive(child), grandchild, subprocesstest.Alive(grandchild))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	if err := p.StopTree(ctx, 2*time.Second); err != nil {
		t.Fatalf("StopTree: %v", err)
	}
	if subprocesstest.Alive(child) || subprocesstest.Alive(grandchild) {
		t.Fatalf("StopTree returned with child alive=%v, grandchild alive=%v",
			subprocesstest.Alive(child), subprocesstest.Alive(grandchild))
	}
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("TreeGone after StopTree: %v", err)
	}
	if err := p.StopTree(ctx, 0); err != nil {
		t.Fatalf("second StopTree: %v", err)
	}
	t.Logf("owner %+v; stopped in %v", p.Owner(), time.Since(start))
}

func TestContextEndStopsTheTree(t *testing.T) {
	spec := subprocesstest.Spec(t, subprocesstest.RoleStubborn)
	ctx, cancel := context.WithCancel(context.Background())
	p, err := subprocess.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.StopTree(context.Background(), 0) })
	cancel()
	wctx, wcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer wcancel()
	if err := p.TreeGone(wctx); err != nil {
		t.Fatalf("tree survived the context: %v", err)
	}
}

func TestExitCode(t *testing.T) {
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleExit, "7"))
	if err := p.Wait(); err == nil {
		t.Fatal("Wait: no error for exit code 7")
	}
	if got := p.ExitCode(); got != 7 {
		t.Fatalf("ExitCode = %d, want 7", got)
	}
}

func TestArgvRules(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		argv []string
		want herr.Reason
	}{
		{"empty", nil, subprocess.ReasonEmptyArgv},
		{"relative", []string{"go", "version"}, subprocess.ReasonRelativeProgram},
		{"nul", []string{exe, "a\x00b"}, subprocess.ReasonBadArgument},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct {
				name string
				argv []string
				want herr.Reason
			}{"batch", []string{`C:\tools\run.cmd`}, subprocess.ReasonBatchRefused},
			struct {
				name string
				argv []string
				want herr.Reason
			}{"too long", []string{exe, strings.Repeat("x", 33000)}, subprocess.ReasonLineTooLong},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := subprocess.Start(context.Background(), subprocess.Spec{Argv: c.argv})
			if got := herr.Kind(err); got != c.want {
				t.Fatalf("Start(%q) = %v (kind %q), want kind %q", c.argv, err, got, c.want)
			}
		})
	}
}

func TestStartFailureIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-program")
	_, err := subprocess.Start(context.Background(), subprocess.Spec{Argv: []string{missing}})
	if herr.Kind(err) != subprocess.ReasonStartFailed {
		t.Fatalf("Start(missing) = %v, want %s", err, subprocess.ReasonStartFailed)
	}
}

func TestLookupUsesTheChildPath(t *testing.T) {
	dir := t.TempDir()
	name := "dsh-lookup-probe"
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	full := filepath.Join(dir, file)
	if err := os.WriteFile(full, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := subprocess.Env{"PATH=" + dir, "PATHEXT=.COM;.EXE"}
	got, err := subprocess.Lookup(name, env)
	if err != nil || got != full {
		t.Fatalf("Lookup = %q, %v; want %q", got, err, full)
	}
	if _, err := subprocess.Lookup(name, subprocess.Env{"PATH=" + t.TempDir()}); herr.Kind(err) != subprocess.ReasonNotFound {
		t.Fatalf("Lookup on another PATH = %v, want %s", err, subprocess.ReasonNotFound)
	}
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(dir, "dsh-batch.cmd"), []byte("@echo x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := subprocess.Lookup("dsh-batch", subprocess.Env{"PATH=" + dir, "PATHEXT=.CMD;.EXE"})
		if herr.Kind(err) != subprocess.ReasonBatchRefused {
			t.Fatalf("Lookup(batch) = %v, want %s", err, subprocess.ReasonBatchRefused)
		}
	}
}

func TestBuildEnv(t *testing.T) {
	host := []string{
		"PATH=/bin",
		"HOME=/home/u",
		"DSH_DEEPSEEK_API_KEY=sk",
		"dsh_home=/x",
		"GITHUB_TOKEN=t",
		"aws_secret_access_key=s",
		"DB_PASSWORD=p",
		"GPG_PASSPHRASE=p",
		"GOOGLE_APPLICATION_CREDENTIALS=/c.json",
		"SSH_AUTH_SOCK=/tmp/agent",
		"KEYBOARD_LAYOUT=us",
		"MY_REF=v",
		"CUSTOM_DENIED=v",
	}
	env := subprocess.BuildEnv(host, subprocess.EnvOptions{
		Deny:        []string{"CUSTOM_*"},
		Allow:       []string{"KEYBOARD_LAYOUT", "GITHUB_TOKEN", "MY_REF", "DSH_HOME"},
		Credentials: []string{"MY_REF", "GITHUB_TOKEN"},
		Entries:     []string{"PATH=/usr/bin", "EXTRA=1"},
		Managed:     []string{"EXTRA=managed"},
	})
	for _, name := range []string{"DSH_DEEPSEEK_API_KEY", "dsh_home", "GITHUB_TOKEN", "aws_secret_access_key",
		"DB_PASSWORD", "GPG_PASSPHRASE", "GOOGLE_APPLICATION_CREDENTIALS", "MY_REF", "CUSTOM_DENIED"} {
		if hasExact(env, name) {
			t.Errorf("%s survived: %q", name, env)
		}
	}
	for name, want := range map[string]string{"HOME": "/home/u", "SSH_AUTH_SOCK": "/tmp/agent",
		"KEYBOARD_LAYOUT": "us", "PATH": "/usr/bin", "EXTRA": "managed"} {
		if got := env.Get(name); got != want {
			t.Errorf("%s = %q, want %q (env %q)", name, got, want, env)
		}
	}
}

func hasExact(env subprocess.Env, name string) bool {
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func TestTreeOwnerIsReported(t *testing.T) {
	o := subprocess.TreeOwner()
	switch runtime.GOOS {
	case "windows":
		if o.Kind != subprocess.JobObject || !o.Exact {
			t.Fatalf("TreeOwner = %+v, want an exact job object", o)
		}
	case "linux":
		if o.Kind == subprocess.ProcessGroup && o.Fallback == "" {
			t.Fatalf("TreeOwner = %+v: a process-group fallback on Linux must say why", o)
		}
	default:
		if o.Kind != subprocess.ProcessGroup {
			t.Fatalf("TreeOwner = %+v, want a process group", o)
		}
	}
}

func TestReasonsAreDeclared(t *testing.T) {
	for _, r := range subprocess.Reasons {
		if !r.Valid() || r.Domain() != "subprocess" {
			t.Errorf("reason %q is not <subprocess>/<reason>", r)
		}
	}
	err := herr.Wrap(subprocess.ReasonStartFailed, errors.New("x"), "y")
	if !herr.Has(err, subprocess.ReasonStartFailed) {
		t.Fatal("Has does not see the reason")
	}
}
