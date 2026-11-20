package boot

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Real are the steps of a real launch.
type Real struct{}

var _ Steps = Real{}

// Facts builds the launch facts from the request.
func (Real) Facts(_ context.Context, s *State) error {
	d, env, err := launch.Build(s.Options.Request)
	if err != nil {
		return err
	}
	s.Data, s.Env = d, env
	return nil
}

// Home creates the home's directories if needed and checks they are private.
func (Real) Home(_ context.Context, s *State) error {
	s.Home = home.New(s.Data.Home)
	return home.Ensure(s.Home)
}

// Guards installs the fatal path: the log ring, the report writer and the crash
// output of the runtime. The watchdog is the tree's status hook (step 5).
func (Real) Guards(_ context.Context, s *State) error {
	s.Ring = diag.NewRing(diag.RingSize)
	var secrets []string
	if s.Env != nil {
		for _, name := range s.Env.Names() {
			if secret.SecretShaped(name) {
				if v, _, ok := s.Env.Lookup(name); ok {
					secrets = append(secrets, v)
				}
			}
		}
	}
	s.Diag = diag.New(diag.Config{
		Reports: s.Home.Reports(),
		Stderr:  s.Stderr,
		Facts:   diag.Facts{Version: s.Data.Version, OS: s.Data.OS, Profile: s.Data.Profile},
		Ring:    s.Ring,
		Secrets: secrets,
		Now:     s.Clock.Now,
		// A fatal error during the run stops the app; Run then unloads within
		// FatalReleaseTimeout.
		Release:        func(context.Context) { s.App.Stop(launch.Fatal, output.Fatal) },
		ReleaseTimeout: s.limits().FatalReleaseTimeout,
	})
	uninstall, err := diag.Install(s.Home.Run())
	if err != nil {
		// The crash file is a convenience; the fatal path stands without it.
		_, _ = fmt.Fprintf(s.Ring, "boot: crash output not installed: %v\n", err)
	}
	s.OnRelease(uninstall)
	return nil
}

// Layers reads and parses every layer of the profile.
func (Real) Layers(_ context.Context, s *State) error {
	layers, issues, err := profile.Stack(s.Options.Profile, profile.Sources{
		Home:     s.Data.Home,
		Overlays: s.Data.Overlays,
		Read:     profile.ReadFile(s.Options.Request.ReadFile),
	})
	if err != nil {
		return err
	}
	s.Layers, s.Issues = layers, issues
	return nil
}

// Compose composes the layers into the tree to apply (pure).
func (Real) Compose(_ context.Context, s *State) error {
	composed, index, issues := loader.Compose(s.Layers)
	s.Composed, s.Index = composed, index
	s.Issues = append(s.Issues, issues...)
	for _, issue := range s.Issues {
		_, _ = fmt.Fprintf(s.Ring, "boot: layer issue: %s\n", issue.Error())
	}
	return nil
}

// Tree creates the tree without the console exporter, provides the launch
// services and mounts the loader with the catalogue and the evaluator.
func (Real) Tree(_ context.Context, s *State) error {
	cat := s.Options.Catalog
	if cat == nil {
		var err error
		if cat, err = builtin.Catalog(); err != nil {
			return err
		}
	}
	opts := launch.Scope(s.Data, s.Env, secret.SecretShaped)
	opts.Views = cat.Views()
	ev, err := expr.New(opts)
	if err != nil {
		return err
	}
	var exporters []cordis.Exporter
	if s.Ring != nil {
		exporters = append(exporters, s.Ring)
	}
	root := cordis.New(cordis.WithExporters(exporters...))
	procs := subprocess.NewRegistry()
	t := newTree(root, procs)
	s.Host = t
	if s.Diag != nil {
		if _, err := diag.Key.Provide(root, s.Diag); err != nil {
			return err
		}
	}
	if _, err := launch.AppKey.Provide(root, launch.App(s.App)); err != nil {
		return err
	}
	if _, err := launch.ProcessesKey.Provide(root, procs); err != nil {
		return err
	}
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat, Evaluator: ev})
	if err != nil {
		return err
	}
	if err := fiber.Wait(); err != nil {
		return err
	}
	svc, err := loader.Key.Get(root)
	if err != nil {
		return err
	}
	t.svc, s.Service = svc, svc
	return nil
}

// Settle applies the composition and waits until no entry is loading, at most
// StartupTimeout on the clock; a stop asked meanwhile ends the wait.
func (Real) Settle(ctx context.Context, s *State) error {
	if s.Service == nil {
		return errors.New("no loader")
	}
	done := make(chan error, 1)
	go func() { done <- s.Service.Apply(s.Composed) }()
	select {
	case err := <-done:
		s.ApplyErr = err
		if err != nil {
			_, _ = fmt.Fprintf(s.Ring, "boot: apply: %v\n", err)
		}
	case <-s.Clock.After(s.limits().StartupTimeout):
		_, _ = fmt.Fprintf(s.Ring, "boot: entries still loading after %s\n", s.limits().StartupTimeout)
	case <-s.App.Stopping():
		_, _ = fmt.Fprintln(s.Ring, "boot: stop requested during start-up")
	case <-ctx.Done():
	}
	return nil
}

// Audit checks the required entries and lists every outbound kind as the sharing
// gate decides it. No kind is configured yet, so every kind is off; the gate reads
// DSH_SHARING_OFF from the launch environment.
func (Real) Audit(_ context.Context, s *State) error {
	if s.Sharing == nil {
		s.Sharing = sharing.New(sharing.Config{}, s.Env.Func())
	}
	s.Audit.FromGate(s.Sharing)
	if s.Service == nil {
		return errors.New("no loader")
	}
	inactive, err := audit(s.Service, s.Options.Profile.Required)
	s.Audit.Inactive = inactive
	return err
}
