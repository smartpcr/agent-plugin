package local

import (
	"fmt"
	"io"
	"os"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// SetBeforeOpenRoot runs f just before each mutation opens its root handle,
// after the fence admitted the target. The returned func restores the default.
func SetBeforeOpenRoot(f func(dir string)) (restore func()) {
	beforeOpenRoot = f
	return func() { beforeOpenRoot = nil }
}

// FailSyncAfterPublish makes every publication report a failed sync after the
// new content is in place, as fsx does when the directory sync fails.
func FailSyncAfterPublish() (restore func()) {
	fail := func(publish func(*os.Root, string, func(io.Writer) error, fsx.Options) error) func(*os.Root, string, func(io.Writer) error, fsx.Options) error {
		return func(root *os.Root, name string, w func(io.Writer) error, o fsx.Options) error {
			if err := publish(root, name, w, o); err != nil {
				return err
			}
			return fmt.Errorf("%w: sync %s: injected fault", fsx.ErrDurabilityUnknown, name)
		}
	}
	replaceIn, createIn = fail(fsx.ReplaceIn), fail(fsx.CreateIfAbsentIn)
	return func() { replaceIn, createIn = fsx.ReplaceIn, fsx.CreateIfAbsentIn }
}
