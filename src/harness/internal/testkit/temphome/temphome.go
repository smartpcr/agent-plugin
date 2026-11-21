// Package temphome gives a test a private harness home (DH-D-UTIL-6). The home
// lives under t.TempDir and is removed when the test ends; a home that cannot be
// removed (a child still holding a file in it) fails the test, so a leak shows as
// a failure rather than as a stray directory (TC-06b).
package temphome

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// Dir is the root of a fresh home that does not exist yet: the shape a boot test
// hands to the harness, which creates the home itself. It is removed, and checked
// gone, when the test ends.
func Dir(t testing.TB) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "home")
	t.Cleanup(func() {
		if err := removeAll(root); err != nil {
			t.Errorf("temphome: removing %s: %v", root, err)
			return
		}
		if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("temphome: %s survives the test (%v)", root, err)
		}
	})
	return root
}

// removeAll retries for up to two seconds: Windows releases a stopped process's
// handles on the home (its working directory, open files) shortly after the
// process is reported gone.
func removeAll(root string) error {
	var err error
	for range 40 {
		if err = os.RemoveAll(root); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// New is a created, private home: every directory of its layout exists and passes
// the permission check boot runs.
func New(t testing.TB) home.Layout {
	t.Helper()
	l := home.New(Dir(t))
	if err := home.Ensure(l); err != nil {
		t.Fatalf("temphome: %v", err)
	}
	return l
}

// Write writes text to path, creating its parent directories private to the user.
func Write(t testing.TB, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("temphome: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("temphome: %v", err)
	}
}

// Layer writes text as the profile patch of profile name in the home at root and
// returns its path.
func Layer(t testing.TB, root, name, text string) string {
	t.Helper()
	path := profile.LayerPath(root, name)
	Write(t, path, text)
	return path
}

// UserLayer writes text as the user layer of the home at root and returns its path.
func UserLayer(t testing.TB, root, text string) string {
	t.Helper()
	path := profile.UserLayerPath(root)
	Write(t, path, text)
	return path
}
