package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Scenario: Nothing published -- the committed release files equal their copies at the
// story's base commit (testdata/release_base.golden, whose blob IDs are recomputed), no
// dsh release job exists and the default egress set is empty (M2-DOD-7, NG-7).
func TestNothingPublished(t *testing.T) {
	root := filepath.Join("..", "..")
	base, profiles, problems := CheckRelease(root)
	if len(problems) != 0 {
		t.Fatalf("release check: %q", problems)
	}
	if base != "896c7604c220815b7633bb148b2e22e0b65cb85a" {
		t.Errorf("base commit %q, want the parent of the story's first plan commit", base)
	}
	if profiles < len(sendNothingProfiles) {
		t.Errorf("%d default profiles read", profiles)
	}
	var out, errOut bytes.Buffer
	if code := run(t.Context(), []string{"release", "--root", root}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "the default egress set of") {
		t.Errorf("release exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// releaseTree is a copy of the release check's inputs from the repository in a temporary
// root, so each case seeds one fault.
func releaseTree(t *testing.T) string {
	t.Helper()
	repo, root := filepath.Join("..", ".."), t.TempDir()
	for _, rel := range append([]string{ReleaseBasePath, EgressGoldenPath, workflowsDir + "/ci.yml"}, ReleaseFiles...) {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), string(b))
	}
	return root
}

func editReleaseInput(t *testing.T, root, rel string, edit func(string) string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, edit(string(b)))
}

func TestReleaseCheckRefuses(t *testing.T) {
	const dshJob = "\n  dsh:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go -C src/harness build ./cmd/dsh\n"
	for _, tc := range []struct {
		name string
		seed func(t *testing.T, root string)
		want [][]string // each a problem's parts
	}{
		{"dsh release job added", func(t *testing.T, root string) {
			editReleaseInput(t, root, ReleaseFiles[0], func(s string) string { return s + dshJob })
		}, [][]string{{ReleaseFiles[0], "differs from its copy at the base commit 896c760"}, {ReleaseFiles[0], "names dsh", `"dsh:"`}, {ReleaseFiles[0], "names dsh", "cmd/dsh"}}},
		{"goreleaser builds dsh", func(t *testing.T, root string) {
			editReleaseInput(t, root, ReleaseFiles[1], func(s string) string {
				return strings.Replace(s, "builds:\n", "builds:\n  - id: dsh\n    dir: src/harness\n", 1)
			})
		}, [][]string{{ReleaseFiles[1], "differs"}, {ReleaseFiles[1], "names dsh", "id: dsh"}, {ReleaseFiles[1], "names dsh", "src/harness"}}},
		{"another workflow on a tag push", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".github", "workflows", "dsh-release.yml"), "# release dsh\non:\n  push:\n    tags: [\"dsh-v*\"]\njobs:\n  r:\n    steps:\n      - uses: goreleaser/goreleaser-action@v6\n")
		}, [][]string{{"dsh-release.yml:4", "tag push"}, {"dsh-release.yml:8", "goreleaser"}}},
		{"a default profile sends", func(t *testing.T, root string) {
			editReleaseInput(t, root, EgressGoldenPath, func(s string) string {
				return strings.Replace(s, "headless: []", "headless: [model-request https://api.example.test]", 1)
			})
		}, [][]string{{"default profile headless sends", "model-request", "NG-7"}}},
		{"sdk egress set missing", func(t *testing.T, root string) {
			editReleaseInput(t, root, EgressGoldenPath, func(s string) string { return strings.Replace(s, "sdk: []\n", "", 1) })
		}, [][]string{{"no egress set for the default profile sdk"}}},
		{"base copy drifts with the file", func(t *testing.T, root string) {
			for _, rel := range []string{ReleaseBasePath, ReleaseFiles[0]} {
				editReleaseInput(t, root, rel, func(s string) string { return strings.Replace(s, "name: release", "name: release-all", 1) })
			}
		}, [][]string{{ReleaseBasePath, "the copy of " + ReleaseFiles[0], "hashes to blob", "4ab2c446"}}},
		{"release file deleted", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(ReleaseFiles[1]))); err != nil {
				t.Fatal(err)
			}
		}, [][]string{{"release file " + ReleaseFiles[1]}}},
	} {
		root := releaseTree(t)
		if _, _, p := CheckRelease(root); len(p) != 0 {
			t.Fatalf("%s: unseeded tree: %q", tc.name, p)
		}
		tc.seed(t, root)
		_, _, p := CheckRelease(root)
		if len(p) != len(tc.want) {
			t.Errorf("%s: problems %q, want %d", tc.name, p, len(tc.want))
		}
		for _, w := range tc.want {
			if !slices.ContainsFunc(p, func(s string) bool {
				for _, part := range w {
					if !strings.Contains(s, part) {
						return false
					}
				}
				return true
			}) {
				t.Errorf("%s: problems %q name no %q", tc.name, p, w)
			}
		}
		var out bytes.Buffer
		if code := run(t.Context(), []string{"release", "--root", root}, &out, &bytes.Buffer{}); code != 1 || !strings.Contains(out.String(), "FAILED") {
			t.Errorf("%s: release exit %d, stdout %q; want 1 FAILED", tc.name, code, out.String())
		}
	}
}
