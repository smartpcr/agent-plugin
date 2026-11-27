// Package dshbin builds the dsh binary once per test binary (DH-D-UTIL-7, C54):
// a thin wrapper over the M1 test kit's launcher.Build of ./cmd/dsh. The first
// Path call builds it into a directory that outlives that test; every later call
// in the same test binary returns the same path. The directory is removed when
// the package's tests end, so a package that calls Path runs its tests through
// Main from its TestMain; Path refuses to build otherwise.
package dshbin

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// Module is the import path of the module whose ./cmd/dsh is built.
const Module = "github.com/smartpcr/agent-plugin/src/harness"

var (
	mu     sync.Mutex
	inMain bool   // Main is running the tests
	dir    string // the build directory, removed by Main
	path   string // the built binary, once built
	failed string // the first build's failure, once it failed
)

// Main runs the tests of m, then removes the binary Path built and exits with
// the tests' status. A TestMain calls it:
//
//	func TestMain(m *testing.M) { dshbin.Main(m) }
func Main(m *testing.M) {
	mu.Lock()
	inMain = true
	mu.Unlock()
	code := m.Run()
	mu.Lock()
	if dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "dshbin: removing %s: %v\n", dir, err)
			if code == 0 {
				code = 1
			}
		}
	}
	mu.Unlock()
	os.Exit(code)
}

// Path returns the dsh binary built from this checkout's ./cmd/dsh, building it
// on the first call. A failed build fails t and every later caller, without a
// second build.
func Path(t testing.TB) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	switch {
	case path != "":
		return path
	case failed != "":
		t.Fatalf("dshbin: the build of ./cmd/dsh failed earlier in this test binary:\n%s", failed)
	case !inMain:
		t.Fatal("dshbin: the package's TestMain must run its tests through dshbin.Main, which removes the binary built once per test binary")
	}
	mod, err := ModuleDir()
	if err != nil {
		failed = err.Error()
		t.Fatalf("dshbin: %v", err)
	}
	d, err := os.MkdirTemp("", "dshbin-")
	if err != nil {
		failed = err.Error()
		t.Fatalf("dshbin: %v", err)
	}
	dir = d
	path = launcher.Build(&once{TB: t, dir: d}, mod, "./cmd/dsh")
	return path
}

// once is the test that triggers the build, with the build directory standing in
// for its TempDir (which would be removed when that test ends), and its fatal
// message recorded so later callers fail with it. Path holds mu around it.
type once struct {
	testing.TB
	dir string
}

func (o *once) TempDir() string { return o.dir }

func (o *once) Fatalf(format string, args ...any) {
	o.TB.Helper()
	failed = fmt.Sprintf(format, args...)
	o.TB.Fatalf(format, args...)
}

func (o *once) Fatal(args ...any) {
	o.TB.Helper()
	failed = fmt.Sprint(args...)
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
