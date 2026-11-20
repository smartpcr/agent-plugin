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
// signals. A first SIGINT ends the context, an Interrupted stop (status 130); a
// first SIGTERM or SIGHUP is the supervisor's ordinary stop, a Terminated stop
// (status 0 inside the shutdown bound). A second signal of any kind, during
// shutdown, ends the shutdown wait at once (status 124).
func Process(ctx context.Context, o Options) int {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	return runSignals(ctx, o, sigs)
}

// runSignals is Run, with each signal read from sigs turned into a stop.
func runSignals(ctx context.Context, o Options, sigs <-chan os.Signal) int {
	if o.App == nil {
		o.App = launch.NewApp()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		for first := true; ; first = false {
			var sig os.Signal
			select {
			case sig = <-sigs:
			case <-quit:
				return
			}
			switch {
			case !first:
				o.App.Stop(launch.Interrupted, output.Interrupted)
			case sig == os.Interrupt:
				cancel()
			default:
				o.App.Stop(launch.Terminated, output.OK)
			}
		}
	}()
	return Run(ctx, o)
}
