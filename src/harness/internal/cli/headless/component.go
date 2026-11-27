// Package headless is component cli.headless (A24, C50), the carrier of the
// headless profile: the command line as the one client of a one-shot run.
//
// The carrier (impl.md 16.1, DH-D-BUNDLE-3) binds no listener and writes no
// runtime record (M2-C-13). Its runner (impl.md 16.3, DH-D-CLI-2) parses the
// app words of `dsh headless`, waits for the launch to be ready, creates the
// agent (or resumes --session), sends the task, waits until the agent and its
// descendants are idle, flushes, prints the last assistant text of its interval
// (or, under --json, the interval's committed events as export lines and one
// run/result line) and stops the launch with the run's status: 0 for a turn
// that ended completed, 1 for any other ending, 5 for an unknown session or
// agent preset, 6 for a session another process holds, 64 for a usage error.
//
// Progress lines go to the diagnostic stream only (U-K2-4). No `cli` answerer is
// registered yet, so an approval settles unavailable and the call is denied.
// A Ctrl-C stops the launch with boot's Interrupted status (130) at once; the
// app-cli.md rule that the runner first waits for turn/end and prints what text
// exists is the open alignment I-6.
package headless

import (
	"context"
	"os"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Name is the catalogue name of Component.
const Name = "cli.headless"

// Config is cli.headless's configuration.
type Config struct {
	// Route is the model route of a run; "" is the one route the runtime
	// serves.
	Route string `json:"route,omitempty"`
	// Model is the route's model; "" is the route's default.
	Model string `json:"model,omitempty"`
}

// Component is cli.headless. It provides nothing. Without a launch App and its
// data (a tree that is not a launch) it starts no run.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	reg, err := agent.Agents.Get(ctx)
	if err != nil {
		return err
	}
	if _, err := session.StoreKey.Get(ctx); err != nil {
		return err
	}
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	app, hasApp := launch.AppKey.Lookup(ctx)
	data, hasData := launch.DataKey.Lookup(ctx)
	if !hasApp || !hasData {
		return nil
	}
	r := &runner{owner: ctx, cfg: cfg, app: app, data: data, agents: reg, routes: rt, diag: os.Stderr, diagTTY: isTerminal(os.Stderr)}
	if s, ok := launch.StdioKey.Lookup(ctx); ok {
		r.in, r.out = s.In, s.Out
	} else {
		r.in, r.out, r.inTTY = os.Stdin, os.Stdout, isTerminal(os.Stdin)
	}
	_, err = ctx.Effect("cli.headless.run", func() (cordis.Disposer, error) {
		run, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.run(run)
		}()
		return func() error { cancel(); <-done; return nil }, nil
	})
	return err
}, cordis.WithInject(agent.Agents.Name(), session.StoreKey.Name(), llm.RuntimeKey.Name()))
