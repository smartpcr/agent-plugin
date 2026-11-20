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
	typ, err := FromJSON([]byte(`{"$defs":{"n":{"type":"integer"}},"type":"object","properties":{"a":{"$ref":"#/$defs/n"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if typ.fallback == nil {
		t.Fatal("a $ref document should use the module's validator")
	}
	if is := typ.Validate([]byte(`{"a":"x"}`)); len(is) != 1 {
		t.Fatalf("issues = %v", is)
	}
	if is := typ.Validate([]byte(`{"a":1,"b":[1.5,{"c":2}]}`)); is != nil {
		t.Fatalf("issues = %v", is)
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
