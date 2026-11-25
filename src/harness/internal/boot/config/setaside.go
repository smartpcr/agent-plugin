package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// BackupInfix joins a layer file's name and the Unix time in milliseconds of a
// backup left by SetAside: layer.yml.bak-<unix ms> (boot.md §6.1).
const BackupInfix = ".bak-"

// maxBackupTries bounds the search for a free backup name: one per millisecond
// from the clock's.
const maxBackupTries = 1000

// SetAside moves the layer file at path aside, the write of `dsh profile reset`
// (DH-D-BOOT-3): under the layer lock every edit takes (LockGuard), it renames the
// file to path+BackupInfix+<unix ms of at>, or of the first later millisecond whose
// name no file has, and returns the backup's path. It never deletes or rewrites a
// file, so the backup holds the layer's exact bytes; a later read of path finds no
// file, which the stack reads as an empty layer. A missing file has nothing to set
// aside: SetAside returns "" and no error. A lock not taken in time is ReasonBusy.
func SetAside(ctx context.Context, path string, at time.Time) (string, error) {
	var backup string
	err := LockGuard(ctx, 0)(path, func() error {
		info, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case info.IsDir():
			return fmt.Errorf("layer %s is a directory", path)
		}
		free, err := freeBackup(path, at.UnixMilli())
		if err != nil {
			return err
		}
		if err := os.Rename(path, free); err != nil {
			return fmt.Errorf("set layer %s aside: %w", path, err)
		}
		backup = free
		return fsx.SyncDir(filepath.Dir(path))
	})
	return backup, err
}

// freeBackup returns path.bak-<ms> for the first millisecond from ms whose name no
// file has. The caller holds the layer lock, which every writer of the layer and
// of its backups takes, so the name stays free until the rename.
func freeBackup(path string, ms int64) (string, error) {
	for i := int64(0); i < maxBackupTries; i++ {
		candidate := path + BackupInfix + strconv.FormatInt(ms+i, 10)
		_, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("backup %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("no free backup name for %s after %d tries", path, maxBackupTries)
}
