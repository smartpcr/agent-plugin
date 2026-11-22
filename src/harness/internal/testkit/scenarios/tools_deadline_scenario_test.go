//go:build scenario

package scenarios

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tools3Budget is the shell call's budget the scenario asks for.
const tools3Budget = 5 * time.Second

// tools3Margin is the "small margin" over budget plus grace.
const tools3Margin = 3 * time.Second

var tools3Process = regexp.MustCompile(`(?m)^\s*(\d+)\s+(.*\S)\s*$`)

// tools3Call is what the log says of one tool call: its tool/call and
// tool/result in order, with their times.
type tools3Call struct {
	id, name     string
	called, done time.Time
	result       string
	answered     bool
}

// tools3Record is one line the log hook prints: a log record of the session,
// its time in Unix milliseconds and its payload as the JSON text the log holds.
// The scenario reads the log through the hook, never through the product's
// internal packages (it is authored ahead of the stages it requires).
type tools3Record struct {
	Seq     int64  `json:"seq"`
	Type    string `json:"type"`
	Time    int64  `json:"time"`
	Payload string `json:"payload"`
}

// tools3CallPayload and tools3ResultPayload are the fields of tool/call and
// tool/result payloads the scenario reads (core-session.md).
type tools3CallPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
}

type tools3ResultPayload struct {
	CallID string `json:"call_id"`
}

// tools3Log runs the log hook and decodes its lines.
func tools3Log(t *testing.T, line string) []tools3Record {
	t.Helper()
	out, err := storeStep(t, "print the session's log", line)
	if err != nil {
		t.Fatalf("printing the session's log: %v\n%s", err, out)
	}
	var recs []tools3Record
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var rec tools3Record
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			t.Fatalf("decoding the log line %q: %v", text, err)
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the session's log: %v", err)
	}
	return recs
}

// tools3Calls returns the tool calls of a session's log in their order.
func tools3Calls(t *testing.T, recs []tools3Record) []*tools3Call {
	t.Helper()
	var evs []protocol.Event
	for i, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var e protocol.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("the log's line %d is not an SDK event: %v\n%s", i+1, err, line)
		}
		evs = append(evs, e)
	}
	return evs
}

// tools3Calls returns the tool calls of a session's log in their order.
func tools3Calls(t *testing.T, evs []protocol.Event) []*tools3Call {
	t.Helper()
	at := func(e protocol.Event) time.Time {
		ts, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil {
			t.Fatalf("the time of %s at seq %d: %v", e.Type, e.Seq, err)
		}
		return ts
	}
	var out []*tools3Call
	byID := map[string]*tools3Call{}
	for _, e := range evs {
		var p struct {
			CallID string `json:"call_id"`
			Name   string `json:"name"`
		}
		switch e.Type {
		case "tool/call":
			var p tools3CallPayload
			if err := json.Unmarshal([]byte(rec.Payload), &p); err != nil {
				t.Fatalf("decoding the tool call at seq %d: %v", rec.Seq, err)
			}
			c := &tools3Call{id: p.CallID, name: p.Name, called: rec.Time}
			byID[p.CallID] = c
			out = append(out, c)
		case "tool/result":
			var p tools3ResultPayload
			if err := json.Unmarshal([]byte(rec.Payload), &p); err != nil {
				t.Fatalf("decoding the tool result at seq %d: %v", rec.Seq, err)
			}
			if c := byID[p.CallID]; c != nil {
				c.done, c.result, c.answered = at(e), string(e.Data), true
			}
		}
	}
	return out
}

// TestDeadlineStopsCommandThatIgnoresTermination: a command that ignores
// termination is stopped at its deadline and leaves no process
// (areas/tools.md DH-E-TOOLS-3; DH-D-TOOLS-3, DH-ADR-TOOLS-5). Guarded: it
// needs a real machine per operating system and the real shell tool (A09), so it
// runs in DH-S-39 once per operating system; authored in DH-S-10.
//
// Given the built binary started as a headless host on an empty home, and a
// shell tool call with a 5 second budget running a fixture program that ignores
// the polite stop request and starts one child (each writes its pid under
// {pids}), when (1) the call runs to its deadline and (2) an identical call is
// started and the turn cancelled while it runs, then in (1) the result is
// tools/timeout within the budget plus grace plus a small margin, no process of
// the run remains, and the next tool call of the turn is served; in (2) the
// result is tools/aborted within the same bound and no process remains. Each
// process step has a 60 second timeout.
//
// Inputs are shell command lines over {home}, {pids}, {mode}, {session} and
// {pid}: DSH_SCENARIO_TOOLS3_START starts the headless host on {home} in the
// foreground; DSH_SCENARIO_TOOLS3_CALL starts a new session that makes the shell
// call of the fixture with a 5 second budget and then one more tool call, with
// {mode} "deadline" (the call runs to its budget) or "cancel" (the turn is
// cancelled one second into the call), waits for the turn and prints
// "session=<id>"; DSH_SCENARIO_TOOLS3_LOG prints the log of {session} on {home}
// as one SDK protocol.Event per line (the wire shape of session/history);
// DSH_SCENARIO_TOOLS3_PS prints "<pid> <command line>" for each
// process whose pid is recorded under {pids} and is still running;
// DSH_SCENARIO_TOOLS3_LOG prints {session}'s log on {home}, one JSON object
// {"seq","type","time" (Unix ms),"payload" (the payload's JSON text)} per line;
// DSH_SCENARIO_TOOLS3_KILL kills {pid}; DSH_SCENARIO_TOOLS3_DELETE deletes
// {session}; DSH_SCENARIO_TOOLS3_STOP stops the host. DSH_SCENARIO_TOOLS3_GRACE
// is the registry's grace in seconds.
//
//dsh:scenario DH-E-TOOLS-3
func TestDeadlineStopsCommandThatIgnoresTermination(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	startT := need(t, "DSH_SCENARIO_TOOLS3_START", "command starting the headless host on {home}")
	callT := need(t, "DSH_SCENARIO_TOOLS3_CALL", "command running the fixture's shell call in a new session in {mode}")
	logT := need(t, "DSH_SCENARIO_TOOLS3_LOG", "command printing the log of {session} as SDK events, one per line")
	psT := need(t, "DSH_SCENARIO_TOOLS3_PS", "command listing the live processes whose pids are under {pids}")
	logT := need(t, "DSH_SCENARIO_TOOLS3_LOG", "command printing {session}'s log as JSON lines")
	killT := need(t, "DSH_SCENARIO_TOOLS3_KILL", "command killing {pid}")
	deleteT := need(t, "DSH_SCENARIO_TOOLS3_DELETE", "command deleting {session}")
	stopT := need(t, "DSH_SCENARIO_TOOLS3_STOP", "command stopping the host")
	graceS, err := strconv.Atoi(need(t, "DSH_SCENARIO_TOOLS3_GRACE", "the registry's grace in seconds"))
	if err != nil || graceS <= 0 {
		t.Fatalf("DSH_SCENARIO_TOOLS3_GRACE is not a positive number of seconds: %v", err)
	}
	bound := tools3Budget + time.Duration(graceS)*time.Second + tools3Margin
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	pidsRoot := filepath.Join(t.TempDir(), "pids")
	with := func(mode, id, pid string) map[string]string {
		return map[string]string{"home": root, "pids": filepath.Join(pidsRoot, mode), "mode": mode, "session": id, "pid": pid}
	}
	var sessions []string
	t.Cleanup(func() {
		if !t.Failed() {
			for _, id := range sessions {
				_, _ = storeStep(t, "delete "+id, expand(deleteT, with("", id, "")))
			}
		}
		_, _ = storeStep(t, "stop the host", expand(stopT, with("", "", "")))
	})
	startShell(t, expand(startT, with("", "", "")))

	for _, step := range []struct {
		mode, code string
	}{
		{"deadline", "tools/timeout"},
		{"cancel", "tools/aborted"},
	} {
		if err := os.MkdirAll(filepath.Join(pidsRoot, step.mode), 0o700); err != nil {
			t.Fatal(err)
		}
		out, err := storeStep(t, "the "+step.mode+" call", expand(callT, with(step.mode, "", "")))
		m := storeSessionLine.FindStringSubmatch(out)
		if err != nil || m == nil {
			t.Fatalf("the %s call: %v\n%s", step.mode, err, out)
		}
		sessions = append(sessions, m[1])
		calls := tools3Calls(t, tools3Log(t, expand(logT, with(step.mode, m[1], ""))))
		if len(calls) == 0 || !calls[0].answered {
			t.Fatalf("%s: the shell call has no result: %+v", step.mode, calls)
		}
		first := calls[0]
		if !strings.Contains(first.result, step.code) {
			t.Errorf("%s: the shell call's result is not %s: %s", step.mode, step.code, first.result)
		}
		if took := first.done.Sub(first.called); took > bound {
			t.Errorf("%s: the result came %s after the call, the bound is %s", step.mode, took, bound)
		}
		if step.mode == "deadline" && (len(calls) < 2 || !calls[1].answered) {
			t.Errorf("deadline: the turn did not continue to its next tool call: %d calls", len(calls))
		}

		// No process of the run remains; cleanup records and kills any that does.
		left, err := storeStep(t, "list the "+step.mode+" run's processes", expand(psT, with(step.mode, "", "")))
		if err != nil {
			t.Fatalf("listing the %s run's processes: %v\n%s", step.mode, err, left)
		}
		for _, p := range tools3Process.FindAllStringSubmatch(left, -1) {
			t.Errorf("%s: process %s (%s) of the run remains", step.mode, p[1], p[2])
			_, _ = storeStep(t, "kill "+p[1], expand(killT, with(step.mode, "", p[1])))
		}
	}
}
