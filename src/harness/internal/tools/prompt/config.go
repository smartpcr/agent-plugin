package prompt

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Unlisted is the toolOrder entry that stands for every visible tool the order
// does not name, sorted by name.
const Unlisted = "<unlisted-tools>"

// Config is the tools.prompt component's config (areas/tools.md §6.2).
type Config struct {
	// IncludeHarnessIdentity puts IdentityLine first.
	IncludeHarnessIdentity bool `json:"includeHarnessIdentity"`
	// IncludeRuntimeContext delivers the registered runtime contexts.
	IncludeRuntimeContext bool `json:"includeRuntimeContext"`
	// PersonaPrefix is literal text after the identity line; live.
	PersonaPrefix string `json:"personaPrefix" dsh:"live"`
	// PersonaSuffix is literal text after every other section; live.
	PersonaSuffix string `json:"personaSuffix" dsh:"live"`
	// ToolOrder orders the step's tools: the named ones in this order, with
	// exactly one Unlisted entry standing for the rest by name. Unset orders all
	// by name. A name must be compiled in or fall under a declared dynamic prefix;
	// a known name that is not visible in a step is skipped.
	ToolOrder []string `json:"toolOrder,omitempty"`
}

// Defaults is the config with every default (identity and runtime context on).
func Defaults() Config {
	return Config{IncludeHarnessIdentity: true, IncludeRuntimeContext: true}
}

// clone is c with its own ToolOrder.
func (c Config) clone() Config {
	c.ToolOrder = slices.Clone(c.ToolOrder)
	return c
}

// Validate checks toolOrder: exactly one Unlisted, no duplicates, every other name
// known to the binary (tools.Known). Each issue carries its index path, so config
// load reports it with file and line.
func (c Config) Validate() error {
	if len(c.ToolOrder) == 0 {
		return nil
	}
	var issues []cordis.ConfigIssue
	at := func(i int) string { return "toolOrder." + strconv.Itoa(i) }
	seen := map[string]int{}
	marks := 0
	for i, name := range c.ToolOrder {
		if prev, dup := seen[name]; dup {
			issues = append(issues, cordis.ConfigIssue{Path: at(i), Message: fmt.Sprintf("%q is listed again (first at toolOrder.%d)", name, prev)})
			continue
		}
		seen[name] = i
		switch {
		case name == Unlisted:
			marks++
		case !tools.Known(name):
			issues = append(issues, cordis.ConfigIssue{Path: at(i), Message: fmt.Sprintf("%q is not a compiled-in tool and matches no declared dynamic prefix", name)})
		}
	}
	if marks == 0 {
		issues = append(issues, cordis.ConfigIssue{Path: "toolOrder", Message: "must list " + Unlisted + " exactly once"})
	}
	if len(issues) > 0 {
		return &cordis.ConfigError{Issues: issues}
	}
	return nil
}
