//go:build scenario

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/home until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/testkit/procwait until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
)

// TestKilledHostRecoversHonestly: a host killed mid-turn, while a tool body is
// running, recovers honestly when the session is resumed (areas/session-store.md
// DH-E-STORE-2; DH-D-STORE-4, DH-D-STORE-2). Guarded; executed in DH-S-16 (step
// 16.8) on scratch homes; authored in DH-S-7.
//
// Given the built binary started as a headless host on the signed-in Claude Code
// route (M2-I-1: decision models-signed-in-claude-copilot reads "a real model API
// key" as that route; no key exists), a
// real shell under the platform's sandbox, an empty home and a workspace with one
// file, when (1) the agent is prompted to run a shell command that sleeps for a
// minute and then writes a file, (2) the host is killed once the tool call is
// recorded, (3) the host is started again and resumes the session and (4) a new
// prompt is sent, then after (3) the session shows the turn as interrupted and
// the tool call with an outcome-unknown result, no event recorded before the kill
// is missing, and the first model request after (4) contains that result; then
// (5) the host is killed and the session deleted, and nothing of it remains: no
// session in the home's database, no spill file and no blob. The home's files
// before and after (5), the log's seq range and the session's absence are logged
// as "evidence:" lines.
//
// Inputs are shell command lines over {home}, {workspace}, {session}, {prompt},
// {route} and {model}: DSH_SCENARIO_STORE2_START starts the headless host on
// {home} in {workspace} on route {route} with model {model} in the foreground;
// DSH_SCENARIO_STORE2_PROMPT sends {prompt} to a
// new session (when {session} is empty, printing "session=<id>") or to {session},
// returning once the prompt is accepted; DSH_SCENARIO_STORE2_RESUME makes the
// host resume {session}; DSH_SCENARIO_STORE2_REQUEST prints the body of every
// model request sent for {session}, in the order sent, one JSON document a line
// (store2Requests); DSH_SCENARIO_STORE2_KILL kills the host and
// any child it started without a clean stop; DSH_SCENARIO_STORE2_DELETE deletes
// {session} of {home} with no host running and returns when it is done. The route input is the Claude leg's
// DSH_SCENARIO_CLAUDE_CODE_MODEL (signedin_routes_test.go): the model the user's
// signed-in Claude Code accepts. The client's own sign-in is the only credential.
//
//dsh:scenario DH-E-STORE-2
func TestKilledHostRecoversHonestly(t *testing.T) {
	model := claudeCodeRoute.model(t)
	startT := need(t, "DSH_SCENARIO_STORE2_START", "command starting the headless host on {home} in {workspace}")
	promptT := need(t, "DSH_SCENARIO_STORE2_PROMPT", "command sending {prompt} to {session} (or a new session)")
	resumeT := need(t, "DSH_SCENARIO_STORE2_RESUME", "command making the host resume {session}")
	requestT := need(t, "DSH_SCENARIO_STORE2_REQUEST", "command printing every model request of {session}, one a line, in order")
	killT := need(t, "DSH_SCENARIO_STORE2_KILL", "command killing the host and its children")
	deleteT := need(t, "DSH_SCENARIO_STORE2_DELETE", "command deleting {session} with no host running")

	dir := t.TempDir()
	root, ws := filepath.Join(dir, "home"), filepath.Join(dir, "workspace")
	if err := home.Ensure(home.New(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("one file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(ws, "late.txt")
	var id string
	with := func(prompt string) map[string]string {
		return map[string]string{"home": root, "workspace": ws, "session": id, "prompt": prompt, "route": claudeCodeRoute.ID, "model": model}
	}
	kill := func() {
		if out, err := storeStep(t, "kill the host", expand(killT, with(""))); err != nil {
			t.Fatalf("killing the host: %v\n%s", err, out)
		}
	}
	t.Cleanup(func() { _, _ = shell(t, expand(killT, with(""))) })

	// (1)
	host := startShell(t, expand(startT, with("")))
	out, err := storeStep(t, "first prompt", expand(promptT, with(
		"Run exactly this shell command and nothing else: sleep 60 && echo done > late.txt")))
	m := storeSessionLine.FindStringSubmatch(out)
	if err != nil || m == nil {
		t.Fatalf("the first prompt was not accepted: %v\n%s", err, out)
	}
	id = m[1]

	// (2) The tool call is recorded once the log holds a tool/call: a fact only
	// the host process makes true, so it is polled (procwait, M1-D-GATE-24).
	var recorded []string
	ctx, cancel := context.WithTimeout(context.Background(), storeStepTimeout)
	defer cancel()
	if err := procwait.Poll(ctx, func() bool {
		recs, _ := storeLog(t, root, id)
		called := false
		recorded = recorded[:0]
		for _, r := range recs {
			recorded = append(recorded, r.Type)
			called = called || r.Type == "tool/call"
		}
		return called
	}); err != nil {
		t.Fatalf("no tool call recorded within %s: %v: %v", storeStepTimeout, recorded, err)
	}
	beforeKill, _ := storeLog(t, root, id)
	kill()
	select {
	case <-host.done:
	case <-time.After(storeStepTimeout):
		t.Fatal("the host did not exit after the kill")
	}

	// (3)
	host = startShell(t, expand(startT, with("")))
	if out, err := storeStep(t, "resume", expand(resumeT, with(""))); err != nil {
		t.Fatalf("the resume failed: %v\n%s", err, out)
	}
	recs, _ := storeLog(t, root, id)
	if len(recs) < len(beforeKill) {
		t.Fatalf("%d records before the kill, %d after the resume", len(beforeKill), len(recs))
	}
	for i, r := range beforeKill {
		if recs[i].Type != r.Type || recs[i].Payload != r.Payload {
			t.Fatalf("record %d recorded before the kill changed: %s, then %s", i, r.Type, recs[i].Type)
		}
	}
	var unknown, interrupted bool
	for _, r := range recs[len(beforeKill):] {
		switch {
		case r.Type == "tool/result" && strings.Contains(r.Payload, "tools/outcome-unknown"):
			unknown = true
		case r.Type == "turn/end" && strings.Contains(r.Payload, "interrupted"):
			interrupted = true
		}
	}
	if !unknown || !interrupted {
		t.Fatalf("after the resume: outcome-unknown result %v, interrupted turn %v", unknown, interrupted)
	}

	// (4) The requests already sent are the boundary: the one checked is the first
	// sent after the second prompt, not whichever is last when polled.
	const second = "What happened to the last command?"
	listed, err := shell(t, expand(requestT, with("")))
	if err != nil {
		t.Fatalf("listing the model requests of %s: %v\n%s", id, err, listed)
	}
	before := len(store2Requests(listed))
	if out, err := storeStep(t, "second prompt", expand(promptT, with(second))); err != nil {
		t.Fatalf("the second prompt was not accepted: %v\n%s", err, out)
	}
	// The host's requests are another process's state: polled.
	var (
		req      string
		ready    bool
		firstErr error
	)
	rctx, rcancel := context.WithTimeout(context.Background(), storeStepTimeout)
	defer rcancel()
	if err := procwait.Poll(rctx, func() bool {
		out, _ := shell(t, expand(requestT, with("")))
		req, ready, firstErr = store2FirstRequest(store2Requests(out), before, second)
		return ready
	}); err != nil {
		t.Fatalf("no model request after the second prompt within %s (%d before it): %v", storeStepTimeout, before, err)
	}
	if firstErr != nil {
		t.Fatalf("%v:\n%s", firstErr, req)
	}
	t.Logf("evidence: model request %d of %s, the first after the resume's prompt, carries the outcome-unknown result", before+1, id)
	kill()
	if _, err := os.Stat(marker); err == nil {
		t.Log("the killed host's command finished anyway; the result stays outcome-unknown")
	}
	select {
	case <-host.done:
	case <-time.After(storeStepTimeout):
		t.Fatal("the resumed host did not exit after the kill")
	}

	// (5)
	storeSeq(t, root, id)
	blobs := home.New(root).Blobs()
	var owned []string
	for _, f := range storeFiles(t, blobs) {
		owned = append(owned, filepath.Join(blobs, filepath.FromSlash(f)))
	}
	t.Logf("evidence: session %s before its deletion: spill files %v, blobs %v", id, storeFiles(t, storeSpillDir(root, id)), owned)
	storeTree(t, root, "before the deletion")
	if out, err := storeStep(t, "delete the session", expand(deleteT, with(""))); err != nil {
		t.Fatalf("deleting %s: %v\n%s", id, err, out)
	}
	storeGone(t, root, id, owned)
	if rep, _ := storeCheck(t, root); rep.Sessions != 0 || len(rep.Deleting) != 0 || !rep.Clean() {
		t.Fatalf("after the deletion the home holds %d sessions, deleting %v, problems %v %v", rep.Sessions, rep.Deleting, rep.Integrity, rep.Problems)
	}
	if left := append(storeFiles(t, blobs), storeFiles(t, home.New(root).Spill())...); len(left) != 0 {
		t.Fatalf("blob or spill files remain after the only session's deletion: %v", left)
	}
	storeTree(t, root, "after the deletion")
}
