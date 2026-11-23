package approval

import (
	"context"
	"slices"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// A pending approval lives in memory only (DH-ADR-HUMAN-1), so a host that dies
// while a request waits loses the prompt; its log keeps approval/asked with no
// approval/decided. The resume closer writes the missing decision, unavailable
// with detail host-stopped and by the harness, so the audit pair is closed and
// says that nobody decided. Nothing ran: the pipeline starts a body only after
// an allowed decision, which this log does not hold.

// resumeScan reads log once: the approval/asked records with no approval/decided,
// oldest first, and the approval fold of the log.
func resumeScan(ctx context.Context, log GrantLog) ([]Asked, PolicyState, error) {
	var open []Asked
	fold := PolicyView()
	st := fold.Init(session.Header{})
	const page = 256
	for from, n := event.Offset(0), log.Len(); from < n; from += page {
		evs, err := log.Read(ctx, from, page)
		if err != nil {
			return nil, PolicyState{}, err
		}
		for _, ev := range evs {
			switch ev.Type() {
			case AskedType.Name():
				if a, err := AskedType.As(ev); err == nil {
					open = append(open, a)
				}
			case DecidedType.Name():
				if d, err := DecidedType.As(ev); err == nil {
					open = slices.DeleteFunc(open, func(a Asked) bool { return a.ID == d.ID })
				}
			case PolicyType.Name():
				st, _ = fold.Apply(st, ev)
			}
		}
	}
	return open, st, nil
}

// closeOpen appends approval/decided{unavailable, host-stopped} by the harness for
// each request of open, returning their ids.
func closeOpen(ctx context.Context, log Log, open []Asked) ([]string, error) {
	ids := make([]string, 0, len(open))
	for _, a := range open {
		d, err := DecidedType.Draft(Decided{ID: a.ID, Tool: a.Tool, Call: a.Call, Outcome: OutcomeUnavailable,
			Detail: DetailHostStopped, By: By{Principal: PrincipalHarness}})
		if err == nil {
			_, err = log.Append(context.WithoutCancel(ctx), d)
		}
		if err != nil {
			return ids, err
		}
		ids = append(ids, a.ID)
	}
	return ids, nil
}

// CloseOnResume appends approval/decided{unavailable, host-stopped} for each
// approval/asked of log with no approval/decided, in the order they were asked,
// and returns their ids. Service.Bind runs it when an agent is created, before
// the agent accepts input.
func CloseOnResume(ctx context.Context, log GrantLog) ([]string, error) {
	open, _, err := resumeScan(ctx, log)
	if err != nil {
		return nil, err
	}
	return closeOpen(ctx, log, open)
}

// Bind ties the service to the agent lifecycle under root, as Grants.Bind ties
// the grant table (M1-G-PIPE-4): when an agent is created (a new or resumed
// incarnation) its session's log is read once, every request the previous holder
// left open is closed (CloseOnResume), and the session's policy is restored from
// the log's approval fold; a failed close fails the creation, so the agent never
// accepts input over an open pair. When an agent is disposed its session's policy
// is forgotten here (the log keeps it). Sessions also serves SetPolicy when
// Config.Sessions is nil. The returned handles unbind.
//
// A composition binds the service before the grant table, so the closers of one
// resume are the decisions first and then the grant ends.
func (s *Service) Bind(root *cordis.Context, sessions Sessions) ([]*cordis.EffectHandle, error) {
	if s.sessions.Load() == nil {
		s.sessions.Store(&sessions)
	}
	created, err := agent.Created.On(root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		ref := n.Agent.Ref()
		h, ok := sessions.Get(ref.Session)
		if !ok {
			s.forgetPolicy(ref.Session)
			return nil
		}
		ctx := context.Background()
		open, st, err := resumeScan(ctx, h)
		if err != nil {
			return err
		}
		s.restorePolicy(ref.Session, st)
		_, err = closeOpen(ctx, h, open)
		return err
	})
	if err != nil {
		return nil, err
	}
	disposed, err := agent.Disposed.On(root, func(_ *cordis.Context, n agent.DisposedNotice) error {
		s.forgetPolicy(n.Ref.Session)
		return nil
	})
	if err != nil {
		created.Dispose()
		return nil, err
	}
	return []*cordis.EffectHandle{created, disposed}, nil
}
