package watch

import (
	"errors"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Notifier is the operating system's change notification, as the service uses it:
// directories are added and removed, and every event names a path inside (or
// equal to) an added directory. The real one wraps fsnotify; tests inject a fake
// (package watchtest).
type Notifier interface {
	Add(dir string) error
	Remove(dir string) error
	Events() <-chan string // paths that changed, in any way
	Errors() <-chan error  // ErrOverflow when events were dropped; anything else is fatal to notification
	Close() error
}

// ErrOverflow: the notifier dropped events. Every watch re-checks its target.
var ErrOverflow = errors.New("watch: change notifications overflowed")

// NewFSNotifier is the Notifier over fsnotify.
func NewFSNotifier() (Notifier, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	n := &fsNotifier{w: w, events: make(chan string, 256), errs: make(chan error, 16), done: make(chan struct{}), quit: make(chan struct{})}
	go n.pump()
	return n, nil
}

type fsNotifier struct {
	w      *fsnotify.Watcher
	events chan string
	errs   chan error
	done   chan struct{}
	quit   chan struct{}
	once   sync.Once
}

// pump translates fsnotify's events and errors until the watcher is closed.
func (n *fsNotifier) pump() {
	defer close(n.done)
	defer close(n.events)
	defer close(n.errs)
	for {
		select {
		case ev, ok := <-n.w.Events:
			if !ok {
				return
			}
			select {
			case n.events <- ev.Name:
			case <-n.quit:
				return
			}
		case err, ok := <-n.w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				err = ErrOverflow
			}
			select {
			case n.errs <- err:
			case <-n.quit:
				return
			}
		case <-n.quit:
			return
		}
	}
}

func (n *fsNotifier) Add(dir string) error    { return n.w.Add(dir) }
func (n *fsNotifier) Remove(dir string) error { return n.w.Remove(dir) }
func (n *fsNotifier) Events() <-chan string   { return n.events }
func (n *fsNotifier) Errors() <-chan error    { return n.errs }

func (n *fsNotifier) Close() error {
	n.once.Do(func() { close(n.quit) })
	err := n.w.Close()
	<-n.done
	return err
}
