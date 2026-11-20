package loader_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// notices records the Reloaded notices that reach ctx.
type notices struct {
	mu   sync.Mutex
	list []loader.ReloadNotice
}

func listen(t *testing.T, ctx *cordis.Context) *notices {
	t.Helper()
	n := &notices{}
	Must(loader.Reloaded.On(ctx, func(_ *cordis.Context, notice loader.ReloadNotice) error {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.list = append(n.list, notice)
		return nil
	}))
	return n
}

func (n *notices) all() []loader.ReloadNotice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.list)
}

// constructs counts the loads of the Echo entry whose text starts with prefix.
func constructs(log *Log, prefix string) int {
	count := 0
	for _, line := range log.Lines() {
		if strings.HasPrefix(line, "+"+prefix) {
			count++
		}
	}
	return count
}

func kinds(events []loader.LifecycleEvent, id string) []string {
	var out []string
	for _, event := range events {
		if event.ID == id {
			out = append(out, string(event.Kind))
		}
	}
	return out
}

// ------------------------------------------------------------ nested loaders

type ownerHarness struct {
	root   *cordis.Context
	echo   *Log
	fiber  *cordis.Fiber
	nested *loader.Service
	heard  *notices
}

// startOwner loads component Owner, which runs a nested loader of its own over layers.
func startOwner(t *testing.T, layers ...loader.Layer) *ownerHarness {
	t.Helper()
	root, _ := Quiet()
	h := &ownerHarness{root: root, echo: &Log{}}
	components := loader.Components{"EchoPlugin": Echo(h.echo)}
	MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: components})))
	ownerComponent := cordis.Define("Owner", func(ctx *cordis.Context, _ EchoOptions) error {
		h.heard = listen(t, ctx)
		// acquired before the nested loader, so released after its entries
		if _, err := ctx.Effect("owner", func() (cordis.Disposer, error) {
			return func() error { h.echo.Add("-owner"); return nil }, nil
		}); err != nil {
			return err
		}
		nested, err := loader.New(ctx, loader.Options{Resolver: components}, layers...)
		if err != nil {
			return err
		}
		h.nested = nested
		return nil
	})
	h.fiber = MustWait(t, Must(root.Use(ownerComponent, EchoOptions{Times: 1})))
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	return h
}

func inserts(entries ...loader.EntryOptions) loader.Layer {
	return loader.Layer{Label: "preset", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: entries}}}
}

func TestNestedLoaderUnloadsWithItsOwner(t *testing.T) {
	h := startOwner(t, inserts(echo("a", "a"), echo("b", "b")))
	True(t, h.echo.Contains("+a") && h.echo.Contains("+b"), "nested entries not loaded: "+h.echo.String())
	True(t, h.nested.Active(h.nested.Find("a")), "nested entry a inactive")

	Within(t, "owner dispose", func() {
		h.fiber.Dispose()
		_ = h.fiber.Wait()
	})
	lines := h.echo.Lines()
	True(t, slices.Contains(lines, "-a") && slices.Contains(lines, "-b"), "nested entries not unloaded: "+h.echo.String())
	True(t, slices.Index(lines, "-a") < slices.Index(lines, "-owner") && slices.Index(lines, "-b") < slices.Index(lines, "-owner"),
		"nested entries unloaded after their owner's own effects: "+h.echo.String())
	True(t, errors.Is(h.nested.Apply(nil), loader.ErrClosed), "an unloaded nested loader still applies")
}

func TestNestedLoaderIsPrivateToItsOwnerAndReadOnly(t *testing.T) {
	h := startOwner(t, inserts(echo("a", "a")))
	root := Must(loader.Key.Get(h.root))
	True(t, root != h.nested, "the root context sees the nested loader")
	True(t, root.Find("a") == nil, "a nested entry leaked into the root loader")

	entry := h.nested.Find("a")
	True(t, h.nested.EntryOf(h.nested.Fiber(entry)) == entry, "EntryOf does not find the nested entry's fiber")
	True(t, root.EntryOf(h.nested.Fiber(entry)) == nil, "the root loader claims a nested fiber")

	_, err := h.nested.Create("", -1, echo("x", "x"))
	True(t, errors.Is(err, loader.ErrReadOnly), "Create on a nested loader")
	True(t, errors.Is(h.nested.Set("a", echo("a", "z")), loader.ErrReadOnly), "Set on a nested loader")
	True(t, errors.Is(h.nested.Remove("a"), loader.ErrReadOnly), "Remove on a nested loader")
	True(t, errors.Is(h.nested.SetDisabled("a", true), loader.ErrReadOnly), "SetDisabled on a nested loader")
	True(t, h.nested.ApplyLayers([]loader.Layer{{Label: "x", Writable: true, Path: "x.json"}}) != nil,
		"a nested loader accepted a writable layer")
	Equal(t, "+a", h.echo.String())
}

func TestNestedLoaderANewLayerListRestartsOnlyWhatChanged(t *testing.T) {
	h := startOwner(t, inserts(echo("a", "a"), echo("b", "b")))
	Within(t, "apply layers", func() {
		NoError(t, h.nested.ApplyLayers([]loader.Layer{inserts(echo("a", "a"), echo("b", "b2"))}))
	})
	Equal(t, 1, constructs(h.echo, "a"))
	Equal(t, "-b,+b2", strings.Join(h.echo.Lines()[2:], ","))
	heard := h.heard.all()
	Equal(t, 2, len(heard), "one notice for the first load and one for the new layer list")
	Equal(t, "b", strings.Join(heard[1].Updated, ","))
	True(t, heard[1].Loader == h.nested, "notice names another loader")
}

// ---------------------------------------------------------- restart and notices

func TestRestartOneEntryLeavesItsSiblingsAlone(t *testing.T) {
	h := start(t)
	heard := listen(t, h.root)
	h.apply(t, echo("one", "one"), echo("two", "two"), echo("three", "three"))
	changes := 0
	h.loader.OnChange(func() { changes++ })

	Within(t, "restart", func() { NoError(t, h.loader.Restart("two")) })
	Equal(t, 1, constructs(h.echo, "one"))
	Equal(t, 2, constructs(h.echo, "two"))
	Equal(t, 1, constructs(h.echo, "three"))
	all := heard.all()
	Equal(t, 2, len(all), "one notice for the Apply and one for the Restart")
	Equal(t, "two", strings.Join(all[1].Restarted, ","))
	Equal(t, 0, changes, "a restart is not a change to persist")
	Equal(t, "add,restart", strings.Join(kinds(h.loader.Lifecycle(), "two"), ","))

	True(t, errors.Is(h.loader.Restart("missing"), loader.ErrNotFound), "restart of an unknown entry")
}

func TestAConfigEditRestartsOnlyTheChangedEntryWithOneNotice(t *testing.T) {
	h := start(t)
	h.apply(t, echo("one", "one"), echo("two", "two"), echo("three", "three"))
	heard := listen(t, h.root)

	h.apply(t, echo("one", "one"), echo("two", "two!"), echo("three", "three"))
	Equal(t, 1, constructs(h.echo, "one"))
	Equal(t, 2, constructs(h.echo, "two"))
	Equal(t, 1, constructs(h.echo, "three"))
	all := heard.all()
	Equal(t, 1, len(all), "exactly one settled-reload notice")
	Equal(t, "two", strings.Join(all[0].Updated, ","))

	// an edit touching two entries, one rebuilt and one updated, is still one notice
	h.apply(t, echo("one", "one!"), echo("two", "two!"), loader.EntryOptions{ID: "three", Name: "EchoPlugin", Disabled: true})
	all = heard.all()
	Equal(t, 2, len(all), "one notice per edit")
	True(t, h.echo.Contains("+one!") && h.echo.Contains("-three"), "the edit did not settle before its notice: "+h.echo.String())
	Equal(t, "one,three", strings.Join(append(all[1].Updated, all[1].Restarted...), ","))
}

func TestWatchedLayersDebounceABurstOfSavesIntoOneReload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "user.json")
		NoError(t, os.WriteFile(path, []byte(`[{"insert":[`+echoJSON("a", "0")+`]}]`), 0o644))
		root, _ := Quiet()
		echoLog := &Log{}
		MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: loader.Components{"EchoPlugin": Echo(echoLog)}})))
		heard := listen(t, root)
		MustWait(t, Must(root.Use(loader.LayerSetComponent, loader.LayerSetOptions{
			Layers:       []loader.Layer{{Label: "user", Path: path, Writable: true}},
			Watch:        true,
			PollInterval: 10 * time.Millisecond,
			Debounce:     100 * time.Millisecond,
		})))
		synctest.Wait()
		before := len(heard.all())

		for _, text := range []string{"1", "2", "3", "4", "5"} {
			NoError(t, os.WriteFile(path, []byte(`[{"insert":[`+echoJSON("a", text)+`]}]`), 0o644))
			Advance(20 * time.Millisecond)
		}
		Equal(t, before, len(heard.all()), "reloaded inside the debounce window")
		Advance(200 * time.Millisecond)
		Equal(t, before+1, len(heard.all()), "a burst of saves is one reload")
		Equal(t, "a", strings.Join(heard.all()[before].Updated, ","), "the notice lists the changed ids")
		True(t, echoLog.Contains("+5") && !echoLog.Contains("+3"), "the reload did not load the last save only: "+echoLog.String())
		root.Dispose()
	})
}

func echoJSON(id, text string) string {
	return `{"id":"` + id + `","name":"EchoPlugin","config":{"times":1,"text":"` + text + `"}}`
}

// ------------------------------------------------------------ mutation API

func TestLifecycleLogRecordsMutationsInOrder(t *testing.T) {
	h := start(t)
	h.apply(t, group("g"))
	var observed []string
	h.loader.OnLifecycle(func(event loader.LifecycleEvent) { observed = append(observed, string(event.Kind)+" "+event.ID) })

	id := Must(h.loader.Create("g", -1, echo("x", "1")))
	Equal(t, "g:x", id)
	Within(t, "set", func() { NoError(t, h.loader.Set("g:x", echo("x", "2"))) })
	found, ok := h.loader.Lookup("g:x")
	True(t, ok, "Lookup misses a set entry")
	Equal(t, "2", textOf(found))
	Within(t, "remove", func() { NoError(t, h.loader.Remove("g:x")) })
	_, ok = h.loader.Lookup("g:x")
	True(t, !ok, "Lookup finds a removed entry")

	Equal(t, "add,update,remove", strings.Join(kinds(h.loader.Lifecycle(), "g:x"), ","))
	Equal(t, "add g:x,update g:x,remove g:x", strings.Join(observed, ","))
	Equal(t, "+1,-1,+2,-2", h.echo.String())
	events := h.loader.Lifecycle()
	for i := 1; i < len(events); i++ {
		True(t, events[i].Seq > events[i-1].Seq, "lifecycle events out of order")
	}
	last := events[len(events)-1]
	True(t, last.Before != nil && last.After == nil, "a remove carries the entry it removed")
}

func TestMutationsValidateAndSetUpserts(t *testing.T) {
	h := start(t)
	h.apply(t, echo("a", "a"), group("g"))
	tree := h.loader.Snapshot()

	_, err := h.loader.Create("", -1, echo("a", "dup"))
	True(t, err != nil, "a duplicate id was created")
	_, err = h.loader.Create("", -1, loader.EntryOptions{ID: "bad:id", Name: "EchoPlugin"})
	True(t, err != nil, "an invalid id was created")
	_, err = h.loader.Create("a", -1, echo("x", "x"))
	True(t, err != nil, "created under a non-group")
	True(t, errors.Is(h.loader.Remove("nope"), loader.ErrNotFound), "removed an unknown entry")
	Equal(t, len(tree), len(h.loader.Snapshot()), "a refused mutation changed the tree")
	Equal(t, "+a", h.echo.String())

	Within(t, "set new", func() { NoError(t, h.loader.Set("g:new", echo("", "n"))) })
	True(t, h.active("g:new"), "Set did not create the entry")
	moved := Must(h.loader.Move("g:new", "", 0))
	Equal(t, "new", moved)
	True(t, h.active("new") && h.loader.Find("g:new") == nil, "Move did not reparent the entry")
	Equal(t, "new,a,g", strings.Join(topIDs(h.loader.Snapshot()), ","))
	_, err = h.loader.Move("g", "g", 0)
	True(t, err != nil, "moved a group into itself")
}

func TestMutationAndRestartErrorsChangeNothing(t *testing.T) {
	h := start(t)
	h.apply(t, echo("a", "a"), group("g", echo("b", "b")), loader.EntryOptions{ID: "off", Name: "EchoPlugin", Disabled: true})
	events := len(h.loader.Lifecycle())

	True(t, errors.Is(h.loader.Update("nope", echo("", "x")), loader.ErrNotFound), "Update of an unknown entry")
	True(t, h.loader.Update("a", echo("other", "x")) != nil, "Update renamed an entry")
	True(t, h.loader.Update("a", loader.EntryOptions{Name: ""}) != nil, "Update accepted an invalid entry")
	True(t, h.loader.Set("", echo("", "x")) != nil, "Set without a path")
	True(t, h.loader.Set("a", echo("b", "x")) != nil, "Set with a mismatched id")
	_, err := h.loader.Move("nope", "", 0)
	True(t, errors.Is(err, loader.ErrNotFound), "Move of an unknown entry")
	_, err = h.loader.Move("a", "missing", 0)
	True(t, errors.Is(err, loader.ErrNotFound), "Move into an unknown group")
	_, err = h.loader.Move("a", "g", 0)
	NoError(t, err)
	True(t, h.active("g:a") && h.loader.Find("a") == nil, "Move into a group did not reparent")
	_, err = h.loader.Move("g:a", "g", 5)
	NoError(t, err)
	True(t, h.loader.Restart("off") != nil, "restarted a disabled entry")
	True(t, h.active("g:b"), "a sibling was touched")
	Within(t, "restart group", func() { NoError(t, h.loader.Restart("g")) })
	True(t, h.active("g:b") && h.active("g:a"), "a restarted group did not bring its children back before returning")

	kept := kinds(h.loader.Lifecycle()[events:], "g:a")
	Equal(t, "add,move", strings.Join(kept, ","))

	root, _ := Quiet()
	MustWait(t, Must(root.Use(loader.Component, loader.Options{})))
	_, err = loader.New(root, loader.Options{}, loader.Layer{Label: "f", Path: "f.json"})
	True(t, err != nil, "a nested loader accepted a file layer")
	_, err = loader.New(root, loader.Options{}, loader.Layer{Label: "w", Writable: true})
	True(t, err != nil, "a nested loader accepted a writable layer")
	service := Must(loader.Key.Get(root))
	remove := service.OnLifecycle(func(loader.LifecycleEvent) { t.Error("removed observer called") })
	remove()
	root.Dispose()
	True(t, errors.Is(service.Restart("x"), loader.ErrClosed), "restart on an unloaded loader")
}

func TestMutationsReportTheChangeAndASelfDisposedEntryIsNotDisabled(t *testing.T) {
	h := start(t)
	h.apply(t, echo("a", "a"), group("g"))
	changes := 0
	h.loader.OnChange(func() { changes++ })

	Must(h.loader.Create("", -1, echo("c", "c")))
	Must(h.loader.Move("c", "g", -1))
	NoError(t, h.loader.Remove("g:c"))
	Equal(t, 3, changes, "create, move and remove each report a change")

	Within(t, "self dispose", func() { h.fiber("a").Dispose() })
	found, ok := h.loader.Lookup("a")
	True(t, ok && !found.Disabled, "a self-disposed entry was recorded as disabled")
	Equal(t, 3, changes, "a self-disposed entry reported a change to persist")
}

func TestNestedLoaderEvaluatesItsOwnExpressions(t *testing.T) {
	root, _ := Quiet()
	echoLog := &Log{}
	components := loader.Components{"EchoPlugin": Echo(echoLog)}
	MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: components})))
	evaluator := newEvaluator(func(string, loader.Scope) (any, error) { return 2, nil })
	entry := loader.EntryOptions{ID: "e", Name: "EchoPlugin", Config: raw(`{"times":"${{ preset.n }}","text":"e"}`)}
	var nested *loader.Service
	ownerComponent := cordis.Define("Owner", func(ctx *cordis.Context, _ EchoOptions) error {
		var err error
		nested, err = loader.New(ctx, loader.Options{Resolver: components, Evaluator: evaluator}, inserts(entry))
		return err
	})
	MustWait(t, Must(root.Use(ownerComponent, EchoOptions{Times: 1})))
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	Equal(t, "+e e", echoLog.String())
	Equal(t, 1, evaluator.count("config:preset.n"))
	True(t, errors.Is(nested.Update("e", entry), loader.ErrReadOnly), "Update on a nested loader")
	_, err := nested.Move("e", "", 0)
	True(t, errors.Is(err, loader.ErrReadOnly), "Move on a nested loader")
}

func TestRestartWritesNoFileAndANoticeListsTheChangedIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	text := `[{"insert":[` + echoJSON("a", "a") + `,` + echoJSON("b", "b") + `]}]`
	writeFile(t, path, text)
	h := start(t)
	heard := listen(t, h.root)
	set := MustWait(t, Must(h.root.Use(loader.LayerSetComponent, loader.LayerSetOptions{
		Layers: []loader.Layer{{Label: "user", Path: path, Writable: true}},
	}))).Instance().(*loader.LayerSet)

	Within(t, "restart", func() { NoError(t, h.loader.Restart("a")) })
	NoError(t, set.Flush())
	Equal(t, text, readFile(t, path), "a restart wrote the layer file")
	Equal(t, 2, constructs(h.echo, "a"))
	Equal(t, 1, constructs(h.echo, "b"))
	all := heard.all()
	Equal(t, "a,b", strings.Join(all[0].Created, ","), "the first notice lists the created ids")
	Equal(t, "a", strings.Join(all[len(all)-1].Restarted, ","))
}

func textOf(options loader.EntryOptions) string {
	text := string(options.Config)
	at := strings.Index(text, `"text":"`)
	if at < 0 {
		return ""
	}
	text = text[at+len(`"text":"`):]
	return text[:strings.IndexByte(text, '"')]
}

func topIDs(tree []loader.EntryOptions) []string {
	var ids []string
	for _, entry := range tree {
		ids = append(ids, entry.ID)
	}
	return ids
}

// ------------------------------------------------------------ settled notices

func TestAReloadListenerCanCallBackIntoTheLayerSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	writeFile(t, path, `[{"insert":[`+echoJSON("a", "a")+`]}]`)
	h := start(t)
	var set atomic.Pointer[loader.LayerSet]
	seen := make(chan []string, 4)
	Must(loader.Reloaded.On(h.root, func(_ *cordis.Context, _ loader.ReloadNotice) error {
		if s := set.Load(); s != nil {
			_ = s.Layers()
			var owned []string
			for _, id := range []string{"a", "b"} {
				if _, ok := s.Index().Owner(id, ""); ok {
					owned = append(owned, id)
				}
			}
			seen <- owned
		}
		return nil
	}))
	set.Store(MustWait(t, Must(h.root.Use(loader.LayerSetComponent, loader.LayerSetOptions{
		Layers: []loader.Layer{{Label: "user", Path: path, Writable: true}},
	}))).Instance().(*loader.LayerSet))

	writeFile(t, path, `[{"insert":[`+echoJSON("a", "a")+`,`+echoJSON("b", "b")+`]}]`)
	Within(t, "reload with a listener reading the set", func() { NoError(t, set.Load().Reload()) })
	Within(t, "flush", func() { NoError(t, set.Load().Flush()) })
	Equal(t, "a,b", strings.Join(<-seen, ","), "the listener saw the index from before the reload")
}

func TestARejectedConfigUpdateIsAnIssueNotAnUpdate(t *testing.T) {
	h := start(t)
	h.apply(t, echo("a", "a"), echo("b", "b"))
	heard := listen(t, h.root)

	rejected := echo("a", "a")
	rejected.Config = json.RawMessage(`{"times":0,"text":"a"}`)
	h.apply(t, rejected, echo("b", "b!"))
	all := heard.all()
	Equal(t, 1, len(all))
	Equal(t, "b", strings.Join(all[0].Updated, ","), "a refused update was reported as applied")
	True(t, slices.ContainsFunc(all[0].Issues, func(issue loader.Issue) bool { return issue.Path == "a" }),
		"the refused update is not an issue of the notice")
	True(t, h.active("a"), "the old config of a stopped running")
	Equal(t, 1, constructs(h.echo, "a"))
}

func TestALayerFilesCompositionIssuesReachTheNotice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	writeFile(t, path, `[{"insert":[`+echoJSON("a", "a")+`]},{"id":"missing","remove":true}]`)
	h := start(t)
	heard := listen(t, h.root)
	set := MustWait(t, Must(h.root.Use(loader.LayerSetComponent, loader.LayerSetOptions{
		Layers: []loader.Layer{{Label: "user", Path: path}},
	}))).Instance().(*loader.LayerSet)
	missing := func(notice loader.ReloadNotice) bool {
		return slices.ContainsFunc(notice.Issues, func(issue loader.Issue) bool {
			return strings.Contains(issue.Path+" "+issue.Message, "missing")
		})
	}
	True(t, missing(heard.all()[0]), "the initial notice lacks the unmatched operation")

	writeFile(t, path, `[{"insert":[`+echoJSON("a", "a!")+`]},{"id":"missing","remove":true}]`)
	NoError(t, set.Reload())
	all := heard.all()
	True(t, missing(all[len(all)-1]), "the reload notice lacks the unmatched operation")
}

func TestConcurrentSetsOfOneAbsentPathAllSucceed(t *testing.T) {
	h := start(t)
	h.apply(t, group("g"))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() { errs <- h.loader.Set("g:x", echo("x", string(rune('a'+i)))) })
	}
	Within(t, "concurrent sets", wg.Wait)
	close(errs)
	for err := range errs {
		NoError(t, err)
	}
	group := h.loader.Snapshot()[0]
	Equal(t, 1, len(group.Children), "concurrent Set created duplicates")

	// a Set racing a Remove of the same path recreates or updates, never fails
	for range 4 {
		wg.Go(func() { _ = h.loader.Remove("g:x") })
		wg.Go(func() { NoError(t, h.loader.Set("g:x", echo("x", "z"))) })
		Within(t, "set racing remove", wg.Wait)
	}
}
