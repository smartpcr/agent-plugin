package fstest_test

import (
	"context"
	"crypto/sha256"
	"io"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
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

// TestMemFSOpenBounded: a bound of zero or below the size copies that prefix
// with metadata only, no digest; a bound at or above it copies the file with
// its digest. A file that resolves outside within is refused before the copy.
//
//dsh:test DH-T-CONTEXT-6
func TestMemFSOpenBounded(t *testing.T) {
	ctx := context.Background()
	m := fstest.NewMemFS()
	dir := t.TempDir()
	ws, out := filepath.Join(dir, "ws"), filepath.Join(dir, "out")
	full := sandboxtest.Policy(sandbox.DangerFullAccess)
	for p, text := range map[string]string{filepath.Join(ws, "f"): "hello", filepath.Join(out, "secret"): "OUTSIDE"} {
		if _, err := m.Write(ctx, fs.Ref{Path: p}, []byte(text), fs.NoGuard, full); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		limit  int64
		want   string
		digest bool
	}{{0, "", false}, {3, "hel", false}, {5, "hello", true}, {9, "hello", true}} {
		rc, in, err := m.OpenBounded(ctx, fs.Ref{Path: filepath.Join(ws, "f")}, ws, c.limit)
		if err != nil {
			t.Fatalf("limit %d: %v", c.limit, err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(got) != c.want || in.Version.HasDigest != c.digest || in.Size != 5 {
			t.Fatalf("limit %d: %q digest=%v size=%d, want %q digest=%v size 5", c.limit, got, in.Version.HasDigest, in.Size, c.want, c.digest)
		}
		if c.digest && in.Version.Digest != sha256.Sum256([]byte("hello")) {
			t.Fatalf("limit %d: the digest is not the content's", c.limit)
		}
	}
	if err := m.Symlink(filepath.Join(out, "secret"), filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	rc, _, err := m.OpenBounded(ctx, fs.Ref{Path: filepath.Join(ws, "link")}, ws, 64)
	if herr.Kind(err) != fs.CodeSandboxDenied || rc != nil {
		t.Fatalf("a link outside within: reader=%v err=%v, want fs/sandbox-denied", rc != nil, err)
	}
}
