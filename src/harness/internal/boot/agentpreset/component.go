package agentpreset

import (
	"errors"
	"reflect"
	"slices"

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

// HostKey is the service under which boot provides the launch's Host on the root,
// before any entry loads: the catalogue and evaluator of the tree itself. A
// RegistryComponent built with a nil host func reads it.
var HostKey = cordis.NewServiceKey[Host]("boot.presets.host")

// RegistryComponent is agent.presets (bundle-preset.md §6.1): it builds the
// registry over the scope table and host's catalogue and evaluator, and provides it
// under Key. Its fiber owns every revision, so they close when it unloads. Config is
// its config; the declarations arrive as agent.preset entries. An update of Config
// (Default, SelectedDefault) is accepted in place: the fiber does not restart, so
// every revision, its leases and its bindings survive and only the default changes.
// A nil host reads the Host boot provides under HostKey.
func RegistryComponent(host func() Host) *cordis.Component {
	return cordis.Define(RegistryName, func(ctx *cordis.Context, cfg Config) error {
		scopes, err := scope.TableKey.Get(ctx)
		if err != nil {
			return err
		}
		var h Host
		if host != nil {
			h = host()
		} else {
			h, _ = HostKey.Lookup(ctx)
		}
		if h.Catalogue == nil {
			return errors.New("agentpreset: agent.presets is mounted without a catalogue")
		}
		r, err := New(ctx, Options{Catalogue: h.Catalogue, Scopes: scopes, Evaluator: h.Evaluator, Version: h.Version, Config: cfg})
		if err != nil {
			return err
		}
		if _, err := cordis.AcceptUpdate(ctx, r.reconfigure); err != nil {
			return err
		}
		_, err = Key.Provide(ctx, r)
		return err
	}, cordis.WithInject(scope.TableKey.Name()))
}

// reconfigure swaps the registry config in place. Config only selects the default,
// which no revision's digest depends on, so no revision is rebuilt or retired.
func (r *Registry) reconfigure(cfg Config) {
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
}

// DeclarationComponent is agent.preset: an ordinary top-level entry whose config is
// one Declaration. It owns only its registration: unloading it removes the
// declaration and retires its revision. An update that keeps the id is accepted in
// place: the declaration is swapped and the set rebuilt once, so the declaration and
// every preset that extends it keep their revisions unless their resolved children
// changed. An update that changes the id restarts the entry.
var DeclarationComponent = cordis.Define(DeclarationName, func(ctx *cordis.Context, d Declaration) error {
	r, err := Key.Get(ctx)
	if err != nil {
		return err
	}
	if err := r.Declare(ctx, d); err != nil {
		return err
	}
	reg := r.registration(d.ID)
	_, err = ctx.OnUpdate(func(resolved any, next func() error) error {
		nd, ok := resolved.(Declaration)
		if !ok || reg == nil {
			return next()
		}
		accepted, err := r.redeclare(reg, nd)
		if !accepted {
			return next()
		}
		return err
	}, false)
	return err
}, cordis.WithInject(RegistryName))

// registration is the agent.preset registration of id.
func (r *Registry) registration(id string) *declared {
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	for _, x := range r.declared {
		if x.decl.ID == id {
			return x
		}
	}
	return nil
}

// redeclare replaces reg's declaration in place by d and rebuilds the set once. It
// declines (false) when reg is no longer registered or d changes the id, which the
// registration's disposer and the duplicate check are keyed on.
func (r *Registry) redeclare(reg *declared, d Declaration) (bool, error) {
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	if !slices.Contains(r.declared, reg) || d.ID != reg.decl.ID {
		return false, nil
	}
	old := reg.decl
	reg.decl = d
	if err := r.syncLocked(); err != nil {
		reg.decl = old
		return true, err
	}
	return true, nil
}

// Items are the catalogue items of the two components; host is read when
// agent.presets loads, and a nil host reads HostKey.
func Items(host func() Host) []catalog.Item {
	return []catalog.Item{
		{Name: RegistryName, Component: RegistryComponent(host), Config: reflect.TypeFor[Config](), Provides: []string{RegistryName}},
		{Name: DeclarationName, Component: DeclarationComponent, Config: reflect.TypeFor[Declaration]()},
	}
}
