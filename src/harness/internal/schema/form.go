package schema

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// DecodeForm is Decode for an HTML form body (area A19): it turns the form into
// the JSON object Decode reads, then decodes it with the same checks. A key names
// a root property, or a nested one by dots ("inner.depth"). Each value is read
// by the kind its property declares: a number or integer field from its
// decimal text, a boolean from "true", "false" or a checkbox's "on", an array
// from every value the key carries, anything else as text. A value that does not
// read as its kind stays text, so the check reports the wrong type at its path;
// an unknown key stays too, so a closed shape refuses it. An empty value of a
// field that is not a string is absent, so the field keeps the value dst holds.
// For a Union the discriminator's value selects the variant whose fields are read.
func (t *Type) DecodeForm(v url.Values, dst any) Issues {
	root := t.schema
	if t.union != nil {
		if uv, ok := t.union.variants[v.Get(t.union.disc)]; ok {
			root = uv.typ.schema
		}
	}
	obj := map[string]any{}
	for _, key := range sortedKeys(v) {
		steps := strings.Split(key, ".")
		s, at := root, obj
		for i, step := range steps {
			var ps *jsonschema.Schema
			if s != nil {
				ps = s.Properties[step]
			}
			if i == len(steps)-1 {
				if val, ok := formValue(ps, v[key]); ok {
					at[step] = val
				}
				break
			}
			next, ok := at[step].(map[string]any)
			if !ok {
				next = map[string]any{}
				at[step] = next
			}
			s, at = ps, next
		}
	}
	if t.union != nil {
		return t.union.decode(obj, dst)
	}
	return t.decode(obj, nil, dst)
}

// formValue reads the values of one key by the kind s declares; it reports
// false for an empty value of a field that is not text.
func formValue(s *jsonschema.Schema, vals []string) (any, bool) {
	kind := ""
	if s != nil {
		kind = fieldOf(s, "").Kind
	}
	if kind == "array" {
		out := make([]any, 0, len(vals))
		for _, e := range vals {
			if x, ok := formScalar(s.Items, e); ok {
				out = append(out, x)
			}
		}
		return out, true
	}
	if len(vals) != 1 {
		out := make([]any, len(vals))
		for i, e := range vals {
			out[i] = e
		}
		return out, true // several values for one field: the check names the wrong type
	}
	return formScalar(s, vals[0])
}

func formScalar(s *jsonschema.Schema, text string) (any, bool) {
	kind := ""
	if s != nil {
		kind = fieldOf(s, "").Kind
	}
	if text == "" && kind != "string" && kind != "" {
		return nil, false
	}
	switch kind {
	case "integer", "number":
		if _, ok := parseDecimal(text); ok {
			return json.Number(text), true
		}
	case "boolean":
		switch text {
		case "true", "on":
			return true, true
		case "false":
			return false, true
		}
	}
	return text, true
}
