package local

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

func wantKind(t *testing.T, what string, err error, want herr.Reason) {
	t.Helper()
	if got := herr.Kind(err); got != want {
		t.Fatalf("%s: %v (code %q), want %q", what, err, got, want)
	}
}

// A target's lock is held across providers sharing the lock directory: a second
// locker waits until its context ends and then fails fs/aborted or fs/timeout,
// and takes the lock once the holder releases it.
func TestLockWaitsForTheHolderUntilTheContextEnds(t *testing.T) {
	dir := t.TempDir()
	a, b := NewWithOptions(Options{LockDir: dir}), NewWithOptions(Options{LockDir: dir})
	const k = fs.TargetKey("target")
	held, err := a.lock(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	// A release must run once; the cleanup covers a test that fails early.
	release := sync.OnceFunc(held)
	t.Cleanup(release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = b.lock(ctx, k)
	wantKind(t, "a cancelled wait for the holder", err, fs.CodeAborted)
	ctx, cancel = context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	_, err = b.lock(ctx, k)
	wantKind(t, "a wait past its deadline", err, fs.CodeTimeout)

	// The failed waits released b's stripe: another target of b locks at once.
	other, err := b.lock(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	got, err := b.lock(context.Background(), k)
	if err != nil {
		t.Fatalf("after the holder released: %v", err)
	}
	got()
}

// Locking the same target twice in one call (a move onto itself is refused
// later) takes its locks once and does not deadlock.
func TestLockTakesARepeatedTargetOnce(t *testing.T) {
	l := NewWithOptions(Options{LockDir: t.TempDir()})
	release, err := l.lock(context.Background(), "k", "k")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release2, err := l.lock(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	release2()
}

// A lock file that cannot be opened is fs/io, not a wait.
func TestLockFileThatCannotBeOpenedIsIO(t *testing.T) {
	l := NewWithOptions(Options{LockDir: t.TempDir()})
	if err := os.Mkdir(l.lockFile("k"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := l.lock(context.Background(), "k")
	wantKind(t, "a lock file that is a directory", err, fs.CodeIO)
	// The stripe was released with the failure.
	release, err := l.lock(context.Background(), "j")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// mapErr gives every error an fs code; an error that has one keeps it.
func TestMapErrCodes(t *testing.T) {
	coded := herr.New(fs.CodeStaleVersion, "stale")
	// On Windows syscall.ENOTDIR is ERROR_PATH_NOT_FOUND, which is a missing path.
	notDir := fs.CodeNotDirectory
	if runtime.GOOS == "windows" {
		notDir = fs.CodeNotFound
	}
	for _, c := range []struct {
		err  error
		want herr.Reason
	}{
		{coded, fs.CodeStaleVersion},
		{&os.PathError{Op: "openat", Path: "x", Err: errors.New("path escapes from parent")}, fs.CodeSandboxDenied},
		{fmt.Errorf("w: %w", context.Canceled), fs.CodeAborted},
		{fmt.Errorf("w: %w", context.DeadlineExceeded), fs.CodeTimeout},
		{&os.PathError{Op: "open", Path: "x", Err: iofs.ErrNotExist}, fs.CodeNotFound},
		{&os.PathError{Op: "mkdir", Path: "x", Err: iofs.ErrExist}, fs.CodeAlreadyExists},
		{&os.PathError{Op: "open", Path: "x", Err: iofs.ErrPermission}, fs.CodePermission},
		{&os.PathError{Op: "open", Path: "x", Err: syscall.ENOTDIR}, notDir},
		{errors.New("disk on fire"), fs.CodeIO},
	} {
		if got := herr.Kind(mapErr(c.err, "p")); got != c.want {
			t.Errorf("mapErr(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	if mapErr(nil, "p") != nil {
		t.Error("mapErr(nil) is not nil")
	}
	if got := mapErr(coded, "p"); got != coded {
		t.Errorf("a coded error was rewrapped: %v", got)
	}
}

// rooted marks only a link refusal and a changed tree; any other failure of a
// rooted step is the step's own.
func TestRootedClassifies(t *testing.T) {
	if rooted(nil) != nil {
		t.Fatal("rooted(nil) is not nil")
	}
	if err := rooted(&os.PathError{Op: "openat", Path: "x", Err: errors.New("path escapes from parent")}); !errors.Is(err, errRelinked) {
		t.Fatalf("an escape = %v, want errRelinked", err)
	}
	var ch changed
	if err := rooted(&os.PathError{Op: "open", Path: "x", Err: iofs.ErrNotExist}); !errors.As(err, &ch) {
		t.Fatalf("a vanished name = %v, want changed", err)
	}
	own := errors.New("disk on fire")
	if err := rooted(own); err != own {
		t.Fatalf("another failure = %v, want it unchanged", err)
	}
}

// fakeInfo is a FileInfo whose Sys is sys.
type fakeInfo struct {
	iofs.FileInfo
	sys any
}

func (f fakeInfo) Sys() any { return f.sys }

type withUID struct{ Uid uint32 }
type withoutUID struct{ Ino uint64 }
type stringUID struct{ Uid string }

// ownerUID reads the Uid of a POSIX stat and reports none for anything else.
func TestOwnerUID(t *testing.T) {
	for _, c := range []struct {
		name string
		sys  any
		uid  int
		ok   bool
	}{
		{"no sys", nil, 0, false},
		{"nil pointer", (*withUID)(nil), 0, false},
		{"not a struct", 42, 0, false},
		{"no Uid field", withoutUID{Ino: 1}, 0, false},
		{"Uid not unsigned", stringUID{Uid: "1000"}, 0, false},
		{"pointer", &withUID{Uid: 1000}, 1000, true},
		{"value", withUID{Uid: 7}, 7, true},
	} {
		if uid, ok := ownerUID(fakeInfo{sys: c.sys}); uid != c.uid || ok != c.ok {
			t.Errorf("%s: ownerUID = %d, %v; want %d, %v", c.name, uid, ok, c.uid, c.ok)
		}
	}
	fi, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid, ok := ownerUID(fi)
	switch {
	case ok && uid != os.Geteuid():
		t.Fatalf("a directory this process made is owned by uid %d, not %d", uid, os.Geteuid())
	case !ok && os.Geteuid() >= 0:
		t.Fatalf("no owner uid on a platform with uids (euid %d)", os.Geteuid())
	}
}
