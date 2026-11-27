package agent

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// Name is the catalogue name of Component.
const Name = "agent.registry"

// Component is agent.registry (agent-loop.md §6.1): an empty registry whose
// lifecycle notices dispatch through the component's fiber, provided under
// Agents. Its factory seat is empty until agent.driver fills it.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	_, err := Agents.Provide(ctx, NewRegistry(ctx))
	return err
})
