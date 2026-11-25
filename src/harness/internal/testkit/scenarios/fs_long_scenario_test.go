//go:build scenario

package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// fs2Tasks is the fixed number of tasks (areas/filesystem.md DH-E-FS-2).
const fs2Tasks = 20

// fs2InvalidShare is the proposed threshold: fewer than this share of grep calls
// may return search/invalid-pattern.
const fs2InvalidShare = 0.05

// TestLongSessionCorrectsItsOwnPatterns: a real model searches with RE2 and
// edits files with the shipped tools (areas/filesystem.md DH-E-FS-2; DH-D-FS-6;
// e2e-scenarios.md TC-21s4, Linux and Windows legs). Authored in DH-S-12
// (implementation-plan Stage 2.8); executed in DH-S-18 on the tester's Linux
// machine (ext4) and the Windows variant machine (NTFS), each on its own
// signed-in Claude Code (route claude-code, OWNER-2; a signed-in client elsewhere
// does not satisfy a leg). It needs the headless SDK launcher (DH-S-16).
//
// Given the built dsh with a fresh home and no rg on its PATH, the default tool
// set under workspace-write, and for each of the 20 tasks a fresh clone of one of
// DSH_SCENARIO_FS_CHECKOUTS listed by hash with its parent directory; when each
// task runs as one prompt on the signed-in route; then fewer than 5% of grep
// calls return search/invalid-pattern and each is followed by a corrected grep,
// not a shell search; no child process named rg is started; the files the
// clone's git reports changed are exactly the files of the logged successful
// edits, each holding its last edit; and no file outside the write root changed.
// A task that runs longer than the run's shard budget is the runner's to shard
// (M2-R-06): the scenario runs the tasks DSH_SCENARIO_FS2_SHARD selects.
//
// Cleanup: the clones and home are removed; both listings and the session logs
// are kept as artifacts (keepListings, keepExport), and the count of invalid
// patterns by construct is logged.
//
// Inputs: DSH_SCENARIO_FS2_TASKS a file of the 20 tasks, one per line, each a
// prompt template over {checkout} that needs a content search, a read of a line
// range, one edit and one multi-file patch; DSH_SCENARIO_FS_CHECKOUTS the
// ';'-separated git URLs or paths of small public repositories, task i using
// checkout i mod n; DSH_SCENARIO_FS2_CHILDREN a command template over {pid} that
// prints the names of every descendant process of pid, one per line;
// DSH_SCENARIO_FS2_LAYER a configuration layer enabling the default tool set with
// no title route; DSH_SCENARIO_FS2_SHARD the tasks to run, "all" or "i-j"
// (1-based, inclusive); DSH_SCENARIO_CLAUDE_CODE_MODEL the route's model.
//
//dsh:scenario DH-E-FS-2
func TestLongSessionCorrectsItsOwnPatterns(t *testing.T) {
	tasks := fs2ReadTasks(t, need(t, "DSH_SCENARIO_FS2_TASKS", "a file of the 20 tasks, one prompt template over {checkout} per line"))
	checkouts := needList(t, "DSH_SCENARIO_FS_CHECKOUTS", "the git URLs or paths of small public repositories")
	children := need(t, "DSH_SCENARIO_FS2_CHILDREN", "a command over {pid} printing the names of its descendant processes")
	layer := writeLayer(t, need(t, "DSH_SCENARIO_FS2_LAYER", "a layer enabling the default tool set"))
	from, to := fs2Shard(t, need(t, "DSH_SCENARIO_FS2_SHARD", `"all" or "i-j"`))
	route := &protocol.ModelRoute{Provider: claudeCodeRoute.ID, Model: claudeCodeRoute.model(t)}
	bin := dshBinary(t)

	h := startSDK(t, bin, []string{"PATH=" + fs2PathWithoutRG(t)}, []string{"--layer", layer})
	rgSeen := fs2WatchChildren(t, children, h.Process.Pid())

	var greps, invalid int
	byConstruct := map[string]int{}
	for i := from; i <= to; i++ {
		t.Run(fmt.Sprintf("task%02d", i), func(t *testing.T) {
			g, bad := fs2Task(t, h, route, checkouts[(i-1)%len(checkouts)], tasks[i-1], byConstruct)
			greps += g
			invalid += bad
		})
	}
	t.Logf("%d grep calls, %d returned %s; by construct: %v", greps, invalid, codeInvalidRegex, byConstruct)
	if greps == 0 {
		t.Fatal("no task made a grep call")
	}
	if share := float64(invalid) / float64(greps); share >= fs2InvalidShare {
		t.Fatalf("%.1f%% of grep calls returned %s, want under %.0f%%", 100*share, codeInvalidRegex, 100*fs2InvalidShare)
	}
	if names := rgSeen(); len(names) > 0 {
		t.Fatalf("an rg child process was started: %v", names)
	}
}

// fs2Task runs one task on a fresh clone and checks its calls and its disk.
func fs2Task(t *testing.T, h *sdkHost, route *protocol.ModelRoute, checkout, taskT string, byConstruct map[string]int) (greps, invalid int) {
	scratch := t.TempDir()
	clone := filepath.Join(scratch, "checkout")
	if out, err := exec.Command("git", "clone", "--quiet", "--depth", "1", checkout, clone).CombinedOutput(); err != nil {
		t.Fatalf("git clone %s: %v\n%s", checkout, err, out)
	}
	// The listing is of the clone's parent, so a file created beside the clone,
	// outside the write root, shows; the clone's own files are judged by git.
	sibling := filepath.Join(scratch, "outside")
	if err := os.WriteFile(sibling, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := func() map[string]string {
		all := hashListing(t, scratch)
		for k := range all {
			if strings.HasPrefix(filepath.ToSlash(k), "checkout/") {
				delete(all, k)
			}
		}
		return all
	}
	before := outside()
	keepListings(t, "fs2-outside", before, outside)

	session := h.createIn(t, route, clone, "workspace-write")
	h.keepExport(t, session)
	outs := toolOutcomes(t, h.runPrompt(t, session, expand(taskT, map[string]string{"checkout": filepath.ToSlash(clone)}), 3*time.Hour))

	edited := map[string]string{} // file -> the new text its last successful edit left in it
	for i, o := range outs {
		switch o.Name {
		case "grep":
			greps++
			if !o.IsError || !o.has(codeInvalidRegex) {
				continue
			}
			invalid++
			var a struct {
				Pattern string `json:"pattern"`
			}
			_ = json.Unmarshal(o.Args, &a)
			byConstruct[fs2Construct(a.Pattern)]++
			if i+1 >= len(outs) || outs[i+1].Name != "grep" {
				next := "nothing"
				if i+1 < len(outs) {
					next = outs[i+1].Name
				}
				t.Fatalf("grep %q returned %s and was followed by %s, not a corrected grep", a.Pattern, codeInvalidRegex, next)
			}
		case "write", "edit":
			if o.IsError {
				continue
			}
			var a struct {
				FilePath  string `json:"file_path"`
				Content   string `json:"content"`
				NewString string `json:"new_string"`
			}
			if err := json.Unmarshal(o.Args, &a); err != nil {
				t.Fatalf("%s arguments %s: %v", o.Name, o.Args, err)
			}
			edited[fs2Rel(t, clone, a.FilePath)] = a.Content + a.NewString
		case "apply_patch":
			if o.IsError {
				continue
			}
			var a struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal(o.Args, &a); err != nil {
				t.Fatalf("apply_patch arguments %s: %v", o.Args, err)
			}
			for path, added := range fs2PatchFiles(a.Input) {
				edited[fs2Rel(t, clone, path)] = added
			}
		case "bash", "shell", "exec":
			var a map[string]any
			_ = json.Unmarshal(o.Args, &a)
			if line := fmt.Sprint(a["command"]); strings.Contains(line, "grep") || strings.Contains(line, "rg ") || strings.Contains(line, "findstr") {
				t.Fatalf("a shell search ran instead of the grep tool: %s", line)
			}
		}
	}

	changed := fs2GitChanged(t, clone)
	var want []string
	for f := range edited {
		want = append(want, f)
	}
	sort.Strings(want)
	if strings.Join(changed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git reports %v changed, the log's successful edits name %v", changed, want)
	}
	for f, text := range edited {
		data, err := os.ReadFile(filepath.Join(clone, filepath.FromSlash(f)))
		if text != "" && (err != nil || !strings.Contains(strings.ReplaceAll(string(data), "\r\n", "\n"), strings.ReplaceAll(text, "\r\n", "\n"))) {
			t.Fatalf("%s does not hold its last logged edit %q (%v)", f, text, err)
		}
	}
	if diff := listingDiff(before, outside()); len(diff) > 0 {
		t.Fatalf("files outside the write root changed: %v", diff)
	}
	return greps, invalid
}

// fs2PatchFiles is each file a patch adds or updates, with the lines it adds.
func fs2PatchFiles(patch string) map[string]string {
	files := map[string]string{}
	var cur string
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			cur = strings.TrimPrefix(line, "*** Add File: ")
			files[cur] = ""
		case strings.HasPrefix(line, "*** Update File: "):
			cur = strings.TrimPrefix(line, "*** Update File: ")
			files[cur] = ""
		case strings.HasPrefix(line, "*** Delete File: "):
			files[strings.TrimPrefix(line, "*** Delete File: ")] = ""
			cur = ""
		case strings.HasPrefix(line, "*** "):
			cur = ""
		case cur != "" && strings.HasPrefix(line, "+"):
			if files[cur] == "" {
				files[cur] = strings.TrimPrefix(line, "+")
			}
		}
	}
	return files
}

// fs2Rel is path relative to the clone, slash-separated.
func fs2Rel(t *testing.T, clone, path string) string {
	t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(clone, path)
	}
	rel, err := filepath.Rel(clone, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("a successful edit names %s, outside the clone %s", path, clone)
	}
	return filepath.ToSlash(rel)
}

// fs2GitChanged is every file git reports modified or added in the clone.
func fs2GitChanged(t *testing.T, clone string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", clone, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) > 3 {
			files = append(files, strings.Trim(line[3:], `"`))
		}
	}
	sort.Strings(files)
	return files
}

// fs2Construct names the construct an invalid pattern used that RE2 lacks.
func fs2Construct(p string) string {
	for _, c := range []struct{ marker, name string }{
		{"(?=", "lookahead"}, {"(?!", "negative lookahead"}, {"(?<=", "lookbehind"}, {"(?<!", "negative lookbehind"},
		{`\1`, "backreference"}, {"(?>", "atomic group"}, {"*+", "possessive"}, {"++", "possessive"},
	} {
		if strings.Contains(p, c.marker) {
			return c.name
		}
	}
	return "other"
}

// fs2ReadTasks reads the task file: exactly fs2Tasks non-blank lines.
func fs2ReadTasks(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			tasks = append(tasks, line)
		}
	}
	if len(tasks) != fs2Tasks {
		t.Fatalf("%s holds %d tasks, want %d", path, len(tasks), fs2Tasks)
	}
	return tasks
}

// fs2Shard parses the shard selector.
func fs2Shard(t *testing.T, s string) (int, int) {
	t.Helper()
	if s == "all" {
		return 1, fs2Tasks
	}
	var from, to int
	if _, err := fmt.Sscanf(s, "%d-%d", &from, &to); err != nil || from < 1 || to > fs2Tasks || from > to {
		t.Fatalf("DSH_SCENARIO_FS2_SHARD %q: want \"all\" or i-j within 1-%d", s, fs2Tasks)
	}
	return from, to
}

// fs2PathWithoutRG is this process's PATH without any directory holding rg.
func fs2PathWithoutRG(t *testing.T) string {
	t.Helper()
	exe := "rg"
	if runtime.GOOS == "windows" {
		exe = "rg.exe"
	}
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, exe)); err == nil {
			t.Logf("rg removed from PATH: %s", dir)
			continue
		}
		keep = append(keep, dir)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// fs2WatchChildren polls the descendants of pid until cleanup; the returned func
// reports every rg seen.
func fs2WatchChildren(t *testing.T, children string, pid int) func() []string {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		line := expand(children, map[string]string{"pid": fmt.Sprint(pid)})
		for {
			select {
			case <-done:
				return
			case <-time.After(200 * time.Millisecond):
			}
			out, err := shell(t, line)
			if err != nil {
				continue
			}
			for _, name := range strings.Fields(out) {
				if base := strings.ToLower(filepath.Base(name)); base == "rg" || base == "rg.exe" {
					mu.Lock()
					seen = append(seen, name)
					mu.Unlock()
				}
			}
		}
	}()
	t.Cleanup(func() { close(done); <-stopped })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}
