@story-harness:DSH @phase-cordis-port-guarantees @stage-context-values-and-scoped-dispatch @setup-inline
Feature: Context values and scoped dispatch
  A cordis dispatch can be scoped to one fiber subtree, a notified fact cannot be
  vetoed by a failing listener, and around listeners wrap in registration order.

  # DH-T-CORDIS-6, TC-02a
  Scenario: Scoped dispatch reaches one subtree
    Given two agent subtrees "A" and "B" each listening on the same event key
    When an event is emitted scoped to subtree "A"
    Then the listener of subtree "A" ran 1 time
    And the listener of subtree "B" ran 0 times

  # DH-T-CORDIS-7
  Scenario: A listener cannot veto a notified fact
    Given three Notify listeners where the second panics
    When the fact is notified
    Then Notify returns normally
    And listeners one and three ran
    And the panic is recorded once

  # DH-T-CORDIS-12
  Scenario: Around key wraps in registration order
    Given two around listeners that append "a" and "b" to the request and "B" and "A" to the result
    When the key is called with an inner handler
    Then the handler sees the request "ab"
    And the caller receives the result suffixed "BA"
