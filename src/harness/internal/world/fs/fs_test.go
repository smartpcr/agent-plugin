package fs_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

func TestCodesAreDeclared(t *testing.T) {
	seen := map[herr.Reason]bool{}
	for _, c := range fs.Codes {
		if !c.Valid() || c.Domain() != "fs" || seen[c] {
			t.Errorf("code %q is malformed, outside fs/ or repeated", c)
		}
		seen[c] = true
	}
	for _, c := range []herr.Reason{fs.CodeSandboxDenied, fs.CodeStaleVersion} {
		if !seen[c] {
			t.Errorf("code %q is not listed", c)
		}
	}
}

//dsh:test DH-T-FS-9
func TestFenceTable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	in, out := filepath.Join(root, "a", "f"), filepath.Join(filepath.Dir(root), "wsx", "f")
	mutations := []fs.Op{fs.OpWrite, fs.OpEdit, fs.OpPatch, fs.OpDelete, fs.OpMoveSource, fs.OpMoveDestination}
	pols := map[string]sandbox.Policy{
		"read-only":          sandboxtest.Policy(sandbox.ReadOnly),
		"workspace-write":    sandboxtest.Policy(sandbox.WorkspaceWrite, root),
		"danger-full-access": sandboxtest.Policy(sandbox.DangerFullAccess),
	}
	want := map[string][2]herr.Reason{ // inside, outside
		"read-only":          {fs.CodeSandboxDenied, fs.CodeSandboxDenied},
		"workspace-write":    {"", fs.CodeSandboxDenied},
		"danger-full-access": {"", ""},
	}
	for name, p := range pols {
		for _, op := range mutations {
			for i, path := range []string{in, out} {
				if got := herr.Kind(fs.Fence(p, op, path)); got != want[name][i] {
					t.Errorf("%s %s %s = %q, want %q", name, op, path, got, want[name][i])
				}
			}
		}
		if err := fs.Fence(p, fs.OpRead, out); err != nil {
			t.Errorf("%s read outside: %v", name, err)
		}
	}
	if err := fs.Fence(pols["workspace-write"], fs.OpWrite, root); err != nil {
		t.Errorf("the root itself is writable: %v", err)
	}
}

func TestWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	cases := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "x", "y"), true},
		{filepath.Dir(root), false},
		{root + "x", false},
		{filepath.Join(root, "..", "other"), false},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct {
			path string
			want bool
		}{filepath.Join(root, "X"), true})
	}
	for _, c := range cases {
		if got := fs.Within(root, filepath.Clean(c.path)); got != c.want {
			t.Errorf("Within(%q, %q) = %v, want %v", root, c.path, got, c.want)
		}
	}
}

func TestVersionSameAndGuards(t *testing.T) {
	a := fs.Version{Meta: "m1", Digest: [32]byte{1}, HasDigest: true}
	b := fs.Version{Meta: "m2", Digest: [32]byte{1}, HasDigest: true}
	if !a.Same(b) {
		t.Error("equal digests with different metadata are not Same")
	}
	if (fs.Version{Meta: "m1"}).Same(fs.Version{Meta: "m2"}) || !(fs.Version{Meta: "m1"}).Same(a) {
		t.Error("metadata decides when a digest is missing")
	}
	if (fs.Version{}).Same(fs.Version{}) {
		t.Error("zero versions are Same")
	}
	if err := fs.CheckGuard(fs.CreateIfAbsent, true, a); herr.Kind(err) != fs.CodeAlreadyExists {
		t.Errorf("create-if-absent on existing: %v", err)
	}
	if err := fs.CheckGuard(fs.ReplaceIfVersion{Version: a}, false, fs.Version{}); herr.Kind(err) != fs.CodeStaleVersion {
		t.Errorf("replace on missing: %v", err)
	}
	if err := fs.CheckGuard(fs.ReplaceIfVersion{Version: fs.Version{Meta: "old"}}, true, a); herr.Kind(err) != fs.CodeStaleVersion {
		t.Errorf("replace stale: %v", err)
	}
	if err := fs.CheckGuard(fs.NoGuard, true, a); err != nil {
		t.Errorf("no guard: %v", err)
	}
}
