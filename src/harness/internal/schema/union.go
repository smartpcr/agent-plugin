package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
)

// Variant is one shape of a union: the discriminator value that selects it and
// the Go type a value of that shape decodes into, a struct or a pointer to one.
type Variant struct {
	Value string
	Type  reflect.Type
}

// VariantOf is the Variant of Go type V, selected by value.
func VariantOf[V any](value string) Variant {
	return Variant{Value: value, Type: reflect.TypeFor[V]()}
}

type union struct {
	disc     string
	values   []string // in declaration order
	variants map[string]unionVariant
}

type unionVariant struct {
	typ    *Type
	goType reflect.Type
}

// Union derives one value that is one of several shapes told apart by one string
// field, the discriminator (DH-LIB-ZOD-R9). Each variant is a struct, or a
// pointer to one, assignable to T (typically an interface the variants
// implement) and declaring the discriminator as a string field; its value there
// is fixed to the variant's Value. The document is a oneOf with one alternative
// per variant, in order, each holding the discriminator as a const.
//
// Validate and Decode read the discriminator first: a missing or unknown value is
// one issue at the discriminator's path, and a known one is checked against its
// variant alone, so every issue names the variant's own paths. Decode into a *T
// then decodes into a new value of the variant's type, as that type's own Decode
// would, and stores it in *T.
func Union[T any](discriminator string, variants ...Variant) (*Type, error) {
	target := reflect.TypeFor[T]()
	name := fmt.Sprintf("union %s on %q", target, discriminator)
	bad := func(format string, args ...any) (*Type, error) {
		return nil, fmt.Errorf("schema: %s: "+format, append([]any{name}, args...)...)
	}
	if discriminator == "" {
		return bad("the discriminator is blank")
	}
	if len(variants) == 0 {
		return bad("no variants")
	}
	u := &union{disc: discriminator, variants: map[string]unionVariant{}}
	root := &jsonschema.Schema{}
	exact := exactTable{}
	for _, v := range variants {
		switch {
		case v.Value == "":
			return bad("a variant has a blank value")
		case slices.Contains(u.values, v.Value):
			return bad("the value %q names two variants", v.Value)
		case v.Type == nil:
			return bad("variant %q has no type", v.Value)
		case !v.Type.AssignableTo(target):
			return bad("variant %q: %s is not assignable to %s", v.Value, v.Type, target)
		}
		st := v.Type
		if st.Kind() == reflect.Pointer {
			st = st.Elem()
		}
		if st.Kind() != reflect.Struct {
			return bad("variant %q: %s is not a struct", v.Value, v.Type)
		}
		s, ex, err := derive(st)
		if err != nil {
			return nil, err
		}
		ps := s.Properties[discriminator]
		if ps == nil || ps.Type != "string" {
			return bad("variant %q: %s has no string field %q", v.Value, st, discriminator)
		}
		var c any = v.Value
		ps.Const, ps.Enum = &c, nil
		if !slices.Contains(s.Required, discriminator) {
			s.Required = append(s.Required, discriminator)
		}
		vt, err := build(st.String(), s, false, ex)
		if err != nil {
			return nil, err
		}
		for k, m := range ex {
			exact[k] = m
		}
		root.OneOf = append(root.OneOf, s)
		u.values = append(u.values, v.Value)
		u.variants[v.Value] = unionVariant{typ: vt, goType: v.Type}
	}
	t, err := build(name, root, false, exact)
	if err != nil {
		return nil, err
	}
	t.union = u
	return t, nil
}

// MustUnion is Union for package-level declarations; it panics on an error.
func MustUnion[T any](discriminator string, variants ...Variant) *Type {
	t, err := Union[T](discriminator, variants...)
	if err != nil {
		panic(err)
	}
	return t
}

// variant returns the variant inst selects, or the one issue saying why none.
func (u *union) variant(inst any) (unionVariant, Issues) {
	obj, ok := inst.(map[string]any)
	if !ok {
		return unionVariant{}, Issues{{Path: "", Message: fmt.Sprintf("must be object, not %s", kindOf(inst))}}
	}
	p := pointer("", u.disc)
	v, ok := obj[u.disc]
	if !ok {
		return unionVariant{}, Issues{{Path: p, Message: "is required"}}
	}
	s, isString := v.(string)
	uv, known := u.variants[s]
	if !isString || !known {
		b, _ := json.Marshal(u.values)
		return unionVariant{}, Issues{{Path: p, Message: fmt.Sprintf("must be one of %s", b)}}
	}
	return uv, nil
}

func (u *union) check(inst any) Issues {
	uv, issues := u.variant(inst)
	if issues != nil {
		return issues
	}
	return uv.typ.check(inst)
}

func (u *union) decode(inst any, dst any) Issues {
	uv, issues := u.variant(inst)
	if issues != nil {
		return issues
	}
	dv := reflect.ValueOf(dst)
	if !dv.IsValid() || dv.Kind() != reflect.Pointer || dv.IsNil() {
		return Issues{{Path: "", Message: "does not decode: a union decodes into a non-nil pointer"}}
	}
	elem := dv.Elem()
	if !uv.goType.AssignableTo(elem.Type()) {
		return Issues{{Path: "", Message: fmt.Sprintf("does not decode: %s is not assignable to %s", uv.goType, elem.Type())}}
	}
	holder := reflect.New(uv.goType)
	if uv.goType.Kind() == reflect.Pointer {
		holder = reflect.New(uv.goType.Elem())
	}
	if issues := uv.typ.decode(inst, nil, holder.Interface()); issues != nil {
		return issues
	}
	if uv.goType.Kind() == reflect.Pointer {
		elem.Set(holder)
	} else {
		elem.Set(holder.Elem())
	}
	return nil
}
