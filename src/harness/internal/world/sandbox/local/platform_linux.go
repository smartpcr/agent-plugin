//go:build linux

package local

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
)

// Platform is the Linux backend chain, in selection order (DH-D-SANDBOX-4):
// bubblewrap, then Landlock, which is selected when bwrap is absent or fails its
// probe. When neither passes, a confined policy is refused with
// sandbox/unavailable, never run unconfined.
func Platform() []Backend {
	return []Backend{Bwrap(), Landlock(kernel{})}
}

// kernel is the running kernel.
type kernel struct{}

func (kernel) LandlockABI() (int, error) { return launch.LandlockABI() }

// runtimeDir is the user's runtime directory: $XDG_RUNTIME_DIR when it is an
// absolute, clean path, else /run/user/<uid> when it exists, else empty.
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && filepath.IsAbs(d) && filepath.Clean(d) == d {
		return d
	}
	d := "/run/user/" + strconv.Itoa(os.Getuid())
	if fi, err := os.Stat(d); err == nil && fi.IsDir() {
		return d
	}
	return ""
}
