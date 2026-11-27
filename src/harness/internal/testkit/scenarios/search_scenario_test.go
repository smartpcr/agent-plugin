//go:build scenario

package scenarios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/util/search until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/search"
)

// searchSet is the fixed set of searches of DH-E-RIPGREP-1: plain words and
// regular expressions (Go RE2, which rg also accepts).
var searchSet = []string{
	`TODO`,
	`func`,
	`license`,
	`return nil`,
	`(?i)error`,
	`^import\s`,
	`[A-Z][a-z]+Error\b`,
	`\b\d{4}-\d{2}-\d{2}\b`,
	`https?://[^\s"']+`,
	`(foo|bar|baz)\w*\(`,
}

// TestSearchMeetsBudget: in-process search meets the agreed budget on real
// checkouts (libs/vscode-ripgrep.md DH-E-RIPGREP-1; DH-LIB-RIPGREP-R4; DH-D-FS-3).
// Executed in DH-S-18 (step 18.7) on Linux, macOS and Windows.
//
// Given real checkouts of a small, a medium and a very large public repository and
// an rg installed for comparison only, when the fixed set of plain-word and regular
// expression searches runs through the harness search and through rg, then every
// search meets the agreed budget and is not cut short, and the ratio to rg is
// recorded per search. An rg run that fails or reports a problem fails that search's
// comparison rather than counting as zero matches. Inputs: DSH_SCENARIO_REPOS, the ";"-separated checkouts;
// DSH_SCENARIO_SEARCH_BUDGET, the agreed budget per search as a Go duration;
// DSH_SCENARIO_REPORT, the file the timing report (JSON lines) is written to.
// Cleanup: removing the checkouts is the operator's; the report is kept.
//
//dsh:scenario DH-E-RIPGREP-1
func TestSearchMeetsBudget(t *testing.T) {
	repos := needList(t, "DSH_SCENARIO_REPOS", "real checkouts: small, medium and very large, ';'-separated")
	budget, err := time.ParseDuration(need(t, "DSH_SCENARIO_SEARCH_BUDGET", "the agreed budget per search, e.g. 2s"))
	if err != nil {
		t.Fatal(err)
	}
	reportPath := need(t, "DSH_SCENARIO_REPORT", "file the timing report is written to")
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatalf("rg is needed for the comparison: %v", err)
	}
	report, err := os.Create(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = report.Close() })
	enc := json.NewEncoder(report)

	for _, repo := range repos {
		src, err := search.OpenDir(repo)
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		t.Cleanup(func() { _ = src.Close() })
		for _, pattern := range searchSet {
			ctx, cancel := context.WithTimeout(t.Context(), 10*budget)
			start := time.Now()
			matches := 0
			summary, err := search.Grep(ctx, search.GrepQuery{From: src, Pattern: pattern, Output: search.OutputFiles,
				Limits: search.Limits{MaxMatches: 1 << 30}}, func(search.Match) bool { matches++; return true })
			took := time.Since(start)
			cancel()
			if err != nil {
				t.Errorf("%s %q: %v", repo, pattern, err)
				continue
			}
			rgMatches, rgTook, err := runRG(t, rg, repo, pattern)
			if err != nil {
				t.Errorf("%s %q: the rg comparison did not run: %v", repo, pattern, err)
				continue
			}
			ratio := took.Seconds() / max(rgTook.Seconds(), 1e-6)
			_ = enc.Encode(map[string]any{"repo": repo, "pattern": pattern, "harness_ms": took.Milliseconds(),
				"rg_ms": rgTook.Milliseconds(), "ratio": ratio, "files": matches, "rg_files": rgMatches, "visited": summary.Files})
			t.Logf("%s %q: %v (rg %v, ratio %.2f), %d files (rg %d)", repo, pattern, took, rgTook, ratio, matches, rgMatches)
			if took > budget {
				t.Errorf("%s %q took %v, over the budget %v", repo, pattern, took, budget)
			}
			if summary.Incomplete {
				t.Errorf("%s %q was cut short: %s", repo, pattern, summary.Reason)
			}
		}
	}
}

// TestFileListingMatchesGit: the file listing with ignore rules equals Git's view
// of the checkout (libs/vscode-ripgrep.md DH-E-RIPGREP-2; DH-LIB-RIPGREP-R1;
// DH-D-FS-3). Executed in DH-S-18 (step 18.7); also the manual check of DH-S-4.
//
// Given the same real checkouts with Git installed, when the harness lists files
// with ignore rules on, then the set equals `git ls-files --cached --others
// --exclude-standard` minus hidden files. Every difference is printed for
// triage; an explained one is listed, one slash path per line, in the file named by
// DSH_SCENARIO_LISTING_EXPLAINED (optional). Inputs: DSH_SCENARIO_REPOS. Cleanup:
// none beyond removing the checkouts.
//
//dsh:scenario DH-E-RIPGREP-2
func TestFileListingMatchesGit(t *testing.T) {
	repos := needList(t, "DSH_SCENARIO_REPOS", "real Git checkouts, ';'-separated")
	explained := map[string]bool{}
	if path := os.Getenv("DSH_SCENARIO_LISTING_EXPLAINED"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				explained[line] = true
			}
		}
	}
	for _, repo := range repos {
		src, err := search.OpenDir(repo)
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		t.Cleanup(func() { _ = src.Close() })
		listed, summary, err := search.List(t.Context(), search.FilesQuery{From: src, Limits: search.Limits{MaxFiles: 10_000_000}})
		if err != nil || summary.Incomplete {
			t.Fatalf("%s: listing: %v (%s)", repo, err, summary.Reason)
		}
		ours := map[string]bool{}
		for _, p := range listed {
			ours[filepath.ToSlash(p)] = true
		}

		cmd := exec.CommandContext(t.Context(), "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: git ls-files: %v", repo, err)
		}
		git := map[string]bool{}
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		sc.Split(splitNUL)
		for sc.Scan() {
			p := sc.Text()
			if hidden(p) {
				continue
			}
			if info, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(p))); err != nil || info.IsDir() {
				continue // deleted in the work tree, or a submodule
			}
			git[p] = true
		}

		var diff []string
		for p := range ours {
			if !git[p] && !explained[p] {
				diff = append(diff, "+ "+p)
			}
		}
		for p := range git {
			if !ours[p] && !explained[p] {
				diff = append(diff, "- "+p)
			}
		}
		slices.Sort(diff)
		if len(diff) > 0 {
			t.Errorf("%s: %d unexplained differences (+ listed by the harness only, - by Git only):\n%s",
				repo, len(diff), strings.Join(diff, "\n"))
		}
	}
}

// runRG runs rg over repo for pattern and returns the number of files it matched
// and how long it took. rg exits 0 with matches and 1 with none; any other outcome
// (exit 2 for a bad pattern or an unreadable path, a signal, a timeout, a missing
// binary), output on its standard error, or exit 1 with output, means the
// comparison did not run correctly and is an error, never a zero.
func runRG(t *testing.T, rg, repo, pattern string) (int, time.Duration, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), rg, "--files-with-matches", "--", pattern, ".")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	took := time.Since(start)
	files := strings.Count(stdout.String(), "\n")
	var exit *exec.ExitError
	switch {
	case err == nil && files > 0:
	case errors.As(err, &exit) && exit.ExitCode() == 1 && files == 0:
	case err == nil:
		return 0, took, errors.New("rg exited 0 with no files listed")
	default:
		return 0, took, fmt.Errorf("rg: %v (%d files listed)\n%s", err, files, stderr.String())
	}
	if stderr.Len() > 0 {
		return 0, took, fmt.Errorf("rg reported problems, so its count is not comparable:\n%s", stderr.String())
	}
	return files, took, nil
}

// hidden reports whether a slash path has a segment starting with a dot.
func hidden(p string) bool {
	return slices.ContainsFunc(strings.Split(p, "/"), func(s string) bool { return strings.HasPrefix(s, ".") })
}

func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
