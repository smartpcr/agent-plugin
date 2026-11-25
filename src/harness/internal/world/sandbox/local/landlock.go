package local

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
)

// Kernel is what the Landlock backend asks the running kernel: its Landlock
// interface version, 0 when Landlock is absent or disabled.
type Kernel interface {
	LandlockABI() (int, error)
}

// LandlockBackend is Landlock (DH-D-SANDBOX-4, DH-ADR-NATIVESYSTEM-2): the stage
// restricts itself through go-landlock (pure Go, no cgo, NG-4;
// DH-ADR-NATIVESYSTEM-1) before ready, from the
// rule set launch.LandlockRules builds for the plan. It comes after bubblewrap in
// the Linux chain, so it is selected when bwrap is absent or fails its probe.
type LandlockBackend struct {
	kernel Kernel
	stage  stageRunner
}

// Landlock is the Landlock backend asking kernel for its version.
func Landlock(kernel Kernel) *LandlockBackend { return newLandlock(kernel, runStage) }

func newLandlock(kernel Kernel, stage stageRunner) *LandlockBackend {
	return &LandlockBackend{kernel: kernel, stage: stage}
}

func (b *LandlockBackend) Name() sandbox.Backend { return sandbox.BackendLandlock }

func (b *LandlockBackend) Custom() bool { return false }

func (b *LandlockBackend) Probe(context.Context, string) (Probe, error) {
	return Probe{}, herr.New(sandbox.CodeUnavailable, "the Landlock probe needs the host (ProbeOn)")
}

// abi is the kernel's version, refused below 1.
func (b *LandlockBackend) abi() (int, error) {
	abi, err := b.kernel.LandlockABI()
	if err != nil {
		return 0, herr.Wrap(sandbox.CodeUnavailable, err, "read the Landlock interface version")
	}
	if abi < 1 {
		return 0, herr.Newf(sandbox.CodeUnavailable, "the kernel has no Landlock (interface version %d)", abi)
	}
	return abi, nil
}

// ProbeOn reads the kernel's version and, when Landlock is there, runs a probe
// stage that restricts itself under a read-only policy and tries canary.
func (b *LandlockBackend) ProbeOn(ctx context.Context, h Host, canary string) (Probe, error) {
	if _, err := b.abi(); err != nil {
		return Probe{}, err
	}
	p, err := probePolicy()
	if err != nil {
		return Probe{}, err
	}
	return b.stage(ctx, h, launch.Plan{Kind: launch.Confined, Backend: sandbox.BackendLandlock, Canary: canary, Home: h.Home}, p, nil)
}

// Prefix is nil: a Landlock stage confines itself.
func (b *LandlockBackend) Prefix(Host, sandbox.Policy, string) ([]string, error) { return nil, nil }

// Rules is the rule set a stage would apply for policy on this kernel, with the
// harness home home: what the stage builds from the same plan (TC-04a).
func (b *LandlockBackend) Rules(policy sandbox.Policy, home string) (launch.Ruleset, error) {
	abi, err := b.abi()
	if err != nil {
		return launch.Ruleset{}, err
	}
	return launch.LandlockRules(abi, launch.Plan{Kind: launch.Confined, Backend: sandbox.BackendLandlock, Write: rootPaths(policy), Home: home})
}
