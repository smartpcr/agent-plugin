package loader_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// fakeEvaluator is a stub Evaluator: src/go ships none (NG-5).
// Options is deep-cloned like any config, so the fake keeps its call log behind an
// unexported pointer, which the clone shares.
type fakeEvaluator struct {
	log  *callLog
	eval func(source string, scope loader.Scope) (any, error)
}

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func newEvaluator(eval func(string, loader.Scope) (any, error)) *fakeEvaluator {
	return &fakeEvaluator{log: &callLog{}, eval: eval}
}

func (f *fakeEvaluator) Check(source string, _ loader.ScopeKind) error {
	if strings.Contains(source, "!!") {
		return errors.New("syntax error")
	}
	return nil
}

func (f *fakeEvaluator) Eval(source string, scope loader.Scope) (any, error) {
	f.log.mu.Lock()
	f.log.calls = append(f.log.calls, scope.Kind.String()+":"+source)
	f.log.mu.Unlock()
	return f.eval(source, scope)
}

func (f *fakeEvaluator) count(call string) int {
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

func startWith(t *testing.T, evaluator loader.Evaluator) *harness {
	t.Helper()
	root, logs := Quiet()
	echo := &Log{}
	components := loader.Components{"EchoPlugin": Echo(echo), "CounterService": Counter}
	MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: components, Evaluator: evaluator})))
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	return &harness{root: root, loader: Must(loader.Key.Get(root)), logs: logs, echo: echo}
}

// ---------------------------------------------------------- DH-T-LOADER-4

func TestTheExpressionSeamCallsTheInjectedEvaluator(t *testing.T) {
	evaluator := newEvaluator(func(source string, _ loader.Scope) (any, error) {
		if source == "env.X" {
			return 7, nil
		}
		return nil, fmt.Errorf("unknown name in %q", source)
	})
	h := startWith(t, evaluator)
	raw := `{"times":"${{ env.X }}","text":"x"}`
	h.apply(t, loader.EntryOptions{ID: "e", Name: "EchoPlugin", Config: json.RawMessage(raw)})

	True(t, h.active("e"), "entry with an expression did not load")
	config, ok := h.fiber("e").EffectiveConfig()
	True(t, ok, "no effective config")
	Equal(t, any(EchoOptions{Times: 7, Text: "x"}), config)
	Equal(t, raw, string(h.fiber("e").Config().(json.RawMessage)), "the raw config keeps the expression")
	Equal(t, raw, string(h.loader.Options(h.loader.Find("e")).Config), "the tree keeps the expression")
	Equal(t, 1, evaluator.count("config:env.X"))
}

//dsh:test DH-T-LOADER-4
func TestASavedFileKeepsTheExpressionSource(t *testing.T) {
	// a disabled expression that fails keeps its entry unloaded, and its source is
	// still what the file holds after a write-back (DH-T-LOADER-4)
	evaluator := newEvaluator(func(_ string, scope loader.Scope) (any, error) {
		if scope.Kind == loader.DisabledScope {
			return nil, errors.New("env.OFF is unset")
		}
		return 3, nil
	})
	path := filepath.Join(t.TempDir(), "cordis.json")
	writeFile(t, path, `[
  { "id": "e", "name": "EchoPlugin", "config": { "times": { "$expr": "env.N" }, "text": "e" } },
  { "id": "d", "name": "EchoPlugin", "disabled": { "$expr": "env.OFF" }, "config": { "times": 1, "text": "d" } },
  { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
]`)
	h := startWith(t, evaluator)
	useFile(t, h, loader.ConfigFileOptions{Path: path})
	True(t, h.echo.Contains("+e e e"), "expression was not evaluated: "+h.echo.String())
	True(t, h.fiber("d") == nil && !h.echo.Contains("+d"), "an entry whose disabled expression failed was loaded")

	Within(t, "update", func() { NoError(t, h.fiber("b").Update(EchoOptions{Times: 2, Text: "b"}, false)) })
	Eventually(t, func() bool { return strings.Contains(readFile(t, path), `"times": 2`) }, "config was not written back")
	saved := readFile(t, path)
	True(t, strings.Contains(saved, `"$expr": "env.N"`), "the expression was not kept:\n"+saved)
	True(t, strings.Contains(saved, `"$expr": "env.OFF"`), "the failed disabled expression was not kept:\n"+saved)
	True(t, !strings.Contains(saved, `"disabled": true`), "a failed disabled expression was saved as disabled:\n"+saved)
}

//dsh:test DH-T-LOADER-4
func TestADisabledExpressionIsEvaluatedAtEachApply(t *testing.T) {
	var disabled atomic.Bool
	var fail atomic.Bool
	evaluator := newEvaluator(func(string, loader.Scope) (any, error) {
		if fail.Load() {
			return nil, errors.New("launch.profile is unset")
		}
		return disabled.Load(), nil
	})
	h := startWith(t, evaluator)
	entry := echo("e", "e")
	entry.DisabledExpr = `launch.profile != "web"`
	call := "disabled:" + entry.DisabledExpr

	h.apply(t, entry)
	True(t, h.active("e"), "enabled by its expression, the entry did not load")
	Equal(t, 1, evaluator.count(call))

	disabled.Store(true)
	h.apply(t, entry)
	True(t, h.fiber("e") == nil, "disabled by its expression, the entry kept its fiber")
	Equal(t, 2, evaluator.count(call))

	disabled.Store(false)
	fail.Store(true)
	h.apply(t, entry)
	True(t, h.fiber("e") == nil, "an entry whose expression failed was loaded")
	Equal(t, `launch.profile != "web"`, h.loader.Options(h.loader.Find("e")).DisabledExpr, "a failure must not turn into disabled")
	errorsText := strings.Join(Texts(h.logs, cordis.LevelError), "\n")
	True(t, strings.Contains(errorsText, "cannot evaluate disabled: launch.profile is unset"), errorsText)

	fail.Store(false)
	h.apply(t, entry)
	True(t, h.active("e"), "the entry did not come back once its expression evaluated")
}

func TestAnExpressionWithoutAnEvaluatorIsReportedNotLoaded(t *testing.T) {
	h := startWith(t, nil)
	doc := Must(loader.Parse(loader.JSON, "f.json", []byte(`[
  { "id": "e", "name": "EchoPlugin", "config": { "times": "${{ env.X }}" } },
  { "id": "ok", "name": "EchoPlugin" }
]`)))
	assertTexts(t, []string{`f.json:2:59: [0].config.times: expression "env.X": ` + loader.ErrNoEvaluator.Error()}, h.loader.Validate(doc.Entries))
	h.apply(t, doc.Entries...)
	True(t, h.loader.Find("e") == nil && h.active("ok"), "the entry with an expression was loaded without an evaluator")
}

func TestAnExpressionTheEvaluatorRejectsIsReportedAtItsPosition(t *testing.T) {
	h := startWith(t, newEvaluator(func(string, loader.Scope) (any, error) { return true, nil }))
	doc := Must(loader.Parse(loader.JSON, "f.json", []byte(`[
  { "id": "e", "name": "EchoPlugin", "disabled": { "$expr": "!!x" } }
]`)))
	assertTexts(t, []string{`f.json:2:50: [0].disabled: expression "!!x": syntax error`}, h.loader.Validate(doc.Entries))
}

// ---------------------------------------------------------- DH-T-LOADER-5

//dsh:test DH-T-LOADER-5
func TestAConfigExpressionReadingUseRunsOnlyOnceItsServiceIsActive(t *testing.T) {
	var unavailable atomic.Int32
	var services sync.Map
	evaluator := newEvaluator(func(_ string, scope loader.Scope) (any, error) {
		counter, err := CounterKey.Get(scope.Context)
		if err != nil {
			unavailable.Add(1)
			return nil, err
		}
		services.Store(counter, true)
		return 1, nil
	})
	h := startWith(t, evaluator)
	h.apply(t, loader.EntryOptions{
		ID: "e", Name: "EchoPlugin", Inject: []string{"counter"},
		Config: json.RawMessage(`{"times":{"$expr":"use.counter.n"},"text":"u"}`),
	})
	call := "config:use.counter.n"
	Equal(t, 0, evaluator.count(call), "evaluated before its service was active")
	True(t, !h.active("e"), "loaded without its service")

	first := Must(h.root.Use(Counter, nil))
	MustWait(t, first)
	Eventually(t, func() bool { return h.active("e") }, "entry did not load with its service")
	Equal(t, 1, evaluator.count(call))

	Within(t, "replace", first.Dispose)
	MustWait(t, Must(h.root.Use(Counter, nil)))
	Eventually(t, func() bool { return evaluator.count(call) == 2 && h.active("e") }, "not evaluated again after the service was replaced")
	Equal(t, int32(0), unavailable.Load(), "an evaluation ran while the service was missing")
	n := 0
	services.Range(func(any, any) bool { n++; return true })
	Equal(t, 2, n, "the two evaluations did not see the two providers")
}

func TestAConfigEvaluationFailureNamesTheValuesPosition(t *testing.T) {
	h := startWith(t, newEvaluator(func(string, loader.Scope) (any, error) { return nil, errors.New("env.X is unset") }))
	doc := Must(loader.Parse(loader.JSON, "f.json", []byte(`[
  { "id": "e", "name": "EchoPlugin",
    "config": { "times": "${{ env.X }}" } }
]`)))
	Equal(t, 0, len(h.loader.Validate(doc.Entries)), "Check accepts the expression")
	Within(t, "apply", func() { _ = h.loader.Apply(doc.Entries) })
	Eventually(t, func() bool {
		return strings.Contains(strings.Join(Texts(h.logs, cordis.LevelError), "\n"), "f.json:3:26: [0].config.times: cannot evaluate expression: env.X is unset")
	}, "the evaluation failure lacks its position:\n"+strings.Join(Texts(h.logs, cordis.LevelError), "\n"))
	True(t, !h.active("e"), "an entry whose config failed to evaluate is active")
}

func TestAnEmptyExpressionIsRejectedNotTreatedAsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.json")
	writeFile(t, path, `[
  { "id": "e", "name": "EchoPlugin", "disabled": { "$expr": "" } },
  { "id": "c", "name": "EchoPlugin", "config": { "times": { "$expr": "  " } } },
  { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
]`)
	h := startWith(t, newEvaluator(func(string, loader.Scope) (any, error) { return false, nil }))
	doc := Must(loader.Parse(loader.JSON, "f.json", []byte(readFile(t, path))))
	assertTexts(t, []string{
		"f.json:2:50: [0].disabled: expression must not be empty",
		`f.json:3:59: [1].config.times: expression "  ": expression must not be empty`,
	}, h.loader.Validate(doc.Entries))

	file := useFile(t, h, loader.ConfigFileOptions{Path: path})
	True(t, h.loader.Find("e") == nil && h.loader.Find("c") == nil, "an entry with an empty expression was loaded")
	True(t, h.active("b"), "the valid sibling did not load")
	errorsText := strings.Join(Texts(h.logs, cordis.LevelError), "\n")
	True(t, strings.Contains(errorsText, filepath.Base(path)+":2:50: [0].disabled: expression must not be empty"), errorsText)

	Within(t, "update", func() { NoError(t, h.fiber("b").Update(EchoOptions{Times: 2, Text: "b"}, false)) })
	Within(t, "save", file.Save)
	saved := readFile(t, path)
	True(t, strings.Contains(saved, `"times": 2`), "not written back:\n"+saved)
	True(t, strings.Contains(saved, `"$expr": ""`) && strings.Contains(saved, `"$expr": "  "`), "the invalid source was dropped:\n"+saved)
}
