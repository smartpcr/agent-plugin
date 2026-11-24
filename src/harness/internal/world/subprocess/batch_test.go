package subprocess_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// installTool puts a stand-in tool named "tool" in a fresh directory: either a
// copy of the test binary as tool.exe, or tool.cmd, a shim shaped like the ones
// npm installs, which first writes marker and then forwards %* to the test
// binary. It returns the directory.
func installTool(t *testing.T, kind, marker string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	switch kind {
	case ".exe":
		src, err := os.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		dst, err := os.OpenFile(filepath.Join(dir, "tool.exe"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			t.Fatal(err)
		}
	case ".cmd":
		shim := "@echo off\r\necho ran>\"" + marker + "\"\r\n\"" + exe + "\" %*\r\n"
		if err := os.WriteFile(filepath.Join(dir, "tool.cmd"), []byte(shim), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// toolSpec is the spec that starts "tool", found by bare name on the child's PATH
// (dir), in role with args, opted in to the checked batch run. The environment is
// derived from the host's by BuildEnv, so it is the scrubbed one.
func toolSpec(t *testing.T, dir, role string, args ...string) (subprocess.Spec, subprocess.Program) {
	t.Helper()
	base := subprocesstest.Spec(t, role)
	env := subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Entries: append(slices.Clone([]string(base.Env)), "PATH="+dir)})
	prog, err := subprocess.LookupProgram("tool", env)
	if err != nil {
		t.Fatalf("LookupProgram(tool) on %s: %v", dir, err)
	}
	return subprocess.Spec{Argv: append([]string{prog.Path}, args...), Env: env, Batch: subprocess.AllowChecked, Launch: subprocesstest.Launch}, prog
}

// runTool runs spec to completion, returns its standard output and checks that
// the whole tree is gone once the direct child has exited.
func runTool(t *testing.T, spec subprocess.Spec) string {
	t.Helper()
	var out, errOut bytes.Buffer
	spec.Stdout, spec.Stderr = &out, &errOut
	p := subprocesstest.Start(t, spec)
	if err := p.Wait(); err != nil {
		t.Fatalf("tool failed: %v; stderr %q", err, errOut.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("tool's tree is not gone after it exited: %v", err)
	}
	return out.String()
}

// refused starts spec, which must be refused with want before any process is
// created.
func refused(t *testing.T, spec subprocess.Spec, want herr.Reason) {
	t.Helper()
	p, err := subprocess.Start(context.Background(), spec)
	if p != nil {
		t.Cleanup(func() { _ = p.StopTree(context.Background(), 0) })
	}
	if herr.Kind(err) != want {
		t.Fatalf("Start(%q) = %v (kind %q); want kind %q", spec.Argv[1:], err, herr.Kind(err), want)
	}
}

func absent(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s: %s exists (stat error %v); a command ran", what, path, err)
	}
}

// DH-LIB-NODEPROC-R4, DH-LIB-CROSSSPAWN-R1, R2 (gap-closure 16.5.1, the scenario
// "A .cmd client is started by bare name with checked arguments"): a tool
// installed as tool.exe or as tool.cmd starts by the bare name "tool" through the
// process package. Plain arguments, cmd.exe metacharacters inside them included,
// arrive unchanged. A batch file is refused without the opt-in; with it, each
// argument holding ", %, !, CR, LF or NUL, and the string "&echo injected, is
// refused before anything starts, and a marker the shim writes on every run
// proves that nothing ran. The child gets the scrubbed environment, and its tree
// is gone once it exits.
//
//dsh:test DH-T-NODEPROC-2, DH-T-CROSSSPAWN-1
func TestBatchToolByBareNameWithCheckedArguments(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files and cmd.exe exist only on Windows; the argument and command-line rules are checked on every system by TestBatchRules")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-dummy-never-leaks")
	t.Setenv("GH_TOKEN", "gho_dummy-never-leaks")
	scratch := t.TempDir()
	marker := filepath.Join(scratch, "ran")
	injected := filepath.Join(scratch, "injected")
	plain := []string{"hello", "with space", "", `back\slash\`, `C:\dir\`, "a&b|c<d>e^f(g)", "x&echo injected>" + injected, "trailing-tab\t"}

	t.Run(".cmd", func(t *testing.T) {
		dir := installTool(t, ".cmd", marker)
		spec, prog := toolSpec(t, dir, subprocesstest.RoleArgs)
		if !prog.Batch || !strings.EqualFold(filepath.Ext(prog.Path), ".cmd") {
			t.Fatalf("LookupProgram(tool) = %+v; want the .cmd file, reported as a batch file", prog)
		}
		if _, err := subprocess.Lookup("tool", spec.Env); herr.Kind(err) != subprocess.ReasonBatchRefused {
			t.Fatalf("Lookup(tool) = %v; want %s: Lookup never opts in", err, subprocess.ReasonBatchRefused)
		}

		// Refusals first, while the marker is absent.
		noOptIn := spec
		noOptIn.Batch = subprocess.Refuse
		refused(t, noOptIn, subprocess.ReasonBatchRefused)
		for _, bad := range []string{`say "hi"`, "100%", "%PATH%", "wow!", "a\rb", "a\nb", "a\x00b", `"&echo injected>` + injected} {
			s := spec
			s.Argv = append(slices.Clone(spec.Argv), "ok", bad)
			refused(t, s, subprocess.ReasonBadArgument)
		}
		long := spec
		long.Argv = append(slices.Clone(spec.Argv), strings.Repeat("x", 8200))
		refused(t, long, subprocess.ReasonLineTooLong)
		absent(t, marker, "after the refused starts")

		spec.Argv = append(spec.Argv, plain...)
		assertArgs(t, runTool(t, spec), plain)
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("the shim's marker is missing after a run (%v), so its absence above proves nothing", err)
		}
		absent(t, injected, "after the plain run")

		env, _ := toolSpec(t, dir, subprocesstest.RoleEnvProbe, "ANTHROPIC_API_KEY", "GH_TOKEN")
		if got := normal(runTool(t, env)); got != "ANTHROPIC_API_KEY=absent\nGH_TOKEN=absent\n" {
			t.Fatalf("the .cmd child reported %q; want both credentials absent", got)
		}
	})

	t.Run(".exe", func(t *testing.T) {
		dir := installTool(t, ".exe", "")
		spec, prog := toolSpec(t, dir, subprocesstest.RoleArgs)
		if prog.Batch || !strings.EqualFold(filepath.Ext(prog.Path), ".exe") {
			t.Fatalf("LookupProgram(tool) = %+v; want the .exe, not a batch file", prog)
		}
		// An ordinary program takes what a batch file cannot: the opt-in does not
		// apply to it.
		args := append(slices.Clone(plain), `say "hi"`, "100%", "wow!")
		spec.Argv = append(spec.Argv, args...)
		assertArgs(t, runTool(t, spec), args)

		env, _ := toolSpec(t, dir, subprocesstest.RoleEnvProbe, "ANTHROPIC_API_KEY", "GH_TOKEN")
		if got := normal(runTool(t, env)); got != "ANTHROPIC_API_KEY=absent\nGH_TOKEN=absent\n" {
			t.Fatalf("the .exe child reported %q; want both credentials absent", got)
		}
	})
}

func assertArgs(t *testing.T, out string, want []string) {
	t.Helper()
	var got []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("child output %q: %v", out, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("child saw %q; want %q", got, want)
	}
}
