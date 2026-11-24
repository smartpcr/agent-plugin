// Package compose is the test kit's composition harness (DH-D-UTIL-6,
// util-test-support.md §6.1 "compose"): one call starts a real dsh component
// tree from a shipped bundle, through boot.Run's own steps -- the launch facts,
// the home, the profile stack, loader.Compose, the tree with the loader and the
// catalogue, the settle and the audit -- and ends it, with boot's bounded
// shutdown, when the test ends.
//
// Three boundaries are scripted, and each is an ordinary entry of the tree:
//
//   - util.egress is the recording egress of package egresstest (its component
//     stands in for the production one in the catalogue), so a request or name
//     lookup the test did not allow fails the test;
//   - the entry llm.scripted (ModelComponent) provides, under RuntimeKey, an llm runtime whose
//     route Route is served by a model.Scripted adapter;
//   - the home is a fresh temporary home (temphome.Dir) unless the test names one,
//     and the launch environment is only what Options.Env lists.
//
// Start must be called inside clock.Run: the tree runs in virtual time, and the
// kit's leak rule is the tree's own state after unload plus the bubble's. When
// the test ends the tree is stopped and unloaded; the test fails if boot's
// shutdown did not complete in its bound, if any fiber was left not disposed
// (with the effects it held), or if a goroutine started in the bubble after
// Start is still alive once the bubble has settled -- its stack is printed.
//
// What it replaces of boot: only the fatal path's process-wide crash output is
// not installed (a test process has many trees), and Options.Layers are added
// above the profile's home and overlay layers, below the launcher layer.
package compose

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/egresstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

var _ subprocess.UserManager = noUserManager{}

// Route is the scripted model's route on the composed runtime.
const Route vocab.RouteID = "testkit/scripted"

// ModelComponent is the catalogue name of the entry that provides the runtime.
const ModelComponent = "llm.scripted"

// LayerLabel is the label of the kit's own layer, the lowest of the stack.
const LayerLabel = "testkit"

// RuntimeKey is the service the llm.scripted entry provides: the llm runtime
// with the scripted route registered. The product has no llm runtime entry yet,
// so a component under test that needs a model injects this key.
var RuntimeKey = cordis.NewServiceKey[*llm.Runtime]("testkit.llm")

// Options configure one composed tree.
type Options struct {
	// Bundle is a shipped bundle (profile.BundleNames) stacked on core, or a
	// built-in profile name (profile.Names) whose bundles are used; "" and "core"
	// are core alone.
	Bundle string
	// Layers are in-memory layers added above the bundles, the home's profile and
	// user layers, and below the launcher layer (Layer parses one from YAML).
	Layers []loader.Layer
	// Home is the harness home; "" is a fresh temphome.Dir(t). A home given here
	// is read for its profile and user layers.
	Home string
	// Components are catalogue items for the test's own components, which a
	// layer inserts as entries.
	Components []catalog.Item
	// Scripts are the scripted model's first scripts; Tree.Model.Script adds more.
	Scripts []model.Script
	// Allow are the egress destinations the test allows (egresstest syntax).
	Allow []string
	// Env is the launch environment as NAME=value entries; nil is empty.
	Env []string
	// Safe launches with --safe: the launcher layer disables the plugins group and
	// the optional bundles (profile.SafeOps).
	Safe bool
}

// Tree is a running composed tree.
type Tree struct {
	// Model serves Route; unplayed scripts fail the test at its end.
	Model *model.Scripted
	// Runtime is the llm runtime RuntimeKey provides.
	Runtime *llm.Runtime
	// Egress records every outbound request and lookup of the tree.
	Egress *egresstest.Recorder
	// Loader is the tree's loader service.
	Loader *loader.Service
	// Sharing is the launch's sharing gate, every kind off unless a layer says.
	Sharing *sharing.Gate
	// Data are the launch facts; Data.Home is the home.
	Data launch.Data
	// Layers are the layers composed, lowest first; Issues their skipped ops.
	Layers []loader.Layer
	Issues []loader.Issue
	// Audit is the boot audit of the start.
	Audit boot.Audit
	// App stops the tree early; Start's cleanup stops it otherwise.
	App *launch.Control
	// Stderr is what boot wrote to its diagnostic stream.
	Stderr *testkit.Buffer
}

// Layer parses one layer of the harness YAML dialect, labelled label; a layer
// that does not parse, or has an operation that cannot be decoded, fails the
// test.
func Layer(t testing.TB, label, yaml string) loader.Layer {
	t.Helper()
	l, issues, err := profile.ParseLayer(label, label+".yml", []byte(yaml))
	if err == nil && len(issues) > 0 {
		err = &loader.ParseError{Issues: issues}
	}
	if err != nil {
		t.Fatalf("compose: layer %s: %v", label, err)
	}
	return l
}

// Start composes and starts a tree and returns it once boot has marked it
// ready. It must run inside clock.Run. A launch that ends before ready (a broken
// layer, a required entry that did not load) fails the test with boot's status
// and diagnostics.
func Start(t testing.TB, o Options) *Tree {
	t.Helper()
	self, bubble := current()
	if bubble == "" {
		t.Fatalf("compose: Start must run inside clock.Run (a testing/synctest bubble)")
	}
	baseline := map[int]bool{self: true}
	for _, g := range goroutines() {
		if g.bubble == bubble {
			baseline[g.id] = true
		}
	}

	def, err := definition(o.Bundle)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if o.Home == "" {
		o.Home = temphome.Dir(t)
	}
	tr := &Tree{
		Model:   model.NewScripted(t, o.Scripts...),
		Runtime: llm.New(),
		Egress:  egresstest.New(t, o.Allow...),
		App:     launch.NewApp(),
		Stderr:  &testkit.Buffer{},
	}
	model.RegisterScripted(t, tr.Runtime, Route, tr.Model)
	cat, err := tr.catalog(o.Components)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	own := Layer(t, LayerLabel, "- insert:\n    - id: testkit-model\n      name: "+ModelComponent+"\n")
	steps := &steps{extra: append([]loader.Layer{own}, o.Layers...)}
	opts := boot.Options{
		Request: launch.Request{Args: []string{"dsh"}, Cwd: o.Home, Environ: o.Env, Home: o.Home,
			Profile: def.Name, Safe: o.Safe},
		Profile: def,
		Stderr:  tr.Stderr,
		Catalog: cat,
		App:     tr.App,
		Steps:   steps,
		// A composed test tree never asks the developer's user service manager for
		// a delegated scope: on Linux the provider takes the process-group rung.
		UserManager: noUserManager{},
	}
	done := make(chan int, 1)
	go func() { done <- boot.Run(context.WithoutCancel(t.Context()), opts) }()
	select {
	case <-tr.App.Ready():
	case status := <-done:
		t.Fatalf("compose: the launch ended before ready with status %d: %s", status, tr.Stderr.String())
	}
	s := steps.state
	tr.Loader, tr.Sharing, tr.Data, tr.Layers, tr.Issues, tr.Audit = s.Service, s.Sharing, s.Data, s.Layers, s.Issues, s.Audit
	t.Cleanup(func() { tr.end(t, done, s, bubble, baseline) })
	return tr
}

// end stops the tree and applies the leak rule.
func (tr *Tree) end(t testing.TB, done <-chan int, s *boot.State, bubble string, baseline map[int]bool) {
	tr.App.Stop(launch.Completed, output.OK)
	status := <-done
	if status != output.OK {
		t.Errorf("compose: the tree shut down with status %d (want %d): %s", status, output.OK, tr.Stderr.String())
	}
	if s.Host != nil {
		if stuck := s.Host.Stuck(); len(stuck) > 0 {
			t.Errorf("compose: entries left after unload, with their effects: %s", strings.Join(stuck, "; "))
		}
	}
	synctest.Wait()
	self, _ := current()
	var leaked []string
	for _, g := range goroutines() {
		if g.bubble == bubble && !baseline[g.id] && g.id != self {
			leaked = append(leaked, g.stack)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("compose: %d goroutine(s) still alive after the tree unloaded:\n%s", len(leaked), strings.Join(leaked, "\n\n"))
	}
}

// catalog is the built-in catalogue with util.egress replaced by the recorder's
// component, plus the model entry and the test's components.
func (tr *Tree) catalog(extra []catalog.Item) (*catalog.Catalog, error) {
	util := builtin.Util()
	for i, item := range util {
		if item.Name == egress.Name {
			util[i].Component = tr.Egress.Component()
		}
	}
	rt := tr.Runtime
	mdl := catalog.Item{Name: ModelComponent, Provides: []string{RuntimeKey.Name()},
		Component: cordis.Define(ModelComponent, func(ctx *cordis.Context, _ cordis.None) error {
			_, err := RuntimeKey.Provide(ctx, rt)
			return err
		})}
	return catalog.Build(builtin.Boot(), builtin.Host(), util, []catalog.Item{mdl}, extra)
}

// definition is the profile composed for bundle.
func definition(bundle string) (profile.Definition, error) {
	def := profile.Definition{Name: "compose", Bundles: []string{"core"}, Limits: profile.DefaultLimits()}
	switch {
	case bundle == "" || bundle == "core":
	case contains(profile.Names(), bundle):
		d, err := profile.Lookup(bundle)
		if err != nil {
			return def, err
		}
		def.Name, def.Bundles, def.Limits, def.Required = d.Name, d.Bundles, d.Limits, d.Required
	case contains(profile.BundleNames(), bundle):
		def.Bundles = append(def.Bundles, bundle)
	default:
		return def, fmt.Errorf("%q is neither a shipped bundle %v nor a profile %v", bundle, profile.BundleNames(), profile.Names())
	}
	return def, nil // Reload stays zero: no layer watch runs in a test tree
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// steps are boot's real steps, except that the crash output is not installed
// and the kit's layers are added to the profile's stack.
type steps struct {
	boot.Real
	extra []loader.Layer
	state *boot.State
}

// Guards keeps the log ring and leaves the process-wide crash output alone.
func (st *steps) Guards(_ context.Context, s *boot.State) error {
	s.Ring = diag.NewRing(diag.RingSize)
	return nil
}

// Layers is the real stack with the kit's layers below the launcher layer.
func (st *steps) Layers(ctx context.Context, s *boot.State) error {
	if err := st.Real.Layers(ctx, s); err != nil {
		return err
	}
	n := len(s.Layers)
	layers := append([]loader.Layer(nil), s.Layers[:n-1]...)
	layers = append(layers, st.extra...)
	s.Layers = append(layers, s.Layers[n-1])
	return nil
}

// noUserManager refuses every scope request (boot.Options.UserManager).
type noUserManager struct{}

func (noUserManager) DelegateScope(context.Context, string, int) error {
	return errNoUserManager
}

var errNoUserManager = fmt.Errorf("compose: the test kit asks no user service manager for a scope")

// Tree is the real tree; the state is kept for the cleanup's checks.
func (st *steps) Tree(ctx context.Context, s *boot.State) error {
	st.state = s
	return st.Real.Tree(ctx, s)
}

// goroutine is one goroutine of a runtime.Stack dump.
type goroutine struct {
	id     int
	bubble string // "" outside a synctest bubble
	stack  string
}

var header = regexp.MustCompile(`^goroutine (\d+) \[([^\]]*)\]:`)
var bubbleRE = regexp.MustCompile(`synctest bubble (\d+)`)

func parse(block string) (goroutine, bool) {
	m := header.FindStringSubmatch(block)
	if m == nil {
		return goroutine{}, false
	}
	id, _ := strconv.Atoi(m[1])
	g := goroutine{id: id, stack: block}
	if b := bubbleRE.FindStringSubmatch(m[2]); b != nil {
		g.bubble = b[1]
	}
	return g, true
}

func dump(all bool) string { return dumpFrom(all, 64<<10) }

// dumpFrom is runtime.Stack into a buffer of size bytes, doubled until the dump
// fits.
func dumpFrom(all bool, size int) string {
	buf := make([]byte, size)
	for {
		n := runtime.Stack(buf, all)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// current is the calling goroutine's id and bubble.
func current() (int, string) {
	g, _ := parse(dump(false))
	return g.id, g.bubble
}

func goroutines() []goroutine {
	var out []goroutine
	for _, block := range strings.Split(strings.TrimSpace(dump(true)), "\n\n") {
		if g, ok := parse(strings.TrimSpace(block)); ok {
			out = append(out, g)
		}
	}
	return out
}
