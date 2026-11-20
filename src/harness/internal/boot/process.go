package boot

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
)

// Process runs a launch of the running process: Run, with the process's stop
// signals. The first interrupt or termination signal ends the context, an
// Interrupted stop (status 130); a second one, during shutdown, ends the shutdown
// wait at once (status 124).
func Process(ctx context.Context, o Options) int {
	if o.App == nil {
		o.App = launch.NewApp()
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		for first := true; ; first = false {
			select {
			case <-sigs:
			case <-quit:
				return
			}
			if first {
				cancel()
			} else {
				o.App.Stop(launch.Interrupted, output.Interrupted)
			}
		}
	}()
	return Run(ctx, o)
}
