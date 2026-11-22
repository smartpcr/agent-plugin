package watch_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch/watchtest"
)

func canon(t *testing.T, p string) string {
	t.Helper()
	c, err := fsx.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestFakeRouting: events are hints routed by directory; only the target's name
// (and a real change) produces a notice; the parent directory is what is watched.
func TestFakeRouting(t *testing.T) {
	fake := watchtest.New()
	dir := canon(t, t.TempDir())
	f := filepath.Join(dir, "f")
	write(t, f, "1")
	s := service(t, watch.Options{NewNotifier: fake.Factory()})
	w, r := start(t, s, watch.Target{Path: f, Mode: watch.Notify})
	if got := fake.Watched(); !reflect.DeepEqual(got, []string{dir}) || w.Mode() != watch.Notify {
		t.Fatalf("watched %q in %v", got, w.Mode())
	}
	write(t, f, "silently changed")
	fake.Send(filepath.Join(dir, "unrelated"))
	r.none(t, "event for a sibling")
	fake.Send(f)
	r.one(t, "event for the target")
	fake.Send(f)
	r.none(t, "event without a change")
	w.Close()
	if got := fake.Watched(); len(got) != 0 {
		t.Fatalf("still watched after Close: %q", got)
	}
	s.Close()
	if !fake.Closed() {
		t.Fatal("notifier not closed with the service")
	}
}

// TestFakeRearmOnDirectoryEvent: where identities are reused (ext4 gives a
// directory deleted and made again its old inode number), an event naming the
// watched directory itself, or an overflow, re-adds the directory at the next
// check: the registration may have died with a deleted directory that the
// identity check cannot tell from its successor (M1-G-GATE-15.2).
func TestFakeRearmOnDirectoryEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file IDs are not reused on Windows; the identity check decides there")
	}
	fake := watchtest.New()
	added := make(chan string, 16)
	fake.AddErr = func(dir string) error {
		select {
		case added <- dir:
		default:
		}
		return nil
	}
	dir := canon(t, t.TempDir())
	f := filepath.Join(dir, "f")
	write(t, f, "1")
	start(t, service(t, watch.Options{NewNotifier: fake.Factory()}), watch.Target{Path: f, Mode: watch.Notify})
	readd := func(what string) {
		t.Helper()
		select {
		case got := <-added:
			if got != dir {
				t.Fatalf("%s: added %q, want %q", what, got, dir)
			}
		case <-time.After(deadline):
			t.Fatalf("%s: %s not re-added within %v", what, dir, deadline)
		}
	}
	readd("initial registration")
	fake.Send(dir)
	readd("event naming the directory")
	fake.Fail(watch.ErrOverflow)
	readd("overflow")
}

// TestRearmRefusedDegrades: the watched directory is deleted and recreated with no
// settled check in between; when the notifier then refuses to re-add it, the
// watch does not stay deaf on the dead registration: it switches to polling, says
// so once (ErrDegraded), and still sees the next change.
func TestRearmRefusedDegrades(t *testing.T) {
	fake := watchtest.New()
	var adds atomic.Int32
	fake.AddErr = func(string) error {
		if adds.Add(1) > 1 {
			return errors.New("no more watches")
		}
		return nil
	}
	p := filepath.Join(canon(t, t.TempDir()), "p")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(p, "f")
	write(t, f, "0")
	w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory(), FilePoll: 30 * time.Millisecond}), watch.Target{Path: f, Mode: watch.Notify})
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, f, "1")
	fake.Send(p)
	if err := r.one(t, "re-add refused"); !errors.Is(err, watch.ErrDegraded) || w.Mode() != watch.Poll {
		t.Fatalf("%v in %v", err, w.Mode())
	}
	if got := fake.Watched(); len(got) != 0 {
		t.Fatalf("still registered after degrading: %q", got)
	}
	write(t, f, "two")
	if err := r.one(t, "change while polling"); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileOnReady: a change racing Watch (made while the directory is being
// registered) is reported once the watch is ready.
func TestReconcileOnReady(t *testing.T) {
	fake := watchtest.New()
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	write(t, f, "1")
	fake.AddErr = func(string) error {
		write(t, f, "written while registering")
		fake.AddErr = nil
		return nil
	}
	_, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory()}), watch.Target{Path: f, Mode: watch.Notify})
	if err := r.one(t, "readiness reconcile"); err != nil {
		t.Fatal(err)
	}
}

// TestDegradedNotifications: a notifier that refuses Add, fails later, overflows
// or stays silent still leaves a watch that sees changes, and the degradation is
// reported once.
func TestDegradedNotifications(t *testing.T) {
	t.Run("add fails", func(t *testing.T) {
		fake := watchtest.New()
		fake.AddErr = func(string) error { return errors.New("no more watches") }
		dir := t.TempDir()
		f := filepath.Join(dir, "f")
		w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory(), FilePoll: 30 * time.Millisecond}), watch.Target{Path: f})
		if err := r.one(t, "degraded at start"); !errors.Is(err, watch.ErrDegraded) || w.Mode() != watch.Poll {
			t.Fatalf("%v in %v", err, w.Mode())
		}
		write(t, f, "created")
		if err := r.one(t, "change while polling"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("fails later", func(t *testing.T) {
		fake := watchtest.New()
		f := filepath.Join(t.TempDir(), "f")
		write(t, f, "1")
		w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory(), FilePoll: 30 * time.Millisecond}), watch.Target{Path: f})
		fake.Fail(errors.New("queue broken"))
		if err := r.one(t, "degraded"); !errors.Is(err, watch.ErrDegraded) || w.Mode() != watch.Poll {
			t.Fatalf("%v in %v", err, w.Mode())
		}
		if len(fake.Watched()) != 0 {
			t.Fatalf("still registered: %q", fake.Watched())
		}
		fake.Fail(errors.New("again"))
		r.none(t, "second failure")
		write(t, f, "2")
		r.one(t, "change while polling")
	})
	t.Run("overflow", func(t *testing.T) {
		fake := watchtest.New()
		f := filepath.Join(t.TempDir(), "f")
		write(t, f, "1")
		w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory()}), watch.Target{Path: f, Mode: watch.Notify})
		write(t, f, "missed event")
		fake.Fail(watch.ErrOverflow)
		if err := r.one(t, "re-check after overflow"); err != nil || w.Mode() != watch.Notify {
			t.Fatalf("%v in %v", err, w.Mode())
		}
	})
	t.Run("silent", func(t *testing.T) {
		fake := watchtest.New()
		f := filepath.Join(t.TempDir(), "f")
		write(t, f, "1")
		w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory(), VerifyPoll: 50 * time.Millisecond}), watch.Target{Path: f})
		write(t, f, "nobody told")
		if err := r.one(t, "verification poll"); err != nil || w.Mode() != watch.Notify {
			t.Fatalf("%v in %v", err, w.Mode())
		}
	})
	t.Run("no notifier", func(t *testing.T) {
		s := service(t, watch.Options{
			NewNotifier: func() (watch.Notifier, error) { return nil, errors.New("inotify unavailable") },
			FilePoll:    30 * time.Millisecond,
		})
		f := filepath.Join(t.TempDir(), "f")
		w, r := start(t, s, watch.Target{Path: f})
		if err := r.one(t, "degraded"); !errors.Is(err, watch.ErrDegraded) || w.Mode() != watch.Poll {
			t.Fatalf("%v in %v", err, w.Mode())
		}
		_, quietPoll := start(t, s, watch.Target{Path: f, Mode: watch.Poll})
		quietPoll.none(t, "a Poll watch is not degraded")
		write(t, f, "x")
		r.one(t, "change while polling")
		quietPoll.one(t, "change while polling")
	})
	t.Run("remote", func(t *testing.T) {
		fake := watchtest.New()
		s := service(t, watch.Options{NewNotifier: fake.Factory(), FilePoll: 30 * time.Millisecond,
			IsRemote: func(string) (bool, error) { return true, nil }})
		f := filepath.Join(t.TempDir(), "f")
		w, r := start(t, s, watch.Target{Path: f})
		if w.Mode() != watch.Poll || len(fake.Watched()) != 0 {
			t.Fatalf("remote: %v, watched %q", w.Mode(), fake.Watched())
		}
		r.none(t, "polling chosen, not degraded")
		write(t, f, "x")
		r.one(t, "change on a remote filesystem")
		w2, _ := start(t, s, watch.Target{Path: f, Mode: watch.Notify})
		if w2.Mode() != watch.Notify {
			t.Fatalf("Notify on remote: %v", w2.Mode())
		}
	})
	t.Run("entry cap", func(t *testing.T) {
		fake := watchtest.New()
		dir := t.TempDir()
		for _, n := range []string{"a", "b", "c", "d"} {
			write(t, filepath.Join(dir, n), n)
		}
		w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory(), MaxEntries: 3, DirPoll: 30 * time.Millisecond}), watch.Target{Path: dir})
		if err := r.one(t, "over the cap"); !errors.Is(err, watch.ErrEntriesCap) || w.Mode() != watch.Poll {
			t.Fatalf("%v in %v", err, w.Mode())
		}
		write(t, filepath.Join(dir, "e"), "e")
		r.one(t, "change while polling")
	})
	t.Run("directory grows past the cap", func(t *testing.T) {
		fake := watchtest.New()
		dir := canon(t, t.TempDir())
		w, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory(), MaxEntries: 1, DirPoll: 30 * time.Millisecond}), watch.Target{Path: dir, Mode: watch.Notify})
		write(t, filepath.Join(dir, "a"), "a")
		write(t, filepath.Join(dir, "b"), "b")
		fake.Send(filepath.Join(dir, "b"))
		if err := r.one(t, "grew past the cap"); !errors.Is(err, watch.ErrEntriesCap) || w.Mode() != watch.Poll {
			t.Fatalf("%v in %v", err, w.Mode())
		}
	})
}

// TestSymlinkSameTarget: a watch through a symbolic link watches the file it
// names (DH-LIB-CHOKIDAR-R8).
func TestSymlinkSameTarget(t *testing.T) {
	realDir := t.TempDir()
	f := filepath.Join(realDir, "f")
	write(t, f, "1")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	fake := watchtest.New()
	_, r := start(t, service(t, watch.Options{NewNotifier: fake.Factory()}), watch.Target{Path: filepath.Join(link, "f"), Mode: watch.Notify})
	if got := fake.Watched(); !reflect.DeepEqual(got, []string{canon(t, realDir)}) {
		t.Fatalf("watched %q, want the real directory", got)
	}
	write(t, f, "2")
	fake.Send(filepath.Join(canon(t, realDir), "f"))
	r.one(t, "change through the link")
}
