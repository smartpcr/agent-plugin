package local_test

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

func mkdirs(t *testing.T, ds ...string) {
	t.Helper()
	for _, d := range ds {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// swapForLink moves the directory dir aside and puts a link to target in its
// place; the link is removed (never followed) when the test ends.
func swapForLink(t *testing.T, dir, target string) {
	t.Helper()
	if err := os.Rename(dir, dir+".keep"); err != nil {
		t.Errorf("move %s aside: %v", dir, err)
		return
	}
	kind, err := makeLink(target, dir)
	if err != nil {
		t.Errorf("link %s -> %s: %v", dir, target, err)
		return
	}
	t.Logf("swapped %s for a %s to %s", dir, kind, target)
	t.Cleanup(func() { _ = os.Remove(dir) })
}

// once arms the before-open-root hook to run f on the first mutation only.
func once(t *testing.T, f func()) {
	var o sync.Once
	t.Cleanup(local.SetBeforeOpenRoot(func(string) { o.Do(f) }))
}

// entries lists every name below dir, recursively, without following links.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(dir, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			got = append(got, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func wantCode(t *testing.T, what string, err error, code herr.Reason) {
	t.Helper()
	if got := herr.Kind(err); got != code {
		t.Fatalf("%s: %v (code %q), want %q", what, err, got, code)
	}
}

// TestRootSwappedAfterTheFenceIsDenied swaps the write root, or a directory
// above it, for a link to an outside directory O in the window between the
// fence and the opening of the root handle: the handle must never land on O.
//
//dsh:test DH-T-FS-10
func TestRootSwappedAfterTheFenceIsDenied(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		// swap is the directory swapped, relative to the test dir; root the
		// write root; pre is made in O beforehand; mkRoot makes the root first.
		swap, root, pre string
		mkRoot          bool
	}{
		{name: "root", swap: "R", root: "R", mkRoot: true},
		{name: "ancestor", swap: "A", root: filepath.Join("A", "R"), pre: "R", mkRoot: true},
		{name: "ancestor of a root not made yet", swap: "A", root: filepath.Join("A", "R")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			o := filepath.Join(tmp, "O")
			mkdirs(t, o, filepath.Join(tmp, tc.swap))
			if tc.mkRoot {
				mkdirs(t, filepath.Join(tmp, tc.root))
			}
			if tc.pre != "" {
				mkdirs(t, filepath.Join(o, tc.pre))
			}
			before := entries(t, o)
			root := filepath.Join(tmp, tc.root)
			p := sandboxtest.Policy(sandbox.WorkspaceWrite, root)
			once(t, func() { swapForLink(t, filepath.Join(tmp, tc.swap), o) })

			_, err := local.New().Write(ctx, fs.Ref{Path: filepath.Join(root, "f")}, []byte("x"), fs.NoGuard, p)
			wantCode(t, "write after the swap", err, fs.CodeSandboxDenied)
			if after := entries(t, o); len(after) != len(before) {
				t.Fatalf("O changed: %v, was %v", after, before)
			}
		})
	}
}

// TestMoveWithAnEndSwappedAfterTheFenceIsDenied swaps the destination's or the
// source's parent for a link to O after both ends were fenced.
//
//dsh:test DH-T-FS-10
func TestMoveWithAnEndSwappedAfterTheFenceIsDenied(t *testing.T) {
	ctx := context.Background()
	t.Run("destination parent", func(t *testing.T) {
		tmp := t.TempDir()
		r, o := filepath.Join(tmp, "R"), filepath.Join(tmp, "O")
		mkdirs(t, filepath.Join(r, "d"), o)
		src := filepath.Join(r, "src")
		writeFile(t, src, "s")
		p := sandboxtest.Policy(sandbox.WorkspaceWrite, r)
		once(t, func() { swapForLink(t, filepath.Join(r, "d"), o) })

		_, err := local.New().Move(ctx, fs.Ref{Path: src}, fs.Ref{Path: filepath.Join(r, "d", "x")}, fs.MoveOptions{}, p)
		wantCode(t, "move into the swapped parent", err, fs.CodeSandboxDenied)
		if got := entries(t, o); len(got) != 0 {
			t.Fatalf("O changed: %v", got)
		}
		if _, err := os.Stat(src); err != nil {
			t.Fatalf("the source is gone: %v", err)
		}
	})
	t.Run("source parent", func(t *testing.T) {
		tmp := t.TempDir()
		r, o := filepath.Join(tmp, "R"), filepath.Join(tmp, "O")
		mkdirs(t, filepath.Join(r, "s"), o)
		writeFile(t, filepath.Join(r, "s", "f"), "inside")
		writeFile(t, filepath.Join(o, "f"), "outside")
		p := sandboxtest.Policy(sandbox.WorkspaceWrite, r)
		once(t, func() { swapForLink(t, filepath.Join(r, "s"), o) })

		dst := filepath.Join(r, "g")
		_, err := local.New().Move(ctx, fs.Ref{Path: filepath.Join(r, "s", "f")}, fs.Ref{Path: dst}, fs.MoveOptions{}, p)
		wantCode(t, "move out of the swapped parent", err, fs.CodeSandboxDenied)
		if b, err := os.ReadFile(filepath.Join(o, "f")); err != nil || string(b) != "outside" {
			t.Fatalf("O/f: %q, %v; want it untouched", b, err)
		}
		if _, err := os.Lstat(dst); !errors.Is(err, iofs.ErrNotExist) {
			t.Fatalf("the destination exists: %v", err)
		}
	})
}

// TestMoveBetweenTwoWriteRootsIsRefused: no single root handle confines such a
// rename, so it is refused before anything changes.
func TestMoveBetweenTwoWriteRootsIsRefused(t *testing.T) {
	tmp := t.TempDir()
	r1, r2 := filepath.Join(tmp, "R1"), filepath.Join(tmp, "R2")
	mkdirs(t, r1, r2)
	src := filepath.Join(r1, "f")
	writeFile(t, src, "s")
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, r1, r2)

	_, err := local.New().Move(context.Background(), fs.Ref{Path: src}, fs.Ref{Path: filepath.Join(r2, "f")}, fs.MoveOptions{}, p)
	wantCode(t, "move between roots", err, fs.CodeInvalidPath)
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("the source is gone: %v", err)
	}
	if got := entries(t, r2); len(got) != 0 {
		t.Fatalf("R2 changed: %v", got)
	}
}

// TestFailedSyncAfterPublicationIsReported: the content is in place, and the
// outcome says its durability is unknown instead of reporting a durable write.
func TestFailedSyncAfterPublicationIsReported(t *testing.T) {
	t.Cleanup(local.FailSyncAfterPublish())
	ctx := context.Background()
	dir := t.TempDir()
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	f := filepath.Join(dir, "f")
	l := local.New()
	for _, g := range []fs.Guard{fs.CreateIfAbsent, fs.NoGuard} {
		data := "content " + strconv.Quote(guardName(g))
		out, err := l.Write(ctx, fs.Ref{Path: f}, []byte(data), g, p)
		if err != nil {
			t.Fatalf("%s: %v", guardName(g), err)
		}
		if !errors.Is(out.DurabilityUnknown, fsx.ErrDurabilityUnknown) {
			t.Fatalf("%s: DurabilityUnknown = %v, want fsx.ErrDurabilityUnknown", guardName(g), out.DurabilityUnknown)
		}
		if b, _ := os.ReadFile(f); string(b) != data {
			t.Fatalf("%s: file holds %q, want %q", guardName(g), b, data)
		}
	}
}

func guardName(g fs.Guard) string {
	if g == fs.CreateIfAbsent {
		return "create-if-absent"
	}
	return "no-guard"
}

func readVersion(t testing.TB, l fs.FS, f string) (fs.Version, []byte) {
	t.Helper()
	rc, in, err := l.Open(context.Background(), fs.Ref{Path: f})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return in.Version, b
}
