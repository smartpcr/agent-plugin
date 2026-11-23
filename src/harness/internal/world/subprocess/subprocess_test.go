package subprocess_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

//dsh:test DH-T-NODEPROC-1
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
//
//dsh:test DH-T-EXEC-3, DH-T-NODEPROC-4
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
//
//dsh:test DH-T-NODEPROC-5
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
	// Exited, not !Alive: the grandchild's parent died in the stop, so its reaper
	// (init or the scope's manager) reaps it on its own time, and until then
	// kill(pid, 0) still finds the zombie.
	if !subprocesstest.Exited(child) || !subprocesstest.Exited(grandchild) {
		t.Fatalf("StopTree returned with child exited=%v, grandchild exited=%v",
			subprocesstest.Exited(child), subprocesstest.Exited(grandchild))
	}
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("TreeGone after StopTree: %v", err)
	}
	if err := p.StopTree(ctx, 0); err != nil {
		t.Fatalf("second StopTree: %v", err)
	}
	t.Logf("owner %+v; stopped in %v", p.Owner(), time.Since(start))
}

//dsh:test DH-T-EXEC-5
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

// A context that ends after the direct child exited still stops the descendants
// it left behind.
//
//dsh:test DH-T-NODEPROC-5
func TestContextEndStopsDescendantsAfterChildExit(t *testing.T) {
	spec := subprocesstest.Spec(t, subprocesstest.RoleOrphan)
	var out strings.Builder
	spec.Stdout = &out
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := subprocess.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.StopTree(context.Background(), 0) })
	if err := p.Wait(); err != nil {
		t.Fatalf("orphan child: %v", err)
	}
	var grandchild int
	if _, err := fmt.Sscanf(out.String(), "orphan %d", &grandchild); err != nil {
		t.Fatalf("orphan child said %q: %v", out.String(), err)
	}
	if !subprocesstest.Alive(grandchild) {
		t.Fatalf("grandchild %d is not running after the child exited", grandchild)
	}
	cancel()
	wctx, wcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer wcancel()
	if err := p.TreeGone(wctx); err != nil {
		t.Fatalf("descendant survived the context: %v", err)
	}
	// The grandchild was orphaned when its parent exited, so its reaper reaps
	// it on its own time; a zombie it has not reaped yet has exited all the same.
	if !subprocesstest.Exited(grandchild) {
		t.Fatalf("grandchild %d still running after TreeGone", grandchild)
	}
}

// A grandchild that detaches itself, as a daemon does (a new session on Linux, a
// new process group on Windows), is still the tree's: once the direct child has
// exited, the tree wait still counts it, and StopTree ends it. Only an exact owner
// holds it (the Job Object, or a control group, which on Linux this test gets
// inside the delegated scope of TestControlGroupOwnerInADelegatedScope); a
// process group cannot (DH-LIB-NODEPROC-R9's owner table), so on that rung the
// test skips before it starts anything that would escape.
//
//dsh:test DH-T-NODEPROC-6
func TestDetachedGrandchildStaysInTheTree(t *testing.T) {
	if o := subprocess.TreeOwner(); !o.Exact {
		t.Skipf("owner %s: a process group does not hold a grandchild that leaves it", o)
	}
	spec := subprocesstest.Spec(t, subprocesstest.RoleDetached)
	var out, errOut strings.Builder
	spec.Stdout, spec.Stderr = &out, &errOut
	p := subprocesstest.Start(t, spec)
	if err := p.Wait(); err != nil {
		t.Fatalf("detaching child: %v; stderr %q", err, errOut.String())
	}
	var grandchild int
	if _, err := fmt.Sscanf(out.String(), "detached %d", &grandchild); err != nil {
		t.Fatalf("detaching child said %q: %v", out.String(), err)
	}
	if !subprocesstest.Alive(grandchild) {
		t.Fatalf("detached grandchild %d is not running after the child exited", grandchild)
	}
	// The grandchild waits to be killed, so the tree is not gone, and the wait
	// says so instead of returning as if it were.
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.TreeGone(ended); herr.Kind(err) != subprocess.ReasonSurvivors {
		t.Fatalf("TreeGone with the detached grandchild running = %v; want %s", err, subprocess.ReasonSurvivors)
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	if err := p.StopTree(ctx, 0); err != nil {
		t.Fatalf("StopTree: %v", err)
	}
	if !subprocesstest.Exited(grandchild) {
		t.Fatalf("detached grandchild %d still running after StopTree", grandchild)
	}
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("TreeGone after StopTree: %v", err)
	}
	t.Logf("owner %+v", p.Owner())
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

//dsh:test DH-T-EXEC-2
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

//dsh:test DH-T-NODEPROC-3, DH-T-WHICH-1
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

// BuildEnv removes deny-pattern names unless allowed, exact credential names and
// every DSH_* name, then adds the caller's entries and the managed snapshot last.
//
//dsh:test DH-T-EXEC-3
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

// secretNames is a SecretNames over a fixed list of names, compared without
// regard to case as the secret store compares them.
type secretNames []string

func (s secretNames) IsSecretName(name string) bool {
	for _, n := range s {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// panicNames is a SecretNames whose check fails.
type panicNames struct{}

func (panicNames) IsSecretName(string) bool { panic("store unavailable") }

// SP-11: BuildEnv removes every host name the secret names report, a name such as
// GITHUB_PAT that no deny pattern catches included, whatever its case and whatever
// the allow list says; a check that panics removes the name (fail closed); the
// caller's entries are its own and are kept.
//
//dsh:test DH-T-EXEC-3
func TestBuildEnvRemovesSecretNames(t *testing.T) {
	host := []string{"PATH=/bin", "GITHUB_PAT=ghp", "github_pat=lower", "VENDOR_SIGNING_SEED=s", "PLAIN=1"}
	env := subprocess.BuildEnv(host, subprocess.EnvOptions{
		Secrets: secretNames{"GITHUB_PAT", "VENDOR_SIGNING_SEED"},
		Allow:   []string{"GITHUB_PAT", "*"},
		Entries: []string{"VENDOR_SIGNING_SEED=explicit"},
	})
	for _, name := range []string{"GITHUB_PAT", "github_pat"} {
		if hasExact(env, name) {
			t.Errorf("%s survived: %q", name, env)
		}
	}
	if env.Get("PATH") != "/bin" || env.Get("PLAIN") != "1" || env.Get("VENDOR_SIGNING_SEED") != "explicit" {
		t.Errorf("env = %q; want PATH, PLAIN and the explicit entry", env)
	}
	without := subprocess.BuildEnv(host, subprocess.EnvOptions{})
	if !hasExact(without, "GITHUB_PAT") {
		t.Errorf("without Secrets GITHUB_PAT is gone from %q; no deny pattern names it, so only Secrets removes it", without)
	}
	if failed := subprocess.BuildEnv(host, subprocess.EnvOptions{Secrets: panicNames{}}); len(failed) != 0 {
		t.Errorf("a failing check kept %q; want every host name removed", failed)
	}
}

// A re-executed child started with a store-like SecretNames reports GITHUB_PAT
// absent though the host holds it (the plan's scenario "A named reference never
// reaches a child").
//
//dsh:test DH-T-EXEC-3, DH-T-NODEPROC-4
func TestChildNeverInheritsANamedSecret(t *testing.T) {
	t.Setenv("GITHUB_PAT", "ghp-dummy-never-leaks")
	spec := subprocesstest.Spec(t, subprocesstest.RoleEnvProbe, "GITHUB_PAT")
	spec.Env = subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Secrets: secretNames{"GITHUB_PAT"}, Entries: spec.Env})
	if out := normal(subprocesstest.Output(t, spec)); out != "GITHUB_PAT=absent\n" {
		t.Fatalf("child reported %q; want GITHUB_PAT absent", out)
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

//dsh:test DH-T-EXEC-4
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

//dsh:test DH-T-UTIL-8
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
