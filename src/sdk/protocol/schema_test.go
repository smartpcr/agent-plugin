package protocol

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// TestSchemaMatchesGolden is the stage scenario: the schema derived from the Go types
// is byte-identical to the committed schema.json. On a deliberate change, run
// go -C src/sdk generate ./protocol and commit the result.
func TestSchemaMatchesGolden(t *testing.T) {
	got, err := Document()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, Schema()) {
		t.Fatal("schema.json is stale: run `go -C src/sdk generate ./protocol` and commit it")
	}
	again, _ := Document()
	if !bytes.Equal(got, again) {
		t.Fatal("two derivations differ")
	}
	if bytes.Contains(got, []byte("\r")) {
		t.Fatal("the schema has a carriage return")
	}
}

func TestSchemaContent(t *testing.T) {
	var doc struct {
		Version int `json:"version"`
		Methods []struct {
			Name       string          `json:"name"`
			Kind       string          `json:"kind"`
			Params     json.RawMessage `json:"params"`
			Result     json.RawMessage `json:"result"`
			Capability string          `json:"capability"`
		} `json:"methods"`
		Errors []jsonrpc.Entry           `json:"errors"`
		Defs   map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != Version || len(doc.Methods) != len(Methods) || len(doc.Errors) != len(Errors) {
		t.Fatalf("version %d, %d methods, %d errors", doc.Version, len(doc.Methods), len(doc.Errors))
	}
	for i, m := range doc.Methods {
		if m.Name != Methods[i].Name || (m.Kind == "request") != (len(m.Result) > 0) {
			t.Errorf("method %d = %+v", i, m)
		}
	}
	event := doc.Defs["SessionEventNotification"]
	props, _ := event["properties"].(map[string]any)
	for _, f := range []string{"sessionId", "rootSessionId", "parentSessionId", "event"} {
		if _, ok := props[f]; !ok {
			t.Errorf("SessionEventNotification lacks %s (embedded Scope must flatten)", f)
		}
	}
	if req, _ := json.Marshal(event["required"]); string(req) != `["sessionId","rootSessionId","event"]` {
		t.Errorf("required = %s", req)
	}
	cancel := doc.Defs["SessionCancelParams"]["properties"].(map[string]any)["inbox"].(map[string]any)
	if enum, _ := json.Marshal(cancel["enum"]); string(enum) != `["keep","discard"]` {
		t.Errorf("inbox enum = %s", enum)
	}
}

func TestTables(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Methods {
		if seen[m.Name] {
			t.Errorf("method %s is listed twice", m.Name)
		}
		seen[m.Name] = true
		switch {
		case m.Kind == Request && m.Result == nil, m.Kind == Notification && m.Result != nil:
			t.Errorf("%s: kind %s with result %v", m.Name, m.Kind, m.Result)
		case m.Kind == Notification && m.Name != jsonrpc.CancelMethod && !strings.Contains(m.Name, "."):
			t.Errorf("notification %s does not use '.'", m.Name)
		case m.Kind == Request && m.Direction == FromHost && m.Capability == "":
			t.Errorf("host request %s names no capability", m.Name)
		}
		if got, ok := Lookup(m.Name); !ok || got.Name != m.Name {
			t.Errorf("Lookup(%s) failed", m.Name)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup found an unknown method")
	}
	reasons := map[string]bool{}
	for _, e := range Errors {
		if !jsonrpc.ValidReason(e.Reason) || reasons[e.Reason] || e.When == "" {
			t.Errorf("error row %+v", e)
		}
		reasons[e.Reason] = true
	}
}

type dupA struct{ X Empty }

func TestDeriveRefusals(t *testing.T) {
	type Empty struct{ Y int } // shadows protocol.Empty: two types named Empty
	for name, typ := range map[string]reflect.Type{
		"channel":       reflect.TypeFor[chan int](),
		"map key":       reflect.TypeFor[map[int]string](),
		"slice of func": reflect.TypeFor[[]func()](),
		"map value":     reflect.TypeFor[map[string]func()](),
		"field":         reflect.TypeFor[struct{ F func() }](),
		"named field":   reflect.TypeFor[struct{ D struct{ F chan int } }](),
		"embedded":      reflect.TypeFor[struct{ badEmbed }](),
		"unknown dsh": reflect.TypeFor[struct {
			A int `dsh:"pattern=x"`
		}](),
		"bad bound": reflect.TypeFor[struct {
			A int `dsh:"min=0.5"`
		}](),
		"duplicate": reflect.TypeFor[struct {
			A dupA
			B Empty
		}](),
		"bad def": reflect.TypeFor[struct{ B badDef }](),
	} {
		d := &deriver{defs: map[string]obj{}, types: map[string]reflect.Type{}}
		if _, err := d.schemaFor(typ); err == nil {
			t.Errorf("%s: derivation succeeded", name)
		}
	}
	d := &deriver{defs: map[string]obj{}, types: map[string]reflect.Type{}}
	s, err := d.schemaFor(reflect.TypeFor[struct {
		U  uint8
		F  float64
		I  any
		M  map[string]bool
		P  *int    `json:"p,omitzero" dsh:"min=1,max=3"`
		x  int     //nolint:unused
		No string  `json:"-"`
		E  []Empty `json:",omitempty"`
	}]())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	want := `{"type":"object","properties":{"U":{"type":"integer","minimum":0},"F":{"type":"number"},"I":{},"M":{"type":"object","additionalProperties":{"type":"boolean"}},"p":{"type":"integer","minimum":1,"maximum":3},"E":{"type":"array","items":{"$ref":"#/$defs/Empty"}}},"required":["U","F","I","M"],"additionalProperties":false}`
	if string(b) != want {
		t.Fatalf("derived\n%s\nwant\n%s", b, want)
	}
	if _, err := json.Marshal(obj{{"f", func() {}}}); err == nil {
		t.Fatal("marshalling a func value succeeded")
	}
}

type badDef struct{ F chan int }

type badEmbed struct{ F func() }

func TestDocumentRefusals(t *testing.T) {
	saved := Methods
	t.Cleanup(func() { Methods = saved })
	Methods = []Method{{Name: "p", Params: reflect.TypeFor[chan int]()}}
	if _, err := Document(); err == nil || !strings.Contains(err.Error(), "p params") {
		t.Fatalf("Document = %v", err)
	}
	Methods = []Method{{Name: "r", Params: reflect.TypeFor[Empty](), Result: reflect.TypeFor[chan int]()}}
	if _, err := Document(); err == nil || !strings.Contains(err.Error(), "r result") {
		t.Fatalf("Document = %v", err)
	}
}
