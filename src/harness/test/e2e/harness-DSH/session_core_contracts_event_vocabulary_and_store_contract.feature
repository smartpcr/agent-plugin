@story-harness:DSH @phase-session-core-contracts @stage-event-vocabulary-and-store-contract @setup-inline
Feature: Event vocabulary and store contract
  The session event registry decodes only the versions it knows, every store
  provider passes the shared conformance suite, and a persisted type cannot
  change shape without a version bump and a note.

  # DH-T-SESSION-2, TC-05 [proof: in-process]
  Scenario: Unknown event version surfaces
    Given the default event registry knows "tool/result" at version 2
    And an in-memory encoded event "tool/result" at version 3 with seq 9
    When the event is decoded
    Then the decoder returns a typed unknown-version error naming "tool/result" version 3 known 2 at seq 9
    And no tool result value is fabricated
    And the generic decoder and the classifier also refuse the event

  # DH-T-STORE-1, TC-05 [proof: in-process]
  Scenario: Conformance suite passes on memstore
    Given the storetest conformance suite with the memstore factory
    When the suite runs
    Then every case passes
    And the cases "AppendOrderAndReadYourWrites", "FlushBarrier", "PagedReads", "List" ran on a fresh store

  # DH-T-STORE-10 [proof: golden: internal/session/store/testdata/persisted.golden]
  Scenario: Persisted type cannot change unnoticed
    Given the committed persisted-type golden and notes
    And the current registry inventory matches the committed golden
    When a test-only change adds a field to the persisted type "tool/call" without a version bump
    And the inventory check runs against the committed golden
    Then the check fails naming "tool/call"
