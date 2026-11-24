package fstest_test

import (
	"context"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

//dsh:test DH-T-FS-8, DH-T-FS-9
func TestMemFSPassesConformance(t *testing.T) {
	fstest.Run(t, func(t *testing.T) fstest.Subject {
		m := fstest.NewMemFS()
		return fstest.Subject{FS: m, Symlink: m.Symlink}
	})
}

//dsh:test DH-T-SANDBOX-2
func TestZeroPolicyIsUnavailableNotAllowedOrDenied(t *testing.T) {
	var p sandbox.Policy
	m := fstest.NewMemFS()
	path := t.TempDir() + "/f"
	_, err := m.Write(context.Background(), fs.Ref{Path: path}, []byte("x"), fs.NoGuard, p)
	if got := herr.Kind(err); got != sandbox.CodeUnavailable {
		t.Fatalf("write with a zero policy: error %v (code %q), want %q", err, got, sandbox.CodeUnavailable)
	}
	if err := fs.Fence(p, fs.OpWrite, path); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("fence with a zero policy: %v, want %s", err, sandbox.CodeUnavailable)
	}
	if err := fs.Fence(p, fs.OpRead, path); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("fence of a read with a zero policy: %v, want %s", err, sandbox.CodeUnavailable)
	}
	if in, _ := m.Stat(context.Background(), fs.Ref{Path: path}); in.Exists {
		t.Fatal("a zero policy created the file")
	}
}
