// Package golden is the project's one golden-file helper (QG-6, DH-ADR-TOOLCHAIN-1,
// DH-ADR-TOOLCHAIN-4).
//
//	golden.Assert(t, "render/plain", got)
//
// compares got with testdata/render/plain.golden, relative to the package directory
// go test runs in, and fails the test with a line diff when they differ. The file is
// rewritten only when the test binary is run with the single switch -update:
//
//	go test ./internal/... -run TestRender -update
//
// and never when the CI environment variable is set: there -update fails the test,
// so a CI run cannot re-record what it is meant to check (DH-ADR-TOOLCHAIN-4). No
// environment variable, second flag or per-test option rewrites a golden, so a
// golden changes only through an explicit, reviewable re-record.
package golden

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// update is the one update switch. Registering it here makes a second "-update"
// flag in any test binary that imports this package a duplicate-flag panic, so the
// switch stays single.
var update = flag.Bool("update", false, "rewrite testdata/*.golden files from this run (golden.Assert)")

// Dir is the directory, relative to the package under test, that holds the goldens.
const Dir = "testdata"

// Path returns the file name of the golden called name: testdata/<name>.golden. name
// is slash-separated and may name a subdirectory ("render/plain").
func Path(name string) string {
	return filepath.Join(Dir, filepath.FromSlash(name)+".golden")
}

// Assert fails t when got differs from the golden called name, reporting a line
// diff; with -update it writes got to the golden instead and does not fail.
func Assert[T ~string | ~[]byte](t testing.TB, name string, got T) {
	t.Helper()
	if err := checkName(name); err != nil {
		t.Fatalf("golden: %v", err)
		return
	}
	file := Path(name)
	if *update {
		if ci, set := os.LookupEnv("CI"); set {
			t.Fatalf("golden: -update refused for %s: CI is set (CI=%q); goldens are re-recorded locally and reviewed",
				filepath.ToSlash(file), ci)
			return
		}
		if err := write(file, []byte(got)); err != nil {
			t.Fatalf("golden: update %s: %v", filepath.ToSlash(file), err)
			return
		}
		t.Logf("golden: updated %s", filepath.ToSlash(file))
		return
	}
	want, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("golden: %s does not exist (record it with -update)", filepath.ToSlash(file))
		return
	}
	if err != nil {
		t.Fatalf("golden: %v", err)
		return
	}
	if !bytes.Equal(want, []byte(got)) {
		t.Errorf("golden: %s differs (re-record an intended change with -update)\n%s",
			filepath.ToSlash(file), Diff(string(want), string(got)))
	}
}

// checkName rejects a name that would place the golden outside testdata/.
func checkName(name string) error {
	switch {
	case name == "":
		return errors.New("empty golden name")
	case strings.Contains(name, `\`):
		return fmt.Errorf("golden name %q: use '/' as the separator", name)
	case path.IsAbs(name) || filepath.IsAbs(name) || filepath.VolumeName(name) != "":
		return fmt.Errorf("golden name %q is absolute", name)
	case path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../"):
		return fmt.Errorf("golden name %q is not a clean path below %s/", name, Dir)
	}
	return nil
}

// write stores data at file, creating its directory; an identical file is left
// untouched.
func write(file string, data []byte) error {
	if old, err := os.ReadFile(file); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}
