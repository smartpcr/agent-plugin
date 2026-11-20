package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// The module stores numeric keywords as float64, which cannot hold every JSON
// number: 9007199254740993 would silently become 9007199254740992. Each Type
// therefore keeps the literal of every numeric bound in an exact table, keyed by the
// subschema, and the validator, the exported document and the fingerprint all read
// the literal. Enum and const values are kept as json.Number in the Schema itself.

// boundKeys are the numeric keywords held as float64 by the module.
var boundKeys = []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"}

// numLit is one declared number: its literal as written and its exact value.
type numLit struct {
	lit string
	d   decimal
}

// exactTable maps a subschema to its declared numeric bounds by keyword.
type exactTable map[*jsonschema.Schema]map[string]numLit

func (t exactTable) set(s *jsonschema.Schema, key, lit string) error {
	d, ok := parseDecimal(lit)
	if !ok {
		return fmt.Errorf("%s %q is not a number", key, lit)
	}
	if t[s] == nil {
		t[s] = map[string]numLit{}
	}
	t[s][key] = numLit{lit, d}
	return nil
}

// bound returns the declared bound key of s, exactly when the table has it.
func (t exactTable) bound(s *jsonschema.Schema, key string) (numLit, bool) {
	n, ok := t[s][key]
	return n, ok
}

// floatField returns the module field that holds bound key.
func floatField(s *jsonschema.Schema, key string) **float64 {
	switch key {
	case "minimum":
		return &s.Minimum
	case "maximum":
		return &s.Maximum
	case "exclusiveMinimum":
		return &s.ExclusiveMinimum
	case "exclusiveMaximum":
		return &s.ExclusiveMaximum
	}
	return &s.MultipleOf
}

// floatExact reports whether the literal lit survives float64 unchanged.
func floatExact(lit string) bool {
	d, ok := parseDecimal(lit)
	if !ok {
		return false
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return false
	}
	fd, ok := toDecimal(f)
	return ok && fd == d
}

// child is one subschema of the subset and its JSON path below its parent.
type child struct {
	path []string
	s    *jsonschema.Schema
}

// subsetChildren returns the subschemas of s the subset defines, in a stable order.
func subsetChildren(s *jsonschema.Schema) []child {
	var out []child
	add := func(c *jsonschema.Schema, path ...string) {
		if c != nil {
			out = append(out, child{path, c})
		}
	}
	add(s.Items, "items")
	add(s.AdditionalProperties, "additionalProperties")
	add(s.Not, "not")
	for kw, list := range map[string][]*jsonschema.Schema{"allOf": s.AllOf, "anyOf": s.AnyOf, "oneOf": s.OneOf} {
		for i, c := range list {
			add(c, kw, strconv.Itoa(i))
		}
	}
	for kw, m := range map[string]map[string]*jsonschema.Schema{"properties": s.Properties, "$defs": s.Defs, "definitions": s.Definitions} {
		for _, name := range sortedKeys(m) {
			add(m[name], kw, name)
		}
	}
	slices.SortStableFunc(out, func(a, b child) int { return strings.Compare(a.path[0], b.path[0]) })
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// at follows path from a decoded JSON value; it reports false when a step is absent.
func at(v any, path []string) (any, bool) {
	for _, p := range path {
		switch x := v.(type) {
		case map[string]any:
			e, ok := x[p]
			if !ok {
				return nil, false
			}
			v = e
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// collectExact walks s with g, the same document decoded with exact numbers, and
// records every numeric keyword exactly: bounds into the table, enum and const
// values into s as json.Number.
func collectExact(s *jsonschema.Schema, g any, t exactTable) error {
	obj, ok := g.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range boundKeys {
		if n, ok := obj[key].(json.Number); ok {
			if err := t.set(s, key, string(n)); err != nil {
				return err
			}
		}
	}
	if e, ok := obj["enum"].([]any); ok && s.Enum != nil {
		s.Enum = e
	}
	if c, ok := obj["const"]; ok && s.Const != nil {
		s.Const = &c
	}
	for _, c := range subsetChildren(s) {
		if sub, ok := at(g, c.path); ok {
			if err := collectExact(c.s, sub, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// inexactDeclaration returns the first number a document declares that float64
// would round, outside annotations. The module's validator would compare against
// the rounded value, so such a document is refused rather than weakened.
func inexactDeclaration(g any, path string) (string, string, bool) {
	switch x := g.(type) {
	case json.Number:
		if !floatExact(string(x)) {
			return path, string(x), true
		}
	case []any:
		for i, e := range x {
			if p, n, ok := inexactDeclaration(e, pointer(path, strconv.Itoa(i))); ok {
				return p, n, true
			}
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			if annotationKeys[k] {
				continue
			}
			if p, n, ok := inexactDeclaration(x[k], pointer(path, k)); ok {
				return p, n, true
			}
		}
	}
	return "", "", false
}

// inexactInstance reports every number of inst that float64 would round: the
// module's validator would check the rounded value, not the one sent.
func inexactInstance(inst any, path string) Issues {
	var issues Issues
	switch x := inst.(type) {
	case json.Number:
		if !floatExact(string(x)) {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("number %s cannot be checked exactly against this schema", x)})
		}
	case []any:
		for i, e := range x {
			issues = append(issues, inexactInstance(e, pointer(path, strconv.Itoa(i)))...)
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			issues = append(issues, inexactInstance(x[k], pointer(path, k))...)
		}
	}
	return issues
}

// exportDoc marshals s and writes back each bound literal float64 could not hold,
// keeping the module's key order.
func exportDoc(s *jsonschema.Schema, t exactTable) ([]byte, error) {
	doc, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	patched := false
	for _, m := range t {
		for _, n := range m {
			if !floatExact(n.lit) {
				patched = true
			}
		}
	}
	if !patched {
		return doc, nil
	}
	root, err := readOrdered(doc)
	if err != nil {
		return nil, err
	}
	patchBounds(s, root, t)
	var buf bytes.Buffer
	if err := writeOrdered(&buf, root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func patchBounds(s *jsonschema.Schema, node any, t exactTable) {
	obj, ok := node.(*ordered)
	if !ok {
		return
	}
	for key, n := range t[s] {
		if !floatExact(n.lit) {
			obj.set(key, json.Number(n.lit))
		}
	}
	for _, c := range subsetChildren(s) {
		if sub, ok := obj.at(c.path); ok {
			patchBounds(c.s, sub, t)
		}
	}
}

// ordered is a JSON object that keeps its key order.
type ordered struct {
	keys []string
	vals map[string]any
}

func (o *ordered) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *ordered) at(path []string) (any, bool) {
	var v any = o
	for _, p := range path {
		switch x := v.(type) {
		case *ordered:
			e, ok := x.vals[p]
			if !ok {
				return nil, false
			}
			v = e
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

func readOrdered(doc []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the value")
	}
	return v, nil
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := &ordered{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(kt.(string), v)
		}
		_, err := dec.Token()
		return o, err
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	return tok, nil
}

func writeOrdered(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case *ordered:
		buf.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeOrdered(buf, x.vals[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeOrdered(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}

// canonNumbers rewrites every number for the fingerprint: one float64 can hold
// keeps its float64 spelling, any other becomes its exact decimal, digits e exp.
// Equal numbers therefore fingerprint alike however they were written.
func canonNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if floatExact(string(x)) {
			f, _ := strconv.ParseFloat(string(x), 64)
			return f
		}
		d, _ := parseDecimal(string(x))
		sign := ""
		if d.neg {
			sign = "-"
		}
		return json.Number(sign + d.digits + "e" + d.exp)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canonNumbers(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = canonNumbers(e)
		}
		return out
	}
	return v
}
