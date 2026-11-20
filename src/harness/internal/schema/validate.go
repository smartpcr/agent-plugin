package schema

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

// subsetFields are the Schema fields this package's validator enforces or that are
// annotations only. A document using any other keyword ($ref, if/then, contains...)
// is validated by the module instead.
var subsetFields = map[string]bool{
	"Schema": true, "Comment": true, "Title": true, "Description": true, "Default": true,
	"Deprecated": true, "ReadOnly": true, "WriteOnly": true, "Examples": true, "Format": true,
	"Extra": true, "PropertyOrder": true,
	"Type": true, "Types": true, "Enum": true, "Const": true,
	"MultipleOf": true, "Minimum": true, "Maximum": true, "ExclusiveMinimum": true, "ExclusiveMaximum": true,
	"MinLength": true, "MaxLength": true, "Pattern": true,
	"Items": true, "MinItems": true, "MaxItems": true, "UniqueItems": true,
	"Required": true, "Properties": true, "AdditionalProperties": true,
	"MinProperties": true, "MaxProperties": true,
	"AllOf": true, "AnyOf": true, "OneOf": true, "Not": true,
}

// children returns the subschemas of s that the subset defines.
func children(s *jsonschema.Schema) []*jsonschema.Schema {
	out := []*jsonschema.Schema{s.Items, s.AdditionalProperties, s.Not}
	out = append(out, s.AllOf...)
	out = append(out, s.AnyOf...)
	out = append(out, s.OneOf...)
	for _, p := range s.Properties {
		out = append(out, p)
	}
	return slices.DeleteFunc(out, func(c *jsonschema.Schema) bool { return c == nil })
}

func inSubset(s *jsonschema.Schema) bool {
	v := reflect.ValueOf(s).Elem()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.IsExported() && !subsetFields[f.Name] && !v.Field(i).IsZero() {
			return false
		}
	}
	for _, c := range children(s) {
		if !inSubset(c) {
			return false
		}
	}
	return true
}

var patterns sync.Map // string -> *regexp.Regexp

func compilePatterns(s *jsonschema.Schema) error {
	if s.Pattern != "" {
		if _, ok := patterns.Load(s.Pattern); !ok {
			re, err := regexp.Compile(s.Pattern)
			if err != nil {
				return fmt.Errorf("pattern %q: %w", s.Pattern, err)
			}
			patterns.Store(s.Pattern, re)
		}
	}
	for _, c := range children(s) {
		if err := compilePatterns(c); err != nil {
			return err
		}
	}
	return nil
}

// validator collects every violation of one value.
type validator struct{ issues Issues }

func (v *validator) add(path, format string, args ...any) {
	v.issues = append(v.issues, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

func pointer(path, key string) string {
	return path + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
}

// isFalse reports the schema "false", which jsonschema-go spells {"not": {}}.
func isFalse(s *jsonschema.Schema) bool {
	return s.Not != nil && reflect.DeepEqual(*s.Not, jsonschema.Schema{}) &&
		reflect.DeepEqual(*s, jsonschema.Schema{Not: s.Not})
}

func kindOf(inst any) string {
	switch inst.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// number reads an instance number exactly.
func number(inst any) (decimal, bool) {
	n, ok := inst.(json.Number)
	if !ok {
		return decimal{}, false
	}
	return parseDecimal(string(n))
}

func matchesType(t string, inst any) bool {
	k := kindOf(inst)
	switch t {
	case "integer":
		d, ok := number(inst)
		return ok && d.isInteger()
	case k:
		return true
	}
	return false
}

// norm makes enum and const values comparable: every number, of the instance or of
// the schema, becomes its exact decimal.
func norm(v any) any {
	if d, ok := toDecimal(v); ok {
		return d
	}
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = norm(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = norm(e)
		}
		return out
	}
	return v
}

func equal(a, b any) bool { return reflect.DeepEqual(norm(a), norm(b)) }

func (v *validator) check(s *jsonschema.Schema, inst any, path string) {
	if s == nil {
		return
	}
	if isFalse(s) {
		v.add(path, "is not allowed")
		return
	}
	types := s.Types
	if s.Type != "" {
		types = []string{s.Type}
	}
	if len(types) > 0 && !slices.ContainsFunc(types, func(t string) bool { return matchesType(t, inst) }) {
		v.add(path, "must be %s, not %s", strings.Join(types, " or "), kindOf(inst))
		return
	}
	if s.Enum != nil && !slices.ContainsFunc(s.Enum, func(e any) bool { return equal(e, inst) }) {
		b, _ := json.Marshal(s.Enum)
		v.add(path, "must be one of %s", b)
	}
	if s.Const != nil && !equal(*s.Const, inst) {
		b, _ := json.Marshal(*s.Const)
		v.add(path, "must be %s", b)
	}
	switch x := inst.(type) {
	case json.Number:
		v.checkNumber(s, x, path)
	case string:
		v.checkString(s, x, path)
	case []any:
		v.checkArray(s, x, path)
	case map[string]any:
		v.checkObject(s, x, path)
	}
	for _, sub := range s.AllOf {
		v.check(sub, inst, path)
	}
	if len(s.AnyOf) > 0 && count(s.AnyOf, inst, path) == 0 {
		v.add(path, "matches none of the anyOf shapes")
	}
	if len(s.OneOf) > 0 {
		if n := count(s.OneOf, inst, path); n != 1 {
			v.add(path, "matches %d of the oneOf shapes, not exactly one", n)
		}
	}
	if s.Not != nil && count([]*jsonschema.Schema{s.Not}, inst, path) == 1 {
		v.add(path, "matches the shape it must not match")
	}
}

// count returns how many of subs inst conforms to.
func count(subs []*jsonschema.Schema, inst any, path string) int {
	n := 0
	for _, sub := range subs {
		var sv validator
		sv.check(sub, inst, path)
		if len(sv.issues) == 0 {
			n++
		}
	}
	return n
}

func (v *validator) checkNumber(s *jsonschema.Schema, n json.Number, path string) {
	d, ok := number(n)
	if !ok {
		v.add(path, "is not a number")
		return
	}
	// cmpTo compares d with a schema bound; ±Inf compare as such and NaN fails.
	cmpTo := func(bound float64) (int, bool) {
		switch {
		case math.IsInf(bound, 1):
			return -1, true
		case math.IsInf(bound, -1):
			return 1, true
		}
		b, ok := toDecimal(bound)
		return d.cmp(b), ok
	}
	if b := s.Minimum; b != nil {
		if c, ok := cmpTo(*b); !ok || c < 0 {
			v.add(path, "must be at least %v", *b)
		}
	}
	if b := s.Maximum; b != nil {
		if c, ok := cmpTo(*b); !ok || c > 0 {
			v.add(path, "must be at most %v", *b)
		}
	}
	if b := s.ExclusiveMinimum; b != nil {
		if c, ok := cmpTo(*b); !ok || c <= 0 {
			v.add(path, "must be above %v", *b)
		}
	}
	if b := s.ExclusiveMaximum; b != nil {
		if c, ok := cmpTo(*b); !ok || c >= 0 {
			v.add(path, "must be below %v", *b)
		}
	}
	if m := s.MultipleOf; m != nil && *m > 0 {
		if md, ok := toDecimal(*m); !ok || !d.multipleOf(md) {
			v.add(path, "must be a multiple of %v", *m)
		}
	}
}

func (v *validator) checkString(s *jsonschema.Schema, str string, path string) {
	n := utf8.RuneCountInString(str)
	if s.MinLength != nil && n < *s.MinLength {
		v.add(path, "must be at least %d characters", *s.MinLength)
	}
	if s.MaxLength != nil && n > *s.MaxLength {
		v.add(path, "must be at most %d characters", *s.MaxLength)
	}
	if s.Pattern != "" {
		if re, ok := patterns.Load(s.Pattern); ok && !re.(*regexp.Regexp).MatchString(str) {
			v.add(path, "must match %s", s.Pattern)
		}
	}
}

func (v *validator) checkArray(s *jsonschema.Schema, arr []any, path string) {
	if s.MinItems != nil && len(arr) < *s.MinItems {
		v.add(path, "must have at least %d items", *s.MinItems)
	}
	if s.MaxItems != nil && len(arr) > *s.MaxItems {
		v.add(path, "must have at most %d items", *s.MaxItems)
	}
	if s.UniqueItems {
		for i := range arr {
			if slices.ContainsFunc(arr[:i], func(e any) bool { return equal(e, arr[i]) }) {
				v.add(pointer(path, strconv.Itoa(i)), "repeats an earlier item")
			}
		}
	}
	for i, e := range arr {
		v.check(s.Items, e, pointer(path, strconv.Itoa(i)))
	}
}

func (v *validator) checkObject(s *jsonschema.Schema, obj map[string]any, path string) {
	for _, name := range s.Required {
		if _, ok := obj[name]; !ok {
			v.add(pointer(path, name), "is required")
		}
	}
	if s.MinProperties != nil && len(obj) < *s.MinProperties {
		v.add(path, "must have at least %d properties", *s.MinProperties)
	}
	if s.MaxProperties != nil && len(obj) > *s.MaxProperties {
		v.add(path, "must have at most %d properties", *s.MaxProperties)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		p := pointer(path, k)
		if ps, ok := s.Properties[k]; ok {
			v.check(ps, obj[k], p)
			continue
		}
		if s.AdditionalProperties != nil && isFalse(s.AdditionalProperties) {
			v.add(p, "is not a known field")
			continue
		}
		v.check(s.AdditionalProperties, obj[k], p)
	}
}
