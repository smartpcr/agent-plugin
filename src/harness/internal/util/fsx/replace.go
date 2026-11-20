package fsx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// StagePrefix starts the name of every file fsx stages beside a target. A file with
// this prefix that outlived its call is a leftover; SweepStaged removes it.
const StagePrefix = ".dsh-stage-"

// Options says how a replaced or created file is published (DH-LIB-NODEFS-R1).
type Options struct {
	// Perm is the mode of the result. It is required when the target does not exist
	// and is applied to an existing target unless KeepPerm is set. On Windows only
	// the read-only bit has an effect.
	Perm fs.FileMode
	// KeepPerm keeps the mode (POSIX) or the mode and the access-control list
	// (Windows) of an existing target. An existing target replaced with Perm zero
	// keeps its mode too: a mode is never left to chance.
	KeepPerm bool
	// Durable syncs the file before publication and its directory after it (POSIX),
	// or publishes with MOVEFILE_WRITE_THROUGH (Windows), so the result survives
	// power loss once the call returns nil.
	Durable bool
}

// Replace writes path through write so that any reader sees the complete old or the
// complete new content. The staged file is removed on every failure before
// publication, leaving the target untouched. A failed sync after publication returns
// an error wrapping ErrDurabilityUnknown: the new content is in place.
func Replace(path string, write func(io.Writer) error, opt Options) error {
	return publish(path, write, opt, false)
}

// CreateIfAbsent writes path through write only if no file has that name, safely
// against another goroutine or process doing the same (DH-LIB-NODEFS-R2). When the
// name exists the error matches fs.ErrExist and nothing is changed.
func CreateIfAbsent(path string, write func(io.Writer) error, opt Options) error {
	return publish(path, write, opt, true)
}

// WriteFile replaces path with data.
func WriteFile(path string, data []byte, opt Options) error {
	return Replace(path, Bytes(data), opt)
}

// Bytes returns a writer function that writes data, for Replace and CreateIfAbsent.
func Bytes(data []byte) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	}
}

// statFunc is os.Lstat or a rooted Lstat.
type statFunc func(name string) (fs.FileInfo, error)

// resultMode decides the mode of the published file and whether the existing
// target's access-control list is carried over.
func resultMode(stat statFunc, name string, opt Options, exclusive bool) (mode fs.FileMode, keepACL bool, err error) {
	info, err := stat(name)
	switch {
	case err == nil && exclusive:
		return 0, false, &fs.PathError{Op: "create", Path: name, Err: fs.ErrExist}
	case err == nil && (opt.KeepPerm || opt.Perm == 0):
		return info.Mode().Perm(), opt.KeepPerm, nil
	case err == nil:
		return opt.Perm.Perm(), false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return 0, false, err
	case opt.Perm == 0:
		return 0, false, &fs.PathError{Op: "create", Path: name, Err: ErrPermRequired}
	}
	return opt.Perm.Perm(), false, nil
}

func publish(path string, write func(io.Writer) error, opt Options, exclusive bool) (err error) {
	mode, keepACL, err := resultMode(os.Lstat, path, opt, exclusive)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, StagePrefix+"*")
	if err != nil {
		return err
	}
	stage := f.Name()
	published := false
	defer func() {
		if !published {
			_ = f.Close()
			_ = os.Remove(stage)
		}
	}()
	if err := fill(f, write, mode, opt.Durable); err != nil {
		return err
	}
	if keepACL {
		if err := copyACL(path, stage); err != nil {
			return err
		}
	}
	if exclusive {
		err = renameNoReplace(stage, path, opt.Durable)
	} else {
		err = renameReplace(stage, path, opt.Durable)
	}
	if err != nil {
		return &os.LinkError{Op: "publish", Old: stage, New: path, Err: kind(err)}
	}
	published = true
	if opt.Durable {
		if err := syncDir(dir); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrDurabilityUnknown, path, err)
		}
	}
	return nil
}

// fill runs write on the staged file, sets its final mode, syncs it when durable and
// closes it.
func fill(f *os.File, write func(io.Writer) error, mode fs.FileMode, durable bool) error {
	if err := write(f); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if durable {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	return f.Close()
}

// retryDelays are the waits between attempts of an operation another program blocks
// by holding the target open: eight tries from 20 ms to 200 ms, as upstream
// (atomic-write/src/index.ts:20-23).
var retryDelays = []time.Duration{
	20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond, 80 * time.Millisecond,
	100 * time.Millisecond, 140 * time.Millisecond, 170 * time.Millisecond, 200 * time.Millisecond,
}

// beforeRetry is called before each wait of retryInUse; tests replace it.
var beforeRetry = func(attempt int) {}

// retryInUse runs op until it succeeds, fails with an error that is not "in use",
// or the retry window is spent.
func retryInUse(op func() error) error {
	err := op()
	for attempt := 0; err != nil && isInUse(err) && attempt < len(retryDelays); attempt++ {
		beforeRetry(attempt)
		time.Sleep(retryDelays[attempt])
		err = op()
	}
	return err
}

// SweepStaged removes files in dir whose names start with StagePrefix and that were
// last modified before now minus age, and returns how many it removed. A provider
// that stages in a directory calls it first, with an age of a day.
func SweepStaged(dir string, age time.Duration) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-age)
	removed := 0
	var errs []error
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < len(StagePrefix) || e.Name()[:len(StagePrefix)] != StagePrefix {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
