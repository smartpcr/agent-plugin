@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-run-results-and-scoped-notifications @setup-inline
Feature: Run results and scoped notifications
  The SDK server forwards each session's committed log to the connection that
  owns it as scoped session.event notifications, and reports every prompt's run
  as session.run.completed carrying the last assistant text of its turn (TC-12a).

  Scenario: Ordered scoped events
    Given an attached jsonrpctest peer initialized against an SDK server over a memstore and a fake model replying "one" then "two"
    And the client creates two sessions on the connection
    When the client prompts both sessions and waits for each to settle
    Then every session.event names one of the two sessions as its own root
    And per session the session.event seqs strictly increase from create's lastSeq to wait's lastSeq with no gap
    And per session the told events are exactly that session's own history
    And each session gets one session.run.completed for its own message id

  Scenario: Result equals last assistant text
    Given an attached jsonrpctest peer initialized against an SDK server over a memstore and a fake model replying "A" with a tool call then "B"
    And the client creates one session on the connection
    When the client prompts the session with message id "m1" and waits for it to settle
    Then a session.run.completed arrives for message id "m1" of that session
    And the run's result text is "B"
    And the run's bracket holds the assistant messages "A" then "B" and ends with its turn/end
