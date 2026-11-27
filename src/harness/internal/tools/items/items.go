// Package items lists the tools family's catalogue items. Only packages under
// internal/tools may import internal/tools/core (DH-T-TOOLS-18), so the build
// catalogue takes the tool registry's item from here.
package items

import (
	"reflect"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// Items are tools.registry and tools.prompt.
func Items() []catalog.Item {
	return []catalog.Item{
		{Name: core.Name, Component: core.Component, Provides: []string{tools.Key.Name()}},
		{Name: prompt.Name, Component: prompt.Component, Config: reflect.TypeFor[prompt.Config](), Provides: []string{prompt.Key.Name()}},
	}
}
