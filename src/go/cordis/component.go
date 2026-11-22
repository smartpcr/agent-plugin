package cordis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

// None is the config type of a component that takes no config.
type None = struct{}

// Validator is implemented by config types that check themselves. The default schema
// calls Validate after converting the config; an error fails the load (or rejects an
// Update) as a ConfigError.
type Validator interface {
	Validate() error
}

// Schema resolves a component's config on every load and on Update. It receives the
// config already converted to the component's config type, and returns the value the
// component sees. It runs on the calling goroutine and must not call into the context.
type Schema interface {
	Validate(config any) (any, error)
}

// SchemaFunc adapts a function to Schema.
type SchemaFunc func(config any) (any, error)

// Validate calls f.
func (f SchemaFunc) Validate(config any) (any, error) { return f(config) }

// Component is a unit cordis loads: a name, the services it declares, and how to
// apply it to a context. Components are compared by identity, so define each one once
// (typically as a package-level variable) and reuse it.
type Component struct {
	name     string
	inject   []Dependency
	schema   Schema
	defaults any // func() C
	lenient  bool
	apply    func(ctx *Context, config any) (any, error)
	resolve  func(config any, strict bool) (any, error)
}

// Option configures a component.
type Option func(*Component)

// WithInject declares services the component needs. Its fiber loads only while every
// declared service has an active provider, and reloads when one is replaced.
func WithInject(names ...string) Option {
	return func(c *Component) {
		for _, name := range names {
			c.addInject(Dependency{Name: name})
		}
	}
}

// WithInjectConfig declares a service with per-dependency config, which the service
// reads through Context.InterceptChain below context intercepts.
func WithInjectConfig(name string, config any) Option {
	return func(c *Component) { c.addInject(Dependency{Name: name, Config: config}) }
}

// WithSchema replaces the default schema (which calls Validator.Validate).
func WithSchema(schema Schema) Option {
	return func(c *Component) { c.schema = schema }
}

// LenientConfig exempts the component from WithStrictConfig: unknown keys in its JSON
// config are ignored, as in a tree without the option.
func LenientConfig() Option {
	return func(c *Component) { c.lenient = true }
}

// WithDefaults supplies the config used when none is given, and the base that JSON
// config is decoded onto, so fields missing from the JSON keep their defaults.
// defaults must return the component's config type C.
func WithDefaults[C any](defaults func() C) Option {
	return func(c *Component) { c.defaults = defaults }
}

func (c *Component) addInject(dependency Dependency) {
	if i := slices.IndexFunc(c.inject, func(d Dependency) bool { return d.Name == dependency.Name }); i >= 0 {
		c.inject[i] = dependency
		return
	}
	c.inject = append(c.inject, dependency)
}

// Name returns the component's name.
func (c *Component) Name() string { return c.name }

// Inject returns the component's declared dependencies.
func (c *Component) Inject() []Dependency { return slices.Clone(c.inject) }

func (c *Component) String() string { return "Component <" + c.name + ">" }

// Define makes a function component: apply runs on each load with the resolved config.
func Define[C any](name string, apply func(ctx *Context, config C) error, options ...Option) *Component {
	c := newComponent[C](name, options)
	c.apply = func(ctx *Context, config any) (any, error) {
		//lint:ignore typeassert config is resolved for this component's own config type C (newComponent[C]), so it always holds a C
		return nil, apply(ctx, config.(C))
	}
	return c
}

// Construct makes a constructor component: construct runs on each load and returns
// an instance. An instance implementing Initializer is initialised before the fiber
// becomes Active; one implementing io.Closer is closed when the fiber unloads.
func Construct[C, T any](name string, construct func(ctx *Context, config C) (T, error), options ...Option) *Component {
	c := newComponent[C](name, options)
	c.apply = func(ctx *Context, config any) (any, error) {
		//lint:ignore typeassert config is resolved for this component's own config type C (newComponent[C]), so it always holds a C
		instance, err := construct(ctx, config.(C))
		if err != nil {
			return nil, err
		}
		return instance, nil
	}
	return c
}

func newComponent[C any](name string, options []Option) *Component {
	c := &Component{name: name}
	for _, option := range options {
		option(c)
	}
	defaults := func() C { return zeroConfig[C]() }
	if c.defaults != nil {
		typed, ok := c.defaults.(func() C)
		if !ok {
			var zero C
			panic(fmt.Sprintf("cordis: WithDefaults for %q must return %T, got %T", name, zero, c.defaults))
		}
		defaults = typed
	}
	schema := c.schema
	lenient := c.lenient
	c.resolve = func(input any, strict bool) (any, error) {
		value, err := convertConfig(input, defaults, strict && !lenient)
		if err != nil {
			return nil, err
		}
		if schema != nil {
			resolved, err := schema.Validate(value)
			if err != nil {
				return nil, asConfigError(err)
			}
			if _, ok := resolved.(C); !ok {
				return nil, &ConfigError{Issues: []ConfigIssue{{Message: fmt.Sprintf("schema returned %T, expected %T", resolved, value)}}}
			}
			return resolved, nil
		}
		if err := validate(value); err != nil {
			return nil, asConfigError(err)
		}
		return value, nil
	}
	return c
}

// convertConfig turns a config as handed in (nil, a C, a *C, or JSON) into C. With
// strict set, JSON with an unknown key or trailing data is rejected.
func convertConfig[C any](input any, defaults func() C, strict bool) (C, error) {
	var zero C
	switch value := input.(type) {
	case nil:
		return defaults(), nil
	case C:
		return value, nil
	case *C:
		if value == nil {
			return defaults(), nil
		}
		return *value, nil
	case json.RawMessage:
		return decodeConfig(value, defaults, strict)
	case []byte:
		return decodeConfig(value, defaults, strict)
	case map[string]any:
		raw, err := json.Marshal(value)
		if err != nil {
			return zero, &ConfigError{Issues: []ConfigIssue{{Message: err.Error()}}}
		}
		return decodeConfig(raw, defaults, strict)
	}
	return zero, &ConfigError{Issues: []ConfigIssue{{Message: fmt.Sprintf("expected %T, got %T", zero, input)}}}
}

func decodeConfig[C any](raw []byte, defaults func() C, strict bool) (C, error) {
	value := defaults()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return value, nil
	}
	var zero C
	if !strict {
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return zero, decodeError(err, trimmed, reflect.TypeFor[C]())
		}
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return zero, decodeError(err, trimmed, reflect.TypeFor[C]())
	}
	end := decoder.InputOffset()
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return zero, &ConfigError{Issues: []ConfigIssue{{
			Message: fmt.Sprintf("trailing data after the config at offset %d: %q", end, trailing(trimmed, end)),
		}}}
	}
	return value, nil
}

// decodeError turns a JSON decoding error into a ConfigError whose issue carries the
// full dotted path of the offending field: the unknown key, or the field of a type
// mismatch. data is the JSON that was decoded onto a value of type t.
func decodeError(err error, data []byte, t reflect.Type) error {
	issue := ConfigIssue{Message: err.Error()}
	var typeError *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeError):
		issue.Path = typeError.Field
	case strings.HasPrefix(err.Error(), unknownFieldPrefix):
		name := strings.TrimPrefix(err.Error(), unknownFieldPrefix)
		if end := strings.IndexByte(name, '"'); end >= 0 {
			name = name[:end]
		}
		issue.Message = "unknown key"
		issue.Path = name
		if path, ok := unknownKeyPath(data, t); ok && (path == name || strings.HasSuffix(path, "."+name)) {
			issue.Path = path
		}
	}
	return &ConfigError{Issues: []ConfigIssue{issue}}
}

// unknownFieldPrefix starts the error json.Decoder reports for an unknown key; the
// standard library has no typed error for it.
const unknownFieldPrefix = `json: unknown field "`

// unknownKeyPath finds the first key in data, in document order, that names no field
// of the struct it is decoded into, and returns its dotted path. json.Decoder reports
// only the leaf name of an unknown key; this recovers the rest of the path, built the
// way UnmarshalTypeError.Field is (struct field names; map keys and array indices are
// not segments), so both kinds of issue report paths alike.
func unknownKeyPath(data []byte, t reflect.Type) (string, bool) {
	walker := keyWalker{decoder: json.NewDecoder(bytes.NewReader(data))}
	path, found, err := walker.value(t, nil)
	if err != nil || !found {
		return "", false
	}
	return path, true
}

type keyWalker struct {
	decoder *json.Decoder
}

var jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// value walks the next JSON value as decoded into t. It stops at the first unknown
// key, returning its path.
func (w *keyWalker) value(t reflect.Type, path []string) (string, bool, error) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() == reflect.Interface || reflect.PointerTo(t).Implements(jsonUnmarshalerType) {
		return "", false, w.skip()
	}
	token, err := w.decoder.Token()
	if err != nil {
		return "", false, err
	}
	switch token {
	case json.Delim('{'):
		return w.object(t, path)
	case json.Delim('['):
		if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
			return "", false, w.rest()
		}
		for w.decoder.More() {
			if path, found, err := w.value(t.Elem(), path); err != nil || found {
				return path, found, err
			}
		}
		_, err := w.decoder.Token()
		return "", false, err
	}
	return "", false, nil
}

// object walks the members of a JSON object, its opening brace already read.
func (w *keyWalker) object(t reflect.Type, path []string) (string, bool, error) {
	switch {
	case t.Kind() == reflect.Map:
		for w.decoder.More() {
			if _, err := w.decoder.Token(); err != nil {
				return "", false, err
			}
			if path, found, err := w.value(t.Elem(), path); err != nil || found {
				return path, found, err
			}
		}
	case t.Kind() == reflect.Struct:
		fields := jsonFields(t)
		for w.decoder.More() {
			token, err := w.decoder.Token()
			if err != nil {
				return "", false, err
			}
			key, _ := token.(string)
			field, ok := fields.lookup(key)
			if !ok {
				return strings.Join(append(slices.Clone(path), key), "."), true, nil
			}
			if path, found, err := w.value(field.typ, append(slices.Clone(path), field.path...)); err != nil || found {
				return path, found, err
			}
		}
	default:
		return "", false, w.rest()
	}
	_, err := w.decoder.Token()
	return "", false, err
}

// skip consumes the next JSON value whole.
func (w *keyWalker) skip() error {
	token, err := w.decoder.Token()
	if err != nil {
		return err
	}
	if token == json.Delim('{') || token == json.Delim('[') {
		return w.rest()
	}
	return nil
}

// rest consumes the remainder of an object or array whose opening delimiter is read.
func (w *keyWalker) rest() error {
	for depth := 1; depth > 0; {
		token, err := w.decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
	return nil
}

// jsonField is a struct field as encoding/json sees it: its JSON name, its type, and
// the path segments the decoder records for it (embedding struct names, then name).
type jsonField struct {
	name  string
	typ   reflect.Type
	path  []string
	depth int
}

type jsonFieldSet []jsonField

// lookup matches a key the way encoding/json does: exactly, then case-insensitively.
func (fields jsonFieldSet) lookup(key string) (jsonField, bool) {
	for _, field := range fields {
		if field.name == key {
			return field, true
		}
	}
	for _, field := range fields {
		if strings.EqualFold(field.name, key) {
			return field, true
		}
	}
	return jsonField{}, false
}

// jsonFields lists the fields encoding/json decodes into for struct type t, applying
// its rules for tags, embedding and name conflicts.
func jsonFields(t reflect.Type) jsonFieldSet {
	type candidate struct {
		jsonField
		tagged bool
	}
	var candidates []candidate
	for _, field := range reflect.VisibleFields(t) {
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if field.Anonymous && name == "" && fieldType.Kind() == reflect.Struct {
			continue // its fields are promoted, and VisibleFields lists them
		}
		if !field.IsExported() {
			continue
		}
		tagged := name != ""
		if !tagged {
			name = field.Name
		}
		var path []string
		outer := t
		for _, index := range field.Index[:len(field.Index)-1] {
			embedded := outer.Field(index)
			path = append(path, embedded.Name)
			outer = embedded.Type
			if outer.Kind() == reflect.Pointer {
				outer = outer.Elem()
			}
		}
		path = append(path, name)
		candidates = append(candidates, candidate{jsonField{name: name, typ: field.Type, path: path, depth: len(field.Index)}, tagged})
	}
	var fields jsonFieldSet
	for _, c := range candidates {
		// Of the fields sharing a name, the shallowest wins; at equal depth a single
		// tagged field wins; otherwise the name is ambiguous and no field decodes it.
		var rivals []candidate
		for _, other := range candidates {
			if other.name == c.name && other.depth <= c.depth {
				rivals = append(rivals, other)
			}
		}
		if len(rivals) == 1 {
			fields = append(fields, c.jsonField)
			continue
		}
		if !c.tagged || slices.ContainsFunc(rivals, func(other candidate) bool { return other.depth < c.depth }) {
			continue
		}
		tagged := 0
		for _, other := range rivals {
			if other.tagged {
				tagged++
			}
		}
		if tagged == 1 {
			fields = append(fields, c.jsonField)
		}
	}
	return fields
}

// trailing returns a short excerpt of data from offset, for an error message.
func trailing(data []byte, offset int64) string {
	if offset < 0 || offset > int64(len(data)) {
		offset = int64(len(data))
	}
	rest := bytes.TrimSpace(data[offset:])
	if len(rest) > 32 {
		rest = append(slices.Clone(rest[:32]), "..."...)
	}
	return string(rest)
}

// zeroConfig returns the zero C, allocating it when C is a pointer type.
func zeroConfig[C any]() C {
	var zero C
	if t := reflect.TypeFor[C](); t.Kind() == reflect.Pointer {
		//lint:ignore typeassert C is a pointer type here, and reflect.New of its element type yields exactly a C
		return reflect.New(t.Elem()).Interface().(C)
	}
	return zero
}

func validate[C any](value C) error {
	if validator, ok := any(value).(Validator); ok {
		return validator.Validate()
	}
	if validator, ok := any(&value).(Validator); ok {
		return validator.Validate()
	}
	return nil
}
