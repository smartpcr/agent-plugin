@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-model-tools-titles-and-configuration-acceptance @setup-inline
Feature: Model, tools, titles and configuration acceptance (TC-20s)
  DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1 run once per signed-in client route:
  the claude-code legs must pass and the copilot-cli legs are run and their
  result kept whatever it is (M1-D-LOOP-6 A). DH-E-TOOLS-1 drives a guarded
  pipeline through the claude-code route, and DH-E-LOADER-1 edits the user
  layer under a running config watch and sees the edit on the next harness turn.

  Proof service:model-route-claude-code. Each scenario first picks its client
  instance. With every DSH_SCENARIO_* input of its live scenario tests set (the
  S17 machine), it is the provided signed-in clients, and the real scenario
  tests run against them and are asserted. With none set, it is an ephemeral
  stand-in client per route: this test binary re-executed, which answers the
  route's version probe, starts the route's listing MCP server and replays the
  route's committed synthetic client lines. A partial set fails and names what is
  missing. Either way the in-process legs drive the real route packages
  (claudecode, copilot) and the agent loop, with sessions in a scratch SQLite store.
  A stand-in run proves the harness side of each route only: acceptance of
  service:model-route-claude-code itself is the provided run on the S17 machine.

  Scenario: DH-E-LLM-1, LLM-3 and VIEW-1 two-route legs
    Given the clients of "claude-code,copilot-cli" for "TestTurnWithReasoningAndToolCall,TestSignedOutIsAFact,TestModelTitlesSession": the provided signed-in clients when their S17 inputs are set, or else ephemeral stand-ins
    And both client routes "claude-code" and "copilot-cli" with a scratch store
    When the LLM-1, LLM-3 and VIEW-1 legs run on each route
    Then every "claude-code" leg passed
    And every "copilot-cli" leg has a kept result

  Scenario: DH-E-TOOLS-1 and DH-E-LOADER-1
    Given the clients of "claude-code" for "TestGuardedPipelineRefusesTheWrite,TestRunningHarnessFollowsProfilePatchEdit": the provided signed-in clients when their S17 inputs are set, or else ephemeral stand-ins
    And the claude-code route over a workspace holding "a.txt,b.txt,c.txt"
    When the model reads "a.txt" and "b.txt" and asks to write "c.txt" through the guarded pipeline and the write is rejected
    Then each tool/call precedes its tool/result in the order the model sent them
    And the write ended with an "approval/" code and "c.txt" is unchanged
    And the turn ended completed with the stand-in's reply
    Given a running config watch over a scratch home whose probe level is "info"
    When the user layer is saved editor-style with level "debug"
    Then the next turn sees level "debug" and the log shows the edit applied before that turn
    When the user layer is saved with an invalid level "loud"
    Then the edit is rejected with a diagnostic positioned in the user layer and the next turn still sees "debug"
    When the user layer is saved editor-style with level "warn"
    Then the next turn sees level "warn" and the log shows the edit applied before that turn
    And the "claude-code" proof is complete: every turn ran on the client and every live scenario passed
