package ops_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// faulty is a provider whose calls fail on demand: statErr is consulted for
// each Stat with its Ref, the other errors fail every call of their method.
type faulty struct {
	fs.FS
	statErr                                  func(fs.Ref) error
	openErr, dirErr, writeErr, delErr, mvErr error
}

func (f *faulty) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	if f.statErr != nil {
		if err := f.statErr(r); err != nil {
			return fs.Info{}, err
		}
	}
	return f.FS.Stat(ctx, r)
}

func (f *faulty) Open(ctx context.Context, r fs.Ref) (io.ReadCloser, fs.Info, error) {
	if f.openErr != nil {
		return nil, fs.Info{}, f.openErr
	}
	return f.FS.Open(ctx, r)
}

func (f *faulty) ReadDir(ctx context.Context, r fs.Ref, limit int) ([]fs.DirEntry, bool, error) {
	if f.dirErr != nil {
		return nil, false, f.dirErr
	}
	return f.FS.ReadDir(ctx, r, limit)
}

func (f *faulty) Write(ctx context.Context, r fs.Ref, data []byte, g fs.Guard, p sandbox.Policy) (fs.WriteOutcome, error) {
	if f.writeErr != nil {
		return fs.WriteOutcome{}, f.writeErr
	}
	return f.FS.Write(ctx, r, data, g, p)
}

func (f *faulty) Delete(ctx context.Context, r fs.Ref, g fs.Guard, p sandbox.Policy) (fs.DeleteOutcome, error) {
	if f.delErr != nil {
		return fs.DeleteOutcome{}, f.delErr
	}
	return f.FS.Delete(ctx, r, g, p)
}

func (f *faulty) Move(ctx context.Context, src, dst fs.Ref, o fs.MoveOptions, p sandbox.Policy) (fs.MoveOutcome, error) {
	if f.mvErr != nil {
		return fs.MoveOutcome{}, f.mvErr
	}
	return f.FS.Move(ctx, src, dst, o, p)
}

// brokenDirs is a working-directory lookup that fails: at Access, or at Path.
type brokenDirs struct{ atPath bool }

func (b brokenDirs) Access(session.ID) (workdir.Access, error) {
	if !b.atPath {
		return nil, herr.New(workdir.CodeUnavailable, "the session has no working directory")
	}
	return b, nil
}
func (brokenDirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{}, herr.New(workdir.CodeUnavailable, "gone")
}
func (brokenDirs) Path() (string, error) { return "", herr.New(workdir.CodeUnavailable, "gone") }
func (brokenDirs) Root() (string, error) { return "", herr.New(workdir.CodeUnavailable, "gone") }

// zeroResolver resolves every call to the zero Policy, which no provider may
// act on.
type zeroResolver struct{ sandbox.Resolver }

func (zeroResolver) Resolve(context.Context, sandbox.Request) (sandbox.Policy, error) {
	return sandbox.Policy{}, nil
}

// seed writes name under r's root through the service.
func (r *rig) seed(t *testing.T, name, content string) {
	t.Helper()
	if _, err := r.ops.Write(context.Background(), call, fs.Ref{Path: name}, []byte(content), fs.NoGuard); err != nil {
		t.Fatal(err)
	}
}

// content reads name under r's root from the provider directly.
func (r *rig) content(t *testing.T, name string) (string, bool) {
	t.Helper()
	rc, _, err := r.fs.Open(context.Background(), fs.Ref{Path: filepath.Join(r.root, name)})
	if herr.Kind(err) == fs.CodeNotFound {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func (r *rig) mark() int { return len(r.rec.got()) }

func (r *rig) since(n int) []string { return r.rec.got()[n:] }

// The component injects the provider, the resolver and the working directory
// and provides the service under ops.Key; that service fences with the injected
// policy and anchors a relative path at the injected working directory.
func TestComponentProvidesTheServiceFromItsInjections(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	m := fstest.NewMemFS()
	ws := filepath.Join(t.TempDir(), "ws")
	wd := dirs{ws}
	if _, err := fs.Key.Provide(root, fs.FS(m)); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.ResolverKey.Provide(root, sandbox.Resolver(&sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd})); err != nil {
		t.Fatal(err)
	}
	if _, err := workdir.Key.Provide(root, workdir.Lookup(wd)); err != nil {
		t.Fatal(err)
	}
	f, err := root.Use(ops.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	var svc *ops.Service
	g, err := root.Inject([]string{ops.Key.Name()}, func(c *cordis.Context) error {
		var err error
		svc, err = ops.Key.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := svc.Write(ctx, call, fs.Ref{Path: "a.txt"}, []byte("a"), fs.NoGuard)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(ws, "a.txt"); out.Target.Display != want {
		t.Fatalf("wrote %s, want %s", out.Target.Display, want)
	}
	outside := filepath.Join(filepath.Dir(ws), "outside.txt")
	if _, err := svc.Write(ctx, call, fs.Ref{Path: outside}, []byte("x"), fs.NoGuard); herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("write outside the injected root: %v, want %q", err, fs.CodeSandboxDenied)
	}
}

// A dependency bound to a value of the wrong type fails the component's load,
// and no service is provided.
func TestComponentFailsOnAMistypedDependency(t *testing.T) {
	for _, bad := range []string{fs.Key.Name(), sandbox.ResolverKey.Name(), workdir.Key.Name()} {
		t.Run(bad, func(t *testing.T) {
			root := cordis.New()
			t.Cleanup(root.Dispose)
			wd := dirs{t.TempDir()}
			values := map[string]any{
				fs.Key.Name():              fs.FS(fstest.NewMemFS()),
				sandbox.ResolverKey.Name(): sandbox.Resolver(&sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}),
				workdir.Key.Name():         workdir.Lookup(wd),
			}
			values[bad] = "not a service"
			for name, v := range values {
				if _, err := root.Provide(name, v); err != nil {
					t.Fatal(err)
				}
			}
			f, err := root.Use(ops.Component, cordis.None{})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Wait(); err == nil {
				t.Fatalf("the component loaded with %s mistyped", bad)
			}
			if _, ok := ops.Key.Lookup(root); ok {
				t.Fatal("a failed load provided the service")
			}
		})
	}
}

// Both catalogue items are well formed and name the components they carry.
func TestCatalogueItems(t *testing.T) {
	c, err := catalog.Build([]catalog.Item{ops.Item(), ops.PolicyItem()})
	if err != nil {
		t.Fatal(err)
	}
	it, err := c.Lookup(ops.Name)
	if err != nil {
		t.Fatal(err)
	}
	if it.Component != ops.Component || it.Config != nil || !slices.Equal(it.Provides, []string{ops.Key.Name()}) {
		t.Fatalf("fs.ops item: %+v", it)
	}
	it, err = c.Lookup(policy.Name)
	if err != nil {
		t.Fatal(err)
	}
	if it.Component != policy.Component || it.Config != reflect.TypeFor[policy.Config]() || !slices.Equal(it.Provides, []string{sandbox.ResolverKey.Name()}) {
		t.Fatalf("sandbox.policy item: %+v", it)
	}
}

// Every call needs a session: each method refuses one without, with
// sandbox/unavailable, before the provider or any listener sees it.
func TestEveryMethodRefusesACallWithoutASession(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	r.seed(t, "f", "x")
	n := r.mark()
	none := ops.Call{CallID: "c1"}
	ref := fs.Ref{Path: "f"}
	errs := map[string]error{}
	_, errs["stat"] = r.ops.Stat(ctx, none, ref)
	_, _, errs["open"] = r.ops.Open(ctx, none, ref)
	_, _, errs["readdir"] = r.ops.ReadDir(ctx, none, fs.Ref{Path: "."}, 0)
	_, errs["write"] = r.ops.Write(ctx, none, ref, []byte("y"), fs.NoGuard)
	_, errs["delete"] = r.ops.Delete(ctx, none, ref, fs.NoGuard)
	_, errs["move"] = r.ops.Move(ctx, none, ref, fs.Ref{Path: "g"}, fs.MoveOptions{})
	for name, err := range errs {
		if herr.Kind(err) != sandbox.CodeUnavailable {
			t.Errorf("%s without a session: %v (code %q), want %q", name, err, herr.Kind(err), sandbox.CodeUnavailable)
		}
	}
	if got := r.since(n); len(got) != 0 {
		t.Fatalf("refused calls emitted %q", got)
	}
	if got, _ := r.content(t, "f"); got != "x" {
		t.Fatalf("f is %q after refused calls", got)
	}
}

// Stat and ReadDir resolve a relative path at the session's working directory
// and emit nothing; neither takes a policy, so they work under read-only.
func TestStatAndReadDirAnchorAtTheWorkingDirectory(t *testing.T) {
	ctx := context.Background()
	m := fstest.NewMemFS()
	r := newRig(t, m, sandbox.WorkspaceWrite)
	r.seed(t, "b.txt", "bb")
	r.seed(t, "a.txt", "a")
	if err := m.Mkdir(filepath.Join(r.root, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := r.res.SetMode(ctx, sess, sandbox.ReadOnly); err != nil {
		t.Fatal(err)
	}
	n := r.mark()

	in, err := r.ops.Stat(ctx, call, fs.Ref{Path: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !in.Exists || in.Type != fs.TypeFile || in.Target.Display != filepath.Join(r.root, "a.txt") {
		t.Fatalf("stat a.txt: %+v", in)
	}
	missing, err := r.ops.Stat(ctx, call, fs.Ref{Path: "nope"})
	if err != nil || missing.Exists {
		t.Fatalf("stat of a missing file: %+v, %v; want absent with no error", missing, err)
	}

	ents, cut, err := r.ops.ReadDir(ctx, call, fs.Ref{Path: "."}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name+":"+string(e.Type))
	}
	if want := []string{"a.txt:file", "b.txt:file", "sub:dir"}; cut || !slices.Equal(names, want) {
		t.Fatalf("readdir: %q (cut %v), want %q", names, cut, want)
	}
	ents, cut, err = r.ops.ReadDir(ctx, call, fs.Ref{Path: "."}, 2)
	if err != nil || !cut || len(ents) != 2 {
		t.Fatalf("readdir limited to 2: %d entries, cut %v, %v", len(ents), cut, err)
	}
	if _, _, err := r.ops.ReadDir(ctx, call, fs.Ref{Path: "a.txt"}, 0); herr.Kind(err) != fs.CodeNotDirectory {
		t.Fatalf("readdir of a file: %v, want %q", err, fs.CodeNotDirectory)
	}
	if got := r.since(n); len(got) != 0 {
		t.Fatalf("stat and readdir emitted %q", got)
	}
}

// A read observes what it read: a missing file is observed absent, a failure
// that is not "missing" observes nothing.
func TestOpenObservesAbsenceButNotOtherFailures(t *testing.T) {
	ctx := context.Background()
	m := fstest.NewMemFS()
	r := newRig(t, m, sandbox.WorkspaceWrite)
	if err := m.Mkdir(filepath.Join(r.root, "d")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ops.Open(ctx, call, fs.Ref{Path: "gone.txt"}); herr.Kind(err) != fs.CodeNotFound {
		t.Fatalf("open of a missing file: %v, want %q", err, fs.CodeNotFound)
	}
	if want := []string{"observed read gone.txt absent"}; !slices.Equal(r.rec.got(), want) {
		t.Fatalf("events %q, want %q", r.rec.got(), want)
	}
	n := r.mark()
	if _, _, err := r.ops.Open(ctx, call, fs.Ref{Path: "d"}); herr.Kind(err) != fs.CodeNotRegularFile {
		t.Fatalf("open of a directory: %v, want %q", err, fs.CodeNotRegularFile)
	}
	if got := r.since(n); len(got) != 0 {
		t.Fatalf("a failed open of a directory emitted %q", got)
	}
}

// A not-found from Open whose follow-up Stat fails is not observed: absence is
// only recorded when the provider confirms it.
func TestOpenNotFoundIsObservedOnlyWhenConfirmed(t *testing.T) {
	ctx := context.Background()
	f := &faulty{FS: fstest.NewMemFS()}
	r := newRig(t, f, sandbox.WorkspaceWrite)
	r.seed(t, "here.txt", "x")
	n := r.mark()
	// Open says not-found for a file that is present: Stat sees it, so no
	// absence is observed.
	f.openErr = herr.New(fs.CodeNotFound, "raced away")
	if _, _, err := r.ops.Open(ctx, call, fs.Ref{Path: "here.txt"}); herr.Kind(err) != fs.CodeNotFound {
		t.Fatalf("open: %v", err)
	}
	// Open says not-found and Stat fails: nothing is observed either.
	f.statErr = func(fs.Ref) error { return herr.New(fs.CodeIO, "the disk is gone") }
	if _, _, err := r.ops.Open(ctx, call, fs.Ref{Path: "here.txt"}); herr.Kind(err) != fs.CodeNotFound {
		t.Fatalf("open: %v", err)
	}
	if got := r.since(n); len(got) != 0 {
		t.Fatalf("an unconfirmed absence was observed: %q", got)
	}
}

// A relative path needs the session's working directory: when the lookup
// fails, at Access or at Path, the call fails with its error before the policy,
// the provider or any listener is reached. An absolute path does not need it.
func TestWorkingDirectoryFailureStopsARelativeCall(t *testing.T) {
	ctx := context.Background()
	for name, wd := range map[string]brokenDirs{"access": {}, "path": {atPath: true}} {
		t.Run(name, func(t *testing.T) {
			m := fstest.NewMemFS()
			r := newRig(t, m, sandbox.WorkspaceWrite)
			r.ops = ops.New(r.ctx, m, r.res, ops.Options{Workdir: wd})
			abs := fs.Ref{Path: filepath.Join(r.root, "f")}
			if _, err := m.Write(ctx, abs, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, r.root)); err != nil {
				t.Fatal(err)
			}
			rel := fs.Ref{Path: "f"}
			errs := map[string]error{}
			_, errs["stat"] = r.ops.Stat(ctx, call, rel)
			_, _, errs["open"] = r.ops.Open(ctx, call, rel)
			_, _, errs["readdir"] = r.ops.ReadDir(ctx, call, fs.Ref{Path: "."}, 0)
			_, errs["write"] = r.ops.Write(ctx, call, rel, []byte("y"), fs.NoGuard)
			_, errs["delete"] = r.ops.Delete(ctx, call, rel, fs.NoGuard)
			_, errs["move"] = r.ops.Move(ctx, call, rel, abs, fs.MoveOptions{})
			// The source is absolute; the relative destination still needs it.
			_, errs["move-destination"] = r.ops.Move(ctx, call, abs, rel, fs.MoveOptions{})
			for op, err := range errs {
				if herr.Kind(err) != workdir.CodeUnavailable {
					t.Errorf("%s: %v (code %q), want %q", op, err, herr.Kind(err), workdir.CodeUnavailable)
				}
			}
			if got := r.rec.got(); len(got) != 0 {
				t.Fatalf("failed calls emitted %q", got)
			}
			if in, err := r.ops.Stat(ctx, call, abs); err != nil || !in.Exists {
				t.Fatalf("stat of an absolute path: %+v, %v", in, err)
			}
			if got, ok := r.content(t, "f"); !ok || got != "x" {
				t.Fatalf("f is %q (present %v)", got, ok)
			}
		})
	}
}

// Without a working-directory lookup a relative path keeps no Dir, and the
// provider refuses it.
func TestNoWorkingDirectoryLeavesARelativePathUnanchored(t *testing.T) {
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	o := ops.New(r.ctx, r.fs, r.res, ops.Options{})
	if _, err := o.Stat(context.Background(), call, fs.Ref{Path: "f"}); herr.Kind(err) != fs.CodeInvalidPath {
		t.Fatalf("a relative path with no working directory: %v, want %q", err, fs.CodeInvalidPath)
	}
}

// A mutation needs a usable policy from the resolver: no resolver, a resolver
// error and a zero policy each refuse every mutation before any event, and the
// file stays as it was. Reads take no policy and still work.
func TestMutationWithoutAUsablePolicyIsRefused(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		res  func(*rig) sandbox.Resolver
		call ops.Call
		code herr.Reason
	}{
		{"no resolver", func(*rig) sandbox.Resolver { return nil }, call, sandbox.CodeUnavailable},
		{"invalid explicit mode", func(r *rig) sandbox.Resolver { return r.res }, ops.Call{Session: sess, Mode: "everything"}, sandbox.CodeInvalidMode},
		{"zero policy", func(*rig) sandbox.Resolver { return zeroResolver{} }, call, sandbox.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
			r.seed(t, "f", "x")
			n := r.mark()
			o := ops.New(r.ctx, r.fs, tc.res(r), ops.Options{Workdir: dirs{r.root}})
			errs := map[string]error{}
			_, errs["write"] = o.Write(ctx, tc.call, fs.Ref{Path: "f"}, []byte("y"), fs.NoGuard)
			_, errs["delete"] = o.Delete(ctx, tc.call, fs.Ref{Path: "f"}, fs.NoGuard)
			_, errs["move"] = o.Move(ctx, tc.call, fs.Ref{Path: "f"}, fs.Ref{Path: "g"}, fs.MoveOptions{})
			for op, err := range errs {
				if herr.Kind(err) != tc.code {
					t.Errorf("%s: %v (code %q), want %q", op, err, herr.Kind(err), tc.code)
				}
			}
			if got := r.since(n); len(got) != 0 {
				t.Fatalf("refused mutations emitted %q", got)
			}
			if got, _ := r.content(t, "f"); got != "x" {
				t.Fatalf("f is %q", got)
			}
			if _, err := o.Stat(ctx, tc.call, fs.Ref{Path: "f"}); err != nil {
				t.Fatalf("stat needs no policy: %v", err)
			}
		})
	}
}

// The pre-fence resolves the target through the provider: a failure stops the
// mutation, with the provider's error, before BeforeMutation.
func TestPreFenceTargetResolutionFailureStopsTheMutation(t *testing.T) {
	ctx := context.Background()
	f := &faulty{FS: fstest.NewMemFS()}
	r := newRig(t, f, sandbox.WorkspaceWrite)
	r.seed(t, "f", "x")
	n := r.mark()
	f.statErr = func(ref fs.Ref) error {
		if filepath.Base(ref.Path) == "f" {
			return herr.New(fs.CodePermission, "stat refused")
		}
		return nil
	}
	errs := map[string]error{}
	_, errs["write"] = r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("y"), fs.NoGuard)
	_, errs["delete"] = r.ops.Delete(ctx, call, fs.Ref{Path: "f"}, fs.NoGuard)
	_, errs["move"] = r.ops.Move(ctx, call, fs.Ref{Path: "f"}, fs.Ref{Path: "g"}, fs.MoveOptions{})
	_, errs["move-destination"] = r.ops.Move(ctx, call, fs.Ref{Path: "g0"}, fs.Ref{Path: "sub/f"}, fs.MoveOptions{})
	for op, err := range errs {
		if herr.Kind(err) != fs.CodePermission {
			t.Errorf("%s: %v (code %q), want %q", op, err, herr.Kind(err), fs.CodePermission)
		}
	}
	if got := r.since(n); len(got) != 0 {
		t.Fatalf("failed mutations emitted %q", got)
	}
	f.statErr = nil
	if got, _ := r.content(t, "f"); got != "x" {
		t.Fatalf("f is %q", got)
	}
}

// A write root reached through a link is resolved by the same provider as the
// target, so a file under the link's target passes the pre-fence and reaches
// BeforeMutation; when the root's resolution fails, the target is judged
// against the root as issued only, and is refused before any event. (MemFS
// fences lexically itself, so the listener stops each admitted call: this
// test is about the pre-fence ops runs, not the provider's own.)
func TestPreFenceResolvesEachRootThroughTheProvider(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	real, link := filepath.Join(base, "real"), filepath.Join(base, "link")
	m := fstest.NewMemFS()
	if err := m.Mkdir(real); err != nil {
		t.Fatal(err)
	}
	if err := m.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f := &faulty{FS: m}
	r := newRig(t, f, sandbox.WorkspaceWrite)
	r.res.Workdir = dirs{link}
	r.ops = ops.New(r.ctx, f, r.res, ops.Options{Workdir: dirs{link}})
	const admitted herr.Reason = "test/admitted"
	var announced []string
	if _, err := fs.BeforeMutation.On(r.ctx, func(_ *cordis.Context, mu fs.Mutation) error {
		announced = append(announced, mu.Target.Display)
		return herr.New(admitted, "stop after the pre-fence")
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("x"), fs.NoGuard); herr.Kind(err) != admitted {
		t.Fatalf("write under a root reached through a link: %v (code %q), want the pre-fence to admit it", err, herr.Kind(err))
	}
	if want := []string{filepath.Join(real, "f")}; !slices.Equal(announced, want) {
		t.Fatalf("announced %q, want %q", announced, want)
	}

	f.statErr = func(ref fs.Ref) error {
		if ref.Path == link {
			return herr.New(fs.CodeIO, "the root cannot be resolved")
		}
		return nil
	}
	announced = nil
	if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("y"), fs.NoGuard); herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("write when the root cannot be resolved: %v (code %q), want %q", err, herr.Kind(err), fs.CodeSandboxDenied)
	}
	if len(announced) != 0 {
		t.Fatalf("a refused write was announced: %q", announced)
	}
}

// A BeforeMutation listener's coded error is returned as it is, not rewrapped
// as fs/aborted, and the mutation does not run.
func TestBeforeMutationCodedErrorIsKept(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	r.seed(t, "f", "x")
	const code herr.Reason = "capture/full"
	if _, err := fs.BeforeMutation.On(r.ctx, func(*cordis.Context, fs.Mutation) error {
		return herr.New(code, "the capture store is full")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("y"), fs.NoGuard); herr.Kind(err) != code {
		t.Fatalf("write: %v (code %q), want %q", err, herr.Kind(err), code)
	}
	if got, _ := r.content(t, "f"); got != "x" {
		t.Fatalf("f is %q", got)
	}
}

// A provider failure after BeforeMutation is returned unchanged and nothing is
// observed: the event was announced, the change did not happen.
func TestProviderFailureIsReturnedAndNotObserved(t *testing.T) {
	ctx := context.Background()
	f := &faulty{FS: fstest.NewMemFS()}
	r := newRig(t, f, sandbox.WorkspaceWrite)
	r.seed(t, "f", "x")
	n := r.mark()
	f.writeErr = herr.New(fs.CodeIO, "write failed")
	f.delErr = herr.New(fs.CodeStaleVersion, "delete raced")
	f.mvErr = herr.New(fs.CodeAlreadyExists, "move target exists")
	if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("y"), fs.NoGuard); herr.Kind(err) != fs.CodeIO {
		t.Fatalf("write: %v", err)
	}
	if _, err := r.ops.Delete(ctx, call, fs.Ref{Path: "f"}, fs.NoGuard); herr.Kind(err) != fs.CodeStaleVersion {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.ops.Move(ctx, call, fs.Ref{Path: "f"}, fs.Ref{Path: "g"}, fs.MoveOptions{}); herr.Kind(err) != fs.CodeAlreadyExists {
		t.Fatalf("move: %v", err)
	}
	want := []string{
		"before write f present=yes",
		"before delete f present=yes",
		"before move-source f present=yes", "before move-destination g present=no",
	}
	if got := r.since(n); !slices.Equal(got, want) {
		t.Fatalf("events:\n got %q\nwant %q", got, want)
	}
	f.dirErr = errors.New("listing failed")
	if _, _, err := r.ops.ReadDir(ctx, call, fs.Ref{Path: "."}, 0); !errors.Is(err, f.dirErr) {
		t.Fatalf("readdir: %v, want the provider's error", err)
	}
}

// Both ends of a move are fenced before either event: a source or a
// destination outside the write roots refuses the whole move, and a
// BeforeMutation refusal of the destination stops it after the source was
// announced. Neither file changes.
func TestMoveFencesAndAnnouncesBothEnds(t *testing.T) {
	ctx := context.Background()
	m := fstest.NewMemFS()
	r := newRig(t, m, sandbox.WorkspaceWrite)
	r.seed(t, "f", "x")
	outside := filepath.Join(filepath.Dir(r.root), "elsewhere", "o")
	if _, err := m.Write(ctx, fs.Ref{Path: outside}, []byte("o"), fs.NoGuard, sandboxtest.Policy(sandbox.DangerFullAccess)); err != nil {
		t.Fatal(err)
	}
	n := r.mark()
	if _, err := r.ops.Move(ctx, call, fs.Ref{Path: "f"}, fs.Ref{Path: outside}, fs.MoveOptions{Overwrite: true}); herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("move to outside the root: %v, want %q", err, fs.CodeSandboxDenied)
	}
	if _, err := r.ops.Move(ctx, call, fs.Ref{Path: outside}, fs.Ref{Path: "in"}, fs.MoveOptions{}); herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("move from outside the root: %v, want %q", err, fs.CodeSandboxDenied)
	}
	if got := r.since(n); len(got) != 0 {
		t.Fatalf("fenced moves emitted %q", got)
	}

	if _, err := fs.BeforeMutation.On(r.ctx, func(_ *cordis.Context, mu fs.Mutation) error {
		if mu.Op == fs.OpMoveDestination {
			return errors.New("the destination cannot be captured")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n = r.mark()
	if _, err := r.ops.Move(ctx, call, fs.Ref{Path: "f"}, fs.Ref{Path: "g"}, fs.MoveOptions{}); herr.Kind(err) != fs.CodeAborted {
		t.Fatalf("move with a refused destination: %v, want %q", err, fs.CodeAborted)
	}
	if want := []string{"before move-source f present=yes", "before move-destination g present=no"}; !slices.Equal(r.since(n), want) {
		t.Fatalf("events:\n got %q\nwant %q", r.since(n), want)
	}
	if got, ok := r.content(t, "f"); !ok || got != "x" {
		t.Fatalf("f is %q (present %v)", got, ok)
	}
	if _, ok := r.content(t, "g"); ok {
		t.Fatal("g exists after a refused move")
	}
	if b, _, err := m.Open(ctx, fs.Ref{Path: outside}); err != nil {
		t.Fatal(err)
	} else {
		got, _ := io.ReadAll(b)
		_ = b.Close()
		if string(got) != "o" {
			t.Fatalf("the outside file is %q", got)
		}
	}
}

// A move whose source BeforeMutation refuses never announces the destination.
func TestMoveSourceRefusalStopsBeforeTheDestination(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	r.seed(t, "f", "x")
	var seen []fs.Op
	if _, err := fs.BeforeMutation.On(r.ctx, func(_ *cordis.Context, mu fs.Mutation) error {
		seen = append(seen, mu.Op)
		if mu.Op == fs.OpMoveSource {
			return errors.New("no")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ops.Move(ctx, call, fs.Ref{Path: "f"}, fs.Ref{Path: "g"}, fs.MoveOptions{}); herr.Kind(err) != fs.CodeAborted {
		t.Fatalf("move: %v, want %q", err, fs.CodeAborted)
	}
	if !slices.Equal(seen, []fs.Op{fs.OpMoveSource}) {
		t.Fatalf("listener saw %q", seen)
	}
}
