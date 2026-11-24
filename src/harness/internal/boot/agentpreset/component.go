package agentpreset

import (
	"errors"
	"reflect"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// Host is what agent.presets takes from the launch that mounts it: the catalogue the
// children resolve in (the host's own, which lists agent.presets itself, hence a
// func read at load), the evaluator of the children's expressions, and the harness
// version that is part of every digest.
type Host struct {
	Catalogue Catalogue
	Evaluator loader.Evaluator
	Version   string
}

// RegistryComponent is agent.presets (bundle-preset.md §6.1): it builds the
// registry over the scope table and host's catalogue and evaluator, and provides it
// under Key. Its fiber owns every revision, so they close when it unloads. Config is
// its config; the declarations arrive as agent.preset entries.
func RegistryComponent(host func() Host) *cordis.Component {
	return cordis.Define(RegistryName, func(ctx *cordis.Context, cfg Config) error {
		scopes, err := scope.TableKey.Get(ctx)
		if err != nil {
			return err
		}
		h := host()
		if h.Catalogue == nil {
			return errors.New("agentpreset: agent.presets is mounted without a catalogue")
		}
		r, err := New(ctx, Options{Catalogue: h.Catalogue, Scopes: scopes, Evaluator: h.Evaluator, Version: h.Version, Config: cfg})
		if err != nil {
			return err
		}
		_, err = Key.Provide(ctx, r)
		return err
	}, cordis.WithInject(scope.TableKey.Name()))
}

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

// Items are the catalogue items of the two components; host is read when
// agent.presets loads.
func Items(host func() Host) []catalog.Item {
	return []catalog.Item{
		{Name: RegistryName, Component: RegistryComponent(host), Config: reflect.TypeFor[Config](), Provides: []string{RegistryName}},
		{Name: DeclarationName, Component: DeclarationComponent, Config: reflect.TypeFor[Declaration]()},
	}
}
