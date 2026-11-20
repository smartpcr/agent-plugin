package watch

import (
	"context"
	"errors"
	"hash/fnv"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

var isRemote = fsx.IsRemote

func canonical(abs string) string {
	if c, err := fsx.Canonical(abs); err == nil {
		return c
	}
	return abs
}

// watch is one target. Everything below the channels is owned by its goroutine
// (and by start, which runs before the goroutine does).
type watch struct {
	s       *Service
	t       Target
	changed func(error)
	mode    atomic.Int32

	kick    chan struct{}
	fails   chan error
	closing chan struct{}
	exited  chan struct{}
	once    sync.Once

	dirs     map[string]bool
	last     snap
	startErr error
	pending  bool
	every    time.Duration
}

func newWatch(s *Service, t Target, changed func(error)) *watch {
	return &watch{
		s: s, t: t, changed: changed,
		kick:    make(chan struct{}, 1),
		fails:   make(chan error, 1),
		closing: make(chan struct{}),
		exited:  make(chan struct{}),
		dirs:    map[string]bool{},
	}
}

func (w *watch) Mode() Mode { return Mode(w.mode.Load()) }

func (w *watch) Close() error {
	w.once.Do(func() { close(w.closing) })
	<-w.exited
	return nil
}

// event is called by the dispatcher (holding the service lock) for a path in one
// of w's directories; "" means "re-check regardless". It never blocks.
func (w *watch) event(p string) {
	if p != "" && !w.relevant(p) {
		return
	}
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// degrade is called by the dispatcher when notification failed. It never blocks.
func (w *watch) degrade(err error) {
	select {
	case w.fails <- err:
	default:
	}
}

// relevant reports whether an event for p can change the target: p is the
// target, one of its ancestors (the chain to a missing target, a directory being
// removed), or an entry of the target directory within Depth.
func (w *watch) relevant(p string) bool {
	t := w.t.Path
	if same(p, t) || isAncestor(p, t) {
		return true
	}
	parent := filepath.Dir(p)
	if same(parent, t) {
		return true
	}
	return w.t.Depth == 1 && same(filepath.Dir(parent), t)
}

func same(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isAncestor(a, p string) bool {
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		if same(parent, a) {
			return true
		}
		p = parent
	}
}

// start takes the baseline, picks the mode, registers the directories and then
// reconciles once, so a change racing Watch is reported (DH-LIB-CHOKIDAR-R4).
func (w *watch) start() {
	w.last = w.snapshot()
	switch {
	case w.t.Mode == Poll:
		w.mode.Store(int32(Poll))
	case w.s.n == nil:
		w.mode.Store(int32(Poll))
		w.startErr = errors.Join(ErrDegraded, w.s.nErr)
	case w.t.Mode == Auto && w.remote():
		w.mode.Store(int32(Poll))
	default:
		w.mode.Store(int32(Notify))
		if err := w.sync(w.last.n); err != nil {
			w.toPoll()
			w.startErr = err
		}
	}
	if cur := w.snapshot(); !cur.same(w.last) {
		w.last, w.pending = cur, true
	}
}

func (w *watch) remote() bool {
	r, err := w.s.opt.IsRemote(nearestDir(w.t.Path))
	return err == nil && r
}

// nearestDir is the closest existing directory at or above p.
func nearestDir(p string) string {
	for {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// wanted is the set of directories to receive events from: for an existing
// directory target, itself, its parent and (Depth 1) its subdirectories; for
// anything else the nearest existing directory above it.
func (w *watch) wanted(entries int) (map[string]bool, error) {
	t := w.t.Path
	want := map[string]bool{}
	info, err := os.Stat(t)
	if err != nil || !info.IsDir() {
		want[nearestDir(filepath.Dir(t))] = true
		return want, nil
	}
	want[t] = true
	if parent := filepath.Dir(t); parent != t {
		want[parent] = true
	}
	if entries > w.s.opt.MaxEntries {
		return nil, ErrEntriesCap
	}
	if w.t.Depth == 1 {
		entries, _ := os.ReadDir(t)
		for _, e := range entries {
			if e.IsDir() {
				want[filepath.Join(t, e.Name())] = true
			}
		}
	}
	return want, nil
}

// sync makes the registered directories the wanted ones; entries is the target
// directory's current entry count, checked against the cap.
func (w *watch) sync(entries int) error {
	want, err := w.wanted(entries)
	if err != nil {
		return err
	}
	for d := range w.dirs {
		if !want[d] {
			w.s.unsubscribe(w, d)
			delete(w.dirs, d)
		}
	}
	for _, d := range slices.Sorted(maps.Keys(want)) {
		// Subscribe even when already registered: the service re-adds a directory
		// deleted and recreated since, whose kernel registration died with it.
		if err := w.s.subscribe(w, d); err != nil {
			return err
		}
		w.dirs[d] = true
	}
	return nil
}

func (w *watch) toPoll() {
	w.release()
	w.mode.Store(int32(Poll))
}

func (w *watch) release() {
	for d := range w.dirs {
		w.s.unsubscribe(w, d)
	}
	clear(w.dirs)
}

// interval is how often the watch checks without an event: the poll interval in
// Poll mode, the verification interval for a file in Auto mode, else never.
func (w *watch) interval() time.Duration {
	switch {
	case w.Mode() == Poll && w.last.dir:
		return w.s.opt.DirPoll
	case w.Mode() == Poll:
		return w.s.opt.FilePoll
	case w.t.Mode == Auto && !w.last.dir:
		return w.s.opt.VerifyPoll
	}
	return 0
}

// maxHold bounds, in settle windows, how long a still-moving target defers its
// notice.
const maxHold = 20

func (w *watch) run(ctx context.Context) {
	defer close(w.exited)
	defer w.s.forget(w)
	defer w.release()

	settle := time.NewTimer(time.Hour)
	settle.Stop()
	defer settle.Stop()
	var ticker *time.Ticker
	var tick <-chan time.Time
	retick := func() {
		every := w.interval()
		if every == w.every {
			return
		}
		w.every = every
		if ticker != nil {
			ticker.Stop()
			ticker, tick = nil, nil
		}
		if every > 0 {
			ticker = time.NewTicker(every)
			tick = ticker.C
		}
	}
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	retick()
	seen := w.last
	var held time.Time

	switch {
	case w.startErr != nil:
		w.notify(w.startErr)
	case w.pending:
		w.notify(nil)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closing:
			return
		case <-w.kick:
			settle.Reset(w.t.Settle)
		case err := <-w.fails:
			if w.Mode() != Poll {
				w.toPoll()
				w.last = w.snapshot()
				seen = w.last
				retick()
				w.notify(err)
			}
		case <-tick:
			// Debounce on what the previous tick saw, so a change settles once
			// the target stops moving.
			if cur := w.snapshot(); !cur.same(seen) {
				seen = cur
				settle.Reset(w.t.Settle)
			}
		case <-settle.C:
			// A change has settled only when the target looks the same as at the
			// previous check: if it still moved, wait another settle window (up to
			// maxHold, so a target that never stops is still reported).
			cur := w.snapshot()
			if !cur.same(seen) && (held.IsZero() || time.Since(held) < maxHold*w.t.Settle) {
				if held.IsZero() {
					held = time.Now()
				}
				seen = cur
				settle.Reset(w.t.Settle)
				continue
			}
			held = time.Time{}
			var err error
			if w.Mode() != Poll {
				if err = w.sync(cur.n); err != nil {
					w.toPoll()
				}
			}
			moved := !cur.same(w.last)
			w.last, seen = cur, cur
			retick()
			switch {
			case err != nil:
				w.notify(err)
			case moved:
				w.notify(nil)
			}
		}
	}
}

// notify runs the callback unless the watch is closing.
func (w *watch) notify(err error) {
	select {
	case <-w.closing:
		return
	default:
	}
	w.changed(err)
}

// snap is what a check compares: a file's identity, size and times; a
// directory's identity and a digest of its entries (names, identities, kinds,
// and the size and time of files) to Depth.
type snap struct {
	exists, dir bool
	v           fsx.Version
	n           int
	sum         uint64
}

// same reports whether two snapshots show no change. A file snapshot taken while
// the file could not be opened (a scanner or writer holding it) has no identity;
// against it only size and modification time are compared, so a transient open
// failure is not itself a change.
func (a snap) same(b snap) bool {
	if a.exists && b.exists && !a.dir && !b.dir && (a.v.ID == fsx.FileID{} || b.v.ID == fsx.FileID{}) {
		return a.v.Size == b.v.Size && a.v.ModNs == b.v.ModNs
	}
	return a == b
}

func (w *watch) snapshot() snap {
	t := w.t.Path
	info, err := os.Stat(t)
	if err != nil {
		return snap{}
	}
	if !info.IsDir() {
		v, err := fsx.Stat(t)
		if err != nil {
			// Raced a replace: fall back to what os.Stat saw.
			v = fsx.Version{Size: info.Size(), ModNs: info.ModTime().UnixNano()}
		}
		return snap{exists: true, v: v}
	}
	s := snap{exists: true, dir: true}
	if id, err := fsx.ID(t); err == nil {
		s.v.ID = id
	}
	h := fnv.New64a()
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if depth == 0 {
				s.n++
			}
			h.Write([]byte(e.Name()))
			// The entry's identity: an atomic replace that keeps the size and
			// restores the time is still a new file. An entry that cannot be
			// opened right now hashes as unknown; that passes once it settles back.
			if id, err := fsx.ID(filepath.Join(dir, e.Name())); err == nil {
				h.Write([]byte{0})
				h.Write(strconv.AppendUint(nil, id.Dev, 10))
				h.Write([]byte{':'})
				h.Write(strconv.AppendUint(nil, id.Ino, 10))
			} else {
				h.Write([]byte{0, '?'})
			}
			if e.IsDir() {
				h.Write([]byte{0, 'd'})
				if depth < w.t.Depth {
					h.Write([]byte{'{'})
					walk(filepath.Join(dir, e.Name()), depth+1)
					h.Write([]byte{'}'})
				}
				continue
			}
			h.Write([]byte{0, 'f'})
			// Stat the entry itself: on Windows the directory listing's sizes and
			// times are updated lazily, so they can move after a change settled.
			fi, err := os.Lstat(filepath.Join(dir, e.Name()))
			if err != nil {
				fi, err = e.Info()
			}
			if err == nil {
				h.Write(strconv.AppendInt(nil, fi.Size(), 10))
				h.Write([]byte{':'})
				h.Write(strconv.AppendInt(nil, fi.ModTime().UnixNano(), 10))
			}
			h.Write([]byte{0})
		}
	}
	walk(t, 0)
	s.sum = h.Sum64()
	return s
}
