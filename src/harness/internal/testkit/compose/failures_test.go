package compose_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/compose"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// fatal runs fn against rec and checks that it failed the test with one message
// containing want.
func fatal(t *testing.T, rec *recorder, want string, fn func()) {
	t.Helper()
	if !rec.run(fn) {
		t.Fatalf("the call did not fail the test; it reported %q", rec.failed())
	}
	if f := rec.failed(); len(f) != 1 || !strings.Contains(f[0], want) {
		t.Fatalf("reported %q, want one failure containing %q", f, want)
	}
}

// Layer fails the test on a document that is not a layer, and on a layer with
// an operation it cannot decode; a valid layer keeps its label.
func TestLayerFailsTheTestOnABrokenLayer(t *testing.T) {
	if l := compose.Layer(t, "fine", "[]"); l.Label != "fine" || len(l.Ops) != 0 {
		t.Fatalf("an empty layer: %+v", l)
	}
	rec := &recorder{TB: t}
	fatal(t, rec, "compose: layer bad", func() { compose.Layer(rec, "bad", "{a: 1}") })
	rec = &recorder{TB: t}
	fatal(t, rec, "compose: layer odd", func() { compose.Layer(rec, "odd", "- frobnicate: {}\n") })
}

// Start fails the test before any launch on a bundle that is neither shipped
// nor a profile, and on a catalogue the test's components break.
func TestStartFailsOnABadBundleOrCatalogue(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		t.Cleanup(rec.cleanup)
		fatal(t, rec, `"no-such" is neither a shipped bundle`, func() { compose.Start(rec, compose.Options{Bundle: "no-such"}) })

		twice := catalog.Item{Name: egress.Name, Component: cordis.Define(egress.Name, func(*cordis.Context, cordis.None) error { return nil })}
		rec = &recorder{TB: t}
		t.Cleanup(rec.cleanup)
		fatal(t, rec, "util.egress is declared twice", func() { compose.Start(rec, compose.Options{Components: []catalog.Item{twice}}) })
	})
}

// A launch that ends before ready fails the test with boot's status and
// diagnostics: here the home's profile layer does not parse, so boot stops at
// the layers step.
func TestStartFailsWhenTheLaunchEndsBeforeReady(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		home := temphome.Dir(t)
		layer := profile.LayerPath(home, "compose")
		if err := os.MkdirAll(filepath.Dir(layer), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layer, []byte("{not: [a layer"), 0o600); err != nil {
			t.Fatal(err)
		}
		rec := &recorder{TB: t}
		t.Cleanup(rec.cleanup)
		fatal(t, rec, "the launch ended before ready with status", func() { compose.Start(rec, compose.Options{Home: home}) })
	})
}

// A shipped bundle other than core is stacked on core and starts.
func TestStartStacksAShippedBundleOnCore(t *testing.T) {
	clock.Run(t, func(t *testing.T) {
		tr := compose.Start(t, compose.Options{Bundle: "minimal"})
		var labels []string
		for _, l := range tr.Layers {
			labels = append(labels, l.Label)
		}
		got := strings.Join(labels, ",")
		if !strings.HasPrefix(got, profile.BundleLabel+"core,"+profile.BundleLabel+"minimal,") ||
			!strings.Contains(got, compose.LayerLabel) || !strings.HasSuffix(got, profile.LauncherLabel) {
			t.Fatalf("layers %s: want core, minimal, ..., the kit's layer, the launcher layer last", got)
		}
	})
}
