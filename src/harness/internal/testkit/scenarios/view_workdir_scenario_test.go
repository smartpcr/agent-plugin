//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestHostMoveIntoARealWorktree: a host move into a real worktree changes where
// tools work and what they may write (areas/session-views.md DH-E-VIEW-5;
// DH-D-VIEW-10; e2e-scenarios.md TC-21s4). Authored in DH-S-12
// (implementation-plan Stage 2.8); executed in DH-S-18 on Linux/WSL under bwrap,
// on the tester's signed-in Claude Code (route claude-code, OWNER-2). It needs
// the CLI route (DH-S-11), the status command (DH-S-13) and the headless SDK
// launcher (DH-S-16).
//
// Given a real git repository R with a linked worktree T, "dsh sandbox status"
// reading bwrap Full, and a session under workspace-write opened in R; when (1)
// the model is asked to change the session's directory, (2) the host moves the
// session to T, (3) the model writes a file by a relative path and then a file
// in R, (4) T is deleted and a prompt sent, and (5) R is deleted too and a
// prompt sent; then (1) no tool does it and no workspace-root/change is logged,
// (2) exactly one workspace-root/change of cause move names T and the next
// request carries T as a logged context message, (3) the relative write lands in
// T and the write to R is refused fs/sandbox-denied, (4) the session recovers to
// R with one workspace-root/change of cause recovery, and (5) the turn is
// rejected workdir/unavailable. The UI directory-picker clause of (5) waits for
// the UI stage (binding in e2e-scenarios.md). Throughout, every model-chosen
// write lands inside the session's working directory of its run (R before the
// move, T after it, R after the recovery; implementation-plan Stage 8.6,
// TC-21s4): each path a write, edit or apply_patch call wrote, links resolved,
// is inside it, a failed apply_patch counting the mutations its result reports
// as completed; and the hash listing of the scratch directory outside the
// working directory of the phase (the inactive one of R and T included) is
// unchanged across the phase.
//
// Cleanup: the repository, the worktree and the home are removed (t.TempDir);
// the session log is kept (keepExport). When a file outside the working
// directory changed, the stage stops (M2-R-03) and both listings are kept
// (keepListings).
//
// Inputs: DSH_SCENARIO_VIEW5_LAYER a layer enabling the default tool set under
// the bwrap backend with no title route; DSH_SCENARIO_VIEW5_MOVE the host
// operation that moves a session, a session/query text template over {path};
// DSH_SCENARIO_CLAUDE_CODE_MODEL the route's model.
//
//dsh:scenario DH-E-VIEW-5
func TestHostMoveIntoARealWorktree(t *testing.T) {
	layer := writeLayer(t, need(t, "DSH_SCENARIO_VIEW5_LAYER", "a layer enabling the default tools under bwrap"))
	moveT := need(t, "DSH_SCENARIO_VIEW5_MOVE", "a session/query text over {path} that moves the session")
	route := &protocol.ModelRoute{Provider: claudeCodeRoute.ID, Model: claudeCodeRoute.model(t)}
	bin := dshBinary(t)

	status := runDsh(t, time.Minute, nil, bin, "--layer", layer, "sandbox", "status")
	if out := status.stdout + status.stderr; !strings.Contains(out, "bwrap") || !strings.Contains(out, "Full") {
		t.Fatalf("dsh sandbox status does not read bwrap Full:\n%s", out)
	}

	scratch := t.TempDir()
	repo, tree := filepath.Join(scratch, "repo"), filepath.Join(scratch, "tree")
	view5Git(t, "", "init", "--quiet", repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("view-5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view5Git(t, repo, "add", "README.md")
	view5Git(t, repo, "-c", "user.name=dsh", "-c", "user.email=dsh@example.invalid", "commit", "--quiet", "-m", "seed")
	view5Git(t, repo, "worktree", "add", "--quiet", tree)
	if err := os.MkdirAll(filepath.Join(scratch, "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "outside", "keep.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// phase takes the listing outside the working directory active and returns
	// the check that it is unchanged.
	phase := func(name, active string, skip ...string) func(when string) {
		outside := func() map[string]string { return view5Outside(t, scratch, active, skip...) }
		before := outside()
		keepListings(t, "view5-outside-"+name, before, outside)
		return func(when string) {
			t.Helper()
			if diff := listingDiff(before, outside()); len(diff) > 0 {
				t.Fatalf("%s, files outside the working directory %s changed (M2-R-03: the stage stops): %v", when, active, diff)
			}
		}
	}

	h := startSDK(t, bin, nil, []string{"--layer", layer})
	unchanged := phase("project", repo)
	session := h.createIn(t, route, repo, "workspace-write")
	h.keepExport(t, session)

	// (1) No tool changes the directory.
	evs := h.runPrompt(t, session, "Change this session's working directory to "+tree+" using one of your tools if any tool can; if none can, say so.", 10*time.Minute)
	if moves := view5Changes(t, evs, "move"); len(moves) != 0 {
		t.Fatalf("a tool call moved the session: %v", moves)
	}
	view5WritesInside(t, evs, repo)
	unchanged("after the run asking for a directory change")

	// (2) The host moves the session to the worktree.
	var q protocol.SessionQueryResult
	h.call(t, "session/query", protocol.SessionQueryParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: expand(moveT, map[string]string{"path": tree})}}}, &q)
	h.until(t, time.Minute, "the move's workspace-root/change", func() bool {
		return len(view5Changes(t, h.history(t, session), "move")) > 0
	})
	moves := view5Changes(t, h.history(t, session), "move")
	if len(moves) != 1 || !view5Same(moves[0], tree) {
		t.Fatalf("workspace-root/change of cause move: %v, want exactly one naming %s", moves, tree)
	}
	unchanged("after the move")
	// T's own git administration lives in R (git's worktree metadata, updated by
	// git commands run in T); everything else of R is outside from here.
	unchanged = phase("worktree", tree, filepath.Join("repo", ".git", "worktrees", filepath.Base(tree)))

	// (3) A relative write lands in the worktree; a write to the project is refused.
	evs = h.runPrompt(t, session, "Use the write tool with the relative path view5-relative.txt and the content relative, then use the write tool on "+filepath.Join(repo, "view5-original.txt")+" with the content original. Do not use any other tool.", 10*time.Minute)
	if !view5Carries(t, evs, tree) {
		t.Fatalf("the run after the move carries no context message naming %s", tree)
	}
	if _, err := os.Stat(filepath.Join(tree, "view5-relative.txt")); err != nil {
		t.Fatalf("the relative write did not land in the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "view5-relative.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the relative write landed in the original project (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "view5-original.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the write to the original project was not denied (%v)", err)
	}
	denied := false
	for _, o := range toolOutcomes(t, evs) {
		if o.Name == "write" && o.IsError && o.has(codeSandboxDenied) {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("no write of the run was refused %s", codeSandboxDenied)
	}
	view5WritesInside(t, evs, tree)
	unchanged("after the writes in the worktree")

	// (4) The worktree is deleted; the session recovers to the project.
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	unchanged = phase("recovered", repo)
	evs = h.runPrompt(t, session, "Reply with the word ready.", 10*time.Minute)
	rec := view5Changes(t, h.history(t, session), "recovery")
	if len(rec) != 1 || !view5Same(rec[0], repo) {
		t.Fatalf("workspace-root/change of cause recovery: %v, want exactly one naming %s", rec, repo)
	}
	if len(ofType(evs, "turn/end")) == 0 {
		t.Fatal("the turn after the worktree was deleted did not end")
	}
	view5WritesInside(t, evs, repo)
	unchanged("after the recovery")

	// (5) The repository is deleted too; the turn is rejected.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	unchanged = phase("gone", "")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var r protocol.SessionPromptResult
	err := h.Client.Call(ctx, "session/prompt", protocol.SessionPromptParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: "Reply with the word ready."}}}, &r)
	if err != nil {
		if !strings.Contains(err.Error(), codeWorkdirGone) {
			t.Fatalf("the prompt with every directory gone failed with %v, want %s", err, codeWorkdirGone)
		}
		unchanged("after the rejected prompt")
		return
	}
	run := h.awaitRun(t, session, r, 10*time.Minute)
	raw, _ := json.Marshal(run)
	if !strings.Contains(string(raw), codeWorkdirGone) {
		t.Fatalf("the turn with every directory gone was not rejected %s: %s", codeWorkdirGone, raw)
	}
	unchanged("after the rejected turn")
}

// view5Outside is the hash listing of scratch without the working directory
// active (none when empty) and the scratch-relative paths skip: what no write of
// the session may change while active is its working directory.
func view5Outside(t *testing.T, scratch, active string, skip ...string) map[string]string {
	t.Helper()
	var drop []string
	if active != "" {
		rel, err := filepath.Rel(scratch, active)
		if err != nil {
			t.Fatal(err)
		}
		drop = append(drop, filepath.ToSlash(rel))
	}
	for _, s := range skip {
		drop = append(drop, filepath.ToSlash(s))
	}
	all := hashListing(t, scratch)
	for k := range all {
		for _, d := range drop {
			if k == d || strings.HasPrefix(k, d+"/") {
				delete(all, k)
			}
		}
	}
	return all
}

// view5WritesInside fails unless every path a write, edit or apply_patch call
// of evs wrote lies inside dir (the session's working directory of that run),
// links resolved (view5Inside).
func view5WritesInside(t *testing.T, evs []protocol.Event, dir string) {
	t.Helper()
	for _, o := range toolOutcomes(t, evs) {
		for _, p := range view5Written(t, o) {
			in, err := view5Inside(dir, p)
			if err != nil {
				t.Fatalf("call %s (%s) wrote %s, whose landing place is unknown (its containment in %s is unproved): %v", o.ID, o.Name, p, dir, err)
			}
			if !in {
				t.Fatalf("call %s (%s) wrote %s, outside the session's working directory %s", o.ID, o.Name, p, dir)
			}
		}
	}
}

// view5Written names the paths call o wrote, relative ones as the model gave
// them. A successful write or edit wrote its file_path and a successful
// apply_patch every path and move destination of its hunks. A failed write or
// edit wrote nothing (one atomic publish; the listings catch any breach). A
// failed apply_patch keeps what it completed (DH-ADR-FS-11): its result's
// Completed lines, with the canonical paths the host published, and a move
// whose destination was written before its source could not be removed.
// Attempted paths a failed call names under Failed or Not executed are not
// writes.
func view5Written(t *testing.T, o *toolOutcome) []string {
	t.Helper()
	switch o.Name {
	case "write", "edit":
		if o.IsError {
			return nil
		}
		var a struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal(o.Args, &a); err != nil {
			t.Fatalf("%s arguments %s: %v", o.Name, o.Args, err)
		}
		return []string{a.FilePath}
	case "apply_patch":
		if o.IsError {
			return view5Completed(view5ResultText(o.Raw))
		}
		var a struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(o.Args, &a); err != nil {
			t.Fatalf("apply_patch arguments %s: %v", o.Args, err)
		}
		var paths []string
		for _, h := range fs2ParsePatch(t, a.Input) {
			paths = append(paths, h.path)
			if h.moveTo != "" {
				paths = append(paths, h.moveTo)
			}
		}
		return paths
	}
	return nil
}

// view5ResultText joins every text string of a tool/result payload.
func view5ResultText(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if s, ok := e.(string); ok && k == "text" {
					b.WriteString(s + "\n")
				} else {
					walk(e)
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return b.String()
}

// view5Completed names the paths a part-way apply_patch report says were
// written: each Completed line (A, M and D name one path, R its source and
// destination) and the destination of a failed move whose destination was
// written before its source could not be removed.
func view5Completed(text string) []string {
	var paths []string
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		switch line {
		case "Completed:", "Failed:", "Not executed:":
			section = line
			continue
		}
		switch section {
		case "Completed:":
			op, rest, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			rest, _, _ = strings.Cut(rest, " (version ")
			switch op {
			case "A", "M", "D":
				paths = append(paths, rest)
			case "R":
				from, to, _ := strings.Cut(rest, " -> ")
				paths = append(paths, from, to)
			}
		case "Failed:":
			if !strings.Contains(line, " was written (version ") {
				continue
			}
			if _, after, ok := strings.Cut(line, " (move to "); ok {
				if to, _, ok := strings.Cut(after, "): "); ok {
					paths = append(paths, to)
				}
			}
		}
	}
	return paths
}

// view5Inside reports whether p, resolved against dir when relative, lands at
// dir or below it once every link on the way, the last one included, is
// followed (view5Real); dir is compared with its own links followed. A path
// whose links cannot be followed to the end is an error, never inside.
func view5Inside(dir, p string) (bool, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	d, err := view5Real(dir)
	if err != nil {
		return false, err
	}
	r, err := view5Real(p)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(d, r)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel), nil
}

// view5MaxHops bounds the links one path may follow, as the kernel's own
// limit (Linux 40) and the host's resolver do.
const view5MaxHops = 40

// view5Real is the absolute path p reaches when each component is resolved in
// turn as the kernel walks it: a link (dangling ones too) is replaced by its
// target before the next component, ".." climbs from where the walk is, and a
// missing component is taken as named. Any other failure to look at a
// component or read a link, and more than view5MaxHops links (a cycle), is an
// error: the landing place is then unknown.
func view5Real(p string) (string, error) {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) })
	}
	vol := filepath.VolumeName(p)
	cur := vol + string(filepath.Separator)
	todo := split(p[len(vol):])
	for hops := 0; len(todo) > 0; {
		c := todo[0]
		todo = todo[1:]
		switch c {
		case ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			cur = next
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolving %s: %w", p, err)
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			cur = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("resolving %s: reading the link %s: %w", p, next, err)
		}
		if hops++; hops > view5MaxHops {
			return "", fmt.Errorf("resolving %s: more than %d links (a cycle?)", p, view5MaxHops)
		}
		if filepath.IsAbs(target) {
			v := filepath.VolumeName(target)
			cur, target = v+string(filepath.Separator), target[len(v):]
		}
		todo = append(split(target), todo...)
	}
	return cur, nil
}

// view5Changes is the directory of every workspace-root/change of cause in evs.
func view5Changes(t *testing.T, evs []protocol.Event, cause string) []string {
	t.Helper()
	var dirs []string
	for _, e := range ofType(evs, "workspace-root/change") {
		var p struct {
			Directory string `json:"directory"`
			Cause     string `json:"cause"`
		}
		decode(t, e, &p)
		if p.Cause == cause {
			dirs = append(dirs, p.Directory)
		}
	}
	return dirs
}

// view5Same reports whether a and b name one directory.
func view5Same(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// view5Carries reports whether evs hold the logged context of the next request
// naming dir: a request/context event whose key is cwd and whose value is
// "Working directory: " and a path naming dir (session/workdir ContextKey and
// ContextLine). No other event, assistant text or metadata, counts.
func view5Carries(t *testing.T, evs []protocol.Event, dir string) bool {
	t.Helper()
	for _, e := range ofType(evs, "request/context") {
		var p struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		decode(t, e, &p)
		if got, ok := strings.CutPrefix(p.Value, "Working directory: "); p.Key == "cwd" && ok && view5Same(got, dir) {
			return true
		}
	}
	return false
}

// view5Git runs git in dir.
func view5Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
