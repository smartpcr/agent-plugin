package agentpreset

import (
	"fmt"
	"reflect"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// RegistryComponent is agent.presets (bundle-preset.md §6.1): it builds the
// registry over the scope table and the host loader's catalogue and evaluator, and
// provides it under Key. Its fiber owns every revision, so they close when it
// unloads. Config is its config; the declarations arrive as agent.preset entries.
var RegistryComponent = cordis.Define(RegistryName, func(ctx *cordis.Context, cfg Config) error {
	scopes, err := scope.TableKey.Get(ctx)
	if err != nil {
		return err
	}
	host, err := loader.Key.Get(ctx)
	if err != nil {
		return err
	}
	cat, ok := host.Resolver().(Catalogue)
	if !ok {
		return fmt.Errorf("agentpreset: the loader resolves with %T, not a catalogue", host.Resolver())
	}
	r, err := New(ctx, Options{Catalogue: cat, Scopes: scopes, Evaluator: host.Evaluator(), Config: cfg})
	if err != nil {
		return err
	}
	_, err = Key.Provide(ctx, r)
	return err
}, cordis.WithInject(scope.TableKey.Name(), loader.Key.Name()))

// DeclarationComponent is agent.preset: an ordinary top-level entry whose config is
// one Declaration. It owns only its registration: unloading it removes the
// declaration and retires its revision.
var DeclarationComponent = cordis.Define(DeclarationName, func(ctx *cordis.Context, d Declaration) error {
	r, err := Key.Get(ctx)
	if err != nil {
		return err
	}
	return r.Declare(ctx, d)
}, cordis.WithInject(RegistryName))

// Items are the catalogue items of the two components.
func Items() []catalog.Item {
	return []catalog.Item{
		{Name: RegistryName, Component: RegistryComponent, Config: reflect.TypeFor[Config](), Provides: []string{RegistryName}},
		{Name: DeclarationName, Component: DeclarationComponent, Config: reflect.TypeFor[Declaration]()},
	}
}
