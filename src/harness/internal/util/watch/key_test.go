package watch_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch/watchtest"
)

// provider is one implementation of watch.Key: how to load it under a root, and
// how to make one change to a file it watches.
type provider struct {
	name   string
	load   func(t *testing.T, r *cordis.Context) *cordis.Fiber
	change func(t *testing.T, path, body string)
	// degrade, when set, makes notifications for path fail.
	degrade func(path string)
}

func providers(t *testing.T) []provider {
	manual := watchtest.NewManual()
	return []provider{
		{
			name: "component",
			load: func(t *testing.T, r *cordis.Context) *cordis.Fiber {
				return ready(t, r, watch.Component, watch.Options{FilePoll: 30 * time.Millisecond, DirPoll: 30 * time.Millisecond})
			},
			change: write,
		},
		{
			name: "manual",
			load: func(t *testing.T, r *cordis.Context) *cordis.Fiber {
				return ready(t, r, cordis.Define("test.watch-manual", func(ctx *cordis.Context, _ cordis.None) error {
					_, err := manual.Provide(ctx)
					return err
				}), nil)
			},
			change:  func(t *testing.T, path, _ string) { manual.Fire(path) },
			degrade: func(path string) { manual.Degrade(path, errors.New("lost")) },
		},
	}
}

func cordisRoot(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	return r
}

func ready(t *testing.T, r *cordis.Context, c *cordis.Component, config any) *cordis.Fiber {
	t.Helper()
	f, err := r.Use(c, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatalf("%s: %v", c.Name(), err)
	}
	t.Cleanup(f.Dispose)
	return f
}

// asker loads a consumer that resolves watch.Key and asks for a watch on path.
func asker(t *testing.T, r *cordis.Context, name, path string) (*cordis.Fiber, *cordis.Context, watch.Watch, recorder) {
	t.Helper()
	rec := make(recorder, 16)
	var w watch.Watch
	var c *cordis.Context
	f := ready(t, r, cordis.Define(name, func(ctx *cordis.Context, _ cordis.None) error {
		s, err := watch.Key.Get(ctx)
		if err != nil {
			return err
		}
		c = ctx
		w, err = s.Watch(ctx, watch.Target{Path: path, Settle: settle}, rec.changed)
		return err
	}, cordis.WithInject(watch.Key.Name())), nil)
	return f, c, w, rec
}

// TestOwnedRefusals: Owned refuses a watch with no owner, and a watch its engine
// refuses is refused to the caller with the engine's error and leaves nothing
// registered or owned: the fiber unloads cleanly and the notifier holds no
// directory.
//
//dsh:test DH-T-UTIL-6
func TestOwnedRefusals(t *testing.T) {
	fake := watchtest.New()
	s := service(t, watch.Options{NewNotifier: fake.Factory()})
	noop := func(error) {}
	if _, err := watch.Owned(s).Watch(nil, watch.Target{Path: t.TempDir()}, noop); !errors.Is(err, watch.ErrBadTarget) {
		t.Fatalf("no owner: %v", err)
	}
	var refused, accepted error
	f := ready(t, cordisRoot(t), cordis.Define("test.watch-refused", func(ctx *cordis.Context, _ cordis.None) error {
		_, refused = watch.Owned(s).Watch(ctx, watch.Target{Path: "x", Depth: 2}, noop)
		_, accepted = watch.Owned(s).Watch(ctx, watch.Target{Path: t.TempDir(), Mode: watch.Notify}, noop)
		return nil
	}), nil)
	if !errors.Is(refused, watch.ErrBadTarget) {
		t.Fatalf("engine refusal: %v", refused)
	}
	if accepted != nil || len(fake.Watched()) == 0 {
		t.Fatalf("accepted watch: %v, watched %q", accepted, fake.Watched())
	}
	f.Dispose()
	if got := fake.Watched(); len(got) != 0 {
		t.Fatalf("still watched after the owner unloaded: %q", got)
	}
}

// TestKeyContract: the real component and watchtest.Manual behave alike behind
// watch.Key: one notice per change, a watch is closed with its asker's fiber,
// only that fiber may close it early, and the provider's unload closes every
// watch.
//
//dsh:test DH-T-UTIL-6
func TestKeyContract(t *testing.T) {
	for _, p := range providers(t) {
		t.Run(p.name, func(t *testing.T) {
			r := cordisRoot(t)
			service := p.load(t, r)
			f := filepath.Join(t.TempDir(), "settings.json")
			write(t, f, "0")

			a, actx, aw, ra := asker(t, r, "test.watch-a", f)
			_, bctx, _, rb := asker(t, r, "test.watch-b", f)
			if aw.Mode() != watch.Notify {
				t.Fatalf("mode %v, want notify", aw.Mode())
			}
			p.change(t, f, "1")
			if err := ra.one(t, "a: change"); err != nil {
				t.Fatalf("notice carried %v", err)
			}
			rb.one(t, "b: change")

			h := aw.(*watch.Handle)
			if err := h.CloseFrom(bctx); !errors.Is(err, watch.ErrNotOwner) {
				t.Fatalf("close from another fiber: %v", err)
			}
			if h.Owner() != a {
				t.Fatal("handle not owned by its asker's fiber")
			}
			p.change(t, f, "2")
			ra.one(t, "a: after a refused close")
			rb.one(t, "b: after a refused close")

			if err := h.CloseFrom(actx); err != nil {
				t.Fatalf("owner close: %v", err)
			}
			p.change(t, f, "3")
			ra.none(t, "a: after its owner closed it")
			rb.one(t, "b: after a closed")

			c, _, _, rc := asker(t, r, "test.watch-c", f)
			c.Dispose()
			p.change(t, f, "4")
			rc.none(t, "c: after its fiber unloaded")
			rb.one(t, "b: after c unloaded")

			if p.degrade != nil {
				p.degrade(f)
				if err := rb.one(t, "b: degraded"); !errors.Is(err, watch.ErrDegraded) {
					t.Fatalf("degraded notice carried %v", err)
				}
			}

			service.Dispose()
			p.change(t, f, "5")
			rb.none(t, "b: after the provider unloaded")
		})
	}
}
