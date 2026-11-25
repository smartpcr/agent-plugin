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
	pin, err := pinDir(root, dir)
	if err != nil {
		return err
	}
	defer pin.close()
	f, stage, err := pin.createStage(dir)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = f.Close()
			pin.remove(stage)
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
		err = linkOrReserveIn(pin, stage, name, opt.BeforePublish)
	} else {
		err = retryInUse(opt.BeforePublish, func() error { return root.Rename(stage, name) })
	}
	if cerr, ok := refused(err); ok {
		return cerr
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
// leaves the staged name behind, which is then removed. check runs before every
// retried rename of the fallback; a refusal removes the reservation.
func linkOrReserve(root *os.Root, stage, name string, check func() error) error {
	return linkOrReserveIn(dirPin{root: root}, stage, name, check)
}

// linkOrReserveIn is linkOrReserve whose leftovers (the staged name after a link,
// the reservation after a refusal) are removed through pin, in the same directory.
func linkOrReserveIn(pin dirPin, stage, name string, check func() error) error {
	root := pin.root
	err := rootLink(root, stage, name)
	if err == nil {
		pin.remove(stage)
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
	if err := retryInUse(check, func() error { return root.Rename(stage, name) }); err != nil {
		pin.remove(name)
		return err
	}
	return nil
}

// dirPin holds a handle on the directory a publication stages in, opened inside
// root when the stage is created. The staged file is created through it and every
// leftover is removed through it, so when the directory's path is later swapped for
// a link (to outside the root, or elsewhere) the cleanup still reaches the real
// directory instead of being refused by name and leaving the staged file behind.
// Publication itself stays by name through root, so the fence decides as before.
type dirPin struct {
	root *os.Root
	dir  *os.Root // nil only for linkOrReserve without a pin; names go through root
}

// pinDir opens dir inside root. Its error is the stage step's: dir missing, not a
// directory, or resolving outside root (a link). There is no by-name fallback; a
// stage created by name could again be left behind by a later swap.
func pinDir(root *os.Root, dir string) (dirPin, error) {
	d, err := root.OpenRoot(dir)
	if err != nil {
		return dirPin{}, err
	}
	return dirPin{root: root, dir: d}, nil
}

func (p dirPin) close() {
	if p.dir != nil {
		_ = p.dir.Close()
	}
}

// createStage creates the staged file in the pinned directory; stage is its name
// relative to root (dir joined with the staged base name).
func (p dirPin) createStage(dir string) (*os.File, string, error) {
	f, base, err := createStageIn(p.dir, ".")
	if err != nil {
		return nil, "", err
	}
	return f, path.Join(dir, base), nil
}

// remove removes name, a file of the pinned directory, through the pin; by name
// through root when there is no pin or the pinned removal fails.
func (p dirPin) remove(name string) {
	if p.dir != nil && p.dir.Remove(path.Base(name)) == nil {
		return
	}
	_ = p.root.Remove(name)
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
