package policy_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func wantKind(t *testing.T, what string, err error, want herr.Reason) {
	t.Helper()
	if got := herr.Kind(err); got != want {
		t.Fatalf("%s: %v (code %q), want %q", what, err, got, want)
	}
}

// sessions is a session.Service whose sessions a test can end without closing
// them (Get no longer returns them, as after another process took one over),
// and whose log reads, appends and stored opens can be made to fail.
type sessions struct {
	session.Service
	mu         sync.Mutex
	ended      map[session.ID]bool
	failRead   bool
	failAppend bool
	failOpen   bool
	opens      int
}

var errLog = errors.New("injected log failure")

func (s *sessions) Get(id session.ID) (session.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended[id] {
		return nil, false
	}
	live, ok := s.Service.Get(id)
	switch {
	case !ok:
		return nil, false
	case s.failRead:
		return failingRead{live}, true
	case s.failAppend:
		return failingAppend{live}, true
	}
	return live, true
}

func (s *sessions) OpenRead(ctx context.Context, id session.ID) (session.ReadSession, func(), error) {
	s.mu.Lock()
	s.opens++
	fail := s.failOpen
	s.mu.Unlock()
	if fail {
		return nil, nil, errLog
	}
	return s.Service.OpenRead(ctx, id)
}

func (s *sessions) set(f func(s *sessions)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended == nil {
		s.ended = map[session.ID]bool{}
	}
	f(s)
}

type failingRead struct{ session.Session }

func (failingRead) Read(context.Context, session.Offset, int, ...session.ReadOption) ([]event.Event, error) {
	return nil, errLog
}

type failingAppend struct{ session.Session }

func (failingAppend) Append(context.Context, event.Draft) (event.Receipt, error) {
	return event.Receipt{}, errLog
}

// liveSessions is a session service over a memory store with live sessions ids.
func liveSessions(t *testing.T, root *cordis.Context, ids ...session.ID) (*sessions, map[session.ID]session.Handle) {
	t.Helper()
	svc := session.NewService(root, memstore.New(), session.Config{})
	hs := map[session.ID]session.Handle{}
	for _, id := range ids {
		h, err := svc.Create(context.Background(), nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(context.Background()) })
		hs[id] = h
	}
	return &sessions{Service: svc}, hs
}

// Every entry point refuses a call whose context has already ended, before it
// reads or changes anything.
func TestEndedContextIsRefused(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	r := newResolver(t, policy.Config{}, baseDeps(t, root, w))
	ctx := cancelled()
	_, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	wantKind(t, "Resolve", err, sandbox.CodeUnavailable)
	_, err = r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess, Justification: "j"})
	wantKind(t, "Escalate", err, sandbox.CodeUnavailable)
	_, err = r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp"})
	wantKind(t, "ForHelper", err, sandbox.CodeUnavailable)
	_, err = r.GrantRoot(ctx, sid, w.G)
	wantKind(t, "GrantRoot", err, sandbox.CodeUnavailable)
	wantKind(t, "SetMode", r.SetMode(ctx, sid, sandbox.ReadOnly), sandbox.CodeUnavailable)
}

// The configured network setting is the default of a session that never set
// one, and the configured confinement level is a floor a helper cannot loosen.
func TestConfiguredNetworkAndRequire(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	r := newResolver(t, policy.Config{Network: policy.NetworkDenied, Require: sandbox.RequireFull}, baseDeps(t, root, w))
	p, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	if err != nil || p.Network() || p.Require() != sandbox.RequireFull {
		t.Fatalf("resolve = net %v require %s, %v; want the configured denied and full", p.Network(), p.Require(), err)
	}
	if p, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", Require: sandbox.RequireAny}); err != nil || p.Require() != sandbox.RequireFull {
		t.Fatalf("a helper asking for any under a configured full = %s, %v", p.Require(), err)
	}
	loose := newResolver(t, policy.Config{}, baseDeps(t, root, w))
	if p, err := loose.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", Require: sandbox.RequireFull}); err != nil || p.Require() != sandbox.RequireFull {
		t.Fatalf("a helper may ask for full: %s, %v", p.Require(), err)
	}
}

// rootsFS is a MemFS on which a test can make a directory vanish (as another
// process removing it would) and on which one path cannot be checked at all.
type rootsFS struct {
	*fstest.MemFS
	mu     sync.Mutex
	gone   map[string]bool
	broken string
}

func newRootsFS(t *testing.T, dirs ...string) *rootsFS {
	t.Helper()
	f := &rootsFS{MemFS: fstest.NewMemFS(), gone: map[string]bool{}}
	for _, d := range dirs {
		if err := f.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *rootsFS) remove(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gone[p] = true
}

func (f *rootsFS) restore(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.gone, p)
}

func (f *rootsFS) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	f.mu.Lock()
	gone, broken := f.gone[r.Path], r.Path == f.broken
	f.mu.Unlock()
	switch {
	case broken:
		return fs.Info{}, herr.New(fs.CodeIO, "injected stat failure")
	case gone:
		return fs.Info{}, nil
	}
	return f.MemFS.Stat(ctx, r)
}

// A write root is re-checked on every call (sandbox-ssh.md sec. 3.2): a grant
// removed after it was granted, a helper root that is not a directory, or a
// root that cannot be checked at all fails the call with sandbox/invalid-root.
func TestRootsAreRecheckedOnEveryCall(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	mem := newRootsFS(t, w.P, w.G)
	unchecked := filepath.Join(filepath.Dir(w.P), "unchecked")
	mem.broken = unchecked
	deps := baseDeps(t, root, w)
	deps.FS = mem
	deps.Scratch = nil
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	if _, err := r.GrantRoot(ctx, sid, w.G); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, sandbox.Request{Session: sid}); err != nil {
		t.Fatal(err)
	}
	mem.remove(w.G)
	_, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	wantKind(t, "a grant removed since it was granted", err, sandbox.CodeInvalidRoot)

	_, err = r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", WriteRoots: []string{filepath.Join(w.P, "missing")}})
	wantKind(t, "a helper root that does not exist", err, sandbox.CodeInvalidRoot)
	// The check's own failure keeps its code innermost (herr.Kind); the refusal
	// around it is sandbox/invalid-root.
	_, err = r.GrantRoot(ctx, sid, unchecked)
	var he *herr.Error
	if !errors.As(err, &he) || he.Reason() != string(sandbox.CodeInvalidRoot) || herr.Kind(err) != fs.CodeIO {
		t.Fatalf("a root that cannot be checked: %v; want sandbox/invalid-root around the check's fs/io", err)
	}
}

// The workspace-write roots need the session's directory and scratch: when
// either cannot be had, the call fails rather than running with fewer roots.
func TestMissingWorkspaceOrScratchFailsTheCall(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	deps := baseDeps(t, root, w)
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	if _, err := r.Resolve(ctx, sandbox.Request{Session: "s-nowhere"}); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("a session with no working directory: %v", err)
	}
	wd := workdirtest.New()
	wd.Set(sid, w.W, w.P).Gone()
	gone := deps
	gone.Workdir = wd
	if _, err := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, gone).Resolve(ctx, sandbox.Request{Session: sid}); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("a working directory that is gone: %v", err)
	}
	noScratch := deps
	noScratch.Scratch = func(session.ID) (string, error) { return "", errors.New("no scratch volume") }
	_, err := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, noScratch).Resolve(ctx, sandbox.Request{Session: sid})
	wantKind(t, "a scratch directory that cannot be had", err, sandbox.CodeUnavailable)
	// A read-only call needs neither.
	if _, err := newResolver(t, policy.Config{}, noScratch).Resolve(ctx, sandbox.Request{Session: "s-nowhere"}); err != nil {
		t.Fatalf("a read-only call: %v", err)
	}
}

// Escalate: a mode named only by an unknown Mode is sandbox/invalid-mode; an
// approver's own escalation-rejected error is returned as it is; a same-mode
// widening needs the session's current roots; and an escalation root that
// vanished while the approver was deciding is refused on the call.
func TestEscalateRefusals(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	out := filepath.Join(filepath.Dir(w.P), "out")
	mem := newRootsFS(t, w.P, out)
	deps := baseDeps(t, root, w)
	deps.FS = mem
	deps.Scratch = nil
	own := herr.New(sandbox.CodeEscalationRejected, "alice: not this one")
	var answer func(policy.Escalation) error
	deps.Approver = policy.ApproverFunc(func(_ context.Context, e policy.Escalation) error { return answer(e) })
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)

	_, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: "root", Justification: "j"})
	wantKind(t, "an unknown mode", err, sandbox.CodeInvalidMode)

	answer = func(policy.Escalation) error { return own }
	if _, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess, Justification: "j"}); err != own {
		t.Fatalf("an approver's own rejection = %v, want it unchanged", err)
	}

	answer = func(e policy.Escalation) error {
		if !slices.Equal(e.Paths, []string{out}) {
			t.Errorf("approver asked for %v, want the cleaned, deduplicated %v", e.Paths, []string{out})
		}
		mem.remove(out)
		return nil
	}
	_, err = r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{out, out + string(filepath.Separator) + "."}, Justification: "j"})
	wantKind(t, "a root removed while the approver decided", err, sandbox.CodeInvalidRoot)

	mem.restore(out)
	answer = func(policy.Escalation) error { return nil }
	_, err = r.Escalate(ctx, sandbox.EscalationRequest{Session: "s-nowhere", Mode: sandbox.WorkspaceWrite, Paths: []string{out}, Justification: "j"})
	if herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("a same-mode widening of a session with no working directory: %v", err)
	}
}

// On Windows the roots are compared as the host compares paths: a root spelled
// in another case is the same root and listed once, a path below a root in
// another case is already writable, and a path on another volume is not below
// any root.
func TestRootsCompareAsTheHostDoes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding and volumes are Windows path rules")
	}
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	deps := baseDeps(t, root, w)
	deps.Scratch = nil
	deps.Approver = policy.ApproverFunc(func(context.Context, policy.Escalation) error { return nil })
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite, ExtraWriteRoots: []string{strings.ToUpper(w.P)}}, deps)
	p, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	if err != nil || !slices.Equal(p.WriteRoots(), []sandbox.WriteRoot{{Path: w.P, Source: sandbox.SourceWorkspace}}) {
		t.Fatalf("roots = %v, %v; want the workspace once", p.WriteRoots(), err)
	}
	_, err = r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{filepath.Join(strings.ToUpper(w.P), "x")}, Justification: "j"})
	wantKind(t, "a path below the workspace in another case", err, sandbox.CodeEscalationRejected)
	other := `Z:\out`
	if strings.EqualFold(filepath.VolumeName(w.P), "Z:") {
		other = `Y:\out`
	}
	if p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{other}, Justification: "j"}); err != nil || len(p.WriteRoots()) != 2 {
		t.Fatalf("a path on another volume = %v, %v; want it added", p.WriteRoots(), err)
	}
}

// A session log that cannot be read fails the call with sandbox/unavailable
// rather than falling back to the default mode, live or stored.
func TestUnreadableLogFailsTheCall(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	svc, _ := liveSessions(t, root, sid)
	deps := baseDeps(t, root, w)
	deps.Sessions = svc
	deps.Approver = policy.ApproverFunc(func(context.Context, policy.Escalation) error { return nil })

	svc.set(func(s *sessions) { s.failRead = true })
	r := newResolver(t, policy.Config{}, deps)
	_, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	wantKind(t, "Resolve over an unreadable live log", err, sandbox.CodeUnavailable)
	_, err = r.Mode(ctx, sid)
	wantKind(t, "Mode over an unreadable live log", err, sandbox.CodeUnavailable)
	_, err = r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess, Justification: "j"})
	wantKind(t, "Escalate over an unreadable live log", err, sandbox.CodeUnavailable)
	wantKind(t, "SetMode over an unreadable live log", r.SetMode(ctx, sid, sandbox.DangerFullAccess), sandbox.CodeUnavailable)

	svc.set(func(s *sessions) { s.failRead, s.failOpen = false, true })
	_, err = r.Mode(ctx, "s-stored")
	wantKind(t, "Mode of a stored session that cannot be opened", err, sandbox.CodeUnavailable)
}

// SetMode needs a session log to write to; a failed append leaves the mode as
// it was; with no actor on the context the change is the harness's.
func TestSetModeAppendAndAttribution(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	noLog := newResolver(t, policy.Config{}, baseDeps(t, root, w))
	wantKind(t, "SetMode with no session log", noLog.SetMode(ctx, sid, sandbox.WorkspaceWrite), sandbox.CodeUnavailable)

	svc, hs := liveSessions(t, root, sid)
	deps := baseDeps(t, root, w)
	deps.Sessions = svc
	r := newResolver(t, policy.Config{}, deps)
	svc.set(func(s *sessions) { s.failAppend = true })
	err := r.SetMode(ctx, sid, sandbox.WorkspaceWrite)
	if herr.Kind(err) != sandbox.CodeUnavailable || !errors.Is(err, errLog) {
		t.Fatalf("a failed append: %v", err)
	}
	if m, err := r.Mode(ctx, sid); err != nil || m != sandbox.ReadOnly {
		t.Fatalf("after a failed append the mode is %s, %v; want read-only", m, err)
	}
	svc.set(func(s *sessions) { s.failAppend = false })

	// Another event in the log between mode changes is not a mode change.
	if d, err := workdir.ChangeType.Draft(workdir.ChangePayload{Directory: w.P, Workspace: w.P, Root: w.P, Cause: workdir.CauseMove}); err != nil {
		t.Fatal(err)
	} else if _, err := hs[sid].Append(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMode(ctx, sid, sandbox.WorkspaceWrite); err != nil {
		t.Fatal(err)
	}
	evs, err := hs[sid].Read(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sets []policy.ModeSet
	for _, ev := range evs {
		if ev.Type() == sandbox.ModeEventType {
			m, err := policy.ModeType.As(ev)
			if err != nil {
				t.Fatal(err)
			}
			sets = append(sets, m)
		}
	}
	if len(sets) != 1 || sets[0].By != (policy.Actor{Principal: policy.PrincipalHarness}) || sets[0].From != sandbox.ReadOnly {
		t.Fatalf("sandbox/mode events = %+v; want one by %q from read-only", sets, policy.PrincipalHarness)
	}
	if m, err := newResolver(t, policy.Config{}, deps).Mode(ctx, sid); err != nil || m != sandbox.WorkspaceWrite {
		t.Fatalf("a fresh resolver folding a log with other events = %s, %v", m, err)
	}
}

// Recording the configured default pins it without asking anyone: no
// ModeChanging is dispatched, sandbox/mode is appended, and a resolver with
// another default still resolves the pinned mode.
func TestSetModePinsTheDefault(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	svc, _ := liveSessions(t, root, sid)
	deps := baseDeps(t, root, w)
	deps.Sessions = svc
	var dispatched atomic.Int32
	if _, err := sandbox.ModeChanging.On(owner(t, root, "test.listener"), func(*cordis.Context, sandbox.ModeChange) error {
		dispatched.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := newResolver(t, policy.Config{}, deps)
	if err := r.SetMode(ctx, sid, sandbox.ReadOnly); err != nil {
		t.Fatal(err)
	}
	if n := dispatched.Load(); n != 0 {
		t.Fatalf("pinning the default dispatched ModeChanging %d times", n)
	}
	if m, err := newResolver(t, policy.Config{DefaultMode: sandbox.DangerFullAccess}, deps).Mode(ctx, sid); err != nil || m != sandbox.ReadOnly {
		t.Fatalf("a resolver with another default = %s, %v; want the pinned read-only", m, err)
	}
}

// A listener that cancels the caller's context makes SetMode give up before
// anything is appended.
func TestSetModeCancelledDuringModeChanging(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	svc, _ := liveSessions(t, root, sid)
	deps := baseDeps(t, root, w)
	deps.Sessions = svc
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := sandbox.ModeChanging.On(owner(t, root, "test.listener"), func(*cordis.Context, sandbox.ModeChange) error {
		cancel()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := newResolver(t, policy.Config{}, deps)
	wantKind(t, "cancelled during ModeChanging", r.SetMode(ctx, sid, sandbox.WorkspaceWrite), sandbox.CodeUnavailable)
	if m, err := r.Mode(context.Background(), sid); err != nil || m != sandbox.ReadOnly {
		t.Fatalf("after a cancelled change the mode is %s, %v", m, err)
	}
}

// endsWhileQueued is a context live at SetMode's first check and ended from
// then on: a caller that gives up while another transition of the session runs.
type endsWhileQueued struct {
	context.Context
	checks atomic.Int32
	done   chan struct{}
}

func newEndsWhileQueued() *endsWhileQueued {
	c := &endsWhileQueued{Context: context.Background(), done: make(chan struct{})}
	close(c.done)
	return c
}

func (c *endsWhileQueued) Done() <-chan struct{}       { return c.done }
func (c *endsWhileQueued) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *endsWhileQueued) Err() error {
	if c.checks.Add(1) == 1 {
		return nil
	}
	return context.Canceled
}

// Transitions of one session are serialized: a SetMode queued behind one whose
// ModeChanging listener is still running gives up when its context ends, and
// the running one completes.
func TestSetModeQueuedTransitionGivesUp(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	svc, _ := liveSessions(t, root, sid)
	deps := baseDeps(t, root, w)
	deps.Sessions = svc
	entered, release := make(chan struct{}), make(chan struct{})
	if _, err := sandbox.ModeChanging.On(owner(t, root, "test.listener"), func(_ *cordis.Context, c sandbox.ModeChange) error {
		if c.To == sandbox.WorkspaceWrite {
			close(entered)
			<-release
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := newResolver(t, policy.Config{}, deps)
	var wg sync.WaitGroup
	var first error
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() { unblock(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		first = r.SetMode(context.Background(), sid, sandbox.WorkspaceWrite)
	}()
	<-entered
	err := r.SetMode(newEndsWhileQueued(), sid, sandbox.DangerFullAccess)
	wantKind(t, "a transition queued behind another", err, sandbox.CodeUnavailable)
	if !strings.Contains(err.Error(), "another transition") {
		t.Fatalf("the queued transition failed for another reason: %v", err)
	}
	unblock()
	wg.Wait()
	if first != nil {
		t.Fatalf("the running transition: %v", first)
	}
	if m, err := r.Mode(context.Background(), sid); err != nil || m != sandbox.WorkspaceWrite {
		t.Fatalf("mode = %s, %v; want the running transition's", m, err)
	}
}

// A session that is no longer live is read from its stored log, not from what
// the resolver cached while it was live: an entry is dropped when the ended
// session is next read, and by the sweep when the cache grows, so a session that
// comes back after another resolver changed its mode is folded afresh.
func TestEndedSessionsLeaveTheCache(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	a, b, c := session.ID("s-a"), session.ID("s-b"), session.ID("s-c")
	svc, _ := liveSessions(t, root, a, b, c)
	deps := baseDeps(t, root, w)
	deps.Sessions = svc
	r := newResolver(t, policy.Config{}, deps)
	other := newResolver(t, policy.Config{}, deps)
	ended := func(id session.ID, v bool) { svc.set(func(s *sessions) { s.ended[id] = v }) }
	mode := func(r *policy.Resolver, id session.ID) sandbox.Mode {
		t.Helper()
		m, err := r.Mode(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	// Read while ended: the stored log answers.
	if m := mode(r, a); m != sandbox.ReadOnly {
		t.Fatalf("a = %s", m)
	}
	ended(a, true)
	if m := mode(r, a); m != sandbox.ReadOnly || svc.opens != 1 {
		t.Fatalf("an ended session = %s after %d stored opens; want it read from the store", m, svc.opens)
	}
	ended(a, false)
	if err := other.SetMode(ctx, a, sandbox.DangerFullAccess); err != nil {
		t.Fatal(err)
	}
	if m := mode(r, a); m != sandbox.DangerFullAccess {
		t.Fatalf("a back live after another resolver set its mode = %s; want it folded afresh", m)
	}

	// Never read while ended: the sweep drops it once the cache has grown.
	ended(a, true)
	mode(r, b)
	mode(r, c)
	ended(a, false)
	if err := other.SetMode(ctx, a, sandbox.WorkspaceWrite); err != nil {
		t.Fatal(err)
	}
	if m := mode(r, a); m != sandbox.WorkspaceWrite {
		t.Fatalf("a back live after the sweep = %s; want it folded afresh", m)
	}
}

// ModeType refuses a sandbox/mode that would fold to nonsense.
func TestModeEventValidates(t *testing.T) {
	for name, m := range map[string]policy.ModeSet{
		"mode":      {Mode: "full", Network: policy.NetworkAllowed, By: policy.Actor{Principal: "p"}},
		"from":      {Mode: sandbox.ReadOnly, From: "full", Network: policy.NetworkAllowed, By: policy.Actor{Principal: "p"}},
		"network":   {Mode: sandbox.ReadOnly, Network: "maybe", By: policy.Actor{Principal: "p"}},
		"principal": {Mode: sandbox.ReadOnly, Network: policy.NetworkAllowed},
	} {
		if _, err := policy.ModeType.Draft(m); err == nil {
			t.Errorf("%s: a bad sandbox/mode drafted", name)
		}
	}
	if _, err := policy.ModeType.Draft(policy.ModeSet{Mode: sandbox.ReadOnly, Network: policy.NetworkDenied, By: policy.Actor{Principal: "p"}}); err != nil {
		t.Fatalf("a good sandbox/mode: %v", err)
	}
	if st := policy.ModeView().Init(session.Header{}); st != (policy.ModeState{}) {
		t.Fatalf("the fold starts at %+v, want the zero state (the configured default applies)", st)
	}
}

// The label of a value that is not a mode is the value itself; Describe states
// a policy's mode, roots and network; TextVersion is the SHA-256 of the text.
func TestRenderHelpers(t *testing.T) {
	if got := policy.ModeLabel("custom"); got != "custom" {
		t.Fatalf("ModeLabel(custom) = %q", got)
	}
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	deps := baseDeps(t, root, w)
	deps.Scratch = nil
	p, err := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps).Resolve(context.Background(), sandbox.Request{Session: sid})
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.Describe(p); got.Mode != sandbox.WorkspaceWrite || !got.Network ||
		!slices.Equal(got.WriteRoots, []sandbox.WriteRoot{{Path: w.P, Source: sandbox.SourceWorkspace}}) {
		t.Fatalf("Describe = %+v", got)
	}
	if got := policy.TextVersion(""); got != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("TextVersion(\"\") = %s", got)
	}
}

// The component provides the resolver under sandbox.ResolverKey over the
// session, working-directory and filesystem services, and refuses a bad config.
func TestComponentProvidesTheResolver(t *testing.T) {
	ctx := context.Background()
	root := testkit.Tree(t)
	w := newWorld(t)
	svc, _ := liveSessions(t, root, sid)
	wd := workdirtest.New()
	wd.Set(sid, w.W, w.P)
	mem := fstest.NewMemFS()
	if _, err := session.Key.Provide(root, session.Service(svc.Service)); err != nil {
		t.Fatal(err)
	}
	if _, err := workdir.Key.Provide(root, workdir.Lookup(wd)); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Key.Provide(root, fs.FS(mem)); err != nil {
		t.Fatal(err)
	}
	if err := mem.Mkdir(w.P); err != nil {
		t.Fatal(err)
	}
	f, err := root.Use(policy.Component, policy.Config{DefaultMode: sandbox.WorkspaceWrite})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	res, err := sandbox.ResolverKey.Get(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := res.Resolve(ctx, sandbox.Request{Session: sid})
	if err != nil || !slices.Equal(p.WriteRoots(), []sandbox.WriteRoot{{Path: w.P, Source: sandbox.SourceWorkspace}}) {
		t.Fatalf("the component's resolver = %v, %v", p.WriteRoots(), err)
	}
	if err := res.SetMode(ctx, sid, sandbox.DangerFullAccess); err != nil {
		t.Fatalf("the component's resolver appends through the session service: %v", err)
	}

	bad, err := root.Use(policy.Component, policy.Config{DefaultMode: "full"})
	if err == nil {
		err = bad.Wait()
	}
	if err == nil {
		t.Fatal("the component loaded with a bad default mode")
	}
}
