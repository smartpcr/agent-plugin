package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// cycleC3 is one process booted with the cycle C-3 unit from the build
// catalogue: a session service over an embedded SQLite store and the four
// entries fs.local, session.workdir, sandbox.policy (workspace-write) and fs.ops,
// each wired to the others only through the services they provide.
type cycleC3 struct {
	st       *sqlite.Store
	svc      session.Service
	wd       *workdir.Service
	lookup   workdir.Lookup
	resolver sandbox.Resolver
	ops      *ops.Service
}

func bootCycleC3(t *testing.T, ctx context.Context, db string) *cycleC3 {
	t.Helper()
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// Registered before the tree, so the store closes after the tree has
	// unloaded every entry (cleanups run last-registered first).
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	root := testkit.Tree(t)
	reg := views.New()
	svc := session.NewService(root, st, session.Config{Folder: reg})
	if _, err := views.Key.Provide(root, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Key.Provide(root, svc); err != nil {
		t.Fatal(err)
	}
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat})
	if err != nil {
		t.Fatal(err)
	}
	if err := fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	ldr, err := loader.Key.Get(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ldr.Apply([]loader.EntryOptions{
		{ID: "fs.local", Name: "fs.local"},
		{ID: "session.workdir", Name: "session.workdir"},
		{ID: "sandbox.policy", Name: "sandbox.policy", Config: json.RawMessage(`{"defaultMode":"workspace-write"}`)},
		{ID: "fs.ops", Name: "fs.ops"},
	}); err != nil {
		t.Fatal(err)
	}
	b := &cycleC3{st: st, svc: svc}
	b.wd = get(t, root, workdir.ServiceKey)
	b.lookup = get(t, root, workdir.Key)
	b.resolver = get(t, root, sandbox.ResolverKey)
	b.ops = get(t, root, ops.Key)
	return b
}

func get[T any](t *testing.T, root *cordis.Context, k cordis.ServiceKey[T]) T {
	t.Helper()
	v, err := k.Get(root)
	if err != nil {
		t.Fatalf("%s is not provided after boot: %v", k.Name(), err)
	}
	return v
}

// project is a fresh directory with its own .git, so it is its own project root.
func project(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (b *cycleC3) write(ctx context.Context, id session.ID, path, data string) error {
	_, err := b.ops.Write(ctx, ops.Call{Session: id}, fs.Ref{Path: path}, []byte(data), fs.NoGuard)
	return err
}

// assertFence checks that a write into old is refused as fs/sandbox-denied and
// leaves the file as it was, and that a write into cur succeeds.
func (b *cycleC3) assertFence(t *testing.T, ctx context.Context, id session.ID, old, cur, keep string) {
	t.Helper()
	err := b.write(ctx, id, filepath.Join(old, "x"), "outside")
	if herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("write to %s/x after the move: %v (%s), want %s", old, err, herr.Kind(err), fs.CodeSandboxDenied)
	}
	if got, err := os.ReadFile(filepath.Join(old, "x")); err != nil || string(got) != keep {
		t.Fatalf("%s/x = %q, %v after a denied write; want %q untouched", old, got, err, keep)
	}
	if err := b.write(ctx, id, filepath.Join(cur, "x"), "inside"); err != nil {
		t.Fatalf("write to %s/x: %v", cur, err)
	}
	if got, err := os.ReadFile(filepath.Join(cur, "x")); err != nil || string(got) != "inside" {
		t.Fatalf("%s/x = %q, %v; want %q", cur, got, err, "inside")
	}
}

// assertResolve checks that the resolver, given only the session, resolves
// workspace-write with the session's current directory as the workspace root and
// the old directory nowhere among the write roots.
func (b *cycleC3) assertResolve(t *testing.T, ctx context.Context, id session.ID, old, cur string) {
	t.Helper()
	p, err := b.resolver.Resolve(ctx, sandbox.Request{Session: id})
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode() != sandbox.WorkspaceWrite {
		t.Fatalf("resolved mode %q, want %q", p.Mode(), sandbox.WorkspaceWrite)
	}
	var roots []string
	for _, r := range p.WriteRoots() {
		roots = append(roots, r.Path)
	}
	if !slices.Contains(roots, cur) || slices.Contains(roots, old) {
		t.Fatalf("write roots %q; want %s and not %s", roots, cur, old)
	}
}

// TC-01b: one session, on the real local provider and an embedded SQLite log,
// moves from A to B (a sibling project outside A); the fence follows it, and a
// second process that reopens the store and resumes the session rebuilds B and
// holds the same write decisions.
func TestCycleC3DirectoryMoveChangesTheFenceAndResumeRestoresIt(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	a := project(t, filepath.Join(base, "A"))
	bdir := project(t, filepath.Join(base, "B"))
	db := filepath.Join(base, "sessions.db")

	var id session.ID
	var a0, b0 string
	t.Run("directory move changes the fence", func(t *testing.T) {
		p1 := bootCycleC3(t, ctx, db)
		h, err := p1.svc.Create(ctx, nil, session.CreateOptions{Header: session.Header{Cwd: a}, Scope: scope.Key{}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(ctx) })
		id = h.ID()
		w, err := p1.wd.Attach(ctx, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		a0 = w.Path
		if err := p1.write(ctx, id, filepath.Join(a0, "x"), "before"); err != nil {
			t.Fatalf("write to A/x before the move: %v", err)
		}
		p1.assertResolve(t, ctx, id, "", a0)

		moved, err := p1.wd.Change(ctx, id, bdir)
		if err != nil {
			t.Fatal(err)
		}
		b0 = moved.Path
		if moved.Root != b0 || b0 == a0 {
			t.Fatalf("moved to %+v; want B as its own project root", moved)
		}
		if got, err := p1.lookup.Access(id); err != nil {
			t.Fatal(err)
		} else if p, err := got.Path(); err != nil || p != b0 {
			t.Fatalf("workdir.Access path = %q, %v; want %q", p, err, b0)
		}
		p1.assertResolve(t, ctx, id, a0, b0)
		p1.assertFence(t, ctx, id, a0, b0, "before")
	})
	if id == "" || t.Failed() {
		return
	}

	t.Run("resume restores the fence", func(t *testing.T) {
		p2 := bootCycleC3(t, ctx, db)
		h, _, err := p2.svc.Open(ctx, nil, id, session.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(ctx) })
		w, err := p2.wd.Attach(ctx, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		if w.Path != b0 || w.Root != b0 {
			t.Fatalf("resumed working directory %+v; want %s", w, b0)
		}
		if err := os.Remove(filepath.Join(b0, "x")); err != nil {
			t.Fatal(err)
		}
		p2.assertResolve(t, ctx, id, a0, b0)
		p2.assertFence(t, ctx, id, a0, b0, "before")
	})
}
