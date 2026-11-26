package launch

import (
	"os"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	bootlaunch "github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
)

// Name is the component's catalogue name.
const Name = "host.launch"

// Key is the running host.launch.
var Key = cordis.NewServiceKey[*Host]("host.launch")

// optionsHook, set by tests, adjusts the options before Start.
var optionsHook func(*Options)

// Component is host.launch. It loads only beside a listener and host.auth, so a
// profile that binds no listener (headless, sdk) never writes a runtime record. The
// announcement waits for the listener's Ready; host.stop calls the boot App's
// Stop(Requested, 0); the peer rule and the work summary ask the boot tree's
// process registry. Unloading it removes the record and the socket.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	l, err := listen.Key.Get(ctx)
	if err != nil {
		return err
	}
	a, err := auth.Key.Get(ctx)
	if err != nil {
		return err
	}
	o := Options{Config: cfg, Origin: l.Origin(), Mint: a.Mint, Ready: l.Ready(), Terminal: os.Stderr,
		Warn: ctx.Logger().Warnf}
	if app, ok := bootlaunch.AppKey.Lookup(ctx); ok {
		o.Stop = func() { app.Stop(bootlaunch.Requested, 0) }
	}
	if procs, ok := bootlaunch.ProcessesKey.Lookup(ctx); ok && procs != nil {
		o.Trees = procs
		o.Work = func() Work { return Work{Commands: len(procs.Live())} }
	}
	if optionsHook != nil {
		optionsHook(&o)
	}
	var h *Host
	if _, err := ctx.Effect("runtime record and control endpoint", func() (cordis.Disposer, error) {
		var err error
		if h, err = Start(ctx.Lifetime(), o); err != nil {
			return nil, err
		}
		return h.Close, nil
	}); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, h)
	return err
}, cordis.WithInject(listen.Key.Name(), auth.Key.Name()))
