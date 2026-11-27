// Package dshbin builds the dsh binary once per test binary (DH-D-UTIL-7, C54):
// a thin wrapper over the M1 test kit's launcher.Build of ./cmd/dsh. The first
// Path call for a set of build tags builds it into a directory that outlives that
// test; every later call with the same tags in the same test binary returns the
// same path. The directory is removed when
// the package's tests end, so a package that calls Path runs its tests through
// Main from its TestMain; Path refuses to build otherwise.
package dshbin

import (
	"bufio"
	"cmp"
	"debug/buildinfo"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// Module is the import path of the module whose ./cmd/dsh is built.
const Module = "github.com/smartpcr/agent-plugin/src/harness"

var (
	mu     sync.Mutex
	inMain bool                // Main is running the tests
	dir    string              // the build directory, removed by Main
	builds = map[string]*bin{} // by build tags and variant, once built or failed
)

// VariantEnv are the variables that select a build other than the host's default
// one (a target platform, cgo, -race and other go flags, a C toolchain). A test
// that sets one of them (t.Setenv) before Path gets a build of its own.
var VariantEnv = []string{"GOOS", "GOARCH", "GOARM", "GOARM64", "GOAMD64", "GO386", "CGO_ENABLED", "GOFLAGS", "CC", "CGO_CFLAGS", "CGO_LDFLAGS"}

// base is the variant of the test binary's own environment, before any test ran.
var base = variant()

// variant is the current value of every VariantEnv variable.
func variant() string {
	var b strings.Builder
	for _, k := range VariantEnv {
		b.WriteString(k + "=" + os.Getenv(k) + "\n")
	}
	return b.String()
}

// buildKey is the key of builds for tags and the current variant.
func buildKey(tags []string) string { return strings.Join(tags, ",") + "\n" + variant() }

// Default reports whether no VariantEnv variable changed since the test binary started,
// so a binary built for the host's default build (DSH_BIN) stands for Path(t).
func Default() bool {
	mu.Lock()
	defer mu.Unlock()
	return variant() == base
}

// bin is one build of ./cmd/dsh: its path, or the first build's failure.
type bin struct{ path, failed string }

// Main runs the tests of m through Run and exits with their status. A TestMain
// calls it:
//
//	func TestMain(m *testing.M) { dshbin.Main(m) }
func Main(m *testing.M) { os.Exit(Run(m)) }

// Run runs the tests of m, then removes every binary Path built, and returns the
// tests' status (1 when the removal fails); a TestMain with more to do after the
// tests calls it in place of Main.
func Run(m *testing.M) int {
	mu.Lock()
	inMain = true
	mu.Unlock()
	code := m.Run()
	mu.Lock()
	defer mu.Unlock()
	if dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "dshbin: removing %s: %v\n", dir, err)
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}

// Path returns the dsh binary built from this checkout's ./cmd/dsh with the
// build tags tags (none: the release build) and the current VariantEnv values,
// building it on the first call for that pair; the build's embedded build
// information must show the requested platform, cgo, -race and tags. A failed
// build fails t and every later caller for the same pair, without a second build.
func Path(t testing.TB, tags ...string) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	key := buildKey(tags)
	b := builds[key]
	switch {
	case b != nil && b.path != "":
		return b.path
	case b != nil:
		t.Fatalf("dshbin: the build of ./cmd/dsh (tags %q) failed earlier in this test binary:\n%s", key, b.failed)
	case !inMain:
		t.Fatal("dshbin: the package's TestMain must run its tests through dshbin.Main, which removes the binary built once per test binary")
	}
	b = &bin{}
	builds[key] = b
	mod, err := ModuleDir()
	if err != nil {
		b.failed = err.Error()
		t.Fatalf("dshbin: %v", err)
	}
	if dir == "" {
		d, err := os.MkdirTemp("", "dshbin-")
		if err != nil {
			b.failed = err.Error()
			t.Fatalf("dshbin: %v", err)
		}
		dir = d
	}
	d, err := os.MkdirTemp(dir, "build-")
	if err != nil {
		b.failed = err.Error()
		t.Fatalf("dshbin: %v", err)
	}
	p := launcher.Build(&once{TB: t, dir: d, b: b}, mod, "./cmd/dsh", tags...)
	if err := Verify(p, tags); err != nil {
		b.failed = err.Error()
		t.Fatalf("dshbin: %v", err)
	}
	b.path = p
	return p
}

// Verify checks the build information embedded in the binary at p against the
// build tags tags and the current VariantEnv values: GOOS and GOARCH (the host's
// when unset), CGO_ENABLED when set, -race when GOFLAGS asks for it, and -tags.
func Verify(p string, tags []string) error {
	info, err := buildinfo.ReadFile(p)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	got := map[string]string{}
	for _, s := range info.Settings {
		got[s.Key] = s.Value
	}
	want := map[string]string{"GOOS": cmp.Or(os.Getenv("GOOS"), runtime.GOOS), "GOARCH": cmp.Or(os.Getenv("GOARCH"), runtime.GOARCH), "-tags": strings.Join(tags, ",")}
	if v := os.Getenv("CGO_ENABLED"); v != "" {
		want["CGO_ENABLED"] = v
	}
	if slices.Contains(strings.Fields(os.Getenv("GOFLAGS")), "-race") {
		want["-race"] = "true"
	}
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if got[k] != want[k] {
			return fmt.Errorf("%s was built with %s=%q, want %q", p, k, got[k], want[k])
		}
	}
	return nil
}

// once is the test that triggers the build, with the build directory standing in
// for its TempDir (which would be removed when that test ends), and its fatal
// message recorded so later callers fail with it. Path holds mu around it.
type once struct {
	testing.TB
	dir string
	b   *bin
}

func (o *once) TempDir() string { return o.dir }

func (o *once) Fatalf(format string, args ...any) {
	o.TB.Helper()
	o.b.failed = fmt.Sprintf(format, args...)
	o.TB.Fatalf(format, args...)
}

func (o *once) Fatal(args ...any) {
	o.TB.Helper()
	o.b.failed = fmt.Sprint(args...)
	o.TB.Fatal(args...)
}

// ModuleDir is the directory of Module: the nearest directory at or above the
// working directory whose go.mod declares it.
func ModuleDir() (string, error) {
	d, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if declares(filepath.Join(d, "go.mod"), Module) {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod of %s at or above the working directory", Module)
		}
		d = parent
	}
}

// declares reports whether the go.mod at p declares module mod.
func declares(p, mod string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == mod
		}
	}
	return false
}
