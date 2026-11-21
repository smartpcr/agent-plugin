@story-harness:DSH @phase-deepseek-provider-retry-and-usage @stage-deepseek-messages-provider @setup-inline
Feature: DeepSeek messages provider
  The deepseek-official route speaks the Anthropic-compatible Messages API over
  the harness transport. Every attempt settles exactly once, and the bytes it
  sent are the request rebuilt from the session log at the settlement's
  requestAsOf. No real key and no real API are used: the secret store resolves
  a fake value, and replies come from the test kit's in-memory fake transport,
  committed SSE fixtures, or an httptest server on 127.0.0.1:0.

  Scenario: Request at the transport equals rebuilt request
    Given a deepseek session with a system prompt and one user message
    And the fake transport of the test kit capturing bytes
    When one attempt is made for the session
    Then exactly one settlement is recorded and it succeeded
    And the captured body equals surface.Rebuild at the settlement's requestAsOf
    And the settlement's requestDigest matches the rebuilt request's digest

  Scenario Outline: Decoded fixtures settle once
    Given a deepseek session with a system prompt and one user message
    And the SSE fixture "<fixture>" served in memory
    When one attempt is made for the session
    Then exactly one settlement of kind "<kind>" is recorded with failure code "<code>"
    And the decoded settlement equals the committed golden "<fixture>"

    Examples:
      | fixture                | kind              | code               |
      | success                | assistant/message |                    |
      | overloaded-retry-after | assistant/attempt | llm/server         |
      | bad-key                | assistant/attempt | llm/auth           |
      | mid-stream-drop        | assistant/attempt | llm/stream-closed  |

  Scenario: Provider over loopback HTTP
    Given a deepseek session with a system prompt and one user message
    And the success fixture served by httptest on 127.0.0.1:0
    And a model-request permit for that loopback host
    When one attempt is made for the session
    Then one assistant/message is settled
    And the server saw the x-api-key header from the secret store
    And the server saw the request body rebuilt from the log
