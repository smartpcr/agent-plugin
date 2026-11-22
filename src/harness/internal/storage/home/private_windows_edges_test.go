//go:build windows

package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"golang.org/x/sys/windows"
)

// A directory whose access list inherits from its parent is not private, even
// when every entry names a trusted account: a later change to the parent would
// reach it.
func TestCheckPrivateRefusesAnInheritingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	err := CheckPrivate(dir)
	if !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "inherits from the parent directory") {
		t.Fatalf("CheckPrivate of an inheriting directory = %v", err)
	}
}

// A file with a NULL access list grants everyone access, so it is not private.
func TestCheckPrivateRefusesANullAccessList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "f")
	f, err := CreatePrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	err = CheckPrivate(path)
	if !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "no access list") {
		t.Fatalf("CheckPrivate of a NULL access list = %v", err)
	}
}
