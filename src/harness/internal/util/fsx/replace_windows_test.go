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

func TestDurableReadOnlyReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro")
	if err := WriteFile(path, []byte("x"), Options{Perm: 0o444, Durable: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	if got, _ := ReadFile(path); string(got) != "x" {
		t.Fatalf("content %q", got)
	}
	if _, err := ReadFile(path + ".none"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
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
