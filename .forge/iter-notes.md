# Stage 7.4 e2e — iteration 3 reflection

## Done this iteration
- s74Compose now takes the approval replies from its caller. The guarded-tools harness passes
  answerer.Deny(), and the loader harness passes none. This removes the
  "1 scripted repl(ies) never asked for" cleanup failure.
- runLive: a copilot-cli subtest that skips or is missing is no longer a kept result. Only an
  executed pass or fail is kept, and a fail is kept as it is. thenRouteKept lists each result
  it did not keep.
- The feature description now says a stand-in run proves the harness side only.

## Gate state
- Scenario 2 passes, with a clean cleanup.
- Scenario 1 is red only on the two VIEW-1 legs: llm/unsupported-option, 0 client runs.

## Blocked outside QA scope (needs an operator pin)
- VIEW-1 title output cap vs. client routes (spec conflict). Options:
  - (a) a route capability flag, so no cap is sent;
  - (b) a "no cap" sentinel in title.GeneratorConfig;
  - (c) amend VIEW-1.
- Signed-in service proof: only the S17 machine's provided clients (DSH_SCENARIO_* inputs)
  can prove service:model-route-claude-code. The gate host has none. The test does not use a
  claude binary found on PATH unless the operator has opted in.
- Still open: no dsh catalogue entries for the client routes, and title.Attempt does not
  record that the client was run.
