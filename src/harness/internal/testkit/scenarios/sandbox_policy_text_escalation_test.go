//go:build scenario

package scenarios

import (
	"encoding/json"
	"testing"

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
