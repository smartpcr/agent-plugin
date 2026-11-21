package tools

import (
	"fmt"
	"reflect"
)

// isolate deep-copies v, an already decoded and validated input, without
// decoding or validating it again: slices, maps, arrays, pointers, interfaces
// and exported struct fields are copied recursively, aliasing within v is kept
// and cycles terminate. Unexported struct fields are copied by value, as a
// listener outside the declaring package cannot reach them. A value holding a
// non-nil channel, function or unsafe pointer cannot be isolated and is an
// error, never a silently shared value.
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

func (c copier) copy(v reflect.Value, path string) (reflect.Value, error) {
	out := reflect.New(v.Type()).Elem()
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
		out.Set(v)
		t := v.Type()
		for i := range t.NumField() {
			if !t.Field(i).IsExported() {
				continue
			}
			e, err := c.copy(v.Field(i), path+"."+t.Field(i).Name)
			if err != nil {
				return out, err
			}
			out.Field(i).Set(e)
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
