package local_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// kinds records which link kinds a test made, for the test log (architecture.md
// "Gate-local services": a Windows lane without the symlink privilege uses
// junctions).
type kinds struct {
	mu   sync.Mutex
	seen map[string]int
}

func (k *kinds) link(t *testing.T) func(target, link string) error {
	return func(target, link string) error {
		kind, err := makeLink(target, link)
		if err == nil {
			k.mu.Lock()
			if k.seen == nil {
				k.seen = map[string]int{}
			}
			k.seen[kind]++
			k.mu.Unlock()
		}
		return err
	}
}

func (k *kinds) log(t *testing.T) {
	k.mu.Lock()
	defer k.mu.Unlock()
	t.Logf("link kinds made: %v", k.seen)
}

//dsh:test DH-T-FS-8, DH-T-FS-9
func TestLocalPassesConformance(t *testing.T) {
	var k kinds
	fstest.Run(t, func(t *testing.T) fstest.Subject {
		return fstest.Subject{FS: local.New(), Symlink: k.link(t)}
	})
	k.log(t)
}

//dsh:test DH-T-FS-13
func TestStaleVersionIsRefusedAndKeepsTheNewerBytes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	f := filepath.Join(dir, "f")
	a := local.New()
	if _, err := a.Write(ctx, fs.Ref{Path: f}, []byte("v1"), fs.NoGuard, p); err != nil {
		t.Fatal(err)
	}
	rc, in, err := a.Open(ctx, fs.Ref{Path: f})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(rc)
	_ = rc.Close()
	v1 := in.Version

	// Another handle (a second provider, as another process would be) rewrites f.
	b := local.New()
	if _, err := b.Write(ctx, fs.Ref{Path: f}, []byte("v2-other"), fs.ReplaceIfVersion{Version: v1}, p); err != nil {
		t.Fatalf("rewrite to v2: %v", err)
	}

	_, err = a.Write(ctx, fs.Ref{Path: f}, []byte("v3-stale"), fs.ReplaceIfVersion{Version: v1}, p)
	if got := herr.Kind(err); got != fs.CodeStaleVersion {
		t.Fatalf("write with v1 after v2: %v (code %q), want %q", err, got, fs.CodeStaleVersion)
	}
	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("v2-other")) {
		t.Fatalf("file holds %q after the refused write, want v2 bytes", got)
	}

	// An outside rewrite of the same size is still seen.
	if err := os.WriteFile(f, []byte("v2-OTHER"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = a.Write(ctx, fs.Ref{Path: f}, []byte("v4-stale"), fs.ReplaceIfVersion{Version: v1}, p)
	if got := herr.Kind(err); got != fs.CodeStaleVersion {
		t.Fatalf("write with v1 after an outside rewrite: %v (code %q), want %q", err, got, fs.CodeStaleVersion)
	}
}

// TestFenceHoldsThroughASwappedLink swaps R/sub between a real directory and a
// link to the outside directory O while the seam writes R/sub/f: nothing may ever
// land in O, and a refused write is fs/sandbox-denied.
//
//dsh:test DH-T-FS-10
func TestFenceHoldsThroughASwappedLink(t *testing.T) {
	const swaps = 1000
	ctx := context.Background()
	tmp := t.TempDir()
	r := filepath.Join(tmp, "R")
	o := filepath.Join(tmp, "O")
	for _, d := range []string{r, o} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(r, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Probe the link kind once so the log records it and an unsupported lane
	// fails loudly rather than racing nothing.
	probe := filepath.Join(tmp, "probe")
	kind, err := makeLink(o, probe)
	if err != nil {
		t.Fatalf("cannot make a directory link on this lane: %v", err)
	}
	_ = os.Remove(probe)
	t.Logf("swap race uses a %s for R/sub -> O", kind)

	p := sandboxtest.Policy(sandbox.WorkspaceWrite, r)
	seam := local.New()

	// The swapper moves the real R/sub aside and puts a link to O in its place,
	// then removes the link and moves the directory back. It never deletes a
	// tree through a name that may be a link.
	keep := filepath.Join(r, "sub.keep")
	var done, stop atomic.Bool
	var swapErrs, linked int
	var wg sync.WaitGroup
	// Stop and join the swapper however the test ends, before the temporary
	// directories it renames inside are removed.
	t.Cleanup(func() { stop.Store(true); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer done.Store(true)
		for attempt := 0; !stop.Load() && linked < swaps && attempt < 500*swaps; attempt++ {
			if err := os.Rename(sub, keep); err != nil {
				swapErrs++
				continue
			}
			if _, err := makeLink(o, sub); err != nil {
				swapErrs++
				restore(sub, keep)
				continue
			}
			linked++
			// os.Remove removes the link itself, never O's content.
			for j := 0; os.Remove(sub) != nil; j++ {
				if j == 10000 {
					panic("cannot remove the link R/sub")
				}
			}
			restore(sub, keep)
		}
	}()

	counts := map[string]int{}
	target := fs.Ref{Path: filepath.Join(sub, "f")}
	for i := 0; !done.Load() || i == 0; i++ {
		_, err := seam.Write(ctx, target, []byte("x"), fs.NoGuard, p)
		switch code := herr.Kind(err); {
		case err == nil:
			counts["written"]++
		case code == fs.CodeSandboxDenied:
			counts["sandbox-denied"]++
		default:
			t.Errorf("write %d: %v (code %q), want success or %q", i, err, code, fs.CodeSandboxDenied)
		}
		if leaked(t, o) {
			t.Fatalf("write %d: a file appeared under the outside directory O", i)
		}
	}
	wg.Wait()
	if leaked(t, o) {
		t.Fatal("a file appeared under the outside directory O")
	}
	t.Logf("swaps=%d linked=%d swapErrors=%d writes=%v", swaps, linked, swapErrs, counts)
	if linked < swaps {
		t.Fatalf("the swapper made only %d of %d links; the race was not fully exercised", linked, swaps)
	}

	// A write while R/sub is a link to O, with no race, is refused outright.
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if _, err := makeLink(o, sub); err != nil {
		t.Fatal(err)
	}
	_, err = seam.Write(ctx, target, []byte("x"), fs.NoGuard, p)
	if got := herr.Kind(err); got != fs.CodeSandboxDenied {
		t.Fatalf("write through R/sub -> O: %v (code %q), want %q", err, got, fs.CodeSandboxDenied)
	}
	if leaked(t, o) {
		t.Fatal("a file appeared under O")
	}
}

func leaked(t *testing.T, dir string) bool {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return len(es) > 0
}

// restore moves the directory keep back to dir; if dir was made again meanwhile
// (a write re-created it), keep, a real directory, is removed instead.
func restore(dir, keep string) {
	if os.Rename(keep, dir) == nil {
		return
	}
	if fi, err := os.Lstat(keep); err == nil && fi.IsDir() {
		_ = os.RemoveAll(keep)
	}
}
