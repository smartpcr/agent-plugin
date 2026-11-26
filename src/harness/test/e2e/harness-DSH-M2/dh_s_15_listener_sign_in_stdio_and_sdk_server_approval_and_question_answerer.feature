@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-approval-and-question-answerer @setup-inline
Feature: Approval and question answerer on the SDK channel
  The SDK server registers one answerer on the sdk interaction channel. An
  interaction becomes a host request (approval/request or question/ask) on the
  connection owning the session; only offered answers settle it, a withdrawn
  interaction is cancelled on the wire, and no answer grants for the session.

  # TC-12a
  Scenario: Invalid answer rejected
    Given a client served over stdio pipes advertising approvals and questions against an SDK server with an interaction registry
    And the client creates a session
    And an approval for tool "write" is pending on that session
    When the client answers the approval/request with outcome "allowed-forever"
    Then the server sends sdk/invalid-answer to the null id naming the interaction
    And the approval is still pending in the registry
    And the same approval is asked again under a new request id
    When the client answers the approval/request with outcome "allowed-once"
    Then the approval settles allowed-once by the sdk channel

  # TC-12a
  Scenario: Withdrawal cancels
    Given a client served over stdio pipes advertising approvals and questions against an SDK server with an interaction registry and a model that holds the turn open
    And the client creates a session
    And the client prompts the session and its turn is running
    And a question "Which file?" with choices "a.go" and "b.go" is pending on that session
    When the client cancels the session's turn
    Then the client receives $/cancel for the question/ask request
    And the question settles denied as withdrawn
    When the client sends a late answer selecting the first option
    Then the server sends sdk/late-answer to the null id
    And nothing is pending in the registry and a late registry answer is refused

  # TC-12a, NG-8
  Scenario: No session grant survives
    Given a client served over stdio pipes advertising approvals and questions against an SDK server with an interaction registry
    And the client creates a session
    And an approval for tool "write" is pending on that session
    When the client answers the approval/request with outcome "allowed-session"
    Then the server sends sdk/invalid-answer to the null id naming the interaction
    And the approval is still pending in the registry
    And the same approval is asked again under a new request id
    When the client answers the approval/request with outcome "allowed-once"
    Then the approval settles allowed-once by the sdk channel
    When connection 1 closes
    And a second client connects and resumes the session
    And an identical approval for tool "write" is raised on that session
    Then the second client is asked again with approval/request for tool "write"
    When the second client answers the approval/request with outcome "rejected"
    Then the approval settles denied
