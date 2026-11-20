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
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

const codecTimeout = 5 * time.Second

// codecGoldenDir is the loader's committed testdata, relative to this package.
var codecGoldenDir = filepath.Join("..", "..", "..", "loader", "testdata")

// base.json: a wrong-typed id on line 4 column 9 and an unknown component on line 8.
const codecBaseJSON = `[
{
  "name": "EchoPlugin",
  "id": 7
},
{
  "id": "ghost",
  "name": "NoSuchComponent"
}
]
`

const codecThreeJSON = `[
  { "id": "a:b", "name": "EchoPlugin" },
  { "id": "ok", "name": "EchoPlugin" },
  { "id": "nameless" },
  { "id": "typo", "name": "EchoPlugin", "disabeld": true }
]`

func codecWithin(what string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-time.After(codecTimeout):
		return fmt.Errorf("%s did not return within %s", what, codecTimeout)
	}
}

// codecEvaluator is a fake Evaluator: src/go ships none. Options is deep-cloned like
// any config, so the call log sits behind an unexported pointer the clone shares.
type codecEvaluator struct {
	log *codecCalls
}

type codecCalls struct {
	mu     sync.Mutex
	values map[string]any
	calls  []string
}

func (f *codecEvaluator) Check(string, loader.ScopeKind) error { return nil }

func (f *codecEvaluator) Eval(source string, scope loader.Scope) (any, error) {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	f.log.calls = append(f.log.calls, scope.Kind.String()+":"+source)
	if v, ok := f.log.values[source]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("unknown name in %q", source)
}

func (f *codecEvaluator) count(call string) int {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	n := 0
	for _, c := range f.log.calls {
		if c == call {
			n++
		}
	}
	return n
}

type codecWorld struct {
	root      *cordis.Context
	svc       *loader.Service
	evaluator *codecEvaluator

	file   string
	data   []byte
	issues []loader.Issue

	entry     loader.EntryOptions
	rawConfig string
}

func (w *codecWorld) start(evaluator loader.Evaluator) error {
	root, _ := testkit.Quiet()
	w.root = root
	components := loader.Components{"EchoPlugin": testkit.Echo(&testkit.Log{})}
	opts := loader.Options{Resolver: components}
	if evaluator != nil {
		opts.Evaluator = evaluator
	}
	fiber, err := root.Use(loader.Component, opts)
	if err != nil {
		return err
	}
	var waitErr error
	if err := codecWithin("loader start", func() { waitErr = fiber.Wait() }); err != nil {
		return err
	}
	if waitErr != nil {
		return waitErr
	}
	w.svc, err = loader.Key.Get(root)
	return err
}

func (w *codecWorld) close() error {
	if w.root == nil {
		return nil
	}
	root := w.root
	w.root = nil
	return codecWithin("root dispose", root.Dispose)
}

func (w *codecWorld) issueTexts() string {
	texts := make([]string, len(w.issues))
	for i, issue := range w.issues {
		texts[i] = issue.Error()
	}
	return strings.Join(texts, "\n") + "\n"
}

// ---------------------------------------------------------- steps

func (w *codecWorld) layerBytesWithTypeError(file string, line, column int) error {
	if line != 4 || column != 9 {
		return fmt.Errorf("fixture places the type error at 4:9, not %d:%d", line, column)
	}
	w.file, w.data = file, []byte(codecBaseJSON)
	return w.start(nil)
}

func (w *codecWorld) documentWithThreeInvalidEntries(file string) error {
	w.file, w.data = file, []byte(codecThreeJSON)
	return w.start(nil)
}

func (w *codecWorld) loaderValidates() error {
	doc, err := loader.Parse(loader.JSON, w.file, w.data)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if err := codecWithin("validate", func() { w.issues = w.svc.Validate(doc.Entries) }); err != nil {
		return err
	}
	return nil
}

func (w *codecWorld) firstDiagnosticIs(want string) error {
	if len(w.issues) == 0 {
		return errors.New("no diagnostics returned")
	}
	got := w.issues[0]
	if got.Error() != want {
		return fmt.Errorf("first diagnostic = %q, want %q", got.Error(), want)
	}
	if got.Pos.File != w.file || got.Pos.Line != 4 || got.Pos.Column != 9 {
		return fmt.Errorf("first diagnostic position = %+v, want %s:4:9", got.Pos, w.file)
	}
	return nil
}

func (w *codecWorld) diagnosticsEqualGolden(name string) error {
	path := filepath.Join(codecGoldenDir, name+".golden")
	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	got := w.issueTexts()
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		return fmt.Errorf("diagnostics differ from %s\n--- got\n%s--- want\n%s", path, got, want)
	}
	return nil
}

func (w *codecWorld) nDiagnosticsReturned(n int) error {
	if len(w.issues) != n {
		return fmt.Errorf("got %d diagnostics, want %d:\n%s", len(w.issues), n, w.issueTexts())
	}
	return nil
}

func (w *codecWorld) diagnosticsInOrder(table *godog.Table) error {
	var want []string
	for _, row := range table.Rows {
		want = append(want, strings.TrimSpace(row.Cells[0].Value))
	}
	if got, exp := w.issueTexts(), strings.Join(want, "\n")+"\n"; got != exp {
		return fmt.Errorf("diagnostics out of order or wrong\n--- got\n%s--- want\n%s", got, exp)
	}
	return nil
}

func (w *codecWorld) loaderWithFakeEvaluator(value int, source string) error {
	w.evaluator = &codecEvaluator{log: &codecCalls{values: map[string]any{source: value}}}
	return w.start(w.evaluator)
}

func (w *codecWorld) entryWithConfig(id, config string) error {
	if !json.Valid([]byte(config)) {
		return fmt.Errorf("config is not JSON: %s", config)
	}
	w.rawConfig = config
	w.entry = loader.EntryOptions{ID: id, Name: "EchoPlugin", Config: json.RawMessage(config)}
	return nil
}

func (w *codecWorld) entryIsLoaded() error {
	var applyErr error
	if err := codecWithin("apply", func() { applyErr = w.svc.Apply([]loader.EntryOptions{w.entry}) }); err != nil {
		return err
	}
	return applyErr
}

func (w *codecWorld) fiber(id string) (*cordis.Fiber, error) {
	entry := w.svc.Find(id)
	if entry == nil {
		return nil, fmt.Errorf("entry %q not in the tree", id)
	}
	fiber := w.svc.Fiber(entry)
	if fiber == nil {
		return nil, fmt.Errorf("entry %q has no fiber", id)
	}
	return fiber, nil
}

func (w *codecWorld) entryIsActive(id string) error {
	entry := w.svc.Find(id)
	if entry == nil || !w.svc.Active(entry) {
		return fmt.Errorf("entry %q with an expression did not load", id)
	}
	return nil
}

func (w *codecWorld) effectiveConfigHolds(id string, times int) error {
	fiber, err := w.fiber(id)
	if err != nil {
		return err
	}
	config, ok := fiber.EffectiveConfig()
	if !ok {
		return errors.New("no effective config")
	}
	got, ok := config.(testkit.EchoOptions)
	if !ok {
		return fmt.Errorf("effective config is %T, want EchoOptions", config)
	}
	if got.Times != times {
		return fmt.Errorf("effective times = %d, want %d", got.Times, times)
	}
	return nil
}

func (w *codecWorld) rawConfigHoldsExpression(id string) error {
	fiber, err := w.fiber(id)
	if err != nil {
		return err
	}
	raw, ok := fiber.Config().(json.RawMessage)
	if !ok {
		return fmt.Errorf("fiber config is %T, want json.RawMessage", fiber.Config())
	}
	if string(raw) != w.rawConfig {
		return fmt.Errorf("fiber raw config = %s, want %s", raw, w.rawConfig)
	}
	if tree := string(w.svc.Options(w.svc.Find(id)).Config); tree != w.rawConfig {
		return fmt.Errorf("tree config = %s, want %s", tree, w.rawConfig)
	}
	if !strings.Contains(string(raw), "${{ env.X }}") {
		return fmt.Errorf("raw config lost the expression text: %s", raw)
	}
	return nil
}

func (w *codecWorld) evaluatorCalledOnce(source string) error {
	if n := w.evaluator.count("config:" + source); n != 1 {
		return fmt.Errorf("evaluator called %d times for %q, want 1", n, source)
	}
	return nil
}

func InitializeScenario_loader_layers_and_write_back_codec_seam_and_entry_schema(ctx *godog.ScenarioContext) {
	w := &codecWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if closeErr := w.close(); closeErr != nil && err == nil {
			return c, closeErr
		}
		return c, nil
	})

	ctx.Step(`^in-memory layer bytes named "([^"]+)" with a type error on line (\d+) column (\d+)$`, w.layerBytesWithTypeError)
	ctx.Step(`^an in-memory document "([^"]+)" with three invalid entries$`, w.documentWithThreeInvalidEntries)
	ctx.Step(`^the loader validates the layer$`, w.loaderValidates)
	ctx.Step(`^the first diagnostic is "(.+)"$`, w.firstDiagnosticIs)
	ctx.Step(`^the diagnostic text equals the committed golden "([^"]+)"$`, w.diagnosticsEqualGolden)
	ctx.Step(`^(\d+) diagnostics are returned$`, w.nDiagnosticsReturned)
	ctx.Step(`^the diagnostics are, in document order:$`, w.diagnosticsInOrder)
	ctx.Step(`^a loader with a fake evaluator returning (\d+) for "([^"]+)"$`, w.loaderWithFakeEvaluator)
	ctx.Step(`^an entry "([^"]+)" whose config is (.+)$`, w.entryWithConfig)
	ctx.Step(`^the entry is loaded$`, w.entryIsLoaded)
	ctx.Step(`^the entry "([^"]+)" is active$`, w.entryIsActive)
	ctx.Step(`^the effective config of "([^"]+)" holds times (\d+)$`, w.effectiveConfigHolds)
	ctx.Step(`^the raw config of "([^"]+)" still holds the expression text$`, w.rawConfigHoldsExpression)
	ctx.Step(`^the evaluator was called once for "([^"]+)"$`, w.evaluatorCalledOnce)
}

func TestE2E_loader_layers_and_write_back_codec_seam_and_entry_schema(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_loader_layers_and_write_back_codec_seam_and_entry_schema,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"loader_layers_and_write_back_codec_seam_and_entry_schema.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
