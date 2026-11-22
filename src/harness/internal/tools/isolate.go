package tools

import (
	"fmt"
	"reflect"
	"time"
	//lint:ignore typeassert copying unexported struct fields needs reflect.NewAt with unsafe.Pointer of the field's address
	"unsafe"
)

// isolate deep-copies v, an already decoded and validated input, without
// decoding or validating it again: slices, maps, arrays, pointers, interfaces
// and struct fields, unexported ones included, are copied recursively, so not
// even a method of an opaque value that mutates its private state can reach
// another copy; aliasing within v is kept and cycles terminate. A value holding
// a non-nil channel, function or unsafe pointer, in any field, cannot be
// isolated and is an error, never a silently shared value. Only the immutable
// time.Time and *time.Location are shared as they are.
func isolate(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	c := copier{seen: map[visit]reflect.Value{}}
	out, err := c.copy(reflect.ValueOf(v), "")
	if err != nil {
		return nil, err
	}
	return out.Interface(), nil
}

type visit struct {
	ptr uintptr
	typ reflect.Type
}

type copier struct{ seen map[visit]reflect.Value }

// immutable are types whose values no method mutates; they are shared, not copied.
var immutable = map[reflect.Type]bool{
	reflect.TypeFor[time.Time]():      true,
	reflect.TypeFor[*time.Location](): true,
}

func (c copier) copy(v reflect.Value, path string) (reflect.Value, error) {
	out := reflect.New(v.Type()).Elem()
	if immutable[v.Type()] {
		out.Set(v)
		return out, nil
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		if !v.IsNil() {
			return out, fmt.Errorf("%s holds a %s, which cannot be copied", where(path), v.Kind())
		}
		return out, nil
	case reflect.Pointer:
		if v.IsNil() {
			return out, nil
		}
		k := visit{v.Pointer(), v.Type()}
		if p, ok := c.seen[k]; ok {
			return p, nil
		}
		p := reflect.New(v.Type().Elem())
		c.seen[k] = p
		e, err := c.copy(v.Elem(), path)
		if err != nil {
			return out, err
		}
		p.Elem().Set(e)
		return p, nil
	case reflect.Interface:
		if v.IsNil() {
			return out, nil
		}
		e, err := c.copy(v.Elem(), path)
		if err != nil {
			return out, err
		}
		out.Set(e)
		return out, nil
	case reflect.Slice:
		if v.IsNil() {
			return out, nil
		}
		k := visit{v.Pointer(), v.Type()}
		if s, ok := c.seen[k]; ok && s.Len() == v.Len() {
			return s, nil
		}
		s := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		c.seen[k] = s
		for i := range v.Len() {
			e, err := c.copy(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return out, err
			}
			s.Index(i).Set(e)
		}
		return s, nil
	case reflect.Array:
		for i := range v.Len() {
			e, err := c.copy(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return out, err
			}
			out.Index(i).Set(e)
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return out, nil
		}
		k := visit{v.Pointer(), v.Type()}
		if m, ok := c.seen[k]; ok {
			return m, nil
		}
		m := reflect.MakeMapWithSize(v.Type(), v.Len())
		c.seen[k] = m
		it := v.MapRange()
		for it.Next() {
			key, err := c.copy(it.Key(), path+"{key}")
			if err != nil {
				return out, err
			}
			val, err := c.copy(it.Value(), fmt.Sprintf("%s[%v]", path, it.Key()))
			if err != nil {
				return out, err
			}
			m.SetMapIndex(key, val)
		}
		return m, nil
	case reflect.Struct:
		// Unexported fields are neither readable as interfaces nor settable
		// through reflect; both sides are reached through their addresses.
		src := reflect.New(v.Type()).Elem()
		src.Set(v)
		t := v.Type()
		for i := range t.NumField() {
			sf, df := open(src.Field(i)), open(out.Field(i))
			e, err := c.copy(sf, path+"."+t.Field(i).Name)
			if err != nil {
				return out, err
			}
			df.Set(e)
		}
		return out, nil
	default:
		out.Set(v)
		return out, nil
	}
}

func where(path string) string {
	if path == "" {
		return "the input"
	}
	return "the input at " + path
}

// open is f, a field of an addressable struct, readable and settable even when
// unexported.
func open(f reflect.Value) reflect.Value {
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}
