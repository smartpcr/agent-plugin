//go:build linux

package local_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
)

// TestPlatformChainOrder: the Linux chain is bubblewrap, then Landlock
// (DH-D-SANDBOX-4); both are built-in, and neither can probe without the host,
// so a bare Probe is unavailable rather than a pass.
//
//dsh:test DH-T-SANDBOX-4
func TestPlatformChainOrder(t *testing.T) {
	var names []sandbox.Backend
	for _, b := range local.Platform() {
		names = append(names, b.Name())
		if b.Custom() {
			t.Errorf("%s reports itself custom", b.Name())
		}
		if _, err := b.Probe(context.Background(), "canary"); herr.Kind(err) != sandbox.CodeUnavailable {
			t.Errorf("%s: Probe without the host = %v; want %s", b.Name(), err, sandbox.CodeUnavailable)
		}
	}
	if want := []sandbox.Backend{sandbox.BackendBwrap, sandbox.BackendLandlock}; !slices.Equal(names, want) {
		t.Fatalf("Platform = %v, want %v", names, want)
	}
}

// TestBwrapNotOnPathIsUnavailable: with no bwrap on PATH, bubblewrap fails its
// probe and every start's prefix with sandbox/unavailable, "bwrap not found",
// so the chain moves on to Landlock.
//
//dsh:test DH-T-SANDBOX-4
func TestBwrapNotOnPathIsUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	b, ok := local.Bwrap().(local.HostBackend)
	if !ok {
		t.Fatal("the bubblewrap backend does not probe on the host")
	}
	// The lookup's own reason stays innermost; sandbox/unavailable wraps it.
	if _, err := b.Prefix(local.Host{}, sandbox.Policy{}, ""); !herr.Has(err, sandbox.CodeUnavailable) || !strings.Contains(err.Error(), "bwrap not found") {
		t.Errorf("Prefix = %v; want %s, bwrap not found", err, sandbox.CodeUnavailable)
	}
	if _, err := b.ProbeOn(context.Background(), local.Host{}, ""); !herr.Has(err, sandbox.CodeUnavailable) {
		t.Errorf("ProbeOn = %v; want %s", err, sandbox.CodeUnavailable)
	}
}

// TestRuntimeDirFromEnvironment: the host's runtime directory is
// $XDG_RUNTIME_DIR when it is absolute and clean; otherwise /run/user/<uid>
// when that directory exists, else none.
//
//dsh:test DH-T-SANDBOX-4
func TestRuntimeDirFromEnvironment(t *testing.T) {
	fallback := "/run/user/" + strconv.Itoa(os.Getuid())
	if fi, err := os.Stat(fallback); err != nil || !fi.IsDir() {
		fallback = ""
	}
	dir := t.TempDir()
	for _, tc := range []struct{ env, want string }{
		{dir, dir},
		{"relative/run", fallback},
		{filepath.Dir(dir) + "/./" + filepath.Base(dir), fallback},
		{"", fallback},
	} {
		t.Setenv("XDG_RUNTIME_DIR", tc.env)
		if got := local.ServiceHost(newService(t)).RuntimeDir; got != tc.want {
			t.Errorf("XDG_RUNTIME_DIR=%q: RuntimeDir = %q, want %q", tc.env, got, tc.want)
		}
	}
}
