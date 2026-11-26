//go:build windows

package local_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// unresolvable is an absolute, clean path that cannot be resolved for a reason
// other than not existing: a name Windows refuses as syntactically invalid.
func unresolvable(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "a<b")
}

// TestRootOnAnotherVolume: a write root on a volume with no path relative to the
// canary directory, the harness home or the binary is below none of them; it is
// judged by its (absent) volume root and prepared as written.
//
//dsh:test DH-T-SANDBOX-13
func TestRootOnAnotherVolume(t *testing.T) {
	var root string
	for c := 'Z'; c >= 'D'; c-- {
		if _, err := os.Stat(string(c) + `:\`); err != nil {
			root = string(c) + `:\dsh\root`
			break
		}
	}
	if root == "" {
		t.Skip("every drive letter from D to Z is in use")
	}
	s, err := local.New(context.Background(), local.Config{Home: t.TempDir()}, []local.Backend{passing(sandbox.BackendBwrap, sandbox.Effects...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	plan, _, err := local.PreparedPrefix(context.Background(), s, sandboxtest.Policy(sandbox.WorkspaceWrite, root), command(t))
	if err != nil || !slices.Equal(plan.Write, []string{root}) {
		t.Fatalf("Prepare of a root on another volume = %v (write %q), %v; want it prepared with write [%s]", plan, plan.Write, err, root)
	}
}
