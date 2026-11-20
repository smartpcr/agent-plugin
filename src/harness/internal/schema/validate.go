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
// annotations only. A document using any other keyword (if/then, contains, $id, a
// non-local $ref...) is validated by the module instead.
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
	"Ref": true, "Defs": true, "Definitions": true,
}

// children returns the subschemas of s that the subset defines.
func children(s *jsonschema.Schema) []*jsonschema.Schema {
	var out []*jsonschema.Schema
	for _, c := range subsetChildren(s) {
		out = append(out, c.s)
	}
	return out
}

// localRef splits a $ref the subset resolves itself: "#", "#/$defs/<name>" or
// "#/definitions/<name>". It reports false for any other reference.
func localRef(ref string) (kw, name string, ok bool) {
	if ref == "#" {
		return "", "", true
	}
	if strings.Contains(ref, "%") {
		return "", "", false
	}
	for _, kw := range []string{"$defs", "definitions"} {
		if name, found := strings.CutPrefix(ref, "#/"+kw+"/"); found && !strings.Contains(name, "/") {
			return kw, strings.NewReplacer("~1", "/", "~0", "~").Replace(name), true
		}
	}
	return "", "", false
}

// target resolves a local $ref against root; nil when it names nothing.
func target(root *jsonschema.Schema, ref string) *jsonschema.Schema {
	kw, name, ok := localRef(ref)
	switch {
	case !ok:
		return nil
	case kw == "":
		return root
	case kw == "$defs":
		return root.Defs[name]
	}
	return root.Definitions[name]
}

func inSubset(s *jsonschema.Schema) bool {
	v := reflect.ValueOf(s).Elem()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.IsExported() && !subsetFields[f.Name] && !v.Field(i).IsZero() {
			return false
		}
	}
	if _, _, ok := localRef(s.Ref); s.Ref != "" && !ok {
		return false
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
type validator struct {
	issues Issues
	root   *jsonschema.Schema
	exact  exactTable
	// active holds the $ref targets being applied at a path, so a reference cycle
	// that does not descend into the value ends instead of recursing forever.
	active map[visit]bool
}

type visit struct {
	s    *jsonschema.Schema
	path string
}

func newValidator(root *jsonschema.Schema, exact exactTable) *validator {
	return &validator{root: root, exact: exact, active: map[visit]bool{}}
}

// sub returns a fresh validator sharing v's document.
func (v *validator) sub() *validator {
	return &validator{root: v.root, exact: v.exact, active: v.active}
}

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
	if len(s.AnyOf) > 0 && v.count(s.AnyOf, inst, path) == 0 {
		v.add(path, "matches none of the anyOf shapes")
	}
	if len(s.OneOf) > 0 {
		if n := v.count(s.OneOf, inst, path); n != 1 {
			v.add(path, "matches %d of the oneOf shapes, not exactly one", n)
		}
	}
	if s.Not != nil && v.count([]*jsonschema.Schema{s.Not}, inst, path) == 1 {
		v.add(path, "matches the shape it must not match")
	}
	if s.Ref != "" {
		t := target(v.root, s.Ref)
		if t == nil {
			v.add(path, "refers to %s, which the document does not define", s.Ref)
			return
		}
		if k := (visit{t, path}); !v.active[k] {
			v.active[k] = true
			v.check(t, inst, path)
			delete(v.active, k)
		}
	}
}

// count returns how many of subs inst conforms to.
func (v *validator) count(subs []*jsonschema.Schema, inst any, path string) int {
	n := 0
	for _, s := range subs {
		sv := v.sub()
		sv.check(s, inst, path)
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
	// cmpTo compares d with a declared bound: the exact literal when the document
	// gave one, else the float64 field, where ±Inf compare as such and NaN fails.
	cmpTo := func(key string) (c int, lit string, ok, set bool) {
		if n, found := v.exact.bound(s, key); found {
			return d.cmp(n.d), n.lit, true, true
		}
		f := *floatField(s, key)
		if f == nil {
			return 0, "", false, false
		}
		lit = strconv.FormatFloat(*f, 'g', -1, 64)
		switch {
		case math.IsInf(*f, 1):
			return -1, lit, true, true
		case math.IsInf(*f, -1):
			return 1, lit, true, true
		}
		b, ok := toDecimal(*f)
		return d.cmp(b), lit, ok, true
	}
	if c, lit, ok, set := cmpTo("minimum"); set && (!ok || c < 0) {
		v.add(path, "must be at least %s", lit)
	}
	if c, lit, ok, set := cmpTo("maximum"); set && (!ok || c > 0) {
		v.add(path, "must be at most %s", lit)
	}
	if c, lit, ok, set := cmpTo("exclusiveMinimum"); set && (!ok || c <= 0) {
		v.add(path, "must be above %s", lit)
	}
	if c, lit, ok, set := cmpTo("exclusiveMaximum"); set && (!ok || c >= 0) {
		v.add(path, "must be below %s", lit)
	}
	var m numLit
	if n, found := v.exact.bound(s, "multipleOf"); found {
		m = n
	} else if s.MultipleOf != nil {
		md, ok := toDecimal(*s.MultipleOf)
		if !ok {
			v.add(path, "must be a multiple of %v", *s.MultipleOf)
			return
		}
		m = numLit{strconv.FormatFloat(*s.MultipleOf, 'g', -1, 64), md}
	} else {
		return
	}
	if !m.d.neg && !m.d.isZero() && !d.multipleOf(m.d) {
		v.add(path, "must be a multiple of %s", m.lit)
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
