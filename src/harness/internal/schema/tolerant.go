package schema

import "github.com/google/jsonschema-go/jsonschema"

// maxRefHops bounds the local references prune follows without descending into
// the value, so a reference cycle ends.
const maxRefHops = 32

// prune drops from inst, in place, every key of an object that s does not
// declare where s is closed (additionalProperties false), at every depth. It
// follows local $ref against root, and prunes a oneOf or anyOf only through the
// one alternative whose const properties the object matches (a union's
// discriminator); with none or several it leaves the object as it is, for the
// check to judge.
func prune(root, s *jsonschema.Schema, inst any, hops int) any {
	if s == nil || hops > maxRefHops {
		return inst
	}
	if s.Ref != "" {
		if t := target(root, s.Ref); t != nil {
			inst = prune(root, t, inst, hops+1)
		}
	}
	switch x := inst.(type) {
	case map[string]any:
		if s.Properties != nil || s.AdditionalProperties != nil {
			for k, v := range x {
				if ps, ok := s.Properties[k]; ok {
					x[k] = prune(root, ps, v, 0)
					continue
				}
				if s.AdditionalProperties != nil && isFalse(s.AdditionalProperties) {
					delete(x, k)
					continue
				}
				x[k] = prune(root, s.AdditionalProperties, v, 0)
			}
		}
		for _, alts := range [][]*jsonschema.Schema{s.OneOf, s.AnyOf} {
			if alt := selected(alts, x); alt != nil {
				inst = prune(root, alt, inst, hops+1)
			}
		}
	case []any:
		for i, e := range x {
			x[i] = prune(root, s.Items, e, 0)
		}
	}
	return inst
}

// selected returns the one alternative whose const properties obj matches, nil
// when no alternative or more than one does.
func selected(alts []*jsonschema.Schema, obj map[string]any) *jsonschema.Schema {
	var found *jsonschema.Schema
	for _, alt := range alts {
		consts := 0
		match := true
		for name, ps := range alt.Properties {
			if ps == nil || ps.Const == nil {
				continue
			}
			consts++
			if v, ok := obj[name]; !ok || !equal(*ps.Const, v) {
				match = false
			}
		}
		if consts == 0 || !match {
			continue
		}
		if found != nil {
			return nil
		}
		found = alt
	}
	return found
}
