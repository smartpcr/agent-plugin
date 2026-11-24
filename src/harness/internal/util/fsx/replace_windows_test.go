//go:build windows

package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// holdOpen opens path without sharing delete access, as an editor or a virus scanner
// does, so a rename over it fails with a sharing violation until the handle closes.
func holdOpen(t *testing.T, path string) windows.Handle {
	t.Helper()
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func shortRetries(t *testing.T) {
	savedDelays, savedHook := retryDelays, beforeRetry
	t.Cleanup(func() { retryDelays, beforeRetry = savedDelays, savedHook })
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
}

//dsh:test DH-T-NODEFS-1
func TestReplaceRetriesWhileHeldOpen(t *testing.T) {
	shortRetries(t)
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := holdOpen(t, path)
	retried := 0
	beforeRetry = func(attempt int) {
		retried++
		if attempt == 0 {
			_ = windows.CloseHandle(h)
		}
	}
	if err := WriteFile(path, []byte("new"), Options{}); err != nil {
		t.Fatal(err)
	}
	if retried == 0 {
		t.Fatal("the rename was not retried")
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("content %q", got)
	}
}

//dsh:test DH-T-NODEFS-1
func TestReplaceGivesUpWithErrInUse(t *testing.T) {
	shortRetries(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := holdOpen(t, path)
	t.Cleanup(func() { windows.CloseHandle(h) })
	err := WriteFile(path, []byte("new"), Options{Durable: true})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("got %v, want ErrInUse", err)
	}
	if !errors.Is(err, fs.ErrPermission) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("the platform cause is lost: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("target changed to %q", got)
	}
	assertNoStaged(t, dir)
}

func TestMoveFileExFallback(t *testing.T) {
	t.Cleanup(func() { usePOSIXRename = true })
	usePOSIXRename = false
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := WriteFile(path, []byte("one"), Options{Perm: 0o644, Durable: true}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("two"), Options{Durable: true}); err != nil {
		t.Fatal(err)
	}
	if err := CreateIfAbsent(path, Bytes(nil), Options{Perm: 0o644}); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("create over an existing file: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Fatalf("content %q", got)
	}
	assertNoStaged(t, dir)
}

// TestDurableReadOnlyReplace: a durable publication of a read-only result cannot
// flush its new name after a POSIX rename (that needs a writable handle), so it
// reports ErrDurabilityUnknown with the content in place and no stage left behind;
// the MoveFileEx path writes the rename through and succeeds.
func TestDurableReadOnlyReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ro")
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	err := WriteFile(path, []byte("x"), Options{Perm: 0o444, Durable: true})
	if !errors.Is(err, ErrDurabilityUnknown) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("read-only durable publish: %v", err)
	}
	if got, _ := ReadFile(path); string(got) != "x" {
		t.Fatalf("content %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
	if _, err := ReadFile(path + ".none"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}

	usePOSIXRename = false
	t.Cleanup(func() { usePOSIXRename = true })
	other := filepath.Join(dir, "ro2")
	t.Cleanup(func() { os.Chmod(other, 0o644) })
	if err := WriteFile(other, []byte("y"), Options{Perm: 0o444, Durable: true}); err != nil {
		t.Fatalf("write-through publish: %v", err)
	}
	if got, _ := ReadFile(other); string(got) != "y" {
		t.Fatalf("content %q", got)
	}
}

func TestErrorKinds(t *testing.T) {
	if !errors.Is(kind(windows.ERROR_NOT_SAME_DEVICE), ErrCrossDevice) {
		t.Fatal("cross-device not recognised")
	}
	if err := kind(ErrInUse); err != ErrInUse {
		t.Fatalf("kind re-wrapped %v", err)
	}
	other := errors.New("other")
	if kind(other) != other || kind(nil) != nil {
		t.Fatal("kind changed an unrelated error")
	}
}

func TestKeepPermCopiesACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A protected DACL granting only the owner.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, _ := sd.DACL()
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	before := daclString(t, path)
	if err := WriteFile(path, []byte("y"), Options{KeepPerm: true}); err != nil {
		t.Fatal(err)
	}
	if after := daclString(t, path); after != before {
		t.Fatalf("DACL %s, want %s", after, before)
	}
	if err := copyACL(filepath.Join(filepath.Dir(path), "none"), path); err == nil {
		t.Fatal("copying the ACL of a missing file succeeded")
	}
}

func daclString(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

// TestRefusalDuringRetryPublishesNothing: a publication another program blocks is
// retried, and BeforePublish runs again before every retry. A refusal raised while
// the publication waited (here: the handle is released and the call cancelled in
// the same backoff) leaves the old bytes and no staged file, and comes back as the
// hook's own error rather than a failed publication.
func TestRefusalDuringRetryPublishesNothing(t *testing.T) {
	cancelled := errors.New("cancelled during backoff")
	for _, tc := range []struct {
		name    string
		publish func(dir string, opt Options) error
	}{
		{"replace", func(dir string, opt Options) error {
			return Replace(filepath.Join(dir, "f"), Bytes([]byte("new")), opt)
		}},
		{"replace-in", func(dir string, opt Options) error {
			return withRoot(dir, func(r *os.Root) error { return ReplaceIn(r, "f", Bytes([]byte("new")), opt) })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortRetries(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "f")
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			h := holdOpen(t, path)
			released, stop, checks := false, false, 0
			t.Cleanup(func() {
				if !released {
					windows.CloseHandle(h)
				}
			})
			beforeRetry = func(int) {
				if !released {
					released, stop = true, true
					_ = windows.CloseHandle(h)
				}
			}
			opt := Options{Durable: true, BeforePublish: func() error {
				checks++
				if stop {
					return cancelled
				}
				return nil
			}}
			err := tc.publish(dir, opt)
			if !errors.Is(err, cancelled) {
				t.Fatalf("publish: %v, want the refusal", err)
			}
			var le *os.LinkError
			if errors.As(err, &le) {
				t.Fatalf("the refusal came back as a failed publication: %v", err)
			}
			if !released || checks < 2 {
				t.Fatalf("released %v, BeforePublish ran %d times; want a blocked first attempt and a re-check", released, checks)
			}
			if got, _ := os.ReadFile(path); string(got) != "old" {
				t.Fatalf("target holds %q, want the old bytes", got)
			}
			assertNoStaged(t, dir)
		})
	}
}
