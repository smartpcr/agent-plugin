//go:build scenario

package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
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

// forEachRoute runs leg once per selected client route (selectedRoutes), each as
// a subtest named by its route ID. Under M1-D-LOOP-6 A the claude-code subtest
// must pass and the copilot-cli subtest is run and its result recorded either
// way: the run's report (go test -json) records each subtest's own result, a
// Copilot failure is reported and never hidden by a skip, and
// DSH_SCENARIO_ROUTES=claude-code runs the required leg alone, leaving every
// other subtest of the scenario in place.
func forEachRoute(t *testing.T, leg func(t *testing.T, r clientRoute)) {
	t.Helper()
	for _, r := range selectedRoutes(t) {
		t.Run(r.ID, func(t *testing.T) { leg(t, r) })
	}
}

// routesEnv selects the client routes forEachRoute runs: comma-separated route
// IDs, all of clientRoutes when unset or empty. "harness-gate scenarios" runs a
// DH-S-17 phase once with claude-code, whose result decides the stage, and its
// routed scenarios once more with copilot-cli, whose result is recorded
// (M2-DEC-4); a parent failure of the required run is therefore never mistaken for
// the recorded leg's. It is a selection, not a credential.
const routesEnv = "DSH_SCENARIO_ROUTES"

// selectedRoutes returns the routes routesEnv selects (parseRoutes); a value that
// selects no route, or names one that is not a route, fails t, so a typo or a
// separator alone never runs no leg and passes.
func selectedRoutes(t *testing.T) []clientRoute {
	t.Helper()
	routes, err := parseRoutes(os.Getenv(routesEnv))
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

// parseRoutes is the routes of a routesEnv value v in clientRoutes order: all of
// them when v is unset or blank; otherwise the comma- or space-separated route IDs,
// at least one, each a route of clientRoutes.
func parseRoutes(v string) ([]clientRoute, error) {
	if strings.TrimSpace(v) == "" {
		return clientRoutes, nil
	}
	ids := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s=%q selects no route; name claude-code, copilot-cli or both, or leave it unset for both", routesEnv, v)
	}
	for _, id := range ids {
		if !slices.ContainsFunc(clientRoutes, func(r clientRoute) bool { return r.ID == id }) {
			return nil, fmt.Errorf("%s=%q names route %q; the routes are claude-code and copilot-cli", routesEnv, v, id)
		}
	}
	var out []clientRoute
	for _, r := range clientRoutes {
		if slices.Contains(ids, r.ID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// TestParseRoutes: DSH_SCENARIO_ROUTES selects routes in clientRoutes order, all
// of them when unset; a value of separators alone or an unknown ID is refused, so
// forEachRoute never runs zero legs. It starts no client.
func TestParseRoutes(t *testing.T) {
	ids := func(rs []clientRoute) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	for v, want := range map[string][]string{
		"":                        {"claude-code", "copilot-cli"},
		"  ":                      {"claude-code", "copilot-cli"},
		"claude-code":             {"claude-code"},
		"copilot-cli":             {"copilot-cli"},
		"copilot-cli,claude-code": {"claude-code", "copilot-cli"},
		" claude-code , ":         {"claude-code"},
	} {
		got, err := parseRoutes(v)
		if err != nil || !slices.Equal(ids(got), want) {
			t.Errorf("parseRoutes(%q) = %q, %v; want %q", v, ids(got), err, want)
		}
	}
	for _, v := range []string{",", " , ,", ",,", "claude", "claude-code,codex"} {
		if got, err := parseRoutes(v); err == nil {
			t.Errorf("parseRoutes(%q) = %q, want an error", v, ids(got))
		}
	}
}

// TestSignedInRoutePreflight is the signed-in route preflight of the s17 job of
// .github/workflows/harness-scenarios.yml (harness:DSH-M2 Stage 7.1, M2-EN-8):
// one probe turn per client route against the built dsh, run right after
// "harness-gate entry" and before any DH-S-17 scenario starts. It is not a
// scenario: it carries no //dsh:scenario directive and is in no index entry. The
// job runs its claude-code leg as a required step, so a client that is not
// signed in stops the job before the first scenario, and its copilot-cli leg as
// a recorded step whose failure the record keeps as "failed (route preflight)"
// (M2-DEC-4). The same commands run by hand on a signed-in WSL machine
// (docs/stories/harness-DSH-M2/evidence/runbook-s17-wsl.md).
//
// Given the built dsh, a fresh temporary home, no API key anywhere and the
// user's own installed client signed in as themselves, with its route
// registered by a layer, when one prompt the model answers in text is sent,
// then the turn ends completed and its last assistant/message records an
// invocation of the route's client with its version and digests. A signed-out
// client fails the leg naming llm/sign-in-required (noteSignIn). Cleanup: the
// launcher stops dsh and deletes the home; nothing of the user's sign-in is
// read or changed; on failure the export is kept.
//
// Inputs: DSH_SCENARIO_PREFLIGHT_LAYER a headless layer, a template over
// {route}, that registers that client route; DSH_SCENARIO_<route>_MODEL
// (<route> is CLAUDE_CODE or COPILOT_CLI) the model; DSH_SCENARIO_PREFLIGHT_PROMPT
// a prompt answered in text without a tool.
func TestSignedInRoutePreflight(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_PREFLIGHT_LAYER", "a headless layer over {route} that registers that client route")
	prompt := need(t, "DSH_SCENARIO_PREFLIGHT_PROMPT", "a prompt answered in text without a tool")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		route := &protocol.ModelRoute{Provider: r.ID, Model: r.model(t)}
		h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, map[string]string{"route": r.ID}))})
		session := h.create(t, route)
		h.keepExport(t, session)
		run := h.awaitRun(t, session, h.prompt(t, session, prompt), 5*time.Minute)
		evs := inRun(h.history(t, session), run.FirstSeq, run.LastSeq)
		logInvocations(t, evs)
		noteSignIn(t, evs)
		if end := turnEnd(t, evs); end.Reason.Kind != "completed" {
			t.Fatalf("the probe turn on %s ended %+v, want completed", r.ID, end.Reason)
		}
		msgs := ofType(evs, "assistant/message")
		if len(msgs) == 0 {
			t.Fatalf("the probe turn on %s holds no assistant/message", r.ID)
		}
		requireInvocation(t, msgs[len(msgs)-1], r)
	})
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

// codeSignInRequired is the failure code of a client route whose client is not
// signed in (internal/llm/vocab CodeSignInRequired).
const codeSignInRequired = "llm/sign-in-required"

// signInFailure describes the first event among evs that failed for a missing
// sign-in -- an attempt or message settlement, or a turn end, whose failure code
// is codeSignInRequired -- and is empty when none did.
func signInFailure(evs []protocol.Event) string {
	for _, e := range evs {
		var f *wireFailure
		switch e.Type {
		case "assistant/attempt", "assistant/message":
			var s wireSettlement
			if json.Unmarshal(e.Data, &s) == nil {
				f = s.Failure
			}
		case "turn/end":
			var end wireTurnEnd
			if json.Unmarshal(e.Data, &end) == nil {
				f = end.Reason.Failure
			}
		}
		if f != nil && f.Code == codeSignInRequired {
			return fmt.Sprintf("%s: event %d %s: the route's client is not signed in", codeSignInRequired, e.Seq, e.Type)
		}
	}
	return ""
}

// noteSignIn records a missing sign-in among evs as the cause of t's scenario
// (scenario.Cause), so a scenario that then fails on it has a report row whose
// reason names it; a scenario that expects the failure and passes is unaffected.
func noteSignIn(t testing.TB, evs []protocol.Event) {
	if why := signInFailure(evs); why != "" {
		scenario.Cause(t, why)
	}
}
