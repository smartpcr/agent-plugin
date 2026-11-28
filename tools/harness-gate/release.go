package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The release check (implementation-plan Stage 9.2; M2-DOD-7, NG-7; tech-spec sec. 4.6:
// nothing built in M2 is enabled for users and no build is published):
//
//	harness-gate release [--root dir]
//
// It reads committed files only:
//
//   - each release file (ReleaseFiles) equals its copy in ReleaseBasePath, the story's
//     base commit, whose git blob ID the golden records and the check recomputes;
//   - no release file names dsh or src/harness, and no other workflow runs on a tag
//     push or a release event or runs goreleaser, so no dsh release job exists;
//   - every default profile's egress set in EgressGoldenPath (computed from the shipped
//     bundles by profile.Egress and held to it by TestDefaultProfilesSendNothing) is
//     empty, the headless and sdk profiles included.
//
// It exits 0 when all hold, 1 naming each problem, 2 on a usage error.

const (
	ReleaseBasePath  = "tools/harness-gate/testdata/release_base.golden"
	EgressGoldenPath = "src/harness/internal/boot/profile/testdata/egress.golden"
	workflowsDir     = ".github/workflows"
)

// ReleaseFiles are the files that publish a build.
var ReleaseFiles = []string{".github/workflows/release.yml", ".goreleaser.yaml"}

// sendNothingProfiles are the default profiles NG-7 names: they send nothing until a
// route is configured.
var sendNothingProfiles = []string{"headless", "sdk"}

var (
	dshPattern        = regexp.MustCompile(`(?i)\bdsh\b|src/harness|cmd/dsh`)
	tagTriggerPattern = regexp.MustCompile(`^\s+(tags|release)\s*:`)
	goreleaserPattern = regexp.MustCompile(`(?i)goreleaser`)
	egressLinePattern = regexp.MustCompile(`^([a-z][a-z0-9-]*): \[(.*)\]$`)
	baseHeaderPattern = regexp.MustCompile(`^# base: ([0-9a-f]{40})$`)
)

// releaseCopy is one file of the base golden.
type releaseCopy struct {
	path, blob string
	content    []byte
}

// parseReleaseBase reads the base golden: "#" header lines (one of them "# base:
// <sha>"), then for each file a "=== <path> <blob>" line and its content.
func parseReleaseBase(src []byte) (base string, copies []releaseCopy, err error) {
	lines := strings.SplitAfter(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var cur *releaseCopy
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(l, "=== "); ok {
			f := strings.Fields(rest)
			if len(f) != 2 {
				return "", nil, fmt.Errorf("%s:%d: want \"=== <path> <git blob ID>\", got %q", ReleaseBasePath, i+1, strings.TrimSpace(l))
			}
			copies = append(copies, releaseCopy{path: f[0], blob: f[1]})
			cur = &copies[len(copies)-1]
			continue
		}
		if cur != nil {
			cur.content = append(cur.content, l...)
			continue
		}
		if m := baseHeaderPattern.FindStringSubmatch(strings.TrimRight(l, "\n")); m != nil {
			base = m[1]
		} else if l != "" && !strings.HasPrefix(l, "#") {
			return "", nil, fmt.Errorf("%s:%d: header line %q is not a comment", ReleaseBasePath, i+1, strings.TrimSpace(l))
		}
	}
	if base == "" {
		return "", nil, fmt.Errorf("%s: no \"# base: <commit SHA>\" line", ReleaseBasePath)
	}
	return base, copies, nil
}

// gitBlobID is the git object ID of content as a blob.
func gitBlobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// firstDifference is the first line where a and b differ, 1-based, with b's line.
func firstDifference(a, b []byte) (int, string) {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := range max(len(al), len(bl)) {
		if i >= len(al) || i >= len(bl) || al[i] != bl[i] {
			if i < len(bl) {
				return i + 1, bl[i]
			}
			return i + 1, "(end of file)"
		}
	}
	return 0, ""
}

func readLF(root, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), err
}

// CheckRelease is the release check over the tree at root; it returns the base commit
// the release files were compared with, the number of default profiles read, and the
// problems found.
func CheckRelease(root string) (base string, profiles int, problems []string) {
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	golden, err := readLF(root, ReleaseBasePath)
	if err != nil {
		add("no base golden: %v", err)
	}
	base, copies, err := parseReleaseBase(golden)
	if golden != nil && err != nil {
		add("%v", err)
	}
	byPath := map[string]releaseCopy{}
	for _, c := range copies {
		if _, twice := byPath[c.path]; twice {
			add("%s: %s is given twice", ReleaseBasePath, c.path)
		}
		byPath[c.path] = c
		if !slices.Contains(ReleaseFiles, c.path) {
			add("%s: %s is not a release file (%s)", ReleaseBasePath, c.path, strings.Join(ReleaseFiles, ", "))
		}
		if got := gitBlobID(c.content); got != c.blob {
			add("%s: the copy of %s hashes to blob %s, not %s, its blob at the base commit", ReleaseBasePath, c.path, got, c.blob)
		}
	}
	for _, rel := range ReleaseFiles {
		cur, err := readLF(root, rel)
		if err != nil {
			add("release file %s: %v", rel, err)
			continue
		}
		if c, ok := byPath[rel]; !ok {
			add("%s has no copy of %s", ReleaseBasePath, rel)
		} else if !bytes.Equal(cur, c.content) {
			n, line := firstDifference(c.content, cur)
			add("%s differs from its copy at the base commit %s: line %d reads %q (M2-DOD-7)", rel, short(base), n, line)
		}
		for i, l := range strings.Split(string(cur), "\n") {
			if dshPattern.MatchString(l) {
				add("%s:%d names dsh: a dsh release job or build (M2-DOD-7): %q", rel, i+1, strings.TrimSpace(l))
			}
		}
	}
	workflows, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(workflowsDir), "*.y*ml"))
	if err != nil {
		add("%s: %v", workflowsDir, err)
	}
	for _, path := range workflows {
		rel := workflowsDir + "/" + filepath.Base(path)
		if slices.Contains(ReleaseFiles, rel) {
			continue
		}
		cur, err := readLF(root, rel)
		if err != nil {
			add("%s: %v", rel, err)
			continue
		}
		for i, l := range strings.Split(string(cur), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if tagTriggerPattern.MatchString(l) || goreleaserPattern.MatchString(l) {
				add("%s:%d: only %s may run on a tag push or a release, or run goreleaser (M2-DOD-7): %q", rel, i+1, ReleaseFiles[0], strings.TrimSpace(l))
			}
		}
	}

	egress, err := readLF(root, EgressGoldenPath)
	if err != nil {
		add("no default egress set: %v", err)
		return base, 0, problems
	}
	seen := map[string]bool{}
	for i, l := range strings.Split(string(egress), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m := egressLinePattern.FindStringSubmatch(l)
		if m == nil {
			add("%s:%d: want \"<profile>: [<kind> <endpoint>, ...]\", got %q", EgressGoldenPath, i+1, l)
			continue
		}
		seen[m[1]] = true
		if strings.TrimSpace(m[2]) != "" {
			add("%s:%d: default profile %s sends %s; the default egress set must be empty (NG-7)", EgressGoldenPath, i+1, m[1], m[2])
		}
	}
	for _, p := range sendNothingProfiles {
		if !seen[p] {
			add("%s has no egress set for the default profile %s (NG-7)", EgressGoldenPath, p)
		}
	}
	return base, len(seen), problems
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// runRelease is "harness-gate release".
func runRelease(args []string, stdout, stderr io.Writer) int {
	root, ok := subcommandFlags("release", args, stderr, nil)
	if !ok {
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate release: %v\n", err)
		return 2
	}
	base, profiles, problems := CheckRelease(abs)
	if len(problems) > 0 {
		printBlockers(stdout, "harness-gate release: FAILED", problems)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate release: %s equal their copies at the base commit %s; no dsh release job; the default egress set of %d profiles is empty\n",
		strings.Join(ReleaseFiles, " and "), short(base), profiles)
	return 0
}
