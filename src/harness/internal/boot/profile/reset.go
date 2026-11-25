package profile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Reset sets the profile layer of the profile called name aside, the library half
// of `dsh profile reset <name>` (DH-D-BOOT-3): config.SetAside renames
// <home>/config/profiles/<name>/layer.yml, under the layer lock every edit takes,
// to layer.yml.bak-<unix ms>, a name no file has yet, and Reset returns the
// backup's path. Nothing is deleted or rewritten: the backup holds the layer's
// exact bytes, and the home layer, other profiles and every other file of the
// profile are left as they are. The next launch reads the missing layer as an
// empty one.
//
// A name that is not one path element is ReasonUnknown. A profile with no layer
// file has nothing to set aside: Reset returns "" and no error, and creates
// nothing. A layer that cannot be locked in time is config.ReasonBusy; any other
// failure is ReasonBadLayer.
func Reset(ctx context.Context, homeRoot, name string) (string, error) {
	return reset(ctx, homeRoot, name, time.Now)
}

func reset(ctx context.Context, homeRoot, name string, now func() time.Time) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || filepath.Base(name) != name {
		return "", herr.Newf(ReasonUnknown, "no profile %q", name)
	}
	path := LayerPath(homeRoot, name)
	// Checked before the lock, so that resetting a profile without a layer creates
	// neither its directory nor a lock file; SetAside checks again under the lock.
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	backup, err := config.SetAside(ctx, path, now())
	if err != nil && herr.Kind(err) == "" {
		err = herr.Wrap(ReasonBadLayer, err, "reset profile "+name)
	}
	return backup, err
}
