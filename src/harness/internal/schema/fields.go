package schema

import (
	"iter"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
)

// Field is one property of a shape, at any depth below the root object: its JSON
// Pointer path ("/inner/depth"), its JSON kind ("string", "integer", "object",
// ...; "" when any value is admitted), whether null is admitted and whether its
// object requires it, and the dsh metadata words live, secret and ui=.
type Field struct {
	Path     string
	Kind     string
	Nullable bool
	Required bool
	Live     bool
	Secret   bool
	UI       string
}

// Fields yields every property of the shape in document order, a parent before
// its children: the properties of a nested object follow it, so a nested struct
// and a pointer to one are walked, while the items of an array and the values of
// a map are not (a change inside them is a change of the array or map field).
func (t *Type) Fields() iter.Seq[Field] {
	return func(yield func(Field) bool) {
		walkFields(t.schema, "", yield)
	}
}

func walkFields(s *jsonschema.Schema, path string, yield func(Field) bool) bool {
	for _, name := range propertyOrder(s) {
		ps := s.Properties[name]
		f := fieldOf(ps, pointer(path, name))
		f.Required = slices.Contains(s.Required, name)
		if !yield(f) || !walkFields(ps, f.Path, yield) {
			return false
		}
	}
	return true
}

// propertyOrder returns the property names of s in declaration order when the
// schema records it, then any others sorted.
func propertyOrder(s *jsonschema.Schema) []string {
	if s == nil || len(s.Properties) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Properties))
	for _, name := range s.PropertyOrder {
		if _, ok := s.Properties[name]; ok && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, name := range sortedKeys(s.Properties) {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func fieldOf(s *jsonschema.Schema, path string) Field {
	f := Field{Path: path, Kind: s.Type}
	for _, k := range s.Types {
		if k == "null" {
			f.Nullable = true
		} else if f.Kind == "" {
			f.Kind = k
		}
	}
	f.Live, _ = s.Extra["x-dsh-live"].(bool)
	f.Secret, _ = s.Extra["x-dsh-secret"].(bool)
	f.UI, _ = s.Extra["x-dsh-ui"].(string)
	return f
}

// Changed compares two values of t's shape field by field, through their wire
// form, and returns the JSON Pointer of every changed field in document order,
// and whether every one of them is tagged live or lies below a live field. A
// field present in one value and absent in the other is changed; a nested object
// is compared field by field unless one side is not an object. With no change it
// returns no path and liveOnly true: nothing needs a restart. A value that does
// not encode reports the root "" as changed and liveOnly false.
func Changed[T any](t *Type, before, after T) (paths []string, liveOnly bool) {
	a, errA := toInstance(before)
	b, errB := toInstance(after)
	if errA != nil || errB != nil {
		return []string{""}, false
	}
	c := changes{liveOnly: true}
	c.compare(t.schema, a, b, true, true, "", false)
	return c.paths, c.liveOnly
}

type changes struct {
	paths    []string
	liveOnly bool
}

func (c *changes) compare(s *jsonschema.Schema, a, b any, aok, bok bool, path string, live bool) {
	ao, aObj := a.(map[string]any)
	bo, bObj := b.(map[string]any)
	if s != nil && len(s.Properties) > 0 && aok && bok && aObj && bObj {
		for _, name := range propertyOrder(s) {
			ps := s.Properties[name]
			av, ahas := ao[name]
			bv, bhas := bo[name]
			if !ahas && !bhas {
				continue
			}
			c.compare(ps, av, bv, ahas, bhas, pointer(path, name), live || fieldOf(ps, "").Live)
		}
		return
	}
	if aok == bok && equal(a, b) {
		return
	}
	c.paths = append(c.paths, path)
	c.liveOnly = c.liveOnly && live
}
