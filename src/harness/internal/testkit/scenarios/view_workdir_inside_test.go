//go:build scenario

package scenarios

import (
	"path/filepath"
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
			if got := view5Inside(tree, c.path); got != c.inside {
				t.Fatalf("view5Inside(%s, %s) = %v, want %v", tree, c.path, got, c.inside)
			}
		})
	}
}
