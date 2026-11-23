package watchfiber_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch/watchfiber"
)

const (
	settle   = 300 * time.Millisecond
	deadline = 5 * time.Second
	quiet    = 800 * time.Millisecond
)

type recorder chan error

func (r recorder) changed(err error) { r <- err }

func (r recorder) one(t *testing.T, what string) {
	t.Helper()
	select {
	case err := <-r:
		if err != nil {
			t.Fatalf("%s: notice carried %v", what, err)
		}
	case <-time.After(deadline):
		t.Fatalf("%s: no notice within %v", what, deadline)
	}
	r.none(t, what+" (second notice)")
}

func (r recorder) none(t *testing.T, what string) {
	t.Helper()
	select {
	case err := <-r:
		t.Fatalf("%s: unexpected notice (err %v)", what, err)
	case <-time.After(quiet):
	}
}

func save(t *testing.T, path, body string) {
	t.Helper()
	if err := fsx.WriteFile(path, []byte(body), fsx.Options{Perm: 0o644}); err != nil {
		t.Fatal(err)
	}
}

func root(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	return r
}

// use loads body as a component under r and waits for it to be active.
func use(t *testing.T, r *cordis.Context, name string, body func(ctx *cordis.Context) error) *cordis.Fiber {
	t.Helper()
	f, err := r.Use(cordis.Define(name, func(ctx *cordis.Context, _ cordis.None) error { return body(ctx) }), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	t.Cleanup(f.Dispose)
	return f
}

// fixture is a service owned by its own fiber and a file to watch.
func fixture(t *testing.T) (*cordis.Context, *watch.Service, *cordis.Fiber, string) {
	t.Helper()
	r := root(t)
	s := watch.New(watch.Options{FilePoll: 30 * time.Millisecond, DirPoll: 30 * time.Millisecond})
	t.Cleanup(func() { s.Close() })
	owner := use(t, r, "watch-service", func(ctx *cordis.Context) error {
		_, err := watchfiber.Own(ctx, s)
		return err
	})
	f := filepath.Join(t.TempDir(), "f.txt")
	save(t, f, "0")
	return r, s, owner, f
}

// consumer loads a component that asks for a watch on f and returns its fiber,
// context, handle and notices.
func consumer(t *testing.T, r *cordis.Context, s *watch.Service, name, f string) (*cordis.Fiber, *cordis.Context, *watchfiber.Handle, recorder) {
	t.Helper()
	rec := make(recorder, 16)
	var h *watchfiber.Handle
	var c *cordis.Context
	fiber := use(t, r, name, func(ctx *cordis.Context) error {
		var err error
		c = ctx
		h, err = watchfiber.Watch(ctx, s, watch.Target{Path: f, Mode: watch.Poll, Settle: settle}, rec.changed)
		return err
	})
	return fiber, c, h, rec
}

// TestUnloadClosesWatch: a consumer that never closes its watch still stops
// receiving notices once its fiber unloads; another consumer of the same service
// is not affected.
//
//dsh:test DH-T-UTIL-6
func TestUnloadClosesWatch(t *testing.T) {
	r, s, _, f := fixture(t)
	a, _, h, ra := consumer(t, r, s, "a", f)
	_, _, _, rb := consumer(t, r, s, "b", f)
	if h.Mode() != watch.Poll {
		t.Fatalf("mode %v", h.Mode())
	}
	save(t, f, "1")
	ra.one(t, "a before unload")
	rb.one(t, "b before unload")

	a.Dispose()
	save(t, f, "2")
	ra.none(t, "a after its fiber unloaded")
	rb.one(t, "b after a unloaded")
}

// TestOnlyOwnerCloses: closing a watch from another fiber is refused and the
// watch keeps running; the owner's Close stops it, and a second Close is harmless.
//
//dsh:test DH-T-UTIL-6
func TestOnlyOwnerCloses(t *testing.T) {
	r, s, _, f := fixture(t)
	_, actx, h, ra := consumer(t, r, s, "a", f)
	_, bctx, _, _ := consumer(t, r, s, "b", f)
	if err := h.Close(bctx); !errors.Is(err, watchfiber.ErrNotOwner) {
		t.Fatalf("close from another fiber: %v", err)
	}
	if err := h.Close(nil); !errors.Is(err, watchfiber.ErrNotOwner) {
		t.Fatalf("close without a context: %v", err)
	}
	save(t, f, "1")
	ra.one(t, "after a refused close")

	// A context isolated from the owner's is still the owner's fiber.
	if err := h.Close(actx.Isolate("scope")); err != nil {
		t.Fatalf("owner close: %v", err)
	}
	if err := h.Close(actx); err != nil {
		t.Fatalf("second close: %v", err)
	}
	save(t, f, "2")
	ra.none(t, "after the owner closed")
}

// TestServiceUnloadClosesAll: unloading the service's fiber closes the service
// and every watch on it, whichever fiber asked for them.
//
//dsh:test DH-T-UTIL-6
func TestServiceUnloadClosesAll(t *testing.T) {
	r, s, owner, f := fixture(t)
	_, _, _, ra := consumer(t, r, s, "a", f)
	owner.Dispose()
	save(t, f, "1")
	ra.none(t, "after the service unloaded")
	if _, err := s.Watch(t.Context(), watch.Target{Path: f}, func(error) {}); !errors.Is(err, watch.ErrClosed) {
		t.Fatalf("watch on an unloaded service: %v", err)
	}
}

// TestWatchFailureFailsLoad: a watch the service refuses is not registered, and
// the error reaches the component, which fails its load.
func TestWatchFailureFailsLoad(t *testing.T) {
	r, s, _, _ := fixture(t)
	var got error
	f, err := r.Use(cordis.Define("bad", func(ctx *cordis.Context, _ cordis.None) error {
		_, got = watchfiber.Watch(ctx, s, watch.Target{}, func(error) {})
		return got
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	if err := f.Wait(); !errors.Is(err, watch.ErrBadTarget) || !errors.Is(got, watch.ErrBadTarget) {
		t.Fatalf("load %v, watch %v", err, got)
	}
}
