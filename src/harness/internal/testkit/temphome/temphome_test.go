package temphome_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

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
