package local

import (
	"context"
	"errors"
	"os"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Host is what a backend that runs the real stage needs to know about the host:
// the harness binary the stage is, the harness home it keeps read-only and the
// user's runtime directory, where the session bus lives. New resolves it once.
type Host struct {
	// Exe is the absolute path of the harness binary.
	Exe string
	// Home is the harness home (Config.Home); empty when none was named.
	Home string
	// RuntimeDir is the user's runtime directory ($XDG_RUNTIME_DIR, else
	// /run/user/<uid>) on Linux; empty elsewhere.
	RuntimeDir string
}

// HostBackend is a Backend that probes with the real stage and may confine it
// from outside. The service calls ProbeOn in place of Probe, and asks Prefix for
// the outside confiner of each start it prepares under the backend.
type HostBackend interface {
	Backend
	// ProbeOn confines a probe stage on host h and has it try canary.
	ProbeOn(ctx context.Context, h Host, canary string) (Probe, error)
	// Prefix is the argv the stage of one start under policy runs under, nil
	// when the stage confines itself.
	Prefix(h Host, policy sandbox.Policy, canary string) ([]string, error)
}

// stageRunner runs a probe stage for plan under prefix and returns its ready as
// a Probe. The command is never started: the decision refuses every ready.
type stageRunner func(ctx context.Context, h Host, plan launch.Plan, policy sandbox.Policy, prefix []string) (Probe, error)

// errProbed is the decision a probe stage receives: its ready is all the probe
// wanted, so no command runs.
var errProbed = errors.New("the probe stage is never given go")

// stageEnv are the host variables a probe stage keeps: the ones its bus
// observation reads. probeEnvExtra is added to them (a test re-executes its own
// binary as the stage).
var (
	stageEnv      = []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"}
	probeEnvExtra []string
)

// runStage is the real probe: the stage of plan, started by the one starter
// under prefix, reports ready; the decision records it and refuses it, so the
// stage ends without running anything.
func runStage(ctx context.Context, h Host, plan launch.Plan, policy sandbox.Policy, prefix []string) (Probe, error) {
	if h.Exe == "" {
		return Probe{}, herr.New(sandbox.CodeUnavailable, "the harness binary is unknown, so no probe stage can run")
	}
	var got *launch.Ready
	l, err := launch.NewLaunch(policy, plan, launch.Hooks{
		Exe: h.Exe, Prefix: prefix,
		Confirm: func(_ context.Context, _ launch.Plan, r launch.Ready) (sandbox.Confinement, error) {
			got = &r
			return sandbox.Confinement{}, errProbed
		},
	})
	if err != nil {
		return Probe{}, err
	}
	env := subprocess.Env{}
	for _, name := range stageEnv {
		if v, ok := os.LookupEnv(name); ok {
			env = env.With(name, v)
		}
	}
	env = append(env, probeEnvExtra...)
	p, err := subprocess.Start(ctx, subprocess.Spec{Argv: []string{h.Exe}, Env: env, Launch: l})
	if p != nil {
		_ = p.StopTree(context.WithoutCancel(ctx), 0)
		return Probe{}, herr.New(sandbox.CodeLaunchFailed, "a probe stage was given go")
	}
	if got == nil {
		return Probe{}, herr.Wrap(sandbox.CodeUnavailable, err, "the probe stage ended before ready")
	}
	if got.Backend != plan.Backend {
		return Probe{}, herr.Newf(sandbox.CodeUnavailable, "the probe stage reports backend %s, not %s", got.Backend, plan.Backend)
	}
	return Probe{Canary: got.Canary, Governed: slices.Clone(got.Governed)}, nil
}

// probePolicy is the policy a probe stage runs under: read-only, network denied,
// the narrowest confined policy.
func probePolicy() (sandbox.Policy, error) {
	return sandbox.Issue(mint.Spec{Mode: string(sandbox.ReadOnly)})
}

// resolveHost is the Host of a service: the running binary, the configured home
// and the platform's runtime directory.
func resolveHost(home string) Host {
	h := Host{Home: home, RuntimeDir: runtimeDir()}
	if exe, err := os.Executable(); err == nil {
		h.Exe = exe
	}
	return h
}
