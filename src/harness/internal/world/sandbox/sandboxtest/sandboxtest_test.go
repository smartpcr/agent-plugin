package sandboxtest_test

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

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

const sid = session.ID("s-fake")

// layout is a project root P, a grant directory G, a scratch directory and a
// directory O outside all of them, as clean absolute host paths (none need
// exist: the fake checks no filesystem).
type layout struct{ P, G, Scratch, O string }

func newLayout(t *testing.T) layout {
	t.Helper()
	base := t.TempDir()
	return layout{P: filepath.Join(base, "proj"), G: filepath.Join(base, "grant"),
		Scratch: filepath.Join(base, "scratch"), O: filepath.Join(base, "out")}
}

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

// Policy issues the policy the arguments describe, each root sourced
// working-directory; Issue carries the network setting; input Issue refuses
// makes Policy panic, since it is the calling test's mistake.
func TestPolicyIssuesWhatItIsGiven(t *testing.T) {
	l := newLayout(t)
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, l.P, l.G)
	want := []sandbox.WriteRoot{{Path: l.P, Source: sandbox.SourceWorkingDirectory}, {Path: l.G, Source: sandbox.SourceWorkingDirectory}}
	if p.Mode() != sandbox.WorkspaceWrite || !slices.Equal(p.WriteRoots(), want) || p.Network() || p.Validate() != nil {
		t.Fatalf("Policy = %s %v net=%v", p.Mode(), p.WriteRoots(), p.Network())
	}
	if p, err := sandboxtest.Issue(sandbox.ReadOnly, true); err != nil || !p.Network() || p.Mode() != sandbox.ReadOnly {
		t.Fatalf("Issue(read-only, network) = %s net=%v, %v", p.Mode(), p.Network(), err)
	}
	if _, err := sandboxtest.Issue(sandbox.WorkspaceWrite, false, "rel"); herr.Kind(err) != sandbox.CodeInvalidRoot {
		t.Fatalf("Issue with a relative root: %v", err)
	}
	defer func() {
		r := recover()
		if s, _ := r.(string); !strings.HasPrefix(s, "sandboxtest: ") {
			t.Fatalf("Policy with a bad mode recovered %v, want a sandboxtest panic", r)
		}
	}()
	sandboxtest.Policy("bogus")
	t.Fatal("Policy with a bad mode did not panic")
}

// Resolve follows the real precedence: an explicit mode, then the session's
// SetMode, then Default (read-only when unset). Under workspace-write the roots
// are the session's project root, its grants and Scratch, in that order.
func TestResolvePrecedenceAndRoots(t *testing.T) {
	ctx := context.Background()
	l := newLayout(t)
	wd := workdirtest.New()
	wd.Set(sid, filepath.Join(l.P, "sub"), l.P)

	var zero sandboxtest.Resolver
	if p, err := zero.Resolve(ctx, sandbox.Request{Session: sid}); err != nil || p.Mode() != sandbox.ReadOnly || len(p.WriteRoots()) != 0 {
		t.Fatalf("zero resolver = %s %v, %v; want read-only with no roots", p.Mode(), p.WriteRoots(), err)
	}

	r := &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd, Scratch: l.Scratch}
	release, err := r.GrantRoot(ctx, sid, l.G)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	want := []sandbox.WriteRoot{{Path: l.P, Source: sandbox.SourceWorkingDirectory}, {Path: l.G, Source: sandbox.SourceGrant}, {Path: l.Scratch, Source: sandbox.SourceScratch}}
	if err != nil || p.Mode() != sandbox.WorkspaceWrite || !slices.Equal(p.WriteRoots(), want) {
		t.Fatalf("default resolve = %s %v, %v; want %v", p.Mode(), p.WriteRoots(), err, want)
	}
	release()
	release() // idempotent
	if p, err := r.Resolve(ctx, sandbox.Request{Session: sid}); err != nil || len(p.WriteRoots()) != 2 {
		t.Fatalf("after release: %v, %v", p.WriteRoots(), err)
	}
	if p, err := r.Resolve(ctx, sandbox.Request{Session: sid, Mode: sandbox.DangerFullAccess}); err != nil || p.Mode() != sandbox.DangerFullAccess || len(p.WriteRoots()) != 0 {
		t.Fatalf("explicit full access = %s %v, %v", p.Mode(), p.WriteRoots(), err)
	}
	if err := r.SetMode(ctx, sid, sandbox.ReadOnly); err != nil {
		t.Fatal(err)
	}
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: sid}); p.Mode() != sandbox.ReadOnly {
		t.Fatalf("the session's mode did not outrank Default: %s", p.Mode())
	}
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: sid, Mode: sandbox.WorkspaceWrite}); p.Mode() != sandbox.WorkspaceWrite {
		t.Fatalf("an explicit mode did not outrank the session's: %s", p.Mode())
	}

	_, err = r.Resolve(ctx, sandbox.Request{Session: sid, Mode: "full"})
	wantKind(t, "a bogus explicit mode", err, sandbox.CodeInvalidMode)
	_, err = r.Resolve(cancelled(), sandbox.Request{Session: sid})
	wantKind(t, "a cancelled resolve", err, sandbox.CodeUnavailable)
	if _, err := r.Resolve(ctx, sandbox.Request{Session: "s-unknown", Mode: sandbox.WorkspaceWrite}); err == nil {
		t.Fatal("a workspace-write resolve for a session with no working directory succeeded")
	}
	wd.Set("s-gone", l.P, l.P).Gone()
	if _, err := r.Resolve(ctx, sandbox.Request{Session: "s-gone", Mode: sandbox.WorkspaceWrite}); err == nil {
		t.Fatal("a workspace-write resolve for an unavailable working directory succeeded")
	}
}

// Escalate widens one call only as Approve decides: strictly wider, or the
// same workspace-write mode with an absolute path not already writable; it
// never changes the session's mode.
func TestEscalateWidensOneCall(t *testing.T) {
	ctx := context.Background()
	l := newLayout(t)
	wd := workdirtest.New()
	wd.Set(sid, l.P, l.P)
	var answer struct {
		mode  sandbox.Mode
		paths []string
		err   error
	}
	var asked []sandbox.EscalationRequest
	r := &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}

	_, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess})
	wantKind(t, "no Approve", err, sandbox.CodeEscalationRejected)

	r.Approve = func(req sandbox.EscalationRequest) (sandbox.Mode, []string, error) {
		asked = append(asked, req)
		return answer.mode, answer.paths, answer.err
	}
	answer.err = errors.New("alice said no")
	if _, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Justification: "j"}); !errors.Is(err, answer.err) {
		t.Fatalf("a refusing Approve: %v, want its own error", err)
	}
	answer.err = nil
	for name, c := range map[string]struct {
		mode  sandbox.Mode
		paths []string
	}{
		"relative path":       {sandbox.WorkspaceWrite, []string{"rel"}},
		"paths with full":     {sandbox.DangerFullAccess, []string{l.O}},
		"paths with ro":       {sandbox.ReadOnly, []string{l.O}},
		"narrower":            {sandbox.ReadOnly, nil},
		"same, no paths":      {sandbox.WorkspaceWrite, nil},
		"same, already below": {sandbox.WorkspaceWrite, []string{filepath.Join(l.P, "deep", "er")}},
		"same, the root":      {sandbox.WorkspaceWrite, []string{l.P}},
	} {
		answer.mode, answer.paths = c.mode, c.paths
		_, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Justification: "j"})
		if herr.Kind(err) != sandbox.CodeEscalationRejected {
			t.Errorf("%s: %v, want %s", name, err, sandbox.CodeEscalationRejected)
		}
	}

	answer.mode, answer.paths = sandbox.WorkspaceWrite, []string{l.O + string(filepath.Separator) + "."}
	p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Justification: "j"})
	want := []sandbox.WriteRoot{{Path: l.P, Source: sandbox.SourceWorkingDirectory}, {Path: l.O, Source: sandbox.SourceEscalation}}
	if err != nil || p.Mode() != sandbox.WorkspaceWrite || !slices.Equal(p.WriteRoots(), want) {
		t.Fatalf("same mode with an outside path = %v, %v; want %v (the path cleaned)", p.WriteRoots(), err, want)
	}
	answer.mode, answer.paths = sandbox.DangerFullAccess, nil
	if p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Justification: "j"}); err != nil || p.Mode() != sandbox.DangerFullAccess {
		t.Fatalf("a wider mode = %s, %v", p.Mode(), err)
	}
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: sid}); p.Mode() != sandbox.WorkspaceWrite || len(r.Changes()) != 0 {
		t.Fatalf("an escalation changed the session: %s, changes %v", p.Mode(), r.Changes())
	}
	if len(asked) == 0 || asked[0].Session != sid {
		t.Fatalf("Approve saw %+v", asked)
	}

	// A same-mode widening needs the session's own roots to compare against.
	answer.mode, answer.paths = sandbox.WorkspaceWrite, []string{l.O}
	if _, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: "s-unknown", Justification: "j"}); err == nil {
		t.Fatal("a same-mode widening of a session with no working directory succeeded")
	}
	_, err = r.Escalate(cancelled(), sandbox.EscalationRequest{Session: sid, Justification: "j"})
	wantKind(t, "a cancelled escalation", err, sandbox.CodeUnavailable)
}

// On Windows a path below a root in another letter case is already writable,
// and a path on another volume is not below it.
func TestEscalateComparesPathsAsTheHostDoes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding and volumes are Windows path rules")
	}
	ctx := context.Background()
	l := newLayout(t)
	wd := workdirtest.New()
	wd.Set(sid, l.P, l.P)
	var paths []string
	r := &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd,
		Approve: func(sandbox.EscalationRequest) (sandbox.Mode, []string, error) {
			return sandbox.WorkspaceWrite, paths, nil
		}}
	paths = []string{filepath.Join(strings.ToUpper(l.P), "x")}
	_, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Justification: "j"})
	wantKind(t, "a path below the root in another case", err, sandbox.CodeEscalationRejected)

	other := `Z:\out`
	if strings.EqualFold(filepath.VolumeName(l.P), "Z:") {
		other = `Y:\out`
	}
	paths = []string{other}
	if p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Justification: "j"}); err != nil || len(p.WriteRoots()) != 2 {
		t.Fatalf("a path on another volume = %v, %v; want it added", p.WriteRoots(), err)
	}
}

// ForHelper is read-only plus the helper's own roots, and unconfined only for a
// kind in UnconfinedKinds.
func TestForHelper(t *testing.T) {
	ctx := context.Background()
	l := newLayout(t)
	r := &sandboxtest.Resolver{Default: sandbox.DangerFullAccess, UnconfinedKinds: []string{"converter"}}
	if p, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp"}); err != nil || p.Mode() != sandbox.ReadOnly || len(p.WriteRoots()) != 0 {
		t.Fatalf("plain helper = %s %v, %v", p.Mode(), p.WriteRoots(), err)
	}
	p, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", WriteRoots: []string{l.Scratch}, Network: true, Require: sandbox.RequireFull})
	if err != nil || p.Mode() != sandbox.WorkspaceWrite || !p.Network() || p.Require() != sandbox.RequireFull ||
		!slices.Equal(p.WriteRoots(), []sandbox.WriteRoot{{Path: l.Scratch, Source: sandbox.SourceHelper}}) {
		t.Fatalf("helper with a root = %s %v net=%v require=%s, %v", p.Mode(), p.WriteRoots(), p.Network(), p.Require(), err)
	}
	_, err = r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", Unconfined: true})
	wantKind(t, "an unlisted unconfined helper", err, sandbox.CodeUnavailable)
	if p, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "converter", Unconfined: true}); err != nil || p.Mode() != sandbox.DangerFullAccess {
		t.Fatalf("a listed unconfined helper = %s, %v", p.Mode(), err)
	}
	_, err = r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", WriteRoots: []string{"rel"}})
	wantKind(t, "a helper with a relative root", err, sandbox.CodeInvalidRoot)
	_, err = r.ForHelper(cancelled(), sandbox.HelperRequest{Session: sid, Kind: "mcp"})
	wantKind(t, "a cancelled helper request", err, sandbox.CodeUnavailable)
}

// GrantRoot takes only an absolute, clean directory.
func TestGrantRootRefusesBadPaths(t *testing.T) {
	ctx := context.Background()
	l := newLayout(t)
	var r sandboxtest.Resolver
	for _, bad := range []string{"rel", l.G + string(filepath.Separator) + "."} {
		_, err := r.GrantRoot(ctx, sid, bad)
		wantKind(t, "GrantRoot("+bad+")", err, sandbox.CodeInvalidRoot)
	}
	_, err := r.GrantRoot(cancelled(), sid, l.G)
	wantKind(t, "a cancelled grant", err, sandbox.CodeUnavailable)
}

// SetMode refuses a bad mode or an ended context, lets OnModeChanging refuse,
// records each accepted change in Changes with the mode it replaced, and gives
// up a transition whose context ends during the listener.
func TestSetMode(t *testing.T) {
	ctx := context.Background()
	r := &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite}
	wantKind(t, "a bogus mode", r.SetMode(ctx, sid, "full"), sandbox.CodeInvalidMode)
	wantKind(t, "a cancelled SetMode", r.SetMode(cancelled(), sid, sandbox.ReadOnly), sandbox.CodeUnavailable)

	var seen []sandbox.ModeChange
	refuse := errors.New("a command is running")
	var refusing atomic.Bool
	r.OnModeChanging = func(c sandbox.ModeChange) error {
		seen = append(seen, c)
		if refusing.Load() {
			return refuse
		}
		return nil
	}
	if err := r.SetMode(ctx, sid, sandbox.DangerFullAccess); err != nil {
		t.Fatal(err)
	}
	refusing.Store(true)
	if err := r.SetMode(ctx, sid, sandbox.ReadOnly); !errors.Is(err, refuse) {
		t.Fatalf("a refused change: %v", err)
	}
	refusing.Store(false)
	if err := r.SetMode(ctx, sid, sandbox.ReadOnly); err != nil {
		t.Fatal(err)
	}
	want := []sandbox.ModeChange{
		{Session: sid, From: sandbox.WorkspaceWrite, To: sandbox.DangerFullAccess},
		{Session: sid, From: sandbox.DangerFullAccess, To: sandbox.ReadOnly},
	}
	if got := r.Changes(); !slices.Equal(got, want) {
		t.Fatalf("Changes = %v, want %v", got, want)
	}
	if len(seen) != 3 || seen[1].To != sandbox.ReadOnly || seen[1].From != sandbox.DangerFullAccess {
		t.Fatalf("OnModeChanging saw %v", seen)
	}

	lctx, cancel := context.WithCancel(ctx)
	r.OnModeChanging = func(sandbox.ModeChange) error { cancel(); return nil }
	wantKind(t, "cancelled during the listener", r.SetMode(lctx, sid, sandbox.WorkspaceWrite), sandbox.CodeUnavailable)
	if got := r.Changes(); len(got) != 2 {
		t.Fatalf("a change cancelled during the listener was recorded: %v", got)
	}
}

// endsWhileQueued is a context that is live at SetMode's first check and ended
// from then on, the shape of a caller that gives up while another transition of
// the same session runs.
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

func (c *endsWhileQueued) Done() <-chan struct{} { return c.done }
func (c *endsWhileQueued) Err() error {
	if c.checks.Add(1) == 1 {
		return nil
	}
	return context.Canceled
}
func (c *endsWhileQueued) Deadline() (time.Time, bool) { return time.Time{}, false }

// Transitions of one session are serialized: one queued behind a running
// transition gives up when its context ends, and the running one is unaffected.
func TestSetModeQueuedTransitionGivesUp(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	r := &sandboxtest.Resolver{OnModeChanging: func(c sandbox.ModeChange) error {
		if c.To == sandbox.WorkspaceWrite {
			close(entered)
			<-release
		}
		return nil
	}}
	var wg sync.WaitGroup
	var first error
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() { unblock(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		first = r.SetMode(ctx, sid, sandbox.WorkspaceWrite)
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
	if got := r.Changes(); len(got) != 1 || got[0].To != sandbox.WorkspaceWrite || got[0].From != sandbox.ReadOnly {
		t.Fatalf("Changes = %v, want only the running transition", got)
	}
}
