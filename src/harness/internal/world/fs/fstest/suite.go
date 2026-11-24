package fstest

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// Subject is one provider under test.
type Subject struct {
	FS fs.FS
	// Symlink makes link a symlink to target; nil skips the symlink subtests
	// (a platform without unprivileged symlinks).
	Symlink func(target, link string) error
}

// Run is the conformance suite every fs.FS provider must pass (DH-T-FS-8). newFS
// returns a fresh, empty provider for each subtest. Paths live under
// t.TempDir(): <tmp>/ws is the workspace-write root, <tmp>/out is outside it.
func Run(t *testing.T, newFS func(t *testing.T) Subject) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			e := &env{t: t, Subject: newFS(t), ws: filepath.Join(base, "ws"), out: filepath.Join(base, "out")}
			e.full = sandboxtest.Policy(sandbox.DangerFullAccess)
			e.write = sandboxtest.Policy(sandbox.WorkspaceWrite, e.ws)
			e.ro = sandboxtest.Policy(sandbox.ReadOnly)
			e.mustWrite(e.in(".keep"), "")
			e.mustWrite(e.outside(".keep"), "")
			c.run(e)
		})
	}
}

type env struct {
	Subject
	t               *testing.T
	ws, out         string
	full, write, ro sandbox.Policy
}

func (e *env) in(rel string) string      { return filepath.Join(e.ws, rel) }
func (e *env) outside(rel string) string { return filepath.Join(e.out, rel) }
func ref(p string) fs.Ref                { return fs.Ref{Path: p} }

func (e *env) mustWrite(p, s string) fs.WriteOutcome {
	e.t.Helper()
	o, err := e.FS.Write(context.Background(), ref(p), []byte(s), fs.NoGuard, e.full)
	if err != nil {
		e.t.Fatalf("write %s: %v", p, err)
	}
	return o
}

func (e *env) read(p string) (string, fs.Info) {
	e.t.Helper()
	rc, in, err := e.FS.Open(context.Background(), ref(p))
	if err != nil {
		e.t.Fatalf("open %s: %v", p, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		e.t.Fatalf("read %s: %v", p, err)
	}
	return string(b), in
}

func (e *env) exists(p string) bool {
	e.t.Helper()
	in, err := e.FS.Stat(context.Background(), fs.Ref{Path: p, NoFollowFinal: true})
	if err != nil {
		e.t.Fatalf("stat %s: %v", p, err)
	}
	return in.Exists
}

func (e *env) code(err error, want herr.Reason) {
	e.t.Helper()
	if got := herr.Kind(err); got != want {
		e.t.Fatalf("error = %v (code %q), want code %q", err, got, want)
	}
}

func (e *env) symlinks() {
	e.t.Helper()
	if e.Symlink == nil {
		e.t.Skip("provider has no symlinks")
	}
}

var cases = []struct {
	name string
	run  func(e *env)
}{
	{"stat-missing-is-not-an-error", func(e *env) {
		in, err := e.FS.Stat(context.Background(), ref(e.in("nope")))
		if err != nil || in.Exists {
			e.t.Fatalf("stat missing = %+v, %v; want Exists=false, nil", in, err)
		}
		_, _, err = e.FS.Open(context.Background(), ref(e.in("nope")))
		e.code(err, fs.CodeNotFound)
	}},
	{"relative-path-resolves-against-dir", func(e *env) {
		e.mustWrite(e.in("rel.txt"), "x")
		in, err := e.FS.Stat(context.Background(), fs.Ref{Path: "rel.txt", Dir: e.ws})
		if err != nil || !in.Exists || in.Target.Display != e.in("rel.txt") {
			e.t.Fatalf("stat relative = %+v, %v", in, err)
		}
		_, err = e.FS.Stat(context.Background(), ref("rel.txt"))
		e.code(err, fs.CodeInvalidPath)
	}},
	{"create-then-read-same-version", func(e *env) {
		o, err := e.FS.Write(context.Background(), ref(e.in("a/b/c.txt")), []byte("hello"), fs.CreateIfAbsent, e.write)
		if err != nil || !o.Created {
			e.t.Fatalf("create = %+v, %v", o, err)
		}
		s, in := e.read(e.in("a/b/c.txt"))
		if s != "hello" || in.Size != 5 || in.Type != fs.TypeFile || !in.Version.Same(o.Version) {
			e.t.Fatalf("read = %q %+v; want hello with version %+v", s, in, o.Version)
		}
		st, _ := e.FS.Stat(context.Background(), ref(e.in("a/b")))
		if st.Type != fs.TypeDir {
			e.t.Fatalf("parent type = %q, want dir", st.Type)
		}
	}},
	{"create-if-absent-refuses-existing", func(e *env) {
		e.mustWrite(e.in("f"), "one")
		_, err := e.FS.Write(context.Background(), ref(e.in("f")), []byte("two"), fs.CreateIfAbsent, e.write)
		e.code(err, fs.CodeAlreadyExists)
		if s, _ := e.read(e.in("f")); s != "one" {
			e.t.Fatalf("content = %q, want one", s)
		}
	}},
	{"replace-current-version", func(e *env) {
		e.mustWrite(e.in("f"), "one")
		_, in := e.read(e.in("f"))
		o, err := e.FS.Write(context.Background(), ref(e.in("f")), []byte("two"), fs.ReplaceIfVersion{Version: in.Version}, e.write)
		if err != nil || o.Created {
			e.t.Fatalf("replace = %+v, %v", o, err)
		}
		st, _ := e.FS.Stat(context.Background(), ref(e.in("f")))
		if _, err := e.FS.Write(context.Background(), ref(e.in("f")), []byte("three"), fs.ReplaceIfVersion{Version: st.Version}, e.write); err != nil {
			e.t.Fatalf("replace with stat version: %v", err)
		}
	}},
	{"stale-write-refused", func(e *env) {
		e.mustWrite(e.in("f"), "v1")
		_, v1 := e.read(e.in("f"))
		e.mustWrite(e.in("f"), "v2")
		_, err := e.FS.Write(context.Background(), ref(e.in("f")), []byte("v3"), fs.ReplaceIfVersion{Version: v1.Version}, e.write)
		e.code(err, fs.CodeStaleVersion)
		if s, _ := e.read(e.in("f")); s != "v2" {
			e.t.Fatalf("content = %q, want v2", s)
		}
		e.FS.Delete(context.Background(), ref(e.in("f")), fs.NoGuard, e.full)
		_, err = e.FS.Write(context.Background(), ref(e.in("f")), []byte("v3"), fs.ReplaceIfVersion{Version: v1.Version}, e.write)
		e.code(err, fs.CodeStaleVersion)
	}},
	{"same-bytes-rewrite-not-stale", func(e *env) {
		e.mustWrite(e.in("f"), "same")
		_, v1 := e.read(e.in("f"))
		e.mustWrite(e.in("f"), "same")
		if _, err := e.FS.Write(context.Background(), ref(e.in("f")), []byte("new"), fs.ReplaceIfVersion{Version: v1.Version}, e.write); err != nil {
			e.t.Fatalf("digest-guarded write after same-bytes rewrite: %v", err)
		}
	}},
	{"fence-read-only-refuses-every-mutation", func(e *env) {
		e.mustWrite(e.in("f"), "x")
		ctx := context.Background()
		_, err := e.FS.Write(ctx, ref(e.in("g")), []byte("x"), fs.NoGuard, e.ro)
		e.code(err, fs.CodeSandboxDenied)
		_, err = e.FS.Delete(ctx, ref(e.in("f")), fs.NoGuard, e.ro)
		e.code(err, fs.CodeSandboxDenied)
		_, err = e.FS.Move(ctx, ref(e.in("f")), ref(e.in("h")), fs.MoveOptions{}, e.ro)
		e.code(err, fs.CodeSandboxDenied)
		if e.exists(e.in("g")) || e.exists(e.in("h")) || !e.exists(e.in("f")) {
			e.t.Fatal("a refused mutation changed the filesystem")
		}
		if s, _ := e.read(e.in("f")); s != "x" {
			e.t.Fatal("reads are not fenced")
		}
	}},
	{"fence-workspace-write-refuses-outside-root", func(e *env) {
		ctx := context.Background()
		e.mustWrite(e.outside("o"), "keep")
		_, err := e.FS.Write(ctx, ref(e.outside("new")), []byte("x"), fs.NoGuard, e.write)
		e.code(err, fs.CodeSandboxDenied)
		_, err = e.FS.Write(ctx, fs.Ref{Path: filepath.Join("..", "out", "new"), Dir: e.ws}, []byte("x"), fs.NoGuard, e.write)
		e.code(err, fs.CodeSandboxDenied)
		_, err = e.FS.Delete(ctx, ref(e.outside("o")), fs.NoGuard, e.write)
		e.code(err, fs.CodeSandboxDenied)
		if e.exists(e.outside("new")) || !e.exists(e.outside("o")) {
			e.t.Fatal("a refused mutation changed the filesystem")
		}
	}},
	{"fence-move-with-one-end-outside-changes-nothing", func(e *env) {
		ctx := context.Background()
		e.mustWrite(e.in("i"), "in")
		e.mustWrite(e.outside("o"), "out")
		_, err := e.FS.Move(ctx, ref(e.in("i")), ref(e.outside("moved")), fs.MoveOptions{}, e.write)
		e.code(err, fs.CodeSandboxDenied)
		_, err = e.FS.Move(ctx, ref(e.outside("o")), ref(e.in("moved")), fs.MoveOptions{}, e.write)
		e.code(err, fs.CodeSandboxDenied)
		if !e.exists(e.in("i")) || !e.exists(e.outside("o")) || e.exists(e.in("moved")) || e.exists(e.outside("moved")) {
			e.t.Fatal("a refused move changed the filesystem")
		}
	}},
	{"fence-danger-full-access-writes-anywhere", func(e *env) {
		if _, err := e.FS.Write(context.Background(), ref(e.outside("f")), []byte("x"), fs.NoGuard, e.full); err != nil {
			e.t.Fatalf("danger-full-access write outside: %v", err)
		}
	}},
	{"zero-policy-is-unavailable", func(e *env) {
		var zero sandbox.Policy
		ctx := context.Background()
		e.mustWrite(e.in("f"), "x")
		_, err := e.FS.Write(ctx, ref(e.in("g")), []byte("x"), fs.NoGuard, zero)
		e.code(err, sandbox.CodeUnavailable)
		_, err = e.FS.Delete(ctx, ref(e.in("f")), fs.NoGuard, zero)
		e.code(err, sandbox.CodeUnavailable)
		_, err = e.FS.Move(ctx, ref(e.in("f")), ref(e.in("h")), fs.MoveOptions{}, zero)
		e.code(err, sandbox.CodeUnavailable)
		if e.exists(e.in("g")) || e.exists(e.in("h")) || !e.exists(e.in("f")) {
			e.t.Fatal("a zero policy changed the filesystem")
		}
	}},
	{"delete", func(e *env) {
		ctx := context.Background()
		e.mustWrite(e.in("f"), "v1")
		_, v1 := e.read(e.in("f"))
		e.mustWrite(e.in("f"), "v2")
		_, err := e.FS.Delete(ctx, ref(e.in("f")), fs.ReplaceIfVersion{Version: v1.Version}, e.write)
		e.code(err, fs.CodeStaleVersion)
		o, err := e.FS.Delete(ctx, ref(e.in("f")), fs.NoGuard, e.write)
		if err != nil || !o.Existed || e.exists(e.in("f")) {
			e.t.Fatalf("delete = %+v, %v", o, err)
		}
		o, err = e.FS.Delete(ctx, ref(e.in("f")), fs.NoGuard, e.write)
		if err != nil || o.Existed {
			e.t.Fatalf("delete missing = %+v, %v; want Existed=false, nil", o, err)
		}
		e.mustWrite(e.in("d/x"), "")
		_, err = e.FS.Delete(ctx, ref(e.in("d")), fs.NoGuard, e.write)
		e.code(err, fs.CodeNotRegularFile)
	}},
	{"read-dir", func(e *env) {
		for _, n := range []string{"c", "a", "b"} {
			e.mustWrite(e.in(filepath.Join("d", n)), n)
		}
		ents, trunc, err := e.FS.ReadDir(context.Background(), ref(e.in("d")), 0)
		if err != nil || trunc || len(ents) != 3 || ents[0].Name != "a" || ents[2].Name != "c" || ents[0].Type != fs.TypeFile {
			e.t.Fatalf("readdir = %+v, %v, %v", ents, trunc, err)
		}
		ents, trunc, err = e.FS.ReadDir(context.Background(), ref(e.in("d")), 2)
		if err != nil || !trunc || len(ents) != 2 {
			e.t.Fatalf("readdir limit 2 = %+v, %v, %v", ents, trunc, err)
		}
		_, _, err = e.FS.ReadDir(context.Background(), ref(e.in("d/a")), 0)
		e.code(err, fs.CodeNotDirectory)
		_, _, err = e.FS.ReadDir(context.Background(), ref(e.in("nope")), 0)
		e.code(err, fs.CodeNotFound)
	}},
	{"move", func(e *env) {
		ctx := context.Background()
		e.mustWrite(e.in("a"), "A")
		e.mustWrite(e.in("b"), "B")
		_, err := e.FS.Move(ctx, ref(e.in("a")), ref(e.in("a")), fs.MoveOptions{}, e.write)
		e.code(err, fs.CodeSameTarget)
		_, err = e.FS.Move(ctx, ref(e.in("a")), ref(e.in("b")), fs.MoveOptions{}, e.write)
		e.code(err, fs.CodeAlreadyExists)
		o, err := e.FS.Move(ctx, ref(e.in("a")), ref(e.in("b")), fs.MoveOptions{Overwrite: true}, e.write)
		if err != nil || !o.Replaced || e.exists(e.in("a")) {
			e.t.Fatalf("move overwrite = %+v, %v", o, err)
		}
		if s, _ := e.read(e.in("b")); s != "A" {
			e.t.Fatalf("moved content = %q, want A", s)
		}
		o, err = e.FS.Move(ctx, ref(e.in("b")), ref(e.in("sub/c")), fs.MoveOptions{}, e.write)
		if err != nil || o.Replaced {
			e.t.Fatalf("move to new parent = %+v, %v", o, err)
		}
		_, err = e.FS.Move(ctx, ref(e.in("nope")), ref(e.in("x")), fs.MoveOptions{}, e.write)
		e.code(err, fs.CodeNotFound)
	}},
	{"fence-resolves-symlinks-first", func(e *env) {
		e.symlinks()
		ctx := context.Background()
		if err := e.Symlink(e.out, e.in("escape")); err != nil {
			e.t.Fatalf("symlink: %v", err)
		}
		if err := e.Symlink(e.in("real"), e.in("alias")); err != nil {
			e.t.Fatalf("symlink: %v", err)
		}
		e.mustWrite(e.in("real/.keep"), "")
		_, err := e.FS.Write(ctx, ref(e.in("escape/f")), []byte("x"), fs.NoGuard, e.write)
		e.code(err, fs.CodeSandboxDenied)
		if e.exists(e.outside("f")) {
			e.t.Fatal("a write through a link escaped the root")
		}
		o, err := e.FS.Write(ctx, ref(e.in("alias/f")), []byte("x"), fs.NoGuard, e.write)
		if err != nil {
			e.t.Fatalf("write through an inside link: %v", err)
		}
		if o.Target.Display != e.in("real/f") {
			e.t.Fatalf("target = %q, want the resolved %q", o.Target.Display, e.in("real/f"))
		}
		if _, err := e.FS.Delete(ctx, ref(e.in("escape")), fs.NoGuard, e.write); err != nil {
			e.t.Fatalf("delete the link itself: %v", err)
		}
		if !e.exists(e.outside(".keep")) {
			e.t.Fatal("deleting a link touched its target")
		}
	}},
	{"relative-symlink-follows-its-moved-directory", func(e *env) {
		e.symlinks()
		ctx := context.Background()
		e.mustWrite(e.in("old/f"), "content")
		if err := e.Symlink("f", e.in("old/link")); err != nil {
			e.t.Fatalf("symlink: %v", err)
		}
		if _, err := e.FS.Move(ctx, ref(e.in("old")), ref(e.in("new")), fs.MoveOptions{}, e.write); err != nil {
			e.t.Fatalf("move the directory: %v", err)
		}
		s, in := e.read(e.in("new/link"))
		if s != "content" || in.Target.Display != e.in("new/f") {
			e.t.Fatalf("read through the moved relative link = %q at %q, want content at %q", s, in.Target.Display, e.in("new/f"))
		}
		if e.exists(e.in("old")) {
			e.t.Fatal("the old directory still exists")
		}
	}},
	{"cancelled-context-aborts", func(e *env) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := e.FS.Write(ctx, ref(e.in("f")), []byte("x"), fs.NoGuard, e.write)
		e.code(err, fs.CodeAborted)
		if e.exists(e.in("f")) {
			e.t.Fatal("a cancelled write changed the filesystem")
		}
	}},
	{"create-if-absent-has-one-winner", func(e *env) {
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.FS.Write(context.Background(), ref(e.in("race")), []byte("x"), fs.CreateIfAbsent, e.write)
				switch herr.Kind(err) {
				case "":
					wins.Add(1)
				case fs.CodeAlreadyExists:
				default:
					e.t.Errorf("racing create: %v", err)
				}
			}()
		}
		wg.Wait()
		if n := wins.Load(); n != 1 {
			e.t.Fatalf("%d racing CreateIfAbsent writes succeeded, want 1", n)
		}
	}},
}
