//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

const nestedTimeout = 5 * time.Second

func nestedWithin(what string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-time.After(nestedTimeout):
		return fmt.Errorf("%s did not return within %s", what, nestedTimeout)
	}
}

func nestedEcho(id, text string) loader.EntryOptions {
	config, _ := json.Marshal(testkit.EchoOptions{Times: 1, Text: text})
	return loader.EntryOptions{ID: id, Name: "EchoPlugin", Config: config}
}

type nestedNotices struct {
	mu   sync.Mutex
	list []loader.ReloadNotice
}

func (n *nestedNotices) add(notice loader.ReloadNotice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, notice)
}

func (n *nestedNotices) all() []loader.ReloadNotice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.list)
}

type nestedWorld struct {
	root *cordis.Context
	svc  *loader.Service
	echo *testkit.Log

	owner  *cordis.Fiber
	nested *loader.Service

	heard    *nestedNotices
	changes  int
	observed []string
}

func (w *nestedWorld) startRoot() error {
	root, _ := testkit.Quiet()
	w.root = root
	w.echo = &testkit.Log{}
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: w.components()})
	if err != nil {
		return err
	}
	if err := w.wait("loader start", fiber); err != nil {
		return err
	}
	w.svc, err = loader.Key.Get(root)
	return err
}

func (w *nestedWorld) components() loader.Components {
	return loader.Components{"EchoPlugin": testkit.Echo(w.echo)}
}

func (w *nestedWorld) wait(what string, fiber *cordis.Fiber) error {
	var waitErr error
	if err := nestedWithin(what, func() { waitErr = fiber.Wait() }); err != nil {
		return err
	}
	return waitErr
}

func (w *nestedWorld) close() error {
	if w.root == nil {
		return nil
	}
	return nestedWithin("root dispose", w.root.Dispose)
}

func (w *nestedWorld) listen() error {
	w.heard = &nestedNotices{}
	_, err := loader.Reloaded.On(w.root, func(_ *cordis.Context, notice loader.ReloadNotice) error {
		w.heard.add(notice)
		return nil
	})
	return err
}

func (w *nestedWorld) constructs(prefix string) int {
	n := 0
	for _, line := range w.echo.Lines() {
		if line == "+"+prefix || strings.HasPrefix(line, "+"+prefix+"!") {
			n++
		}
	}
	return n
}

func (w *nestedWorld) apply(entries ...loader.EntryOptions) error {
	var applyErr error
	if err := nestedWithin("apply", func() { applyErr = w.svc.Apply(entries) }); err != nil {
		return err
	}
	return applyErr
}

func (w *nestedWorld) lifecycleKinds(id string) string {
	var out []string
	for _, event := range w.svc.Lifecycle() {
		if event.ID == id {
			out = append(out, string(event.Kind))
		}
	}
	return strings.Join(out, ",")
}

// ------------------------------------------------------------ nested loaders

func (w *nestedWorld) ownerWithNestedLoader(name, a, b string) error {
	if err := w.startRoot(); err != nil {
		return err
	}
	layer := loader.Layer{Label: "preset", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{
		nestedEcho(a, a), nestedEcho(b, b),
	}}}}
	owner := cordis.Define(name, func(ctx *cordis.Context, _ testkit.EchoOptions) error {
		// acquired before the nested loader, so released after its entries
		if _, err := ctx.Effect("owner", func() (cordis.Disposer, error) {
			return func() error { w.echo.Add("-owner"); return nil }, nil
		}); err != nil {
			return err
		}
		nested, err := loader.New(ctx, loader.Options{Resolver: w.components()}, layer)
		if err != nil {
			return err
		}
		w.nested = nested
		return nil
	})
	fiber, err := w.root.Use(owner, testkit.EchoOptions{Times: 1})
	if err != nil {
		return err
	}
	w.owner = fiber
	if err := w.wait("owner start", fiber); err != nil {
		return err
	}
	if w.nested == nil {
		return errors.New("the owner did not create its nested loader")
	}
	return nil
}

func (w *nestedWorld) nestedEntriesActive(a, b string) error {
	for _, id := range []string{a, b} {
		entry := w.nested.Find(id)
		if entry == nil || !w.nested.Active(entry) {
			return fmt.Errorf("nested entry %q is not active (log %s)", id, w.echo.String())
		}
		if !w.echo.Contains("+" + id) {
			return fmt.Errorf("nested entry %q was not constructed (log %s)", id, w.echo.String())
		}
		if root := w.svc.Find(id); root != nil {
			return fmt.Errorf("nested entry %q leaked into the root loader", id)
		}
	}
	return nil
}

func (w *nestedWorld) ownerUnloaded(string) error {
	return nestedWithin("owner dispose and wait", func() {
		w.owner.Dispose()
		_ = w.owner.Wait()
	})
}

func (w *nestedWorld) nestedDisposersRan(a, b string) error {
	lines := w.echo.Lines()
	for _, id := range []string{a, b} {
		if !slices.Contains(lines, "-"+id) {
			return fmt.Errorf("disposer of nested entry %q had not run when Wait returned (log %s)", id, w.echo.String())
		}
	}
	return nil
}

func (w *nestedWorld) nestedBeforeOwner() error {
	lines := w.echo.Lines()
	owner := slices.Index(lines, "-owner")
	if owner < 0 {
		return fmt.Errorf("the owner's effect was not disposed (log %s)", w.echo.String())
	}
	for _, line := range lines[owner:] {
		if strings.HasPrefix(line, "-") && line != "-owner" {
			return fmt.Errorf("nested %q disposed after the owner's effects (log %s)", line, w.echo.String())
		}
	}
	return nil
}

func (w *nestedWorld) nestedRefusesApply() error {
	if err := w.nested.Apply(nil); !errors.Is(err, loader.ErrClosed) {
		return fmt.Errorf("an unloaded nested loader Apply returned %v, want ErrClosed", err)
	}
	return nil
}

// ---------------------------------------------------------- restart and notices

func (w *nestedWorld) threeLoadedEntries(one, two, three string) error {
	if err := w.startRoot(); err != nil {
		return err
	}
	if err := w.apply(nestedEcho(one, one), nestedEcho(two, two), nestedEcho(three, three)); err != nil {
		return err
	}
	for _, id := range []string{one, two, three} {
		if entry := w.svc.Find(id); entry == nil || !w.svc.Active(entry) {
			return fmt.Errorf("entry %q is not active", id)
		}
	}
	w.svc.OnChange(func() { w.changes++ })
	return w.listen()
}

func (w *nestedWorld) configChangesAndRestarts(id string) error {
	var entries []loader.EntryOptions
	for _, existing := range []string{"one", "two", "three"} {
		text := existing
		if existing == id {
			text += "!"
		}
		entries = append(entries, nestedEcho(existing, text))
	}
	return w.apply(entries...)
}

func (w *nestedWorld) explicitRestart(id string) error {
	var restartErr error
	if err := nestedWithin("restart", func() { restartErr = w.svc.Restart(id) }); err != nil {
		return err
	}
	if restartErr != nil {
		return restartErr
	}
	if err := w.svc.Restart("missing"); !errors.Is(err, loader.ErrNotFound) {
		return fmt.Errorf("restart of an unknown entry returned %v, want ErrNotFound", err)
	}
	return nil
}

func (w *nestedWorld) constructedTimes(id string, want int) error {
	if got := w.constructs(id); got != want {
		return fmt.Errorf("entry %q constructed %d times, want %d (log %s)", id, got, want, w.echo.String())
	}
	return nil
}

func (w *nestedWorld) noticesEmitted(want int) error {
	if got := len(w.heard.all()); got != want {
		return fmt.Errorf("%d settled-reload notices, want %d", got, want)
	}
	return nil
}

func (w *nestedWorld) noticeLists(id, field string) error {
	all := w.heard.all()
	if len(all) == 0 {
		return errors.New("no notice was emitted")
	}
	notice := all[len(all)-1]
	got := notice.Updated
	if field == "restarted" {
		got = notice.Restarted
	}
	if strings.Join(got, ",") != id {
		return fmt.Errorf("notice %s = %v, want [%s] (updated %v, restarted %v)", field, got, id, notice.Updated, notice.Restarted)
	}
	if notice.Loader != w.svc {
		return errors.New("the notice names another loader")
	}
	return nil
}

func (w *nestedWorld) noChangeReported() error {
	if w.changes != 0 {
		return fmt.Errorf("%d changes reported, want 0: a restart is not a change to persist", w.changes)
	}
	return nil
}

// ------------------------------------------------------------ mutation API

func (w *nestedWorld) loaderWithGroup(id string) error {
	if err := w.startRoot(); err != nil {
		return err
	}
	if err := w.apply(loader.EntryOptions{ID: id, Name: "group", Group: true}); err != nil {
		return err
	}
	w.svc.OnLifecycle(func(event loader.LifecycleEvent) {
		w.observed = append(w.observed, string(event.Kind)+" "+event.ID)
	})
	return nil
}

func (w *nestedWorld) createUnder(id, parent string) error {
	got, err := w.svc.Create(parent, -1, nestedEcho(id, "1"))
	if err != nil {
		return err
	}
	if want := parent + ":" + id; got != want {
		return fmt.Errorf("Create returned path %q, want %q", got, want)
	}
	return w.wantActive(got)
}

func (w *nestedWorld) setText(path, text string) error {
	id := path[strings.LastIndex(path, ":")+1:]
	var setErr error
	if err := nestedWithin("set", func() { setErr = w.svc.Set(path, nestedEcho(id, text)) }); err != nil {
		return err
	}
	if setErr != nil {
		return setErr
	}
	if !w.echo.Contains("+" + text) {
		return fmt.Errorf("Set did not rebuild %q (log %s)", path, w.echo.String())
	}
	return w.wantActive(path)
}

func (w *nestedWorld) remove(path string) error {
	var removeErr error
	if err := nestedWithin("remove", func() { removeErr = w.svc.Remove(path) }); err != nil {
		return err
	}
	if removeErr != nil {
		return removeErr
	}
	if _, ok := w.svc.Lookup(path); ok {
		return fmt.Errorf("Lookup still finds removed %q", path)
	}
	return nil
}

func (w *nestedWorld) wantActive(path string) error {
	entry := w.svc.Find(path)
	if entry == nil || !w.svc.Active(entry) {
		return fmt.Errorf("entry %q is not active", path)
	}
	return nil
}

func (w *nestedWorld) lifecycleLogIs(id, want string) error {
	if got := w.lifecycleKinds(id); got != want {
		return fmt.Errorf("lifecycle log for %q = %q, want %q", id, got, want)
	}
	return nil
}

func (w *nestedWorld) observersSaw(want string) error {
	if got := strings.Join(w.observed, ","); got != want {
		return fmt.Errorf("observers saw %q, want %q", got, want)
	}
	return nil
}

func (w *nestedWorld) sequenceIncreasing() error {
	events := w.svc.Lifecycle()
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			return fmt.Errorf("lifecycle event %d has seq %d after %d", i, events[i].Seq, events[i-1].Seq)
		}
	}
	return nil
}

func (w *nestedWorld) lastCarriesRemoved() error {
	events := w.svc.Lifecycle()
	if len(events) == 0 {
		return errors.New("empty lifecycle log")
	}
	last := events[len(events)-1]
	if last.Kind != "remove" || last.Before == nil || last.After != nil {
		return fmt.Errorf("last event %q: before=%v after=%v, want a remove carrying the removed entry", last.Kind, last.Before, last.After)
	}
	return nil
}

func InitializeScenario_loader_layers_and_write_back_nested_loaders_and_entry_restart(ctx *godog.ScenarioContext) {
	w := &nestedWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if closeErr := w.close(); closeErr != nil && err == nil {
			return c, closeErr
		}
		return c, nil
	})

	ctx.Step(`^component "([^"]+)" running a nested loader with entries "([^"]+)" and "([^"]+)"$`, w.ownerWithNestedLoader)
	ctx.Step(`^the nested entries "([^"]+)" and "([^"]+)" are active$`, w.nestedEntriesActive)
	ctx.Step(`^"([^"]+)" is unloaded$`, w.ownerUnloaded)
	ctx.Step(`^the disposers of nested entries "([^"]+)" and "([^"]+)" have run before C's Wait returns$`, w.nestedDisposersRan)
	ctx.Step(`^the nested entries were disposed before C's own effects$`, w.nestedBeforeOwner)
	ctx.Step(`^the nested loader refuses further applies$`, w.nestedRefusesApply)

	ctx.Step(`^three loaded entries "([^"]+)", "([^"]+)" and "([^"]+)"$`, w.threeLoadedEntries)
	ctx.Step(`^the config of entry "([^"]+)" changes and it is restarted$`, w.configChangesAndRestarts)
	ctx.Step(`^entry "([^"]+)" is restarted through the loader$`, w.explicitRestart)
	ctx.Step(`^entry "([^"]+)" has been constructed (\d+) times?$`, w.constructedTimes)
	ctx.Step(`^exactly (\d+) settled-reload notices? (?:is|are) emitted$`, w.noticesEmitted)
	ctx.Step(`^that notice lists "([^"]+)" as (updated|restarted)$`, w.noticeLists)
	ctx.Step(`^no change to persist was reported$`, w.noChangeReported)

	ctx.Step(`^a loader with an empty group "([^"]+)"$`, w.loaderWithGroup)
	ctx.Step(`^entry "([^"]+)" is added under "([^"]+)" through the mutation API$`, w.createUnder)
	ctx.Step(`^entry "([^"]+)" is set to text "([^"]+)" through the mutation API$`, w.setText)
	ctx.Step(`^entry "([^"]+)" is removed through the mutation API$`, w.remove)
	ctx.Step(`^the lifecycle log for "([^"]+)" is "([^"]+)"$`, w.lifecycleLogIs)
	ctx.Step(`^lifecycle observers saw "([^"]+)"$`, w.observersSaw)
	ctx.Step(`^the lifecycle sequence numbers are strictly increasing$`, w.sequenceIncreasing)
	ctx.Step(`^the last lifecycle event carries the removed entry$`, w.lastCarriesRemoved)
}

func TestE2E_loader_layers_and_write_back_nested_loaders_and_entry_restart(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_loader_layers_and_write_back_nested_loaders_and_entry_restart,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"loader_layers_and_write_back_nested_loaders_and_entry_restart.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
