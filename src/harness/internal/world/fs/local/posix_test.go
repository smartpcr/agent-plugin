//go:build linux || darwin

package local_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// A named pipe is neither a file nor a directory: it is listed and stated as
// other, and reading or writing it as a file is fs/not-regular-file (a read
// never blocks on it).
func TestNamedPipeIsNotARegularFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Fatal(err)
	}
	l := local.New()
	if in, err := l.Stat(ctx, fs.Ref{Path: pipe}); err != nil || !in.Exists || in.Type != fs.TypeOther || in.Size != 0 {
		t.Fatalf("Stat(pipe) = %+v, %v", in, err)
	}
	ents, _, err := l.ReadDir(ctx, fs.Ref{Path: dir}, 0)
	if err != nil || len(ents) != 1 || ents[0].Type != fs.TypeOther {
		t.Fatalf("ReadDir = %+v, %v", ents, err)
	}
	_, _, err = l.Open(ctx, fs.Ref{Path: pipe})
	wantCode(t, "Open a pipe", err, fs.CodeNotRegularFile)
	_, err = l.Write(ctx, fs.Ref{Path: pipe}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, dir))
	wantCode(t, "Write a pipe", err, fs.CodeNotRegularFile)
}

// A component longer than the filesystem allows is fs/io, not taken for a
// missing file the call could create.
func TestOverlongNameIsIO(t *testing.T) {
	dir := t.TempDir()
	long := filepath.Join(dir, strings.Repeat("n", 300), "f")
	_, err := local.New().Stat(context.Background(), fs.Ref{Path: long})
	wantCode(t, "Stat an overlong name", err, fs.CodeIO)
	_, err = local.New().Write(context.Background(), fs.Ref{Path: long}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, dir))
	wantCode(t, "Write an overlong name", err, fs.CodeIO)
}

// chmod sets the mode of p for the rest of the test and restores it after,
// so the temporary directory can be removed.
func chmod(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, fi.Mode().Perm()) })
}

func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind the superuser")
	}
}

// What the host refuses for want of permission is fs/permission-denied: a file
// that cannot be read, a directory that cannot be listed or searched, a name
// that cannot be removed and a write root that cannot be created.
func TestPermissionDeniedIsReported(t *testing.T) {
	skipAsRoot(t)
	ctx := context.Background()
	tmp := t.TempDir()
	l := local.New()

	secret := filepath.Join(tmp, "secret")
	writeFile(t, secret, "s")
	chmod(t, secret, 0o000)
	_, _, err := l.Open(ctx, fs.Ref{Path: secret})
	wantCode(t, "Open an unreadable file", err, fs.CodePermission)

	unlisted := filepath.Join(tmp, "unlisted")
	mkdirs(t, unlisted)
	chmod(t, unlisted, 0o300)
	_, _, err = l.ReadDir(ctx, fs.Ref{Path: unlisted}, 0)
	wantCode(t, "ReadDir an unlistable directory", err, fs.CodePermission)

	closed := filepath.Join(tmp, "closed")
	mkdirs(t, closed)
	writeFile(t, filepath.Join(closed, "f"), "f")
	chmod(t, closed, 0o000)
	_, err = l.Stat(ctx, fs.Ref{Path: filepath.Join(closed, "f")})
	wantCode(t, "Stat below an unsearchable directory", err, fs.CodePermission)

	sealed := filepath.Join(tmp, "sealed")
	mkdirs(t, sealed)
	writeFile(t, filepath.Join(sealed, "f"), "f")
	chmod(t, sealed, 0o555)
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, sealed)
	_, err = l.Delete(ctx, fs.Ref{Path: filepath.Join(sealed, "f")}, fs.NoGuard, p)
	wantCode(t, "Delete in a read-only directory", err, fs.CodePermission)
	if _, err := os.Lstat(filepath.Join(sealed, "f")); err != nil {
		t.Fatalf("the file is gone: %v", err)
	}
	root := filepath.Join(sealed, "R")
	_, err = l.Write(ctx, fs.Ref{Path: filepath.Join(root, "f")}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, root))
	wantCode(t, "create a write root in a read-only directory", err, fs.CodePermission)
}

// A lock directory that group or others may write is refused, so no other
// user can plant lock files in it; the provider never loosens or tightens it.
func TestSharedLockDirIsRefused(t *testing.T) {
	dir, locks := t.TempDir(), filepath.Join(t.TempDir(), "locks")
	mkdirs(t, locks)
	chmod(t, locks, 0o777)
	_, err := local.NewWithOptions(local.Options{LockDir: locks}).Write(context.Background(), fs.Ref{Path: filepath.Join(dir, "f")}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, dir))
	wantCode(t, "write with a world-writable lock directory", err, fs.CodeIO)
	if fi, err := os.Stat(locks); err != nil || fi.Mode().Perm() != 0o777 {
		t.Fatalf("the lock directory's mode changed: %v, %v", fi.Mode(), err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("written despite the refused lock directory: %v", got)
	}
}
