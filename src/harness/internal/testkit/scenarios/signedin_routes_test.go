//go:build scenario

package scenarios

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// The signed-in client routes the DH-S-9 scenarios (DH-E-LLM-1, DH-E-LLM-3,
// DH-E-VIEW-1), since gap-closure-plan 17.4.4 the DH-S-11 scenarios
// (DH-E-SESSION-1, -2, DH-E-AGENT-1 to -4, DH-E-TOOLCHAIN-1) and, since 18.1.1,
// DH-E-HUMAN-6 and DH-E-TOOLS-1 run on. Under OWNER-2 (decisions.md
// models-signed-in-claude-copilot) M1 has no API key and no third-party API-key
// route: a real model is reached only through the user's own installed and
// signed-in official
// client, which dsh starts as a child process and never reads a token of
// (M1-D-PIPE-3 A; areas/llm.md DH-D-LLM-12). The scenarios were re-authored onto
// these routes by gap-closure-plan 16.5.8 (M1-D-CLOSE-4 A), "signed out"
// replacing "bad key".
//
// A scenario names a route only by its route ID and the variable that holds its
// sign-in: it imports nothing of the product but src/sdk/... and the test kit's
// launcher (acceptance.BoundaryImport), so it never imports the route packages.
// The launcher removes every credential-shaped name from dsh's environment, so
// the client's own sign-in is the only credential in play.

// clientRoute is one signed-in client route.
type clientRoute struct {
	// ID is the route ID a session names (DH-D-LLM-12).
	ID string
	// Client is the client name the route's invocation record carries.
	Client string
	// Input is the infix of the route's run-time inputs:
	// DSH_SCENARIO_<Input>_MODEL and DSH_SCENARIO_<Input>_EFFORT.
	Input string
	// HomeVar names the client's configuration home, where its sign-in lives.
	// The route passes it through to the client (claude-code-cli.md sec. 5.3,
	// github-copilot-cli.md sec. 5.3), so pointing it at an empty directory in
	// dsh's environment signs that client out and touches nothing of the user's.
	HomeVar string
}

// claudeCodeRoute is the Claude leg: the user's installed, signed-in Claude Code
// on route claude-code (docs/harness/libs/claude-code-cli.md). Under M1-D-LOOP-6 A
// it must pass. Its Copilot counterpart, copilotCLIRoute, is in
// signedin_routes_copilot_test.go.
var claudeCodeRoute = clientRoute{ID: "claude-code", Client: "claude-code", Input: "CLAUDE_CODE", HomeVar: "CLAUDE_CONFIG_DIR"}

// clientRoutes are the routes every DH-S-9 scenario runs on, Claude first.
var clientRoutes = []clientRoute{claudeCodeRoute, copilotCLIRoute}

// forEachRoute runs leg once per client route, each as a subtest named by its
// route ID. Under M1-D-LOOP-6 A the claude-code subtest must pass and the
// copilot-cli subtest is run and its result recorded either way: the run's
// report (go test -json) records each subtest's own result, a Copilot failure is
// reported and never hidden by a skip, and "-run '<Test>/claude-code'" runs the
// required leg alone.
func forEachRoute(t *testing.T, leg func(t *testing.T, r clientRoute)) {
	t.Helper()
	for _, r := range clientRoutes {
		t.Run(r.ID, func(t *testing.T) { leg(t, r) })
	}
}

// model is the route's model, a run-time input: a model id or alias the
// installed client accepts.
func (r clientRoute) model(t *testing.T) string {
	t.Helper()
	return need(t, "DSH_SCENARIO_"+r.Input+"_MODEL", "a model the "+r.ID+" client accepts")
}

// effort is the route's reasoning effort, a run-time input: a level the model
// accepts and at which it reasons.
func (r clientRoute) effort(t *testing.T) string {
	t.Helper()
	return need(t, "DSH_SCENARIO_"+r.Input+"_EFFORT", "a reasoning effort of the "+r.ID+" model at which it reasons")
}

// signedOut is the environment entry that signs the route's client out: its
// configuration home pointed at an empty temporary directory, removed at
// cleanup. The user's own sign-in is never read, moved or changed.
func (r clientRoute) signedOut(t *testing.T) string {
	t.Helper()
	return r.HomeVar + "=" + t.TempDir()
}

// other is the other client route, the scenarios' second registered route.
func (r clientRoute) other() clientRoute {
	for _, o := range clientRoutes {
		if o.ID != r.ID {
			return o
		}
	}
	return r
}

// wireInvocation is the client invocation a settlement on a client route
// records: the harness-to-client boundary where SP-1 holds (M1-D-PIPE-4 A).
// Args is the argument list with the attempt directory written as
// "{attempt}".
type wireInvocation struct {
	Client      string   `json:"client"`
	Version     string   `json:"version"`
	Args        []string `json:"args"`
	ArgsDigest  string   `json:"args_digest"`
	StdinDigest string   `json:"stdin_digest"`
	SessionID   string   `json:"session_id"`
}

// invocationOf is the client invocation e records, nil for none.
func invocationOf(t *testing.T, e protocol.Event) *wireInvocation {
	t.Helper()
	var s struct {
		Client *wireInvocation `json:"client"`
	}
	decode(t, e, &s)
	return s.Client
}

// requireInvocation fails the scenario unless e records a complete invocation of
// r's client, and returns it.
func requireInvocation(t *testing.T, e protocol.Event, r clientRoute) *wireInvocation {
	t.Helper()
	inv := invocationOf(t, e)
	if inv == nil || inv.Client != r.Client || inv.Version == "" || inv.ArgsDigest == "" || inv.StdinDigest == "" {
		t.Fatalf("event %d %s records invocation %+v, want one of client %s with its version and its argument and standard-input digests", e.Seq, e.Type, inv, r.Client)
	}
	return inv
}

// logInvocations logs the client invocation of every settlement among evs
// (client, version and client session id), so a failed run can be traced in the
// client's own records. A client route has no provider request id.
func logInvocations(t *testing.T, evs []protocol.Event) {
	t.Helper()
	var lines []string
	for _, e := range ofType(evs, "assistant/attempt", "assistant/message") {
		var s struct {
			Client *wireInvocation `json:"client"`
		}
		if json.Unmarshal(e.Data, &s) == nil && s.Client != nil {
			lines = append(lines, s.Client.Client+" "+s.Client.Version+" session "+s.Client.SessionID)
		}
	}
	if len(lines) > 0 {
		t.Logf("client invocations: %s", strings.Join(lines, "; "))
	}
}
