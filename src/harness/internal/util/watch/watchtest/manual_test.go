package watchtest_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch/watchtest"
)

// owner loads an empty component and returns its context.
func owner(t *testing.T) (*cordis.Fiber, *cordis.Context) {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	var c *cordis.Context
	f, err := r.Use(cordis.Define("test.owner", func(ctx *cordis.Context, _ cordis.None) error { c = ctx; return nil }), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	return f, c
}

// TestManualCovers: Fire reaches a file target, a directory's entries, and at
// Depth 1 the entries of its subdirectories; nothing else.
func TestManualCovers(t *testing.T) {
	_, ctx := owner(t)
	m := watchtest.NewManual()
	dir := t.TempDir()
	counts := map[string]*int{}
	for _, tg := range []watch.Target{
		{Path: filepath.Join(dir, "f.txt")},
		{Path: dir},
		{Path: dir, Depth: 1, Mode: watch.Poll},
	} {
		n := new(int)
		counts[tg.Path+string(rune('0'+tg.Depth))] = n
		w, err := m.Watch(ctx, tg, func(error) { *n++ })
		if err != nil {
			t.Fatal(err)
		}
		if want := map[watch.Mode]watch.Mode{watch.Auto: watch.Notify, watch.Poll: watch.Poll}[tg.Mode]; w.Mode() != want {
			t.Fatalf("mode %v, want %v", w.Mode(), want)
		}
	}
	if got := len(m.Watching()); got != 3 {
		t.Fatalf("watching %d", got)
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{filepath.Join(dir, "f.txt"), 3},
		{filepath.Join(dir, "other"), 2},
		{filepath.Join(dir, "sub", "x"), 1},
		{filepath.Join(dir, "sub", "deeper", "x"), 0},
		{filepath.Dir(dir), 0},
	} {
		if got := m.Fire(tc.path); got != tc.want {
			t.Errorf("Fire(%s) told %d, want %d", tc.path, got, tc.want)
		}
	}
}

// TestManualDegradeAndClose: Degrade switches to Poll and wraps ErrDegraded; a
// closed watch, a cancelled owner and a closed Manual deliver nothing more.
func TestManualDegradeAndClose(t *testing.T) {
	f, ctx := owner(t)
	m := watchtest.NewManual()
	path := filepath.Join(t.TempDir(), "f")
	var got []error
	w, err := m.Watch(ctx, watch.Target{Path: path}, func(err error) { got = append(got, err) })
	if err != nil {
		t.Fatal(err)
	}
	if n := m.Degrade(path, watch.ErrEntriesCap); n != 1 || w.Mode() != watch.Poll {
		t.Fatalf("degrade told %d, mode %v", n, w.Mode())
	}
	if n := m.Degrade(path, watch.ErrDegraded); n != 1 {
		t.Fatalf("second degrade told %d", n)
	}
	if len(got) != 2 || !errors.Is(got[0], watch.ErrDegraded) || !strings.Contains(got[0].Error(), watch.ErrEntriesCap.Error()) || !errors.Is(got[1], watch.ErrDegraded) {
		t.Fatalf("notices %v", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if n := m.Fire(path); n != 0 {
		t.Fatalf("closed watch told: %d", n)
	}

	if _, err := m.Watch(ctx, watch.Target{}, func(error) {}); !errors.Is(err, watch.ErrBadTarget) {
		t.Fatalf("bad target: %v", err)
	}
	if _, err := m.Watch(nil, watch.Target{Path: path}, func(error) {}); !errors.Is(err, watch.ErrBadTarget) {
		t.Fatalf("no owner: %v", err)
	}
	if _, err := m.Watch(ctx, watch.Target{Path: path}, func(error) {}); err != nil {
		t.Fatal(err)
	}
	f.Dispose()
	if n := m.Fire(path); n != 0 {
		t.Fatalf("watch outlived its owner: %d", n)
	}

	_, ctx2 := owner(t)
	m.Close()
	if _, err := m.Watch(ctx2, watch.Target{Path: path}, func(error) {}); !errors.Is(err, watch.ErrClosed) {
		t.Fatalf("watch on a closed manual: %v", err)
	}
}

// TestFakeScript: the fake notifier records directories, delivers what it is
// sent and refuses what AddErr refuses.
func TestFakeScript(t *testing.T) {
	f := watchtest.New()
	refused := errors.New("refused")
	f.AddErr = func(dir string) error {
		if dir == "no" {
			return refused
		}
		return nil
	}
	n, err := f.Factory()()
	if err != nil || n != f {
		t.Fatalf("factory: %v %v", n, err)
	}
	if err := f.Add("no"); !errors.Is(err, refused) {
		t.Fatalf("refused add: %v", err)
	}
	f.Add("b")
	f.Add("a")
	f.Add("a")
	f.Remove("a")
	if got := f.Watched(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("watched %v", got)
	}
	f.Send("a/x")
	f.Fail(watch.ErrOverflow)
	if ev := <-f.Events(); ev != "a/x" {
		t.Fatalf("event %q", ev)
	}
	if err := <-f.Errors(); !errors.Is(err, watch.ErrOverflow) {
		t.Fatalf("error %v", err)
	}
	if f.Closed() || f.Close() != nil || !f.Closed() {
		t.Fatal("close not recorded")
	}
}

// TestManualProvide: a Manual provided under watch.Key is what a consumer
// resolves, and unloading the providing fiber closes its watches.
func TestManualProvide(t *testing.T) {
	m := watchtest.NewManual()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	p, err := r.Use(cordis.Define("test.manual", func(ctx *cordis.Context, _ cordis.None) error {
		_, err := m.Provide(ctx)
		return err
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Dispose)
	path := filepath.Join(t.TempDir(), "f")
	var got watch.Watcher
	c, err := r.Use(cordis.Define("test.consumer", func(ctx *cordis.Context, _ cordis.None) error {
		s, err := watch.Key.Get(ctx)
		got = s
		if err == nil {
			_, err = s.Watch(ctx, watch.Target{Path: path}, func(error) {})
		}
		return err
	}, cordis.WithInject(watch.Key.Name())), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Dispose)
	if got != watch.Watcher(m) {
		t.Fatalf("resolved %T", got)
	}
	p.Dispose()
	if n := m.Fire(path); n != 0 || len(m.Watching()) != 0 {
		t.Fatalf("watches survived the provider: %d", n)
	}
}

// TestManualCallbackReentry: a callback may ask its watch for its mode, during
// a change and during a degradation, without blocking.
func TestManualCallbackReentry(t *testing.T) {
	_, ctx := owner(t)
	m := watchtest.NewManual()
	path := filepath.Join(t.TempDir(), "f")
	var w watch.Watch
	var modes []watch.Mode
	w, err := m.Watch(ctx, watch.Target{Path: path}, func(error) { modes = append(modes, w.Mode()) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	// P-C: nothing gates the firing goroutine; the cleanup joins it, bounded.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the firing goroutine did not return")
		}
	})
	go func() {
		defer close(done)
		m.Fire(path)
		m.Degrade(path, errors.New("lost"))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("callback calling Mode blocked")
	}
	if !slices.Equal(modes, []watch.Mode{watch.Notify, watch.Poll}) {
		t.Fatalf("modes seen in callbacks %v", modes)
	}
}
