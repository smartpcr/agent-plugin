//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

const layersTimeout = 5 * time.Second

// layersGoldenDir is the loader's committed testdata, relative to this package.
var layersGoldenDir = filepath.Join("..", "..", "..", "loader", "testdata")

const layersBaseFile = `// base layer: shipped defaults
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

const layersUserFile = `// user layer: local edits
[
  // raise b
  { "id": "svc", "merge": { "b": 2 } }, // b was too low
  { "id": "other", "disabled": false }
]
`

func layersWithin(what string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-time.After(layersTimeout):
		return fmt.Errorf("%s did not return within %s", what, layersTimeout)
	}
}

func layersCompact(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	return string(out), err
}

type layersWorld struct {
	// in-memory composition
	layers []loader.Layer
	tree   []loader.EntryOptions
	index  *loader.Index
	issues []loader.Issue

	// file-backed layer set
	dir    string
	root   *cordis.Context
	svc    *loader.Service
	set    *loader.LayerSet
	paths  map[string]string
	before map[string]string
	other  string
	err    error
}

func (w *layersWorld) close() error {
	var err error
	if w.root != nil {
		root := w.root
		w.root = nil
		err = layersWithin("root dispose", root.Dispose)
	}
	if w.dir != "" {
		if rmErr := os.RemoveAll(w.dir); rmErr != nil && err == nil {
			err = rmErr
		}
		w.dir = ""
	}
	return err
}

// ------------------------------------------------- in-memory composition

func (w *layersWorld) parse(label, text string) error {
	doc, err := loader.ParseLayer(loader.JSONC, label+".jsonc", []byte(text))
	if err != nil {
		return fmt.Errorf("parse %s: %w", label, err)
	}
	if len(doc.Issues) != 0 {
		return fmt.Errorf("parse %s: %v", label, doc.Issues)
	}
	w.layers = append(w.layers, loader.Layer{Label: label, Ops: doc.Ops})
	return nil
}

func (w *layersWorld) inMemoryInsertLayer(label, id, config string) error {
	return w.parse(label, fmt.Sprintf(`[ { "insert": [ { "id": %q, "name": "Values", "config": %s } ] } ]`, id, config))
}

func (w *layersWorld) inMemoryMergeLayer(label, patch, id string) error {
	return w.parse(label, fmt.Sprintf(`[ { "id": %q, "merge": %s } ]`, id, patch))
}

func (w *layersWorld) layersComposed() error {
	w.tree, w.index, w.issues = loader.Compose(w.layers)
	return nil
}

func (w *layersWorld) noCompositionIssues() error {
	if len(w.issues) != 0 {
		return fmt.Errorf("composition issues: %v", w.issues)
	}
	return nil
}

func (w *layersWorld) effectiveConfig(id, want string) error {
	for _, entry := range w.tree {
		if entry.ID != id {
			continue
		}
		got, err := layersCompact(entry.Config)
		if err != nil {
			return err
		}
		exp, err := layersCompact([]byte(want))
		if err != nil {
			return err
		}
		if got != exp {
			return fmt.Errorf("effective config of %q = %s, want %s", id, got, exp)
		}
		return nil
	}
	return fmt.Errorf("no entry %q in the composed tree", id)
}

func (w *layersWorld) indexOwner(field, id, label string) error {
	got, ok := w.index.Owner(id, field)
	if !ok {
		return fmt.Errorf("no owner for %s %s", id, field)
	}
	if got != label {
		return fmt.Errorf("owner of %s %s = %q, want %q", id, field, got, label)
	}
	return nil
}

// ------------------------------------------------------ file-backed layer set

func (w *layersWorld) layerFiles(writable string) error {
	if writable != "user" {
		return fmt.Errorf("fixture makes the user layer writable, not %q", writable)
	}
	dir, err := os.MkdirTemp("", "dsh-layers-*")
	if err != nil {
		return err
	}
	w.dir = dir
	w.paths, w.before = map[string]string{}, map[string]string{}
	var layers []loader.Layer
	for _, f := range []struct{ label, text string }{{"base", layersBaseFile}, {"user", layersUserFile}} {
		path := filepath.Join(dir, f.label+".jsonc")
		if err := os.WriteFile(path, []byte(f.text), 0o644); err != nil {
			return err
		}
		w.paths[f.label], w.before[f.label] = path, f.text
		layers = append(layers, loader.Layer{Label: f.label, Path: path, Writable: f.label == writable})
	}

	root, _ := testkit.Quiet()
	w.root = root
	component := cordis.Define("Values", func(*cordis.Context, map[string]int) error { return nil })
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: loader.Components{"Values": component}})
	if err != nil {
		return err
	}
	if err := w.wait("loader start", fiber); err != nil {
		return err
	}
	if w.svc, err = loader.Key.Get(root); err != nil {
		return err
	}
	setFiber, err := root.Use(loader.LayerSetComponent, loader.LayerSetOptions{Layers: layers})
	if err != nil {
		return err
	}
	if err := w.wait("layer set start", setFiber); err != nil {
		return err
	}
	set, ok := setFiber.Instance().(*loader.LayerSet)
	if !ok {
		return fmt.Errorf("layer set instance is %T", setFiber.Instance())
	}
	w.set = set
	return nil
}

func (w *layersWorld) wait(what string, fiber *cordis.Fiber) error {
	var waitErr error
	if err := layersWithin(what, func() { waitErr = fiber.Wait() }); err != nil {
		return err
	}
	return waitErr
}

func (w *layersWorld) runningConfig(id, want string) error {
	w.tree = w.svc.Snapshot()
	return w.effectiveConfig(id, want)
}

func (w *layersWorld) otherWriterModifies(label string) error {
	w.other = strings.Replace(w.before[label], `"disabled": false`, `"disabled": true`, 1)
	if w.other == w.before[label] {
		return errors.New("fixture edit did not change the file")
	}
	return os.WriteFile(w.paths[label], []byte(w.other), 0o644)
}

func (w *layersWorld) setAndWriteBack(field, id string, value int) error {
	entry := w.svc.Find(id)
	if entry == nil {
		return fmt.Errorf("no entry %q", id)
	}
	var current map[string]int
	if err := json.Unmarshal(w.svc.Options(entry).Config, &current); err != nil {
		return fmt.Errorf("decode %s config: %w", id, err)
	}
	current[field] = value
	var updateErr error
	if err := layersWithin("update", func() { updateErr = w.svc.Fiber(entry).Update(current, false) }); err != nil {
		return err
	}
	if updateErr != nil {
		return fmt.Errorf("update: %w", updateErr)
	}
	return layersWithin("flush", func() { w.err = w.set.Flush() })
}

func (w *layersWorld) writeBackSucceeds() error {
	if w.err != nil {
		return fmt.Errorf("write-back failed: %w", w.err)
	}
	return nil
}

func (w *layersWorld) read(label string) (string, error) {
	data, err := os.ReadFile(w.paths[label])
	return string(data), err
}

func (w *layersWorld) fileEqualsGolden(label, name string) error {
	got, err := w.read(label)
	if err != nil {
		return err
	}
	path := filepath.Join(layersGoldenDir, name+".golden")
	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		return fmt.Errorf("%s layer differs from %s\n--- got\n%s--- want\n%s", label, path, got, want)
	}
	return nil
}

func (w *layersWorld) fileKeepsComments(label string) error {
	got, err := w.read(label)
	if err != nil {
		return err
	}
	for _, comment := range []string{"// user layer: local edits", "// raise b", "// b was too low"} {
		if !strings.Contains(got, comment) {
			return fmt.Errorf("comment %q lost:\n%s", comment, got)
		}
	}
	return nil
}

func (w *layersWorld) fileUnchanged(label string) error {
	got, err := w.read(label)
	if err != nil {
		return err
	}
	if got != w.before[label] {
		return fmt.Errorf("the %s layer was rewritten:\n%s", label, got)
	}
	return nil
}

func (w *layersWorld) staleError(label string) error {
	if !errors.Is(w.err, loader.ErrStaleLayer) {
		return fmt.Errorf("a stale write was not refused: %v", w.err)
	}
	var stale *loader.StaleLayerError
	if !errors.As(w.err, &stale) || stale.Label != label {
		return fmt.Errorf("not a StaleLayerError for %s: %v", label, w.err)
	}
	return nil
}

func (w *layersWorld) holdsOtherWriter(label string) error {
	got, err := w.read(label)
	if err != nil {
		return err
	}
	if got != w.other {
		return fmt.Errorf("the other writer's content was lost:\n%s", got)
	}
	return nil
}

func InitializeScenario_loader_layers_and_write_back_layer_composition_and_write_back(ctx *godog.ScenarioContext) {
	w := &layersWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if closeErr := w.close(); closeErr != nil && err == nil {
			return c, closeErr
		}
		return c, nil
	})

	ctx.Step(`^an in-memory layer "([^"]+)" inserting entry "([^"]+)" with config (\{.*\})$`, w.inMemoryInsertLayer)
	ctx.Step(`^an in-memory layer "([^"]+)" merging (\{.*\}) into entry "([^"]+)"$`, w.inMemoryMergeLayer)
	ctx.Step(`^the layers are composed$`, w.layersComposed)
	ctx.Step(`^there are no composition issues$`, w.noCompositionIssues)
	ctx.Step(`^the effective config of entry "([^"]+)" is (\{.*\})$`, w.effectiveConfig)
	ctx.Step(`^the index reports field "([^"]+)" of "([^"]+)" from layer "([^"]+)"$`, w.indexOwner)

	ctx.Step(`^base and user layer files in a temporary directory with "([^"]+)" writable$`, w.layerFiles)
	ctx.Step(`^the running config of entry "([^"]+)" is (\{.*\})$`, w.runningConfig)
	ctx.Step(`^another writer modifies the "([^"]+)" layer file on disk after the layer set read it$`, w.otherWriterModifies)
	ctx.Step(`^"([^"]+)" of entry "([^"]+)" is set to (\d+) and the layer set writes back$`, w.setAndWriteBack)
	ctx.Step(`^the write-back succeeds$`, w.writeBackSucceeds)
	ctx.Step(`^the "([^"]+)" layer file equals the committed golden "([^"]+)"$`, w.fileEqualsGolden)
	ctx.Step(`^the "([^"]+)" layer file still holds its comments$`, w.fileKeepsComments)
	ctx.Step(`^the "([^"]+)" layer file is byte-identical to before$`, w.fileUnchanged)
	ctx.Step(`^the write fails with a stale-source error for layer "([^"]+)"$`, w.staleError)
	ctx.Step(`^the "([^"]+)" layer file holds the other writer's content$`, w.holdsOtherWriter)
}

func TestE2E_loader_layers_and_write_back_layer_composition_and_write_back(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_loader_layers_and_write_back_layer_composition_and_write_back,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"loader_layers_and_write_back_layer_composition_and_write_back.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
