package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// New is the delegation manager over agents.
func New(agents agent.Registry) Runtime {
	return &manager{agents: agents, acts: map[session.ID]*activation{}, refusing: make(chan struct{})}
}

type manager struct {
	agents agent.Registry

	mu      sync.Mutex
	acts    map[session.ID]*activation // unsettled runs by child
	closing bool                       // the manager is unloading: no new runs
	// refusing is closed when closing is set: the tests' signal that every
	// later Start is refused (export_test.go). Nothing else reads it.
	refusing chan struct{}

	// trace is a test seam, nil in production: it records each settle point
	// a run passes, S1 to S7, with the child and a detail (export_test.go).
	// It is set before the first Start and never changed.
	trace func(child session.ID, point, detail string)
}

// mark records point at the trace seam, if one is set.
func (a *activation) mark(point, detail string) {
	if t := a.m.trace; t != nil {
		t(a.child, point, detail)
	}
}

// activation is one run. Its fiber is a child of the owner's and holds, as
// effects in this order: settle (S6, S7), the registry's handle effect, and
// release (S2 stop, S4 release). A fiber unloads its effects last first, so
// whichever trigger unloads it (the owner, Dispose, the run closing by
// itself) stops, releases and settles in that order.
type activation struct {
	m       *manager
	owner   *cordis.Context
	name    string
	child   session.ID
	creator agent.Ref

	fiber *cordis.Fiber
	ctx   *cordis.Context
	h     agent.Handle
	watch sync.WaitGroup

	mu      sync.Mutex
	from    session.Offset // the child's log length before it went live
	stopped bool           // S2 cancelled a still-working run; named in the Diagnostic when no turn end records it
	// releasing is set by the first trigger that releases the run (S1's close,
	// Dispose, or the release effect of an owner's teardown): a Start for the
	// same child or name then waits for this run to settle instead of being
	// refused (A2).
	releasing bool
	captured  *Result
	startErr  error
	cleanErr  error

	once    sync.Once
	settled chan struct{}
	result  Result
	// ready is closed when Start has returned; h is set by then, nil for a
	// start that failed before the agent was created.
	ready chan struct{}
}

func (m *manager) Start(owner *cordis.Context, s Spec) (Activation, error) {
	// A1 admit.
	switch {
	case owner == nil:
		return nil, errorf(CodeInvalid, nil, "Start needs an owner context")
	case strings.TrimSpace(s.Prompt) == "":
		return nil, errorf(CodeInvalid, nil, "a delegation needs a prompt")
	case s.Resume && s.Session == "":
		return nil, errorf(CodeInvalid, nil, "a resume needs the stored child session")
	}
	if s.Session == "" {
		s.Session = mint()
	}
	a := &activation{m: m, owner: owner, name: s.Name, child: s.Session, creator: s.Creator,
		settled: make(chan struct{}), ready: make(chan struct{})}
	if a.name == "" {
		a.name = string(s.Session)
	}

	// A2 reserve: one run per child, one name per owner, none once the
	// manager unloads (closeAll then sees every run admitted before). The
	// operations on one child are serialised: a start that meets a run of the
	// same child or name which is already releasing waits for that run to
	// settle and is then admitted, never refused for a state about to pass
	// (DH-T-SUBAGENT-9). A run still working holds its child and name.
	if err := m.reserve(owner, a); err != nil {
		return nil, err
	}
	defer close(a.ready)

	// A4 materialise: the activation fiber under the owner, then the agent,
	// owned by that fiber alone.
	fiber, err := owner.Apply("subagent:"+a.name, func(c *cordis.Context) error {
		a.ctx = c
		return nil
	})
	if err == nil {
		err = fiber.Wait()
	}
	if err != nil {
		if fiber != nil {
			fiber.Dispose()
		}
		m.forget(a)
		return nil, err
	}
	if _, err := a.ctx.Effect("subagent.settle", func() (cordis.Disposer, error) {
		return func() error { a.settle(); return nil }, nil
	}); err != nil {
		fiber.Dispose()
		m.forget(a)
		return nil, err
	}
	a.fiber = fiber
	var model agent.ModelSelection
	if s.Model != nil {
		model = *s.Model
	}
	create := m.agents.Create
	if s.Resume {
		create = m.agents.Resume
	}
	h, err := create(a.ctx, agent.CreateOptions{
		Session: s.Session, Preset: s.Preset, Parent: s.Creator, Model: model, Setup: a.setup(s.Setup),
	})
	if err != nil {
		return nil, a.abort(err)
	}
	a.h = h
	if _, err := a.ctx.Effect("subagent.release", func() (cordis.Disposer, error) {
		return func() error { return a.release() }, nil
	}); err != nil {
		return nil, a.abort(err)
	}

	// A5 accept: the task, as next-turn input from the creator.
	by := string(s.Creator.Session)
	if _, err := agent.Send(context.Background(), h.Agent(), agent.ListNextTurn,
		agent.InboxMessage{Source: SourceSubagent, By: by, Text: s.Prompt}); err != nil {
		return nil, a.abort(err)
	}

	// A6 publish.
	a.watch.Add(1)
	go a.closeWhenIdle()
	return a, nil
}

// reserve is A2: it registers a under owner, or refuses it. A run of the same
// child, or of the same name under owner, that is already releasing is waited
// for, and the admission is then retried.
func (m *manager) reserve(owner *cordis.Context, a *activation) error {
	for {
		if err := owner.Lifetime().Err(); err != nil {
			return errorf(CodeInactive, err, "the owner is not active")
		}
		m.mu.Lock()
		if m.closing {
			m.mu.Unlock()
			return errorf(CodeInactive, nil, "the subagent manager is unloading")
		}
		busy, err := m.conflict(owner, a)
		if busy == nil {
			m.acts[a.child] = a
			m.mu.Unlock()
			return nil
		}
		releasing := busy.isReleasing()
		m.mu.Unlock()
		if !releasing {
			return err
		}
		select {
		case <-busy.settled:
		case <-owner.Lifetime().Done():
		}
	}
}

// conflict is the run that holds a's child or a's name under owner, with the
// refusal it means; the caller holds m.mu.
func (m *manager) conflict(owner *cordis.Context, a *activation) (*activation, error) {
	if o, ok := m.acts[a.child]; ok {
		return o, errorf(CodeNameTaken, nil, "session %q already has an owner", a.child)
	}
	for _, o := range m.acts {
		if o.owner == owner && o.name == a.name {
			return o, errorf(CodeNameTaken, nil, "the owner already has a child named %q", a.name)
		}
	}
	return nil, nil
}

// begin marks the run as releasing; it is true for the first trigger only.
func (a *activation) begin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	first := !a.releasing
	a.releasing = true
	return first
}

func (a *activation) isReleasing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.releasing
}

// abort is a failed start: the run settles start-failed once its fiber, and
// with it anything created, is gone.
func (a *activation) abort(err error) error {
	a.mu.Lock()
	a.startErr = err
	a.mu.Unlock()
	a.fiber.Dispose()
	return err
}

// setup puts the capture (S3) in the child's scope ahead of the caller's
// Setup: the scope closes at teardown's T5, after the driver wrote the turn's
// closers and before the session closes, whichever trigger released it.
// The run's start offset is sampled here, before the caller's Setup and
// before the agent goes live, so no turn of the run can precede it.
func (a *activation) setup(user agent.SetupFunc) agent.SetupFunc {
	return func(sc *cordis.Context, ag agent.Agent) (func() error, error) {
		a.mu.Lock()
		a.from = ag.Session().Len()
		a.mu.Unlock()
		if _, err := sc.Effect("subagent.capture", func() (cordis.Disposer, error) {
			return func() error { a.capture(ag.Session()); return nil }, nil
		}); err != nil {
			return nil, err
		}
		if user != nil {
			return user(sc, ag)
		}
		return nil, nil
	}
}

// closeWhenIdle is S1: a run closes by itself once its child is idle with no
// waking input. Its release then unloads the activation fiber.
func (a *activation) closeWhenIdle() {
	defer a.watch.Done()
	_ = agent.WaitIdle(context.Background(), a.h.Agent())
	// Another trigger that began first (Dispose, the owner's teardown) is
	// what woke the wait; only a run that went idle by itself closes here.
	if a.begin() {
		a.mark("S1", "close")
	}
	a.fiber.Dispose()
}

// release is S2 and S4: a run still working is cancelled with cause parent
// and its pending input discarded; then the handle is disposed, which waits
// for the child's driver goroutine to exit. S3 runs inside that disposal.
func (a *activation) release() error {
	a.begin()
	ag := a.h.Agent()
	if ag.Status() == agent.StatusRunning {
		a.mu.Lock()
		a.stopped = true
		a.mu.Unlock()
		a.mark("S2", "stop "+string(agent.CauseParent))
	}
	err := errors.Join(
		agent.Cancel(ag, agent.CauseParent, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxDiscard}),
		a.h.Dispose(context.Background()),
	)
	if err != nil {
		a.mu.Lock()
		a.cleanErr = err
		a.mu.Unlock()
		a.mark("S4", "release "+string(CleanupFailed))
		return err
	}
	a.mark("S4", "release "+string(CleanupOK))
	return nil
}

// capture is S3: the outcome of the last turn since the run's start and the
// last non-empty assistant text.
func (a *activation) capture(s session.ReadSession) {
	a.mu.Lock()
	from := a.from
	stopped := a.stopped
	a.mu.Unlock()
	r := Result{Stop: StopAborted, Diagnostic: "released before the run ended a turn"}
	if stopped {
		r.Diagnostic = "stopped by the parent before the run ended a turn"
	}
	evs, err := s.Read(context.Background(), from, 0)
	if err != nil {
		r = Result{Stop: StopError, Diagnostic: err.Error()}
	}
	for _, e := range evs {
		switch e.Type() {
		case event.TurnEndType.Name():
			if te, err := event.TurnEndType.Decode(e.Record()); err == nil {
				r.Stop, r.Diagnostic = stopOf(te.Reason)
			}
		case event.AssistantMessage.Name():
			if p, err := event.AssistantMessage.Decode(e.Record()); err == nil {
				if t := textOf(p.Message); t != "" {
					r.Output = t
				}
			}
		}
	}
	a.mu.Lock()
	a.captured = &r
	a.mu.Unlock()
	a.mark("S3", "capture "+string(r.Stop))
}

func stopOf(r event.Reason) (StopReason, string) {
	switch r.Kind {
	case event.ReasonCompleted:
		return StopCompleted, ""
	case event.ReasonAborted, event.ReasonInterrupted:
		return StopAborted, strings.TrimSpace(r.Kind + " " + r.Cause)
	}
	d := r.Kind
	if r.Failure != nil {
		d += ": " + r.Failure.Message
	}
	return StopError, d
}

func textOf(m vocab.Message) string {
	var b strings.Builder
	for _, bl := range m.Blocks() {
		if bl.Text != nil {
			b.WriteString(bl.Text.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// settle is S6 and S7, once: the run leaves its owner and Result unblocks.
func (a *activation) settle() {
	a.once.Do(func() {
		a.mu.Lock()
		var r Result
		switch {
		case a.startErr != nil:
			r = Result{Stop: StopStartFailed, Diagnostic: a.startErr.Error()}
		case a.captured != nil:
			r = *a.captured
		case a.stopped:
			r = Result{Stop: StopAborted, Diagnostic: "stopped by the parent before the run was captured"}
		default:
			r = Result{Stop: StopAborted, Diagnostic: "released before the run was captured"}
		}
		r.Cleanup = CleanupOK
		if a.cleanErr != nil {
			r.Cleanup = CleanupFailed
			r.Diagnostic = strings.TrimSpace(r.Diagnostic + "; cleanup: " + a.cleanErr.Error())
		}
		a.result = r
		a.mu.Unlock()
		// S5, the ledger record, arrives with DH-D-SUBAGENT-2 (DH-S-29).
		a.m.forget(a)
		a.mark("S6", "free")
		a.mark("S7", "deliver "+string(r.Stop)+" cleanup "+string(r.Cleanup))
		close(a.settled)
	})
}

func (a *activation) Child() session.ID  { return a.child }
func (a *activation) Name() string       { return a.name }
func (a *activation) Agent() agent.Agent { return a.h.Agent() }

func (a *activation) Result(ctx context.Context) (Result, error) {
	select {
	case <-a.settled:
		return a.result, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (a *activation) Dispose(ctx context.Context) error {
	a.begin()
	a.fiber.Dispose()
	if _, err := a.Result(ctx); err != nil {
		return err
	}
	a.watch.Wait()
	return nil
}

func (m *manager) forget(a *activation) {
	m.mu.Lock()
	if m.acts[a.child] == a {
		delete(m.acts, a.child)
	}
	m.mu.Unlock()
}

func (m *manager) OwnerOf(child session.ID) (*cordis.Context, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.acts[child]
	if !ok {
		return nil, false
	}
	return a.owner, true
}

func (m *manager) of(owner *cordis.Context) []*activation {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*activation
	for _, a := range m.acts {
		if a.owner == owner {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y *activation) int { return strings.Compare(string(x.child), string(y.child)) })
	return out
}

func (m *manager) Children(owner *cordis.Context) []session.ID {
	var out []session.ID
	for _, a := range m.of(owner) {
		out = append(out, a.child)
	}
	return out
}

// WaitQuiet returns once every child of owner is idle with no waking input.
// A start still in progress is waited for, and children reserved meanwhile
// are waited for in turn: it returns on a snapshot that brought nothing new.
func (m *manager) WaitQuiet(ctx context.Context, owner *cordis.Context) error {
	seen := map[*activation]bool{}
	for {
		fresh := false
		for _, a := range m.of(owner) {
			if seen[a] {
				continue
			}
			fresh = true
			select {
			case <-a.ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			if a.h != nil {
				if err := agent.WaitIdle(ctx, a.h.Agent()); err != nil && ctx.Err() != nil {
					return err
				}
			}
			seen[a] = true
		}
		if !fresh {
			return nil
		}
	}
}

// closeAll is the manager unloading: it refuses new runs and, under the same
// lock, takes every run admitted so far, starts still in progress included;
// each is waited for, released and settled before closeAll returns.
func (m *manager) closeAll() {
	m.mu.Lock()
	if !m.closing {
		close(m.refusing)
	}
	m.closing = true
	acts := make([]*activation, 0, len(m.acts))
	for _, a := range m.acts {
		acts = append(acts, a)
	}
	m.mu.Unlock()
	for _, a := range acts {
		<-a.ready
		if a.fiber == nil {
			continue
		}
		a.fiber.Dispose()
		<-a.settled
		a.watch.Wait()
	}
}

func mint() session.ID {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return session.ID("sub_" + hex.EncodeToString(b[:]))
}
