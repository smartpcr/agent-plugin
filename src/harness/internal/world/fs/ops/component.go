package ops

import (
	"reflect"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

// Name is the component's catalogue name.
const Name = "fs.ops"

// Component is fs.ops (C32, cycle C-3): the operation service over the seam
// (fs.Key), fenced by the resolver (sandbox.ResolverKey) and anchoring relative
// paths at the session's working directory (workdir.Key). It is provided under
// Key; it has no configuration of its own.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	provider, err := fs.Key.Get(ctx)
	if err != nil {
		return err
	}
	resolver, err := sandbox.ResolverKey.Get(ctx)
	if err != nil {
		return err
	}
	wd, err := workdir.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = Key.Provide(ctx, New(ctx, provider, resolver, Options{Workdir: wd}))
	return err
}, cordis.WithInject(fs.Key.Name(), sandbox.ResolverKey.Name(), workdir.Key.Name()))

// Item is the fs.ops catalogue item.
func Item() catalog.Item {
	return catalog.Item{
		Name:      Name,
		Component: Component,
		Provides:  []string{Key.Name()},
	}
}

// PolicyItem is the sandbox.policy catalogue item. The resolver package is
// importable only from here (DH-D-SANDBOX-1: ops is its one caller), so the
// catalogue takes its item from ops.
func PolicyItem() catalog.Item {
	return catalog.Item{
		Name:      policy.Name,
		Component: policy.Component,
		Config:    reflect.TypeFor[policy.Config](),
		Provides:  []string{sandbox.ResolverKey.Name()},
	}
}
