package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// jsonName returns the wire name of a struct field and whether it is on the wire,
// following encoding/json.
func jsonName(f reflect.StructField) (string, bool) {
	if !f.IsExported() {
		return "", false
	}
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, true
	}
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return "", false
	case "":
		return f.Name, true
	}
	return name, true
}

// applyTags writes the dsh tag constraints of t's fields into s, recursively.
func applyTags(t reflect.Type, s *jsonschema.Schema, exact exactTable) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if shape, ok := shapeOf(t); ok {
		return applyTags(shape, s, exact)
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if s.Items != nil {
			return applyTags(t.Elem(), s.Items, exact)
		}
	case reflect.Map:
		if s.AdditionalProperties != nil {
			return applyTags(t.Elem(), s.AdditionalProperties, exact)
		}
	case reflect.Struct:
		for _, f := range reflect.VisibleFields(t) {
			if f.Anonymous {
				continue
			}
			name, ok := jsonName(f)
			ps := s.Properties[name]
			if !ok || ps == nil {
				continue
			}
			if tag, ok := f.Tag.Lookup("dsh"); ok {
				if err := applyTag(s, name, ps, f.Type, tag, exact); err != nil {
					return fmt.Errorf("field %s.%s: %w", t, f.Name, err)
				}
			}
			if err := applyTags(f.Type, ps, exact); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyTag applies one dsh tag, a comma-separated list of min=, max=, enum=a|b,
// required, live, secret, ui= and pattern= (which takes the rest of the tag, so a
// pattern may hold commas).
func applyTag(parent *jsonschema.Schema, name string, s *jsonschema.Schema, ft reflect.Type, tag string, exact exactTable) error {
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	kind := ft.Kind()
	numeric := kind >= reflect.Int && kind <= reflect.Float64
	for tag != "" {
		var word string
		if strings.HasPrefix(tag, "pattern=") {
			word, tag = tag, ""
		} else {
			word, tag, _ = strings.Cut(tag, ",")
		}
		key, val, hasVal := strings.Cut(word, "=")
		switch {
		case key == "required" && !hasVal:
			if !slices.Contains(parent.Required, name) {
				parent.Required = append(parent.Required, name)
			}
		case (key == "min" || key == "max") && hasVal:
			n, err := strconv.ParseFloat(val, 64)
			if _, exactOK := parseDecimal(val); err != nil || !exactOK {
				return fmt.Errorf("dsh %s=%q is not a number", key, val)
			}
			if err := setBound(s, key == "min", n, numeric, kind); err != nil {
				return fmt.Errorf("dsh %s=%s: %w", key, val, err)
			}
			if numeric {
				kw := map[string]string{"min": "minimum", "max": "maximum"}[key]
				if err := exact.set(s, kw, val); err != nil {
					return err
				}
			}
		case key == "enum" && hasVal:
			s.Enum = nil
			for _, e := range strings.Split(val, "|") {
				if !numeric {
					s.Enum = append(s.Enum, e)
					continue
				}
				if _, ok := parseDecimal(e); !ok {
					return fmt.Errorf("dsh enum value %q is not a number", e)
				}
				s.Enum = append(s.Enum, json.Number(e))
			}
		case key == "pattern" && hasVal:
			if kind != reflect.String {
				return errors.New("dsh pattern= needs a string field")
			}
			s.Pattern = val
		case (key == "live" || key == "secret") && !hasVal:
			setExtra(s, "x-dsh-"+key, true)
		case key == "ui" && hasVal:
			setExtra(s, "x-dsh-ui", val)
		default:
			return fmt.Errorf("dsh tag word %q is not known", word)
		}
	}
	lo, okLo := exact.bound(s, "minimum")
	hi, okHi := exact.bound(s, "maximum")
	if okLo && okHi && lo.d.cmp(hi.d) > 0 {
		return errors.New("dsh min is above max")
	}
	return nil
}

// setBound sets a numeric bound, or a length, item or property count bound.
func setBound(s *jsonschema.Schema, isMin bool, n float64, numeric bool, kind reflect.Kind) error {
	if numeric {
		if isMin {
			s.Minimum = &n
		} else {
			s.Maximum = &n
		}
		return nil
	}
	var lo, hi **int
	switch kind {
	case reflect.String:
		lo, hi = &s.MinLength, &s.MaxLength
	case reflect.Slice, reflect.Array:
		lo, hi = &s.MinItems, &s.MaxItems
	case reflect.Map:
		lo, hi = &s.MinProperties, &s.MaxProperties
	default:
		return fmt.Errorf("does not apply to a %s field", kind)
	}
	if n < 0 || n != float64(int(n)) {
		return errors.New("must be a whole count")
	}
	c := int(n)
	if isMin {
		*lo = &c
	} else {
		*hi = &c
	}
	return nil
}

func setExtra(s *jsonschema.Schema, key string, v any) {
	if s.Extra == nil {
		s.Extra = map[string]any{}
	}
	s.Extra[key] = v
}
