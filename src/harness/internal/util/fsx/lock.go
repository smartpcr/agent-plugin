package fsx

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

// FileLock is an operating-system lock held on a lock file (DH-LIB-NODEFS-R3). The
// kernel releases it when the holder's process ends, so a crash never leaves a lock
// for a later process to break by guesswork. Locks are advisory: they exclude only
// other lockers.
type FileLock struct {
	mu     sync.Mutex
	f      *os.File
	beyond bool
}

// TryLock takes the lock on path, creating the lock file if needed. When another
// holder (in this or another process) has it, the error matches ErrLocked.
func TryLock(path string) (*FileLock, error) { return tryLock(path, false) }

// TryLockBeyondEnd is TryLock on a lock that never refuses a reader of the lock
// file: on Windows the locked byte lies far beyond the end of the file, where no
// read reaches (on Unix an flock never refuses a read). Lockers of one file must
// all use the same form. The session lease's liveness lock uses it
// (session-store.md §6.2, DH-T-STORE-7).
func TryLockBeyondEnd(path string) (*FileLock, error) { return tryLock(path, true) }

func tryLock(path string, beyond bool) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f, beyond); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f, beyond: beyond}, nil
}

// lockBackoff is the longest wait between attempts of Lock.
const lockBackoff = 100 * time.Millisecond

// Lock takes the lock on path, waiting while another holder has it until ctx ends.
func Lock(ctx context.Context, path string) (*FileLock, error) {
	wait := 5 * time.Millisecond
	for {
		l, err := TryLock(path)
		if !errors.Is(err, ErrLocked) {
			return l, err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, context.Cause(ctx)
		case <-t.C:
		}
		wait = min(wait*2, lockBackoff)
	}
}

// Unlock releases the lock. The lock file stays, so the next locker finds it; a
// second Unlock is a no-op.
func (l *FileLock) Unlock() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := errors.Join(unlockFile(l.f, l.beyond), l.f.Close())
	l.f = nil
	return err
}
