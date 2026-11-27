// Package headless is component cli.headless (A24, C50), the carrier of the
// headless profile: the command line as the one client of a one-shot run.
//
// This step (impl.md 16.1, DH-D-BUNDLE-3) mounts the carrier so the headless
// profile composes and launches over the spine: it injects the agent registry and
// the session store the runner drives, binds no listener and writes no runtime
// record (M2-C-13). The runner itself -- parsing the task and its words from the
// command line, running it and choosing the exit status -- is DH-D-CLI-2's (impl.md
// 16.3); until it lands the carrier starts no run and the process stops when the
// launcher stops it.
package headless

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Name is the catalogue name of Component.
const Name = "cli.headless"

// Component is cli.headless. It provides nothing.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	if _, err := agent.Agents.Get(ctx); err != nil {
		return err
	}
	_, err := session.StoreKey.Get(ctx)
	return err
}, cordis.WithInject(agent.Agents.Name(), session.StoreKey.Name()))
