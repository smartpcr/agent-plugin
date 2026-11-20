package yamlx

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v4"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

const commented = `# Harness config, written by hand.

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

func parse(t *testing.T, text string) *Document {
	t.Helper()
	doc, err := Parse("config.yaml", []byte(text), WithTag("!expr"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func TestParseRejectsWithPosition(t *testing.T) { // DH-T-YAML-2
	for _, tc := range []struct {
		name, text   string
		line, column int
		message      string
	}{
		{"bad indentation", "a:\n  b: 1\n c: 2\n", 3, 2, "did not find expected key"},
		{"anchor", "a: &x 1\n", 1, 4, "anchor &x"},
		{"alias", "a: [1]\nb: *x\n", 0, 0, ""},
		{"merge key", "m:\n  <<: {a: 1}\n", 2, 3, "merge key"},
		{"second document", "a: 1\n---\nb: 2\n", 2, 1, "second document"},
		{"duplicate key", "a: 1\nb: 2\na: 3\n", 3, 1, `duplicate key "a"`},
		{"unknown tag", "a: !foo 1\n", 1, 4, "tag !foo"},
		{"core tag", "a: !!str 1\n", 1, 4, "tag !!str"},
		{"tag on a mapping", "a: !expr {b: 1}\n", 1, 4, "tag !expr"},
		{"tag on a sequence", "a: !expr [1]\n", 1, 4, "tag !expr"},
		{"tag on a key", "!expr a: 1\n", 1, 1, "on a key"},
		{"anchored key", "&k a: 1\n", 1, 1, "anchor &k"},
		{"complex key", "? [a]\n: 1\n", 1, 3, "must be a scalar"},
		{"alias value", "a: &x 1\n", 1, 4, "anchor"},
		{"leading zero", "mode: 0644\n", 1, 7, "0o644"},
		{"binary", "n: 0b101\n", 1, 4, "binary"},
		{"leading zero float", "n: 01.5\n", 1, 4, "leading zero"},
		{"out of range", "n: 99999999999999999999\n", 1, 4, "out of range"},
		{"hex out of range", "n: 0xFFFFFFFFFFFFFFFFFF\n", 1, 4, "out of range"},
		{"unicode column", "é: [é, x, 017]\n", 1, 11, "leading zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("bad.yaml", []byte(tc.text), WithTag("!expr"))
			var syntax *SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("Parse(%q) = %v, want a *SyntaxError", tc.text, err)
			}
			if tc.line == 0 {
				return
			}
			if syntax.File != "bad.yaml" || syntax.Line != tc.line || syntax.Column != tc.column || !strings.Contains(syntax.Message, tc.message) {
				t.Fatalf("Parse(%q) = %+v, want bad.yaml:%d:%d containing %q", tc.text, syntax, tc.line, tc.column, tc.message)
			}
		})
	}
}

func TestParseRejectsAlias(t *testing.T) {
	// an alias needs an anchor, which is rejected first; check the alias itself on a
	// tree the module built
	w := newWalker("f.yaml", nil, newConfig(nil))
	for _, n := range []*yaml.Node{{Kind: yaml.AliasNode, Value: "x", Line: 2, Column: 4}} {
		if _, err := w.value(n, 1); err == nil || !strings.Contains(err.Error(), "alias *x") {
			t.Fatalf("alias: %v", err)
		}
		if err := w.key(n); err == nil || !strings.Contains(err.Error(), "alias *x") {
			t.Fatalf("alias key: %v", err)
		}
	}
	if err := w.key(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "<<"}); err == nil {
		t.Fatal("merge key accepted")
	}
	if _, err := w.value(&yaml.Node{Kind: 99}, 1); err == nil {
		t.Fatal("unknown node kind accepted")
	}
}

func TestScalarTyping(t *testing.T) { // DH-T-YAML-3
	doc := parse(t, `t: true
f: False
n1: null
n2: ~
n3:
i: 42
neg: -7
octal: 0o17
hex: 0x1F
f1: 1.5
f2: -2.5e3
f3: .5
f4: .inf
f5: -.Inf
f6: .nan
yes: yes
on: on
time: 1:30
quoted: "017"
single: 'true'
zero: 0
signed: +3
word: 0xZZ
dash: --1
e: 1e
dot: .
`)
	want := map[string]any{
		"t": true, "f": false, "n1": nil, "n2": nil, "n3": nil,
		"i": int64(42), "neg": int64(-7), "octal": int64(15), "hex": int64(31),
		"f1": 1.5, "f2": -2500.0, "f3": 0.5, "f4": math.Inf(1), "f5": math.Inf(-1),
		"yes": "yes", "on": "on", "time": "1:30", "quoted": "017", "single": "true",
		"zero": int64(0), "signed": int64(3), "word": "0xZZ", "dash": "--1", "e": "1e", "dot": ".",
	}
	got := doc.Value().Plain().(map[string]any)
	if nan, ok := got["f6"].(float64); !ok || !math.IsNaN(nan) {
		t.Fatalf("f6 = %v, want NaN", got["f6"])
	}
	delete(got, "f6")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typed values:\n got %#v\nwant %#v", got, want)
	}
	if v, _ := doc.Value().Get("hex"); v.Text != "0x1F" || v.Pos.Line != 9 || v.Pos.Column != 6 {
		t.Fatalf("hex value = %+v", v)
	}
}

func TestTaggedScalarKeepsSourceAndPosition(t *testing.T) { // DH-LIB-YAML-R5
	doc := parse(t, commented)
	web := doc.Value().Items[0]
	config, _ := web.Get("config")
	port, ok := config.Get("port")
	if !ok || port.Kind != Tagged || port.Tag != "!expr" || port.Text != "env.PORT ?? 3080" {
		t.Fatalf("port = %+v", port)
	}
	if port.Pos.Line != 8 || port.Pos.Column != 11 || commented[port.Pos.Offset:port.Pos.Offset+5] != "!expr" {
		t.Fatalf("port position = %+v", port.Pos)
	}
	if got := port.Plain(); got != (TaggedScalar{Tag: "!expr", Source: "env.PORT ?? 3080"}) {
		t.Fatalf("Plain = %#v", got)
	}
}

func TestEditKeepsComments(t *testing.T) { // TC-04a
	doc := parse(t, commented)
	out, err := doc.Edit(Set([]any{0, "config", "host"}, "0.0.0.0"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	golden.Assert(t, "edit_keep_comments", out)
	for _, line := range strings.Split(commented, "\n") {
		if i := strings.Index(line, "#"); i >= 0 && !strings.Contains(string(out), strings.TrimSpace(line[i:])) {
			t.Errorf("comment %q lost", line[i:])
		}
	}
	if got, _ := doc.Value().Items[0].Get("config"); got.Fields[0].Value.Text != "localhost" {
		t.Fatal("Edit changed the document")
	}
}

func TestEditOperations(t *testing.T) { // DH-T-YAML-4
	doc := parse(t, commented)
	for _, tc := range []struct {
		name     string
		op       Op
		contains []string
		check    func(t *testing.T, v Value)
	}{
		{"set field", Set([]any{1, "config", "depth"}, 3), []string{"    depth: 3\n"}, func(t *testing.T, v Value) {
			c, _ := v.Items[1].Get("config")
			if d, _ := c.Get("depth"); d.Int != 3 {
				t.Fatalf("depth = %+v", d)
			}
		}},
		{"replace scalar keeps line comment", Set([]any{0, "id"}, "www"), []string{"- id: www # the main one\n"}, nil},
		{"replace with mapping", Set([]any{0, "config", "tags"}, map[string]any{"z": 1, "a": "017"}), []string{"    tags:\n      a: \"017\"\n      z: 1\n"}, nil},
		{"delete field", Delete([]any{0, "config", "banner"}), []string{"    host: \"localhost\"\n    port: !expr env.PORT ?? 3080\n    tags: [a, b]\n"}, nil},
		{"delete entry moves its comments", Delete([]any{0}), []string{"# The web server.\n# the main one\n# The watcher.\n- id: watch\n"}, func(t *testing.T, v Value) {
			if len(v.Items) != 1 {
				t.Fatalf("items = %d", len(v.Items))
			}
		}},
		{"delete last entry moves to the end", Delete([]any{1}), []string{"# The watcher.\n# end of entries\n"}, nil},
		{"insert entry", Insert(nil, -1, Value{Kind: Map, Fields: []Field{{Key: "id", Value: Value{Kind: String, Text: "new"}}, {Key: "when", Value: Value{Kind: Tagged, Tag: "!expr", Text: "launch.tty"}}}}),
			[]string{"\n\n- id: new\n  when: !expr launch.tty\n"}, nil},
		{"insert first", Insert(nil, 0, "first"), []string{"- first\n\n# The web server.\n"}, nil},
		{"insert into nested list", Insert([]any{1, "config", "paths"}, 1, "lib"), []string{"      - src\n      - lib\n      - docs\n"}, nil},
		{"replace item", Set([]any{1, "config", "paths", 0}, "source"), []string{"      - source\n"}, nil},
		{"delete item", Delete([]any{1, "config", "paths", 1}), []string{"      - src\n"}, nil},
		{"set expr", Set([]any{0, "config", "port"}, TaggedScalar{Tag: "!expr", Source: "8080"}), []string{"port: !expr 8080\n"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := doc.Edit(tc.op)
			if err != nil {
				t.Fatalf("Edit: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(string(out), want) {
					t.Fatalf("output lacks %q:\n%s", want, out)
				}
			}
			if !strings.Contains(string(out), "port: !expr env.PORT ?? 3080") && tc.name != "set expr" && !strings.HasPrefix(tc.name, "delete entry") {
				t.Fatalf("!expr scalar changed:\n%s", out)
			}
			back := parse(t, string(out))
			if tc.check != nil {
				tc.check(t, back.Value())
			}
		})
	}
}

func TestEditErrors(t *testing.T) {
	doc := parse(t, commented)
	for _, tc := range []struct {
		name string
		op   Op
		want string
	}{
		{"bad path element", Set([]any{0, 1.5}, 1), "is not a key or an index"},
		{"missing key", Delete([]any{0, "nope"}), "no such key"},
		{"missing nested key", Set([]any{0, "nope", "x"}, 1), "no such key"},
		{"index into mapping", Set([]any{0, "config", 0}, 1), "indexes a mapping"},
		{"key into sequence", Set([]any{"x"}, 1), "no such item"},
		{"index into mapping on the way", Set([]any{0, 0, "x"}, 1), "is not a sequence"},
		{"item out of range", Set([]any{9, "x"}, 1), "no such item"},
		{"set item out of range", Set([]any{9}, 1), "no such item"},
		{"insert into mapping", Insert([]any{0}, 0, 1), "insert needs a sequence"},
		{"into a scalar", Set([]any{0, "id", "x"}, 1), "is not a mapping or a sequence"},
		{"unwritable value", Set([]any{0, "id"}, make(chan int)), "cannot write"},
		{"unwritable insert", Insert(nil, 0, func() {}), "cannot write"},
		{"unwritable replace", Set([]any{0}, map[int]int{1: 1}), "keys must be strings"},
		{"unregistered tag", Set([]any{0, "id"}, TaggedScalar{Tag: "!other", Source: "x"}), "invalid document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := doc.Edit(tc.op)
			if err == nil || out != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Edit = %q, %v; want an error containing %q", out, err, tc.want)
			}
		})
	}
	empty := parse(t, "")
	if _, err := empty.Edit(Delete([]any{0})); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("edit of an empty document: %v", err)
	}
	out, err := empty.Edit(Set(nil, []any{"a", 1}))
	if err != nil || string(out) != "- a\n- 1\n" {
		t.Fatalf("set of an empty document = %q, %v", out, err)
	}
	out, err = parse(t, "# c\nx: 1 # one\n").Edit(Set(nil, "y"))
	if err != nil || !strings.Contains(string(out), "# one") {
		t.Fatalf("replace document = %q, %v", out, err)
	}
	out, err = parse(t, "x: 1\n").Edit(Set([]any{"<<"}, "a"), Set([]any{"n"}, "0b1"))
	if err != nil || !strings.Contains(string(out), `"<<": a`) || !strings.Contains(string(out), `n: "0b1"`) {
		t.Fatalf("quoted key and value = %q, %v", out, err)
	}
	out, err = parse(t, "a:\n  b: 1 # b\n  c: 2\n").Edit(Delete([]any{"a", "c"}), Delete([]any{"a", "b"}))
	if err != nil || !strings.Contains(string(out), "# b") {
		t.Fatalf("delete last nested field = %q, %v", out, err)
	}
}

func TestEditIsStable(t *testing.T) { // DH-T-YAML-5
	long := strings.Repeat("x", 300)
	doc := parse(t, commented+"\n- id: long\n  config:\n    text: "+long+"\n")
	first, err := doc.Edit()
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !strings.Contains(string(first), "text: "+long+"\n") {
		t.Fatal("a 300-character line was wrapped")
	}
	for _, sep := range []string{"\n\n# The web server.\n", "\n\n# The watcher.\n", "\n\n- id: long\n"} {
		if !strings.Contains(string(first), sep) {
			t.Fatalf("blank line before %q lost:\n%s", sep, first)
		}
	}
	second, err := parse(t, string(first)).Edit()
	if err != nil || string(second) != string(first) {
		t.Fatalf("rewriting the harness's own output changed it:\n%s\n---\n%s", first, second)
	}
	m := parse(t, "a: 1\n\n# b\nb: 2\nc: 3\n")
	out, err := m.Edit(Set([]any{"d"}, 4))
	if err != nil || string(out) != "a: 1\n\n# b\nb: 2\nc: 3\n\nd: 4\n" {
		t.Fatalf("mapping blank lines = %q, %v", out, err)
	}
}

func TestEditRefusesUnverifiedText(t *testing.T) { // DH-T-YAML-6
	doc := parse(t, commented)
	real := encode
	t.Cleanup(func() { encode = real })
	for _, tc := range []struct {
		name  string
		stub  func(n *yaml.Node) ([]byte, error)
		wantE string
	}{
		{"drops a comment", func(n *yaml.Node) ([]byte, error) {
			b, err := real(n)
			return []byte(strings.Replace(string(b), "# the main one", "", 1)), err
		}, "is lost"},
		{"changes a value", func(n *yaml.Node) ([]byte, error) {
			b, err := real(n)
			return []byte(strings.Replace(string(b), "util.watch", "util.other", 1)), err
		}, "differs"},
		{"breaks the syntax", func(n *yaml.Node) ([]byte, error) { return []byte("a: [\n"), nil }, "does not match"},
		{"fails", func(n *yaml.Node) ([]byte, error) { return nil, errors.New("boom") }, "boom"},
		{"panics", func(n *yaml.Node) ([]byte, error) { panic("boom") }, "internal YAML error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encode = tc.stub
			out, err := doc.Edit(Set([]any{0, "config", "host"}, "0.0.0.0"))
			if err == nil || out != nil || !strings.Contains(err.Error(), tc.wantE) {
				t.Fatalf("Edit = %q, %v; want an error containing %q", out, err, tc.wantE)
			}
		})
	}
	encode = func(n *yaml.Node) ([]byte, error) { panic("boom") }
	if _, err := Dump(1); err == nil || !strings.Contains(err.Error(), "internal YAML error") {
		t.Fatalf("Dump panic: %v", err)
	}
}

func TestLimits(t *testing.T) { // DH-T-YAML-7
	if _, err := Parse("big.yaml", []byte(strings.Repeat("a", 101)), WithLimits(100, 0)); err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("size limit: %v", err)
	}
	deep := strings.Repeat("[", 10) + strings.Repeat("]", 10)
	if _, err := Parse("deep.yaml", []byte(deep), WithLimits(0, 5)); err == nil || !strings.Contains(err.Error(), "deeper than the limit") {
		t.Fatalf("depth limit: %v", err)
	}
	if _, err := Parse("deep.yaml", []byte(deep), WithLimits(0, 0), nil); err != nil {
		t.Fatalf("default limits: %v", err)
	}
	if _, err := Parse("bom.yaml", []byte("a: \xff\xfe\n")); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestDump(t *testing.T) { // DH-LIB-YAML-R10
	type named string
	value := map[string]any{
		"z": []any{1, int8(2), uint(3), 1.0, float32(0.5), math.Inf(1), math.Inf(-1), math.NaN()},
		"a": map[string]string{"y": "yes", "n": "017", "e": ""},
		"m": []map[string]any{{"k": nil, "b": true}},
		"t": TaggedScalar{Tag: "!expr", Source: "env.X"},
		"p": (*int)(nil),
		"q": named("x"),
		"v": Value{Kind: Seq, Items: []Value{{Kind: Null}, {Kind: Bool, Bool: true}, {Kind: Int, Int: 2}, {Kind: Float, Float: 1e21}}},
		"r": [2]bool{true, false},
	}
	want := "a:\n  e: \"\"\n  n: \"017\"\n  y: yes\nm:\n  - b: true\n    k: null\np: null\nq: x\nr:\n  - true\n  - false\nt: !expr env.X\nv:\n  - null\n  - true\n  - 2\n  - 1e+21\nz:\n  - 1\n  - 2\n  - 3\n  - 1.0\n  - 0.5\n  - .inf\n  - -.inf\n  - .nan\n"
	for range 3 {
		out, err := Dump(value)
		if err != nil {
			t.Fatalf("Dump: %v", err)
		}
		if string(out) != want {
			t.Fatalf("Dump =\n%s\nwant\n%s", out, want)
		}
		if strings.ContainsAny(string(out), "&*") {
			t.Fatal("Dump wrote an anchor or alias")
		}
	}
	x := 5
	if out, err := Dump(&x); err != nil || string(out) != "5\n" {
		t.Fatalf("Dump(&x) = %q, %v", out, err)
	}
	if _, err := Dump(Value{Kind: Kind(42)}); err == nil {
		t.Fatal("unknown kind dumped")
	}
	if _, err := Dump(Value{Kind: Seq, Items: []Value{{Kind: Kind(42)}}}); err == nil {
		t.Fatal("unknown nested kind dumped")
	}
	if _, err := Dump(Value{Kind: Map, Fields: []Field{{Key: "a", Value: Value{Kind: Kind(42)}}}}); err == nil {
		t.Fatal("unknown field kind dumped")
	}
	if _, err := Dump([]any{func() {}}); err == nil {
		t.Fatal("func dumped")
	}
	if _, err := Dump(map[string]any{"f": func() {}}); err == nil {
		t.Fatal("func dumped")
	}
}

func TestDecodeFrontmatter(t *testing.T) {
	fields, body, err := DecodeFrontmatter([]byte("---\nname: skill\nversion: 2\n---\n# Body\n"))
	if err != nil || string(body) != "# Body\n" {
		t.Fatalf("DecodeFrontmatter = %q, %v", body, err)
	}
	if v, _ := fields.Get("version"); v.Int != 2 || v.Pos.Line != 3 {
		t.Fatalf("version = %+v", v)
	}
	if fields, body, err := DecodeFrontmatter([]byte("---\r\n---\r\nbody")); err != nil || fields.Kind != Map || string(body) != "body" {
		t.Fatalf("empty frontmatter = %+v, %q, %v", fields, body, err)
	}
	if fields, body, err := DecodeFrontmatter([]byte("---\na: 1\n...")); err != nil || len(fields.Fields) != 1 || len(body) != 0 {
		t.Fatalf("frontmatter at end = %+v, %q, %v", fields, body, err)
	}
	if fields, body, err := DecodeFrontmatter([]byte("# no frontmatter")); err != nil || fields.Kind != Map || string(body) != "# no frontmatter" {
		t.Fatalf("no frontmatter = %+v, %q, %v", fields, body, err)
	}
	for _, bad := range []string{"---\na: 1\n", "---\na: &x 1\n---\n", "---\n- a\n---\n"} {
		if _, _, err := DecodeFrontmatter([]byte(bad)); err == nil {
			t.Fatalf("DecodeFrontmatter(%q) accepted", bad)
		}
	}
}

func TestValueHelpers(t *testing.T) {
	a := parse(t, "a: [1, 2.5, x, true, null, !expr e]\nb: {c: 1}\n").Value()
	b := parse(t, "a: [1, 2.5, x, true, ~, !expr e]\nb:\n  c: 1\n").Value()
	if !Equal(a, b) {
		t.Fatal("equal documents differ")
	}
	for _, other := range []string{"a: [1]\nb: {c: 1}\n", "a: [2, 2.5, x, true, null, !expr e]\nb: {c: 1}\n", "a: [1, 2.5, x, true, null, !expr f]\nb: {c: 1}\n",
		"a: [1, 2.6, x, true, null, !expr e]\nb: {c: 1}\n", "a: [1, 2.5, y, true, null, !expr e]\nb: {c: 1}\n", "a: [1, 2.5, x, false, null, !expr e]\nb: {c: 1}\n",
		"a: [1, 2.5, x, true, null, !expr e]\nb: {d: 1}\n", "a: [1, 2.5, x, true, null, !expr e]\n", "a: 1\nb: 2\n"} {
		if Equal(a, parse(t, other).Value()) {
			t.Fatalf("%q equals the original", other)
		}
	}
	if !Equal(Value{Kind: Float, Float: math.NaN()}, Value{Kind: Float, Float: math.NaN()}) {
		t.Fatal("NaN differs from NaN")
	}
	if _, ok := a.Get("zz"); ok {
		t.Fatal("Get of a missing key")
	}
	if Seq.String() != "sequence" || Kind(42).String() != "Kind(42)" {
		t.Fatal("Kind.String")
	}
	for _, tc := range []struct {
		err  SyntaxError
		want string
	}{
		{SyntaxError{Message: "m"}, "yaml: m"},
		{SyntaxError{File: "f", Message: "m"}, "f: m"},
		{SyntaxError{Line: 1, Column: 2, Message: "m"}, "1:2: m"},
		{SyntaxError{File: "f", Line: 1, Column: 2, Message: "m"}, "f:1:2: m"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Fatalf("Error() = %q, want %q", got, tc.want)
		}
	}
	if doc := parse(t, "# only a comment\n"); doc.Value().Kind != Null || doc.File() != "config.yaml" {
		t.Fatalf("comment-only document = %+v", doc.Value())
	}
	if pathString(nil) != "(document)" || pathString([]any{"a", 0, "b"}) != "a[0].b" {
		t.Fatal("pathString")
	}
}
