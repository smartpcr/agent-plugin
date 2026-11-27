package llm

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// Name is the catalogue name of Component.
const Name = "llm.runtime"

// RuntimeKey is the service under which llm.runtime provides the process's
// runtime: its routes, prepared calls and the one normalisation point.
var RuntimeKey = cordis.NewServiceKey[*Runtime]("llm.runtime")

// Component is llm.runtime (llm.md §6.1): a runtime with no routes whose stream
// interception (StreamAround) dispatches through the component's fiber,
// provided under RuntimeKey. It injects nothing; a route's owner registers its
// routes on the runtime it reads.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	_, err := RuntimeKey.Provide(ctx, New(WithInterception(ctx)))
	return err
})
