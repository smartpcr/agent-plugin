package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type listenConfig struct {
	Host  string        `json:"host,omitempty"`
	Port  int           `json:"port,omitempty" dsh:"min=1,max=65535"`
	Mode  string        `json:"mode,omitempty" dsh:"enum=fast|safe"`
	Wait  time.Duration `json:"wait,omitempty"`
	Inner struct {
		Depth int `json:"depth,omitempty" dsh:"max=9"`
	} `json:"inner"`
}

func listenDefaults() listenConfig {
	c := listenConfig{Host: "localhost", Port: 3080, Mode: "safe", Wait: 30 * time.Second}
	c.Inner.Depth = 2
	return c
}

// TestDefaults is DH-T-ZOD-6: partial input decodes onto the defaults and absent
// fields keep them; the document's default annotations equal the Go value; a
// default outside its own bounds fails the derivation.
//
//dsh:test DH-T-ZOD-6
func TestDefaults(t *testing.T) {
	def := listenDefaults()
	typ, err := Of[listenConfig](Defaults(def))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			Default    json.RawMessage `json:"default"`
			Properties map[string]struct {
				Default json.RawMessage `json:"default"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(typ.JSONSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	enc, err := Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]json.RawMessage
	if err := json.Unmarshal(enc, &want); err != nil {
		t.Fatal(err)
	}
	for name, raw := range want {
		if name == "inner" {
			continue
		}
		if got := doc.Properties[name].Default; string(got) != string(raw) {
			t.Errorf("default of %s = %s, want the Go value %s", name, got, raw)
		}
	}
	if got := string(doc.Properties["wait"].Default); got != `"30s"` {
		t.Errorf("default of wait = %s, want \"30s\"", got)
	}
	if got := string(doc.Properties["inner"].Properties["depth"].Default); got != "2" {
		t.Errorf("default of inner.depth = %s, want 2", got)
	}
	if typ.Fingerprint() != MustOf[listenConfig]().Fingerprint() {
		t.Error("default annotations changed the fingerprint")
	}

	got := listenDefaults()
	if is := typ.Decode([]byte(`{"port":8080,"inner":{}}`), &got); is != nil {
		t.Fatalf("Decode = %v", is)
	}
	want2 := listenDefaults()
	want2.Port = 8080
	if got != want2 {
		t.Fatalf("partial input = %+v, want %+v", got, want2)
	}

	for name, bad := range map[string]listenConfig{
		"above max":     {Port: 70000},
		"not in enum":   {Port: 1, Mode: "slow"},
		"nested bound":  func() listenConfig { c := listenDefaults(); c.Inner.Depth = 10; return c }(),
		"below min (0)": {Port: -1},
	} {
		if _, err := Of[listenConfig](Defaults(bad)); err == nil || !strings.Contains(err.Error(), "a default breaks the shape") {
			t.Errorf("%s: derivation = %v, want a refusal", name, err)
		}
	}
	if _, err := Of[listenConfig](Defaults(42)); err == nil {
		t.Error("defaults of another type were accepted")
	}
	if _, err := Of[struct {
		F func() `json:"f,omitempty"`
	}](Defaults(struct {
		F func() `json:"f,omitempty"`
	}{})); err == nil {
		t.Error("a type that does not derive was accepted with defaults")
	}
	open, err := Of[listenConfig](Defaults(def), Open(), Tolerant())
	if err != nil || !open.open || !open.tolerant {
		t.Fatalf("Defaults with Open and Tolerant = %+v, %v", open, err)
	}
}

// routeName is a defined id type: its text is checked while it is decoded.
type routeName string

func (r *routeName) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" || strings.ContainsAny(s, " /") {
		return errors.New("route name " + strconvQuote(s) + " is malformed")
	}
	*r = routeName(s)
	return nil
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

type routed struct {
	Route routeName `json:"route"`
	Tries int       `json:"tries"`
}

func (r routed) Validate() error {
	if r.Tries > 3 && r.Route == "slow" {
		return Issues{{Path: "/tries", Message: "a slow route is tried at most 3 times"}}
	}
	return nil
}

// TestValidateAfterShapeAndDefinedTypes is DH-T-ZOD-7: a Validate() error
// surfaces with its field path once the shape holds, and a defined id type
// rejects malformed text during decode.
//
//dsh:test DH-T-ZOD-7
func TestValidateAfterShapeAndDefinedTypes(t *testing.T) {
	typ := MustOf[routed]()
	var r routed
	if is := typ.Decode([]byte(`{"route":"fast","tries":5}`), &r); is != nil || r.Route != "fast" {
		t.Fatalf("Decode = %+v, %v", r, is)
	}
	is := typ.Decode([]byte(`{"route":"slow","tries":5}`), &r)
	if len(is) != 1 || is[0].Path != "/tries" || !strings.Contains(is[0].Message, "at most 3") {
		t.Fatalf("Validate issue = %v, want one at /tries", is)
	}
	// The shape is checked first: a wrong type never reaches Validate.
	if is := typ.Decode([]byte(`{"route":"slow","tries":"many"}`), &r); len(is) != 1 || is[0].Path != "/tries" || !strings.Contains(is[0].Message, "must be integer") {
		t.Fatalf("shape issue = %v", is)
	}
	for _, bad := range []string{`{"route":"a b","tries":1}`, `{"route":"","tries":1}`, `{"route":"x/y","tries":1}`} {
		is := typ.Decode([]byte(bad), &r)
		if len(is) != 1 || !strings.Contains(is[0].Message, "is malformed") {
			t.Errorf("Decode(%s) = %v, want the id type's refusal", bad, is)
		}
	}
}
