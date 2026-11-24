//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// ci3Process is one booted process: a session service over the embedded
// SQLite store and the cycle C-3 catalogue entries applied by the loader.
type ci3Process struct {
	store  *sqlite.Store
	root   *cordis.Context
	svc    session.Service
	wd     *workdir.Service
	lookup workdir.Lookup
	handle session.Handle
}

func (p *ci3Process) shutdown() {
	ctx := context.Background()
	if p.handle != nil {
		_ = p.handle.Close(ctx)
		p.handle = nil
	}
	if p.root != nil {
		p.root.Dispose()
		p.root = nil
	}
	if p.store != nil {
		_ = p.store.Close(ctx)
		p.store = nil
	}
}

type ci3World struct {
	base string
	db   string
	dirs map[string]string // project name -> path as created
	// canon is the path the spine reports for a project (Attach / Change).
	canon map[string]string
	proc  *ci3Process
	id    session.ID
	cur   string // current working directory as reported by the spine
	err   error
}

func ci3Get[T any](root *cordis.Context, k cordis.ServiceKey[T]) (T, error) {
	v, err := k.Get(root)
	if err != nil {
		return v, fmt.Errorf("%s is not provided after boot: %w", k.Name(), err)
	}
	return v, nil
}

func ci3Entries(mode string) []loader.EntryOptions {
	return []loader.EntryOptions{
		{ID: "fs.local", Name: "fs.local"},
		{ID: "session.workdir", Name: "session.workdir"},
		{ID: "sandbox.policy", Name: "sandbox.policy", Config: json.RawMessage(fmt.Sprintf(`{"defaultMode":%q}`, mode))},
		{ID: "fs.ops", Name: "fs.ops"},
	}
}

func (w *ci3World) boot(ctx context.Context, mode string) error {
	cat, err := builtin.Catalog()
	if err != nil {
		return err
	}
	p := &ci3Process{}
	w.proc = p
	if p.store, err = sqlite.Open(ctx, w.db); err != nil {
		return fmt.Errorf("open embedded sqlite store %s: %w", w.db, err)
	}
	p.root = cordis.New(cordis.WithExporters())
	reg := views.New()
	p.svc = session.NewService(p.root, p.store, session.Config{Folder: reg})
	if _, err := views.Key.Provide(p.root, reg); err != nil {
		return err
	}
	if _, err := session.Key.Provide(p.root, p.svc); err != nil {
		return err
	}
	fiber, err := p.root.Use(loader.Component, loader.Options{Resolver: cat})
	if err != nil {
		return err
	}
	if err := fiber.Wait(); err != nil {
		return err
	}
	ldr, err := ci3Get(p.root, loader.Key)
	if err != nil {
		return err
	}
	if err := ldr.Apply(ci3Entries(mode)); err != nil {
		return fmt.Errorf("apply cycle C-3 entries: %w", err)
	}
	if p.wd, err = ci3Get(p.root, workdir.ServiceKey); err != nil {
		return err
	}
	if p.lookup, err = ci3Get(p.root, workdir.Key); err != nil {
		return err
	}
	if _, err := ci3Get(p.root, sandbox.ResolverKey); err != nil {
		return err
	}
	if _, err := ci3Get(p.root, ops.Key); err != nil {
		return err
	}
	return nil
}

func (w *ci3World) givenStoreAndProjects(a, b string) error {
	base, err := os.MkdirTemp("", "dsh-ci3-e2e-")
	if err != nil {
		return err
	}
	w.base = base
	w.db = filepath.Join(base, "sessions.db")
	w.dirs = map[string]string{}
	w.canon = map[string]string{}
	for _, n := range []string{a, b} {
		dir := filepath.Join(base, n)
		// Each project has its own .git, so it is its own project root and B
		// lies outside A's workspace.
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return err
		}
		w.dirs[n] = dir
	}
	return nil
}

func (w *ci3World) givenBootedSpine(ctx context.Context, mode, dir string) error {
	if mode != string(sandbox.WorkspaceWrite) {
		return fmt.Errorf("unsupported mode %q in this suite", mode)
	}
	if err := w.boot(ctx, mode); err != nil {
		return err
	}
	src, ok := w.dirs[dir]
	if !ok {
		return fmt.Errorf("unknown project %q", dir)
	}
	h, err := w.proc.svc.Create(ctx, nil, session.CreateOptions{Header: session.Header{Cwd: src}, Scope: scope.Key{}})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	w.proc.handle = h
	w.id = h.ID()
	got, err := w.proc.wd.Attach(ctx, h, nil)
	if err != nil {
		return fmt.Errorf("attach working directory: %w", err)
	}
	if got.Path == "" {
		return errors.New("attached working directory has an empty path")
	}
	w.canon[dir] = got.Path
	w.cur = got.Path
	return nil
}

// path maps "A/x" to the spine-reported directory of A joined with x.
func (w *ci3World) path(ref string) (string, error) {
	name, rest, _ := strings.Cut(ref, "/")
	d, ok := w.canon[name]
	if !ok {
		return "", fmt.Errorf("project %q has no spine-reported directory yet", name)
	}
	return filepath.Join(d, filepath.FromSlash(rest)), nil
}

func (w *ci3World) write(ctx context.Context, path, data string) error {
	o, ok := ops.Key.Lookup(w.proc.root)
	if !ok {
		return fmt.Errorf("%s is not mounted", ops.Key.Name())
	}
	_, err := o.Write(ctx, ops.Call{Session: w.id}, fs.Ref{Path: path}, []byte(data), fs.NoGuard)
	return err
}

func (w *ci3World) writeSucceeds(ctx context.Context, ref, data string) error {
	p, err := w.path(ref)
	if err != nil {
		return err
	}
	if err := w.write(ctx, p, data); err != nil {
		return fmt.Errorf("write %s: %w", ref, err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != data {
		return fmt.Errorf("%s = %q, %v; want %q", ref, got, err, data)
	}
	return nil
}

func (w *ci3World) move(ctx context.Context, dir string) error {
	dst, ok := w.dirs[dir]
	if !ok {
		return fmt.Errorf("unknown project %q", dir)
	}
	moved, err := w.proc.wd.Change(ctx, w.id, dst)
	if err != nil {
		return fmt.Errorf("move session to %s: %w", dir, err)
	}
	for n, p := range w.canon {
		if p == moved.Path {
			return fmt.Errorf("moved to %q, which is still project %s", moved.Path, n)
		}
	}
	if moved.Root != moved.Path {
		return fmt.Errorf("moved to %+v; want %s as its own project root", moved, dir)
	}
	w.canon[dir] = moved.Path
	w.cur = moved.Path
	return nil
}

func (w *ci3World) thenWorkingDirectory(dir string) error {
	want, ok := w.canon[dir]
	if !ok {
		return fmt.Errorf("project %q has no spine-reported directory", dir)
	}
	if w.cur != want {
		return fmt.Errorf("working directory %q; want %s (%q)", w.cur, dir, want)
	}
	acc, err := w.proc.lookup.Access(w.id)
	if err != nil {
		return fmt.Errorf("workdir.Access: %w", err)
	}
	p, err := acc.Path()
	if err != nil || p != want {
		return fmt.Errorf("workdir.Access path = %q, %v; want %q", p, err, want)
	}
	return nil
}

func (w *ci3World) resolvedRoots(ctx context.Context, mode string) ([]string, error) {
	r, err := ci3Get(w.proc.root, sandbox.ResolverKey)
	if err != nil {
		return nil, err
	}
	p, err := r.Resolve(ctx, sandbox.Request{Session: w.id})
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	if string(p.Mode()) != mode {
		return nil, fmt.Errorf("resolved mode %q, want %q", p.Mode(), mode)
	}
	var roots []string
	for _, r := range p.WriteRoots() {
		roots = append(roots, r.Path)
	}
	return roots, nil
}

func (w *ci3World) thenPolicyIncludes(ctx context.Context, mode, in string) error {
	roots, err := w.resolvedRoots(ctx, mode)
	if err != nil {
		return err
	}
	if !slices.Contains(roots, w.canon[in]) {
		return fmt.Errorf("write roots %q; want %s (%q)", roots, in, w.canon[in])
	}
	return nil
}

func (w *ci3World) thenPolicyIncludesExcludes(ctx context.Context, mode, in, out string) error {
	if err := w.thenPolicyIncludes(ctx, mode, in); err != nil {
		return err
	}
	roots, _ := w.resolvedRoots(ctx, mode)
	if slices.Contains(roots, w.canon[out]) {
		return fmt.Errorf("write roots %q still contain %s (%q)", roots, out, w.canon[out])
	}
	return nil
}

func (w *ci3World) thenDenied(ctx context.Context, ref, code, keep string) error {
	p, err := w.path(ref)
	if err != nil {
		return err
	}
	err = w.write(ctx, p, "outside")
	if err == nil {
		return fmt.Errorf("write to %s succeeded; want %s", ref, code)
	}
	if string(herr.Kind(err)) != code {
		return fmt.Errorf("write to %s: %v (%s), want %s", ref, err, herr.Kind(err), code)
	}
	got, rerr := os.ReadFile(p)
	if rerr != nil || string(got) != keep {
		return fmt.Errorf("%s = %q, %v after a denied write; want %q untouched", ref, got, rerr, keep)
	}
	return nil
}

func (w *ci3World) thenRelative(ctx context.Context, in, notIn string) error {
	cur, old := w.canon[in], w.canon[notIn]
	if cur != w.cur {
		return fmt.Errorf("current directory %q is not %s (%q)", w.cur, in, cur)
	}
	_ = os.Remove(filepath.Join(cur, "y"))
	_ = os.Remove(filepath.Join(old, "y"))
	data := "rel:" + cur
	if err := w.write(ctx, "y", data); err != nil {
		return fmt.Errorf("relative write y: %w", err)
	}
	if got, err := os.ReadFile(filepath.Join(cur, "y")); err != nil || string(got) != data {
		return fmt.Errorf("%s/y = %q, %v; want the relative write anchored at %s", in, got, err, in)
	}
	if _, err := os.Stat(filepath.Join(old, "y")); !os.IsNotExist(err) {
		return fmt.Errorf("%s/y exists (%v); the relative write must not land in %s", notIn, err, notIn)
	}
	return nil
}

func (w *ci3World) givenShutdown() error {
	w.proc.shutdown()
	w.proc = nil
	if st, err := os.Stat(w.db); err != nil || st.Size() == 0 {
		return fmt.Errorf("session store %s missing after shutdown: %v", w.db, err)
	}
	w.cur = ""
	return nil
}

func (w *ci3World) whenResume(ctx context.Context) error {
	if err := w.boot(ctx, string(sandbox.WorkspaceWrite)); err != nil {
		return err
	}
	h, _, err := w.proc.svc.Open(ctx, nil, w.id, session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("resume session %s: %w", w.id, err)
	}
	w.proc.handle = h
	got, err := w.proc.wd.Attach(ctx, h, nil)
	if err != nil {
		return fmt.Errorf("attach resumed session: %w", err)
	}
	w.cur = got.Path
	if got.Root != got.Path {
		return fmt.Errorf("resumed working directory %+v; want its own project root", got)
	}
	// Clear B's files from the first process so the resumed decisions are proven afresh.
	_ = os.Remove(filepath.Join(w.cur, "x"))
	_ = os.Remove(filepath.Join(w.cur, "y"))
	return nil
}

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_integration_at_one_commit(ctx *godog.ScenarioContext) {
	w := &ci3World{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.proc != nil {
			w.proc.shutdown()
		}
		if w.base != "" {
			_ = os.RemoveAll(w.base)
		}
		return c, err
	})
	ctx.Step(`^an embedded SQLite session store and sibling projects "([^"]*)" and "([^"]*)" in a temp directory$`, w.givenStoreAndProjects)
	ctx.Step(`^a booted spine with "([^"]*)" and working directory "([^"]*)"$`, w.givenBootedSpine)
	ctx.Step(`^a write to "([^"]*)" with "([^"]*)" succeeds$`, w.writeSucceeds)
	ctx.Step(`^the resolved policy is "([^"]*)" with write roots including "([^"]*)"$`, w.thenPolicyIncludes)
	ctx.Step(`^the resolved policy is "([^"]*)" with write roots including "([^"]*)" and excluding "([^"]*)"$`, w.thenPolicyIncludesExcludes)
	ctx.Step(`^the host moves the session to "([^"]*)"$`, w.move)
	ctx.Step(`^the host moved the session to "([^"]*)"$`, w.move)
	ctx.Step(`^the working directory reported by the spine is "([^"]*)"$`, w.thenWorkingDirectory)
	ctx.Step(`^the rebuilt working directory is "([^"]*)"$`, w.thenWorkingDirectory)
	ctx.Step(`^a write to "([^"]*)" returns "([^"]*)" and "[^"]*" still holds "([^"]*)"$`, w.thenDenied)
	ctx.Step(`^a relative write lands in "([^"]*)" and not in "([^"]*)"$`, w.thenRelative)
	ctx.Step(`^the process shut down, leaving the session persisted in the SQLite store$`, w.givenShutdown)
	ctx.Step(`^the process reopens the store and resumes the session$`, w.whenResume)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_integration_at_one_commit(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_integration_at_one_commit",
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_integration_at_one_commit,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_cycle_c_3_integration_at_one_commit.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cycle C-3 integration e2e scenarios failed")
	}
}
