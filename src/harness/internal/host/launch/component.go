package launch

import (
	"context"
	"io"
	"os"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	bootlaunch "github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
)

// Name is the component's catalogue name.
const Name = "host.launch"

// WebURL is the managed variable host.launch contributes to the shell
// environment registry (shellenv): the listener's origin.
const WebURL = "DSH_WEB_URL"

// Key is the running host.launch.
var Key = cordis.NewServiceKey[*Host]("host.launch")

// optionsHook, set by tests, adjusts the options before Start.
var optionsHook func(*Options)

// Component is host.launch. It loads only beside a listener and host.auth, so a
// profile that binds no listener (headless, sdk) never writes a runtime record. The
// announcement waits for the listener's Ready; host.stop calls the boot App's
// Stop(Requested, 0); the peer rule and the work summary ask the boot tree's
// process registry. With a managed environment registry in the tree it declares
// DSH_WEB_URL, the origin. Unloading it removes the record, the socket and the
// variable.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	l, err := listen.Key.Get(ctx)
	if err != nil {
		return err
	}
	a, err := auth.Key.Get(ctx)
	if err != nil {
		return err
	}
	o := Options{Config: cfg, Origin: l.Origin(), Mint: a.Mint, Ready: l.Ready(), Terminal: terminal(os.Stderr),
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
	// DSH_WEB_URL is the origin, never a link, so a command can reach this host
	// and still has to sign in; it leaves with this fiber.
	if reg, ok := shellenv.Key.Lookup(ctx); ok && reg != nil {
		origin := o.Origin
		if _, err := shellenv.Mount(ctx, reg, shellenv.Contributor{Name: Name,
			Decls:  []shellenv.Decl{{Name: WebURL, Description: "the listening host's origin; it carries no credential"}},
			Values: func(context.Context) ([]string, error) { return []string{WebURL + "=" + origin}, nil }}); err != nil {
			return err
		}
	}
	_, err = Key.Provide(ctx, h)
	return err
}, cordis.WithInject(listen.Key.Name(), auth.Key.Name()))

// terminal returns f only when it is a character device (a TTY or a console). A
// redirected stderr (a file, a pipe, a service journal) persists what it receives,
// and a launch link is a credential that leaves the host only on its own terminal,
// so such a host has no print channel.
func terminal(f *os.File) io.Writer {
	if f == nil {
		return nil
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return f
}
