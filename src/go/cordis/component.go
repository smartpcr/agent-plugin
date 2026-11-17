package cordis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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
	apply    func(ctx *Context, config any) (any, error)
	resolve  func(config any) (any, error)
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
	c.resolve = func(input any) (any, error) {
		value, err := convertConfig(input, defaults)
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

// convertConfig turns a config as handed in (nil, a C, a *C, or JSON) into C.
func convertConfig[C any](input any, defaults func() C) (C, error) {
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
		return decodeConfig(value, defaults)
	case []byte:
		return decodeConfig(value, defaults)
	case map[string]any:
		raw, err := json.Marshal(value)
		if err != nil {
			return zero, &ConfigError{Issues: []ConfigIssue{{Message: err.Error()}}}
		}
		return decodeConfig(raw, defaults)
	}
	return zero, &ConfigError{Issues: []ConfigIssue{{Message: fmt.Sprintf("expected %T, got %T", zero, input)}}}
}

func decodeConfig[C any](raw []byte, defaults func() C) (C, error) {
	value := defaults()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return value, nil
	}
	if err := json.Unmarshal(trimmed, &value); err != nil {
		var zero C
		return zero, &ConfigError{Issues: []ConfigIssue{{Message: err.Error()}}}
	}
	return value, nil
}

// zeroConfig returns the zero C, allocating it when C is a pointer type.
func zeroConfig[C any]() C {
	var zero C
	if t := reflect.TypeFor[C](); t.Kind() == reflect.Pointer {
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
