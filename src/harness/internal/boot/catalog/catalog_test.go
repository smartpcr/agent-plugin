package catalog_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

func component(name string) *cordis.Component {
	return cordis.Define(name, func(*cordis.Context, cordis.None) error { return nil })
}

func item(name string) catalog.Item { return catalog.Item{Name: name, Component: component(name)} }

type webView struct{ URL string }

// TC-04a: the compiled-in catalogue, printed, equals the committed golden list.
func TestCatalogListsComponentsByName(t *testing.T) {
	c, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := c.Print(&out); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "catalog", out.Bytes())
	for _, it := range c.Items() {
		if got, ok := c.Resolve(it.Name); !ok || got != it.Component {
			t.Fatalf("%s does not resolve to its component", it.Name)
		}
	}
}

// DH-T-BOOT-1: Build rejects malformed names, duplicates and nil components.
func TestBuildRejects(t *testing.T) {
	nameless := item("boot.thing")
	nameless.Component = nil
	mismatch := item("boot.thing")
	mismatch.Component = component("boot.other")
	cases := []struct {
		items []catalog.Item
		want  string
	}{
		{[]catalog.Item{item("agents")}, "is not"},
		{[]catalog.Item{item("boot.a.b.c")}, "is not"},
		{[]catalog.Item{item("agent-driver")}, "is not"},
		{[]catalog.Item{item("boot.fsTools")}, "is not"},
		{[]catalog.Item{item("boot.fs_tools")}, "is not"},
		{[]catalog.Item{item("boot.a--b")}, "is not"},
		{[]catalog.Item{item("nope.thing")}, `family "nope" is not registered`},
		{[]catalog.Item{nameless}, "has no component"},
		{[]catalog.Item{mismatch}, `declares component "boot.other"`},
		{[]catalog.Item{item("boot.thing"), item("boot.thing")}, "declared twice"},
		{[]catalog.Item{{Name: "boot.p", Component: component("boot.p"), Provides: []string{"a", "a"}}}, "repeated"},
		{[]catalog.Item{{Name: "boot.p", Component: component("boot.p"), Provides: []string{""}}}, "empty"},
		{[]catalog.Item{{Name: "boot.v", Component: component("boot.v"), View: reflect.TypeFor[webView]()}}, "provides no service"},
		{[]catalog.Item{{Name: "boot.v", Component: component("boot.v"), Provides: []string{"web"}, View: reflect.TypeFor[struct{ F func() }]()}}, "view"},
		{[]catalog.Item{
			{Name: "boot.v", Component: component("boot.v"), Provides: []string{"web"}, View: reflect.TypeFor[webView]()},
			{Name: "boot.w", Component: component("boot.w"), Provides: []string{"web"}, View: reflect.TypeFor[webView]()},
		}, "both declare a view of web"},
		{[]catalog.Item{{Name: "boot.p", Component: component("boot.p"), Platforms: []string{"plan9"}}}, "platform"},
		{[]catalog.Item{{Name: "boot.p", Component: component("boot.p"), Platforms: []string{"linux", "linux"}}}, "repeated"},
	}
	for _, c := range cases {
		_, err := catalog.Build(c.items)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: got %v, want %q", c.items[0].Name, err, c.want)
		}
	}
	if _, err := catalog.BuildFor("plan9"); err == nil {
		t.Fatal("unknown build platform accepted")
	}
}

func TestNames(t *testing.T) {
	for _, name := range []string{"session.store", "host.web", "storage.spill.policy", "ui.remote-images", "x.session-share", "tool.todo"} {
		if err := catalog.CheckName(name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// DH-T-BOOT-1: Resolve distinguishes an unknown name from one not on this platform;
// two catalogues coexist in one process.
func TestLookupAndPlatforms(t *testing.T) {
	items := []catalog.Item{
		{Name: "boot.win", Component: component("boot.win"), Platforms: []string{"windows"}},
		{Name: "boot.any", Component: component("boot.any")},
		{Name: "web.launch", Component: component("web.launch"), Provides: []string{"web"}, View: reflect.TypeFor[webView]()},
		{Name: "web.mac", Component: component("web.mac"), Provides: []string{"mac"}, View: reflect.TypeFor[webView](), Platforms: []string{"darwin"}},
	}
	linux, err := catalog.BuildFor("linux", items)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := catalog.BuildFor("windows", items[:1])
	if err != nil {
		t.Fatal(err)
	}
	var resolver loader.Resolver = linux
	if _, ok := resolver.Resolve("boot.win"); ok {
		t.Fatal("windows-only component resolved on linux")
	}
	if _, err := linux.Lookup("boot.win"); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if _, err := linux.Lookup("boot.none"); !errors.Is(err, catalog.ErrUnknown) {
		t.Fatalf("got %v", err)
	}
	if _, ok := windows.Resolve("boot.win"); !ok {
		t.Fatal("windows catalogue lacks its component")
	}
	if _, ok := windows.Resolve("boot.any"); ok {
		t.Fatal("catalogues share state")
	}
	got, err := linux.Lookup("boot.any")
	if err != nil || got.Name != "boot.any" {
		t.Fatalf("got %v, %v", got, err)
	}
	views := linux.Views()
	if len(views) != 1 || views["web"] != reflect.TypeFor[webView]() {
		t.Fatalf("views %v", views)
	}
	names := []string{}
	for _, it := range linux.Items() {
		names = append(names, it.Name)
	}
	if strings.Join(names, " ") != "boot.any boot.win web.launch web.mac" {
		t.Fatalf("items %v", names)
	}
	var out bytes.Buffer
	if err := linux.Print(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "darwin") || !strings.Contains(out.String(), "catalog_test.webView") {
		t.Fatalf("print:\n%s", out.String())
	}
}

// Items are frozen: changing a returned item does not change the catalogue.
func TestFrozen(t *testing.T) {
	items := []catalog.Item{{Name: "boot.p", Component: component("boot.p"), Provides: []string{"p"}, Platforms: []string{"windows", "linux"}}}
	c, err := catalog.BuildFor("linux", items)
	if err != nil {
		t.Fatal(err)
	}
	items[0].Provides[0] = "changed"
	got := c.Items()
	got[0].Platforms[0] = "changed"
	again, _ := c.Lookup("boot.p")
	if again.Provides[0] != "p" || again.Platforms[0] != "linux" {
		t.Fatalf("catalogue changed: %+v", again)
	}
}
