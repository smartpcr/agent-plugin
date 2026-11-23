//go:build windows

package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// shortName returns the 8.3 short form of path, or "" when the volume keeps none.
func shortName(t *testing.T, path string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, path) {
		return ""
	}
	return short
}

// TestIdentityShortNameAndCase: one file reached by its long name, by its 8.3 short
// name and in a different letter case has one identity and one canonical path.
//
//dsh:test DH-T-NODEFS-3
func TestIdentityShortNameAndCase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "A Long Directory Name")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "a long file name.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	spellings := map[string]string{"long": path, "upper case": strings.ToUpper(path), "lower case": strings.ToLower(path)}
	if short := shortName(t, path); short != "" {
		spellings["short"] = short
	} else {
		t.Logf("the volume of %s keeps no 8.3 names; the short-name row is not run", path)
	}
	want, err := ID(path)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := Canonical(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range spellings {
		if got, err := ID(p); err != nil || got != want {
			t.Errorf("%s (%s): identity %v %v, want %v", name, p, got, err, want)
		}
		if got, err := Canonical(p); err != nil || got != canon {
			t.Errorf("%s (%s): canonical %q %v, want %q", name, p, got, err, canon)
		}
	}
}
