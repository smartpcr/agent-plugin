package dshbin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

// recorder records a fatal failure instead of ending the real test.
type recorder struct {
	testing.TB
	failures []string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	runtime.Goexit()
}

func (r *recorder) Fatal(args ...any) { r.Fatalf("%s", fmt.Sprint(args...)) }

func (r *recorder) run(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

func TestPathBuildsOncePerTestBinary(t *testing.T) {
	first := Path(t)
	if info, err := os.Stat(first); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("Path = %s: %v", first, err)
	}
	t.Run("again", func(t *testing.T) {
		if again := Path(t); again != first {
			t.Errorf("second Path = %s, want %s", again, first)
		}
	})
	if !strings.HasPrefix(first, dir) {
		t.Errorf("binary %s is outside the build directory %s", first, dir)
	}
	if tagged := Path(t, "testhook"); tagged == first || !strings.HasPrefix(tagged, dir) {
		t.Errorf("Path with testhook = %s, want a second binary under %s", tagged, dir)
	}
}

// swap sets the package state for one test and restores it; fail, when set, is
// the recorded failure of the untagged build.
func swap(t *testing.T, main bool, fail string) {
	t.Helper()
	mu.Lock()
	oldMain, oldBuilds, oldDir := inMain, builds, dir
	inMain, builds = main, map[string]*bin{}
	if fail != "" {
		builds[""] = &bin{failed: fail}
	}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		inMain, builds, dir = oldMain, oldBuilds, oldDir
		mu.Unlock()
	})
}

// failure is the recorded failure of the untagged build.
func failure() string {
	mu.Lock()
	defer mu.Unlock()
	if b := builds[""]; b != nil {
		return b.failed
	}
	return ""
}

func TestPathOutsideMainFails(t *testing.T) {
	swap(t, false, "")
	r := &recorder{TB: t}
	r.run(func() { Path(r) })
	if len(r.failures) != 1 || !strings.Contains(r.failures[0], "dshbin.Main") {
		t.Errorf("failures %q", r.failures)
	}
}

func TestPathAfterAFailedBuildFailsWithoutBuilding(t *testing.T) {
	swap(t, true, "compile error")
	r := &recorder{TB: t}
	r.run(func() { Path(r) })
	if len(r.failures) != 1 || !strings.Contains(r.failures[0], "compile error") {
		t.Errorf("failures %q", r.failures)
	}
}

func TestOnceRecordsTheFatalMessage(t *testing.T) {
	swap(t, true, "")
	r := &recorder{TB: t}
	b := &bin{}
	o := &once{TB: r, dir: "d", b: b}
	if o.TempDir() != "d" {
		t.Errorf("TempDir = %s", o.TempDir())
	}
	r.run(func() { o.Fatalf("build %s", "broke") })
	if b.failed != "build broke" {
		t.Errorf("failed = %q", b.failed)
	}
	r.run(func() { o.Fatal("again") })
	if b.failed != "again" || len(r.failures) != 2 {
		t.Errorf("failed = %q, failures %q", b.failed, r.failures)
	}
}

func TestModuleDirFindsTheHarnessModule(t *testing.T) {
	d, err := ModuleDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "cmd", "dsh")); err != nil {
		t.Errorf("ModuleDir = %s: %v", d, err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("// x\nmodule example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if declares(filepath.Join(other, "go.mod"), Module) || declares(filepath.Join(other, "missing"), Module) {
		t.Error("declares accepted another module or a missing file")
	}
	t.Chdir(other)
	if d, err := ModuleDir(); err == nil {
		t.Errorf("ModuleDir outside the module = %s, want an error", d)
	}
	swap(t, true, "")
	r := &recorder{TB: t}
	r.run(func() { Path(r) })
	if len(r.failures) != 1 || !strings.Contains(r.failures[0], "no go.mod") || failure() == "" {
		t.Errorf("Path outside the module: failures %q, failed %q", r.failures, failure())
	}
}

func TestPathWithoutATempDirFails(t *testing.T) {
	swap(t, true, "")
	missing := filepath.Join(t.TempDir(), "missing")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, missing)
	}
	mu.Lock()
	dir = ""
	mu.Unlock()
	r := &recorder{TB: t}
	r.run(func() { Path(r) })
	if len(r.failures) != 1 || failure() == "" {
		t.Errorf("failures %q, failed %q", r.failures, failure())
	}
	// With the build directory made, a build directory inside it that cannot be
	// made fails too.
	mu.Lock()
	dir = missing
	mu.Unlock()
	r = &recorder{TB: t}
	r.run(func() { Path(r, "other") })
	if len(r.failures) != 1 {
		t.Errorf("failures %q", r.failures)
	}
}
