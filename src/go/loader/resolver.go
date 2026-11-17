package loader

import (
	"fmt"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Resolver maps an entry's name to a component.
type Resolver interface {
	Resolve(name string) (*cordis.Component, bool)
}

// Components is a Resolver backed by a map.
type Components map[string]*cordis.Component

// Resolve looks name up in the map.
func (c Components) Resolve(name string) (*cordis.Component, bool) {
	component, ok := c[name]
	return component, ok
}

var registry = struct {
	sync.RWMutex
	components Components
}{components: Components{}}

// Register makes component resolvable by name for every loader without its own
// Resolver, in the manner of database/sql drivers: call it from an init function.
// It panics if component is nil or name is already registered.
func Register(name string, component *cordis.Component) {
	if component == nil {
		panic("loader: Register component is nil")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, dup := registry.components[name]; dup {
		panic(fmt.Sprintf("loader: Register called twice for %q", name))
	}
	registry.components[name] = component
}

type registered struct{}

func (registered) Resolve(name string) (*cordis.Component, bool) {
	registry.RLock()
	defer registry.RUnlock()
	return registry.components.Resolve(name)
}

// Registered is the Resolver over the components passed to Register.
var Registered Resolver = registered{}
