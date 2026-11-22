package subagent

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
)

// Name is the component's catalogue name.
const Name = "subagent.manager"

// Component is subagent.manager: it injects the agent registry and provides
// the delegation seam under Key. A component that delegates injects Key; it
// is a dependent, so it unloads, releasing its children, before the manager
// does (DH-D-CORDIS-1). Runs still held when the manager unloads are
// released then.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	reg, err := agent.Agents.Get(ctx)
	if err != nil {
		return err
	}
	//lint:ignore typeassert New returns its own *manager; the component needs the concrete type to close every agent on unload
	m := New(reg).(*manager)
	if _, err := ctx.Effect("subagent.manager", func() (cordis.Disposer, error) {
		return func() error { m.closeAll(); return nil }, nil
	}); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, m)
	return err
}, cordis.WithInject(agent.Agents.Name()))
