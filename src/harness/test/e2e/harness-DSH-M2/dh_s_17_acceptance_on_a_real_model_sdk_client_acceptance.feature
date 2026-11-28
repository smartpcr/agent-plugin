@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-sdk-client-acceptance @setup-inline
Feature: SDK client acceptance (DH-E-SDK-1)
  Stage 7.5 of harness:DSH-M2 (impl.md 17.8): a Go program on src/sdk/sdk drives
  the built dsh over stdio through a session -- create, prompt, an approval, a
  cancel and a close -- and each run's session.run.completed text equals the last
  assistant message of its turn in the history persisted to the scratch home's
  SQLite store (TC-20s). The SDK-1 row of
  docs/stories/harness-DSH-M2/evidence/DH-S-17.md keeps the run's commit and
  artifacts (M2-DEC-5).

  # proof: service:model-route-claude-code. The signed-in claude CLI exists only
  # on the S17 machine (DSH_SCENARIO_SDK1_* set): there the real scenario test
  # TestSDKDrivesBuiltBinaryOverStdio also runs and must pass. Everywhere else
  # the route is an ephemeral in-process instance: a loopback model server
  # registered as route "claude-code" through the testhook build of dsh, which
  # runs as a real child process (os-process) on a scratch home.

  Scenario: DH-E-SDK-1 -- create, prompt, approval, cancel and close on the built binary
    Given DSH_BIN set to dsh built from this checkout with the testhook tag
    And a scratch home whose sdk profile layer serves route "claude-code" from an in-process model instance and mounts the file tools on a scratch workspace holding note.txt
    When a Go program on src/sdk/sdk starts "$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering "allowed-once"
    And it creates a session on route "claude-code" in the scratch workspace
    And it runs a prompt whose model writes "allowed" to note.txt
    And it prompts a turn the model holds open and cancels it with inbox discard once the turn's request reached the model
    And it closes the session and the connection
    Then create answered a session id whose cwd is the scratch workspace
    And the run completed with the model's closing text and note.txt reads "allowed"
    And the client advertised approvals, yet no approval/request crossed the wire and the write ran unasked: the built sdk profile mounts no policy that asks before writes and no interaction registry, the gap that keeps the approval half of DH-E-SDK-1 owed
    And the cancel reply followed the held turn's turn/end on the wire
    And the host exited 0 once the connection closed, and no dsh process remains
    And every line the host wrote to stdout was a JSON-RPC 2.0 frame (M2-C-10)
    And on the S17 machine TestSDKDrivesBuiltBinaryOverStdio also passes on the signed-in route

  # The approval half the built binary cannot carry yet (see above): the same Go
  # client answers approval/request from the real SDK server's sdk-channel
  # answerer, put to it by the real tool pipeline whose policy asks before writes.
  Scenario: DH-E-SDK-1 -- the Go client answers the host's approval requests
    Given an SDK host in process whose tool pipeline asks before every write and whose sdk channel answerer is mounted, served to the Go client over stdio pipes
    When the client runs a prompt whose model writes note.txt and answers its approval "allowed-once"
    And the client runs a prompt whose model writes note.txt and answers its approval "rejected"
    Then the handler was asked exactly once per write, each time for tool "write" on the client's session
    And the allowed write changed note.txt and the rejected write left it unchanged with a refused tool result
    And both runs completed with the model's closing text

  # proof: service:sqlite -- the embedded SQLite store in the scratch home.
  Scenario: Run result equals last assistant message
    Given DSH_BIN set to dsh built from this checkout with the testhook tag
    And a scratch home whose sdk profile layer serves route "claude-code" from an in-process model instance and mounts the file tools on a scratch workspace holding note.txt
    When a Go program on src/sdk/sdk starts "$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering "allowed-once"
    And it creates a session on route "claude-code" in the scratch workspace
    And it runs a prompt whose model writes "allowed" to note.txt
    And it closes the session and the connection
    Then the run's session.run.completed text is the model's closing text
    And it equals the last assistant message of the run in the persisted history read back from the scratch home's SQLite store
    And the persisted history read back has no gap or duplicate seq
