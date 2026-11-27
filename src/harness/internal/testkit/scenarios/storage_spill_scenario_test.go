//go:build scenario

package scenarios

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session/event until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session/store until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/home until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/spill until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
)

var storageNotSaved = regexp.MustCompile(`(?i)not\s+(been\s+)?saved`)

// spillResults returns the tool results of a session's log, decoded, with the
// size of each logged row.
func spillResults(t *testing.T, recs []store.Record) (results []event.ToolResultPayload, sizes []int, calls []event.ToolCallPayload, turnEnded bool) {
	t.Helper()
	for _, rec := range recs {
		switch rec.Type {
		case "tool/result":
			var p event.ToolResultPayload
			if err := json.Unmarshal([]byte(rec.Payload), &p); err != nil {
				t.Fatalf("decoding the tool result at seq %d: %v", rec.Seq, err)
			}
			results, sizes = append(results, p), append(sizes, len(rec.Payload))
		case "tool/call":
			var p event.ToolCallPayload
			if err := json.Unmarshal([]byte(rec.Payload), &p); err == nil {
				calls = append(calls, p)
			}
		case "turn/end":
			turnEnded = true
		}
	}
	return results, sizes, calls, turnEnded
}

// TestFiveMegabyteOutputBoundedAndRecoverable: a five-megabyte command output is
// bounded in the log and recoverable from the spill store, and a failed save
// still bounds it (areas/storage.md DH-E-STORAGE-3; DH-D-STORAGE-5,
// DH-D-STORAGE-6, DH-ADR-STORAGE-4). Guarded: it needs the real sandbox backend
// and a real model API, so it runs in DH-S-39 once per operating system
// (Linux, macOS, Windows); authored in DH-S-8.
//
// Given the built binary started as a headless host on an empty home with the
// real sandbox backend in workspace-write mode and a real model API, and a
// workspace file that prints 5 MB of numbered lines with a marker on line
// 40,000, when (1) the model is asked to run the command and report the marker
// line and (2) the spill directory is made read-only and the request repeated in
// a new session, then in (1) every logged tool result is within the budget, one
// carries the spill field, the saved file equals the full output (and is 0600 on
// POSIX), and the model reports the marker after reading or searching the saved
// file with its file tools; in (2) the result is still within the budget, has no
// spill field, is not an error, its notice says the full output was not saved
// and the turn completes. Each process step has a 60 second timeout.
//
// Inputs are shell command lines over {home}, {spill} and {session}:
// DSH_SCENARIO_STORAGE3_START starts the headless host on {home} in the
// foreground; DSH_SCENARIO_STORAGE3_ASK starts a new session in the fixture
// workspace, asks the model to run the printing command and report the marker
// line, waits for the turn, and prints "session=<id>" and the model's answer;
// DSH_SCENARIO_STORAGE3_FULL runs the printing command directly and prints its
// output; DSH_SCENARIO_STORAGE3_LOCK and DSH_SCENARIO_STORAGE3_UNLOCK make
// {spill} read-only and restore it; DSH_SCENARIO_STORAGE3_DELETE deletes
// {session}; DSH_SCENARIO_STORAGE3_STOP stops the host. DSH_SCENARIO_STORAGE3_MARKER
// is the marker text and DSH_SCENARIO_STORAGE3_BUDGET the tool-result budget in
// bytes.
//
//dsh:scenario DH-E-STORAGE-3
func TestFiveMegabyteOutputBoundedAndRecoverable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	l := home.New(root)
	startT := need(t, "DSH_SCENARIO_STORAGE3_START", "command starting the headless host on {home}")
	askT := need(t, "DSH_SCENARIO_STORAGE3_ASK", "command asking a new session to run the printing command")
	fullT := need(t, "DSH_SCENARIO_STORAGE3_FULL", "command printing the full 5 MB output directly")
	lockT := need(t, "DSH_SCENARIO_STORAGE3_LOCK", "command making {spill} read-only")
	unlockT := need(t, "DSH_SCENARIO_STORAGE3_UNLOCK", "command restoring {spill}")
	deleteT := need(t, "DSH_SCENARIO_STORAGE3_DELETE", "command deleting {session}")
	stopT := need(t, "DSH_SCENARIO_STORAGE3_STOP", "command stopping the host")
	marker := need(t, "DSH_SCENARIO_STORAGE3_MARKER", "the marker text on line 40,000")
	budget, err := strconv.Atoi(need(t, "DSH_SCENARIO_STORAGE3_BUDGET", "the tool-result budget in bytes"))
	if err != nil || budget <= 0 {
		t.Fatalf("DSH_SCENARIO_STORAGE3_BUDGET is not a positive number of bytes: %v", err)
	}
	with := func(id string) map[string]string {
		return map[string]string{"home": root, "spill": l.Spill(), "session": id}
	}
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	keep, locked := false, false
	var sessions []string
	t.Cleanup(func() {
		if locked {
			_, _ = storeStep(t, "unlock the spill directory", expand(unlockT, with("")))
		}
		if keep || t.Failed() {
			t.Logf("kept the home, its spill directory and sessions %v at %s", sessions, root)
			_, _ = storeStep(t, "stop the host", expand(stopT, with("")))
			return
		}
		for _, id := range sessions {
			_, _ = storeStep(t, "delete "+id, expand(deleteT, with(id)))
		}
		_, _ = storeStep(t, "stop the host", expand(stopT, with("")))
		_ = os.RemoveAll(root)
	})
	full, err := storeStep(t, "print the full output", fullT)
	if err != nil || len(full) < 5<<20 || !strings.Contains(full, marker) {
		t.Fatalf("the fixture must print at least 5 MB with the marker: %d bytes, %v", len(full), err)
	}
	startShell(t, expand(startT, with("")))
	ask := func(what string) (string, string) {
		out, err := storeStep(t, what, expand(askT, with("")))
		m := storeSessionLine.FindStringSubmatch(out)
		if err != nil || m == nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		sessions = append(sessions, m[1])
		return m[1], out
	}
	withinBudget := func(id string, sizes []int) {
		for i, n := range sizes {
			if n > budget+4096 {
				keep = true
				t.Fatalf("%s: tool result %d is %d bytes logged, the budget is %d", id, i, n, budget)
			}
		}
	}

	// (1)
	id, answer := ask("ask the first session")
	recs, _ := storeLog(t, root, id)
	results, sizes, calls, _ := spillResults(t, recs)
	withinBudget(id, sizes)
	var ref *event.Spill
	for _, r := range results {
		if r.Spill != nil {
			ref = r.Spill
			break
		}
	}
	if ref == nil {
		keep = true
		t.Fatalf("%s: no tool result carries the spill field", id)
	}
	if _, _, err := spill.ParseID(ref.Path); err != nil {
		keep = true
		t.Fatalf("%s: the spill field %q is not a spill id: %v", id, ref.Path, err)
	}
	saved := filepath.Join(l.Spill(), filepath.FromSlash(ref.Path))
	got, err := os.ReadFile(saved)
	if err != nil || !bytes.Equal(got, []byte(full)) || ref.Bytes != int64(len(full)) {
		keep = true
		t.Fatalf("%s: the saved file (%d bytes, %v) does not equal the %d byte output", saved, len(got), err, len(full))
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
			keep = true
			t.Fatalf("%s: mode %v, want 0600 (%v)", saved, info.Mode().Perm(), err)
		}
	} else if err := home.CheckPrivate(saved); err != nil {
		keep = true
		t.Fatalf("%s is not private: %v", saved, err)
	}
	followed := false
	for _, c := range calls {
		followed = followed || strings.Contains(filepath.ToSlash(c.Arguments), filepath.Base(ref.Path))
	}
	if !followed || !strings.Contains(answer, marker) {
		keep = true
		t.Fatalf("%s: the model did not find the marker through the saved file (followed %v)\n%s", id, followed, answer)
	}

	// (2)
	if out, err := storeStep(t, "lock the spill directory", expand(lockT, with(""))); err != nil {
		t.Fatalf("making %s read-only: %v\n%s", l.Spill(), err, out)
	}
	locked = true
	id, _ = ask("ask the second session")
	recs, _ = storeLog(t, root, id)
	results, sizes, _, ended := spillResults(t, recs)
	withinBudget(id, sizes)
	if len(results) == 0 || !ended {
		keep = true
		t.Fatalf("%s: %d tool results, turn ended %v", id, len(results), ended)
	}
	first := results[0]
	text, _ := json.Marshal(first.Message)
	if first.Spill != nil || first.IsError || !storageNotSaved.Match(text) {
		keep = true
		t.Fatalf("%s: with the spill directory read-only the result is spill %+v, error %v, notice %s", id, first.Spill, first.IsError, text)
	}
}
