package local

import (
	"errors"
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

// AfterStaging runs f once each publication's content is written to the staged
// file, before its mode, sync, close and rename: the window in which a
// cancellation must still stop the write.
func AfterStaging(f func()) (restore func()) {
	wrap := func(publish func(*os.Root, string, func(io.Writer) error, fsx.Options) error) func(*os.Root, string, func(io.Writer) error, fsx.Options) error {
		return func(root *os.Root, name string, w func(io.Writer) error, o fsx.Options) error {
			return publish(root, name, func(out io.Writer) error {
				if err := w(out); err != nil {
					return err
				}
				f()
				return nil
			}, o)
		}
	}
	replaceIn, createIn = wrap(fsx.ReplaceIn), wrap(fsx.CreateIfAbsentIn)
	return func() { replaceIn, createIn = fsx.ReplaceIn, fsx.CreateIfAbsentIn }
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

// AroundRecheck runs f(n) before the n-th (from zero) call of each publication's
// BeforePublish check, so a test can act between a blocked publication attempt
// and its retry. The returned func restores the default.
func AroundRecheck(f func(n int)) (restore func()) {
	wrap := func(publish func(*os.Root, string, func(io.Writer) error, fsx.Options) error) func(*os.Root, string, func(io.Writer) error, fsx.Options) error {
		return func(root *os.Root, name string, w func(io.Writer) error, o fsx.Options) error {
			check, n := o.BeforePublish, 0
			o.BeforePublish = func() error {
				f(n)
				n++
				if check == nil {
					return nil
				}
				return check()
			}
			return publish(root, name, w, o)
		}
	}
	replaceIn, createIn = wrap(fsx.ReplaceIn), wrap(fsx.CreateIfAbsentIn)
	return func() { replaceIn, createIn = fsx.ReplaceIn, fsx.CreateIfAbsentIn }
}

// FailReadlink makes every read of a link's target fail, as when the link is
// removed between the Lstat that saw it and the Readlink. The returned func
// restores the default.
func FailReadlink() (restore func()) {
	readlink = func(string) (string, error) { return "", errors.New("injected readlink failure") }
	return func() { readlink = os.Readlink }
}
