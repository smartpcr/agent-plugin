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
