// Package schema is the one description of a data shape (DH-D-UTIL-1, libs/zod.md):
// a Go type is declared once, and its JSON Schema document, its validator and its
// fingerprint are all derived from that declaration.
//
//dsh:untrusted-parser values and schema documents arrive from models, peers and plugins
package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// Type is the derived, immutable form of one shape.
type Type struct {
	name     string
	schema   *jsonschema.Schema
	doc      []byte
	fp       string
	open     bool
	fallback *jsonschema.Resolved // set when the document uses keywords outside the subset
	exact    exactTable           // the declared literal of every numeric bound
}

// Option adjusts a derivation.
type Option func(*options)

type options struct{ open bool }

// Open admits unknown keys at the root object: they are kept out of the decoded
// value instead of being reported. Nested objects stay closed.
func Open() Option { return func(o *options) { o.open = true } }

type cacheKey struct {
	t    reflect.Type
	open bool
}

var cache sync.Map // cacheKey -> *Type

// typeSchemas overrides the module's derivation where it does not follow
// encoding/json: json.RawMessage is any JSON value on the wire, not an array of
// bytes.
var typeSchemas = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[json.RawMessage](): {},
}

// Shaper is implemented, on the value receiver, by a type whose JSON form is not
// its own fields: one that keeps its fields unexported and encodes through a
// MarshalJSON of another type. SchemaShape returns that wire type; the type is
// derived, tagged and fingerprinted as that shape wherever it appears, at the root
// or nested in another value.
type Shaper interface{ SchemaShape() reflect.Type }

var shaperType = reflect.TypeFor[Shaper]()

// shapeOf returns the wire type of t when t is a Shaper.
func shapeOf(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Interface || !t.Implements(shaperType) {
		return nil, false
	}
	return reflect.Zero(t).Interface().(Shaper).SchemaShape(), true
}

// shapeSchemas returns typeSchemas extended with the derived shape of every Shaper
// reachable from t. A shape's own Shapers are derived before it; a Shaper whose
// shape reaches itself is refused, as any recursive type is.
func shapeSchemas(t reflect.Type) (map[reflect.Type]*jsonschema.Schema, error) {
	out := map[reflect.Type]*jsonschema.Schema{}
	for k, v := range typeSchemas {
		out[k] = v
	}
	state := map[reflect.Type]int{} // 1 visiting, 2 done
	var walk func(reflect.Type) error
	walk = func(t reflect.Type) error {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if _, fixed := typeSchemas[t]; fixed {
			return nil
		}
		switch state[t] {
		case 2:
			return nil
		case 1:
			if _, ok := shapeOf(t); ok {
				return fmt.Errorf("the shape of %s reaches %s again", t, t)
			}
			return nil // an ordinary recursive type: the module reports it
		}
		state[t] = 1
		defer func() { state[t] = 2 }()
		if shape, ok := shapeOf(t); ok {
			if err := walk(shape); err != nil {
				return err
			}
			s, err := jsonschema.ForType(shape, &jsonschema.ForOptions{TypeSchemas: out})
			if err != nil {
				return fmt.Errorf("shape of %s: %w", t, err)
			}
			out[t] = s
			return nil
		}
		switch t.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			return walk(t.Elem())
		case reflect.Struct:
			for _, f := range reflect.VisibleFields(t) {
				if f.IsExported() && !f.Anonymous {
					if err := walk(f.Type); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(t); err != nil {
		return nil, err
	}
	return out, nil
}

// Of derives the shape of T. The result is cached per type and options; deriving
// twice returns the same *Type, so documents and fingerprints are byte-identical.
func Of[T any](opts ...Option) (*Type, error) {
	return ForType(reflect.TypeFor[T](), opts...)
}

// ForType is Of for a reflect.Type.
func ForType(t reflect.Type, opts ...Option) (*Type, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	key := cacheKey{t, o.open}
	if v, ok := cache.Load(key); ok {
		return v.(*Type), nil
	}
	overrides, err := shapeSchemas(t)
	if err != nil {
		return nil, fmt.Errorf("schema: deriving %s: %w", t, err)
	}
	s, err := jsonschema.ForType(t, &jsonschema.ForOptions{TypeSchemas: overrides})
	if err != nil {
		return nil, fmt.Errorf("schema: deriving %s: %w", t, err)
	}
	s = s.CloneSchemas()
	exact := exactTable{}
	if err := applyTags(t, s, exact); err != nil {
		return nil, fmt.Errorf("schema: deriving %s: %w", t, err)
	}
	if o.open {
		s.AdditionalProperties = nil
	}
	typ, err := build(t.String(), s, o.open, exact)
	if err != nil {
		return nil, err
	}
	v, _ := cache.LoadOrStore(key, typ)
	return v.(*Type), nil
}

// MustOf is Of for package-level declarations; it panics on a derivation error.
func MustOf[T any](opts ...Option) *Type {
	t, err := Of[T](opts...)
	if err != nil {
		panic(err)
	}
	return t
}

// FromJSON loads a schema document supplied at run time (an external plugin's tool,
// an MCP server). Its strings are data: nothing in it is evaluated. Documents that
// use keywords outside the subset this package enforces are validated by the
// module's full validator instead, which reports the first violation only. Numbers
// keep the precision they are written with; a document outside the subset that
// declares a number float64 cannot hold is refused, since the module would compare
// against the rounded value.
func FromJSON(doc []byte) (*Type, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(doc, &s); err != nil {
		return nil, fmt.Errorf("schema: the document does not parse: %w", err)
	}
	g, err := parse(doc)
	if err != nil {
		return nil, fmt.Errorf("schema: the document does not parse: %w", err)
	}
	exact := exactTable{}
	if err := collectExact(&s, g, exact); err != nil {
		return nil, fmt.Errorf("schema: document: %w", err)
	}
	t, err := build("document", &s, false, exact)
	if err != nil {
		return nil, err
	}
	if t.fallback != nil {
		if p, n, bad := inexactDeclaration(g, ""); bad {
			return nil, fmt.Errorf("schema: document: %s declares %s, which the full validator cannot hold exactly", p, n)
		}
		// The module compares float64 values, so it gets the document as float64.
		var fs jsonschema.Schema
		if err := json.Unmarshal(doc, &fs); err != nil {
			return nil, fmt.Errorf("schema: the document does not parse: %w", err)
		}
		if t.fallback, err = fs.Resolve(nil); err != nil {
			return nil, fmt.Errorf("schema: document: %w", err)
		}
	}
	return t, nil
}

func build(name string, s *jsonschema.Schema, open bool, exact exactTable) (*Type, error) {
	rs, err := s.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("schema: %s: %w", name, err)
	}
	if err := compilePatterns(s); err != nil {
		return nil, fmt.Errorf("schema: %s: %w", name, err)
	}
	doc, err := exportDoc(s, exact)
	if err != nil {
		return nil, fmt.Errorf("schema: %s: %w", name, err)
	}
	fp, err := fingerprint(doc)
	if err != nil {
		return nil, fmt.Errorf("schema: %s: %w", name, err)
	}
	t := &Type{name: name, schema: s, doc: doc, fp: fp, open: open, exact: exact}
	if !inSubset(s) {
		t.fallback = rs
	}
	return t, nil
}

// JSONSchema returns the derived document: properties in field order, compact.
func (t *Type) JSONSchema() []byte { return bytes.Clone(t.doc) }

// Fingerprint returns "sha256:<hex>" of the canonical wire shape: keys sorted,
// descriptions, titles, defaults, examples and x-* metadata removed. Reordering
// fields or editing descriptions keeps it; adding, removing, retyping or
// re-requiring a field changes it.
func (t *Type) Fingerprint() string { return t.fp }

// Validate checks v against the shape and returns every violation with its path. v
// is a Go value, or JSON as json.RawMessage or []byte.
func (t *Type) Validate(v any) Issues {
	inst, err := toInstance(v)
	if err != nil {
		return Issues{{Path: "", Message: err.Error()}}
	}
	return t.check(inst)
}

func (t *Type) check(inst any) Issues {
	if t.fallback != nil {
		if issues := inexactInstance(inst, ""); len(issues) > 0 {
			return issues
		}
		if err := t.fallback.Validate(plain(inst)); err != nil {
			return Issues{{Path: "", Message: err.Error()}}
		}
		return nil
	}
	v := newValidator(t.schema, t.exact)
	v.check(t.schema, inst, "")
	return v.issues
}

// Validator is implemented by decoded values with rules beyond the shape. A returned
// Issues keeps its paths; any other error is reported at the root.
type Validator interface{ Validate() error }

// Decode checks raw against the shape, then decodes it strictly into dst (unknown
// fields are refused unless the shape is Open), then calls dst's Validate when it
// has one. dst is left partly written when issues are returned.
func (t *Type) Decode(raw []byte, dst any) Issues {
	inst, err := parse(raw)
	if err != nil {
		return Issues{{Path: "", Message: err.Error()}}
	}
	if issues := t.check(inst); len(issues) > 0 {
		return issues
	}
	if t.open {
		if issues := t.foldedKeys(inst); len(issues) > 0 {
			return issues
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if !t.open {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		return Issues{{Path: "", Message: "does not decode: " + err.Error()}}
	}
	if val, ok := dst.(Validator); ok {
		if err := val.Validate(); err != nil {
			var issues Issues
			if errors.As(err, &issues) {
				return issues
			}
			return Issues{{Path: "", Message: err.Error()}}
		}
	}
	return nil
}

// foldedKeys refuses unknown root keys that encoding/json would still match to a
// declared property, since it matches names case-insensitively: on an Open shape
// such a key escapes validation yet overwrites the validated field.
func (t *Type) foldedKeys(inst any) Issues {
	obj, ok := inst.(map[string]any)
	if !ok {
		return nil
	}
	var issues Issues
	for k := range obj {
		if _, declared := t.schema.Properties[k]; declared {
			continue
		}
		for p := range t.schema.Properties {
			if strings.EqualFold(k, p) || strings.ToUpper(k) == strings.ToUpper(p) {
				issues = append(issues, Issue{Path: pointer("", k), Message: fmt.Sprintf("differs from the field %q only in case", p)})
				break
			}
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Path < issues[j].Path })
	return issues
}

// Issue is one violation: a JSON Pointer to the offending value and what is wrong.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Path == "" {
		return "/: " + i.Message
	}
	return i.Path + ": " + i.Message
}

// Issues is every violation of one check; nil when the value conforms.
type Issues []Issue

func (is Issues) Error() string {
	parts := make([]string, len(is))
	for i, x := range is {
		parts[i] = x.String()
	}
	return strings.Join(parts, "; ")
}

// parse reads exactly one JSON value, keeping numbers exact.
func parse(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not JSON: data after the value")
	}
	return v, nil
}

func toInstance(v any) (any, error) {
	switch b := v.(type) {
	case json.RawMessage:
		return parse(b)
	case []byte:
		return parse(b)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("does not encode: %w", err)
	}
	return parse(raw)
}

// plain converts json.Number to float64 for the module's validator. Every number has
// passed inexactInstance first, so none is rounded.
func plain(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = plain(e)
		}
		return out
	}
	return v
}

// annotationKeys are removed from the canonical form; they do not change the wire.
var annotationKeys = map[string]bool{
	"description": true, "title": true, "default": true, "examples": true,
	"$comment": true, "deprecated": true, "readOnly": true, "writeOnly": true,
}

// mapKeywords hold a map from names to schemas, not keywords.
var mapKeywords = map[string]bool{
	"properties": true, "$defs": true, "definitions": true, "patternProperties": true,
	"dependentSchemas": true,
}

func fingerprint(doc []byte) (string, error) {
	v, err := parse(doc)
	if err != nil {
		return "", err
	}
	canon, err := json.Marshal(canonNumbers(canonical(v)))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonical strips annotations from a schema value and sorts its required list;
// encoding/json then writes object keys sorted.
func canonical(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canonical(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			switch {
			case annotationKeys[k], strings.HasPrefix(k, "x-"):
			case mapKeywords[k]:
				sub, _ := e.(map[string]any)
				named := map[string]any{}
				for name, s := range sub {
					named[name] = canonical(s)
				}
				out[k] = named
			case k == "required":
				req, _ := e.([]any)
				names := make([]string, 0, len(req))
				for _, r := range req {
					s, _ := r.(string)
					names = append(names, s)
				}
				sort.Strings(names)
				out[k] = names
			case k == "enum" || k == "const":
				out[k] = e
			default:
				out[k] = canonical(e)
			}
		}
		return out
	}
	return v
}
