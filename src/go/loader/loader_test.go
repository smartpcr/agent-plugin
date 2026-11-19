package loader_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/go/timer"
)

type harness struct {
	root   *cordis.Context
	loader *loader.Service
	logs   *cordis.MemoryExporter
	echo   *Log
}

// start loads the loader into a quiet root, resolving the shared fixtures by name.
func start(t *testing.T) *harness {
	t.Helper()
	root, logs := Quiet()
	echo := &Log{}
	components := loader.Components{
		"TimerService":     timer.Component,
		"EchoPlugin":       Echo(echo),
		"CounterService":   Counter,
		"FailingComponent": Failing,
	}
	MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: components})))
	h := &harness{root: root, loader: Must(loader.Key.Get(root)), logs: logs, echo: echo}
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	return h
}

func (h *harness) apply(t *testing.T, entries ...loader.EntryOptions) {
	t.Helper()
	Within(t, "apply", func() { NoError(t, h.loader.Apply(entries)) })
}

func (h *harness) active(id string) bool {
	entry := h.loader.Find(id)
	return entry != nil && h.loader.Active(entry)
}

func (h *harness) fiber(id string) *cordis.Fiber {
	entry := h.loader.Find(id)
	if entry == nil {
		return nil
	}
	return h.loader.Fiber(entry)
}

func (h *harness) text(id string) string {
	entry := h.loader.Find(id)
	if entry == nil {
		return ""
	}
	var config EchoOptions
	_ = json.Unmarshal(h.loader.Options(entry).Config, &config)
	return config.Text
}

func echo(id, text string) loader.EntryOptions {
	config, _ := json.Marshal(EchoOptions{Times: 1, Text: text})
	return loader.EntryOptions{ID: id, Name: "EchoPlugin", Config: config}
}

func group(id string, children ...loader.EntryOptions) loader.EntryOptions {
	return loader.EntryOptions{ID: id, Name: "group", Group: true, Children: children}
}

func echoFile(entries ...[2]string) string {
	var lines []string
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf(`  { "id": %q, "name": "EchoPlugin", "config": { "times": 1, "text": %q } }`, entry[0], entry[1]))
	}
	return "[\n" + strings.Join(lines, ",\n") + "\n]\n"
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	NoError(t, os.WriteFile(path, []byte(text), 0o644))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	NoError(t, err)
	return string(data)
}

func useFile(t *testing.T, h *harness, options loader.ConfigFileOptions) *loader.ConfigFile {
	t.Helper()
	fiber := MustWait(t, Must(h.root.Use(loader.ConfigFileComponent, options)))
	return fiber.Instance().(*loader.ConfigFile)
}

// ---------------------------------------------------------------- loader

func TestLoaderMaterialisesAnEntryTree(t *testing.T) {
	h := start(t)
	entries := []loader.EntryOptions{
		{ID: "timer", Name: "TimerService"},
		group("group", echo("echo", "loaded")),
	}
	h.apply(t, entries...)
	True(t, h.active("timer"), "timer entry inactive")
	True(t, h.active("group:echo"), "nested entry inactive")
	Equal(t, "+loaded", h.echo.String())

	Within(t, "disable", func() { NoError(t, h.loader.SetDisabled("group:echo", true)) })
	Equal(t, "+loaded,-loaded", h.echo.String())
	True(t, h.fiber("group:echo") == nil, "disabled entry still has a fiber")

	Within(t, "enable", func() { NoError(t, h.loader.SetDisabled("group:echo", false)) })
	Eventually(t, func() bool { return h.echo.String() == "+loaded,-loaded,+loaded" }, "entry did not come back: "+h.echo.String())

	// removing the group takes its children with it
	h.apply(t, entries[0])
	Equal(t, "+loaded,-loaded,+loaded,-loaded", h.echo.String())
	True(t, h.loader.Find("group") == nil, "group entry survived removal")
}

func TestLoaderChildrenOfADisabledGroupDoNotRun(t *testing.T) {
	h := start(t)
	disabled := group("g", echo("child", "c"))
	disabled.Disabled = true
	h.apply(t, disabled)
	True(t, h.fiber("g:child") == nil, "child of a disabled group has a fiber")
}

func TestLoaderChildrenComeBackAfterTheirGroupIsRebuilt(t *testing.T) {
	h := start(t)
	h.apply(t, group("g", echo("child", "c")))
	Eventually(t, func() bool { return h.active("g:child") }, "child never loaded")

	Within(t, "disable", func() { NoError(t, h.loader.SetDisabled("g", true)) })
	True(t, h.fiber("g:child") == nil, "child survived its disabled group")

	Within(t, "enable", func() { NoError(t, h.loader.SetDisabled("g", false)) })
	Eventually(t, func() bool { return h.active("g:child") }, "child did not come back with its group")
}

func TestLoaderAPendingGroupHoldsItsChildrenBack(t *testing.T) {
	h := start(t)
	pending := group("g", echo("child", "c"))
	pending.Inject = []string{"counter"}
	h.apply(t, pending)
	True(t, h.fiber("g:child") == nil, "child of a pending group loaded")

	MustWait(t, Must(h.root.Use(Counter, nil)))
	Eventually(t, func() bool { return h.active("g:child") }, "child did not load with its group")
}

func TestLoaderAnInvalidOrFailingEntryDoesNotStopItsSiblings(t *testing.T) {
	h := start(t)
	h.apply(t,
		loader.EntryOptions{ID: "bad:id", Name: "EchoPlugin"},
		loader.EntryOptions{ID: "nameless"},
		loader.EntryOptions{ID: "unknown", Name: "NoSuchComponent"},
		loader.EntryOptions{ID: "broken", Name: "FailingComponent"},
		echo("ok", "fine"),
	)
	True(t, h.active("ok"), "valid sibling did not load")
	True(t, h.loader.Find("bad:id") == nil && h.loader.Find("nameless") == nil, "invalid entries were created")
	Equal(t, cordis.Failed, h.fiber("broken").State())

	errorsText := strings.Join(Texts(h.logs, cordis.LevelError), "\n")
	for _, want := range []string{"must not contain", "requires a name", "cannot resolve"} {
		True(t, strings.Contains(errorsText, want), "missing "+want+" in:\n"+errorsText)
	}
}

func TestLoaderIsolateLabelsArePerServiceAndEmptyIsASharedLabel(t *testing.T) {
	h := start(t)
	shared := map[string]string{"timer": "shared", "counter": "shared"}
	h.apply(t,
		loader.EntryOptions{ID: "timer", Name: "TimerService", Isolate: shared},
		loader.EntryOptions{ID: "counter", Name: "CounterService", Isolate: shared},
		loader.EntryOptions{ID: "a", Name: "CounterService", Isolate: map[string]string{"counter": ""}},
		loader.EntryOptions{ID: "b", Name: "CounterService", Isolate: map[string]string{"counter": ""}},
	)
	True(t, h.active("timer") && h.active("counter"), "two services under one label collided")
	// same realm: exactly one provides "counter", the other fails as already registered.
	// Siblings load concurrently, so which one wins is not specified.
	states := fmt.Sprint(h.fiber("a").State(), h.fiber("b").State())
	True(t, states == "Active Failed" || states == "Failed Active", `entries in the "" realm did not share it: `+states)
}

// ------------------------------------------------------------ config file

func TestConfigFileReadsAndRewritesAJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.json")
	writeFile(t, path, echoFile([2]string{"echo", "file"}))
	h := start(t)
	useFile(t, h, loader.ConfigFileOptions{Path: path})
	True(t, h.active("echo"), "entry from file is not active")
	Equal(t, "+file", h.echo.String())

	Within(t, "update", func() { NoError(t, h.fiber("echo").Update(EchoOptions{Times: 2, Text: "file"}, false)) })
	Eventually(t, func() bool { return strings.Contains(readFile(t, path), `"times": 2`) }, "config was not written back")
}

func TestConfigFileARuntimeSaveKeepsAConcurrentFileEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.json")
	writeFile(t, path, echoFile([2]string{"a", "a"}, [2]string{"b", "b"}))
	h := start(t)
	file := useFile(t, h, loader.ConfigFileOptions{Path: path})

	// the file changes "b" while the runtime changes "a", before the file is re-read
	writeFile(t, path, echoFile([2]string{"a", "a"}, [2]string{"b", "edited"}))
	Within(t, "update", func() { NoError(t, h.fiber("a").Update(EchoOptions{Times: 2, Text: "a"}, false)) })
	Within(t, "save", file.Save)
	Within(t, "save", file.Save)

	text := readFile(t, path)
	True(t, strings.Contains(text, `"edited"`), "the file edit was lost:\n"+text)
	True(t, strings.Contains(text, `"times": 2`), "the runtime edit was lost:\n"+text)
	Equal(t, "edited", h.text("b"))
}

func TestConfigFileADeletedOrCorruptFileKeepsTheLiveTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.json")
	writeFile(t, path, echoFile([2]string{"a", "a"}))
	h := start(t)
	file := useFile(t, h, loader.ConfigFileOptions{Path: path})

	NoError(t, os.Remove(path))
	Within(t, "reload", file.Reload)
	True(t, h.active("a"), "entry was dropped when the file vanished")
	_, err := os.Stat(path)
	True(t, os.IsNotExist(err), "a missing file was recreated at runtime")

	writeFile(t, path, "null")
	Within(t, "reload", file.Reload)
	True(t, h.active("a"), "entry was dropped for a non-array file")
	Equal(t, "null", readFile(t, path))
	Equal(t, 2, Count(h.logs, cordis.LevelWarn))
}

func TestConfigFileAnonymousEntriesSurviveAnEditToANeighbour(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.json")
	anonymous := `  { "name": "EchoPlugin", "config": { "times": 1, "text": "anon" } },` + "\n"
	writeFile(t, path, "[\n"+anonymous+`  { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }`+"\n]")
	h := start(t)
	file := useFile(t, h, loader.ConfigFileOptions{Path: path})

	writeFile(t, path, "[\n"+anonymous+`  { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b2" } }`+"\n]")
	Within(t, "reload", file.Reload)
	Equal(t, "b2", h.text("b"))

	// not restarted by the reload: exactly one "-anon", from the final dispose
	Within(t, "dispose", h.root.Dispose)
	Equal(t, 1, h.echo.Count("-anon"), h.echo.String())
}

// upstream include/tests/watch.spec.ts: "keeps watching the file across its own rename-based writes"
func TestConfigFileWatchingSurvivesRenameBasedSaves(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "cordis.json")
	writeFile(t, path, echoFile([2]string{"a", "v1"}))
	h := start(t)
	useFile(t, h, loader.ConfigFileOptions{Path: path, Watch: true, PollInterval: 20e6})

	// an editor-style save: write a temporary file, then rename it over the config. On
	// Windows the rename is refused while the watcher's poll has the file open, so it
	// is retried, as editors (and the component's own write-back) do.
	saveByRename := func(text string) {
		temp := filepath.Join(directory, "cordis.json."+text+".tmp")
		writeFile(t, temp, echoFile([2]string{"a", text}))
		var err error
		Eventually(t, func() bool { err = os.Rename(temp, path); return err == nil }, "rename-based save kept failing")
		NoError(t, err)
	}

	saveByRename("v2")
	Eventually(t, func() bool { return h.text("a") == "v2" }, "the first rename-based save was not picked up")

	// the component's own write-back is itself a rename; watching must continue after it
	Within(t, "update", func() { NoError(t, h.fiber("a").Update(EchoOptions{Times: 1, Text: "runtime"}, false)) })
	Eventually(t, func() bool { return strings.Contains(readFile(t, path), `"runtime"`) }, "the runtime change was not written back")

	saveByRename("v3")
	Eventually(t, func() bool { return h.text("a") == "v3" }, "a save after the component's own write was not picked up")
}
