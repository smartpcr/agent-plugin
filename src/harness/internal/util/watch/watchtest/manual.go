package watchtest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// Manual is a watch service for consumers' tests: it implements watch.Key's
// Watcher, with the same ownership rules as the real one (each watch an effect of
// the asking fiber, closed when it unloads; CloseFrom only from that fiber), but
// it reads no filesystem and runs no timers. A test says what changed with Fire
// and what degraded with Degrade; the callbacks run before those return.
type Manual struct {
	mu      sync.Mutex
	watches map[*manualWatch]struct{}
	closed  bool
}

// NewManual is a Manual with no watches.
func NewManual() *Manual { return &Manual{watches: map[*manualWatch]struct{}{}} }

var _ watch.Watcher = (*Manual)(nil)

// Watch starts a watch owned by owner's fiber. It validates t as the real
// service does; Auto and Notify watches report Notify until degraded.
func (m *Manual) Watch(owner *cordis.Context, t watch.Target, changed func(err error)) (watch.Watch, error) {
	return watch.Owned(engine{m}).Watch(owner, t, changed)
}

// Provide registers m under watch.Key as an effect of ctx's fiber, and closes m
// when that fiber unloads, as the real component closes its service.
func (m *Manual) Provide(ctx *cordis.Context) (*cordis.EffectHandle, error) {
	if _, err := ctx.Effect("watchtest manual", func() (cordis.Disposer, error) { return m.Close, nil }); err != nil {
		return nil, err
	}
	return watch.Key.Provide(ctx, m)
}

// Fire tells every watch whose target covers path that it changed: the target
// itself, an entry of a directory target, or (Depth 1) an entry of one of its
// subdirectories. It returns how many watches were told.
func (m *Manual) Fire(path string) int { return m.tell(path, nil) }

// Degrade switches every watch covering path to Poll and tells it so, with an
// error wrapping watch.ErrDegraded and err. It returns how many were told.
func (m *Manual) Degrade(path string, err error) int {
	if !errors.Is(err, watch.ErrDegraded) {
		err = fmt.Errorf("%w: %v", watch.ErrDegraded, err)
	}
	return m.tell(path, err)
}

// Watching lists the targets of the open watches, by path.
func (m *Manual) Watching() []watch.Target {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]watch.Target, 0, len(m.watches))
	for w := range m.watches {
		out = append(out, w.t)
	}
	slices.SortFunc(out, func(a, b watch.Target) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// Close closes every watch; later Watch calls fail with watch.ErrClosed.
func (m *Manual) Close() error {
	m.mu.Lock()
	m.closed = true
	ws := m.list()
	m.mu.Unlock()
	for _, w := range ws {
		w.Close()
	}
	return nil
}

func (m *Manual) tell(path string, err error) int {
	p, aerr := filepath.Abs(path)
	if aerr != nil {
		return 0
	}
	m.mu.Lock()
	var hit []*manualWatch
	for _, w := range m.list() {
		if covers(w.t, p) {
			hit = append(hit, w)
		}
	}
	m.mu.Unlock()
	n := 0
	for _, w := range hit {
		if w.deliver(err) {
			n++
		}
	}
	return n
}

func (m *Manual) list() []*manualWatch {
	ws := make([]*manualWatch, 0, len(m.watches))
	for w := range m.watches {
		ws = append(ws, w)
	}
	return ws
}

func covers(t watch.Target, p string) bool {
	parent := filepath.Dir(p)
	return samePath(p, t.Path) || samePath(parent, t.Path) || t.Depth == 1 && samePath(filepath.Dir(parent), t.Path)
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// engine is Manual as a watch.Engine, so watch.Owned applies the ownership rules.
type engine struct{ m *Manual }

func (e engine) Watch(ctx context.Context, t watch.Target, changed func(err error)) (watch.Watch, error) {
	if t.Path == "" || t.Depth < 0 || t.Depth > 1 || t.Mode < watch.Auto || t.Mode > watch.Poll || changed == nil {
		return nil, watch.ErrBadTarget
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(t.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", watch.ErrBadTarget, err)
	}
	t.Path = abs
	mode := watch.Notify
	if t.Mode == watch.Poll {
		mode = watch.Poll
	}
	w := &manualWatch{m: e.m, t: t, changed: changed, mode: mode}
	e.m.mu.Lock()
	if e.m.closed {
		e.m.mu.Unlock()
		return nil, watch.ErrClosed
	}
	e.m.watches[w] = struct{}{}
	e.m.mu.Unlock()
	// Registered first, so a cancellation that fires at once finds the watch to
	// remove. The stop function is published under w.mu, which Close also reads
	// it under: the AfterFunc callback may run before AfterFunc returns.
	stop := context.AfterFunc(ctx, func() { w.Close() })
	w.mu.Lock()
	closed := w.closed
	if !closed {
		w.stop = stop
	}
	w.mu.Unlock()
	if closed {
		stop()
	}
	return w, nil
}

// manualWatch is one watch of a Manual.
type manualWatch struct {
	m       *Manual
	t       watch.Target
	changed func(error)

	// calling is held while the callback runs: callbacks never overlap, and
	// Close waits for one in flight. It is never held with mu, so a callback may
	// call Mode.
	calling sync.Mutex

	mu     sync.Mutex // guards mode, closed and stop
	mode   watch.Mode
	closed bool
	stop   func() bool
}

func (w *manualWatch) Mode() watch.Mode {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mode
}

// Close stops the watch; it returns after the callback can no longer run. It
// must not be called from inside the callback, as with the real service.
func (w *manualWatch) Close() error {
	w.mu.Lock()
	w.closed = true
	stop := w.stop
	w.stop = nil
	w.mu.Unlock()
	w.m.mu.Lock()
	delete(w.m.watches, w)
	w.m.mu.Unlock()
	if stop != nil {
		stop()
	}
	w.calling.Lock()
	//lint:ignore SA2001 the empty section is the point: it waits until a callback in flight releases the lock
	w.calling.Unlock() // only waits for a callback in flight
	return nil
}

func (w *manualWatch) deliver(err error) bool {
	w.calling.Lock()
	defer w.calling.Unlock()
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}
	if err != nil {
		w.mode = watch.Poll
	}
	w.mu.Unlock()
	w.changed(err)
	return true
}
