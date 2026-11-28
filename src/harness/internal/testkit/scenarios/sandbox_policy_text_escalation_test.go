//go:build scenario

package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestSandbox7CountsOnlyEscalatedRetriesOfDeniedCalls checks SANDBOX-7's
// escalation count: only an approval of a retry of a sandbox-denied call, with
// the same arguments plus a wider sandbox_permissions and a justification the
// approval shows, counts; an ordinary tool approval with a reason does not.
func TestSandbox7CountsOnlyEscalatedRetriesOfDeniedCalls(t *testing.T) {
	write := func(id string, call, result int64, args map[string]any, raw string, isErr bool) *toolOutcome {
		a, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return &toolOutcome{ID: id, Name: "write", Args: a, Call: call, Result: result, IsError: isErr, Raw: json.RawMessage(raw)}
	}
	base := map[string]any{"file_path": "/outside/f", "content": "x"}
	with := func(perm, just string) map[string]any {
		m := map[string]any{"sandbox_permissions": perm, "justification": just}
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	denied := write("c1", 1, 2, base, `"fs/sandbox-denied: outside the workspace"`, true)
	approve := func(id, reason string) []protocol.ApprovalRequest {
		return []protocol.ApprovalRequest{{CallID: id, ToolName: "write", Reason: reason}}
	}
	cases := []struct {
		name  string
		outs  []*toolOutcome
		asked []protocol.ApprovalRequest
		want  bool
	}{
		{"escalated retry of a denied call", []*toolOutcome{denied, write("c2", 3, 4, with("danger-full-access", "write the report"), `"ok"`, false)},
			approve("c2", "write the report"), true},
		{"ordinary approval with a reason", []*toolOutcome{write("c1", 1, 2, base, `"ok"`, false)},
			approve("c1", "tool policy asks before write"), false},
		{"ordinary approval after a denial", []*toolOutcome{denied, write("c2", 3, 4, base, `"ok"`, false)},
			approve("c2", "tool policy asks before write"), false},
		{"escalation without a denial first", []*toolOutcome{write("c2", 3, 4, with("danger-full-access", "j"), `"ok"`, false)},
			approve("c2", "j"), false},
		{"denied by tool policy, not the sandbox", []*toolOutcome{write("c1", 1, 2, base, `"tools/denied"`, true), write("c2", 3, 4, with("danger-full-access", "j"), `"ok"`, false)},
			approve("c2", "j"), false},
		{"retry with other arguments", []*toolOutcome{denied, write("c2", 3, 4, map[string]any{"file_path": "/outside/g", "content": "x", "sandbox_permissions": "danger-full-access", "justification": "j"}, `"ok"`, false)},
			approve("c2", "j"), false},
		{"not a wider mode", []*toolOutcome{denied, write("c2", 3, 4, with("workspace-write", "j"), `"ok"`, false)},
			approve("c2", "j"), false},
		{"no justification", []*toolOutcome{denied, write("c2", 3, 4, with("danger-full-access", " "), `"ok"`, false)},
			approve("c2", "some reason"), false},
		{"approval does not show the justification", []*toolOutcome{denied, write("c2", 3, 4, with("danger-full-access", "write the report"), `"ok"`, false)},
			approve("c2", "other"), false},
		{"approval of another call", []*toolOutcome{denied, write("c2", 3, 4, with("danger-full-access", "j"), `"ok"`, false)},
			approve("c9", "j"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if why, got := sandbox7Escalated(c.outs, c.asked); got != c.want {
				t.Fatalf("counted %v (%s), want %v", got, why, c.want)
			}
		})
	}
}

// sandbox7FakeTB records the errors sandbox7Judge reports.
type sandbox7FakeTB struct {
	testing.TB
	errors []string
}

func (f *sandbox7FakeTB) Helper() {}
func (f *sandbox7FakeTB) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

// TestSandbox7KeepsEveryTranscriptOfATextBelowTheFloor: runs that each pass
// alone (a run that does not escalate only logs) still leave their transcripts
// when the text's aggregate count fails the floor (TC-20s); a text at the floor
// keeps none and does not fail.
func TestSandbox7KeepsEveryTranscriptOfATextBelowTheFloor(t *testing.T) {
	sessions := []string{"s1", "s2", "s3"}
	for _, c := range []struct {
		got, want int
		fails     bool
	}{{9, sandbox7Floor, true}, {sandbox7Floor, sandbox7Floor, false}, {11, 12, true}} {
		var kept []string
		tb := &sandbox7FakeTB{}
		sandbox7Judge(tb, "linux", c.got, c.want, sessions, func(s string) { kept = append(kept, s) })
		if c.fails {
			if len(tb.errors) != 1 || !strings.Contains(tb.errors[0], fmt.Sprintf("the linux text: %d of 18", c.got)) {
				t.Errorf("%d of want %d: errors %q", c.got, c.want, tb.errors)
			}
			if !slices.Equal(kept, sessions) {
				t.Errorf("%d of want %d: kept %v, want every run %v", c.got, c.want, kept, sessions)
			}
		} else if len(tb.errors) != 0 || len(kept) != 0 {
			t.Errorf("%d of want %d: errors %q, kept %v; want neither", c.got, c.want, tb.errors, kept)
		}
	}
}

// The report TestPolicyTextMakesTheModelEscalate writes is the one
// "harness-gate sandbox7-record" reads: for the draft texts and fixed counts its
// writer produces, byte for byte, the gate's
// testdata/policytext/sandbox7_scenario/s17-report.txt.detail (the file
// scenario.Main moves DSH_SCENARIO_REPORT to), which the gate's
// TestSandbox7RecordReadsTheScenarioDetailReport parses, and the provenance
// line it logs is in that directory's go test log. A shard reports only its
// text, and a missing or unreadable text stops the run before any model call.
func TestSandbox7ReportIsWhatTheGateReads(t *testing.T) {
	root := repositoryRoot(t)
	fixture := filepath.Join(root, "tools", "harness-gate", "testdata", "policytext", "sandbox7_scenario")
	want, err := os.ReadFile(filepath.Join(fixture, "s17-report.txt"+scenario.DetailSuffix))
	if err != nil {
		t.Fatal(err)
	}
	// On main the texts are the drafts; on s17/policy-text runbook step A has
	// moved the same bytes to text/, which is what the scenario reads.
	policy := filepath.Join(root, "src", "harness", "internal", "world", "sandbox", "policy")
	dir := filepath.Join(policy, "text")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		dir = filepath.Join(policy, "testdata", "draft_text")
	}
	texts, err := sandbox7ReadTexts(dir)
	if err != nil {
		t.Fatal(err)
	}
	run := sandbox7Run{attempt: "s17-full-20260101T000000Z", commit: "0123456789abcdef0123456789abcdef01234567", texts: texts}
	all := map[string]bool{"linux": true, "darwin": true, "windows": true}
	counts := map[string]int{"linux": 12, "darwin": 11, "windows": 13}
	if got := sandbox7ReportBody(run, all, counts); got != string(want) {
		t.Errorf("the report is\n%s\nwant (the gate's fixture)\n%s", got, want)
	}
	log, err := os.ReadFile(filepath.Join(fixture, "go-test.log"))
	if err != nil {
		t.Fatal(err)
	}
	if line := sandbox7ProvenanceLine(run.attempt, run.commit); !strings.Contains(string(log), line) {
		t.Errorf("the gate's go test log has no %q", line)
	}

	shard := sandbox7ReportBody(run, map[string]bool{"darwin": true}, counts)
	if wantShard := "darwin=11\ncommit: " + run.commit + "\nattempt: " + run.attempt + "\ntext: darwin.md sha256=" + texts["darwin"] + "\n"; shard != wantShard {
		t.Errorf("the darwin shard reports\n%s\nwant\n%s", shard, wantShard)
	}

	missing := t.TempDir()
	for _, p := range []string{"linux", "darwin"} {
		if err := os.WriteFile(filepath.Join(missing, p+".md"), []byte("text\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sandbox7ReadTexts(missing); err == nil || !strings.Contains(err.Error(), "the windows text") {
		t.Errorf("a missing windows text: %v, want an error naming it", err)
	}
	if err := os.Mkdir(filepath.Join(missing, "windows.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox7ReadTexts(missing); err == nil || !strings.Contains(err.Error(), "the windows text") {
		t.Errorf("an unreadable windows text: %v, want an error naming it", err)
	}
}
