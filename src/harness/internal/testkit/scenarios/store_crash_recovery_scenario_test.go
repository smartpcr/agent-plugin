//go:build scenario

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
)

// TestKilledHostRecoversHonestly: a host killed mid-turn, while a tool body is
// running, recovers honestly when the session is resumed (areas/session-store.md
// DH-E-STORE-2; DH-D-STORE-4, DH-D-STORE-2). Guarded; executed in DH-S-16 (step
// 16.8) on scratch homes; authored in DH-S-7.
//
// Given the built binary started as a headless host with a real model API key, a
// real shell under the platform's sandbox, an empty home and a workspace with one
// file, when (1) the agent is prompted to run a shell command that sleeps for a
// minute and then writes a file, (2) the host is killed once the tool call is
// recorded, (3) the host is started again and resumes the session and (4) a new
// prompt is sent, then after (3) the session shows the turn as interrupted and
// the tool call with an outcome-unknown result, no event recorded before the kill
// is missing, and the first model request after (4) contains that result.
//
// Inputs are shell command lines over {home}, {workspace}, {session} and
// {prompt}: DSH_SCENARIO_STORE2_START starts the headless host on {home} in
// {workspace} in the foreground; DSH_SCENARIO_STORE2_PROMPT sends {prompt} to a
// new session (when {session} is empty, printing "session=<id>") or to {session},
// returning once the prompt is accepted; DSH_SCENARIO_STORE2_RESUME makes the
// host resume {session}; DSH_SCENARIO_STORE2_REQUEST prints the body of the last
// model request sent for {session}; DSH_SCENARIO_STORE2_KILL kills the host and
// any child it started without a clean stop. DSH_SCENARIO_STORE2_KEY is the
// model API key, checked here and passed to the commands by the environment.
//
//dsh:scenario DH-E-STORE-2
func TestKilledHostRecoversHonestly(t *testing.T) {
	need(t, "DSH_SCENARIO_STORE2_KEY", "the real model API key")
	startT := need(t, "DSH_SCENARIO_STORE2_START", "command starting the headless host on {home} in {workspace}")
	promptT := need(t, "DSH_SCENARIO_STORE2_PROMPT", "command sending {prompt} to {session} (or a new session)")
	resumeT := need(t, "DSH_SCENARIO_STORE2_RESUME", "command making the host resume {session}")
	requestT := need(t, "DSH_SCENARIO_STORE2_REQUEST", "command printing the last model request of {session}")
	killT := need(t, "DSH_SCENARIO_STORE2_KILL", "command killing the host and its children")

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
		return map[string]string{"home": root, "workspace": ws, "session": id, "prompt": prompt}
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

	// (4)
	if out, err := storeStep(t, "second prompt", expand(promptT, with("What happened to the last command?"))); err != nil {
		t.Fatalf("the second prompt was not accepted: %v\n%s", err, out)
	}
	// The host's last model request is another process's state: polled.
	var req string
	rctx, rcancel := context.WithTimeout(context.Background(), storeStepTimeout)
	defer rcancel()
	if err := procwait.Poll(rctx, func() bool {
		req, _ = shell(t, expand(requestT, with("")))
		return strings.Contains(req, "What happened to the last command?")
	}); err != nil {
		t.Fatalf("no model request for the second prompt within %s: %v", storeStepTimeout, err)
	}
	if !strings.Contains(req, "tools/outcome-unknown") && !strings.Contains(strings.ToLower(req), "outcome unknown") {
		t.Fatalf("the first model request after the resume lacks the interrupted call's result:\n%s", req)
	}
	kill()
	if _, err := os.Stat(marker); err == nil {
		t.Log("the killed host's command finished anyway; the result stays outcome-unknown")
	}
}
