package fsx

import (
	"errors"
	"fmt"
)

// Error kinds fsx adds to those of io/fs (fs.ErrNotExist, fs.ErrExist,
// fs.ErrPermission), so callers branch with errors.Is and never import syscall
// (DH-LIB-NODEFS-R9).
var (
	// ErrInUse: another program holds the file open in a way that blocks the
	// operation (a Windows sharing violation that outlasted the retry window).
	ErrInUse = errors.New("fsx: file in use")
	// ErrCrossDevice: the source and destination are on different volumes.
	ErrCrossDevice = errors.New("fsx: different volumes")
	// ErrDurabilityUnknown: the new content is in place but syncing its directory
	// failed, so whether it survives power loss is unknown.
	ErrDurabilityUnknown = errors.New("fsx: content in place, durability unknown")
	// ErrPermRequired: the target does not exist and the options state no mode.
	ErrPermRequired = errors.New("fsx: a mode is required to create a file")
	// ErrLocked: TryLock found the lock held by another holder.
	ErrLocked = errors.New("fsx: lock held")
)

// kind wraps err with the fsx error kind the platform says it is, if any.
func kind(err error) error {
	switch {
	case err == nil, errors.Is(err, ErrInUse), errors.Is(err, ErrCrossDevice):
		return err
	case isInUse(err):
		return fmt.Errorf("%w: %w", ErrInUse, err)
	case isCrossDevice(err):
		return fmt.Errorf("%w: %w", ErrCrossDevice, err)
	}
	return err
}
