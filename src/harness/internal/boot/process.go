package boot

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/lifecycle"
)

// Process runs a launch of the running process: Run, with the process's stop
// signals watched by the one stop path, lifecycle.Watch (DH-D-CLI-4). A first
// Ctrl-C is an Interrupted stop (status 130); a first SIGTERM or SIGHUP (on
// Windows a closed console) is the supervisor's ordinary stop, a Terminated stop
// (status 0 inside the shutdown bound). A second signal of any kind, during
// shutdown, ends the shutdown wait at once (status 124).
func Process(ctx context.Context, o Options) int {
	if o.App == nil {
		o.App = launch.NewApp()
	}
	return lifecycle.Process(ctx, o.App, lifecycle.Inputs{Notify: o.Stderr}, func(ctx context.Context) int {
		return Run(ctx, o)
	})
}
