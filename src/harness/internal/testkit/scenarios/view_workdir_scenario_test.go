//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"errors"
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
// write that succeeds (write, edit and each path of apply_patch) lands inside
// the session's working directory of its run (R before the move, T after it),
// and the hash listing of the scratch directory outside R and T is unchanged
// (implementation-plan Stage 8.6, TC-21s4).
//
// Cleanup: the repository, the worktree and the home are removed (t.TempDir);
// the session log is kept (keepExport). When a file outside R and T changed, the
// stage stops (M2-R-03) and both listings are kept (keepListings).
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
	outside := func() map[string]string { return view5Outside(t, scratch) }
	before := outside()
	keepListings(t, "view5-outside", before, outside)
	unchanged := func(when string) {
		t.Helper()
		if diff := listingDiff(before, outside()); len(diff) > 0 {
			t.Fatalf("%s, files outside the project and the worktree changed (M2-R-03: the stage stops): %v", when, diff)
		}
	}

	h := startSDK(t, bin, nil, []string{"--layer", layer})
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

// view5Outside is the hash listing of scratch without the project (repo) and the
// worktree (tree): what no write of the session may change.
func view5Outside(t *testing.T, scratch string) map[string]string {
	t.Helper()
	all := hashListing(t, scratch)
	for k := range all {
		if top, _, _ := strings.Cut(k, "/"); top == "repo" || top == "tree" {
			delete(all, k)
		}
	}
	return all
}

// view5WritesInside fails unless every successful write, edit and apply_patch
// path of evs, relative ones resolved against dir, names a file inside dir (the
// session's working directory of that run). A refused call wrote nothing.
func view5WritesInside(t *testing.T, evs []protocol.Event, dir string) {
	t.Helper()
	for _, o := range toolOutcomes(t, evs) {
		if o.IsError {
			continue
		}
		var paths []string
		switch o.Name {
		case "write", "edit":
			var a struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(o.Args, &a); err != nil {
				t.Fatalf("%s arguments %s: %v", o.Name, o.Args, err)
			}
			paths = []string{a.FilePath}
		case "apply_patch":
			var a struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal(o.Args, &a); err != nil {
				t.Fatalf("apply_patch arguments %s: %v", o.Args, err)
			}
			for _, h := range fs2ParsePatch(t, a.Input) {
				paths = append(paths, h.path)
				if h.moveTo != "" {
					paths = append(paths, h.moveTo)
				}
			}
		default:
			continue
		}
		for _, p := range paths {
			if !view5Inside(dir, p) {
				t.Fatalf("call %s (%s) wrote %s, outside the session's working directory %s", o.ID, o.Name, p, dir)
			}
		}
	}
}

// view5Inside reports whether p, resolved against dir when relative, is dir or
// below it; dir is compared as given and with its links resolved.
func view5Inside(dir, p string) bool {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	dirs := []string{filepath.Clean(dir)}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dirs = append(dirs, filepath.Clean(real))
	}
	for _, d := range dirs {
		if rel, err := filepath.Rel(d, filepath.Clean(p)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
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
