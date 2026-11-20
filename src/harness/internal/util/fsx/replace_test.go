package fsx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReplaceNeverExposesPartialFile is TC-04b: a reader looping over the file (through
// ReadFile, which never blocks a replace) while Replace publishes new content 200
// times sees only one of the two complete contents, and never a missing file.
func TestReplaceNeverExposesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := bytes.Repeat([]byte("o"), 64<<10)
	next := bytes.Repeat([]byte("n"), 24<<10)
	if err := WriteFile(path, old, Options{Perm: 0o644}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	reads := 0
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			got, err := ReadFile(path)
			if err != nil {
				done <- err
				return
			}
			reads++
			if !bytes.Equal(got, old) && !bytes.Equal(got, next) {
				done <- errors.New("read a partial file of " + strconv.Itoa(len(got)) + " bytes")
				return
			}
		}
	}()
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	for i := range 200 {
		content := next
		if i%2 == 1 {
			content = old
		}
		if err := WriteFile(path, content, Options{}); err != nil {
			t.Fatalf("replace %d: %v", i, err)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if reads == 0 {
		t.Fatal("the reader never read the file")
	}
	assertNoStaged(t, filepath.Dir(path))
}

func assertNoStaged(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), StagePrefix) {
			t.Errorf("staged file %s left behind", e.Name())
		}
	}
}

func TestReplaceModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")

	err := WriteFile(path, []byte("x"), Options{})
	if !errors.Is(err, ErrPermRequired) {
		t.Fatalf("absent target without a mode: got %v, want ErrPermRequired", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target created despite the error: %v", err)
	}

	if err := WriteFile(path, []byte("one"), Options{Perm: 0o640, Durable: true}); err != nil {
		t.Fatal(err)
	}
	want := fs.FileMode(0o640)
	if runtime.GOOS == "windows" {
		want = 0o666
	}
	assertMode(t, path, want)

	// Perm zero and KeepPerm both keep the existing mode.
	if err := WriteFile(path, []byte("two"), Options{}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, want)
	if err := WriteFile(path, []byte("three"), Options{Perm: 0o600, KeepPerm: true, Durable: true}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, want)

	// A stated mode applies.
	if err := WriteFile(path, []byte("four"), Options{Perm: 0o444}); err != nil {
		t.Fatal(err)
	}
	ro := fs.FileMode(0o444)
	assertMode(t, path, ro)
	if got, _ := os.ReadFile(path); string(got) != "four" {
		t.Fatalf("content %q", got)
	}
	_ = os.Chmod(path, 0o644)
	assertNoStaged(t, dir)
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode %v, want %v", got, want)
	}
}

func TestReplaceWriterFailureLeavesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := WriteFile(path, []byte("keep"), Options{Perm: 0o644}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := Replace(path, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return boom
	}, Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the writer's error", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep" {
		t.Fatalf("target changed to %q", got)
	}
	assertNoStaged(t, dir)

	if err := WriteFile(filepath.Join(dir, "missing", "f"), nil, Options{Perm: 0o644}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing directory: got %v", err)
	}
	if err := WriteFile(dir, nil, Options{Perm: 0o644}); err == nil {
		t.Fatal("replacing a directory succeeded")
	}
	assertNoStaged(t, dir)
}

func TestCreateIfAbsentOneWinner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claim")
	const n = 50
	errs := make([]error, n)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = CreateIfAbsent(path, Bytes([]byte("writer "+strconv.Itoa(i))), Options{Perm: 0o644})
		}()
	}
	wg.Wait()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner >= 0:
			t.Fatalf("writers %d and %d both created the file", winner, i)
		case err == nil:
			winner = i
		case !errors.Is(err, fs.ErrExist):
			t.Errorf("writer %d: %v, want fs.ErrExist", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no writer created the file")
	}
	if got, _ := os.ReadFile(path); string(got) != "writer "+strconv.Itoa(winner) {
		t.Fatalf("content %q is not the winner's", got)
	}
	assertNoStaged(t, dir)
}

func TestRootedReplaceAndCreate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	if err := CreateIfAbsentIn(root, "sub/a", Bytes([]byte("1")), Options{Perm: 0o644, Durable: true}); err != nil {
		t.Fatal(err)
	}
	if err := CreateIfAbsentIn(root, "sub/a", Bytes([]byte("2")), Options{Perm: 0o644}); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second create: %v, want fs.ErrExist", err)
	}
	if err := ReplaceIn(root, "sub/a", Bytes([]byte("3")), Options{Durable: true, KeepPerm: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "sub", "a")); string(got) != "3" {
		t.Fatalf("content %q", got)
	}
	if err := ReplaceIn(root, "../escape", Bytes(nil), Options{Perm: 0o644}); err == nil {
		t.Fatal("a name escaping the root was accepted")
	}
	if err := ReplaceIn(root, "nodir/x", Bytes(nil), Options{Perm: 0o644}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing directory: %v", err)
	}
	boom := errors.New("boom")
	if err := ReplaceIn(root, "sub/a", func(io.Writer) error { return boom }, Options{}); !errors.Is(err, boom) {
		t.Fatalf("writer failure: %v", err)
	}
	assertNoStaged(t, filepath.Join(dir, "sub"))
}

func TestRootedCreateWithoutLinks(t *testing.T) {
	saved := rootLink
	t.Cleanup(func() { rootLink = saved })
	rootLink = func(*os.Root, string, string) error { return errors.ErrUnsupported }

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	if err := CreateIfAbsentIn(root, "a", Bytes([]byte("first")), Options{Perm: 0o644}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a")); string(got) != "first" {
		t.Fatalf("content %q", got)
	}
	// The name is taken: the reservation fails before anything is renamed.
	if err := os.Remove(filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootLink = func(*os.Root, string, string) error { return errors.ErrUnsupported }
	err = linkOrReserve(root, "missing-stage", "b")
	if err == nil {
		t.Fatal("renaming a missing staged file succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "b")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the reservation was not removed: %v", err)
	}
	if err := linkOrReserve(root, "missing-stage", "a"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("taken name: %v, want fs.ErrExist", err)
	}
	assertNoStaged(t, dir)
}

func TestSweepStaged(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, StagePrefix+"old")
	fresh := filepath.Join(dir, StagePrefix+"new")
	other := filepath.Join(dir, "keep")
	for _, p := range []string{stale, fresh, other} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, StagePrefix+"dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, long, long); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, long, long); err != nil {
		t.Fatal(err)
	}
	n, err := SweepStaged(dir, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v; want 1, nil", n, err)
	}
	for p, want := range map[string]bool{stale: false, fresh: true, other: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: exists=%v, want %v", filepath.Base(p), err == nil, want)
		}
	}
	if _, err := SweepStaged(filepath.Join(dir, "none"), time.Hour); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing directory: %v", err)
	}
}

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: %v, want ErrLocked", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock while held: %v, want the deadline", err)
	}

	got := make(chan *FileLock, 1)
	go func() {
		l, err := Lock(context.Background(), path)
		if err != nil {
			t.Error(err)
		}
		got <- l
	}()
	t.Cleanup(func() {
		select {
		case l := <-got:
			if l != nil {
				_ = l.Unlock()
			}
		default:
		}
	})
	select {
	case <-got:
		t.Fatal("Lock returned while the lock was held")
	case <-time.After(20 * time.Millisecond):
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatalf("second Unlock: %v", err)
	}
	select {
	case l := <-got:
		if err := l.Unlock(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lock did not return after the holder unlocked")
	}
	if _, err := TryLock(filepath.Join(t.TempDir(), "no", "lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lock in a missing directory: %v", err)
	}
}
