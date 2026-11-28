//go:build scenario

package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestView5InsideJudgesWritePaths checks VIEW-5's own containment check of a
// model-chosen write path against the session's working directory: relative paths
// resolve against it, and a sibling sharing its name as a prefix or a path that
// climbs out with ".." is outside.
func TestView5InsideJudgesWritePaths(t *testing.T) {
	scratch := t.TempDir()
	tree := filepath.Join(scratch, "tree")
	cases := []struct {
		name, path string
		inside     bool
	}{
		{"relative file", "view5-relative.txt", true},
		{"relative nested file", filepath.Join("a", "b.txt"), true},
		{"absolute file below", filepath.Join(tree, "x.txt"), true},
		{"the directory itself", tree, true},
		{"relative climb out", filepath.Join("..", "repo", "x.txt"), false},
		{"absolute sibling", filepath.Join(scratch, "repo", "x.txt"), false},
		{"sibling sharing the name as a prefix", filepath.Join(scratch, "tree2", "x.txt"), false},
		{"dot-dot named file inside", "..x.txt", true},
		{"climb back in", filepath.Join("..", "tree", "x.txt"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := view5MustInside(t, tree, c.path); got != c.inside {
				t.Fatalf("view5Inside(%s, %s) = %v, want %v", tree, c.path, got, c.inside)
			}
		})
	}
}

// TestView5InsideFollowsLinks checks that a write path is judged where it lands:
// a link inside the working directory that points out of it, followed as the
// last component, as a parent or dangling, is outside, and one that stays in is
// inside.
func TestView5InsideFollowsLinks(t *testing.T) {
	scratch := t.TempDir()
	repo, tree := filepath.Join(scratch, "repo"), filepath.Join(scratch, "tree")
	for _, d := range []string{filepath.Join(repo, "sub"), filepath.Join(tree, "in")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"out":      repo,
		"outfile":  filepath.Join(repo, "file.txt"),
		"dangling": filepath.Join(repo, "new.txt"),
		"relout":   filepath.Join("..", "repo"),
		"stay":     "in",
		"loopa":    "loopb",
		"loopb":    "loopa",
		"self":     filepath.Join(tree, "self"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(tree, name)); err != nil {
			if runtime.GOOS != "windows" {
				t.Fatalf("symbolic link: %v", err)
			}
			t.Skipf("this Windows host cannot create symbolic links (the check runs on Linux, as VIEW-5 does): %v", err)
		}
	}
	cases := []struct {
		name, path string
		inside     bool
	}{
		{"through an outward directory link", filepath.Join("out", "x.txt"), false},
		{"an outward link to an existing file", "outfile", false},
		{"a dangling outward link", "dangling", false},
		{"a relative outward link", filepath.Join("relout", "sub", "y.txt"), false},
		{"climbing from an outward link", filepath.Join("out", "..", "tree", "x.txt"), true},
		{"climbing past an outward link", filepath.Join("out", "sub", "..", "x.txt"), false},
		{"an inward link", filepath.Join("stay", "x.txt"), true},
		{"a missing directory inside", filepath.Join("missing", "deeper", "x.txt"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := view5MustInside(t, tree, c.path); got != c.inside {
				at, _ := view5Real(filepath.Join(tree, c.path))
				t.Fatalf("view5Inside(%s, %s) = %v (lands at %s), want %v", tree, c.path, got, at, c.inside)
			}
		})
	}
	via := filepath.Join(scratch, "via")
	if err := os.Symlink(tree, via); err != nil {
		t.Fatal(err)
	}
	if !view5MustInside(t, via, filepath.Join(tree, "x.txt")) || view5MustInside(t, via, filepath.Join(via, "out", "x.txt")) {
		t.Fatal("a working directory named through a link is not judged by where it lands")
	}

	// A cycle, or a link that cannot be looked at, leaves the landing place
	// unknown: an error, never an inside path by its spelling.
	unresolved := []string{filepath.Join("loopa", "x.txt"), "loopb", filepath.Join("self", "x.txt")}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(tree, "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(repo, filepath.Join(locked, "esc")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		unresolved = append(unresolved, filepath.Join("locked", "esc", "x.txt"))
	}
	for _, p := range unresolved {
		if in, err := view5Inside(tree, p); err == nil {
			t.Errorf("view5Inside(%s, %s) = %v with no error, want a resolution error", tree, p, in)
		}
	}
}

// view5MustInside is view5Inside failing the test on a resolution error.
func view5MustInside(t *testing.T, dir, p string) bool {
	t.Helper()
	in, err := view5Inside(dir, p)
	if err != nil {
		t.Fatalf("view5Inside(%s, %s): %v", dir, p, err)
	}
	return in
}

// TestView5OutsideWatchesTheInactiveDirectory checks that the listing of a
// phase leaves out only its working directory and the named skips: a change in
// the other of R and T shows, one in the active directory does not.
func TestView5OutsideWatchesTheInactiveDirectory(t *testing.T) {
	scratch := t.TempDir()
	repo, tree := filepath.Join(scratch, "repo"), filepath.Join(scratch, "tree")
	admin := filepath.Join("repo", ".git", "worktrees", "tree")
	for _, d := range []string{filepath.Join(scratch, admin), tree, filepath.Join(scratch, "outside")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, "README.md"), "r\n")
	write(filepath.Join(tree, "README.md"), "t\n")
	write(filepath.Join(scratch, "outside", "keep.txt"), "o\n")
	cases := []struct {
		name, active, change string
		skip                 []string
		want                 []string
	}{
		{"before the move, the worktree is watched", repo, filepath.Join(tree, "README.md"), nil, []string{"changed tree/README.md"}},
		{"before the move, the project is not", repo, filepath.Join(repo, "README.md"), nil, nil},
		{"after the move, the project is watched", tree, filepath.Join(repo, "README.md"), []string{admin}, []string{"changed repo/README.md"}},
		{"after the move, a new project file shows", tree, filepath.Join(repo, "view5-original.txt"), []string{admin}, []string{"created repo/view5-original.txt"}},
		{"after the move, the worktree's git administration is not", tree, filepath.Join(scratch, admin, "index"), []string{admin}, nil},
		{"after the move, the worktree is not", tree, filepath.Join(tree, "README.md"), []string{admin}, nil},
		{"with no working directory, everything is watched", "", filepath.Join(repo, "README.md"), nil, []string{"changed repo/README.md"}},
		{"outside is always watched", tree, filepath.Join(scratch, "outside", "keep.txt"), []string{admin}, []string{"changed outside/keep.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := view5Outside(t, scratch, c.active, c.skip...)
			old, _ := os.ReadFile(c.change)
			write(c.change, string(old)+"changed\n")
			t.Cleanup(func() {
				if old == nil {
					_ = os.Remove(c.change)
				} else {
					write(c.change, string(old))
				}
			})
			if got := listingDiff(before, view5Outside(t, scratch, c.active, c.skip...)); !slices.Equal(got, c.want) {
				t.Fatalf("diff %v, want %v", got, c.want)
			}
		})
	}
}

// TestView5WrittenCountsPartialPatches checks which paths a call is taken to
// have written: a failed apply_patch keeps the mutations its report completed
// and a move destination written before its source could not be removed, but
// not the paths it failed on or never executed; a refused write wrote nothing.
func TestView5WrittenCountsPartialPatches(t *testing.T) {
	result := func(text string, isError bool) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"call_id": "c1", "is_error": isError, "message": map[string]any{
			"blocks": []any{map[string]any{"kind": "tool_result", "tool_result": map[string]any{
				"call_id": "c1", "is_error": isError, "parts": []any{map[string]any{"text": map[string]any{"text": text}}}}}}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	args := func(v any) json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	partial := strings.Join([]string{
		"fs/io: The patch stopped part-way; nothing was rolled back.",
		"Completed:",
		"A /scratch/repo/new.txt (version 3f2a)",
		"M /scratch/tree/a.txt (version 9c01) (not durable)",
		"D /scratch/tree/gone.txt",
		"R /scratch/tree/old.txt -> /scratch/repo/moved.txt (version 77aa)",
		"Failed:",
		"Update File b.txt (move to ../repo/b.txt): fs/io: /scratch/repo/b.txt was written (version 1b) but the source could not be removed: fs/io: busy",
		"Not executed:",
		"Add File ../repo/never.txt",
		"Re-read the files before patching again.",
	}, "\n")
	denied := strings.Join([]string{
		"fs/sandbox-denied: The patch stopped part-way; nothing was rolled back.",
		"Failed:",
		"Add File ../repo/denied.txt: fs/sandbox-denied: outside the write roots",
		"Not executed:",
		"Add File later.txt",
		"Re-read the files before patching again.",
	}, "\n")
	patch := "*** Begin Patch\n*** Add File: a.txt\n+a\n*** Update File: b.txt\n*** Move to: c.txt\n@@\n-b\n+c\n*** End Patch"
	cases := []struct {
		name string
		o    toolOutcome
		want []string
	}{
		{"a part-way patch", toolOutcome{Name: "apply_patch", IsError: true, Raw: result(partial, true)},
			[]string{"/scratch/repo/new.txt", "/scratch/tree/a.txt", "/scratch/tree/gone.txt", "/scratch/tree/old.txt", "/scratch/repo/moved.txt", "../repo/b.txt"}},
		{"a patch refused on its first hunk", toolOutcome{Name: "apply_patch", IsError: true, Raw: result(denied, true)}, nil},
		{"a successful patch", toolOutcome{Name: "apply_patch", Args: args(map[string]string{"input": patch}), Raw: result("Success.", false)},
			[]string{"a.txt", "b.txt", "c.txt"}},
		{"a refused write", toolOutcome{Name: "write", IsError: true, Args: args(map[string]string{"file_path": "../repo/x.txt"}), Raw: result("fs/sandbox-denied: no", true)}, nil},
		{"a successful edit", toolOutcome{Name: "edit", Args: args(map[string]string{"file_path": "x.txt"}), Raw: result("ok", false)}, []string{"x.txt"}},
		{"a read", toolOutcome{Name: "read", Args: args(map[string]string{"file_path": "/etc/passwd"}), Raw: result("ok", false)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := view5Written(t, &c.o); !slices.Equal(got, c.want) {
				t.Fatalf("view5Written = %q, want %q", got, c.want)
			}
		})
	}
	dir := t.TempDir()
	o := toolOutcome{Name: "apply_patch", IsError: true, Raw: result(strings.ReplaceAll(partial, "/scratch/repo/", filepath.Join(filepath.Dir(dir), "elsewhere")+string(filepath.Separator)), true)}
	outside := 0
	for _, p := range view5Written(t, &o) {
		if !view5MustInside(t, dir, p) {
			outside++
		}
	}
	if outside == 0 {
		t.Fatal("no completed mutation of the part-way patch was judged outside the working directory")
	}
}
