package schema

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// withDefaults is ForType with the Defaults option: derived afresh, not cached.
func withDefaults(t reflect.Type, o options) (*Type, error) {
	if dt := reflect.TypeOf(o.defaults); dt != t {
		return nil, fmt.Errorf("schema: deriving %s: the defaults are a %v, not a %s", t, dt, t)
	}
	s, exact, err := derive(t)
	if err != nil {
		return nil, err
	}
	if o.open {
		s.AdditionalProperties = nil
	}
	inst, err := toInstance(o.defaults)
	if err != nil {
		return nil, fmt.Errorf("schema: deriving %s: the defaults %w", t, err)
	}
	v := newValidator(s, exact)
	v.check(s, inst, "")
	if len(v.issues) > 0 {
		return nil, fmt.Errorf("schema: deriving %s: a default breaks the shape: %w", t, v.issues)
	}
	if err := annotate(s, inst); err != nil {
		return nil, fmt.Errorf("schema: deriving %s: %w", t, err)
	}
	typ, err := build(t.String(), s, o.open, exact)
	if err != nil {
		return nil, err
	}
	typ.tolerant = o.tolerant
	return typ, nil
}

// annotate writes, for every property of s that inst holds, the value as that
// property's default, and descends into nested objects.
func annotate(s *jsonschema.Schema, inst any) error {
	obj, ok := inst.(map[string]any)
	if !ok {
		return nil
	}
	for name, ps := range s.Properties {
		val, ok := obj[name]
		if !ok || ps == nil {
			continue
		}
		b, err := json.Marshal(val)
		if err != nil {
			return err
		}
		ps.Default = b
		if err := annotate(ps, val); err != nil {
			return err
		}
	}
	return nil
}
