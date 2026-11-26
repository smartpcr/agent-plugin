@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-sdk-session-methods @setup-inline
Feature: SDK session methods
  The wire/sdkserver connection serves session/create, session/prompt,
  session/wait and session/history over JSON-RPC to a jsonrpctest peer. The
  sessions live in a memstore and the turns are answered by the scripted fake
  model, so the proof is in-process. The wire schema the SDK commits is derived
  from the Go types by internal/genschema and must not drift.

  # TC-12a
  Scenario: Create, prompt, history
    Given a jsonrpctest peer initialized against an SDK server over a memstore and a fake model replying "hi there"
    When the client creates a session
    And the client prompts "hello" with message id "m1"
    And the client waits for the session to settle
    And the client calls session/history for "user/message" and "assistant/message"
    Then history holds 2 events
    And history event 1 is a "user/message" holding "hello"
    And history event 2 is an "assistant/message" holding "hi there"
    And the user message comes before the assistant message
    And history's lastSeq is the lastSeq session/wait returned

  # TC-12g
  Scenario: Wire schema unchanged
    Given the committed wire schema src/sdk/protocol/schema.json
    When internal/genschema regenerates schema.json into a temp directory
    Then the regenerated schema is byte-identical to the committed file
