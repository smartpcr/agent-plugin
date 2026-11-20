package loader_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// ------------------------------------------------------------ composition

func raw(text string) json.RawMessage { return json.RawMessage(text) }

func values(id, config string) loader.EntryOptions {
	return loader.EntryOptions{ID: id, Name: "Values", Config: raw(config)}
}

func owner(t *testing.T, index *loader.Index, id, field string) string {
	t.Helper()
	label, ok := index.Owner(id, field)
	True(t, ok, "no owner for "+id+" "+field)
	return label
}

func config(t *testing.T, tree []loader.EntryOptions, id string) string {
	t.Helper()
	for _, entry := range tree {
		if entry.ID == id {
			var compact strings.Builder
			var v any
			NoError(t, json.Unmarshal(entry.Config, &v))
			encoded, _ := json.Marshal(v)
			compact.Write(encoded)
			return compact.String()
		}
	}
	t.Fatalf("no entry %q in the composed tree", id)
	return ""
}

// DH-T-LOADER-7 (TC-03a)
func TestComposeOwnershipIndexNamesTheSupplyingLayer(t *testing.T) {
	layers := []loader.Layer{
		{Label: "base", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{values("svc", `{"a":1,"b":1}`)}}}},
		{Label: "user", Ops: []loader.Op{{Kind: loader.OpMerge, ID: "svc", Patch: raw(`{"b":2}`)}}},
	}
	before, _ := json.Marshal(layers[0].Ops)

	tree, index, issues := loader.Compose(layers)
	Equal(t, 0, len(issues))
	Equal(t, `{"a":1,"b":2}`, config(t, tree, "svc"))
	Equal(t, "base", owner(t, index, "svc", "config.a"))
	Equal(t, "user", owner(t, index, "svc", "config.b"))
	Equal(t, "base", owner(t, index, "svc", ""))

	after, _ := json.Marshal(layers[0].Ops)
	Equal(t, string(before), string(after), "Compose modified a layer")
}

func TestComposeLaterLayersWinAndUnmatchedOpsAreReported(t *testing.T) {
	layers := []loader.Layer{
		{Label: "bundle", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{
			values("svc", `{"a":1,"nested":{"x":1,"y":1}}`), values("gone", `{}`), group("g"),
		}}}},
		{Label: "profile", Ops: []loader.Op{
			{Kind: loader.OpInsert, ID: "g", Entries: []loader.EntryOptions{values("child", `{"c":1}`)}},
			{Kind: loader.OpMerge, ID: "svc", Patch: raw(`{"nested":{"y":2}}`)},
		}},
		{Label: "user", Ops: []loader.Op{
			{Kind: loader.OpOverride, ID: "g:child", Set: map[string]json.RawMessage{"disabled": raw("true")}},
			{Kind: loader.OpMerge, ID: "svc", Patch: raw(`{"nested":{"x":null}}`)},
			{Kind: loader.OpRemove, ID: "gone"},
			{Kind: loader.OpMerge, ID: "nobody", Patch: raw(`{}`)},
		}},
		{Label: "project", Ops: []loader.Op{
			{Kind: loader.OpOverride, ID: "svc", Set: map[string]json.RawMessage{"config": raw(`{"a":9}`)}},
		}},
	}
	tree, index, issues := loader.Compose(layers)

	Equal(t, 1, len(issues))
	True(t, strings.Contains(issues[0].Error(), `"nobody" matches no entry`) && strings.Contains(issues[0].Error(), "layer user"), issues[0].Error())
	Equal(t, 2, len(tree))
	Equal(t, `{"a":9}`, config(t, tree, "svc"))
	Equal(t, "project", owner(t, index, "svc", "config.a"))
	Equal(t, "project", owner(t, index, "svc", "config.nested"))
	True(t, tree[1].Children[0].Disabled, "override did not disable the child")
	Equal(t, "user", owner(t, index, "g:child", "disabled"))
	Equal(t, "profile", owner(t, index, "g:child", "config.c"))
	_, ok := index.Owner("gone", "")
	True(t, !ok, "a removed entry kept its owner")
}

func TestParseLayerReadsOpsWithComments(t *testing.T) {
	doc, err := loader.ParseLayer(loader.JSONC, "user.jsonc", []byte(userLayer))
	NoError(t, err)
	Equal(t, 0, len(doc.Issues))
	Equal(t, 2, len(doc.Ops))
	Equal(t, loader.OpMerge, doc.Ops[0].Kind)
	Equal(t, "svc", doc.Ops[0].ID)
	Equal(t, loader.OpOverride, doc.Ops[1].Kind)
}

func TestJSONCRejectsAnUnterminatedBlockComment(t *testing.T) {
	for _, text := range []string{"[] /* unfinished", "[ /* open\n 1 ]", "/* x"} {
		_, err := loader.JSONC.Decode("f.jsonc", []byte(text))
		var parse *loader.ParseError
		True(t, errors.As(err, &parse) && strings.Contains(err.Error(), "unterminated block comment"), text+": "+errText(err))
		Equal(t, strings.Index(text, "/*"), parse.Issues[0].Pos.Offset, text)
	}
	_, err := loader.JSONC.Decode("f.jsonc", []byte("[] /* done */"))
	NoError(t, err)
}

func TestEncodeLayerKeepsACommentTrailingTheLastKeptOp(t *testing.T) {
	doc, err := loader.ParseLayer(loader.JSONC, "user.jsonc", []byte(userLayer))
	NoError(t, err)
	codec := loader.JSONC.(loader.LayerCodec)

	dropped, err := codec.EncodeLayer(doc, doc.Ops[:1])
	NoError(t, err)
	Equal(t, "// user layer: local edits\n[\n  // raise b\n  { \"id\": \"svc\", \"merge\": { \"b\": 2 } } // b was too low\n]\n", string(dropped))

	replaced, err := codec.EncodeLayer(doc, []loader.Op{doc.Ops[0], {Kind: loader.OpRemove, ID: "other"}})
	NoError(t, err)
	Equal(t, "// user layer: local edits\n[\n  // raise b\n  { \"id\": \"svc\", \"merge\": { \"b\": 2 } }, // b was too low\n  {\n    \"id\": \"other\",\n    \"remove\": true\n  }\n]\n", string(replaced))
}

// --------------------------------------------------------------- layer set

const baseLayer = `// base layer: shipped defaults
[
  {
    "insert": [
      // the service under test
      { "id": "svc", "name": "Values", "config": { "a": 1, "b": 1 } },
      { "id": "other", "name": "Values", "config": { "a": 0 } }
    ]
  }
]
`

const userLayer = `// user layer: local edits
[
  // raise b
  { "id": "svc", "merge": { "b": 2 } }, // b was too low
  { "id": "other", "disabled": false }
]
`

type layerHarness struct {
	root   *cordis.Context
	loader *loader.Service
	logs   *cordis.MemoryExporter
	base   string
	user   string
	paths  map[string]string
	mu     sync.Mutex
	loads  map[string]int
}

// loadCount counts the loads of an entry: "svc" is the one whose config has "b".
func (h *layerHarness) loadCount(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loads[id]
}

func (h *layerHarness) fiber(t *testing.T, id string) *cordis.Fiber {
	t.Helper()
	entry := h.loader.Find(id)
	True(t, entry != nil, "no entry "+id)
	return h.loader.Fiber(entry)
}

func (h *layerHarness) update(t *testing.T, id string, config map[string]int) {
	t.Helper()
	Within(t, "update", func() { NoError(t, h.fiber(t, id).Update(config, false)) })
}

// startLayers writes base and user layer files into a temporary directory and loads
// a layer set over them, with the user layer writable.
func startLayers(t *testing.T, guard loader.WriteGuard) (*layerHarness, *loader.LayerSet) {
	t.Helper()
	return startSet(t, guard, layerFile{"base", baseLayer, false}, layerFile{"user", userLayer, true})
}

type layerFile struct {
	label    string
	text     string
	writable bool
}

// startSet writes each layer to <label>.jsonc in a temporary directory and loads a
// layer set over them, in order.
func startSet(t *testing.T, guard loader.WriteGuard, files ...layerFile) (*layerHarness, *loader.LayerSet) {
	t.Helper()
	directory := t.TempDir()
	root, logs := Quiet()
	h := &layerHarness{root: root, logs: logs, loads: map[string]int{}, paths: map[string]string{}}
	var layers []loader.Layer
	for _, file := range files {
		path := filepath.Join(directory, file.label+".jsonc")
		h.paths[file.label] = path
		writeFile(t, path, file.text)
		layers = append(layers, loader.Layer{Label: file.label, Path: path, Writable: file.writable})
	}
	h.base, h.user = h.paths["base"], h.paths["user"]
	component := cordis.Define("Values", func(ctx *cordis.Context, config map[string]int) error {
		id := "other"
		if _, ok := config["b"]; ok {
			id = "svc"
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.loads[id]++
		return nil
	})
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: loader.Components{"Values": component}})))
	h.loader = Must(loader.Key.Get(root))
	fiber := MustWait(t, Must(root.Use(loader.LayerSetComponent, loader.LayerSetOptions{
		Layers:     layers,
		WriteGuard: guard,
	})))
	return h, fiber.Instance().(*loader.LayerSet)
}

func (h *layerHarness) svc(t *testing.T) string {
	t.Helper()
	return config(t, h.loader.Snapshot(), "svc")
}

// DH-T-LOADER-8 (TC-03b)
func TestLayerSetWriteBackTouchesOneLayerFileOnly(t *testing.T) {
	h, set := startLayers(t, nil)
	Equal(t, `{"a":1,"b":2}`, h.svc(t))
	Equal(t, "user", owner(t, set.Index(), "svc", "config.b"))

	h.update(t, "svc", map[string]int{"a": 1, "b": 3})
	Within(t, "flush", func() { NoError(t, set.Flush()) })

	assertGolden(t, "writeback_user", readFile(t, h.user))
	Equal(t, baseLayer, readFile(t, h.base), "the base layer was rewritten")

	// a second flush with nothing new leaves the file alone
	Within(t, "flush", func() { NoError(t, set.Flush()) })
	assertGolden(t, "writeback_user", readFile(t, h.user))
}

func TestLayerSetARuntimeEditOfALowerValueBecomesAWritableOp(t *testing.T) {
	h, set := startLayers(t, nil)
	h.update(t, "other", map[string]int{"a": 7})
	Within(t, "flush", func() { NoError(t, set.Flush()) })
	text := readFile(t, h.user)
	True(t, strings.Contains(text, `"id": "other"`) && strings.Contains(text, `"a": 7`), "edit not written:\n"+text)
	True(t, strings.Contains(text, "// b was too low"), "comments were lost:\n"+text)
	Equal(t, baseLayer, readFile(t, h.base))

	// the written layer composes back into the same tree
	tree, _, issues := loader.Compose(set.Layers())
	Equal(t, 0, len(issues))
	Equal(t, `{"a":7}`, config(t, tree, "other"))
	Equal(t, `{"a":1,"b":2}`, config(t, tree, "svc"))

	// the ownership index follows the write, and an unchanged reload keeps it
	Equal(t, "user", owner(t, set.Index(), "other", "config.a"))
	Equal(t, "base", owner(t, set.Index(), "svc", "config.a"))
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Equal(t, "user", owner(t, set.Index(), "other", "config.a"))
}

const bundleLayer = `[ { "insert": [ { "id": "svc", "name": "Values", "config": { "a": 1, "b": 1 } } ] } ]
`
const profileLayer = `[ { "id": "svc", "merge": { "a": 2 } } ]
`
const projectLayer = `// project pins b
[ { "id": "svc", "merge": { "b": 2 } } ]
`

// a layer after the writable one masks an edit: saving it would compose back into
// the masked value, so the write is refused rather than reported as saved
func TestLayerSetAnEditMaskedByALaterLayerIsRefused(t *testing.T) {
	h, set := startSet(t, nil,
		layerFile{"bundle", bundleLayer, false},
		layerFile{"profile", profileLayer, false},
		layerFile{"user", "[]\n", true},
		layerFile{"project", projectLayer, false},
	)
	Equal(t, `{"a":2,"b":2}`, h.svc(t))
	Equal(t, "profile", owner(t, set.Index(), "svc", "config.a"))
	Equal(t, "project", owner(t, set.Index(), "svc", "config.b"))

	// a is not masked: it is saved to user and composes back
	h.update(t, "svc", map[string]int{"a": 5, "b": 2})
	Within(t, "flush", func() { NoError(t, set.Flush()) })
	saved := readFile(t, h.paths["user"])
	True(t, strings.Contains(saved, `"a": 5`) || strings.Contains(saved, `"a":5`), "edit not saved:\n"+saved)
	Equal(t, "user", owner(t, set.Index(), "svc", "config.a"))

	// b is masked by project: nothing is written and the caller is told
	h.update(t, "svc", map[string]int{"a": 5, "b": 3})
	var err error
	Within(t, "flush", func() { err = set.Flush() })
	True(t, errors.Is(err, loader.ErrMaskedEdit), "a masked edit was reported as saved: "+errText(err))
	var masked *loader.MaskedEditError
	True(t, errors.As(err, &masked) && len(masked.IDs) == 1 && masked.IDs[0] == "svc", "masked ids: "+errText(err))
	Equal(t, saved, readFile(t, h.paths["user"]), "a masked edit was written")
	Equal(t, `{"a":5,"b":3}`, h.svc(t), "the runtime edit was dropped")
	for label, text := range map[string]string{"bundle": bundleLayer, "profile": profileLayer, "project": projectLayer} {
		Equal(t, text, readFile(t, h.paths[label]), label+" was rewritten")
	}

	// what the files hold composes into the saved edit, not the masked one
	tree, _, _ := loader.Compose(set.Layers())
	Equal(t, `{"a":5,"b":2}`, config(t, tree, "svc"))
}

// the masked value is already in the writable file, so the write would produce its
// exact bytes: it is still refused, not reported as saved
func TestLayerSetAMaskedEditTheWritableFileAlreadyHoldsIsRefused(t *testing.T) {
	user := "// user wants b=3\n[ { \"id\": \"svc\", \"merge\": { \"b\": 3 } } ]\n"
	h, set := startSet(t, nil,
		layerFile{"bundle", bundleLayer, false},
		layerFile{"user", user, true},
		layerFile{"project", projectLayer, false},
	)
	Equal(t, `{"a":1,"b":2}`, h.svc(t))

	h.update(t, "svc", map[string]int{"a": 1, "b": 3})
	var err error
	Within(t, "flush", func() { err = set.Flush() })
	True(t, errors.Is(err, loader.ErrMaskedEdit), "a masked edit was reported as saved: "+errText(err))
	Equal(t, `{"a":1,"b":3}`, h.svc(t), "the unsaved runtime edit was dropped")
	for label, text := range map[string]string{"bundle": bundleLayer, "user": user, "project": projectLayer} {
		Equal(t, text, readFile(t, h.paths[label]), label+" was rewritten")
	}
	// still unsaved on the next attempt
	Within(t, "flush", func() { err = set.Flush() })
	True(t, errors.Is(err, loader.ErrMaskedEdit), "the second flush reported the edit saved: "+errText(err))
}

// a file that was read and then disappears keeps its last operations: the running
// tree stays, and writing to a vanished writable layer is refused
func TestLayerSetAVanishedLayerKeepsTheRunningTree(t *testing.T) {
	h, set := startLayers(t, nil)
	NoError(t, os.Remove(h.base))
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Equal(t, `{"a":1,"b":2}`, h.svc(t), "entries of a vanished layer were dropped")
	True(t, h.loader.Find("other") != nil, "entries of a vanished layer were dropped")
	Equal(t, 1, h.loadCount("svc"))
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Equal(t, 1, Count(h.logs, cordis.LevelWarn), "a vanished layer was not reported exactly once")

	// it comes back, changed, and is read again
	writeFile(t, h.base, strings.Replace(baseLayer, `"a": 1, "b": 1`, `"a": 4, "b": 1`, 1))
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Equal(t, `{"a":4,"b":2}`, h.svc(t))

	NoError(t, os.Remove(h.user))
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Equal(t, `{"a":4,"b":2}`, h.svc(t), "the user layer's values were dropped")
	h.update(t, "svc", map[string]int{"a": 4, "b": 3})
	var err error
	Within(t, "flush", func() { err = set.Flush() })
	True(t, errors.Is(err, loader.ErrStaleLayer), "a write to a vanished layer was not refused: "+errText(err))
	_, statErr := os.Stat(h.user)
	True(t, os.IsNotExist(statErr), "a vanished layer was recreated")
}

func TestLayerSetAReadOnlyLayerFailsTheWriteUntilItIsWritable(t *testing.T) {
	h, set := startLayers(t, nil)
	NoError(t, os.Chmod(h.user, 0o444))
	t.Cleanup(func() { _ = os.Chmod(h.user, 0o644) })

	h.update(t, "svc", map[string]int{"a": 1, "b": 3})
	var err error
	Within(t, "flush", func() { err = set.Flush() })
	True(t, err != nil && strings.Contains(err.Error(), "read-only"), "a read-only layer was written: "+errText(err))
	Equal(t, userLayer, readFile(t, h.user))
	Equal(t, `{"a":1,"b":3}`, h.svc(t), "the runtime edit was dropped")

	NoError(t, os.Chmod(h.user, 0o644))
	Within(t, "flush", func() { NoError(t, set.Flush()) })
	assertGolden(t, "writeback_user", readFile(t, h.user))
}

// DH-T-LOADER-15 (TC-03b)
func TestLayerSetWriteGuardRefusesAStaleWrite(t *testing.T) {
	h, set := startLayers(t, nil)
	edited := strings.Replace(userLayer, `"disabled": false`, `"disabled": true`, 1)
	writeFile(t, h.user, edited) // another writer, before the layer set reads again

	h.update(t, "svc", map[string]int{"a": 1, "b": 3})
	var err error
	Within(t, "flush", func() { err = set.Flush() })
	True(t, errors.Is(err, loader.ErrStaleLayer), "a stale write was not refused: "+errText(err))
	var stale *loader.StaleLayerError
	True(t, errors.As(err, &stale) && stale.Label == "user", "not a StaleLayerError for user: "+errText(err))
	Equal(t, edited, readFile(t, h.user), "the other writer's content was lost")

	// reading again merges both edits; the next write keeps them both
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Within(t, "flush", func() { NoError(t, set.Flush()) })
	text := readFile(t, h.user)
	True(t, strings.Contains(text, `"disabled": true`), "the file edit was lost:\n"+text)
	True(t, strings.Contains(text, `"b": 3`), "the runtime edit was lost:\n"+text)
	True(t, h.loader.Options(h.loader.Find("other")).Disabled, "the file edit was not applied")
	Equal(t, baseLayer, readFile(t, h.base))
}

func TestLayerSetWriteGuardWrapsEveryWrite(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	fail := true
	guard := func(path string, write func() error) error {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, path)
		if fail {
			return errors.New("lock held elsewhere")
		}
		return write()
	}
	h, set := startLayers(t, guard)
	h.update(t, "svc", map[string]int{"a": 1, "b": 3})

	var err error
	Within(t, "flush", func() { err = set.Flush() })
	True(t, err != nil && strings.Contains(err.Error(), "lock held elsewhere"), "guard error not returned: "+errText(err))
	Equal(t, userLayer, readFile(t, h.user))

	mu.Lock()
	fail = false
	mu.Unlock()
	Within(t, "flush", func() { NoError(t, set.Flush()) })
	assertGolden(t, "writeback_user", readFile(t, h.user))
	mu.Lock()
	defer mu.Unlock()
	for _, path := range paths {
		Equal(t, filepath.Base(h.user), filepath.Base(path), "the guard saw a write to another layer")
	}
}

func TestLayerSetAGuardThatPanicsOrSkipsTheWriteFailsIt(t *testing.T) {
	for name, guard := range map[string]loader.WriteGuard{
		"panics": func(string, func() error) error { panic("boom") },
		"skips":  func(string, func() error) error { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			h, set := startLayers(t, guard)
			h.update(t, "svc", map[string]int{"a": 1, "b": 3})
			var err error
			Within(t, "flush", func() { err = set.Flush() })
			True(t, err != nil, "the write was reported as done")
			Equal(t, userLayer, readFile(t, h.user))
		})
	}
}

// DH-T-LOADER-14
func TestLayerSetEditingALayerRestartsOnlyTheChangedEntry(t *testing.T) {
	h, set := startLayers(t, nil)
	Equal(t, 1, h.loadCount("svc"))
	Equal(t, 1, h.loadCount("other"))

	writeFile(t, h.user, strings.Replace(userLayer, `"b": 2`, `"b": 5`, 1))
	Within(t, "reload", func() { NoError(t, set.Reload()) })
	Equal(t, `{"a":1,"b":5}`, h.svc(t))
	Equal(t, 2, h.loadCount("svc"))
	Equal(t, 1, h.loadCount("other"), "an unchanged entry restarted")
	Equal(t, baseLayer, readFile(t, h.base))
}

func TestLayerSetOptionsAllowOneWritableLayer(t *testing.T) {
	err := loader.LayerSetOptions{Layers: []loader.Layer{
		{Label: "a", Path: "a.json", Writable: true},
		{Label: "b", Path: "b.json", Writable: true},
	}}.Validate()
	True(t, err != nil, "two writable layers were accepted")
	err = loader.LayerSetOptions{Layers: []loader.Layer{{Label: "a"}, {Label: "a"}}}.Validate()
	True(t, err != nil, "duplicate labels were accepted")
}
