package local

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
)

// Decide prepares argv under p and runs the host decision on r, as Confirm does
// once the stage has sent it, with no process: the in-process half of TC-04a.
// It returns the prepared plan (command and channel numbers filled in).
func Decide(ctx context.Context, s *Service, p sandbox.Policy, argv []string, r launch.Ready) (launch.Plan, sandbox.Confinement, error) {
	plan, d, err := s.prepare(ctx, p, argv)
	if err != nil {
		return plan, sandbox.Confinement{}, err
	}
	defer func() { _ = d.release(ctx) }()
	plan.Argv, plan.Control, plan.Status = argv, 3, 4
	c, err := d.confirm(ctx, plan, r)
	return plan, c, err
}

// StageRunner stands in for the real probe stage.
type StageRunner = func(ctx context.Context, h Host, plan launch.Plan, policy sandbox.Policy, prefix []string) (Probe, error)

// NewBwrap is the bubblewrap backend with find in place of the PATH lookup and
// stage in place of the real probe stage.
func NewBwrap(find func() (string, error), stage StageRunner) Backend { return newBwrap(find, stage) }

// NewLandlock is the Landlock backend with stage in place of the real probe stage.
func NewLandlock(kernel Kernel, stage StageRunner) *LandlockBackend {
	return newLandlock(kernel, stage)
}

// PreparedPrefix prepares argv under p and returns the outside confiner the
// start's stage would run under, then releases the start.
func PreparedPrefix(ctx context.Context, s *Service, p sandbox.Policy, argv []string) (launch.Plan, []string, error) {
	plan, d, err := s.prepare(ctx, p, argv)
	if err != nil {
		return plan, nil, err
	}
	defer func() { _ = d.release(ctx) }()
	return plan, d.prefix, nil
}

// ServiceHost is the Host s resolved.
func ServiceHost(s *Service) Host { return s.host }

// SetProbeEnv adds env to every real probe stage's environment until the test ends.
func SetProbeEnv(t interface{ Cleanup(func()) }, env ...string) {
	saved := probeEnvExtra
	probeEnvExtra = env
	t.Cleanup(func() { probeEnvExtra = saved })
}
