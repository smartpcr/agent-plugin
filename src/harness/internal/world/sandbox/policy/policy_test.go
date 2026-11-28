package policy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

const sid = session.ID("s-policy")

// owner is a component context of root, the one that dispatches ModeChanging.
func owner(t *testing.T, root *cordis.Context, name string) *cordis.Context {
	t.Helper()
	var octx *cordis.Context
	f, err := root.Apply(name, func(c *cordis.Context) error { octx = c; return nil })
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	return octx
}

func liveSession(t *testing.T, root *cordis.Context) (session.Service, session.Handle) {
	t.Helper()
	sessions := session.NewService(root, memstore.New(), session.Config{})
	h, err := sessions.Create(context.Background(), nil, session.CreateOptions{Header: session.Header{ID: string(sid)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return sessions, h
}

// world is a fake working directory W below the workspace root P, a grant
// directory G and a scratch directory, all real host paths.
type world struct{ P, W, G, Scratch string }

func newWorld(t *testing.T) world {
	t.Helper()
	base := t.TempDir()
	w := world{P: filepath.Join(base, "proj"), G: filepath.Join(base, "grant"), Scratch: filepath.Join(base, "scratch")}
	w.W = filepath.Join(w.P, "sub")
	for _, d := range []string{w.W, w.G, w.Scratch} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func newResolver(t *testing.T, cfg policy.Config, deps policy.Deps) *policy.Resolver {
	t.Helper()
	r, err := policy.New(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func baseDeps(t *testing.T, root *cordis.Context, w world) policy.Deps {
	t.Helper()
	wd := workdirtest.New()
	wd.Set(sid, w.W, w.P)
	return policy.Deps{Owner: owner(t, root, "world.sandbox.policy"), Workdir: wd,
		Scratch: func(session.ID) (string, error) { return w.Scratch, nil }}
}

// TC-01a resolver precedence: under workspace-write the write roots are exactly
// the workspace root P, the grant G and scratch (W, below P, is not added); a
// read-only or full-access call has none; an explicit mode outranks the
// default.
func TestResolvePrecedence(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, baseDeps(t, root, w))
	release, err := r.GrantRoot(ctx, sid, w.G)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	if err != nil {
		t.Fatal(err)
	}
	want := []sandbox.WriteRoot{{Path: w.P, Source: sandbox.SourceWorkspace}, {Path: w.G, Source: sandbox.SourceGrant}, {Path: w.Scratch, Source: sandbox.SourceScratch}}
	// The version of this host's shipped text: none on main (M2-C-6).
	wantText := ""
	if text, err := policy.Text(runtime.GOOS); err == nil {
		wantText = policy.TextVersion(text)
	}
	if p.Mode() != sandbox.WorkspaceWrite || !slices.Equal(p.WriteRoots(), want) || !p.Network() || p.Require() != sandbox.RequireAny || p.TextVersion() != wantText {
		t.Fatalf("workspace-write policy = %s %v net=%v require=%q text=%q, want text %q", p.Mode(), p.WriteRoots(), p.Network(), p.Require(), p.TextVersion(), wantText)
	}
	for _, m := range []sandbox.Mode{sandbox.ReadOnly, sandbox.DangerFullAccess} {
		p, err := r.Resolve(ctx, sandbox.Request{Session: sid, Mode: m})
		if err != nil || p.Mode() != m || len(p.WriteRoots()) != 0 {
			t.Fatalf("explicit %s = %s %v, %v", m, p.Mode(), p.WriteRoots(), err)
		}
	}
	release()
	release()
	p, err = r.Resolve(ctx, sandbox.Request{Session: sid})
	if err != nil || len(p.WriteRoots()) != 2 {
		t.Fatalf("after release: %v, %v", p.WriteRoots(), err)
	}
	if _, err := r.Resolve(ctx, sandbox.Request{Session: sid, Mode: "full"}); herr.Kind(err) != sandbox.CodeInvalidMode {
		t.Fatalf("a bogus explicit mode: %v", err)
	}

	ro := newResolver(t, policy.Config{}, baseDeps(t, root, w))
	if p, err := ro.Resolve(ctx, sandbox.Request{Session: sid}); err != nil || p.Mode() != sandbox.ReadOnly || len(p.WriteRoots()) != 0 {
		t.Fatalf("the default default is read-only with no roots: %s %v, %v", p.Mode(), p.WriteRoots(), err)
	}
}

// Configured roots come after the workspace and before grants; a path already
// a root is listed once, under its first source; a configured root or a grant
// that is missing, a file or a symbolic link is refused on the call.
func TestRootsAreCheckedPerCall(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	extra := filepath.Join(filepath.Dir(w.P), "extra")
	mem := fstest.NewMemFS()
	for _, d := range []string{w.P, w.G, extra} {
		if err := mem.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(filepath.Dir(w.P), "link")
	if err := mem.Symlink(w.G, link); err != nil {
		t.Fatal(err)
	}
	deps := baseDeps(t, root, w)
	deps.FS = mem
	deps.Scratch = nil
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite, ExtraWriteRoots: []string{extra, w.P}}, deps)
	if _, err := r.GrantRoot(ctx, sid, w.G); err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve(ctx, sandbox.Request{Session: sid})
	want := []sandbox.WriteRoot{{Path: w.P, Source: sandbox.SourceWorkspace}, {Path: extra, Source: sandbox.SourceConfig}, {Path: w.G, Source: sandbox.SourceGrant}}
	if err != nil || !slices.Equal(p.WriteRoots(), want) {
		t.Fatalf("roots = %v, %v", p.WriteRoots(), err)
	}
	for _, bad := range []string{filepath.Join(w.G, "missing"), link, "relative", w.G + string(filepath.Separator) + "."} {
		if _, err := r.GrantRoot(ctx, sid, bad); herr.Kind(err) != sandbox.CodeInvalidRoot {
			t.Errorf("GrantRoot(%q): %v", bad, err)
		}
	}
	gone := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite, ExtraWriteRoots: []string{filepath.Join(extra, "nope")}}, deps)
	if _, err := gone.Resolve(ctx, sandbox.Request{Session: sid}); herr.Kind(err) != sandbox.CodeInvalidRoot {
		t.Fatalf("a missing configured root: %v", err)
	}
	if _, err := gone.Resolve(ctx, sandbox.Request{Session: sid, Mode: sandbox.ReadOnly}); err != nil {
		t.Fatalf("a read-only call checks no roots: %v", err)
	}
}

// TC-01a SetMode is logged and announced: one ModeChanging dispatch is observed
// while the old mode is still in force and nothing is logged yet, then one
// sandbox/mode is appended and the next Resolve uses the new mode. A refusing
// listener changes nothing; a change to the mode in force writes nothing; a
// resolver that never saw the change folds it from the log.
func TestSetModeIsLoggedAndAnnounced(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	sessions, h := liveSession(t, root)
	deps := baseDeps(t, root, w)
	deps.Sessions = sessions
	r := newResolver(t, policy.Config{}, deps)
	modeEvents := func() []policy.ModeSet {
		evs, err := h.Read(ctx, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []policy.ModeSet
		for _, ev := range evs {
			if ev.Type() == sandbox.ModeEventType {
				m, err := policy.ModeType.As(ev)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, m)
			}
		}
		return out
	}
	var seen []sandbox.ModeChange
	var logged []int // sandbox/mode events in the log at each dispatch
	var refuse atomic.Bool
	listener := owner(t, root, "test.listener")
	if _, err := sandbox.ModeChanging.On(listener, func(_ *cordis.Context, c sandbox.ModeChange) error {
		seen = append(seen, c)
		if p, err := r.Resolve(ctx, sandbox.Request{Session: sid}); err != nil || p.Mode() != c.From {
			t.Errorf("during ModeChanging the mode is %s, %v; want the old %s", p.Mode(), err, c.From)
		}
		logged = append(logged, len(modeEvents()))
		if refuse.Load() {
			return errors.New("a command is running")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	actx := policy.WithActor(ctx, policy.Actor{Principal: "alice", Channel: "cli"})
	if err := r.SetMode(actx, sid, sandbox.DangerFullAccess); err != nil {
		t.Fatal(err)
	}
	if want := []sandbox.ModeChange{{Session: sid, From: sandbox.ReadOnly, To: sandbox.DangerFullAccess}}; !slices.Equal(seen, want) {
		t.Fatalf("ModeChanging dispatches = %v, want %v", seen, want)
	}
	if !slices.Equal(logged, []int{0}) {
		t.Fatalf("sandbox/mode events in the log during ModeChanging = %v, want none", logged)
	}
	got := modeEvents()
	if len(got) != 1 || got[0] != (policy.ModeSet{Mode: sandbox.DangerFullAccess, From: sandbox.ReadOnly, Network: policy.NetworkAllowed, By: policy.Actor{Principal: "alice", Channel: "cli"}}) {
		t.Fatalf("sandbox/mode events = %+v", got)
	}
	if p, err := r.Resolve(ctx, sandbox.Request{Session: sid}); err != nil || p.Mode() != sandbox.DangerFullAccess {
		t.Fatalf("after SetMode: %s, %v", p.Mode(), err)
	}

	if err := r.SetMode(ctx, sid, sandbox.DangerFullAccess); err != nil || len(seen) != 1 || len(modeEvents()) != 1 {
		t.Fatalf("a no-op change dispatched %d, logged %d, %v", len(seen), len(modeEvents()), err)
	}
	refuse.Store(true)
	if err := r.SetMode(ctx, sid, sandbox.WorkspaceWrite); err == nil || len(modeEvents()) != 1 {
		t.Fatalf("a refused change: %v, %d events", err, len(modeEvents()))
	}
	if m, _ := r.Mode(ctx, sid); m != sandbox.DangerFullAccess {
		t.Fatalf("a refused change took effect: %s", m)
	}
	refuse.Store(false)
	if err := r.SetMode(ctx, sid, "bogus"); herr.Kind(err) != sandbox.CodeInvalidMode {
		t.Fatalf("a bogus mode: %v", err)
	}
	if err := r.SetMode(ctx, "s-other", sandbox.WorkspaceWrite); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("a session that is not live: %v", err)
	}

	fresh := newResolver(t, policy.Config{}, deps)
	if m, err := fresh.Mode(ctx, sid); err != nil || m != sandbox.DangerFullAccess {
		t.Fatalf("a fresh resolver folds the log: %s, %v", m, err)
	}
}

// The sandbox-mode fold registers and reads the last sandbox/mode.
func TestModeView(t *testing.T) {
	reg := views.New()
	if _, err := policy.RegisterView(reg); err != nil {
		t.Fatal(err)
	}
	if policy.ModeType.Name() != sandbox.ModeEventType {
		t.Fatalf("ModeType is %q, want the contract's %q", policy.ModeType.Name(), sandbox.ModeEventType)
	}
	if d := policy.ModeView(); d.Key != policy.ModeKey || !slices.Equal(d.Types, []string{sandbox.ModeEventType}) {
		t.Fatalf("view = %s %v", d.Key, d.Types)
	}
}

// Escalate: only a justified, strictly wider (or equal with new paths) request
// that the approver allows yields a wider one-call policy; the session's mode
// does not change.
func TestEscalate(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	deps := baseDeps(t, root, w)
	var asked []policy.Escalation
	var deny atomic.Bool
	deps.Approver = policy.ApproverFunc(func(_ context.Context, e policy.Escalation) error {
		asked = append(asked, e)
		if deny.Load() {
			return errors.New("rejected by alice")
		}
		return nil
	})
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	outside := filepath.Join(filepath.Dir(w.P), "out")
	for name, c := range map[string]struct {
		req  sandbox.EscalationRequest
		want herr.Reason
	}{
		"no justification": {sandbox.EscalationRequest{Session: sid, Permissions: "danger-full-access"}, sandbox.CodeEscalationRejected},
		"no mode":          {sandbox.EscalationRequest{Session: sid, Justification: "j"}, sandbox.CodeEscalationRejected},
		"bad permissions":  {sandbox.EscalationRequest{Session: sid, Permissions: "root", Justification: "j"}, sandbox.CodeEscalationRejected},
		"disagreeing":      {sandbox.EscalationRequest{Session: sid, Permissions: "read-only", Mode: sandbox.DangerFullAccess, Justification: "j"}, sandbox.CodeEscalationRejected},
		"narrower":         {sandbox.EscalationRequest{Session: sid, Mode: sandbox.ReadOnly, Justification: "j"}, sandbox.CodeEscalationRejected},
		"same, no paths":   {sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Justification: "j"}, sandbox.CodeEscalationRejected},
		"same, inside":     {sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{w.W}, Justification: "j"}, sandbox.CodeEscalationRejected},
		"paths, full":      {sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess, Paths: []string{outside}, Justification: "j"}, sandbox.CodeEscalationRejected},
		"relative path":    {sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{"rel"}, Justification: "j"}, sandbox.CodeEscalationRejected},
	} {
		if _, err := r.Escalate(ctx, c.req); herr.Kind(err) != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
	if len(asked) != 0 {
		t.Fatalf("refused requests reached the approver: %v", asked)
	}
	p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{outside}, Justification: "build output", Tool: "bash", Call: "c1"})
	if err != nil || p.Mode() != sandbox.WorkspaceWrite || p.WriteRoots()[len(p.WriteRoots())-1] != (sandbox.WriteRoot{Path: outside, Source: sandbox.SourceEscalation}) {
		t.Fatalf("same mode with a new path: %v, %v", p.WriteRoots(), err)
	}
	p, err = r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Permissions: "danger-full-access", Justification: "install"})
	if err != nil || p.Mode() != sandbox.DangerFullAccess {
		t.Fatalf("wider: %s, %v", p.Mode(), err)
	}
	if len(asked) != 2 || asked[1].From != sandbox.WorkspaceWrite || asked[1].To != sandbox.DangerFullAccess {
		t.Fatalf("approver saw %+v", asked)
	}
	if m, _ := r.Mode(ctx, sid); m != sandbox.WorkspaceWrite {
		t.Fatalf("an escalation changed the session's mode to %s", m)
	}
	deny.Store(true)
	if _, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess, Justification: "j"}); herr.Kind(err) != sandbox.CodeEscalationRejected {
		t.Fatalf("a denied escalation: %v", err)
	}
	deps.Approver = nil
	none := newResolver(t, policy.Config{}, deps)
	if _, err := none.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.DangerFullAccess, Justification: "j"}); herr.Kind(err) != sandbox.CodeEscalationRejected {
		t.Fatalf("no approver: %v", err)
	}
}

// statFile is a MemFS on which one path stats as a regular file.
type statFile struct {
	*fstest.MemFS
	file string
}

func (s statFile) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	if r.Path == s.file {
		return fs.Info{Exists: true, Type: fs.TypeFile}, nil
	}
	return s.MemFS.Stat(ctx, r)
}

// An escalation root is validated like every other root (sandbox-ssh.md sec.
// 3.2): a missing path, a regular file or a symbolic link is
// sandbox/invalid-root before the approver is asked; a real directory is
// approved and issued.
func TestEscalationRootsAreChecked(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	base := filepath.Dir(w.P)
	out, link, file := filepath.Join(base, "out"), filepath.Join(base, "link"), filepath.Join(base, "file")
	mem := fstest.NewMemFS()
	for _, d := range []string{w.P, out} {
		if err := mem.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := mem.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	deps := baseDeps(t, root, w)
	deps.FS = statFile{MemFS: mem, file: file}
	deps.Scratch = nil
	asked := 0
	deps.Approver = policy.ApproverFunc(func(context.Context, policy.Escalation) error { asked++; return nil })
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	for _, bad := range []string{filepath.Join(base, "missing"), file, link} {
		_, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{out, bad}, Justification: "j"})
		if herr.Kind(err) != sandbox.CodeInvalidRoot {
			t.Errorf("escalation root %q: %v, want %s", bad, err, sandbox.CodeInvalidRoot)
		}
	}
	if asked != 0 {
		t.Fatalf("invalid roots reached the approver %d times", asked)
	}
	p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: sid, Mode: sandbox.WorkspaceWrite, Paths: []string{out}, Justification: "j"})
	want := []sandbox.WriteRoot{{Path: w.P, Source: sandbox.SourceWorkspace}, {Path: out, Source: sandbox.SourceEscalation}}
	if err != nil || asked != 1 || !slices.Equal(p.WriteRoots(), want) {
		t.Fatalf("a real directory: %v, %v (asked %d)", p.WriteRoots(), err, asked)
	}
}

// ForHelper is read-only plus the helper's own roots whatever the session's
// mode, and unconfined only for a registered kind.
func TestForHelper(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	r := newResolver(t, policy.Config{DefaultMode: sandbox.DangerFullAccess, UnconfinedHelpers: map[string]string{"converter": "needs the GPU driver"}}, baseDeps(t, root, w))
	p, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp"})
	if err != nil || p.Mode() != sandbox.ReadOnly || len(p.WriteRoots()) != 0 {
		t.Fatalf("plain helper: %s %v, %v", p.Mode(), p.WriteRoots(), err)
	}
	p, err = r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", WriteRoots: []string{w.Scratch}, Network: true})
	if err != nil || p.Mode() != sandbox.WorkspaceWrite || !slices.Equal(p.WriteRoots(), []sandbox.WriteRoot{{Path: w.Scratch, Source: sandbox.SourceHelper}}) || !p.Network() {
		t.Fatalf("helper with a root: %s %v, %v", p.Mode(), p.WriteRoots(), err)
	}
	if _, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "mcp", Unconfined: true}); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("unregistered unconfined helper: %v", err)
	}
	if p, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid, Kind: "converter", Unconfined: true}); err != nil || p.Mode() != sandbox.DangerFullAccess {
		t.Fatalf("registered unconfined helper: %s, %v", p.Mode(), err)
	}
	if _, err := r.ForHelper(ctx, sandbox.HelperRequest{Session: sid}); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("a helper with no kind: %v", err)
	}
}

func TestConfigValidates(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	o := owner(t, root, "x")
	for name, c := range map[string]policy.Config{
		"mode":     {DefaultMode: "full"},
		"root":     {ExtraWriteRoots: []string{"rel"}},
		"network":  {Network: "maybe"},
		"require":  {Require: "some"},
		"helper":   {UnconfinedHelpers: map[string]string{"k": " "}},
		"platform": {Platform: "plan9"},
	} {
		if _, err := policy.New(c, policy.Deps{Owner: o}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := policy.New(policy.Config{}, policy.Deps{}); err == nil {
		t.Error("no owner: accepted")
	}
}
