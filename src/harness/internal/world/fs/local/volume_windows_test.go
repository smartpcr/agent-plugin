//go:build windows

package local_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// Under danger-full-access a move works under the volume root, so a move to
// another volume, which no one root handle can confine, is fs/invalid-path
// before anything changes.
func TestMoveToAnotherVolumeIsRefused(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f")
	writeFile(t, src, "s")
	vol := `Z:`
	if strings.EqualFold(filepath.VolumeName(dir), vol) {
		vol = `Y:`
	}
	// A directory that does not exist on the other volume, so nothing there can
	// change even if the volume exists.
	dst := filepath.Join(vol+`\`, "dsh-local-test-no-such-dir", filepath.Base(dir), "f")
	_, err := local.New().Move(context.Background(), fs.Ref{Path: src}, fs.Ref{Path: dst}, fs.MoveOptions{}, sandboxtest.Policy(sandbox.DangerFullAccess))
	wantCode(t, "move to another volume", err, fs.CodeInvalidPath)
	if b, err := os.ReadFile(src); err != nil || string(b) != "s" {
		t.Fatalf("the source = %q, %v", b, err)
	}
}
