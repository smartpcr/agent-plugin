package compose_test

import (
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/compose"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/egresstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// item is a test component called name whose body is apply.
func item(name string, apply func(ctx *cordis.Context) error, inject ...string) catalog.Item {
	return catalog.Item{Name: name, Component: cordis.Define(name, func(ctx *cordis.Context, _ cordis.None) error {
		return apply(ctx)
	}, cordis.WithInject(inject...))}
}

// inserting is a layer inserting one entry id of the component name.
func inserting(t testing.TB, id, name string) []loader.Layer {
	return []loader.Layer{compose.Layer(t, id, "- insert:\n    - id: "+id+"\n      name: "+name+"\n")}
}

func ids(tr *compose.Tree) []string {
	var out []string
	for _, e := range tr.Loader.Entries() {
		out = append(out, e.ID())
	}
	return out
}

// DH-T-UTIL-13: compose.Start loads the base bundle through boot's real steps
// on a fresh home: core's entries and the kit's model entry are loaded, a
// component under test is injected with the recording egress and the scripted
// runtime, and when the test ends nothing is left -- the fake TB records no
// failure.
//
//dsh:test DH-T-UTIL-13
func TestStartComposesTheBaseBundleAndLeavesNothing(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		var svc *egress.Service
		var rt *llm.Runtime
		probe := item("util.test-probe", func(ctx *cordis.Context) error {
			var err error
			if svc, err = egress.Key.Get(ctx); err != nil {
				return err
			}
			rt, err = compose.RuntimeKey.Get(ctx)
			return err
		}, egress.Key.Name(), compose.RuntimeKey.Name())
		tr := compose.Start(rec, compose.Options{Components: []catalog.Item{probe}, Layers: inserting(t, "probe", "util.test-probe")})

		got := ids(tr)
		for _, want := range []string{"logging", "sharing", "egress", "testkit-model", "probe"} {
			if !slices.Contains(got, want) {
				t.Fatalf("entries %v lack %s", got, want)
			}
		}
		if rt != tr.Runtime || svc == nil {
			t.Fatalf("the probe was injected with %p and %v, want the kit's runtime %p", rt, svc, tr.Runtime)
		}
		if _, err := rt.Prepare(t.Context(), compose.Route, "", vocab.Controls{}); err != nil {
			t.Fatalf("the scripted route: %v", err)
		}
		if _, err := os.Stat(home.New(tr.Data.Home).Config()); err != nil {
			t.Fatalf("the fresh home was not laid out: %v", err)
		}
		// The tree's egress is the recorder: a scripted refusal comes back and the
		// request is recorded, with no network.
		tr.Egress.Fail("https://api.model.test", 1, egresstest.Refused)
		c, err := svc.Client("model", permit.New(t, "model", "https://api.model.test"), egress.Options{})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.model.test/v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Do(req); !errors.Is(err, syscall.ECONNREFUSED) {
			t.Fatalf("a request through the tree's egress: %v", err)
		}
		svc.Release(c)
		if n := len(tr.Egress.Requests()); n != 1 {
			t.Fatalf("the recorder saw %d requests", n)
		}

		rec.cleanup()
		if f := rec.failed(); len(f) != 0 {
			t.Fatalf("a clean tree failed the test: %q", f)
		}
	})
}

// Scenario (gap-closure 19.5.1) "A composed tree leaves nothing": given
// compose.Start on the base bundle with a component that leaks a goroutine,
// when the test ends, then the kit fails it, naming the goroutine's stack;
// without the leak (the next subtest, a goroutine owned by an effect) it passes
// and no effect remains.
//
//dsh:test DH-T-UTIL-13
func TestAComposedTreeThatLeaksAGoroutineFailsTheTest(t *testing.T) {
	t.Run("leak", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) }) // after the check, so the bubble can end
			leaky := item("util.test-leaky", func(*cordis.Context) error {
				go leakUntil(release)
				return nil
			})
			rec := &recorder{TB: t}
			compose.Start(rec, compose.Options{Components: []catalog.Item{leaky}, Layers: inserting(t, "leaky", "util.test-leaky")})
			rec.cleanup()
			f := rec.failed()
			if len(f) != 1 || !strings.Contains(f[0], "1 goroutine(s) still alive") || !strings.Contains(f[0], "leakUntil") {
				t.Fatalf("a leaking tree reported %q, want one failure with the goroutine's stack", f)
			}
		})
	})
	t.Run("owned", func(t *testing.T) {
		clock.Run(t, func(t *testing.T) {
			stop, done := make(chan struct{}), make(chan struct{})
			// The effect's disposer releases the worker; this checks that it did.
			t.Cleanup(func() {
				select {
				case <-done:
				default:
					t.Error("the owned worker outlived the tree")
				}
			})
			owned := item("util.test-owned", func(ctx *cordis.Context) error {
				_, err := ctx.Effect("worker", func() (cordis.Disposer, error) {
					go func() { defer close(done); leakUntil(stop) }()
					return func() error { close(stop); <-done; return nil }, nil
				})
				return err
			})
			rec := &recorder{TB: t}
			compose.Start(rec, compose.Options{Components: []catalog.Item{owned}, Layers: inserting(t, "owned", "util.test-owned")})
			rec.cleanup()
			if f := rec.failed(); len(f) != 0 {
				t.Fatalf("a tree whose goroutine an effect owns failed the test: %q", f)
			}
		})
	})
}

// leakUntil blocks until release is closed: the body of the leaked goroutine,
// named so the failure's stack can be checked for it.
func leakUntil(release <-chan struct{}) { <-release }

// DH-T-UTIL-13: an effect whose release never returns is left after unload:
// boot's shutdown bound passes (in virtual time) and the kit fails the test,
// naming the entry and its effect.
//
//dsh:test DH-T-UTIL-13
func TestAnEffectLeftAfterUnloadFailsTheTest(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		stuck := item("util.test-stuck", func(ctx *cordis.Context) error {
			_, err := ctx.Effect("held lock", func() (cordis.Disposer, error) {
				return func() error { <-release; return nil }, nil
			})
			return err
		})
		rec := &recorder{TB: t}
		compose.Start(rec, compose.Options{Components: []catalog.Item{stuck}, Layers: inserting(t, "stuck", "util.test-stuck")})
		rec.cleanup()
		all := strings.Join(rec.failed(), "\n")
		if !strings.Contains(all, "shut down with status") || !strings.Contains(all, "held lock") {
			t.Fatalf("a stuck effect reported %q, want the forced status and the effect named", all)
		}
	})
}

// Start refuses to run outside a bubble.
func TestStartNeedsABubble(t *testing.T) {
	rec := &recorder{TB: t}
	t.Cleanup(rec.cleanup)
	if !rec.run(func() { compose.Start(rec, compose.Options{}) }) {
		t.Fatal("Start outside clock.Run did not fail the test")
	}
	if f := rec.failed(); len(f) != 1 || !strings.Contains(f[0], "inside clock.Run") {
		t.Fatalf("reported %q", f)
	}
}
