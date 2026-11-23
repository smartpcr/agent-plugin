package redact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/redact"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

const value = "sk-redact-dummy"

// released is a store that has released value.
func released(t *testing.T) *secret.Store {
	t.Helper()
	store := secret.NewStore(secret.EnvSource(func(string) (string, bool) { return value, true }))
	if _, _, err := store.Resolve(context.Background(), secret.MustParse("secret://acme/key")); err != nil {
		t.Fatal(err)
	}
	return store
}

// The shaper replaces a text holding a released value with its masked form, a
// failure's text included, and accepts a text with nothing to mask.
//
//dsh:test DH-T-HUMAN-12
func TestShaperMasksReleasedValues(t *testing.T) {
	shape := redact.Shaper(released(t))
	for _, o := range []tools.Outcome{
		{Text: "token " + value + " echoed"},
		{IsError: true, Code: tools.CodeFailed, Text: "tools/failed: bad key " + value},
	} {
		pd := shape(tools.Call{}, o)
		if pd.Kind != tools.PostReplaceContent || strings.Contains(pd.Text, value) || !strings.Contains(pd.Text, secret.Redacted) {
			t.Errorf("shape(%q) = %+v; want the text with %s", o.Text, pd, secret.Redacted)
		}
	}
	if pd := shape(tools.Call{}, tools.Outcome{Text: "nothing secret"}); pd.Kind != tools.PostAccept {
		t.Errorf("shape of a clean text = %+v; want accept", pd)
	}
}

// root is a cordis root with the scope table and the tool registry loaded.
func root(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(r.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		f, err := r.Use(c, cordis.None{})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Wait(); err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
	}
	return r
}

// Mount needs both the registry and the store; the component mounts the shaper
// from the services it injects, and fails its load when the store service is not
// a store.
//
//dsh:test DH-T-HUMAN-12
func TestMountAndComponent(t *testing.T) {
	r := root(t)
	if _, err := redact.Mount(r, nil, released(t)); err == nil {
		t.Error("Mount without a registry succeeded")
	}
	var svc tools.Service
	g, err := r.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		svc, err = tools.Key.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := redact.Mount(r, svc, nil); err == nil {
		t.Error("Mount without a store succeeded")
	}
	h, err := redact.Mount(r, svc, released(t))
	if err != nil || h == nil {
		t.Fatalf("Mount = %v, %v", h, err)
	}

	if _, err := secret.Key.Provide(r, released(t)); err != nil {
		t.Fatal(err)
	}
	f, err := r.Use(redact.Component, cordis.None{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatalf("component: %v", err)
	}

	wrong := root(t)
	if _, err := wrong.Provide(secret.Key.Name(), "not a store"); err != nil {
		t.Fatal(err)
	}
	f, err = wrong.Use(redact.Component, cordis.None{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err == nil || !strings.Contains(err.Error(), "secret.store") {
		t.Errorf("component with a wrong store service: %v; want a load failure naming it", err)
	}

	noTools := cordis.New()
	noTools.Loggers().ClearExporters()
	t.Cleanup(noTools.Dispose)
	if _, err := noTools.Provide(tools.Key.Name(), "not a registry"); err != nil {
		t.Fatal(err)
	}
	if _, err := secret.Key.Provide(noTools, released(t)); err != nil {
		t.Fatal(err)
	}
	f, err = noTools.Use(redact.Component, cordis.None{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err == nil || !strings.Contains(err.Error(), "tools") {
		t.Errorf("component with a wrong registry service: %v; want a load failure naming it", err)
	}
}
