package subprocess_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// A tree that is still running when TreeGone's context ends is reported as
// survivors, naming the running direct child and what its owner still holds; the
// error keeps the context's cause. ExitCode is -1 while the child runs, and Kill
// of a tree that is gone is a no-op.
//
//dsh:test DH-T-EXEC-5
func TestTreeGoneReportsSurvivors(t *testing.T) {
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
	if got := p.ExitCode(); got != -1 {
		t.Fatalf("ExitCode of a running child = %d; want -1", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.TreeGone(ctx)
	if herr.Kind(err) != subprocess.ReasonSurvivors || !errors.Is(err, context.Canceled) {
		t.Fatalf("TreeGone with an ended context = %v; want %s wrapping context.Canceled", err, subprocess.ReasonSurvivors)
	}
	msg := err.Error()
	if !strings.Contains(msg, "pid "+strconv.Itoa(p.Pid())+" running") || !strings.Contains(msg, string(p.Owner().Kind)) {
		t.Fatalf("survivors %q; want the running pid %d and the owner kind %s", msg, p.Pid(), p.Owner().Kind)
	}
	if runtime.GOOS == "windows" && !strings.Contains(msg, "1 processes left in the job") {
		t.Fatalf("survivors %q; want the job's count of one process", msg)
	}

	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	if err := p.StopTree(sctx, 0); err != nil {
		t.Fatalf("StopTree: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill of a stopped tree = %v; want nil", err)
	}
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("TreeGone of a stopped tree, even with an ended context = %v; want nil", err)
	}
}

// An exited child whose tree is empty needs no stop: Kill returns nil, and the
// direct child's channel is closed.
func TestKillAfterExitIsANoOp(t *testing.T) {
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleExit, "0"))
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-p.Exited():
	default:
		t.Fatal("Exited is open after Wait returned")
	}
	if got := p.ExitCode(); got != 0 {
		t.Fatalf("ExitCode = %d; want 0", got)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill after exit = %v; want nil", err)
	}
}

// DH-T-EXEC-2: Lookup refuses an empty name and a relative path, returns an
// absolute path unchanged, skips PATH entries that are empty or relative, and
// never takes a directory for a program. A name that already has an extension is
// tried as given first on Windows, and an empty PATHEXT falls back to the default.
//
//dsh:test DH-T-EXEC-2
func TestLookupEdges(t *testing.T) {
	if _, err := subprocess.Lookup("", nil); herr.Kind(err) != subprocess.ReasonEmptyArgv {
		t.Errorf("Lookup(\"\") = %v; want %s", err, subprocess.ReasonEmptyArgv)
	}
	rel := filepath.Join("bin", "tool")
	if _, err := subprocess.Lookup(rel, nil); herr.Kind(err) != subprocess.ReasonRelativeProgram {
		t.Errorf("Lookup(%q) = %v; want %s", rel, err, subprocess.ReasonRelativeProgram)
	}
	abs := filepath.Join(t.TempDir(), "tool")
	if got, err := subprocess.Lookup(abs, nil); err != nil || got != abs {
		t.Errorf("Lookup(%q) = %q, %v; want it unchanged", abs, got, err)
	}

	dir := t.TempDir()
	name := "dsh-probe"
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	// A directory with the program's name comes first on PATH and is skipped.
	shadow := t.TempDir()
	if err := os.Mkdir(filepath.Join(shadow, file), 0o755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, file)
	if err := os.WriteFile(full, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{"", "relative" + string(filepath.Separator) + "dir", shadow, dir}, string(filepath.ListSeparator))
	got, err := subprocess.Lookup(name, subprocess.Env{"PATH=" + path})
	if err != nil || got != full {
		t.Fatalf("Lookup(%q) on %q = %q, %v; want %q", name, path, got, err, full)
	}
	if runtime.GOOS == "windows" {
		if got, err := subprocess.Lookup(file, subprocess.Env{"PATH=" + dir, "PATHEXT="}); err != nil || got != full {
			t.Fatalf("Lookup(%q) with an empty PATHEXT = %q, %v; want %q", file, got, err, full)
		}
	}
	if _, err := subprocess.Lookup(name, subprocess.Env{"PATH=" + shadow}); herr.Kind(err) != subprocess.ReasonNotFound {
		t.Fatalf("Lookup with only a directory of that name = %v; want %s", err, subprocess.ReasonNotFound)
	}
}

// On Windows a batch file is refused however its name is decorated: an
// alternate data stream suffix or trailing dots and spaces still run cmd.exe.
//
//dsh:test DH-T-EXEC-2
func TestBatchRefusedThroughNameDecorations(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files are a Windows rule")
	}
	for _, prog := range []string{`C:\tools\run.cmd::$DATA`, `C:\tools\run.bat.`, `C:\tools\RUN.BAT `, `C:/tools/run.cmd:stream`} {
		if _, err := subprocess.Start(context.Background(), subprocess.Spec{Argv: []string{prog}}); herr.Kind(err) != subprocess.ReasonBatchRefused {
			t.Errorf("Start(%q) = %v; want %s", prog, err, subprocess.ReasonBatchRefused)
		}
	}
}

// DH-T-EXEC-3: BuildEnv drops host entries that are not NAME=value, keeps a
// Windows per-drive entry by its "=C:" name, and With sets a name on a copy.
//
//dsh:test DH-T-EXEC-3
func TestEnvEntryShapes(t *testing.T) {
	env := subprocess.BuildEnv([]string{"NOEQUALS", "=bad", "=C:=C:\\work", "=", "A=1"}, subprocess.EnvOptions{})
	if env.Has("NOEQUALS") || env.Has("=bad") {
		t.Fatalf("malformed entries survived: %q", env)
	}
	if !env.Has("=C:") || env.Get("=C:") != `C:\work` {
		t.Fatalf("per-drive entry = %q in %q; want C:\\work", env.Get("=C:"), env)
	}
	if env.Get("A") != "1" {
		t.Fatalf("A = %q in %q", env.Get("A"), env)
	}
	with := env.With("A", "2")
	if with.Get("A") != "2" || env.Get("A") != "1" {
		t.Fatalf("With: copy A=%q, original A=%q; want 2 and 1", with.Get("A"), env.Get("A"))
	}
	if n := strings.Count(strings.Join(with, "\n"), "A="); n != 1 {
		t.Fatalf("With left %d entries for A: %q", n, with)
	}
}

// An owner prints its kind, and why a stronger owner was skipped when one was.
//
//dsh:test DH-T-EXEC-4
func TestOwnerString(t *testing.T) {
	if got := (subprocess.Owner{Kind: subprocess.JobObject, Exact: true}).String(); got != "job-object" {
		t.Errorf("String = %q; want job-object", got)
	}
	o := subprocess.Owner{Kind: subprocess.ProcessGroup, Fallback: "cgroup v2 not writable"}
	if got := o.String(); got != "process-group (fallback: cgroup v2 not writable)" {
		t.Errorf("String = %q; want the kind and the fallback", got)
	}
}
