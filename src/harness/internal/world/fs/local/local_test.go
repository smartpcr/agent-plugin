package local_test

import (
	"bytes"
	"context"
	"errors"
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

	var done atomic.Bool
	var swapErrs, linked int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer done.Store(true)
		for i := 0; i < swaps; i++ {
			// Directory -> link.
			if err := os.RemoveAll(sub); err != nil {
				swapErrs++
				continue
			}
			if _, err := makeLink(o, sub); err != nil {
				swapErrs++
				_ = os.Mkdir(sub, 0o755)
				continue
			}
			linked++
			// Link -> directory; os.Remove removes the link itself, never O's content.
			if err := os.Remove(sub); err != nil {
				swapErrs++
				continue
			}
			if err := os.Mkdir(sub, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				swapErrs++
			}
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
		case code == fs.CodeNotFound || code == fs.CodeNotDirectory || code == fs.CodeIO || code == fs.CodePermission || code == fs.CodeAlreadyExists:
			// The swapper removed or recreated a component mid-write: a transient
			// failure, never a write outside R.
			counts[string(code)]++
		default:
			t.Errorf("write %d: unexpected error %v (code %q)", i, err, code)
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
	if linked == 0 {
		t.Fatal("the swapper never made a link; the race was not exercised")
	}

	// A write while R/sub is a link to O, with no race, is refused outright.
	_ = os.RemoveAll(sub)
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
