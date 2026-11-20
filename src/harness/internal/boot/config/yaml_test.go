package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/yamlx"
)

const entriesJSON = `[
  {"id": "web", "name": "server.http", "config": {"host": "localhost", "port": {"$expr": "env.PORT ?? 3080"}, "ratio": 0.5, "tags": ["a", "b"], "tls": null}},
  {"id": "grp", "group": true, "disabled": {"$expr": "launch.safe"}, "children": [
    {"id": "watch", "name": "util.watch", "config": {"paths": ["src"], "depth": 3}}
  ]}
]
`

const entriesYAML = `# The same entries as entriesJSON.
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

// flat renders the effective data of entries, one line per entry.
func flat(t *testing.T, entries []loader.EntryOptions) string {
	t.Helper()
	var b strings.Builder
	var walk func(entries []loader.EntryOptions, depth int)
	walk = func(entries []loader.EntryOptions, depth int) {
		for _, e := range entries {
			var config bytes.Buffer
			if len(e.Config) > 0 {
				if err := json.Compact(&config, e.Config); err != nil {
					t.Fatalf("config of %s: %v", e.ID, err)
				}
			}
			fmt.Fprintf(&b, "%*s%s name=%s group=%v disabled=%v expr=%q config=%s\n", depth*2, "", e.ID, e.Name, e.Group, e.Disabled, e.DisabledExpr, config.String())
			walk(e.Children, depth+1)
		}
	}
	walk(entries, 0)
	return b.String()
}

func TestSameEntriesThroughJSONAndYAML(t *testing.T) { // DH-T-LOADER-2, TC-04a
	fromJSON, err := loader.Parse(loader.JSON, "app.json", []byte(entriesJSON))
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	fromYAML, err := loader.Parse(YAML, "app.yaml", []byte(entriesYAML))
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if j, y := flat(t, fromJSON.Entries), flat(t, fromYAML.Entries); j != y {
		t.Fatalf("effective trees differ:\nJSON:\n%s\nYAML:\n%s", j, y)
	}
	if issues := fromYAML.Issues(); len(issues) > 0 {
		t.Fatalf("YAML issues: %v", issues)
	}
	for _, tc := range []struct {
		doc          *loader.Document
		file         string
		line, column int
	}{
		{fromJSON, "app.json", 3, 3},
		{fromYAML, "app.yaml", 11, 3},
	} {
		grp := tc.doc.Entries[1]
		if pos := grp.Position(); pos.File != tc.file || pos.Line != tc.line || pos.Column != tc.column {
			t.Errorf("%s: entry grp at %v, want %s:%d:%d", tc.file, pos, tc.file, tc.line, tc.column)
		}
	}
	port := fromYAML.Root.Items[0].Fields[2].Value.Fields[1].Value
	if port.Kind != loader.ExprNode || port.Pos.Line != 6 || port.Pos.Column != 11 || entriesYAML[port.Pos.Offset:port.Pos.Offset+5] != "!expr" {
		t.Fatalf("port node = %+v", port)
	}
	jport := fromJSON.Root.Items[0].Fields[2].Value.Fields[1].Value
	if jport.Pos.File != "app.json" || entriesJSON[jport.Pos.Offset] != '{' {
		t.Fatalf("JSON port node = %+v", jport)
	}
}

func TestDecodeRejectsWithPosition(t *testing.T) {
	for _, tc := range []struct {
		text    string
		pos     string
		message string
	}{
		{"- a: &x 1\n", "bad.yaml:1:6", "anchor"},
		{"- a: !foo 1\n", "bad.yaml:1:6", "tag !foo"},
		{"- a: 017\n", "bad.yaml:1:6", "leading zero"},
		{"- a: .inf\n", "bad.yaml:1:6", "not a finite number"},
		{"- a: [1\n", "bad.yaml:2:1", "did not find"},
	} {
		_, err := YAML.Decode("bad.yaml", []byte(tc.text))
		var parse *loader.ParseError
		if !errors.As(err, &parse) || len(parse.Issues) != 1 || parse.Issues[0].Pos.String() != tc.pos || !strings.Contains(parse.Issues[0].Message, tc.message) {
			t.Errorf("Decode(%q) = %v; want %s: ...%s", tc.text, err, tc.pos, tc.message)
		}
	}
	if err := parseError("f", errors.New("plain")); !strings.Contains(err.Error(), "f: plain") {
		t.Fatalf("parseError = %v", err)
	}
	if _, err := loader.Parse(YAML, "empty.yaml", nil); err == nil || !strings.Contains(err.Error(), "must be an array") {
		t.Fatalf("empty file: %v", err)
	}
}

func TestEncodeRoundTrips(t *testing.T) {
	doc, err := loader.Parse(YAML, "app.yaml", []byte(entriesYAML))
	if err != nil {
		t.Fatal(err)
	}
	out, err := YAML.Encode(doc, doc.Entries)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := loader.Parse(YAML, "app.yaml", out)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, out)
	}
	if flat(t, back.Entries) != flat(t, doc.Entries) {
		t.Fatalf("Encode changed the entries:\n%s", out)
	}
	if !strings.Contains(string(out), "port: !expr env.PORT ?? 3080") {
		t.Fatalf("expression not written as a tag:\n%s", out)
	}
	if out, err := YAML.Encode(nil, nil); err != nil || string(out) != "[]\n" {
		t.Fatalf("Encode(nil) = %q, %v", out, err)
	}
}

const layerYAML = `# Project overrides.

# Turn the watcher off.
- id: watch
  disabled: true

# A faster server.
- id: web
  merge:
    port: 8080 # the dev port
    ratio: 1.5

# Gone.
- id: old
  remove: true
`

func TestEncodeLayerKeepsComments(t *testing.T) {
	doc, err := loader.ParseLayer(YAML, "project.yaml", []byte(layerYAML))
	if err != nil {
		t.Fatalf("ParseLayer: %v", err)
	}
	if len(doc.Ops) != 3 || len(doc.Issues) != 0 {
		t.Fatalf("ops = %+v, issues = %v", doc.Ops, doc.Issues)
	}
	same, err := YAML.EncodeLayer(doc, doc.Ops)
	if err != nil || string(same) != layerYAML {
		t.Fatalf("unchanged layer rewritten:\n%s\n%v", same, err)
	}
	ops := append([]loader.Op{}, doc.Ops[:2]...)
	ops[1].Patch = json.RawMessage(`{"port": 9090, "ratio": 1.5, "host": "0.0.0.0"}`)
	ops = append(ops, loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{{ID: "new", Name: "util.watch"}}})
	out, err := YAML.EncodeLayer(doc, ops)
	if err != nil {
		t.Fatalf("EncodeLayer: %v", err)
	}
	for _, want := range []string{"# Project overrides.", "# Turn the watcher off.", "# A faster server.", "port: 9090 # the dev port", "host: 0.0.0.0", "# Gone.", "- insert:\n    - id: new\n      name: util.watch\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	back, err := loader.ParseLayer(YAML, "project.yaml", out)
	if err != nil || len(back.Ops) != 3 || back.Ops[1].ID != "web" || back.Ops[2].Kind != loader.OpInsert {
		t.Fatalf("re-parse = %+v, %v", back, err)
	}
	fresh, err := YAML.EncodeLayer(nil, ops[:1])
	if err != nil || string(fresh) != "- id: watch\n  disabled: true\n" {
		t.Fatalf("fresh layer = %q, %v", fresh, err)
	}
	if empty, err := YAML.EncodeLayer(nil, nil); err != nil || string(empty) != "[]\n" {
		t.Fatalf("empty layer = %q, %v", empty, err)
	}
}

func TestEncodeLayerReordered(t *testing.T) {
	doc, err := loader.ParseLayer(YAML, "project.yaml", []byte(layerYAML))
	if err != nil {
		t.Fatal(err)
	}
	ops := []loader.Op{doc.Ops[2], doc.Ops[0]}
	out, err := YAML.EncodeLayer(doc, ops)
	if err != nil {
		t.Fatalf("EncodeLayer: %v", err)
	}
	back, err := loader.ParseLayer(YAML, "project.yaml", out)
	if err != nil || len(back.Ops) != 2 || back.Ops[0].ID != "old" || back.Ops[1].ID != "watch" {
		t.Fatalf("reordered = %s, %v", out, err)
	}
}

func TestDiff(t *testing.T) {
	for _, tc := range []struct {
		name, old, want string
	}{
		{"index items", "[1, 2, 3]", `[1, 5]`},
		{"append items", "[1]", `[1, 2, 3]`},
		{"scalars", "- a: x\n  b: 1.5\n  c: true\n  d: null\n  e: !expr x\n", `[{"a": "y", "b": 2.5, "c": false, "d": 1, "e": {"$expr": "y"}}]`},
		{"kind change", "- a: {b: 1}\n", `[{"a": [1]}]`},
		{"drop and add ids", "- id: a\n- id: b\n- id: c\n", `[{"id": "c"}, {"id": "d"}]`},
		{"duplicate ids", "- id: a\n- id: a\n", `[{"id": "b"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := loader.ParseLayer(YAML, "l.yaml", []byte(tc.old))
			if err != nil {
				t.Fatal(err)
			}
			want, err := loader.JSON.Decode("", []byte(tc.want))
			if err != nil {
				t.Fatal(err)
			}
			out, err := editTo(doc, want)
			if err != nil {
				t.Fatalf("edit: %v", err)
			}
			back, err := YAML.Decode("l.yaml", out)
			if err != nil || !same(back, want) {
				t.Fatalf("edit gives %s, want %s (%v)", out, tc.want, err)
			}
		})
	}
	if !same(&loader.Node{Kind: loader.NullNode}, &loader.Node{Kind: loader.NullNode}) || same(&loader.Node{Kind: loader.BoolNode}, &loader.Node{Kind: loader.BoolNode, Bool: true}) {
		t.Fatal("same of scalars")
	}
	obj := func(s string) *loader.Node { n, _ := loader.JSON.Decode("", []byte(s)); return n }
	if same(obj(`{"a":1}`), obj(`{"a":1,"b":2}`)) || same(obj(`{"a":1}`), obj(`{"b":1}`)) || same(obj(`[1]`), obj(`[1,2]`)) || same(obj(`[1]`), obj(`[2]`)) {
		t.Fatal("same of different trees")
	}
}

func TestRegister(t *testing.T) {
	Register()
	Register()
	for _, file := range []string{"a.yaml", "b.YML"} {
		if loader.CodecFor(file) != YAML {
			t.Fatalf("CodecFor(%s) is not YAML", file)
		}
	}
}

// editTo applies the edits diff finds to the text of doc.
func editTo(doc *loader.LayerDocument, want *loader.Node) ([]byte, error) {
	parsed, err := yamlx.Parse(doc.File, doc.Data, yamlx.WithTag(ExprTag))
	if err != nil {
		return nil, err
	}
	var ops []yamlx.Op
	diff(parsed.Value(), want, nil, &ops)
	return parsed.Edit(ops...)
}
