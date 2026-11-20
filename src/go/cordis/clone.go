package cordis

import "reflect"

// cloneConfig returns a deep copy of a config value, so resolving a candidate (config
// hooks, decoding, the schema) cannot change a stored raw or effective config through
// a shared map, slice or pointer. Maps, slices, arrays, pointers, interfaces and the
// exported fields of structs are copied; shared references are copied once, so a cycle
// is preserved rather than followed forever. Unexported struct fields, channels and
// functions are kept as they are: reflection cannot copy them, and they are not data a
// config hook edits.
func cloneConfig(config any) any {
	if config == nil {
		return nil
	}
	clone := cloneValue(reflect.ValueOf(config), map[cloneKey]reflect.Value{})
	return clone.Interface()
}

type cloneKey struct {
	typ reflect.Type
	ptr uintptr
	len int
}

func cloneValue(v reflect.Value, seen map[cloneKey]reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		key := cloneKey{typ: v.Type(), ptr: v.Pointer()}
		if done, ok := seen[key]; ok {
			return done
		}
		clone := reflect.New(v.Type().Elem())
		seen[key] = clone
		clone.Elem().Set(cloneValue(v.Elem(), seen))
		return clone
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		key := cloneKey{typ: v.Type(), ptr: v.Pointer()}
		if done, ok := seen[key]; ok {
			return done
		}
		clone := reflect.MakeMapWithSize(v.Type(), v.Len())
		seen[key] = clone
		for iter := v.MapRange(); iter.Next(); {
			clone.SetMapIndex(cloneValue(iter.Key(), seen), cloneValue(iter.Value(), seen))
		}
		return clone
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		key := cloneKey{typ: v.Type(), ptr: v.Pointer(), len: v.Len()}
		if done, ok := seen[key]; ok {
			return done
		}
		clone := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		seen[key] = clone
		for i := range v.Len() {
			clone.Index(i).Set(cloneValue(v.Index(i), seen))
		}
		return clone
	case reflect.Array:
		clone := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			clone.Index(i).Set(cloneValue(v.Index(i), seen))
		}
		return clone
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		clone := reflect.New(v.Type()).Elem()
		clone.Set(cloneValue(v.Elem(), seen))
		return clone
	case reflect.Struct:
		clone := reflect.New(v.Type()).Elem()
		clone.Set(v) // unexported fields are kept as they are
		for i := range v.NumField() {
			if field := clone.Field(i); field.CanSet() {
				field.Set(cloneValue(v.Field(i), seen))
			}
		}
		return clone
	default:
		return v
	}
}
