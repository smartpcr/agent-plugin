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
// Progress lines go to the diagnostic stream only (U-K2-4).
//
// The component mounts the interaction plane of the headless profile: the
// interaction registry (human.Key), the approval service with its session grant
// table (approval.Key, which the tool pipeline asks) and the command registry
// (command.Key, which --command dispatches to). It registers the one `cli`
// answerer (DH-ADR-CLI-7) when a controlling terminal can be opened and --json is
// not set; the run's agent, and each subagent of its tree, is bound to channel
// cli, so with no answerer an approval settles unavailable and the call is
// denied. --approval-policy is recorded on the session before the task is sent.
//
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
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
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

// Component is cli.headless. It provides the interaction registry, the approval
// service and the command registry. Without a launch App and its data (a tree
// that is not a launch) it starts no run.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	reg, err := agent.Agents.Get(ctx)
	if err != nil {
		return err
	}
	if _, err := session.StoreKey.Get(ctx); err != nil {
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
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	h := plane{interactions: human.New(), commands: command.NewRegistry(scopes), sessions: sessions}
	grants := approval.NewGrants()
	h.approvals = approval.New(approval.Config{Registry: h.interactions, Grants: grants, Sessions: sessions})
	if _, err := h.approvals.Bind(ctx, sessions); err != nil {
		return err
	}
	if _, err := grants.Bind(ctx, sessions); err != nil {
		return err
	}
	ps, err := prompt.Key.Get(ctx)
	if err != nil {
		return err
	}
	if _, err := h.approvals.RegisterPromptSection(ctx, ps); err != nil {
		return err
	}
	if _, err := human.Key.Provide(ctx, h.interactions); err != nil {
		return err
	}
	if _, err := approval.Key.Provide(ctx, h.approvals); err != nil {
		return err
	}
	if _, err := command.Key.Provide(ctx, h.commands); err != nil {
		return err
	}
	if _, err := ctx.Effect("cli.headless.interactions", func() (cordis.Disposer, error) {
		return func() error { h.interactions.Close(); return nil }, nil
	}); err != nil {
		return err
	}
	app, hasApp := launch.AppKey.Lookup(ctx)
	data, hasData := launch.DataKey.Lookup(ctx)
	if !hasApp || !hasData {
		return nil
	}
	r := &runner{owner: ctx, cfg: cfg, app: app, data: data, agents: reg, routes: rt, plane: h, diag: os.Stderr, diagTTY: isTerminal(os.Stderr)}
	if s, ok := launch.StdioKey.Lookup(ctx); ok {
		r.in, r.out = s.In, s.Out
	} else {
		r.in, r.out, r.inTTY = os.Stdin, os.Stdout, isTerminal(os.Stdin)
	}
	if err := r.bindTree(); err != nil {
		return err
	}
	// The answerer needs a person: a controlling terminal, and output that is not
	// --json. A parse error is reported by the run.
	if args, perr := Parse(data.Words); perr == nil && !args.JSON {
		if tty, err := openTerminal(); err == nil {
			// Closing the terminal on release unblocks the answerer's reader,
			// which then closes its line channel and exits.
			if _, err := ctx.Effect("cli.headless.terminal", func() (cordis.Disposer, error) {
				return func() error { return tty.Close() }, nil
			}); err != nil {
				tty.Close()
				return err
			}
			if _, err := h.interactions.RegisterAnswerer(ctx, ChannelCLI, newTerminal(h.interactions, tty, r.diag)); err != nil {
				return err
			}
		}
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
}, cordis.WithInject(agent.Agents.Name(), session.StoreKey.Name(), session.Key.Name(), scope.TableKey.Name(), llm.RuntimeKey.Name(), prompt.Key.Name()))

// plane is the interaction plane the component mounts.
type plane struct {
	interactions human.Registry
	approvals    *approval.Service
	commands     *command.Registry
	sessions     session.Service
}
