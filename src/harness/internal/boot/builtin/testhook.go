//go:build testhook

package builtin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/replay"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	fstools "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/pwsh"
	shelltools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

// TestRouteName is the catalogue name of the keyless fake model route a testhook
// build adds, so a built-binary test layer can insert it. No release build
// carries the tag.
const TestRouteName = "llm.testkit-route"

// TestRouteConfig is the route item's configuration: the route id and the
// loopback URL serving the kit's SSE dialect. HoldStop makes the item's release
// never return, so a stop runs into the launch's shutdown bound (status 124).
type TestRouteConfig struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	HoldStop bool   `json:"holdStop,omitempty"`
}

var testRoute = cordis.Define(TestRouteName, func(ctx *cordis.Context, cfg TestRouteConfig) error {
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	_, err = ctx.Effect("llm.testkit-route.register", func() (cordis.Disposer, error) {
		reg, err := rt.Register(TestRouteName, llm.Route{
			ID:      vocab.RouteID(cfg.ID),
			Models:  []llm.Model{{Name: model.ModelName}},
			Adapter: &model.Adapter{Client: &http.Client{}, URL: cfg.URL},
		})
		if err != nil {
			return nil, err
		}
		return func() error {
			reg.Close()
			if cfg.HoldStop {
				select {}
			}
			return nil
		}, nil
	})
	return err
}, cordis.WithInject(llm.RuntimeKey.Name()))

// TestCommandName is the catalogue name of the testhook command item: it
// registers /echo, whose output is its rest, so a built-binary test can run
// `dsh headless --command`.
const TestCommandName = "human.testkit-command"

var testCommand = cordis.Define(TestCommandName, func(ctx *cordis.Context, _ struct{}) error {
	reg, err := command.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = reg.Register(ctx, command.Command{Name: "echo", Summary: "echoes its rest", Run: func(_ context.Context, c command.Call) (string, error) {
		return c.Rest, nil
	}})
	return err
}, cordis.WithInject(command.Key.Name()))

// TestSleepName is the catalogue name of the testhook tool item: it registers
// the tool testkit_sleep, a long fake tool whose body announces itself on the
// diagnostic stream and returns only when its call is cancelled, so a
// built-binary test can stop a launch during a tool call.
const TestSleepName = "tools.testkit-sleep"

// SleepRunning is the line testkit_sleep writes on standard error once its body
// runs.
const SleepRunning = "testkit_sleep: running"

type sleepIn struct{}

type sleepOut struct {
	Text string `json:"text"`
}

var testSleep = cordis.Define(TestSleepName, func(ctx *cordis.Context, _ struct{}) error {
	reg, err := tools.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = reg.Register(ctx, tools.Define(tools.Spec[sleepIn, sleepOut]{
		Name: "testkit_sleep", Description: "Sleeps until the call is cancelled.", Timeout: tools.NoDeadline,
		Effects: []tools.Effect{tools.EffectNone},
		Run: func(c context.Context, _ sleepIn, _ tools.Run) (sleepOut, error) {
			fmt.Fprintln(os.Stderr, SleepRunning)
			<-c.Done()
			return sleepOut{}, c.Err()
		},
	}))
	return err
}, cordis.WithInject(tools.Key.Name()))

// TestWorkdirName is the catalogue name of the testhook working-directory item:
// it provides workdir.Key with one fixed directory as every session's directory
// and project root, standing in for session.workdir, whose fold registry
// (session.views) the binary does not boot yet, so a built-binary test layer can
// mount the real sandbox.policy, fs.ops and fs.tools (DH-E-FS-3).
const TestWorkdirName = "session.testkit-workdir"

// TestWorkdirConfig is the working-directory item's configuration: the
// directory, absolute and clean.
type TestWorkdirConfig struct {
	Root string `json:"root"`
}

type fixedDir string

func (d fixedDir) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d fixedDir) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: string(d), Root: string(d)}, nil
}
func (d fixedDir) Path() (string, error) { return string(d), nil }
func (d fixedDir) Root() (string, error) { return string(d), nil }

var testWorkdir = cordis.Define(TestWorkdirName, func(ctx *cordis.Context, cfg TestWorkdirConfig) error {
	if !filepath.IsAbs(cfg.Root) || filepath.Clean(cfg.Root) != cfg.Root {
		return fmt.Errorf("%s: root %q is not an absolute, clean path", TestWorkdirName, cfg.Root)
	}
	_, err := workdir.Key.Provide(ctx, fixedDir(cfg.Root))
	return err
})

// TestShellsName is the catalogue name of the testhook shell-registry item: it
// provides shellexec.Key with the PowerShell and bash executors this machine has,
// each over the mounted sandbox seam (sandbox.local), standing in for the
// executor items no profile mounts yet, so a built-binary test layer can mount
// the real shellexec.tools and stop or kill a launch while a real shell child
// runs (DH-E-AGENT-3).
const TestShellsName = "shellexec.testkit-shells"

var testShells = cordis.Define(TestShellsName, func(ctx *cordis.Context, _ struct{}) error {
	seam, err := sandbox.ServiceKey.Get(ctx)
	if err != nil {
		return err
	}
	h := shellexec.LocalHost(os.Environ())
	o := shellexec.Options{Sandbox: seam, Env: &shellenv.Builder{Registry: shellenv.NewRegistry(shellenv.Builtins{})}}
	reg := shellexec.NewRegistry()
	var found []error
	if e, err := pwsh.New(pwsh.DefaultConfig(), h, o); err != nil {
		found = append(found, err)
	} else if _, err := reg.Register(e); err != nil {
		return err
	}
	if e, err := bash.New(bash.Config{}, h, o); err != nil {
		found = append(found, err)
	} else if _, err := reg.Register(e); err != nil {
		return err
	}
	if len(found) == 2 {
		return fmt.Errorf("%s: no shell on this machine: %v; %v", TestShellsName, found[0], found[1])
	}
	_, err = shellexec.Key.Provide(ctx, reg)
	return err
}, cordis.WithInject(sandbox.ServiceKey.Name()))

func init() {
	extra = append(extra,
		catalog.Item{Name: TestShellsName, Component: testShells, Provides: []string{shellexec.Key.Name()}},
		// The model-facing shell tools (bash, pwsh), which no profile mounts
		// yet, over the registry above (DH-E-AGENT-3).
		shelltools.Item(),
		catalog.Item{Name: TestWorkdirName, Component: testWorkdir, Config: reflect.TypeFor[TestWorkdirConfig](), Provides: []string{workdir.Key.Name()}},
		// The model-facing file tools (read, write, edit, ...), which no
		// profile mounts yet, so a built-binary test drives edit (DH-E-FS-3).
		fstools.Item(),
		catalog.Item{Name: TestRouteName, Component: testRoute, Config: reflect.TypeFor[TestRouteConfig]()},
		catalog.Item{Name: TestCommandName, Component: testCommand},
		catalog.Item{Name: TestSleepName, Component: testSleep},
		// The keyless replay of a recorded session (DH-D-UTIL-7), driven by
		// test/replay against this build.
		catalog.Item{Name: replay.Name, Component: replay.Component, Config: reflect.TypeFor[replay.Config]()})
}
