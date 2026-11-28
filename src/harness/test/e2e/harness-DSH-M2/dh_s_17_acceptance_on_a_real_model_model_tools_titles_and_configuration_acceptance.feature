@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-model-tools-titles-and-configuration-acceptance @setup-inline
Feature: Model, tools, titles and configuration acceptance (TC-20s)
  DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1 run once per signed-in client route:
  the claude-code legs must pass and the copilot-cli legs are run and their
  result kept whatever it is (M1-D-LOOP-6 A). DH-E-TOOLS-1 drives a guarded
  pipeline through the claude-code route, and DH-E-LOADER-1 edits the user
  layer under a running config watch and sees the edit on the next turn.

  Off the S17 machine each route is the real route package (claudecode,
  copilot) starting a stand-in client: this test binary re-executed, which
  probes, starts the route's listing MCP server and replays the route's
  committed synthetic client lines. The sessions live in a scratch SQLite
  store. On the S17 machine (the DSH_SCENARIO_* inputs of the scenarios set)
  the real scenario tests are also run against the signed-in clients; a
  partly configured machine fails and names what is missing.

  Scenario: DH-E-LLM-1, LLM-3 and VIEW-1 two-route legs
    Given both client routes "claude-code" and "copilot-cli" with a scratch store
    When the LLM-1, LLM-3 and VIEW-1 legs run on each route
    Then every "claude-code" leg passed
    And every "copilot-cli" leg has a kept result
    And on the S17 machine the live "TestTurnWithReasoningAndToolCall,TestSignedOutIsAFact,TestModelTitlesSession" legs agree

  Scenario: DH-E-TOOLS-1 and DH-E-LOADER-1
    Given the claude-code route over a workspace holding "a.txt,b.txt,c.txt"
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
    And on the S17 machine the live "TestGuardedPipelineRefusesTheWrite,TestRunningHarnessFollowsProfilePatchEdit" legs agree
