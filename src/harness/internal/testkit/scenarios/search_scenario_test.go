//go:build scenario

package scenarios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// firstMatches is how many matching lines the budget is measured to: the grep
// tool's default MaxMatches, the "first 250 matches" of the agreed budget
// (libs/vscode-ripgrep.md sec. 7; e2e-scenarios.md, RIPGREP-1).
const firstMatches = 250

// unbounded lifts every bound of a search but the deadline, so a very large
// checkout (past the default 100,000 files or 1 GiB) is searched whole.
var unbounded = search.Limits{MaxMatches: -1, MaxFiles: -1, MaxBytes: -1}

// TestSearchMeetsBudget: in-process search meets the agreed budget on real
// checkouts (libs/vscode-ripgrep.md DH-E-RIPGREP-1; DH-LIB-RIPGREP-R4; DH-D-FS-3).
// Executed in DH-S-18 (step 18.7) on Linux, macOS and Windows.
//
// Given real checkouts of a small, a medium and a very large public repository and
// an rg installed for comparison only, when the fixed set of plain-word and regular
// expression searches runs through the harness search and through rg, then every
// search returns its first 250 matching lines (or, with fewer, all of them) within
// the agreed budget, not cut short by anything but that count; the whole search,
// unbounded, lists the same matching files as rg; and the ratio of the whole
// search's time to rg's is recorded per search. No rg is started by the harness: a
// decoy rg first on PATH and a decoy RIPGREP_CONFIG_PATH must stay unused, and the
// comparison runs the real rg by its path with --no-config. An rg run that fails
// or reports a problem fails that search's comparison rather than counting as zero
// matches. Inputs: DSH_SCENARIO_REPOS, the ";"-separated checkouts;
// DSH_SCENARIO_SEARCH_BUDGET, the agreed budget per search as a Go duration;
// DSH_SCENARIO_REPORT, the file the timing report (JSON lines) is written to;
// DSH_SCENARIO_WHOLE_SEARCH_TIMEOUT (optional, default 100 budgets), the deadline
// of each whole, unbounded search: one that passes it fails with its repo and
// pattern, gets a "timed_out" line in the report, and the rest of the set runs.
// Cleanup: removing the checkouts is the operator's; the report is kept.
//
//dsh:scenario DH-E-RIPGREP-1
func TestSearchMeetsBudget(t *testing.T) {
	repos := needList(t, "DSH_SCENARIO_REPOS", "real checkouts: small, medium and very large, ';'-separated")
	budget, err := time.ParseDuration(need(t, "DSH_SCENARIO_SEARCH_BUDGET", "the agreed budget per search, e.g. 3s"))
	if err != nil {
		t.Fatal(err)
	}
	reportPath := need(t, "DSH_SCENARIO_REPORT", "file the timing report is written to")
	wholeTimeout := wholeSearchFactor * budget
	if v := os.Getenv("DSH_SCENARIO_WHOLE_SEARCH_TIMEOUT"); v != "" {
		if wholeTimeout, err = time.ParseDuration(v); err != nil || wholeTimeout <= 0 {
			t.Fatalf("DSH_SCENARIO_WHOLE_SEARCH_TIMEOUT %q: want a positive Go duration (%v)", v, err)
		}
	}
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatalf("rg is needed for the comparison: %v", err)
	}
	spawned := decoyRG(t)
	report, err := os.Create(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	timings := newTimingReport(report)
	t.Cleanup(func() {
		if err := timings.close(); err != nil {
			t.Errorf("the timing report %s is incomplete: %v", reportPath, err)
		}
	})

	for _, repo := range repos {
		src, err := search.OpenDir(repo)
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		t.Cleanup(func() { _ = src.Close() })
		for _, pattern := range searchSet {
			first, lines, err := timeFirstMatches(t, src, pattern, budget)
			if err != nil {
				t.Errorf("%s %q: first %d matches: %v", repo, pattern, firstMatches, err)
				continue
			}
			files, summary, took, timedOut, err := wholeSearch(t, src, pattern, wholeTimeout)
			if timedOut {
				if rerr := timings.add(map[string]any{"repo": repo, "pattern": pattern, "budget_ms": budget.Milliseconds(),
					"first_ms": first.Milliseconds(), "first_lines": lines, "harness_ms": took.Milliseconds(),
					"timed_out": true, "timeout_ms": wholeTimeout.Milliseconds(), "files": len(files), "visited": summary.Files}); rerr != nil {
					t.Errorf("%s %q: the timing report %s: %v", repo, pattern, reportPath, rerr)
				}
				t.Errorf("%s %q: whole search timed out after %v (DSH_SCENARIO_WHOLE_SEARCH_TIMEOUT %v), %d files visited: %v",
					repo, pattern, took, wholeTimeout, summary.Files, err)
				continue
			}
			if err != nil {
				t.Errorf("%s %q: whole search: %v", repo, pattern, err)
				continue
			}
			rgFiles, rgTook, err := runRG(t, rg, repo, pattern)
			if err != nil {
				t.Errorf("%s %q: the rg comparison did not run: %v", repo, pattern, err)
				continue
			}
			ratio := took.Seconds() / max(rgTook.Seconds(), 1e-6)
			if err := timings.add(map[string]any{"repo": repo, "pattern": pattern, "budget_ms": budget.Milliseconds(),
				"first_ms": first.Milliseconds(), "first_lines": lines, "harness_ms": took.Milliseconds(),
				"rg_ms": rgTook.Milliseconds(), "ratio": ratio, "files": len(files), "rg_files": len(rgFiles), "visited": summary.Files}); err != nil {
				t.Errorf("%s %q: the timing report %s: %v", repo, pattern, reportPath, err)
			}
			t.Logf("%s %q: first %d lines in %v (budget %v); whole %v (rg %v, ratio %.2f), %d files (rg %d)",
				repo, pattern, lines, first, budget, took, rgTook, ratio, len(files), len(rgFiles))
			if first > budget {
				t.Errorf("%s %q: the first %d lines took %v, over the budget %v", repo, pattern, lines, first, budget)
			}
			if diff := setDiff(files, rgFiles, nil); len(diff) > 0 {
				t.Errorf("%s %q: %d files differ from rg's (+ matched by the harness only, - by rg only):\n%s",
					repo, pattern, len(diff), strings.Join(diff, "\n"))
			}
		}
	}
	if spawned() {
		t.Errorf("an rg was started: the decoy rg first on PATH ran during the harness searches")
	}
}

// wholeSearchFactor sets the default deadline of one whole, unbounded search to
// this many budgets; DSH_SCENARIO_WHOLE_SEARCH_TIMEOUT overrides it.
const wholeSearchFactor = 100

// wholeSearch runs the whole, unbounded files search for pattern under timeout and
// returns the matched slash paths, the summary and the time taken. timedOut
// reports that the deadline, not the search, ended it, so a stalled search fails
// on its own and the rest of the set still runs. Any other cut is an error.
func wholeSearch(t *testing.T, src search.Source, pattern string, timeout time.Duration) (map[string]bool, search.Summary, time.Duration, bool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	files := map[string]bool{}
	start := time.Now()
	summary, err := search.Grep(ctx, search.GrepQuery{From: src, Pattern: pattern, Output: search.OutputFiles,
		Limits: unbounded}, func(m search.Match) bool { files[filepath.ToSlash(m.Path)] = true; return true })
	took := time.Since(start)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && t.Context().Err() == nil {
		if err == nil {
			err = fmt.Errorf("cut short: %s", summary.Reason)
		}
		return files, summary, took, true, err
	}
	if err == nil && summary.Incomplete {
		err = fmt.Errorf("cut short: %s", summary.Reason)
	}
	return files, summary, took, false, err
}

// timeFirstMatches times a content search for pattern until its first
// firstMatches matching lines are out, or until the whole of src is searched when
// it has fewer; it returns the time and the lines. A search cut short by anything
// but that count (a deadline of ten budgets, an unreadable directory) is an error.
func timeFirstMatches(t *testing.T, src search.Source, pattern string, budget time.Duration) (time.Duration, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*budget)
	defer cancel()
	lim := unbounded
	lim.MaxMatches = firstMatches
	lines := 0
	start := time.Now()
	summary, err := search.Grep(ctx, search.GrepQuery{From: src, Pattern: pattern, Output: search.OutputLines, Limits: lim},
		func(search.Match) bool { lines++; return true })
	took := time.Since(start)
	if err == nil && summary.Incomplete && summary.Reason != search.CutMatches {
		err = fmt.Errorf("cut short: %s", summary.Reason)
	}
	return took, lines, err
}

// decoyRG puts a decoy rg first on PATH and points RIPGREP_CONFIG_PATH at a decoy
// configuration that would run it as a preprocessor, for the rest of the test. The
// returned func reports whether the decoy ever ran.
func decoyRG(t *testing.T) func() bool {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "rg-started")
	name, body := "rg", "#!/bin/sh\necho started >> '"+marker+"'\nexit 2\n"
	if runtime.GOOS == "windows" {
		name, body = "rg.cmd", "@echo off\r\necho started>> \""+marker+"\"\r\nexit /b 2\r\n"
	}
	decoy := filepath.Join(dir, name)
	if err := os.WriteFile(decoy, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "ripgreprc")
	if err := os.WriteFile(config, []byte("--pre="+decoy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RIPGREP_CONFIG_PATH", config)
	if found, err := exec.LookPath("rg"); err != nil || !strings.EqualFold(found, decoy) {
		t.Fatalf("the decoy rg is not first on PATH: %q, %v", found, err)
	}
	return func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
}

// setDiff lists, sorted, each path of ours missing from theirs ("+ path") and of
// theirs missing from ours ("- path"), leaving out the explained ones.
func setDiff(ours, theirs, explained map[string]bool) []string {
	var diff []string
	for p := range ours {
		if !theirs[p] && !explained[p] {
			diff = append(diff, "+ "+p)
		}
	}
	for p := range theirs {
		if !ours[p] && !explained[p] {
			diff = append(diff, "- "+p)
		}
	}
	slices.Sort(diff)
	return diff
}

// TestFileListingMatchesGit: the file listing with ignore rules equals Git's view
// of the checkout (libs/vscode-ripgrep.md DH-E-RIPGREP-2; DH-LIB-RIPGREP-R1;
// DH-D-FS-3). Executed in DH-S-18 (step 18.7); also the manual check of DH-S-4.
//
// Given the same real checkouts with Git installed, when the harness lists files
// with ignore rules on, then the set equals `git ls-files --cached --others
// --exclude-standard` minus hidden files. Every difference is printed for
// triage; an explained one is listed, one slash path per line, in the file named by
// DSH_SCENARIO_LISTING_EXPLAINED (optional). A tracked file an ignore rule matches
// (`git ls-files --cached --ignored --exclude-standard`) is explained by Git itself
// and logged: Git lists what it tracks, the harness follows the ignore files.
// Inputs: DSH_SCENARIO_REPOS. Cleanup: none beyond removing the checkouts.
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

		git := map[string]bool{}
		for _, p := range gitFiles(t, repo, "--cached", "--others", "--exclude-standard") {
			if hidden(p) {
				continue
			}
			if info, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(p))); err != nil || info.IsDir() {
				continue // deleted in the work tree, or a submodule
			}
			git[p] = true
		}
		// A tracked file an ignore rule matches is listed by Git, which never
		// ignores what it tracks; the harness reads the ignore files, not Git's
		// index (DH-LIB-RIPGREP-R1, libs/vscode-ripgrep.md sec. 6 "Ignore rules"),
		// so it leaves the file out. Git names each one itself.
		why := maps.Clone(explained)
		for _, p := range gitFiles(t, repo, "--cached", "--ignored", "--exclude-standard") {
			if git[p] && !ours[p] && !why[p] {
				why[p] = true
				t.Logf("%s: explained: %s is tracked though an ignore rule matches it", repo, p)
			}
		}

		if diff := setDiff(ours, git, why); len(diff) > 0 {
			t.Errorf("%s: %d unexplained differences (+ listed by the harness only, - by Git only):\n%s",
				repo, len(diff), strings.Join(diff, "\n"))
		}
	}
}

// gitFiles runs git ls-files -z with args in repo and returns the slash paths.
func gitFiles(t *testing.T, repo string, args ...string) []string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append(append([]string{"ls-files"}, args...), "-z")...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: git ls-files %s: %v", repo, strings.Join(args, " "), err)
	}
	var paths []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Split(splitNUL)
	for sc.Scan() {
		paths = append(paths, sc.Text())
	}
	return paths
}

// runRG runs rg over repo for pattern and returns the slash paths, from repo, of
// the files it matched and how long it took. --no-config and --no-ignore-global
// keep rg to what the harness reads: no configuration file (RIPGREP_CONFIG_PATH is
// the decoy's) and no user-wide ignore file (DH-LIB-RIPGREP-R5). --follow makes rg
// read a linked file as the harness does through os.Root (operator decision
// ripgrep1-symlinked-files); a link rg cannot follow is reported on its standard
// error and fails the comparison below rather than passing unseen. rg exits 0 with
// matches and 1 with none; any other outcome (exit 2 for a bad pattern or an
// unreadable path, a signal, a timeout, a missing binary), output on its standard
// error, or exit 1 with output, means the comparison did not run correctly and is
// an error, never a zero.
func runRG(t *testing.T, rg, repo, pattern string) (map[string]bool, time.Duration, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), rg, "--no-config", "--no-ignore-global", "--follow", "--files-with-matches", "--null", "--", pattern, ".")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	took := time.Since(start)
	files := rgFileList(stdout.Bytes())
	var exit *exec.ExitError
	switch {
	case err == nil && len(files) > 0:
	case errors.As(err, &exit) && exit.ExitCode() == 1 && len(files) == 0:
	case err == nil:
		return nil, took, errors.New("rg exited 0 with no files listed")
	default:
		return nil, took, fmt.Errorf("rg: %v (%d files listed)\n%s", err, len(files), stderr.String())
	}
	if stderr.Len() > 0 {
		return nil, took, fmt.Errorf("rg reported problems, so its list is not comparable:\n%s", stderr.String())
	}
	return files, took, nil
}

// timingReport writes one JSON line per measurement. The first write error is
// kept and every later line refused, so a report cut short by a full disk can
// never pass as complete: add returns the error and close returns it again,
// joined with the file's own close error.
type timingReport struct {
	w   io.WriteCloser
	enc *json.Encoder
	err error
}

func newTimingReport(w io.WriteCloser) *timingReport {
	return &timingReport{w: w, enc: json.NewEncoder(w)}
}

func (r *timingReport) add(v any) error {
	if r.err == nil {
		r.err = r.enc.Encode(v)
	}
	return r.err
}

func (r *timingReport) close() error {
	return errors.Join(r.err, r.w.Close())
}

// rgFileList parses the output of rg --files-with-matches --null: each path ends
// in a NUL, so newlines and carriage returns, valid in Linux names, stay part of
// the name. Only rg's leading "./" is removed.
func rgFileList(out []byte) map[string]bool {
	files := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	sc.Split(splitNUL)
	for sc.Scan() {
		if name := sc.Text(); name != "" {
			files[strings.TrimPrefix(filepath.ToSlash(name), "./")] = true
		}
	}
	return files
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
