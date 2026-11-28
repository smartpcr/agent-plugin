@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-sdk-client-acceptance @setup-inline
Feature: SDK client acceptance (DH-E-SDK-1)
  Stage 7.5 of harness:DSH-M2 (impl.md 17.8): a Go program on src/sdk/sdk drives
  the built dsh over stdio through a session -- create, prompt, an approval, a
  cancel and a close -- and each run's session.run.completed text equals the last
  assistant message of its turn in the history persisted to the scratch home's
  SQLite store (TC-20s). The SDK-1 row of
  docs/stories/harness-DSH-M2/evidence/DH-S-17.md keeps the run's commit and
  artifacts (M2-DEC-5).

  # proof: service:model-route-claude-code, as an ephemeral instance. The
  # testhook build of dsh runs as a real child process (os-process) on a scratch
  # home, and llm.testkit-claude-code mounts the real claude-code route
  # (llm/provider/claudecode: argument list, environment stripping, helper launch
  # plan, version probe, listing-only MCP server, stream-json mapping, tree stop)
  # over testkit/claudestandin, a stand-in claude started per model request
  # that starts dsh's listing server and replays the loopback model's
  # stream-json. tools.testkit-ask puts every write to approval. The signed-in
  # client itself (no stand-in) is TestSDKDrivesBuiltBinaryOverStdio, run on the
  # S17 machine per DH-S-17.md.
  #
  # Scope: this feature does NOT cover all of DH-E-SDK-1. The question round trip
  # of impl.md 17.8 (the model asks via question/ask, a Handlers.Question answers
  # "blue" on the same connection, the duplicate answer is refused with an sdk/*
  # error) and the kill/resume steps are not exercised here: the stand-in route
  # has no question tool. They are deferred to TestSDKDrivesBuiltBinaryOverStdio
  # (src/harness/internal/testkit/scenarios/sdk_stdio_scenario_test.go), tracked
  # by the DH-E-SDK-1 row and "Stage 7.5 blockers" of
  # docs/stories/harness-DSH-M2/evidence/DH-S-17.md, which stays "not run" until
  # that signed-in run passes. A pass of this feature never closes that row.

  Scenario: DH-E-SDK-1 -- create, prompt, approval, cancel and close on the built binary
    Given DSH_BIN set to dsh built from this checkout with the testhook tag
    And a scratch home whose sdk profile layer mounts the real claude-code route over an ephemeral stand-in Claude Code client, asks before every write, and mounts the file tools on a scratch workspace holding note.txt
    When a Go program on src/sdk/sdk starts "$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering "allowed-once" then "rejected"
    And it creates a session on route "claude-code" in the scratch workspace
    And it runs a prompt whose model writes "allowed" to note.txt
    And it runs a prompt whose model writes "rejected" to note.txt
    And it prompts a turn the model holds open and cancels it with inbox discard once the turn's request reached the model
    And it closes the session and the connection
    Then create answered a session id whose cwd is the scratch workspace
    And every model request went through the claude-code route's client, which listed the harness tool "write" and was sent the prompt
    And the host sent approval/request once per write, each for tool "write" on the session, and the handler answered each
    And the allowed write changed note.txt to "allowed" and the rejected write left it so with a refused tool result
    And both runs completed with the model's closing text
    And the cancel reply followed the held turn's turn/end on the wire
    And the host exited 0 once the connection closed, and no dsh process remains
    And every line the host wrote to stdout was a JSON-RPC 2.0 frame (M2-C-10)

  # proof: service:sqlite -- the embedded SQLite store in the scratch home.
  Scenario: Run result equals last assistant message
    Given DSH_BIN set to dsh built from this checkout with the testhook tag
    And a scratch home whose sdk profile layer mounts the real claude-code route over an ephemeral stand-in Claude Code client, asks before every write, and mounts the file tools on a scratch workspace holding note.txt
    When a Go program on src/sdk/sdk starts "$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering "allowed-once"
    And it creates a session on route "claude-code" in the scratch workspace
    And it runs a prompt whose model writes "allowed" to note.txt
    And it closes the session and the connection
    Then the run's session.run.completed text is the model's closing text
    And it equals the last assistant message of the run in the persisted history read back from the scratch home's SQLite store
    And the persisted history read back has no gap or duplicate seq
