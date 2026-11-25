//go:build scenario

package scenarios

import (
	"encoding/json"
	"fmt"
	iofs "io/fs"
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
// clone's git reports changed are exactly the files the logged successful writes,
// edits and patches change, and every file of the clone holds exactly the bytes those
// operations, applied in order to the checkout, leave; each task's run finishes with
// stop after a line-range read, an edit and a multi-file patch that succeeded; and no
// file outside the write root changed.
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
	initial := fs2Snapshot(t, clone)
	model := fs2Clone(initial)
	text := expand(taskT, map[string]string{"checkout": filepath.ToSlash(clone)})
	run := h.awaitRun(t, session, h.prompt(t, session, text), 3*time.Hour)
	if run.FinishReason != "stop" {
		t.Fatalf("the task's run finished %q, want stop: %s", run.FinishReason, run.FinalResponse)
	}
	outs := toolOutcomes(t, inRun(h.history(t, session), run.FirstSeq, run.LastSeq))

	var rangeReads, edits, multiPatches, okGreps int
	for i, o := range outs {
		if !o.IsError {
			switch o.Name {
			case "grep":
				okGreps++
			case "read":
				var a struct {
					Offset int `json:"offset"`
					Limit  int `json:"limit"`
				}
				if json.Unmarshal(o.Args, &a) == nil && (a.Offset > 0 || a.Limit > 0) {
					rangeReads++
				}
			}
		}
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
			var b struct {
				Pattern string `json:"pattern"`
			}
			if err := json.Unmarshal(outs[i+1].Args, &b); err != nil || b.Pattern == a.Pattern || outs[i+1].IsError {
				t.Fatalf("grep %q returned %s and the next grep (pattern %q, error %v, decode %v) is not a successful corrected search",
					a.Pattern, codeInvalidRegex, b.Pattern, outs[i+1].IsError, err)
			}
		case "write", "edit":
			if o.IsError {
				continue
			}
			var a struct {
				FilePath   string `json:"file_path"`
				Content    string `json:"content"`
				OldString  string `json:"old_string"`
				NewString  string `json:"new_string"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := json.Unmarshal(o.Args, &a); err != nil {
				t.Fatalf("%s arguments %s: %v", o.Name, o.Args, err)
			}
			p := fs2Rel(t, clone, a.FilePath)
			if o.Name == "write" {
				model[p] = []byte(a.Content)
				continue
			}
			edits++
			cur, ok := model[p]
			if !ok {
				t.Fatalf("a successful edit of %s, which the model of the clone does not hold", p)
			}
			old, n := lf(a.OldString), strings.Count(string(cur), lf(a.OldString))
			if n == 0 || (n > 1 && !a.ReplaceAll) {
				t.Fatalf("a successful edit of %s replaces %q, found %d times in the model", p, old, n)
			}
			count := 1
			if a.ReplaceAll {
				count = -1
			}
			model[p] = []byte(strings.Replace(string(cur), old, lf(a.NewString), count))
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
			if files := fs2ApplyPatch(t, model, clone, a.Input); files >= 2 {
				multiPatches++
			}
		case "bash", "shell", "exec":
			var a map[string]any
			_ = json.Unmarshal(o.Args, &a)
			if line := fmt.Sprint(a["command"]); strings.Contains(line, "grep") || strings.Contains(line, "rg ") || strings.Contains(line, "findstr") {
				t.Fatalf("a shell search ran instead of the grep tool: %s", line)
			}
		}
	}

	if okGreps == 0 || rangeReads == 0 || edits == 0 || multiPatches == 0 {
		t.Fatalf("the task made %d successful greps, %d line-range reads, %d successful edits and %d successful multi-file patches; want at least one of each",
			okGreps, rangeReads, edits, multiPatches)
	}

	// The disk must be exactly the checkout with the logged successful operations
	// applied in order: same files, same bytes. A file the checkout held with CRLF
	// line ends is compared with its line ends normalized; any other file byte for byte.
	var want []string
	for p, b := range model {
		if old, ok := initial[p]; !ok || lf(string(old)) != string(b) {
			want = append(want, p)
		}
	}
	for p := range initial {
		if _, ok := model[p]; !ok {
			want = append(want, p)
		}
	}
	sort.Strings(want)
	if changed := fs2GitChanged(t, clone); strings.Join(changed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git reports %v changed, the log's successful operations change %v", changed, want)
	}
	after := fs2Snapshot(t, clone)
	for p, b := range model {
		got, ok := after[p]
		switch {
		case !ok:
			t.Fatalf("%s is missing; the logged operations leave it holding %d bytes", p, len(b))
		case strings.Contains(string(initial[p]), "\r\n") && lf(string(got)) == string(b):
		case string(got) != string(b):
			t.Fatalf("%s holds %q; the logged operations leave %q", p, got, b)
		}
	}
	for p := range after {
		if _, ok := model[p]; !ok {
			t.Fatalf("%s exists; the logged operations do not create it", p)
		}
	}
	if diff := listingDiff(before, outside()); len(diff) > 0 {
		t.Fatalf("files outside the write root changed: %v", diff)
	}
	return greps, invalid
}

// fs2Hunk is one file operation of a patch in the apply_patch format.
type fs2Hunk struct {
	op     string // "add", "delete" or "update"
	path   string
	moveTo string
	lines  []string // add: the file's lines; update: the chunk lines, "@@" lines included
}

// fs2ParsePatch splits an apply_patch input into its file operations. It is the
// scenario's own reading of the format the tool documents (Add/Delete/Update File,
// Move to, @@ chunks of ' ', '-' and '+' lines); the scenario boundary keeps it from
// importing the harness's parser.
func fs2ParsePatch(t *testing.T, input string) []fs2Hunk {
	t.Helper()
	var hunks []fs2Hunk
	started := false
parse:
	for _, line := range strings.Split(lf(input), "\n") {
		marker := strings.TrimSpace(line)
		switch {
		case !started:
			// Lines before the opening marker (a heredoc wrapper) are not patch content.
			started = marker == "*** Begin Patch"
			continue
		case marker == "*** End Patch":
			// The terminator ends the patch: what follows (the input's trailing newline,
			// a heredoc's closing line) is not chunk content.
			break parse
		case marker == "*** End of File":
			if len(hunks) > 0 && hunks[len(hunks)-1].op == "update" {
				hunks[len(hunks)-1].lines = append(hunks[len(hunks)-1].lines, "*")
			}
			continue
		}
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			hunks = append(hunks, fs2Hunk{op: "add", path: strings.TrimPrefix(line, "*** Add File: ")})
		case strings.HasPrefix(line, "*** Delete File: "):
			hunks = append(hunks, fs2Hunk{op: "delete", path: strings.TrimPrefix(line, "*** Delete File: ")})
		case strings.HasPrefix(line, "*** Update File: "):
			hunks = append(hunks, fs2Hunk{op: "update", path: strings.TrimPrefix(line, "*** Update File: ")})
		case strings.HasPrefix(line, "*** Move to: ") && len(hunks) > 0:
			hunks[len(hunks)-1].moveTo = strings.TrimPrefix(line, "*** Move to: ")
		case len(hunks) > 0 && hunks[len(hunks)-1].op != "delete":
			h := &hunks[len(hunks)-1]
			if h.op == "add" && strings.HasPrefix(line, "+") {
				h.lines = append(h.lines, line[1:])
			} else if h.op == "update" && line != "" {
				h.lines = append(h.lines, line)
			} else if h.op == "update" {
				h.lines = append(h.lines, " ") // a blank context line
			}
		}
	}
	if len(hunks) == 0 {
		t.Fatalf("a successful apply_patch call's patch names no file: %q", input)
	}
	return hunks
}

// fs2ApplyChunks applies an update's chunks to text: each chunk's context and '-'
// lines are found at or after the previous chunk (after its "@@ anchor" line when it
// names one; only at the end of the file when it ends with "*** End of File"),
// exactly, else ignoring trailing white space, and replaced by its context and '+'
// lines. ok is false when a chunk is not found.
func fs2ApplyChunks(text string, lines []string) (string, bool) {
	file := strings.Split(text, "\n")
	trailing := len(file) > 0 && file[len(file)-1] == ""
	if trailing {
		file = file[:len(file)-1]
	}
	at := 0
	var anchor string
	var old, repl []string
	eof := false
	flush := func() bool {
		if len(old) == 0 && len(repl) == 0 {
			return true
		}
		if anchor != "" {
			for k := at; k < len(file); k++ {
				if strings.TrimSpace(file[k]) == anchor {
					at = k + 1
					break
				}
			}
		}
		pos := -1
		if len(old) == 0 {
			pos = len(file)
		}
		for _, eq := range []func(a, b string) bool{
			func(a, b string) bool { return a == b },
			func(a, b string) bool { return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t") },
		} {
			for k := at; pos < 0 && k+len(old) <= len(file); k++ {
				if eof && k+len(old) != len(file) {
					continue
				}
				match := true
				for n := range old {
					if !eq(file[k+n], old[n]) {
						match = false
						break
					}
				}
				if match {
					pos = k
				}
			}
		}
		if pos < 0 {
			return false
		}
		file = append(file[:pos], append(append([]string(nil), repl...), file[pos+len(old):]...)...)
		at = pos + len(repl)
		old, repl, anchor, eof = nil, nil, "", false
		return true
	}
	for _, l := range lines {
		switch l[0] {
		case '@':
			if !flush() {
				return "", false
			}
			anchor = strings.TrimSpace(strings.TrimPrefix(l, "@@"))
		case '-':
			old = append(old, l[1:])
		case '+':
			repl = append(repl, l[1:])
		case '*':
			eof = true // "*** End of File": this chunk matches at the end of the file
		default:
			old = append(old, l[1:])
			repl = append(repl, l[1:])
		}
	}
	if !flush() {
		return "", false
	}
	// Each remaining line ends with a newline; a file with no lines left is empty
	// (textdiff/patch Hunk.Apply), not one blank line.
	out := strings.Join(file, "\n")
	if len(file) > 0 {
		out += "\n"
	}
	return out, true
}

// fs2ApplyPatch applies a successful apply_patch call's patch to model and returns
// how many files it touched. A patch that does not apply to the model fails the
// task: the tool reported success, so the disk and the log disagree.
func fs2ApplyPatch(t *testing.T, model map[string][]byte, clone, input string) int {
	t.Helper()
	hunks := fs2ParsePatch(t, input)
	for _, h := range hunks {
		p := fs2Rel(t, clone, h.path)
		switch h.op {
		case "add":
			model[p] = []byte(strings.Join(h.lines, "\n") + "\n")
		case "delete":
			delete(model, p)
		case "update":
			cur, ok := model[p]
			if !ok {
				t.Fatalf("a successful patch updates %s, which the model of the clone does not hold", p)
			}
			next, ok := fs2ApplyChunks(string(cur), h.lines)
			if !ok {
				t.Fatalf("a successful patch does not apply to the model of %s", p)
			}
			if h.moveTo != "" {
				delete(model, p)
				p = fs2Rel(t, clone, h.moveTo)
			}
			model[p] = []byte(next)
		}
	}
	return len(hunks)
}

// fs2Snapshot is every regular file of the clone outside .git, by slash path.
func fs2Snapshot(t *testing.T, clone string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(clone, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(clone, p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatalf("snapshot of %s: %v", clone, err)
	}
	return files
}

// fs2Clone is the model's start: the snapshot with CRLF line ends normalized, so
// logged edits (whose text the model writes with LF) match.
func fs2Clone(files map[string][]byte) map[string][]byte {
	model := make(map[string][]byte, len(files))
	for p, b := range files {
		model[p] = []byte(lf(string(b)))
	}
	return model
}

// lf is s with CRLF line ends made LF.
func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

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
// reports every rg seen. It fails closed: a listing that never succeeded, or that
// failed even once, fails the scenario instead of reporting no rg.
func fs2WatchChildren(t *testing.T, children string, pid int) func() []string {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	var listed, failed int
	var lastErr error
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
			mu.Lock()
			if err != nil {
				failed++
				lastErr = fmt.Errorf("%s: %w: %s", line, err, strings.TrimSpace(out))
				mu.Unlock()
				continue
			}
			listed++
			mu.Unlock()
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
		if listed == 0 || failed > 0 {
			t.Fatalf("the descendant listing succeeded %d times and failed %d times (last: %v); no rg child cannot be shown", listed, failed, lastErr)
		}
		return append([]string(nil), seen...)
	}
}
