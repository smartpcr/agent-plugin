//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// human6Ready finds the first page's address with its launch token in the output.
var human6Ready = regexp.MustCompile(`https?://[^\s"']+[?&#]token=[^\s"']+`)

// human6Item is one row of the agent's question list as the query hook prints it.
type human6Item struct {
	ID     string `json:"id"`
	Call   string `json:"call"`
	Status string `json:"status"`
}

// human6Log is what the log hook prints: the tool result of the asking call, the
// turns of the session in order, the user messages that name a call, and the
// client named by the invocation each model settlement (assistant/message and
// assistant/attempt) records, in log order.
type human6Log struct {
	Result struct {
		Status string `json:"status"`
	} `json:"result"`
	ContinuedAfterResult bool `json:"continuedAfterResult"`
	Answers              []struct {
		Call      string `json:"call"`
		StartedBy string `json:"startedTurn"`
	} `json:"answers"`
	Clients []string `json:"clients"`
}

// TestQuestionTimesOutSurvivesRestartAndIsAnsweredOnce: the model asks the user
// one question with a deadline and moves on; the answer, given after the host was
// killed and restarted, arrives once through the inbox (areas/human.md
// DH-E-HUMAN-6; DH-D-HUMAN-5, DH-D-HUMAN-6, DH-D-AGENT-2; SP-5; on the signed-in
// client route of areas/llm.md DH-D-LLM-12). Authored in DH-S-10 and retargeted
// onto the signed-in client routes by gap-closure-plan 18.1.1 (M1-D-CLOSE-4 A);
// executed in DH-S-22, which requires the agent loop (DH-S-11), the headless
// launcher (DH-S-16) and the web UI (DH-S-21).
//
// Given the built dsh, a fresh temporary home, no API key anywhere, the user's
// own installed official client signed in as themselves with its route
// registered by a layer as the default route of a new session, profile web and
// a prompt that asks the model to ask the user one question with
// ask_user_question and a 10 s timeout and then continue, when, per route, (1)
// the deadline passes, (2) the host is killed and started again on the same
// home and (3) the question is answered in the browser, then the tool result of
// the asking call is pending and the turn continued after it; after the restart
// the question is listed as continued; the answer is one user message that
// names the asking call and starts a turn; (4) a second answer to the same
// question is refused; and every model settlement of the session records the
// invocation of the route's client (the model was reached only through it). The
// client runs only while an attempt runs and returns ask_user_question
// unexecuted (M1-D-PIPE-6 A), so the question, its deadline and its answer are
// dsh's; a kill during an attempt stops the client with the host's tree.
// Cleanup: the hosts are stopped and the home deleted; nothing of the user's
// sign-in is touched; on failure the hooks' output is logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_HUMAN6_LAYER a web layer, a template over {route} and
// {model}, that registers that client route and makes it, with model {model},
// the route of a session started in the browser; DSH_SCENARIO_<route>_MODEL
// (<route> is CLAUDE_CODE or COPILOT_CLI) the model; DSH_SCENARIO_HUMAN6_PROMPT
// the prompt; DSH_SCENARIO_HUMAN6_SEND a command line over {page} (the first
// page with its token) that sends the prompt in the browser and prints the
// session id; DSH_SCENARIO_HUMAN6_LIST a command line over {page} and {session}
// that prints the agent's questions as a JSON array of {id, call, status};
// DSH_SCENARIO_HUMAN6_ANSWER a command line over {page}, {session}, {question}
// and {text} that answers in the browser and exits non-zero when the answer is
// refused; DSH_SCENARIO_HUMAN6_LOG a command line over {home} and {session} that
// prints the session's log facts (human6Log); DSH_SCENARIO_HUMAN6_BOUND the
// bound for each wait (a Go duration). The client is the one on the PATH,
// signed in beforehand by the user.
//
//dsh:scenario DH-E-HUMAN-6
func TestQuestionTimesOutSurvivesRestartAndIsAnsweredOnce(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_HUMAN6_LAYER", "a web layer over {route} and {model} that makes that client route a new session's route")
	prompt := need(t, "DSH_SCENARIO_HUMAN6_PROMPT", "a prompt that asks one question with a 10s timeout and continues")
	sendT := need(t, "DSH_SCENARIO_HUMAN6_SEND", "a browser driver that sends {prompt} on {page}")
	listT := need(t, "DSH_SCENARIO_HUMAN6_LIST", "a command that lists {session}'s questions")
	answerT := need(t, "DSH_SCENARIO_HUMAN6_ANSWER", "a browser driver that answers {question}")
	logT := need(t, "DSH_SCENARIO_HUMAN6_LOG", "a command that prints {session}'s log facts")
	bound, err := time.ParseDuration(need(t, "DSH_SCENARIO_HUMAN6_BOUND", "the bound for each wait, a Go duration"))
	if err != nil {
		t.Fatalf("DSH_SCENARIO_HUMAN6_BOUND: %v", err)
	}
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		layer := expand(layerT, map[string]string{"route": r.ID, "model": r.model(t)})
		questionAnsweredOnce(t, bin, r, layer, prompt, sendT, listT, answerT, logT, bound)
	})
}

// questionAnsweredOnce is DH-E-HUMAN-6 on route r.
func questionAnsweredOnce(t *testing.T, bin string, r clientRoute, layer, prompt, sendT, listT, answerT, logT string, bound time.Duration) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	// No entry: the launcher's scrubbed environment holds no credential, and the
	// client finds the user's own sign-in in its configuration home.
	var env []string
	args := []string{"--home", home, "--layer", writeLayer(t, layer), "web"}
	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}

	host := startDsh(t, env, bin, args...)
	page := host.waitFor(t, bound, "readiness with a launch token", human6Ready.FindString)
	session := strings.TrimSpace(mustShell(t, expand(sendT, map[string]string{"page": page, "prompt": promptFile})))
	vars := func(extra map[string]string) map[string]string {
		m := map[string]string{"page": page, "session": session, "home": home}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// (1) The deadline passes: the call returned pending and the turn went on.
	var asked human6Item
	waitUntil(t, bound, "a question listed as pending or continued", func() bool {
		items := human6List(t, expand(listT, vars(nil)))
		if len(items) == 1 {
			asked = items[0]
		}
		return len(items) == 1 && (asked.Status == "pending" || asked.Status == "continued")
	})
	var facts human6Log
	waitUntil(t, bound, "the turn continuing after the pending result", func() bool {
		facts = human6Facts(t, expand(logT, vars(nil)))
		return facts.ContinuedAfterResult
	})
	if facts.Result.Status != "pending" {
		t.Errorf("the asking call's result is %q, want pending", facts.Result.Status)
	}

	// (2) Kill and restart on the same home.
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	if err := host.Process.StopTree(ctx, 0); err != nil {
		t.Fatalf("killing the host: %v", err)
	}
	host = startDsh(t, env, bin, args...)
	page = host.waitFor(t, bound, "readiness after the restart", human6Ready.FindString)
	items := human6List(t, expand(listT, vars(nil)))
	if len(items) != 1 || items[0].ID != asked.ID || items[0].Status != "continued" {
		t.Fatalf("after the restart the questions are %+v, want %s continued", items, asked.ID)
	}

	// (3) Answer in the browser.
	mustShell(t, expand(answerT, vars(map[string]string{"question": asked.ID, "text": "blue"})))
	waitUntil(t, bound, "the answer starting a turn", func() bool {
		facts = human6Facts(t, expand(logT, vars(nil)))
		return len(facts.Answers) > 0 && facts.Answers[0].StartedBy != ""
	})
	if len(facts.Answers) != 1 || facts.Answers[0].Call != asked.Call {
		t.Errorf("answers %+v, want one naming call %s", facts.Answers, asked.Call)
	}

	// (4) A second answer is refused.
	if out, err := shell(t, expand(answerT, vars(map[string]string{"question": asked.ID, "text": "red"}))); err == nil {
		t.Errorf("a second answer was accepted:\n%s", out)
	}
	if facts = human6Facts(t, expand(logT, vars(nil))); len(facts.Answers) != 1 {
		t.Errorf("after the second answer there are %d answer messages, want 1", len(facts.Answers))
	}

	// The model was reached only through the route's signed-in client.
	if len(facts.Clients) == 0 {
		t.Errorf("the log facts name no settlement's client invocation, want one of client %s per settlement", r.Client)
	}
	for i, c := range facts.Clients {
		if c != r.Client {
			t.Errorf("settlement %d records an invocation of client %q, want %s", i, c, r.Client)
		}
	}
}

func human6List(t *testing.T, line string) []human6Item {
	t.Helper()
	var items []human6Item
	if err := json.Unmarshal([]byte(mustShell(t, line)), &items); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return items
}

func human6Facts(t *testing.T, line string) human6Log {
	t.Helper()
	var f human6Log
	if err := json.Unmarshal([]byte(mustShell(t, line)), &f); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return f
}

// waitUntil polls cond every 200ms until it holds or within passes.
func waitUntil(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(within)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("no %s within %s", what, within)
		}
	}
}
