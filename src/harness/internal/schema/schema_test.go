package schema

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

type sample struct {
	Name   string            `json:"name" jsonschema:"who is asking"`
	N      int               `json:"n" dsh:"min=0"`
	Tags   []string          `json:"tags,omitempty" dsh:"max=3"`
	Mode   string            `json:"mode,omitempty" dsh:"enum=slow|steady"`
	Nested map[string]string `json:"nested,omitempty"`
}

// reordered is sample with its fields reordered and its descriptions edited.
type reordered struct {
	Nested map[string]string `json:"nested,omitempty" jsonschema:"edited"`
	Mode   string            `json:"mode,omitempty" dsh:"enum=slow|steady"`
	N      int               `json:"n" dsh:"min=0" jsonschema:"a count"`
	Tags   []string          `json:"tags,omitempty" dsh:"max=3"`
	Name   string            `json:"name" jsonschema:"someone else"`
}

type retyped struct {
	Name   string            `json:"name"`
	N      string            `json:"n"`
	Tags   []string          `json:"tags,omitempty" dsh:"max=3"`
	Mode   string            `json:"mode,omitempty" dsh:"enum=slow|steady"`
	Nested map[string]string `json:"nested,omitempty"`
}

type unrequired struct {
	Name   string            `json:"name,omitempty"`
	N      int               `json:"n" dsh:"min=0"`
	Tags   []string          `json:"tags,omitempty" dsh:"max=3"`
	Mode   string            `json:"mode,omitempty" dsh:"enum=slow|steady"`
	Nested map[string]string `json:"nested,omitempty"`
}

func paths(is Issues) []string {
	out := make([]string, len(is))
	for i, x := range is {
		out[i] = x.Path
	}
	return out
}

// TestShapeScenario is the stage scenario: one declaration derives a document, a
// validator naming the offending field, and a stable fingerprint.
func TestShapeScenario(t *testing.T) {
	a, err := Of[sample]()
	if err != nil {
		t.Fatal(err)
	}
	issues := a.Validate(json.RawMessage(`{"name":"x","n":-1}`))
	if len(issues) != 1 || issues[0].Path != "/n" || !strings.Contains(issues[0].Message, "at least 0") {
		t.Fatalf("issues = %v, want one at /n", issues)
	}
	b, err := Of[sample]()
	if err != nil || a.Fingerprint() != b.Fingerprint() || string(a.JSONSchema()) != string(b.JSONSchema()) {
		t.Fatalf("two derivations differ: %s vs %s (%v)", a.Fingerprint(), b.Fingerprint(), err)
	}
	if !strings.HasPrefix(a.Fingerprint(), "sha256:") || len(a.Fingerprint()) != len("sha256:")+64 {
		t.Fatalf("fingerprint %q", a.Fingerprint())
	}
}

func TestDocument(t *testing.T) {
	doc := string(MustOf[sample]().JSONSchema())
	order := []string{`"name"`, `"n"`, `"tags"`, `"mode"`, `"nested"`}
	last := -1
	for _, p := range order {
		i := strings.Index(doc, p+":")
		if i <= last {
			t.Fatalf("property %s out of field order in %s", p, doc)
		}
		last = i
	}
	for _, want := range []string{`"description":"who is asking"`, `"minimum":0`, `"maxItems":3`, `"enum":["slow","steady"]`, `"additionalProperties":false`, `"required":["name","n"]`} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %s: %s", want, doc)
		}
	}
}

func TestRawMessageIsAnyJSON(t *testing.T) {
	type carrier struct {
		Data json.RawMessage `json:"data,omitempty"`
	}
	typ := MustOf[carrier]()
	for _, raw := range []string{`{"data":{"x":[1,"a"]}}`, `{"data":"text"}`, `{"data":3}`, `{}`} {
		if is := typ.Validate(json.RawMessage(raw)); is != nil {
			t.Errorf("Validate(%s) = %v, want nil", raw, is)
		}
		var c carrier
		if is := typ.Decode([]byte(raw), &c); is != nil {
			t.Errorf("Decode(%s) = %v, want nil", raw, is)
		}
	}
	if doc := string(typ.JSONSchema()); !strings.Contains(doc, `"data":true`) {
		t.Fatalf("json.RawMessage derived as %s, want any value", doc)
	}
}

func TestFingerprint(t *testing.T) {
	base := MustOf[sample]().Fingerprint()
	if got := MustOf[reordered]().Fingerprint(); got != base {
		t.Fatalf("reordering fields and editing descriptions changed the fingerprint")
	}
	for name, fp := range map[string]string{
		"retyped":    MustOf[retyped]().Fingerprint(),
		"unrequired": MustOf[unrequired]().Fingerprint(),
		"open":       MustOf[sample](Open()).Fingerprint(),
		"removed":    MustOf[struct{ N int }]().Fingerprint(),
	} {
		if fp == base {
			t.Errorf("%s shape kept the fingerprint", name)
		}
	}
}

func TestValidateReportsEveryIssue(t *testing.T) {
	typ := MustOf[sample]()
	issues := typ.Validate([]byte(`{"n":-1,"tags":["a","b","c","d"],"mode":"fast","nested":{"k":1},"extra":true}`))
	want := []string{"/name", "/extra", "/mode", "/n", "/nested/k", "/tags"}
	if got := paths(issues); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v (%v)", got, want, issues)
	}
	if !strings.Contains(issues.Error(), "/name: is required") {
		t.Fatalf("Error() = %q", issues.Error())
	}
	if is := typ.Validate(sample{Name: "x", N: 2}); is != nil {
		t.Fatalf("a conforming Go value has issues: %v", is)
	}
	if is := typ.Validate(make(chan int)); len(is) != 1 || !strings.Contains(is[0].Message, "does not encode") {
		t.Fatalf("Validate(chan) = %v", is)
	}
	if is := typ.Validate([]byte(`{`)); len(is) != 1 || is[0].String() != "/: "+is[0].Message {
		t.Fatalf("Validate(bad JSON) = %v", is)
	}
	if is := typ.Validate([]byte(`{"name":"x","n":1.5}`)); len(is) != 1 || !strings.Contains(is[0].Message, "must be integer, not number") {
		t.Fatalf("Validate(1.5) = %v", is)
	}
}

type checked struct {
	A int `json:"a"`
}

var errPlain = errors.New("plain rule")

func (c checked) Validate() error {
	switch c.A {
	case 1:
		return Issues{{Path: "/a", Message: "must not be 1"}}
	case 2:
		return errPlain
	}
	return nil
}

func TestDecode(t *testing.T) {
	typ := MustOf[checked]()
	var c checked
	if is := typ.Decode([]byte(`{"a":3}`), &c); is != nil || c.A != 3 {
		t.Fatalf("Decode = %v, %+v", is, c)
	}
	cases := map[string]string{
		`{"a":1}`:         "/a: must not be 1",
		`{"a":2}`:         "/: plain rule",
		`{"a":3} {}`:      "data after the value",
		`{"a":3,"b":1}`:   "/b: is not a known field",
		`{"a":"x"}`:       "must be integer",
		`{"a":2.5}`:       "must be integer",
		`{"a":1e400}`:     "does not decode",
		`{"a":3,"a":"x"}`: "must be integer",
	}
	for in, want := range cases {
		if is := typ.Decode([]byte(in), &c); !strings.Contains(is.Error(), want) {
			t.Errorf("Decode(%s) = %v, want %q", in, is, want)
		}
	}
	open := MustOf[checked](Open())
	if is := open.Decode([]byte(`{"a":3,"b":1}`), &c); is != nil {
		t.Fatalf("an open shape refused an unknown root key: %v", is)
	}
	if is := open.Decode([]byte(`{"a":3}`), &json.RawMessage{}); is != nil {
		t.Fatalf("Decode into raw = %v", is)
	}
	if is := open.Decode([]byte(`{"a":3}`), 5); len(is) != 1 || !strings.Contains(is[0].Message, "does not decode") {
		t.Fatalf("Decode into a non-pointer = %v", is)
	}
}

func TestTags(t *testing.T) {
	type tagged struct {
		Re    string         `json:"re" dsh:"pattern=^[a-z]{1,3}$"`
		Size  float64        `json:"size,omitempty" dsh:"min=0.5,max=2,enum=0.5|1|2"`
		Opt   string         `json:"opt,omitempty" dsh:"required,min=1,max=4,live,secret,ui=password"`
		Items []int          `json:"items,omitempty" dsh:"min=1"`
		Map   map[string]int `json:"map,omitempty" dsh:"min=1,max=2"`
		Inner []struct {
			V uint8 `json:"v" dsh:"max=9"`
		} `json:"inner,omitempty"`
		Skipped string `json:"-" dsh:"nonsense"`
		hidden  string
		Ptr     *int `dsh:"min=1"`
	}
	typ, err := Of[tagged]()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(typ.JSONSchema())
	for _, want := range []string{`"pattern":"^[a-z]{1,3}$"`, `"x-dsh-live":true`, `"x-dsh-secret":true`, `"x-dsh-ui":"password"`, `"minProperties":1`, `"minLength":1`, `"Ptr"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %s: %s", want, doc)
		}
	}
	bad := `{"re":"ABCD","size":1.5,"opt":"","items":[],"map":{"a":1,"b":2,"c":3},"inner":[{"v":10}],"Ptr":0}`
	want := []string{"/Ptr", "/inner/0/v", "/items", "/map", "/opt", "/re", "/size"}
	if got := paths(typ.Validate([]byte(bad))); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if is := typ.Validate([]byte(`{"re":"ab","opt":"x","Ptr":null}`)); is != nil {
		t.Fatalf("a conforming value has issues: %v", is)
	}
	_ = tagged{}.hidden
}

func TestTagErrors(t *testing.T) {
	for name, typ := range map[string]reflect.Type{
		"unknown word": reflect.TypeFor[struct {
			A int `dsh:"nope"`
		}](),
		"not a number": reflect.TypeFor[struct {
			A int `dsh:"min=x"`
		}](),
		"bool bound": reflect.TypeFor[struct {
			A bool `dsh:"min=1"`
		}](),
		"count fraction": reflect.TypeFor[struct {
			A string `dsh:"max=1.5"`
		}](),
		"min above max": reflect.TypeFor[struct {
			A int `dsh:"min=3,max=1"`
		}](),
		"enum number": reflect.TypeFor[struct {
			A int `dsh:"enum=1|x"`
		}](),
		"int pattern": reflect.TypeFor[struct {
			A int `dsh:"pattern=x"`
		}](),
		"bad pattern": reflect.TypeFor[struct {
			A string `dsh:"pattern=("`
		}](),
		"nested": reflect.TypeFor[struct {
			B []struct {
				A int `dsh:"nope"`
			}
		}](),
		"map value": reflect.TypeFor[map[string]struct {
			A int `dsh:"nope"`
		}](),
		"channel": reflect.TypeFor[chan int](),
	} {
		if _, err := ForType(typ); err == nil {
			t.Errorf("%s: derivation succeeded", name)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustOf did not panic")
		}
	}()
	MustOf[struct {
		A int `dsh:"nope"`
	}]()
}

func TestFromJSON(t *testing.T) {
	typ, err := FromJSON([]byte(`{
		"type":"object",
		"properties":{
			"a":{"type":"integer","exclusiveMinimum":0,"exclusiveMaximum":10,"multipleOf":2},
			"b":{"type":"string","maxLength":2,"description":"eval('x')"},
			"c":{"type":"array","items":{"type":"number"},"uniqueItems":true,"maxItems":3},
			"d":{"const":{"k":[1]}},
			"e":{"anyOf":[{"type":"string"},{"type":"boolean"}]},
			"f":{"oneOf":[{"type":"number"},{"type":"integer"}]},
			"g":{"not":{"type":"null"}},
			"h":{"allOf":[{"type":["string","null"]},{"minLength":1}]},
			"i":{"type":"object","maxProperties":1,"additionalProperties":{"type":"boolean"}},
			"j":false
		},
		"minProperties":1
	}`))
	if err != nil {
		t.Fatal(err)
	}
	bad := `{"a":-1,"b":"abc","c":[1,1,"x",2],"d":"z","e":1,"f":1,"g":null,"h":"","i":{"x":true,"y":1},"j":1}`
	want := []string{"/a", "/a", "/b", "/c", "/c/1", "/c/2", "/d", "/e", "/f", "/g", "/h", "/i", "/i/y", "/j"}
	if got := paths(typ.Validate([]byte(bad))); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if is := typ.Validate([]byte(`{"a":4,"b":"ab","c":[1,2],"d":{"k":[1]},"e":true,"f":1.5,"g":1,"h":null,"i":{"x":true}}`)); is != nil {
		t.Fatalf("a conforming value has issues: %v", is)
	}
	if is := typ.Validate([]byte(`{}`)); len(is) != 1 || is[0].Path != "" {
		t.Fatalf("minProperties: %v", is)
	}
	if is := typ.Validate([]byte(`{"a":10}`)); len(is) != 1 || !strings.Contains(is[0].Message, "below 10") {
		t.Fatalf("exclusiveMaximum: %v", is)
	}

	for _, doc := range []string{`{`, `{"$ref":"#/nope"}`, `{"pattern":"("}`} {
		if _, err := FromJSON([]byte(doc)); err == nil {
			t.Errorf("FromJSON(%s) succeeded", doc)
		}
	}
}

// TestNumbersAreExact: numbers are compared as decimals, never through float64.
func TestNumbersAreExact(t *testing.T) {
	for _, tc := range []struct {
		schema, value string
		ok            bool
	}{
		{`{"type":"integer"}`, `9007199254740992.5`, false},
		{`{"type":"integer"}`, `9007199254740993`, true},
		{`{"type":"integer"}`, `1.0`, true},
		{`{"type":"integer"}`, `1.5e1`, true},
		{`{"type":"integer"}`, `15e-1`, false},
		{`{"type":"integer"}`, `1e400`, true},
		{`{"type":"integer"}`, `1e-400`, false},
		{`{"enum":[9007199254740992]}`, `9007199254740993`, false},
		{`{"enum":[9007199254740992]}`, `9007199254740992`, true},
		{`{"enum":[9007199254740992]}`, `9.007199254740992e15`, true},
		{`{"const":[0.1]}`, `[1e-1]`, true},
		{`{"const":0}`, `-0.0`, true},
		{`{"type":"array","uniqueItems":true}`, `[9007199254740992,9007199254740993]`, true},
		{`{"type":"array","uniqueItems":true}`, `[10,1e1]`, false},
		{`{"type":"number","multipleOf":0.1}`, `0.3`, true},
		{`{"type":"number","multipleOf":0.1}`, `0.7`, true},
		{`{"type":"number","multipleOf":0.1}`, `-12.3e2`, true},
		{`{"type":"number","multipleOf":0.1}`, `0`, true},
		{`{"type":"number","multipleOf":0.1}`, `0.31`, false},
		{`{"type":"number","multipleOf":0.1}`, `0.30000000000000004`, false},
		{`{"type":"number","multipleOf":0.1}`, `1e-400`, false},
		{`{"type":"number","multipleOf":0.1}`, `1e999999999999999999999`, true},
		{`{"type":"number","multipleOf":0.01}`, `19.99`, true},
		{`{"type":"number","multipleOf":0.01}`, `19.999`, false},
		{`{"type":"number","multipleOf":3}`, `9e400`, true},
		{`{"type":"number","multipleOf":7}`, `1e400`, false},
		{`{"type":"number","multipleOf":2.5}`, `7.5`, true},
		{`{"type":"number","maximum":9007199254740992}`, `9007199254740993`, false},
		{`{"type":"number","minimum":0.1}`, `0.09999999999999999`, false},
		{`{"type":"number","minimum":-1}`, `-0.5`, true},
		{`{"type":"number","exclusiveMinimum":-1}`, `-1`, false},
		{`{"type":"number","exclusiveMaximum":1e308}`, `1e309`, false},
		{`{"type":"number","minimum":-1e308}`, `-1e309`, false},
		{`{"type":"number","maximum":1}`, `-1e-999999999999999999999`, true},
	} {
		typ, err := FromJSON([]byte(tc.schema))
		if err != nil {
			t.Fatal(err)
		}
		if is := typ.Validate([]byte(tc.value)); (is == nil) != tc.ok {
			t.Errorf("%s against %s: issues %v, want ok=%v", tc.value, tc.schema, is, tc.ok)
		}
	}
}

func TestDecimal(t *testing.T) {
	for _, s := range []string{"", "-", "1.", ".1", "1e", "1e+", "x", "1.x", "1ex", "--1"} {
		if _, ok := parseDecimal(s); ok {
			t.Errorf("parseDecimal(%q) succeeded", s)
		}
	}
	for v, want := range map[any]string{
		int(-3): "-3", int64(4): "4", int32(5): "5", uint(6): "6", uint64(7): "7",
		float32(0.5): "0.5", 1.25: "1.25", json.Number("-0012.5000e+1"): "-125",
	} {
		d, ok := toDecimal(v)
		w, _ := parseDecimal(want)
		if !ok || d != w {
			t.Errorf("toDecimal(%v) = %+v, %v; want %+v", v, d, ok, w)
		}
	}
	for _, v := range []any{math.NaN(), math.Inf(1), "1", nil} {
		if _, ok := toDecimal(v); ok {
			t.Errorf("toDecimal(%v) succeeded", v)
		}
	}
	zero, _ := parseDecimal("0")
	one, _ := parseDecimal("1")
	if !zero.multipleOf(one) || one.multipleOf(zero) || zero.cmp(one) >= 0 || one.cmp(zero) <= 0 || zero.cmp(zero) != 0 {
		t.Fatal("zero arithmetic")
	}
	// Bounds that cannot come from JSON: ±Inf compare as such, NaN fails.
	inst := json.Number("5")
	for _, tc := range []struct {
		s  jsonschema.Schema
		ok bool
	}{
		{jsonschema.Schema{Minimum: jsonschema.Ptr(math.Inf(-1)), Maximum: jsonschema.Ptr(math.Inf(1))}, true},
		{jsonschema.Schema{ExclusiveMinimum: jsonschema.Ptr(math.Inf(1))}, false},
		{jsonschema.Schema{ExclusiveMaximum: jsonschema.Ptr(math.NaN())}, false},
		{jsonschema.Schema{MultipleOf: jsonschema.Ptr(math.Inf(1))}, false},
	} {
		var v validator
		v.checkNumber(&tc.s, inst, "")
		if (len(v.issues) == 0) != tc.ok {
			t.Errorf("bounds %+v: %v", tc.s, v.issues)
		}
	}
	var v validator
	v.checkNumber(&jsonschema.Schema{}, json.Number("nope"), "")
	if len(v.issues) != 1 {
		t.Fatalf("a malformed number: %v", v.issues)
	}
}

func TestFromJSONOutsideSubset(t *testing.T) {
	typ, err := FromJSON([]byte(`{"type":"object","properties":{"a":{"type":"integer"}},"propertyNames":{"maxLength":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if typ.fallback == nil {
		t.Fatal("a propertyNames document should use the module's validator")
	}
	if is := typ.Validate([]byte(`{"a":"x"}`)); len(is) != 1 {
		t.Fatalf("issues = %v", is)
	}
	if is := typ.Validate([]byte(`{"a":1,"b":[1.5,{"c":2}]}`)); is != nil {
		t.Fatalf("issues = %v", is)
	}
	// The module compares float64, so a number it would round is refused, never
	// checked as its rounded neighbour.
	for _, v := range []string{`{"a":9007199254740993}`, `{"a":9007199254740992.5}`, `{"b":[1e400]}`} {
		if is := typ.Validate([]byte(v)); len(is) != 1 || !strings.Contains(is[0].Message, "cannot be checked exactly") {
			t.Errorf("%s: issues = %v", v, is)
		}
	}
	for _, doc := range []string{
		`{"propertyNames":{"maxLength":3},"maximum":9007199254740993}`,
		`{"propertyNames":{"maxLength":3},"enum":[9007199254740993]}`,
		`{"propertyNames":{"maxLength":3},"if":{"const":0.30000000000000000001}}`,
	} {
		if _, err := FromJSON([]byte(doc)); err == nil || !strings.Contains(err.Error(), "cannot hold exactly") {
			t.Errorf("FromJSON(%s) = %v, want a refusal", doc, err)
		}
	}
	// Annotations hold no constraint, so their numbers may be anything.
	if _, err := FromJSON([]byte(`{"propertyNames":{"maxLength":3},"default":9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	// An annotation keyword is skipped only as a keyword of a schema: a property
	// named after one, or a key inside a literal, is still checked.
	for _, doc := range []string{
		`{"type":"object","properties":{"default":{"minimum":9007199254740993}},"propertyNames":{"maxLength":20}}`,
		`{"propertyNames":{"maxLength":20},"$defs":{"description":{"maximum":9007199254740993}}}`,
		`{"propertyNames":{"maxLength":20},"patternProperties":{"^title$":{"multipleOf":9007199254740993}}}`,
		`{"propertyNames":{"maxLength":20},"const":{"default":9007199254740993}}`,
		`{"propertyNames":{"maxLength":20},"enum":[{"examples":[9007199254740993]}]}`,
		`{"propertyNames":{"maxLength":20},"allOf":[{"properties":{"title":{"const":9007199254740993}}}]}`,
		`{"propertyNames":{"maxLength":20},"items":[{"default":1},{"maximum":9007199254740993}]}`,
		`{"propertyNames":{"maxLength":20},"x-limit":{"description":9007199254740993}}`,
	} {
		if _, err := FromJSON([]byte(doc)); err == nil || !strings.Contains(err.Error(), "cannot hold exactly") {
			t.Errorf("FromJSON(%s) = %v, want a refusal", doc, err)
		}
	}
	for _, doc := range []string{
		`{"propertyNames":{"maxLength":20},"properties":{"a":{"description":"x","default":9007199254740993,"examples":[9007199254740993]}}}`,
		`{"propertyNames":{"maxLength":20},"properties":{"default":{"minimum":1}}}`,
	} {
		if _, err := FromJSON([]byte(doc)); err != nil {
			t.Errorf("FromJSON(%s) = %v", doc, err)
		}
	}
	// The evaluator's case, end to end: an exact bound on a property named default.
	named, err := FromJSON([]byte(`{"type":"object","properties":{"default":{"minimum":9007199254740992}},"propertyNames":{"maxLength":20}}`))
	if err != nil {
		t.Fatal(err)
	}
	if is := named.Validate([]byte(`{"default":9007199254740991}`)); len(is) != 1 {
		t.Fatalf("issues = %v", is)
	}
}

// TestLocalRefs: a $ref into the document's own $defs or definitions, or to its
// root, is validated by this package, exactly and with every issue.
func TestLocalRefs(t *testing.T) {
	inline := `{"type":"integer"}`
	for _, doc := range []string{
		inline,
		`{"$defs":{"i":{"type":"integer"}},"$ref":"#/$defs/i"}`,
		`{"definitions":{"a/b":{"type":"integer"}},"$ref":"#/definitions/a~1b"}`,
		`{"$defs":{"i":{"$ref":"#/$defs/j"},"j":{"type":"integer"}},"$ref":"#/$defs/i"}`,
	} {
		typ, err := FromJSON([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if typ.fallback != nil {
			t.Errorf("%s uses the module's validator", doc)
		}
		for v, ok := range map[string]bool{`9007199254740992.5`: false, `9007199254740993`: true, `"x"`: false, `1e400`: true} {
			if is := typ.Validate([]byte(v)); (is == nil) != ok {
				t.Errorf("%s against %s: issues %v, want ok=%v", v, doc, is, ok)
			}
		}
	}

	// A recursive shape through the root.
	tree, err := FromJSON([]byte(`{"type":"object","properties":{"v":{"type":"integer"},"kids":{"type":"array","items":{"$ref":"#"}}},"additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	is := tree.Validate([]byte(`{"v":1,"kids":[{"v":2.5},{"kids":[{"x":1}]}]}`))
	if got := paths(is); !reflect.DeepEqual(got, []string{"/kids/0/v", "/kids/1/kids/0/x"}) {
		t.Fatalf("paths = %v (%v)", got, is)
	}

	// A cycle that never descends into the value ends.
	loop, err := FromJSON([]byte(`{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a","minimum":3}},"$ref":"#/$defs/a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if is := loop.Validate([]byte(`2`)); len(is) != 1 {
		t.Fatalf("issues = %v", is)
	}
	if is := loop.Validate([]byte(`3`)); is != nil {
		t.Fatalf("issues = %v", is)
	}

	// A reference the subset does not resolve itself goes to the module.
	for _, doc := range []string{`{"$defs":{"a":{"$defs":{"b":{}}}},"$ref":"#/$defs/a/$defs/b"}`} {
		typ, err := FromJSON([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if typ.fallback == nil {
			t.Errorf("%s does not use the module's validator", doc)
		}
	}
	if _, _, ok := localRef("#/$defs/a%25"); ok {
		t.Fatal("a percent-encoded ref is resolved by the subset")
	}
	if target(&jsonschema.Schema{}, "other.json") != nil {
		t.Fatal("a non-local ref resolved")
	}
	v := newValidator(&jsonschema.Schema{}, nil)
	v.check(&jsonschema.Schema{Ref: "#/$defs/gone"}, json.Number("1"), "")
	if len(v.issues) != 1 {
		t.Fatalf("a dangling ref: %v", v.issues)
	}
}

// TestDeclarationsAreExact: numbers a schema declares keep their precision through
// validation, the exported document and the fingerprint.
func TestDeclarationsAreExact(t *testing.T) {
	const big = "9007199254740993" // float64 holds 9007199254740992, not this
	for _, tc := range []struct {
		schema, value string
		ok            bool
	}{
		{`{"const":` + big + `}`, big, true},
		{`{"const":` + big + `}`, `9007199254740992`, false},
		{`{"enum":[1,` + big + `]}`, big, true},
		{`{"enum":[1,` + big + `]}`, `9007199254740992`, false},
		{`{"const":{"a":[` + big + `]}}`, `{"a":[9007199254740992]}`, false},
		{`{"maximum":` + big + `}`, big, true},
		{`{"maximum":9007199254740992}`, big, false},
		{`{"minimum":` + big + `}`, `9007199254740992`, false},
		{`{"exclusiveMinimum":` + big + `}`, big, false},
		{`{"exclusiveMaximum":` + big + `}`, `9007199254740992`, true},
		{`{"multipleOf":` + big + `}`, `18014398509481986`, true},
		{`{"multipleOf":` + big + `}`, `18014398509481984`, false},
		{`{"minimum":0.30000000000000000001}`, `0.3`, false},
		{`{"$defs":{"n":{"maximum":` + big + `}},"$ref":"#/$defs/n"}`, `9007199254740994`, false},
		{`{"items":{"minimum":` + big + `}}`, `[` + big + `]`, true},
	} {
		typ, err := FromJSON([]byte(tc.schema))
		if err != nil {
			t.Fatal(err)
		}
		if is := typ.Validate([]byte(tc.value)); (is == nil) != tc.ok {
			t.Errorf("%s against %s: issues %v, want ok=%v", tc.value, tc.schema, is, tc.ok)
		}
	}

	// The exported document carries the literal, and the fingerprint tells the
	// neighbours apart while ignoring spelling.
	fp := map[string]string{}
	for _, tc := range []struct {
		name, doc string
		exact     bool
	}{
		{"exact", `{"type":"object","properties":{"b":{"maximum":` + big + `},"a":{"enum":[` + big + `]}}}`, true},
		{"rounded", `{"type":"object","properties":{"b":{"maximum":9007199254740992},"a":{"enum":[9007199254740992]}}}`, false},
		{"respelled", `{"type":"object","properties":{"b":{"maximum":90071992547409930e-1},"a":{"enum":[9.007199254740993e15]}}}`, true},
	} {
		typ, err := FromJSON([]byte(tc.doc))
		if err != nil {
			t.Fatal(err)
		}
		out := string(typ.JSONSchema())
		fp[tc.name] = typ.Fingerprint()
		if tc.name == "exact" && strings.Count(out, big) != 2 {
			t.Errorf("JSONSchema() = %s, want both literals", out)
		}
		if !tc.exact && strings.Contains(out, big) {
			t.Errorf("JSONSchema() = %s", out)
		}
		round, err := FromJSON(typ.JSONSchema())
		if err != nil || round.Fingerprint() != typ.Fingerprint() {
			t.Errorf("re-reading %s: %v", out, err)
		}
	}
	if fp["exact"] != fp["respelled"] || fp["exact"] == fp["rounded"] {
		t.Fatalf("fingerprints = %v, want the exact pair equal and the rounded one distinct", fp)
	}

	// Tags on Go fields keep their literal too.
	type tagged struct {
		N int64 `json:"n" dsh:"enum=1|9007199254740993"`
		M int64 `json:"m" dsh:"min=-9007199254740993,max=9007199254740993"`
	}
	typ, err := Of[tagged]()
	if err != nil {
		t.Fatal(err)
	}
	for v, ok := range map[string]bool{
		`{"n":9007199254740993,"m":0}`:  true,
		`{"n":9007199254740992,"m":0}`:  false,
		`{"n":1,"m":9007199254740993}`:  true,
		`{"n":1,"m":9007199254740994}`:  false,
		`{"n":1,"m":-9007199254740994}`: false,
		`{"n":1,"m":-9007199254740993}`: true,
	} {
		if is := typ.Validate([]byte(v)); (is == nil) != ok {
			t.Errorf("%s: issues %v, want ok=%v", v, is, ok)
		}
	}
	if out := string(typ.JSONSchema()); strings.Count(out, big) != 3 {
		t.Errorf("JSONSchema() = %s", out)
	}
	type inverted struct {
		N int64 `json:"n" dsh:"min=9007199254740993,max=9007199254740992"`
	}
	if _, err := Of[inverted](); err == nil {
		t.Fatal("min above max by one was accepted")
	}
	type badEnum struct {
		N int `json:"n" dsh:"enum=1|x"`
	}
	if _, err := Of[badEnum](); err == nil {
		t.Fatal("a non-numeric enum was accepted")
	}
}

// TestHugeExponents: exponents are exact however large, so distinct numbers never
// collapse into one.
func TestHugeExponents(t *testing.T) {
	typ, err := FromJSON([]byte(`{"type":"array","uniqueItems":true}`))
	if err != nil {
		t.Fatal(err)
	}
	for v, ok := range map[string]bool{
		`[1e1099511627776,1e1099511627777]`:                                 true,
		`[1e1099511627776,10e1099511627775]`:                                false,
		`[1e-1099511627776,1e-1099511627777]`:                               true,
		`[1e99999999999999999999999999999,1e99999999999999999999999999998]`: true,
	} {
		if is := typ.Validate([]byte(v)); (is == nil) != ok {
			t.Errorf("%s: issues %v, want ok=%v", v, is, ok)
		}
	}
	a, _ := parseDecimal("1e1099511627776")
	b, _ := parseDecimal("1e1099511627777")
	c, _ := parseDecimal("-1e99999999999999999999999")
	if a.cmp(b) >= 0 || b.cmp(a) <= 0 || c.cmp(a) >= 0 || !b.multipleOf(a) || a.multipleOf(b) {
		t.Fatal("huge exponent arithmetic")
	}
	if max, err := FromJSON([]byte(`{"maximum":1e1099511627776}`)); err == nil {
		t.Fatalf("an out-of-float64 bound parsed: %s", max.JSONSchema())
	}
}

func TestPointerEscapes(t *testing.T) {
	typ, err := FromJSON([]byte(`{"type":"object","additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(typ.Validate([]byte(`{"a/b~c":1}`))); !reflect.DeepEqual(got, []string{"/a~1b~0c"}) {
		t.Fatalf("paths = %v", got)
	}
}

// opaque keeps its fields unexported and encodes as opaqueWire.
type opaque struct{ level int }

type opaqueWire struct {
	Level int `json:"level" dsh:"min=1,max=3"`
}

func (opaque) SchemaShape() reflect.Type { return reflect.TypeFor[opaqueWire]() }

func (o opaque) MarshalJSON() ([]byte, error) { return json.Marshal(opaqueWire{o.level}) }

// selfShaped's shape holds itself.
type selfShaped struct{}

type selfWire struct {
	Next []selfShaped `json:"next"`
}

func (selfShaped) SchemaShape() reflect.Type { return reflect.TypeFor[selfWire]() }

func TestShaper(t *testing.T) {
	direct, err := Of[opaque]()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Of[opaqueWire]()
	if err != nil {
		t.Fatal(err)
	}
	if direct.Fingerprint() != wire.Fingerprint() {
		t.Fatalf("shaped fingerprint %s, wire %s", direct.Fingerprint(), wire.Fingerprint())
	}
	if issues := direct.Validate(opaque{2}); len(issues) > 0 {
		t.Fatal(issues)
	}
	if issues := direct.Validate(opaque{7}); len(issues) != 1 || issues[0].Path != "/level" {
		t.Fatalf("the shape's dsh tag is not applied: %v", issues)
	}

	type holder struct {
		Many []opaque          `json:"many"`
		One  *opaque           `json:"one"`
		ByID map[string]opaque `json:"by_id"`
	}
	h, err := Of[holder]()
	if err != nil {
		t.Fatal(err)
	}
	if issues := h.Validate(holder{Many: []opaque{{1}}, One: &opaque{2}, ByID: map[string]opaque{"a": {3}}}); len(issues) > 0 {
		t.Fatal(issues)
	}
	issues := h.Validate(holder{Many: []opaque{{1}, {9}}, ByID: map[string]opaque{"a": {0}}})
	if got := issues.Error(); !strings.Contains(got, "/many/1/level") || !strings.Contains(got, "/by_id/a/level") {
		t.Fatalf("nested shape violations: %v", issues)
	}

	if _, err := Of[selfShaped](); err == nil || !strings.Contains(err.Error(), "reaches") {
		t.Fatalf("a shape that reaches itself: %v", err)
	}
}
