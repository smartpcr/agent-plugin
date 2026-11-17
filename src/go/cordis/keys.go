package cordis

import "fmt"

// IsolateKey names a service realm. Two contexts see the same provider for a name only
// when they map that name to the same key; keys compare by identity, like upstream's
// symbols.
type IsolateKey struct {
	label string
}

// NewIsolateKey returns a fresh realm key. The label is for diagnostics only.
func NewIsolateKey(label string) *IsolateKey { return &IsolateKey{label: label} }

func (k *IsolateKey) String() string { return "IsolateKey(" + k.label + ")" }

// ServiceKey is a typed service name. It gives Get, Provide and Set a static type
// without changing how services are resolved: by name.
type ServiceKey[T any] struct {
	name string
}

// NewServiceKey returns the key for the service registered under name.
func NewServiceKey[T any](name string) ServiceKey[T] { return ServiceKey[T]{name: name} }

// Name returns the service name.
func (k ServiceKey[T]) Name() string { return k.name }

// Get resolves the service from ctx, as Context.Get does, and asserts its type.
func (k ServiceKey[T]) Get(ctx *Context) (T, error) {
	var zero T
	value, err := ctx.Get(k.name)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("cordis: service %q is %T, not %T", k.name, value, zero)
	}
	return typed, nil
}

// Provide registers value as the service, as an effect of ctx's fiber.
func (k ServiceKey[T]) Provide(ctx *Context, value T) (*EffectHandle, error) {
	return ctx.Provide(k.name, value)
}

// Set replaces the value of a service that ctx's fiber provides.
func (k ServiceKey[T]) Set(ctx *Context, value T) error { return ctx.Set(k.name, value) }
