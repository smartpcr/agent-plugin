package schema

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// A duration's one wire form is its text in Go duration syntax ("30s", "1h30m",
// "500ms"), so no reader multiplies a bare number by a unit it has to guess. A
// time.Duration field derives to a string with DurationPattern; Decode reads it
// with time.ParseDuration; Marshal writes it with time.Duration.String. A type
// that keeps time.Duration fields and is also encoded by encoding/json elsewhere
// routes its MarshalJSON and UnmarshalJSON through Marshal and Unmarshal, so both
// paths write the same bytes. Duration is the same rule as a named type, for a
// value that must encode itself (inside an interface, say).

// DurationPattern is the derived pattern of a duration: what time.ParseDuration
// accepts, as text. A value the pattern admits can still be out of range; Decode
// reports that as an issue.
const DurationPattern = `^[-+]?(0|(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h))+)$`

var durationType = reflect.TypeFor[time.Duration]()

func durationSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: DurationPattern}
}

// isDuration reports a type whose wire form is a duration's text.
func isDuration(t reflect.Type) bool {
	return t == durationType || t == reflect.TypeFor[Duration]()
}

// Duration is a time.Duration whose JSON form is its text, "30s".
type Duration time.Duration

// String is time.Duration's text.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalJSON writes the text.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON reads the text with time.ParseDuration; a number is refused.
func (d *Duration) UnmarshalJSON(b []byte) error {
	v, err := parseDuration(b)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func parseDuration(b []byte) (time.Duration, error) {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return 0, fmt.Errorf("a duration is text such as \"30s\", not %s", b)
	}
	return durationText(s)
}

func durationText(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("is not a duration such as \"30s\": %q", s)
	}
	return d, nil
}

// Marshal is json.Marshal with every time.Duration of v written as its text. It
// keeps encoding/json's key order and bytes otherwise. A value of a type with
// its own MarshalJSON or MarshalText is left to that method, and an interface
// value is written as encoding/json writes it.
func Marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || v == nil || !hasDurations(reflect.TypeOf(v), true) {
		return b, err
	}
	root, err := readOrdered(b)
	if err != nil {
		return nil, err
	}
	root, err = writeDurations(reflect.TypeOf(v), root)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeOrdered(&buf, root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal is json.Unmarshal into v, a pointer, with every time.Duration read
// from its text. Unknown fields are ignored, as json.Unmarshal ignores them; a
// duration given as a number, or as text time.ParseDuration refuses, is an error
// naming its path.
func Unmarshal(raw []byte, v any) error {
	t := reflect.TypeOf(v)
	if t == nil || !hasDurations(t, false) {
		return json.Unmarshal(raw, v)
	}
	inst, err := parse(raw)
	if err != nil {
		return err
	}
	var issues Issues
	inst = readDurations(t, inst, "", &issues)
	if len(issues) > 0 {
		return issues
	}
	b, err := json.Marshal(inst)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var (
	marshalerType       = reflect.TypeFor[json.Marshaler]()
	unmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// selfCoded reports a type that encodes (or decodes) itself, so its fields are
// none of this package's business.
func selfCoded(t reflect.Type, encode bool) bool {
	if t.Kind() == reflect.Interface {
		return true
	}
	p := reflect.PointerTo(t)
	if encode {
		return t.Implements(marshalerType) || p.Implements(marshalerType) ||
			t.Implements(textMarshalerType) || p.Implements(textMarshalerType)
	}
	return p.Implements(unmarshalerType) || p.Implements(textUnmarshalerType)
}

type durKey struct {
	t      reflect.Type
	encode bool
}

var durCache sync.Map // durKey -> bool

// hasDurations reports whether a value of t can hold a time.Duration that this
// package must convert: one not behind a type that codes itself.
func hasDurations(t reflect.Type, encode bool) bool {
	key := durKey{t, encode}
	if v, ok := durCache.Load(key); ok {
		//lint:ignore typeassert durCache holds only bool values; this function is its only writer
		return v.(bool)
	}
	found := reaches(t, encode, map[reflect.Type]bool{})
	durCache.Store(key, found)
	return found
}

func reaches(t reflect.Type, encode bool, seen map[reflect.Type]bool) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == durationType {
		return true
	}
	if seen[t] || selfCoded(t, encode) {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return reaches(t.Elem(), encode, seen)
	case reflect.Struct:
		for _, f := range reflect.VisibleFields(t) {
			if _, ok := jsonName(f); ok && !f.Anonymous && reaches(f.Type, encode, seen) {
				return true
			}
		}
	}
	return false
}

// readDurations rewrites, in inst, every duration text at a time.Duration of t to
// its nanoseconds, which encoding/json then decodes. A number there, or text that
// does not parse, is an issue.
func readDurations(t reflect.Type, inst any, path string, issues *Issues) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == durationType {
		switch x := inst.(type) {
		case string:
			d, err := durationText(x)
			if err != nil {
				*issues = append(*issues, Issue{Path: path, Message: err.Error()})
				return inst
			}
			return json.Number(strconv.FormatInt(int64(d), 10))
		case nil:
			return inst
		}
		*issues = append(*issues, Issue{Path: path, Message: fmt.Sprintf("must be a duration such as \"30s\", not %s", kindOf(inst))})
		return inst
	}
	if selfCoded(t, false) {
		return inst
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if arr, ok := inst.([]any); ok {
			for i, e := range arr {
				arr[i] = readDurations(t.Elem(), e, pointer(path, strconv.Itoa(i)), issues)
			}
		}
	case reflect.Map:
		if obj, ok := inst.(map[string]any); ok {
			for _, k := range sortedKeys(obj) {
				obj[k] = readDurations(t.Elem(), obj[k], pointer(path, k), issues)
			}
		}
	case reflect.Struct:
		if obj, ok := inst.(map[string]any); ok {
			for _, f := range reflect.VisibleFields(t) {
				name, ok := jsonName(f)
				if e, has := obj[name]; ok && has && !f.Anonymous {
					obj[name] = readDurations(f.Type, e, pointer(path, name), issues)
				}
			}
		}
	}
	return inst
}

// writeDurations rewrites, in an ordered tree encoding/json wrote for a value of
// t, every time.Duration's nanoseconds to its text.
func writeDurations(t reflect.Type, node any) (any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == durationType {
		n, ok := node.(json.Number)
		if !ok {
			return node, nil
		}
		ns, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("schema: duration %s: %w", n, err)
		}
		return time.Duration(ns).String(), nil
	}
	if selfCoded(t, true) {
		return node, nil
	}
	var err error
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if arr, ok := node.([]any); ok {
			for i, e := range arr {
				if arr[i], err = writeDurations(t.Elem(), e); err != nil {
					return nil, err
				}
			}
		}
	case reflect.Map:
		if obj, ok := node.(*ordered); ok {
			for _, k := range obj.keys {
				if obj.vals[k], err = writeDurations(t.Elem(), obj.vals[k]); err != nil {
					return nil, err
				}
			}
		}
	case reflect.Struct:
		if obj, ok := node.(*ordered); ok {
			for _, f := range reflect.VisibleFields(t) {
				name, ok := jsonName(f)
				if e, has := obj.vals[name]; ok && has && !f.Anonymous {
					if obj.vals[name], err = writeDurations(f.Type, e); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return node, nil
}
