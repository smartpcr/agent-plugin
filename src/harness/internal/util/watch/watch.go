// Package watch tells a component when a file or directory changed: debounced,
// one notice per settled change (docs/harness/libs/chokidar.md §6, DH-D-UTIL-3).
//
// A notice says only "re-read this target". Operating-system notifications
// (fsnotify) are hints: an event that names the target restarts the watch's settle
// timer, and when the timer fires the service compares the target's identity, size
// and times (for a directory, its entries) with what it last saw and calls back only
// if something differs. Directories are watched, never files, so a save by rename
// is seen. Where notifications fail, overflow, are not delivered (network and
// virtual filesystems) or would cost too much (a directory above the entry cap),
// the watch polls instead, says so through Mode, and keeps working.
//
// A target need not exist: the watch attaches to its nearest existing ancestor and
// moves down as the path appears, and back up when it disappears. Trees are not
// watched: a directory target covers its entries (Depth 0) or one level below
// (Depth 1).
package watch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// Mode is how a watch learns of changes.
type Mode int

const (
	Auto   Mode = iota // notifications, polling where they cannot be trusted
	Notify             // notifications; polling only if they fail
	Poll               // polling only
)

func (m Mode) String() string {
	switch m {
	case Auto:
		return "auto"
	case Notify:
		return "notify"
	case Poll:
		return "poll"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// Target is what to watch.
type Target struct {
	Path   string        // file or directory; need not exist yet
	Depth  int           // for a directory: 0 its entries, 1 also the entries of its subdirectories
	Settle time.Duration // quiet period before the callback runs; 0 is Options.Settle
	Mode   Mode
}

// Watch is one active watch.
type Watch interface {
	// Mode is what is in effect now: Notify or Poll.
	Mode() Mode
	// Close stops the watch and returns after its callback can no longer run. It
	// must not be called from inside the callback. Closing twice is harmless.
	Close() error
}

// Options configure a Service. Zero fields take the defaults.
type Options struct {
	NewNotifier func() (Notifier, error) // default NewFSNotifier
	MaxWatches  int                      // default 1024
	MaxEntries  int                      // entries of a watched directory before it polls; default 2000
	Settle      time.Duration            // default 100 ms
	FilePoll    time.Duration            // polling interval for a file; default 250 ms
	DirPoll     time.Duration            // polling interval for a directory; default 1 s
	VerifyPoll  time.Duration            // slow check of a notified file in Auto mode; default 5 s
	// IsRemote reports a filesystem that delivers no notifications; Auto watches
	// there poll. Default fsx.IsRemote.
	IsRemote func(path string) (bool, error)
}

// Errors.
var (
	ErrClosed     = errors.New("watch: service closed")
	ErrTooMany    = errors.New("watch: too many watches")
	ErrBadTarget  = errors.New("watch: invalid target")
	ErrDegraded   = errors.New("watch: notifications failed; polling")
	ErrEntriesCap = errors.New("watch: directory above the entry cap; polling")
)

// Service owns the notifier and every watch.
type Service struct {
	opt Options

	mu      sync.Mutex
	n       Notifier
	nErr    error // why there is no notifier
	dirs    map[string]map[*watch]struct{}
	ids     map[string]fsx.FileID // identity of each directory when it was added
	watches map[*watch]struct{}
	closed  bool

	stop    chan struct{}
	stopped chan struct{}
}

// New starts a service. A notifier that cannot be created is not an error: every
// watch polls, and a watch that asked for notifications is told why once.
func New(opt Options) *Service {
	if opt.NewNotifier == nil {
		opt.NewNotifier = NewFSNotifier
	}
	opt.MaxWatches = defInt(opt.MaxWatches, 1024)
	opt.MaxEntries = defInt(opt.MaxEntries, 2000)
	opt.Settle = defDur(opt.Settle, 100*time.Millisecond)
	opt.FilePoll = defDur(opt.FilePoll, 250*time.Millisecond)
	opt.DirPoll = defDur(opt.DirPoll, time.Second)
	opt.VerifyPoll = defDur(opt.VerifyPoll, 5*time.Second)
	if opt.IsRemote == nil {
		opt.IsRemote = isRemote
	}
	s := &Service{
		opt:     opt,
		dirs:    map[string]map[*watch]struct{}{},
		ids:     map[string]fsx.FileID{},
		watches: map[*watch]struct{}{},
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	s.n, s.nErr = opt.NewNotifier()
	if s.nErr != nil {
		s.n = nil
		close(s.stopped)
		return s
	}
	go s.dispatch()
	return s
}

func defInt(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

func defDur(v, d time.Duration) time.Duration {
	if v <= 0 {
		return d
	}
	return v
}

// Close closes every watch, then the notifier.
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ws := make([]*watch, 0, len(s.watches))
	for w := range s.watches {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		w.Close()
	}
	var err error
	if s.n != nil {
		close(s.stop)
		err = s.n.Close()
		<-s.stopped
	}
	return err
}

// Watch starts watching t and returns once the watch is active. changed runs on
// the watch's own goroutine, never concurrently with itself: with nil after a
// settled change, or with an error (wrapping ErrDegraded or ErrEntriesCap) when
// the watch switched to polling, which also means "re-read". The watch ends with
// Close, with ctx, or with the service.
func (s *Service) Watch(ctx context.Context, t Target, changed func(err error)) (Watch, error) {
	if t.Path == "" || t.Depth < 0 || t.Depth > 1 || t.Mode < Auto || t.Mode > Poll || changed == nil {
		return nil, ErrBadTarget
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(t.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadTarget, err)
	}
	t.Path = canonical(abs)
	if t.Settle <= 0 {
		t.Settle = s.opt.Settle
	}
	w := newWatch(s, t, changed)

	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		return nil, ErrClosed
	case len(s.watches) >= s.opt.MaxWatches:
		s.mu.Unlock()
		return nil, ErrTooMany
	}
	s.watches[w] = struct{}{}
	s.mu.Unlock()

	w.start()
	go w.run(ctx)
	return w, nil
}

// subscribe registers w for events in dir, adding dir to the notifier for its
// first subscriber. A directory is registered by name but the kernel watches the
// directory itself, so its identity is kept: when dir now names a different
// directory (deleted and recreated, perhaps within one settle window, so no
// event marked the change), the dead registration is dropped and dir re-added.
func (s *Service) subscribe(w *watch, dir string) error {
	id, idErr := fsx.ID(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n == nil {
		return fmt.Errorf("%w: %v", ErrDegraded, s.nErr)
	}
	if s.closed {
		return ErrClosed
	}
	subs := s.dirs[dir]
	if subs != nil && idErr == nil && s.ids[dir] != id {
		_ = s.n.Remove(dir) // the old registration may be gone already
		if err := s.n.Add(dir); err != nil {
			return fmt.Errorf("%w: watching %s: %v", ErrDegraded, dir, err)
		}
		s.ids[dir] = id
	}
	if subs == nil {
		if err := s.n.Add(dir); err != nil {
			return fmt.Errorf("%w: watching %s: %v", ErrDegraded, dir, err)
		}
		subs = map[*watch]struct{}{}
		s.dirs[dir] = subs
		s.ids[dir] = id
	}
	subs[w] = struct{}{}
	return nil
}

func (s *Service) unsubscribe(w *watch, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.dirs[dir]
	if subs == nil {
		return
	}
	delete(subs, w)
	if len(subs) == 0 {
		delete(s.dirs, dir)
		delete(s.ids, dir)
		if s.n != nil {
			_ = s.n.Remove(dir) // the directory may be gone already
		}
	}
}

func (s *Service) forget(w *watch) {
	s.mu.Lock()
	delete(s.watches, w)
	s.mu.Unlock()
}

// dispatch routes notifier events to the watches subscribed to the directory that
// holds the path (or to the path itself, when it is a watched directory).
func (s *Service) dispatch() {
	defer close(s.stopped)
	events, errs := s.n.Events(), s.n.Errors()
	for {
		select {
		case <-s.stop:
			return
		case p, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			p = filepath.Clean(p)
			s.mu.Lock()
			for _, dir := range []string{filepath.Dir(p), p} {
				for w := range s.dirs[dir] {
					w.event(p)
				}
			}
			s.mu.Unlock()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			s.mu.Lock()
			for w := range s.watches {
				if errors.Is(err, ErrOverflow) {
					w.event("")
				} else {
					w.degrade(fmt.Errorf("%w: %v", ErrDegraded, err))
				}
			}
			s.mu.Unlock()
		}
	}
}
