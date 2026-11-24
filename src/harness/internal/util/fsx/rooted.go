package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// ReplaceIn is Replace by name inside root: name is resolved by root, so neither
// the staged file nor the target can escape it through a symbolic link or "..".
func ReplaceIn(root *os.Root, name string, write func(io.Writer) error, opt Options) error {
	return publishIn(root, name, write, opt, false)
}

// CreateIfAbsentIn is CreateIfAbsent by name inside root. The staged file is linked
// to the name; where the filesystem refuses links, the name is reserved with an
// exclusive create and the staged file renamed over the reservation, so a second
// creator still sees fs.ErrExist.
func CreateIfAbsentIn(root *os.Root, name string, write func(io.Writer) error, opt Options) error {
	return publishIn(root, name, write, opt, true)
}

// rootLink links within a root; tests replace it to take the no-link path.
var rootLink = (*os.Root).Link

// rootedSync is the durability step after a rooted publication; tests replace it
// to observe or fail it.
var rootedSync = syncPublishedIn

func publishIn(root *os.Root, name string, write func(io.Writer) error, opt Options, exclusive bool) error {
	name = filepath.ToSlash(filepath.Clean(name))
	mode, keepACL, err := resultMode(root.Lstat, name, opt, exclusive)
	if err != nil {
		return err
	}
	dir := path.Dir(name)
	if err := fault(stepStage); err != nil {
		return err
	}
	f, stage, err := createStageIn(root, dir)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = f.Close()
			_ = root.Remove(stage)
		}
	}()
	if err := fill(f, write, mode, opt.Durable); err != nil {
		return err
	}
	if keepACL {
		if err := copyACL(filepath.Join(root.Name(), name), filepath.Join(root.Name(), stage)); err != nil {
			return err
		}
	}
	if err := beforePublish(opt); err != nil {
		return err
	}
	if err := fault(stepPublish); err != nil {
		return err
	}
	if exclusive {
		err = linkOrReserve(root, stage, name)
	} else {
		err = retryInUse(func() error { return root.Rename(stage, name) })
	}
	if err != nil {
		return &os.LinkError{Op: "publish", Old: stage, New: name, Err: kind(err)}
	}
	published = true
	if opt.Durable {
		if err := rootedSync(root, dir, name); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrDurabilityUnknown, name, err)
		}
	}
	return nil
}

// linkOrReserve publishes stage as name only if name is free. A successful link
// leaves the staged name behind, which is then removed.
func linkOrReserve(root *os.Root, stage, name string) error {
	err := rootLink(root, stage, name)
	if err == nil {
		_ = root.Remove(stage)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}
	// No links here: reserve the name, then move the content over the reservation.
	r, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_ = r.Close()
	if err := retryInUse(func() error { return root.Rename(stage, name) }); err != nil {
		_ = root.Remove(name)
		return err
	}
	return nil
}

// createStageIn creates an owner-only staged file in dir inside root.
func createStageIn(root *os.Root, dir string) (*os.File, string, error) {
	for range 16 {
		var b [8]byte
		_, _ = rand.Read(b[:])
		stage := path.Join(dir, StagePrefix+hex.EncodeToString(b[:]))
		f, err := root.OpenFile(stage, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, stage, err
	}
	return nil, "", &fs.PathError{Op: "createstage", Path: dir, Err: fs.ErrExist}
}
