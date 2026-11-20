//go:build unix

package fsx

import (
	"errors"
	"os"
	"syscall"
)

func renameReplace(stage, target string, _ bool) error {
	return os.Rename(stage, target)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func syncRootDir(root *os.Root, dir string) error {
	d, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// copyACL is a no-op on POSIX: the mode bits, already copied, are the access control.
func copyACL(string, string) error { return nil }

func isInUse(error) bool { return false }

func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }

// linkThenRemove publishes stage as target only if target is free, where the kernel
// has no no-replace rename: link(2) fails when the name exists.
func linkThenRemove(stage, target string) error {
	if err := os.Link(stage, target); err != nil {
		return err
	}
	_ = os.Remove(stage)
	return nil
}
