package protocol

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

//go:generate go run ./internal/genschema schema.json

//go:embed schema.json
var committed []byte

// Schema returns the committed schema file, src/sdk/protocol/schema.json.
func Schema() []byte { return bytes.Clone(committed) }

// Document derives the schema file from the Go types and the method table: the
// method table, the error table and one JSON Schema (draft 2020-12) definition per
// named type, sorted by name. The bytes depend only on the declarations. src/sdk may
// not import the harness, so this is its own small derivation of the rules
// src/harness/internal/schema applies: properties in field order, a field is
// required unless its json tag says omitempty or omitzero, objects are closed, the
// jsonschema tag is the description, and dsh tags give enum=, min= and max=.
func Document() ([]byte, error) {
	d := &deriver{defs: map[string]obj{}, types: map[string]reflect.Type{}}
	methods := make([]any, 0, len(Methods))
	for _, m := range Methods {
		row := obj{{"name", m.Name}, {"direction", m.Direction}, {"kind", m.Kind}}
		params, err := d.schemaFor(m.Params)
		if err != nil {
			return nil, fmt.Errorf("protocol: %s params: %w", m.Name, err)
		}
		row = append(row, kv{"params", params})
		if m.Result != nil {
			result, err := d.schemaFor(m.Result)
			if err != nil {
				return nil, fmt.Errorf("protocol: %s result: %w", m.Name, err)
			}
			row = append(row, kv{"result", result})
		}
		if m.Capability != "" {
			row = append(row, kv{"capability", m.Capability})
		}
		methods = append(methods, append(row, kv{"since", m.Since}))
	}
	errs := make([]any, 0, len(Errors))
	for _, e := range Errors {
		errs = append(errs, obj{{"reason", e.Reason}, {"code", e.Code}, {"when", e.When}})
	}
	names := make([]string, 0, len(d.defs))
	for name := range d.defs {
		names = append(names, name)
	}
	slices.Sort(names)
	defs := make(obj, 0, len(names))
	for _, name := range names {
		defs = append(defs, kv{name, d.defs[name]})
	}
	doc := obj{
		{"$schema", "https://json-schema.org/draft/2020-12/schema"},
		{"title", "dsh SDK protocol"},
		{"version", Version},
		{"methods", methods},
		{"errors", errs},
		{"$defs", defs},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// obj is a JSON object that keeps its key order.
type obj []kv

type kv struct {
	k string
	v any
}

func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(e.k)
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

type deriver struct {
	defs  map[string]obj
	types map[string]reflect.Type
}

var rawMessage = reflect.TypeFor[json.RawMessage]()

func (d *deriver) schemaFor(t reflect.Type) (obj, error) {
	if t == rawMessage {
		return obj{}, nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		return d.schemaFor(t.Elem())
	case reflect.Bool:
		return obj{{"type", "boolean"}}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return obj{{"type", "integer"}}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return obj{{"type", "integer"}, {"minimum", 0}}, nil
	case reflect.Float32, reflect.Float64:
		return obj{{"type", "number"}}, nil
	case reflect.String:
		return obj{{"type", "string"}}, nil
	case reflect.Interface:
		return obj{}, nil
	case reflect.Slice, reflect.Array:
		items, err := d.schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}
		return obj{{"type", "array"}, {"items", items}}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map key %s is not a string", t.Key())
		}
		values, err := d.schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}
		return obj{{"type", "object"}, {"additionalProperties", values}}, nil
	case reflect.Struct:
		if t.Name() == "" {
			return d.object(t)
		}
		return d.ref(t)
	}
	return nil, fmt.Errorf("type %s has no JSON shape", t)
}

func (d *deriver) ref(t reflect.Type) (obj, error) {
	name := t.Name()
	if prev, ok := d.types[name]; ok {
		if prev != t {
			return nil, fmt.Errorf("two types are named %s: %s and %s", name, prev, t)
		}
	} else {
		d.types[name] = t
		s, err := d.object(t)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		d.defs[name] = s
	}
	return obj{{"$ref", "#/$defs/" + name}}, nil
}

func (d *deriver) object(t reflect.Type) (obj, error) {
	props := obj{}
	var required []string
	if err := d.fields(t, &props, &required); err != nil {
		return nil, err
	}
	s := obj{{"type", "object"}, {"properties", props}}
	if len(required) > 0 {
		s = append(s, kv{"required", required})
	}
	return append(s, kv{"additionalProperties", false}), nil
}

// fields adds the wire fields of t, flattening embedded structs as encoding/json does.
func (d *deriver) fields(t reflect.Type, props *obj, required *[]string) error {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, hasTag := f.Tag.Lookup("json")
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			if err := d.fields(f.Type, props, required); err != nil {
				return err
			}
			continue
		}
		if !f.IsExported() || name == "-" {
			continue
		}
		if !hasTag || name == "" {
			name = f.Name
		}
		s, err := d.schemaFor(f.Type)
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		if s, err = constrain(s, f.Tag.Get("dsh")); err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		if desc := f.Tag.Get("jsonschema"); desc != "" {
			s = append(s, kv{"description", desc})
		}
		*props = append(*props, kv{name, s})
		if !strings.Contains(","+opts+",", ",omitempty,") && !strings.Contains(","+opts+",", ",omitzero,") {
			*required = append(*required, name)
		}
	}
	return nil
}

// constrain applies the dsh tag words this derivation knows: enum=a|b, min=, max=.
func constrain(s obj, tag string) (obj, error) {
	if tag == "" {
		return s, nil
	}
	for _, word := range strings.Split(tag, ",") {
		key, val, _ := strings.Cut(word, "=")
		switch key {
		case "enum":
			s = append(s, kv{"enum", strings.Split(val, "|")})
		case "min", "max":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("dsh %s=%q is not an integer", key, val)
			}
			s = append(s, kv{map[string]string{"min": "minimum", "max": "maximum"}[key], n})
		default:
			return nil, fmt.Errorf("dsh tag word %q is not known to the protocol derivation", word)
		}
	}
	return s, nil
}
