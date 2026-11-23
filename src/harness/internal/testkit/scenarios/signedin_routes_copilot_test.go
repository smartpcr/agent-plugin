//go:build scenario

package scenarios

// copilotCLIRoute is the Copilot leg of the DH-S-9 scenarios: the user's
// installed, signed-in GitHub Copilot CLI on route copilot-cli
// (docs/harness/libs/github-copilot-cli.md; areas/llm.md DH-D-LLM-12). Under
// M1-D-LOOP-6 A it is run and its result recorded either way; it is not required
// to pass. Like the Claude leg it is named only by its route ID, never by an
// import of llm/provider/copilot (acceptance.BoundaryImport).
//
// The route ID and the client name match llm/provider/copilot's RouteID and
// Client ("copilot-cli"), checked by hand when this leg was written; nothing
// here can import them.
//
// TODO(gap-closure-plan 16.5.9): two facts of this leg stay open until the
// Copilot route's manual record DH-E-COPILOTCLI-1 is filed:
//   - HomeVar. An empty COPILOT_HOME may not sign the client out, because the CLI
//     keeps its token in the system credential store (github-copilot-cli.md
//     sec. 7). The DH-E-COPILOTCLI-1 runbook records how the signed-out state is
//     produced; this leg follows it.
//   - The signed-out failure. Until that record pins the CLI's signed-out line, a
//     signed-out run settles as a Permanent failure carrying the exit code, not as
//     llm/sign-in-required (sec. 5.4). DH-E-LLM-3's sign-in-required assertion
//     on this leg is therefore expected to fail, and that failure is recorded.
var copilotCLIRoute = clientRoute{ID: "copilot-cli", Client: "copilot-cli", Input: "COPILOT_CLI", HomeVar: "COPILOT_HOME"}
