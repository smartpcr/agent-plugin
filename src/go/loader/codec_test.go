package loader_test

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

var update = flag.Bool("update", false, "rewrite the loader's testdata/*.golden files from this run")

// assertGolden compares got with testdata/<name>.golden, rewriting it only with -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (record it with -update)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("output differs from %s\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func issueTexts(issues []loader.Issue) string {
	texts := make([]string, len(issues))
	for i, issue := range issues {
		texts[i] = issue.Error()
	}
	return strings.Join(texts, "\n") + "\n"
}

// ---------------------------------------------------------- DH-T-LOADER-1

// base.json: a wrong-typed id on line 4 column 9 and an unknown component on line 8.
const baseJSON = `[
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

//dsh:test DH-T-LOADER-1
func TestDiagnosticsCarryFileLineAndColumn(t *testing.T) {
	h := start(t)
	doc, err := loader.Parse(loader.JSON, "base.json", []byte(baseJSON))
	NoError(t, err)
	issues := h.loader.Validate(doc.Entries)
	Equal(t, 2, len(issues), issueTexts(issues))
	Equal(t, "base.json:4:9: [0].id: must be a string, got number", issues[0].Error())
	assertGolden(t, "diag_position", issueTexts(issues))
}

//dsh:test DH-T-LOADER-1
func TestConfigFileReportsAnInvalidEntryAtItsPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.json")
	writeFile(t, path, baseJSON)
	h := start(t)
	file := useFile(t, h, loader.ConfigFileOptions{Path: path})
	errorsText := strings.Join(Texts(h.logs, cordis.LevelError), "\n")
	for _, want := range []string{"base.json:4:9: [0].id: must be a string", "base.json:8:11: [1].name: cannot resolve"} {
		True(t, strings.Contains(errorsText, want), "missing "+want+" in:\n"+errorsText)
	}
	// a write-back keeps the entry that could not be decoded as it was written
	Within(t, "save", file.Save)
	True(t, strings.Contains(readFile(t, path), `"id": 7`), "the undecodable entry was lost:\n"+readFile(t, path))
}

func TestJSONCodecReportsMalformedInputWithAPosition(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"missing value", "[\n  {\"id\": }\n]", "f.json:2:10: unexpected character '}'"},
		{"unterminated", "[\n  \"abc", "f.json:2:3: unterminated string"},
		{"trailing", "[] x", "f.json:1:4: unexpected trailing data"},
		{"not an array", "\n  {}", "f.json:2:3: must be an array of entries, got object"},
		{"multibyte column", "[\"é\" 1]", "f.json:1:6: expected ',' or ']' in array"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loader.Parse(loader.JSON, "f.json", []byte(tc.text))
			var parseError *loader.ParseError
			True(t, errors.As(err, &parseError), "want a ParseError, got "+errText(err))
			Equal(t, tc.want, err.Error())
		})
	}
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func TestJSONCodecRoundTripsTheEntrySchema(t *testing.T) {
	text := `[{"id":"a","name":"EchoPlugin","config":{"times":"${{ env.X }}"},` +
		`"disabled":{"$expr":"os == \"windows\""},"inject":["counter"],` +
		`"isolate":{"counter":true,"timer":"shared"},"intercept":{"counter":{"step":2}},` +
		`"children":[{"id":"c","name":"EchoPlugin"}]}]`
	doc, err := loader.Parse(nil, "f.json", []byte(text))
	NoError(t, err)
	Equal(t, 0, len(doc.Issues()))
	entry := doc.Entries[0]
	Equal(t, `os == "windows"`, entry.DisabledExpr)
	Equal(t, "true", entry.Isolate["counter"])
	Equal(t, `{"step":2}`, string(entry.Intercept["counter"]))
	Equal(t, `{"times":"${{ env.X }}"}`, string(entry.Config))
	Equal(t, "f.json:1:2", entry.Position().String())

	encoded := Must(loader.JSON.Encode(nil, doc.Entries))
	again, err := loader.Parse(nil, "g.json", encoded)
	NoError(t, err)
	Equal(t, string(Must(json.Marshal(doc.Entries))), string(Must(json.Marshal(again.Entries))))

	var decoded loader.EntryOptions
	NoError(t, json.Unmarshal([]byte(`{"id":"x","disabled":"${{ flag }}","isolate":{"counter":true}}`), &decoded))
	Equal(t, "flag", decoded.DisabledExpr)
	err = json.Unmarshal([]byte(`{"id":1,"bogus":true}`), &decoded)
	True(t, err != nil && strings.Contains(err.Error(), "must be a string") && strings.Contains(err.Error(), "unknown key"), "want both faults, got "+errText(err))
}

// ---------------------------------------------------------- DH-T-LOADER-3

func TestValidateReportsEveryInvalidEntryInDocumentOrder(t *testing.T) {
	h := start(t)
	text := `[
  { "id": "a:b", "name": "EchoPlugin" },
  { "id": "ok", "name": "EchoPlugin" },
  { "id": "nameless" },
  { "id": "typo", "name": "EchoPlugin", "disabeld": true }
]`
	doc, err := loader.Parse(loader.JSON, "three.json", []byte(text))
	NoError(t, err)
	assertTexts(t, []string{
		`three.json:2:11: [0].id: entry id "a:b" must not contain ":"`,
		`three.json:4:3: [2].name: entry "nameless" requires a name`,
		`three.json:5:41: [3].disabeld: unknown key`,
	}, h.loader.Validate(doc.Entries))
}

func assertTexts(t *testing.T, want []string, issues []loader.Issue) {
	t.Helper()
	Equal(t, strings.Join(want, "\n")+"\n", issueTexts(issues))
}

func TestAnEntryWithThreeFaultsReportsThemAllAndIsDisposed(t *testing.T) {
	h := start(t)
	h.apply(t, echo("a", "a"), echo("b", "b"))
	True(t, h.active("a") && h.active("b"), "entries did not load")
	loaded := h.echo.String()

	doc, err := loader.Parse(loader.JSON, "f.json", []byte(`[
  { "id": "a", "name": 5, "group": "yes", "colour": "red" },
  { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
]`))
	NoError(t, err)
	h.apply(t, doc.Entries...)
	True(t, h.loader.Find("a") == nil, "the faulty entry was kept")
	True(t, h.active("b"), "the sibling did not stay active")
	Equal(t, loaded+",-a", h.echo.String(), "only the faulty entry is disposed; b is not reloaded")

	var reported []string
	for _, text := range Texts(h.logs, cordis.LevelError) {
		if strings.Contains(text, `entry "a"`) {
			reported = append(reported, text)
		}
	}
	Equal(t, 1, len(reported), strings.Join(Texts(h.logs, cordis.LevelError), "\n"))
	for _, want := range []string{"has 3 issues", "f.json:2:24: [0].name: must be a string, got number", "f.json:2:36: [0].group: must be a boolean, got string", "f.json:2:43: [0].colour: unknown key"} {
		True(t, strings.Contains(reported[0], want), "missing "+want+" in:\n"+reported[0])
	}
}

func TestAnInterceptReachesTheEntrysServices(t *testing.T) {
	seen := make(chan string, 1)
	probe := cordis.Define("Probe", func(ctx *cordis.Context, _ cordis.None) error {
		raw, _ := cordis.GetIntercept[json.RawMessage](ctx, "probe")
		seen <- string(raw)
		return nil
	})
	root, _ := Quiet()
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	MustWait(t, Must(root.Use(loader.Component, loader.Options{Resolver: loader.Components{"Probe": probe}})))
	service := Must(loader.Key.Get(root))
	Within(t, "apply", func() {
		NoError(t, service.Apply([]loader.EntryOptions{{ID: "p", Name: "Probe", Intercept: map[string]json.RawMessage{"probe": json.RawMessage(`{"step":2}`)}}}))
	})
	Equal(t, `{"step":2}`, <-seen)
}

// ------------------------------------------------- iteration 2 regressions

func TestDescendantsOfAnInvalidGroupAreStillReportedWhenLoading(t *testing.T) {
	h := start(t)
	doc, err := loader.Parse(loader.JSON, "nest.json", []byte(`[
  { "id": "g", "group": true, "colour": 1, "children": [
    { "id": "a:b", "name": "EchoPlugin" },
    { "id": "c", "name": "EchoPlugin", "disabeld": true }
  ] },
  { "id": "ok", "name": "EchoPlugin" }
]`))
	NoError(t, err)
	h.apply(t, doc.Entries...)
	True(t, h.loader.Find("g") == nil && h.loader.Find("g:c") == nil, "the invalid subtree was loaded")
	True(t, h.active("ok"), "the valid sibling did not load")
	errorsText := strings.Join(Texts(h.logs, cordis.LevelError), "\n")
	for _, want := range []string{
		"nest.json:2:31: [0].colour: unknown key",
		`nest.json:3:13: [0].children[0].id: entry id "a:b" must not contain ":"`,
		"nest.json:4:40: [0].children[1].disabeld: unknown key",
	} {
		True(t, strings.Contains(errorsText, want), "missing "+want+" in:\n"+errorsText)
	}
}

func TestChildrenOfDistinctAnonymousGroupsDoNotCollide(t *testing.T) {
	h := start(t)
	anonymous := func(children ...loader.EntryOptions) loader.EntryOptions {
		return loader.EntryOptions{Group: true, Children: children}
	}
	entries := []loader.EntryOptions{anonymous(echo("x", "1")), anonymous(echo("x", "2"))}
	Equal(t, 0, len(h.loader.Validate(entries)), issueTexts(h.loader.Validate(entries)))
	Equal(t, "", entries[0].ID, "Validate must not assign ids")

	issues := h.loader.Validate([]loader.EntryOptions{anonymous(echo("x", "1"), echo("x", "2"))})
	Equal(t, 1, len(issues), issueTexts(issues))
	True(t, strings.Contains(issues[0].Message, "duplicate entry id"), issues[0].Message)
}

// flakyCodec is JSON whose Encode fails while fail is set. ConfigFileOptions is
// deep-cloned, so the switch sits behind an unexported pointer.
type flakyCodec struct{ fail *atomic.Bool }

func (c flakyCodec) Decode(file string, data []byte) (*loader.Node, error) {
	return loader.JSON.Decode(file, data)
}

func (c flakyCodec) Encode(previous *loader.Document, entries []loader.EntryOptions) ([]byte, error) {
	if c.fail.Load() {
		return nil, errors.New("encoder unavailable")
	}
	return loader.JSON.Encode(previous, entries)
}

func TestAnEncodingFailureLeavesTheFileUntouchedUntilARetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.custom")
	writeFile(t, path, echoFile([2]string{"a", "a"}))
	original := readFile(t, path)
	codec := flakyCodec{fail: &atomic.Bool{}}
	h := start(t)
	file := useFile(t, h, loader.ConfigFileOptions{Path: path, Codec: codec})

	codec.fail.Store(true)
	Within(t, "update", func() { NoError(t, h.fiber("a").Update(EchoOptions{Times: 2, Text: "a"}, false)) })
	Within(t, "save", file.Save)
	Equal(t, original, readFile(t, path), "the file was rewritten despite the encoding failure")
	errorsText := strings.Join(Texts(h.logs, cordis.LevelError), "\n")
	True(t, strings.Contains(errorsText, "cannot encode config file") && strings.Contains(errorsText, "encoder unavailable"), errorsText)

	codec.fail.Store(false)
	Within(t, "retry", file.Save)
	True(t, strings.Contains(readFile(t, path), `"times": 2`), "the retry did not write:\n"+readFile(t, path))
}
