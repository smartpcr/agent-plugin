@story-harness:DSH @phase-deepseek-provider-retry-and-usage @stage-bounded-retry-and-token-usage @setup-inline
Feature: Bounded Retry and Token Usage
  The model runtime retries a Transient provider failure within the route's
  bound on the virtual clock, honouring the provider's Retry-After, and never
  retries a Permanent one nor falls back to another route (SP-2). The token
  meter folds the logged settlements into the session's billed usage and the
  context it occupies (DH-T-LLM-7, DH-T-LLM-15, TC-09a).

  # DH-T-LLM-7, TC-09a; proof: in-process (fake transport + virtual clock)
  Scenario: Overloaded retried within bound
    Given an auxiliary request on a deepseek-official session
    And the fake transport serving the SSE fixtures "overloaded-retry-after,overloaded-retry-after,success" in order
    And the deepseek-official route registered with its default retry policy on the virtual clock
    When the call runs, the virtual clock advanced by each wait
    Then the call returns the successful attempt's message
    And the log holds the settlements "assistant/attempt,assistant/attempt,assistant/message" in order
    And the 2 failed settlements carry a transient failure asking a Retry-After of 2 s
    And the waits on the virtual clock were "2s,2s"
    And the fake transport saw exactly 3 requests, so no fourth attempt occurred

  # SP-2, TC-09a; proof: in-process
  Scenario: Bad key never retried
    Given an auxiliary request on a deepseek-official session
    And the fake transport serving the SSE fixtures "bad-key" in order
    And the deepseek-official route registered with the persistent retry policy on the virtual clock
    And a second working route "r/other" registered on the same runtime
    When the call runs, the virtual clock advanced by each wait
    Then the call fails with code "llm/auth" and class "permanent"
    And the log holds the settlements "assistant/attempt" in order
    And the only settlement is on route "deepseek-official" with failure code "llm/auth" and class "permanent"
    And the waits on the virtual clock were ""
    And the fake transport saw exactly 1 requests, so no fourth attempt occurred
    And the route "r/other" was never prepared nor attempted

  # DH-S-9 closure; proof: service:go-toolchain
  # The DH-S-9 closing commit is DSH_E2E_DHS9_COMMIT when set, else the first commit
  # whose acceptance index raises built-through to DH-S-9, checked out into a
  # temporary git worktree; while the checkout is still built through less than
  # DH-S-9 (DH-S-9 not yet closed) the checkout itself is the closing candidate.
  Scenario: DH-S-9 scenarios authored before closure
    Given the DH-S-9 scenario files for "DH-E-LLM-1,DH-E-LLM-3,DH-E-VIEW-1" and "test/harness/acceptance-index.yaml" in the checkout
    When the gate's index check runs on the checkout
    And "go -C src/harness vet -tags scenario ./..." runs
    Then the gate's index check passes
    And each of the IDs has exactly one scenario file bound to a TestXxx
    And each of the IDs has exactly one index entry, under "DH-S-17", authored "DH-S-9" and requiring "DH-S-11,DH-S-16"
    And each file imports only "src/sdk/...", the test kit's scenario launcher and the standard library
    And no file imports "internal/llm,internal/agent,internal/session/views,internal/session/store"
    And vet exits 0
    And the default gate run executes none of them
    And requiring any of them before "DH-S-11,DH-S-16" are built is refused by the preflight

  # DH-T-LLM-15; proof: in-process
  Scenario: Usage folded from settlements
    Given a session whose log holds three settlements with known token counts
      | kind              | input | cache_read | cache_write | output |
      | assistant/attempt | 100   | 0          | 0           | 5      |
      | assistant/message | 20    | 80         | 10          | 30     |
      | assistant/message | 15    | 140        | 0           | 12     |
    When the usage fold runs over the session
    Then the billed usage is input 135, cache read 220, cache write 10 and output 47
    And the fold counted 3 attempts, 3 of them reporting usage, totalling 412 tokens
    And the last reported usage totals 167 tokens at the last settlement
    And the context occupancy is 167 tokens, 0.167 of a 1000-token window
    And folding the session's log from a checkpoint after the first settlement equals folding it from zero
