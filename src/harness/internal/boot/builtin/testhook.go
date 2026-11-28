//go:build testhook

package builtin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/claudecode"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/replay"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	fstools "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
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

// TestClaudeCodeName is the catalogue name of the testhook claude-code route
// item: it registers the real claude-code route (claudecode.Route) over the
// client program it names, so a built-binary test runs the route adapter against
// an ephemeral stand-in client (DH-E-SDK-1). No release build mounts the route
// yet, and no configuration layer can let a helper run unconfined, so the item
// plans the client's launch with a resolver of its own that lets only
// claudecode.RouteID run unconfined.
const TestClaudeCodeName = "llm.testkit-claude-code"

// TestClaudeCodeConfig is the item's configuration: the client's absolute path,
// extra NAME=value entries for it (each still checked by claudecode.Route) and
// its temporary directory.
type TestClaudeCodeConfig struct {
	Program string   `json:"program"`
	Env     []string `json:"env,omitempty"`
	TempDir string   `json:"tempDir,omitempty"`
}

var testClaudeCode = cordis.Define(TestClaudeCodeName, func(ctx *cordis.Context, cfg TestClaudeCodeConfig) error {
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.Program) {
		return fmt.Errorf("%s: program %q is not an absolute path", TestClaudeCodeName, cfg.Program)
	}
	res, err := policy.New(policy.Config{UnconfinedHelpers: map[string]string{
		string(claudecode.RouteID): TestClaudeCodeName + ": the stand-in client of a testhook build",
	}}, policy.Deps{Owner: ctx})
	if err != nil {
		return err
	}
	route, err := claudecode.Route(claudecode.Config{Program: cfg.Program, Env: cfg.Env, TempDir: cfg.TempDir, Sandbox: res})
	if err != nil {
		return err
	}
	_, err = ctx.Effect("llm.testkit-claude-code.register", func() (cordis.Disposer, error) {
		reg, err := rt.Register(TestClaudeCodeName, route)
		if err != nil {
			return nil, err
		}
		return func() error { reg.Close(); return nil }, nil
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

// TestAskName is the catalogue name of the testhook approval policy item: it
// puts every call of the named tools to approval, standing in for the permission
// presets (C34) the binary does not boot yet, so a built-binary test drives the
// approval round trip (DH-E-SDK-1).
const TestAskName = "tools.testkit-ask"

// TestAskConfig is the approval policy item's configuration: the tools to ask
// before.
type TestAskConfig struct {
	Tools []string `json:"tools"`
}

var testAsk = cordis.Define(TestAskName, func(ctx *cordis.Context, cfg TestAskConfig) error {
	reg, err := tools.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = reg.Decide(ctx, TestAskName, func(_ context.Context, c tools.Call) tools.Decision {
		if slices.Contains(cfg.Tools, c.Name()) {
			return tools.Ask(TestAskName+" asks before "+c.Name(), "")
		}
		return tools.Allow()
	})
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

func init() {
	extra = append(extra,
		catalog.Item{Name: TestWorkdirName, Component: testWorkdir, Config: reflect.TypeFor[TestWorkdirConfig](), Provides: []string{workdir.Key.Name()}},
		// The model-facing file tools (read, write, edit, ...), which no
		// profile mounts yet, so a built-binary test drives edit (DH-E-FS-3).
		fstools.Item(),
		catalog.Item{Name: TestRouteName, Component: testRoute, Config: reflect.TypeFor[TestRouteConfig]()},
		catalog.Item{Name: TestClaudeCodeName, Component: testClaudeCode, Config: reflect.TypeFor[TestClaudeCodeConfig]()},
		catalog.Item{Name: TestCommandName, Component: testCommand},
		catalog.Item{Name: TestSleepName, Component: testSleep},
		catalog.Item{Name: TestAskName, Component: testAsk, Config: reflect.TypeFor[TestAskConfig]()},
		// The keyless replay of a recorded session (DH-D-UTIL-7), driven by
		// test/replay against this build.
		catalog.Item{Name: replay.Name, Component: replay.Component, Config: reflect.TypeFor[replay.Config]()})
}
