package watch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// settle is long against on-access scanners, which can stall a single write by a
// few hundred milliseconds: a stall longer than settle is a genuinely settled
// state, and would rightly be reported as a second change.
const (
	settle   = 500 * time.Millisecond
	deadline = 5 * time.Second         // a notice must arrive within this
	quiet    = 1200 * time.Millisecond // and no second one within this after it
)

// recorder collects callbacks.
type recorder chan error

func (r recorder) changed(err error) { r <- err }

// one waits for exactly one notice: it must arrive within deadline, and no other
// may follow within the quiet window.
func (r recorder) one(t *testing.T, what string) error {
	t.Helper()
	var got error
	select {
	case got = <-r:
	case <-time.After(deadline):
		t.Fatalf("%s: no notice within %v", what, deadline)
	}
	r.none(t, what+" (second notice)")
	return got
}

func (r recorder) none(t *testing.T, what string) {
	t.Helper()
	select {
	case err := <-r:
		t.Fatalf("%s: unexpected notice (err %v)", what, err)
	case <-time.After(quiet):
	}
}

func service(t *testing.T, opt watch.Options) *watch.Service {
	t.Helper()
	s := watch.New(opt)
	t.Cleanup(func() { s.Close() })
	return s
}

func start(t *testing.T, s *watch.Service, target watch.Target) (watch.Watch, recorder) {
	t.Helper()
	if target.Settle == 0 {
		target.Settle = settle
	}
	r := make(recorder, 64)
	w, err := s.Watch(t.Context(), target, r.changed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, r
}

// write makes one save of body at path, so the watcher has exactly one change to
// settle on. A new file appears whole (atomic replace): os.WriteFile creates it
// empty first, and on-access scanners can hold the create for longer than a
// settle window, which is a genuinely settled empty file and a second save. An
// existing file is written in place without truncating first, for the same reason.
func write(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		if err := fsx.WriteFile(path, []byte(body), fsx.Options{Perm: 0o644}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt([]byte(body), 0); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(len(body))); err != nil {
		t.Fatal(err)
	}
}

var pollFast = watch.Options{FilePoll: 30 * time.Millisecond, DirPoll: 30 * time.Millisecond}

// TestAtomicReplaceOneNotice: a file replaced atomically yields exactly one notice
// for it within the deadline, with notifications and with polling (TC-04b).
func TestAtomicReplaceOneNotice(t *testing.T) {
	for _, mode := range []watch.Mode{watch.Notify, watch.Poll, watch.Auto} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			f := filepath.Join(dir, "settings.json")
			write(t, f, "old")
			w, r := start(t, service(t, pollFast), watch.Target{Path: f, Mode: mode})
			if want := map[watch.Mode]watch.Mode{watch.Auto: watch.Notify, watch.Notify: watch.Notify, watch.Poll: watch.Poll}[mode]; w.Mode() != want {
				t.Fatalf("mode %v, want %v", w.Mode(), want)
			}
			r.none(t, "before any change")
			if err := fsx.WriteFile(f, []byte("new content"), fsx.Options{}); err != nil {
				t.Fatal(err)
			}
			if err := r.one(t, "atomic replace"); err != nil {
				t.Fatalf("notice carried %v", err)
			}
		})
	}
}

// TestFileTransitions: written in place, deleted, created again; one notice each.
func TestFileTransitions(t *testing.T) {
	for _, mode := range []watch.Mode{watch.Notify, watch.Poll} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			f := filepath.Join(dir, "f.txt")
			write(t, f, "1")
			sibling := filepath.Join(dir, "other.txt")
			_, r := start(t, service(t, pollFast), watch.Target{Path: f, Mode: mode})
			write(t, sibling, "noise")
			r.none(t, "sibling written")
			write(t, f, "two")
			r.one(t, "in place")
			if err := os.Remove(f); err != nil {
				t.Fatal(err)
			}
			r.one(t, "deleted")
			write(t, f, "3")
			r.one(t, "recreated")
		})
	}
}

func TestDirectoryDepth(t *testing.T) {
	for _, mode := range []watch.Mode{watch.Notify, watch.Poll} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			deep := filepath.Join(dir, "sub", "deeper")
			if err := os.MkdirAll(deep, 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, "sub", "x"), "x")
			write(t, filepath.Join(deep, "y"), "y")
			s := service(t, pollFast)
			_, r0 := start(t, s, watch.Target{Path: dir, Mode: mode})
			_, r1 := start(t, s, watch.Target{Path: dir, Depth: 1, Mode: mode})

			write(t, filepath.Join(dir, "a"), "added")
			r0.one(t, "depth 0: added")
			r1.one(t, "depth 1: added")

			for i := range 100 {
				write(t, filepath.Join(dir, "a"), strconv.Itoa(i))
			}
			r0.one(t, "depth 0: burst")
			r1.one(t, "depth 1: burst")

			write(t, filepath.Join(dir, "sub", "x"), "changed below")
			r1.one(t, "depth 1: entry of a subdirectory")
			r0.none(t, "depth 0: entry of a subdirectory")

			write(t, filepath.Join(deep, "y"), "two levels down")
			r1.none(t, "depth 1: two levels down")
			r0.none(t, "depth 0: two levels down")

			if err := os.Remove(filepath.Join(dir, "a")); err != nil {
				t.Fatal(err)
			}
			r0.one(t, "depth 0: removed")
			r1.one(t, "depth 1: removed")
		})
	}
}

// TestMissingParents: a watch on a/b/c.txt before a exists follows the chain as
// it is created, deleted and created again.
func TestMissingParents(t *testing.T) {
	for _, mode := range []watch.Mode{watch.Notify, watch.Poll} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			a := filepath.Join(dir, "a")
			f := filepath.Join(a, "b", "c.txt")
			_, r := start(t, service(t, pollFast), watch.Target{Path: f, Mode: mode})
			for round := range 2 {
				if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
					t.Fatal(err)
				}
				write(t, f, "here")
				r.one(t, "created, round "+strconv.Itoa(round))
				write(t, f, "changed")
				r.one(t, "changed, round "+strconv.Itoa(round))
				if err := os.RemoveAll(a); err != nil {
					t.Fatal(err)
				}
				r.one(t, "chain deleted, round "+strconv.Itoa(round))
			}
		})
	}
}

// TestCloseStopsCallbacks: after Close returns no callback runs; closing twice,
// closing through the context and closing the service are all harmless.
func TestCloseStopsCallbacks(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	write(t, f, "1")
	s := service(t, pollFast)
	w, r := start(t, s, watch.Target{Path: f, Mode: watch.Notify})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	write(t, f, "after close")
	r.none(t, "after Close")

	ctx, cancel := context.WithCancel(t.Context())
	r2 := make(recorder, 4)
	if _, err := s.Watch(ctx, watch.Target{Path: f, Settle: settle}, r2.changed); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.Watch(ctx, watch.Target{Path: f}, r2.changed); err == nil {
		t.Fatal("Watch with a cancelled context")
	}
	write(t, f, "after cancel")
	r2.none(t, "after the context ended")

	_, r3 := start(t, s, watch.Target{Path: f, Mode: watch.Poll})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	write(t, f, "after service close")
	r3.none(t, "after the service closed")
	if _, err := s.Watch(t.Context(), watch.Target{Path: f}, r3.changed); !errors.Is(err, watch.ErrClosed) {
		t.Fatalf("Watch on a closed service: %v", err)
	}
}

func TestBadTargets(t *testing.T) {
	s := service(t, watch.Options{MaxWatches: 1})
	noop := func(error) {}
	for _, tg := range []watch.Target{{}, {Path: "x", Depth: 2}, {Path: "x", Mode: watch.Mode(9)}} {
		if _, err := s.Watch(t.Context(), tg, noop); !errors.Is(err, watch.ErrBadTarget) {
			t.Errorf("%+v: %v", tg, err)
		}
	}
	if _, err := s.Watch(t.Context(), watch.Target{Path: "x"}, nil); !errors.Is(err, watch.ErrBadTarget) {
		t.Errorf("nil callback: %v", err)
	}
	start(t, s, watch.Target{Path: t.TempDir()})
	if _, err := s.Watch(t.Context(), watch.Target{Path: "y"}, noop); !errors.Is(err, watch.ErrTooMany) {
		t.Errorf("over the cap: %v", err)
	}
	if watch.Mode(7).String() != "Mode(7)" || watch.Auto.String() != "auto" {
		t.Error("Mode.String")
	}
}

// TestRecreatedParentWithinSettle: the watched directory is deleted and recreated
// inside one settle window, so the kernel registration dies with no settled notice
// in between. The watch must re-register the new directory: later saves are still
// reported in Notify mode, not missed for ever.
func TestRecreatedParentWithinSettle(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "p")
	f := filepath.Join(p, "f.txt")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, f, "0")
	_, r := start(t, service(t, watch.Options{}), watch.Target{Path: f, Mode: watch.Notify, Settle: 400 * time.Millisecond})
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	// Windows may hold the deleted name until the notifier lets go of it.
	for stop := time.Now().Add(deadline); ; {
		err := os.Mkdir(p, 0o755)
		if err == nil {
			break
		}
		if time.Now().After(stop) {
			t.Fatal(err)
		}
		<-time.After(5 * time.Millisecond)
	}
	write(t, f, "1")
	if err := r.one(t, "recreated"); err != nil {
		t.Fatalf("recreated: %v", err)
	}
	for i := range 2 {
		write(t, f, "after "+strconv.Itoa(i))
		if err := r.one(t, "save after recreation "+strconv.Itoa(i)); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
}

// TestDirectoryAtomicReplaceSameMeta: an entry of a watched directory replaced
// atomically by a file of the same length with the same modification time is a
// change: the entry's identity moved. One notice.
func TestDirectoryAtomicReplaceSameMeta(t *testing.T) {
	for _, mode := range []watch.Mode{watch.Notify, watch.Poll} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			f := filepath.Join(dir, "settings.json")
			write(t, f, "aaaa")
			info, err := os.Stat(f)
			if err != nil {
				t.Fatal(err)
			}
			// Prepared outside the watched directory, so the swap is one rename.
			next := filepath.Join(t.TempDir(), "next")
			write(t, next, "bbbb")
			if err := os.Chtimes(next, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			_, r := start(t, service(t, pollFast), watch.Target{Path: dir, Mode: mode})
			r.none(t, "before any change")
			if err := os.Rename(next, f); err != nil {
				t.Fatal(err)
			}
			if after, err := os.Stat(f); err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
				t.Fatalf("replacement does not keep size and time: %v %v", after, err)
			}
			if err := r.one(t, "atomic replace keeping size and time"); err != nil {
				t.Fatalf("notice carried %v", err)
			}
		})
	}
}
