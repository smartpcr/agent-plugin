package temphome_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// TC-06b: a kit home passes boot's permission check while its test runs and no
// longer exists once the test ends.
func TestTempHomeTornDownByCleanup(t *testing.T) {
	var l home.Layout
	var patch string
	t.Run("use", func(t *testing.T) {
		l = temphome.New(t)
		if err := home.Check(l); err != nil {
			t.Fatalf("the kit home fails the boot check: %v", err)
		}
		patch = temphome.Layer(t, l.Root(), "probe", "[]\n")
		if _, err := os.Stat(patch); err != nil {
			t.Fatal(err)
		}
	})
	for _, path := range []string{l.Root(), patch} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s survives its test (%v)", path, err)
		}
	}
}

// UserLayer writes the user layer where boot reads it, creating its parents.
func TestUserLayerIsWrittenWhereBootReadsIt(t *testing.T) {
	l := temphome.New(t)
	path := temphome.UserLayer(t, l.Root(), "[]\n")
	if want := profile.UserLayerPath(l.Root()); path != want {
		t.Fatalf("UserLayer wrote %s, want %s", path, want)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "[]\n" {
		t.Fatalf("the user layer holds %q (%v)", b, err)
	}
}

// A write the kit cannot make fails its test: a parent that is a file, or a path
// that is a directory.
func TestWriteFailsItsTestOnAnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"parent is a file":    filepath.Join(file, "child.txt"),
		"path is a directory": dir,
	} {
		t.Run(name, func(t *testing.T) {
			r := &recorder{TB: t}
			if !r.run(func() { temphome.Write(r, path, "x") }) {
				t.Fatalf("Write to %s did not fail its test", path)
			}
			if got := r.failed(); len(got) != 1 || !strings.HasPrefix(got[0], "temphome: ") {
				t.Fatalf("failures %q, want one temphome error", got)
			}
		})
	}
}

// homeTaken is a recorder whose TempDir already holds a file named "home", so the
// home Dir names cannot be created.
type homeTaken struct {
	*recorder
	dir string
}

func (h homeTaken) TempDir() string { return h.dir }

// New fails its test when the home cannot be created, and still removes the path.
func TestNewFailsItsTestWhenTheHomeCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "home"), []byte("not a home"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	if !r.run(func() { temphome.New(homeTaken{r, dir}) }) {
		t.Fatal("New over an uncreatable home did not fail its test")
	}
	if got := r.failed(); len(got) != 1 || !strings.HasPrefix(got[0], "temphome: ") {
		t.Fatalf("failures %q, want one temphome error", got)
	}
	r.cleanup()
	if got := r.failed(); len(got) != 1 {
		t.Fatalf("the cleanup of the failed home reported %q", got[1:])
	}
	if _, err := os.Stat(filepath.Join(dir, "home")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the path of the failed home survives its cleanup (%v)", err)
	}
}

// A home that cannot be removed when its test ends fails that test, after the
// retries, instead of leaving a stray directory (TC-06b).
func TestUnremovableHomeFailsItsTest(t *testing.T) {
	r := &recorder{TB: t}
	root := temphome.Dir(r)
	temphome.Write(t, filepath.Join(root, "held.txt"), "held")
	release := holdOpen(t, root, filepath.Join(root, "held.txt"))
	r.cleanup()
	release()
	got := r.failed()
	if len(got) == 0 {
		t.Skip("this host removes a held home (running as root?)")
	}
	if len(got) != 1 || !strings.Contains(got[0], "temphome: removing "+root) {
		t.Fatalf("failures %q, want one naming the home it could not remove", got)
	}
}
