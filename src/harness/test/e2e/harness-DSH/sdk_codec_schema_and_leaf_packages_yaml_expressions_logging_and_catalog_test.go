//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	bootexpr "github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/yamlx"
)

const yelcEntriesJSON = `[
  {"id": "web", "name": "server.http", "config": {"host": "localhost", "port": {"$expr": "env.PORT ?? 3080"}, "ratio": 0.5, "tags": ["a", "b"], "tls": null}},
  {"id": "grp", "group": true, "disabled": {"$expr": "launch.safe"}, "children": [
    {"id": "watch", "name": "util.watch", "config": {"paths": ["src"], "depth": 3}}
  ]}
]
`

const yelcEntriesYAML = `# The same entries as the JSON document.
- id: web # the server
  name: server.http
  config:
    host: localhost
    port: !expr env.PORT ?? 3080
    ratio: 0.5
    tags: [a, b]
    tls: ~

- id: grp
  group: true
  disabled: !expr launch.safe
  children:
    - id: watch
      name: util.watch
      config:
        paths:
          - src
        depth: 0x3
`

const yelcCommented = `# Harness config, written by hand.

# The web server.
- id: web # the main one
  name: server.http
  config:
    host: "localhost"
    port: !expr env.PORT ?? 3080
    banner: |
      hello
      world
    tags: [a, b]

# The watcher.
- id: watch
  name: util.watch
  config:
    paths:
      - src
      - docs
# end of entries
`

type yelcWorld struct {
	modRoot string

	jsonText, yamlText string
	fromJSON, fromYAML *loader.Document

	yamlDoc  *yamlx.Document
	editOut  []byte
	yamlSrc  string
	editPath []any

	eval    *bootexpr.Evaluator
	result  any
	evalErr error

	cat      *catalog.Catalog
	printOut []byte
}

func yelcModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// yelcFlat renders the effective data of entries, one line per entry.
func yelcFlat(entries []loader.EntryOptions) (string, error) {
	var b strings.Builder
	var walk func(entries []loader.EntryOptions, depth int) error
	walk = func(entries []loader.EntryOptions, depth int) error {
		for _, e := range entries {
			var cfg bytes.Buffer
			if len(e.Config) > 0 {
				if err := json.Compact(&cfg, e.Config); err != nil {
					return fmt.Errorf("config of %s: %w", e.ID, err)
				}
			}
			fmt.Fprintf(&b, "%*s%s name=%s group=%v disabled=%v expr=%q config=%s\n",
				depth*2, "", e.ID, e.Name, e.Group, e.Disabled, e.DisabledExpr, cfg.String())
			if err := walk(e.Children, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(entries, 0)
	return b.String(), err
}

func yelcPath(s string) []any {
	var path []any
	for _, part := range strings.Split(s, "/") {
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err == nil && fmt.Sprint(n) == part {
			path = append(path, n)
		} else {
			path = append(path, part)
		}
	}
	return path
}

func (w *yelcWorld) readGolden(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(w.modRoot, filepath.FromSlash(rel)))
}

// --- Scenario: Same entries through JSON and YAML codecs ---

func (w *yelcWorld) equivalentDocuments() error {
	w.jsonText, w.yamlText = yelcEntriesJSON, yelcEntriesYAML
	return nil
}

func (w *yelcWorld) loadThroughCodecs() error {
	var err error
	if w.fromJSON, err = loader.Parse(loader.JSON, "app.json", []byte(w.jsonText)); err != nil {
		return fmt.Errorf("JSON codec: %w", err)
	}
	if w.fromYAML, err = loader.Parse(config.YAML, "app.yaml", []byte(w.yamlText)); err != nil {
		return fmt.Errorf("YAML codec: %w", err)
	}
	return nil
}

func (w *yelcWorld) treesEqual() error {
	j, err := yelcFlat(w.fromJSON.Entries)
	if err != nil {
		return err
	}
	y, err := yelcFlat(w.fromYAML.Entries)
	if err != nil {
		return err
	}
	if j == "" {
		return errors.New("JSON document produced no entries")
	}
	if j != y {
		return fmt.Errorf("effective trees differ:\nJSON:\n%s\nYAML:\n%s", j, y)
	}
	return nil
}

func (w *yelcWorld) yamlNoIssues() error {
	if issues := w.fromYAML.Issues(); len(issues) > 0 {
		return fmt.Errorf("YAML issues: %v", issues)
	}
	return nil
}

func (w *yelcWorld) entryPositioned(id, jsonPos, yamlPos string) error {
	for _, tc := range []struct {
		doc  *loader.Document
		want string
	}{{w.fromJSON, jsonPos}, {w.fromYAML, yamlPos}} {
		var found bool
		for _, e := range tc.doc.Entries {
			if e.ID != id {
				continue
			}
			found = true
			p := e.Position()
			if got := fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column); got != tc.want {
				return fmt.Errorf("entry %s at %s, want %s", id, got, tc.want)
			}
		}
		if !found {
			return fmt.Errorf("entry %s not found", id)
		}
	}
	return nil
}

func yelcPortNode(doc *loader.Document) (*loader.Node, error) {
	if doc.Root == nil || len(doc.Root.Items) == 0 {
		return nil, errors.New("document has no items")
	}
	for _, f := range doc.Root.Items[0].Fields {
		if f.Key != "config" {
			continue
		}
		for _, g := range f.Value.Fields {
			if g.Key == "port" {
				return g.Value, nil
			}
		}
	}
	return nil, errors.New("port node not found")
}

func (w *yelcWorld) yamlExprPoints(_, tag string) error {
	port, err := yelcPortNode(w.fromYAML)
	if err != nil {
		return err
	}
	p := port.Pos
	if port.Kind != loader.ExprNode || p.File != "app.yaml" || p.Line != 6 || p.Column != 11 {
		return fmt.Errorf("YAML port node = %+v, want an expression at app.yaml:6:11", port)
	}
	if p.Offset < 0 || p.Offset+len(tag) > len(w.yamlText) || w.yamlText[p.Offset:p.Offset+len(tag)] != tag {
		return fmt.Errorf("YAML port offset %d does not point at %q", p.Offset, tag)
	}
	return nil
}

func (w *yelcWorld) jsonExprPoints(_, ch string) error {
	port, err := yelcPortNode(w.fromJSON)
	if err != nil {
		return err
	}
	p := port.Pos
	if port.Kind != loader.ExprNode || p.File != "app.json" {
		return fmt.Errorf("JSON port node = %+v", port)
	}
	if p.Offset < 0 || p.Offset >= len(w.jsonText) || string(w.jsonText[p.Offset]) != ch {
		return fmt.Errorf("JSON port offset %d does not point at %q", p.Offset, ch)
	}
	return nil
}

// --- Scenario: yamlx edit keeps comments ---

func (w *yelcWorld) commentedDocument() error {
	w.yamlSrc = yelcCommented
	doc, err := yamlx.Parse("config.yaml", []byte(w.yamlSrc), yamlx.WithTag("!expr"))
	if err != nil {
		return fmt.Errorf("Parse: %w", err)
	}
	w.yamlDoc = doc
	return nil
}

func (w *yelcWorld) setScalar(path, value string) error {
	w.editPath = yelcPath(path)
	out, err := w.yamlDoc.Edit(yamlx.Set(w.editPath, value))
	if err != nil {
		return fmt.Errorf("Edit: %w", err)
	}
	w.editOut = out
	return nil
}

func (w *yelcWorld) editEqualsGolden(rel string) error {
	want, err := w.readGolden(rel)
	if err != nil {
		return fmt.Errorf("read golden: %w", err)
	}
	if !bytes.Equal(want, w.editOut) {
		return fmt.Errorf("edit output differs from %s:\n--- want\n%s\n--- got\n%s", rel, want, w.editOut)
	}
	return nil
}

func (w *yelcWorld) commentsRetained() error {
	out := string(w.editOut)
	n := 0
	for _, line := range strings.Split(w.yamlSrc, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			n++
			if c := strings.TrimSpace(line[i:]); !strings.Contains(out, c) {
				return fmt.Errorf("comment %q lost", c)
			}
		}
	}
	if n == 0 {
		return errors.New("source document holds no comments")
	}
	return nil
}

func (w *yelcWorld) sourceUnchanged(want, path string) error {
	v := w.yamlDoc.Value()
	for _, step := range yelcPath(path) {
		switch s := step.(type) {
		case int:
			if s >= len(v.Items) {
				return fmt.Errorf("index %d out of range", s)
			}
			v = v.Items[s]
		case string:
			next, ok := v.Get(s)
			if !ok {
				return fmt.Errorf("key %q missing", s)
			}
			v = next
		}
	}
	if v.Text != want {
		return fmt.Errorf("source document holds %q at %s, want %q (Edit mutated it)", v.Text, path, want)
	}
	return nil
}

// --- Scenario: Expression engine has no side effects ---

func (w *yelcWorld) evaluator(envPair, profile string) error {
	name, val, ok := strings.Cut(envPair, "=")
	if !ok {
		return fmt.Errorf("bad env pair %q", envPair)
	}
	env := map[string]string{name: val}
	e, err := bootexpr.New(bootexpr.Options{
		Env:    func(n string) (string, bool) { v, ok := env[n]; return v, ok },
		Secret: func(string) bool { return false },
		OS:     "linux",
		Cwd:    "/work",
		Home:   filepath.FromSlash("/home/u/.dsh"),
		Launch: bootexpr.Launch{Profile: profile, Home: "/home/u/.dsh", Cwd: "/work", Version: "1.2.3"},
	})
	if err != nil {
		return err
	}
	w.eval = e
	return nil
}

func (w *yelcWorld) evaluate(source string) error {
	w.result, w.evalErr = nil, nil
	scope := loader.Scope{Kind: loader.ConfigScope}
	if err := w.eval.Check(source, scope.Kind); err != nil {
		w.evalErr = err
		return nil
	}
	w.result, w.evalErr = w.eval.Eval(source, scope)
	return nil
}

func (w *yelcWorld) resolvesTo(want string) error {
	if w.evalErr != nil {
		return fmt.Errorf("evaluation failed: %v", w.evalErr)
	}
	if !reflect.DeepEqual(w.result, want) {
		return fmt.Errorf("resolved to %#v, want %q", w.result, want)
	}
	return nil
}

func (w *yelcWorld) failsNaming(name string) error {
	if w.evalErr == nil {
		return fmt.Errorf("evaluation succeeded with %#v, want an error naming %q", w.result, name)
	}
	if !strings.Contains(w.evalErr.Error(), name) {
		return fmt.Errorf("error %q does not name %q", w.evalErr, name)
	}
	return nil
}

func (w *yelcWorld) noImpureBuiltins() error {
	builtins := bootexpr.Builtins()
	if len(builtins) == 0 {
		return errors.New("no builtins reported")
	}
	// Allowlist of reviewed pure builtins: any addition fails until it is reviewed here.
	reviewed := []string{
		"abs", "all", "any", "bitand", "bitnot", "bitor", "bitshl", "bitshr", "bitushr", "bitxor",
		"ceil", "concat", "count", "filter", "find", "findIndex", "findLast", "findLastIndex",
		"first", "flatten", "float", "floor", "fromBase64", "fromJSON", "fromPairs", "get",
		"groupBy", "hasPrefix", "hasSuffix", "indexOf", "int", "join", "keys", "last",
		"lastIndexOf", "len", "lower", "map", "max", "mean", "median", "min", "none", "one",
		"reduce", "repeat", "replace", "reverse", "round", "sort", "sortBy", "split", "splitAfter",
		"string", "sum", "take", "toBase64", "toJSON", "toPairs", "trim", "trimPrefix",
		"trimSuffix", "type", "uniq", "upper", "values",
	}
	got := slices.Clone(builtins)
	slices.Sort(got)
	want := slices.Clone(reviewed)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		var extra, missing []string
		for _, b := range got {
			if !slices.Contains(want, b) {
				extra = append(extra, b)
			}
		}
		for _, b := range want {
			if !slices.Contains(got, b) {
				missing = append(missing, b)
			}
		}
		return fmt.Errorf("enabled builtins differ from the reviewed pure allowlist: unreviewed %v, missing %v", extra, missing)
	}
	return nil
}

func (w *yelcWorld) callsRejected(list string) error {
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		src := name + `("x")`
		err := w.eval.Check(src, loader.ConfigScope)
		if err == nil {
			_, err = w.eval.Eval(src, loader.Scope{Kind: loader.ConfigScope})
		}
		if err == nil {
			return fmt.Errorf("%s is callable", name)
		}
	}
	return nil
}

// --- Scenario: Catalog lists components by name ---

func (w *yelcWorld) compiledCatalog() error {
	c, err := builtin.Catalog()
	if err != nil {
		return err
	}
	w.cat = c
	return nil
}

func (w *yelcWorld) printed() error {
	var out bytes.Buffer
	if err := w.cat.Print(&out); err != nil {
		return err
	}
	w.printOut = out.Bytes()
	return nil
}

func (w *yelcWorld) printEqualsGolden(rel string) error {
	want, err := w.readGolden(rel)
	if err != nil {
		return fmt.Errorf("read golden: %w", err)
	}
	if !bytes.Equal(want, w.printOut) {
		return fmt.Errorf("catalog print differs from %s:\n--- want\n%s\n--- got\n%s", rel, want, w.printOut)
	}
	return nil
}

func (w *yelcWorld) everyResolves() error {
	items := w.cat.Items()
	if len(items) == 0 {
		return errors.New("catalog is empty")
	}
	for _, it := range items {
		got, ok := w.cat.Resolve(it.Name)
		if !ok || got != it.Component {
			return fmt.Errorf("%s does not resolve to its component", it.Name)
		}
		if !strings.Contains(string(w.printOut), it.Name) {
			return fmt.Errorf("%s missing from the printed list", it.Name)
		}
	}
	return nil
}

func InitializeScenario_sdk_codec_schema_and_leaf_packages_yaml_expressions_logging_and_catalog(ctx *godog.ScenarioContext) {
	w := &yelcWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = yelcWorld{}
		root, err := yelcModuleRoot()
		if err != nil {
			return c, err
		}
		w.modRoot = root
		return c, nil
	})

	ctx.Step(`^equivalent in-memory JSON and YAML entry documents$`, w.equivalentDocuments)
	ctx.Step(`^each document is loaded through its codec$`, w.loadThroughCodecs)
	ctx.Step(`^the effective entry trees are equal$`, w.treesEqual)
	ctx.Step(`^the YAML document reports no issues$`, w.yamlNoIssues)
	ctx.Step(`^the entry "([^"]*)" is positioned at "([^"]*)" in JSON and "([^"]*)" in YAML$`, w.entryPositioned)
	ctx.Step(`^the YAML expression node for "([^"]*)" points at the "([^"]*)" tag in the YAML source$`, w.yamlExprPoints)
	ctx.Step(`^the JSON expression node for "([^"]*)" points at its "([^"]*)" in the JSON source$`, w.jsonExprPoints)

	ctx.Step(`^an in-memory YAML document with comments$`, w.commentedDocument)
	ctx.Step(`^yamlx sets the scalar at "([^"]*)" to "([^"]*)"$`, w.setScalar)
	ctx.Step(`^the edited output equals the committed golden "([^"]*)"$`, w.editEqualsGolden)
	ctx.Step(`^every comment of the source document is retained in the output$`, w.commentsRetained)
	ctx.Step(`^the parsed source document still holds "([^"]*)" at "([^"]*)"$`, w.sourceUnchanged)

	ctx.Step(`^an expression evaluator over an in-memory scope with env "([^"]*)" and launch profile "([^"]*)"$`, w.evaluator)
	ctx.Step(`^the expression "([^"]*)" is evaluated$`, w.evaluate)
	ctx.Step(`^it resolves to "([^"]*)"$`, w.resolvesTo)
	ctx.Step(`^it fails with an error naming "([^"]*)"$`, w.failsNaming)
	ctx.Step(`^no enabled builtin reaches the filesystem, network, processes or clock$`, w.noImpureBuiltins)
	ctx.Step(`^calling any of "([^"]*)" is rejected$`, w.callsRejected)

	ctx.Step(`^the compiled-in catalog$`, w.compiledCatalog)
	ctx.Step(`^it is printed$`, w.printed)
	ctx.Step(`^the output equals the committed golden "([^"]*)"$`, w.printEqualsGolden)
	ctx.Step(`^every listed component resolves by its name$`, w.everyResolves)
}

func TestE2E_sdk_codec_schema_and_leaf_packages_yaml_expressions_logging_and_catalog(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_sdk_codec_schema_and_leaf_packages_yaml_expressions_logging_and_catalog,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sdk_codec_schema_and_leaf_packages_yaml_expressions_logging_and_catalog.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
