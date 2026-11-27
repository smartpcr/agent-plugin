package driver

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// Name is the catalogue name of Component.
const Name = "agent.driver"

// Component is agent.driver (agent-loop.md §6.1): it builds the factory over the
// session service, the scope table, the model runtime, the tool service and the
// prompt assembly, and seats it in the registry for as long as its fiber lives.
// It starts no agent: a configured agent is a later deliverable's.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	reg, err := agent.Agents.Get(ctx)
	if err != nil {
		return err
	}
	sessions, err := session.Key.Get(ctx)
	if err != nil {
		return err
	}
	scopes, err := scope.TableKey.Get(ctx)
	if err != nil {
		return err
	}
	models, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	ts, err := tools.Key.Get(ctx)
	if err != nil {
		return err
	}
	ps, err := prompt.Key.Get(ctx)
	if err != nil {
		return err
	}
	f, err := NewFactory(FactoryOptions{Context: ctx, Sessions: sessions, Scopes: scopes, Models: models, Tools: ts, Prompt: ps})
	if err != nil {
		return err
	}
	_, err = reg.SetFactory(ctx, f)
	return err
}, cordis.WithInject(agent.Agents.Name(), session.Key.Name(), scope.TableKey.Name(), llm.RuntimeKey.Name(), tools.Key.Name(), prompt.Key.Name()))

// Item is the agent.driver catalogue item. Nothing outside this package imports
// it (DH-T-AGENT-27), so the catalogue takes the item from here.
func Item() catalog.Item { return catalog.Item{Name: Name, Component: Component} }
