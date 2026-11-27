package dshbin

import (
	"debug/buildinfo"
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

// A test that sets a build variable gets a build of its own, built once and
// carrying that variant in its build information; DSH_BIN stands for the default
// build only.
func TestPathBuildsEachVariantOnce(t *testing.T) {
	host := Path(t)
	if !Default() {
		t.Fatal("Default = false before any build variable changed")
	}
	t.Run("linux pure Go", func(t *testing.T) {
		t.Setenv("GOOS", "linux")
		t.Setenv("GOARCH", "amd64")
		t.Setenv("CGO_ENABLED", "0")
		if Default() {
			t.Error("Default = true with GOOS, GOARCH and CGO_ENABLED set")
		}
		p := Path(t)
		if p == host {
			t.Fatalf("the linux/amd64 pure-Go build is the host build %s", host)
		}
		if again := Path(t); again != p {
			t.Errorf("second Path = %s, want %s", again, p)
		}
		info, err := buildinfo.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, s := range info.Settings {
			got[s.Key] = s.Value
		}
		if got["GOOS"] != "linux" || got["GOARCH"] != "amd64" || got["CGO_ENABLED"] != "0" {
			t.Errorf("build settings %v, want linux/amd64 with cgo off", got)
		}
	})
	if !Default() {
		t.Error("Default = false after the variables were restored")
	}
}

func TestVerifyRejectsABuildOfAnotherVariant(t *testing.T) {
	host := Path(t)
	if err := Verify(host, nil); err != nil {
		t.Fatalf("Verify(host build) = %v", err)
	}
	if err := Verify(host, []string{"testhook"}); err == nil || !strings.Contains(err.Error(), "-tags") {
		t.Errorf("Verify with tags the build lacks = %v, want a -tags mismatch", err)
	}
	other := "linux"
	if runtime.GOOS == other {
		other = "windows"
	}
	t.Setenv("GOOS", other)
	if err := Verify(host, nil); err == nil || !strings.Contains(err.Error(), "GOOS") {
		t.Errorf("Verify for GOOS=%s = %v, want a GOOS mismatch", other, err)
	}
	t.Setenv("GOOS", "")
	t.Setenv("GOFLAGS", "-race")
	if err := Verify(host, nil); err == nil || !strings.Contains(err.Error(), "-race") {
		t.Errorf("Verify for GOFLAGS=-race = %v, want a -race mismatch", err)
	}
	if err := Verify(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Error("Verify of a missing file passed")
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
		builds[buildKey(nil)] = &bin{failed: fail}
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
	if b := builds[buildKey(nil)]; b != nil {
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
