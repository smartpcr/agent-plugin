package schema

import (
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// timeoutConfig is the duration scenario's config type.
type timeoutConfig struct {
	Timeout time.Duration `json:"timeout"`
}

// TestDurationRoundTrip is the stage scenario "A duration round-trips as text"
// (plan 14.4.1): a time.Duration derives to a string, its default encodes as
// "30s", "45s" decodes onto the default as 45 s, and "45" is an issue.
//
//dsh:test DH-T-UTIL-2
func TestDurationRoundTrip(t *testing.T) {
	typ := MustOf[timeoutConfig]()
	var doc struct {
		Properties map[string]struct {
			Type    string `json:"type"`
			Pattern string `json:"pattern"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(typ.JSONSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	if p := doc.Properties["timeout"]; p.Type != "string" || p.Pattern != DurationPattern {
		t.Fatalf("timeout derives to %+v, want a string with the duration pattern", p)
	}
	def := timeoutConfig{Timeout: 30 * time.Second}
	if b, err := Marshal(def); err != nil || string(b) != `{"timeout":"30s"}` {
		t.Fatalf("encoded default = %s, %v", b, err)
	}
	if is := typ.Validate(def); is != nil {
		t.Fatalf("the default does not validate: %v", is)
	}
	got := def
	if is := typ.Decode([]byte(`{"timeout":"45s"}`), &got); is != nil || got.Timeout != 45*time.Second {
		t.Fatalf("Decode 45s = %v, %v", got.Timeout, is)
	}
	for in, want := range map[string]string{
		`{"timeout":"45"}`:           "/timeout: must match",
		`{"timeout":45}`:             "/timeout: must be string, not number",
		`{"timeout":"99999999999h"}`: "/timeout: is not a duration",
		`{"timeout":null}`:           "/timeout: must be string, not null",
	} {
		is := typ.Decode([]byte(in), &got)
		if len(is) != 1 || !strings.HasPrefix(is[0].String(), want) {
			t.Errorf("Decode(%s) = %v, want one issue %q", in, is, want)
		}
	}
	if is := typ.Decode([]byte(`{"timeout":"1h30m"}`), &got); is != nil || got.Timeout != 90*time.Minute {
		t.Fatalf("Decode 1h30m = %v, %v", got.Timeout, is)
	}
}

// nestedDurations holds durations where Marshal and Unmarshal must find them, and
// where they must leave them to the type.
type nestedDurations struct {
	Ptr   *time.Duration           `json:"ptr,omitempty"`
	List  []time.Duration          `json:"list"`
	ByKey map[string]time.Duration `json:"by_key"`
	Inner struct {
		Wait time.Duration `json:"wait"`
	} `json:"inner"`
	Self Duration `json:"self"`
	Any  any      `json:"any,omitempty"`
	Skip string   `json:"-"`
	embed
}

type embed struct {
	Promoted time.Duration `json:"promoted"`
}

// Durations are text at every depth Marshal and Unmarshal reach; a type that
// codes itself and an interface value are left alone; numbers are refused.
//
//dsh:test DH-T-UTIL-2
func TestDurationsAtEveryDepth(t *testing.T) {
	d := time.Second
	v := nestedDurations{Ptr: &d, List: []time.Duration{time.Millisecond}, ByKey: map[string]time.Duration{"k": time.Minute},
		Self: Duration(2 * time.Hour), Any: time.Duration(7), embed: embed{Promoted: 3 * time.Second}}
	v.Inner.Wait = 1500 * time.Millisecond
	b, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ptr":"1s","list":["1ms"],"by_key":{"k":"1m0s"},"inner":{"wait":"1.5s"},"self":"2h0m0s","any":7,"promoted":"3s"}`
	if string(b) != want {
		t.Fatalf("Marshal = %s\nwant      %s", b, want)
	}
	var back nestedDurations
	if err := Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.Any = v.Any // decoded as a float64 by encoding/json; not a duration
	if !reflect.DeepEqual(back, v) {
		t.Fatalf("Unmarshal = %+v, want %+v", back, v)
	}
	typ := MustOf[nestedDurations]()
	if is := typ.Validate(v); is != nil {
		t.Fatalf("Validate = %v", is)
	}
	var dec nestedDurations
	if is := typ.Decode(b, &dec); is != nil || dec.Inner.Wait != v.Inner.Wait || dec.Promoted != v.Promoted {
		t.Fatalf("Decode = %+v, %v", dec, is)
	}
	for in, want := range map[string]string{
		`{"list":[1],"by_key":{},"inner":{"wait":"1s"},"self":"1s","promoted":"1s"}`:       "/list/0",
		`{"list":[],"by_key":{"k":"x"},"inner":{"wait":"1s"},"self":"1s","promoted":"1s"}`: "/by_key/k",
		`{"list":[],"by_key":{},"inner":{"wait":"1s"},"self":5,"promoted":"1s"}`:           "not 5",
		`not json`: "not JSON",
	} {
		var x nestedDurations
		if err := Unmarshal([]byte(in), &x); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Unmarshal(%s) = %v, want an error naming %s", in, err, want)
		}
	}
	var self Duration
	if err := json.Unmarshal([]byte(`"bad"`), &self); err == nil {
		t.Error("Duration accepted bad text")
	}
	if self.String() != "0s" {
		t.Errorf("Duration.String = %s", self)
	}
	// No duration at all: the helpers are encoding/json.
	if b, err := Marshal(map[string]int{"a": 1}); err != nil || string(b) != `{"a":1}` {
		t.Fatalf("Marshal without durations = %s, %v", b, err)
	}
	if b, err := Marshal(nil); err != nil || string(b) != "null" {
		t.Fatalf("Marshal(nil) = %s, %v", b, err)
	}
	var m map[string]int
	if err := Unmarshal([]byte(`{"a":1}`), &m); err != nil || m["a"] != 1 {
		t.Fatalf("Unmarshal without durations = %v, %v", m, err)
	}
	if _, err := Marshal(func() {}); err == nil {
		t.Fatal("Marshal of a func succeeded")
	}
	type bounded struct {
		D time.Duration `json:"d" dsh:"min=1"`
	}
	if _, err := Of[bounded](); err == nil || !strings.Contains(err.Error(), "do not apply to a duration") {
		t.Fatalf("min= on a duration: %v", err)
	}
	type patterned struct {
		D time.Duration `json:"d" dsh:"pattern=^1s$"`
	}
	if _, err := Of[patterned](); err == nil {
		t.Fatal("pattern= on a duration was accepted")
	}
	type enumerated struct {
		D time.Duration `json:"d" dsh:"enum=1s|2s"`
	}
	en := MustOf[enumerated]()
	if is := en.Validate([]byte(`{"d":"3s"}`)); len(is) != 1 {
		t.Fatalf("enum of durations = %v", is)
	}
}

// Shape is the union scenario's interface; frameA, frameB and *frameC are its
// variants.
type shape interface{ isShape() }

type frameA struct {
	Kind string `json:"kind"`
	N    int    `json:"n" dsh:"min=0"`
}

type frameB struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type frameC struct {
	Kind  string        `json:"kind"`
	Every time.Duration `json:"every"`
}

func (frameA) isShape()  {}
func (frameB) isShape()  {}
func (*frameC) isShape() {}

func (c *frameC) Validate() error {
	if c.Every <= 0 {
		return Issues{{Path: "/every", Message: "must be positive"}}
	}
	return nil
}

// TestUnion is the stage scenario "Three-variant union" (DH-T-ZOD-8): the
// document is a oneOf with one const per variant, each input becomes its Go type,
// and an unknown discriminator is one issue.
//
//dsh:test DH-T-ZOD-8
func TestUnion(t *testing.T) {
	u, err := Union[shape]("kind", VariantOf[frameA]("a"), VariantOf[frameB]("b"), VariantOf[*frameC]("c"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OneOf []struct {
			Properties map[string]struct {
				Const any `json:"const"`
			} `json:"properties"`
			Required []string `json:"required"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(u.JSONSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	var consts []any
	for _, alt := range doc.OneOf {
		consts = append(consts, alt.Properties["kind"].Const)
		if !slices.Contains(alt.Required, "kind") {
			t.Errorf("an alternative does not require kind: %+v", alt)
		}
	}
	if !reflect.DeepEqual(consts, []any{"a", "b", "c"}) {
		t.Fatalf("oneOf consts = %v", consts)
	}
	again := MustUnion[shape]("kind", VariantOf[frameA]("a"), VariantOf[frameB]("b"), VariantOf[*frameC]("c"))
	if again.Fingerprint() != u.Fingerprint() {
		t.Fatal("deriving the union twice changed its fingerprint")
	}

	var s shape
	if is := u.Decode([]byte(`{"kind":"a","n":3}`), &s); is != nil || s != (frameA{Kind: "a", N: 3}) {
		t.Fatalf("a = %#v, %v", s, is)
	}
	if is := u.Decode([]byte(`{"kind":"b","text":"hi"}`), &s); is != nil || s != (frameB{Kind: "b", Text: "hi"}) {
		t.Fatalf("b = %#v, %v", s, is)
	}
	if is := u.Decode([]byte(`{"kind":"c","every":"5s"}`), &s); is != nil {
		t.Fatalf("c: %v", is)
	} else if c, ok := s.(*frameC); !ok || c.Every != 5*time.Second {
		t.Fatalf("c = %#v", s)
	}

	for in, want := range map[string]string{
		`{"kind":"d","n":1}`:           `/kind: must be one of ["a","b","c"]`,
		`{"kind":1}`:                   `/kind: must be one of ["a","b","c"]`,
		`{"n":1}`:                      `/kind: is required`,
		`[]`:                           `/: must be object, not array`,
		`{"kind":"a","n":-1}`:          `/n: must be at least 0`,
		`{"kind":"a","n":1,"text":""}`: `/text: is not a known field`,
		`{"kind":"c","every":"0s"}`:    `/every: must be positive`,
		`{"kind":"c","every":"x"}`:     `/every: must match`,
		`{"kind":"a"} x`:               `not JSON`,
	} {
		is := u.Decode([]byte(in), &s)
		if len(is) != 1 || !strings.Contains(is[0].String(), want) {
			t.Errorf("Decode(%s) = %v, want one issue %q", in, is, want)
		}
	}
	if is := u.Validate([]byte(`{"kind":"d"}`)); len(is) != 1 || is[0].Path != "/kind" {
		t.Fatalf("Validate kind d = %v", is)
	}
	if is := u.Validate(frameB{Kind: "b", Text: "x"}); is != nil {
		t.Fatalf("Validate a Go variant = %v", is)
	}
	var wrong int
	for _, dst := range []any{&wrong, nil, s} {
		if is := u.Decode([]byte(`{"kind":"a","n":1}`), dst); len(is) != 1 || !strings.Contains(is[0].Message, "does not decode") {
			t.Errorf("Decode into %T = %v", dst, is)
		}
	}
	var anything any
	if is := u.Decode([]byte(`{"kind":"a","n":1}`), &anything); is != nil || anything != (frameA{Kind: "a", N: 1}) {
		t.Fatalf("Decode into *any = %#v, %v", anything, is)
	}

	type noKind struct {
		N int `json:"n"`
	}
	type numKind struct {
		Kind int `json:"kind"`
	}
	for name, build := range map[string]func() (*Type, error){
		"blank discriminator": func() (*Type, error) { return Union[shape]("", VariantOf[frameA]("a")) },
		"no variants":         func() (*Type, error) { return Union[shape]("kind") },
		"blank value":         func() (*Type, error) { return Union[shape]("kind", VariantOf[frameA]("")) },
		"repeated value":      func() (*Type, error) { return Union[shape]("kind", VariantOf[frameA]("a"), VariantOf[frameB]("a")) },
		"no type":             func() (*Type, error) { return Union[shape]("kind", Variant{Value: "a"}) },
		"not assignable":      func() (*Type, error) { return Union[shape]("kind", VariantOf[frameC]("c")) },
		"not a struct":        func() (*Type, error) { return Union[any]("kind", VariantOf[int]("i")) },
		"no discriminator":    func() (*Type, error) { return Union[any]("kind", VariantOf[noKind]("x")) },
		"number discriminator": func() (*Type, error) {
			return Union[any]("kind", VariantOf[numKind]("x"))
		},
		"bad tag": func() (*Type, error) {
			return Union[any]("kind", VariantOf[struct {
				Kind string `json:"kind" dsh:"nope"`
			}]("x"))
		},
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("MustUnion of a bad union did not panic")
		}
	}()
	MustUnion[shape]("")
}

type record struct {
	Name  string `json:"name"`
	Inner struct {
		A int `json:"a"`
	} `json:"inner"`
	List []struct {
		B string `json:"b"`
	} `json:"list,omitempty"`
	Bag  map[string]any `json:"bag,omitempty"`
	Wait time.Duration  `json:"wait,omitempty"`
}

// TestTolerant is DH-T-ZOD-5: strict decoding refuses unknown fields and
// trailing data; a Tolerant type accepts unknown fields at any depth and keeps
// the known ones, while its document, fingerprint and Validate stay closed.
//
//dsh:test DH-T-ZOD-5
func TestTolerant(t *testing.T) {
	strict := MustOf[record]()
	tolerant := MustOf[record](Tolerant())
	newer := `{"name":"x","added":1,"inner":{"a":2,"later":true},"list":[{"b":"y","more":[]}],"bag":{"free":1},"wait":"2s"}`
	var r record
	if is := strict.Decode([]byte(newer), &r); len(is) != 3 {
		t.Fatalf("strict = %v, want the three unknown fields", is)
	}
	if is := strict.Decode([]byte(`{"name":"x","inner":{"a":1}} {}`), &r); len(is) != 1 || !strings.Contains(is[0].Message, "data after the value") {
		t.Fatalf("strict trailing data = %v", is)
	}
	r = record{}
	if is := tolerant.Decode([]byte(newer), &r); is != nil {
		t.Fatalf("tolerant = %v", is)
	}
	if r.Name != "x" || r.Inner.A != 2 || len(r.List) != 1 || r.List[0].B != "y" || r.Bag["free"] == nil || r.Wait != 2*time.Second {
		t.Fatalf("tolerant kept %+v", r)
	}
	if is := tolerant.Decode([]byte(`{"name":"x","inner":{"a":"two"},"added":1}`), &r); len(is) != 1 || is[0].Path != "/inner/a" {
		t.Fatalf("tolerant still checks the known fields: %v", is)
	}
	if is := tolerant.Decode([]byte(`{"name":"x","inner":{"a":1}} {}`), &r); len(is) != 1 {
		t.Fatalf("tolerant trailing data = %v", is)
	}
	if tolerant.Fingerprint() != strict.Fingerprint() || string(tolerant.JSONSchema()) != string(strict.JSONSchema()) {
		t.Fatal("Tolerant changed the shape")
	}
	if is := tolerant.Validate([]byte(newer)); len(is) != 3 {
		t.Fatalf("tolerant Validate = %v, want the closed shape's three issues", is)
	}

	// A union of closed variants prunes through the variant its discriminator
	// selects; a $ref is followed.
	doc := `{"$defs":{"item":{"type":"object","properties":{"v":{"type":"integer"}},"additionalProperties":false}},
		"type":"object","properties":{"items":{"type":"array","items":{"$ref":"#/$defs/item"}},
		"pick":{"oneOf":[
			{"type":"object","properties":{"kind":{"const":"a"},"x":{"type":"integer"}},"additionalProperties":false},
			{"type":"object","properties":{"kind":{"const":"b"}},"additionalProperties":false}]}},
		"additionalProperties":false}`
	ft, err := FromJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	ft.tolerant = true
	var out map[string]any
	if is := ft.Decode([]byte(`{"items":[{"v":1,"w":2}],"pick":{"kind":"a","x":1,"y":2},"zz":0}`), &out); is != nil {
		t.Fatalf("tolerant document = %v", is)
	}
	if b, _ := json.Marshal(out); string(b) != `{"items":[{"v":1}],"pick":{"kind":"a","x":1}}` {
		t.Fatalf("pruned to %s", b)
	}
	if is := ft.Decode([]byte(`{"pick":{"kind":"z","x":1}}`), &out); len(is) == 0 {
		t.Fatal("an alternative no discriminator selects was accepted")
	}
}

// TestClosedOpenAndSubset is DH-T-UTIL-1: the root is closed by default and Open
// admits unknown keys; CheckSubset lists every offending path; Decode returns two
// issues for two errors.
//
//dsh:test DH-T-UTIL-1, DH-T-ZOD-3
func TestClosedOpenAndSubset(t *testing.T) {
	var r record
	if is := MustOf[record]().Decode([]byte(`{"name":"x","inner":{"a":1},"extra":1}`), &r); len(is) != 1 || is[0].Path != "/extra" {
		t.Fatalf("closed root = %v", is)
	}
	if is := MustOf[record](Open()).Decode([]byte(`{"name":"x","inner":{"a":1},"extra":1}`), &r); is != nil {
		t.Fatalf("open root = %v", is)
	}
	if is := MustOf[record]().Decode([]byte(`{"name":1,"inner":{"a":"x"}}`), &r); len(is) != 2 {
		t.Fatalf("two errors = %v", is)
	}

	model := Subset{
		Keywords:          []string{"type", "properties", "required", "additionalProperties", "items", "enum", "const", "oneOf", "description"},
		SingleType:        true,
		BooleanAdditional: true,
		ScalarLiterals:    true,
	}
	doc := `{"type":"object","not":{},"$defs":{"x":{}},"properties":{
		"a":{"type":["string","null"],"format":"uri"},
		"b":{"type":"object","additionalProperties":{"type":"string"}},
		"c":{"enum":[{"k":1}],"const":[1]},
		"d":{"oneOf":[{"type":"string"},{"pattern":"x"}]},
		"e":{"type":"array","items":true},
		"g~/h":{"anyOf":[]}}}`
	typ, err := FromJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/$defs: the keyword is not admitted",
		"/not: the keyword is not admitted",
		"/properties/a/format: the keyword is not admitted",
		"/properties/a/type: must be one type name",
		"/properties/b/additionalProperties: must be true or false",
		"/properties/c/const: must be a scalar",
		"/properties/c/enum: must be a list of scalars",
		"/properties/d/oneOf/1/pattern: the keyword is not admitted",
		"/properties/e/items: a schema must be an object",
		"/properties/g~0~1h/anyOf: the keyword is not admitted",
	}
	var got []string
	for _, is := range typ.CheckSubset(model) {
		got = append(got, is.String())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("CheckSubset =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Values the module would refuse to load are still judged, as supplied.
	raw := &Type{src: []byte(`{"required":[1],"properties":[],"oneOf":{},"type":[1],"enum":1}`)}
	want = []string{
		"/enum: must be a list of scalars",
		"/oneOf: must be a list of schemas",
		"/properties: must map names to schemas",
		"/required: must be a list of names",
		"/type: must be one type name",
	}
	got = nil
	for _, is := range raw.CheckSubset(model) {
		got = append(got, is.String())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("CheckSubset of raw values = %v", got)
	}
	if is := (&Type{src: []byte(`{"type":[1]}`)}).CheckSubset(Subset{Keywords: []string{"type"}}); len(is) != 1 || is[0].Path != "/type" {
		t.Fatalf("type list of numbers = %v", is)
	}
	if is := (&Type{src: []byte(`{`)}).CheckSubset(model); len(is) != 1 || is[0].Path != "" {
		t.Fatalf("CheckSubset of a broken document = %v", is)
	}

	// A wider subset: a type list, a schema for additionalProperties, any literal.
	wide := Subset{Keywords: []string{"type", "properties", "additionalProperties", "enum", "const", "items", "pattern", "required"}}
	ok := `{"type":"object","properties":{"a":{"type":["string","null"],"enum":[{"k":1}],"const":[1]},
		"b":{"additionalProperties":{"type":"string","format":"x"}}}}`
	wt, err := FromJSON([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if is := wt.CheckSubset(wide); len(is) != 1 || is[0].Path != "/properties/b/additionalProperties/format" {
		t.Fatalf("wide subset = %v", is)
	}
	// A derived type is checked through its derived document.
	if is := MustOf[frameB]().CheckSubset(model); len(is) != 0 {
		t.Fatalf("a derived type leaves the model subset: %v", is)
	}
	if is := MustOf[frameA]().CheckSubset(Subset{Keywords: []string{"type"}}); len(is) == 0 {
		t.Fatal("a narrow subset admitted a derived object")
	}
}

type liveConfig struct {
	Level string `json:"level" dsh:"live"`
	Port  int    `json:"port"`
	Inner struct {
		Depth int `json:"depth"`
	} `json:"inner" dsh:"live,ui=group"`
	Ptr *struct {
		Key string `json:"key" dsh:"secret"`
	} `json:"ptr,omitempty"`
	List []int `json:"list,omitempty"`
}

// TestFieldsAndChanged is the stage scenario "Live-only change" (DH-T-UTIL-2,
// Changed clause): Fields lists every property with its metadata, and Changed
// reports the changed paths and whether all of them are live.
//
//dsh:test DH-T-UTIL-2
func TestFieldsAndChanged(t *testing.T) {
	typ := MustOf[liveConfig]()
	var fields []Field
	for f := range typ.Fields() {
		fields = append(fields, f)
	}
	want := []Field{
		{Path: "/level", Kind: "string", Required: true, Live: true},
		{Path: "/port", Kind: "integer", Required: true},
		{Path: "/inner", Kind: "object", Required: true, Live: true, UI: "group"},
		{Path: "/inner/depth", Kind: "integer", Required: true},
		{Path: "/ptr", Kind: "object", Nullable: true},
		{Path: "/ptr/key", Kind: "string", Required: true, Secret: true},
		{Path: "/list", Kind: "array", Nullable: true},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("Fields =\n%+v\nwant\n%+v", fields, want)
	}
	n := 0
	for range typ.Fields() {
		if n++; n == 2 {
			break
		}
	}

	base := liveConfig{Level: "info", Port: 80}
	live := base
	live.Level = "debug"
	if paths, liveOnly := Changed(typ, base, live); !slices.Equal(paths, []string{"/level"}) || !liveOnly {
		t.Fatalf("live change = %v, %v", paths, liveOnly)
	}
	both := live
	both.Port = 81
	if paths, liveOnly := Changed(typ, base, both); !slices.Equal(paths, []string{"/level", "/port"}) || liveOnly {
		t.Fatalf("both = %v, %v", paths, liveOnly)
	}
	under := base
	under.Inner.Depth = 3
	if paths, liveOnly := Changed(typ, base, under); !slices.Equal(paths, []string{"/inner/depth"}) || !liveOnly {
		t.Fatalf("below a live field = %v, %v", paths, liveOnly)
	}
	ptr := base
	ptr.Ptr = &struct {
		Key string `json:"key" dsh:"secret"`
	}{Key: "k"}
	if paths, liveOnly := Changed(typ, base, ptr); !slices.Equal(paths, []string{"/ptr"}) || liveOnly {
		t.Fatalf("nil to set pointer = %v, %v", paths, liveOnly)
	}
	list := base
	list.List = []int{1}
	if paths, _ := Changed(typ, base, list); !slices.Equal(paths, []string{"/list"}) {
		t.Fatalf("list = %v", paths)
	}
	if paths, liveOnly := Changed(typ, base, base); paths != nil || !liveOnly {
		t.Fatalf("no change = %v, %v", paths, liveOnly)
	}
	if paths, liveOnly := Changed[any](typ, func() {}, base); !slices.Equal(paths, []string{""}) || liveOnly {
		t.Fatalf("unencodable = %v, %v", paths, liveOnly)
	}
	doc, err := FromJSON([]byte(`{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for f := range doc.Fields() {
		names = append(names, f.Path)
	}
	if !slices.Equal(names, []string{"/a", "/z"}) {
		t.Fatalf("a document's fields = %v, want them sorted", names)
	}
}

type formInput struct {
	Name  string        `json:"name"`
	Count int           `json:"count,omitempty"`
	Ratio float64       `json:"ratio,omitempty"`
	On    bool          `json:"on,omitempty"`
	Tags  []string      `json:"tags,omitempty"`
	Nums  []int         `json:"nums,omitempty"`
	Wait  time.Duration `json:"wait,omitempty"`
	Inner struct {
		Depth int `json:"depth"`
	} `json:"inner"`
}

// TestDecodeForm is DH-T-UTIL-2 (DecodeForm clause): a form body decodes with the
// same checks as JSON; an unknown field and a wrong type are refused.
//
//dsh:test DH-T-UTIL-2
func TestDecodeForm(t *testing.T) {
	typ := MustOf[formInput]()
	in := formInput{Count: 7}
	in.Inner.Depth = 1
	form := url.Values{"name": {"x"}, "ratio": {"0.5"}, "on": {"on"}, "tags": {"a", "b"}, "nums": {"1", "2"},
		"wait": {"2s"}, "inner.depth": {"4"}, "count": {""}}
	if is := typ.DecodeForm(form, &in); is != nil {
		t.Fatalf("DecodeForm = %v", is)
	}
	if in.Name != "x" || in.Count != 7 || in.Ratio != 0.5 || !in.On || !slices.Equal(in.Tags, []string{"a", "b"}) ||
		!slices.Equal(in.Nums, []int{1, 2}) || in.Wait != 2*time.Second || in.Inner.Depth != 4 {
		t.Fatalf("decoded %+v", in)
	}
	if is := typ.DecodeForm(url.Values{"name": {"x"}, "inner.depth": {"1"}, "on": {"false"}}, &in); is != nil || in.On {
		t.Fatalf("false checkbox = %v, %v", in.On, is)
	}
	for name, tc := range map[string]struct {
		form url.Values
		path string
	}{
		"unknown field":         {url.Values{"name": {"x"}, "inner.depth": {"1"}, "extra": {"1"}}, "/extra"},
		"unknown nested":        {url.Values{"name": {"x"}, "inner.depth": {"1"}, "inner.more": {"1"}}, "/inner/more"},
		"wrong type":            {url.Values{"name": {"x"}, "inner.depth": {"one"}}, "/inner/depth"},
		"wrong boolean":         {url.Values{"name": {"x"}, "inner.depth": {"1"}, "on": {"yes"}}, "/on"},
		"two values for one":    {url.Values{"name": {"x", "y"}, "inner.depth": {"1"}}, "/name"},
		"bad duration":          {url.Values{"name": {"x"}, "inner.depth": {"1"}, "wait": {"2"}}, "/wait"},
		"bad item":              {url.Values{"name": {"x"}, "inner.depth": {"1"}, "nums": {"1", "x"}}, "/nums/1"},
		"missing required name": {url.Values{"inner.depth": {"1"}}, "/name"},
	} {
		var x formInput
		is := typ.DecodeForm(tc.form, &x)
		if len(is) != 1 || is[0].Path != tc.path {
			t.Errorf("%s: %v, want one issue at %s", name, is, tc.path)
		}
	}
	u := MustUnion[shape]("kind", VariantOf[frameA]("a"), VariantOf[frameB]("b"))
	var s shape
	if is := u.DecodeForm(url.Values{"kind": {"a"}, "n": {"3"}}, &s); is != nil || s != (frameA{Kind: "a", N: 3}) {
		t.Fatalf("union form = %#v, %v", s, is)
	}
	if is := u.DecodeForm(url.Values{"kind": {"z"}}, &s); len(is) != 1 || is[0].Path != "/kind" {
		t.Fatalf("union form, unknown kind = %v", is)
	}
}

// TestProject is DH-T-UTIL-2 (Project clause), with a fake profile: Project
// removes exactly the profile's keywords, wherever they are keywords, and lists
// each loss.
//
//dsh:test DH-T-UTIL-2
func TestProject(t *testing.T) {
	doc := `{"type":"object","properties":{"pattern":{"type":"string","pattern":"^a","minLength":1},` +
		`"n":{"type":"integer","minimum":0,"default":{"minimum":1}},"e":{"enum":["pattern","minimum"]},` +
		`"xs":{"type":"array","items":{"type":"string","pattern":"b"}},"o":{"oneOf":[{"minimum":2},{"type":"string"}]}},` +
		`"$defs":{"d":{"pattern":"c"}},"required":["pattern"]}`
	typ, err := FromJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	fake := Profile{Name: "fake", Drop: map[string]string{"pattern": "regex dialect differs", "minimum": "bounds ignored"}}
	got, losses := typ.Project(fake)
	want := `{"type":"object","properties":{"pattern":{"type":"string","minLength":1},` +
		`"n":{"type":"integer","default":{"minimum":1}},"e":{"enum":["pattern","minimum"]},` +
		`"xs":{"type":"array","items":{"type":"string"}},"o":{"oneOf":[{},{"type":"string"}]}},` +
		`"$defs":{"d":{}},"required":["pattern"]}`
	if string(got) != want {
		t.Fatalf("Project =\n%s\nwant\n%s", got, want)
	}
	wantLoss := []Loss{
		{Path: "/properties/pattern", Keyword: "pattern", Reason: "regex dialect differs"},
		{Path: "/properties/n", Keyword: "minimum", Reason: "bounds ignored"},
		{Path: "/properties/xs/items", Keyword: "pattern", Reason: "regex dialect differs"},
		{Path: "/properties/o/oneOf/0", Keyword: "minimum", Reason: "bounds ignored"},
		{Path: "/$defs/d", Keyword: "pattern", Reason: "regex dialect differs"},
	}
	if !reflect.DeepEqual(losses, wantLoss) {
		t.Fatalf("losses = %+v", losses)
	}
	if got, losses := typ.Project(Profile{Name: "all"}); string(got) != doc || losses != nil {
		t.Fatalf("an empty profile changed the document: %s %v", got, losses)
	}
	whole, losses := MustOf[liveConfig]().Project(Profile{Drop: map[string]string{"properties": "flat"}})
	if strings.Contains(string(whole), `"properties"`) || len(losses) != 1 || losses[0].Path != "" {
		t.Fatalf("dropping properties = %s %v", whole, losses)
	}
}

// TestLocate is DH-T-ZOD-4's report made positional: an issue's path is located
// in the input it came from, by line and column.
//
//dsh:test DH-T-ZOD-4
func TestLocate(t *testing.T) {
	raw := "{\n  \"name\": \"x\",\n  \"inner\": {\"a\": [1, {\"b~/c\": true}]},\n  \"é\": 1, \"dup\": 1, \"dup\": [ 2 ]\n}"
	for path, want := range map[string][2]int{
		"":                  {1, 1},
		"/name":             {2, 11},
		"/inner":            {3, 12},
		"/inner/a":          {3, 18},
		"/inner/a/0":        {3, 19},
		"/inner/a/1":        {3, 22},
		"/inner/a/1/b~0~1c": {3, 31},
		"/é":                {4, 8},
		"/dup":              {4, 28},
		"/dup/0":            {4, 30},
	} {
		line, col, ok := Locate([]byte(raw), path)
		if !ok || line != want[0] || col != want[1] {
			t.Errorf("Locate(%q) = %d:%d %v, want %d:%d", path, line, col, ok, want[0], want[1])
		}
	}
	for _, path := range []string{"/missing", "/inner/a/2", "/inner/a/x", "/inner/a/01", "/name/x", "nope"} {
		if _, _, ok := Locate([]byte(raw), path); ok {
			t.Errorf("Locate(%q) found something", path)
		}
	}
	for _, bad := range []string{`{"a":`, `{"a":1} 2`, `{"a":[1,}`, ``} {
		if _, _, ok := Locate([]byte(bad), "/a"); ok {
			t.Errorf("Locate in %q found /a", bad)
		}
	}
	if _, _, ok := Locate([]byte(`{"a":1} 2`), ""); ok {
		t.Error("Locate of the root of two values succeeded")
	}
	// An issue from Decode points at the offending value.
	var r record
	in := []byte("{\"name\": \"x\",\n \"inner\": {\"a\": \"two\"}}")
	is := MustOf[record]().Decode(in, &r)
	if len(is) != 1 {
		t.Fatalf("Decode = %v", is)
	}
	if line, col, ok := Locate(in, is[0].Path); !ok || line != 2 || col != 17 {
		t.Fatalf("issue %v located at %d:%d %v", is[0], line, col, ok)
	}
}
