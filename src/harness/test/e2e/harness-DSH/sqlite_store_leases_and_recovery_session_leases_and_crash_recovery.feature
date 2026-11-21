@story-harness:DSH @phase-sqlite-store-leases-and-recovery @stage-session-leases-and-crash-recovery @setup-compose
Feature: Session leases and crash recovery
  One writer per session across processes: a live holder's lease refuses a
  second writer with the store/lease-held refusal (an OwnedError naming the
  live holder, whose code in the store contract, session-store.md section 6.2
  rule 3, is session/owned), a
  dead holder's lease is taken over inside the write transaction at epoch+1 and
  fences the old epoch, a crash between append and flush is recovered honestly,
  and a failed checkpoint flush keeps a tool body from running. Processes A and
  B are re-executions of this test binary sharing one test-kit home under
  t.TempDir() (or a fresh directory under DSH_E2E_STORE_HOME when the compose
  run mounts one); service:sqlite is the embedded modernc.org/sqlite engine.

  Scenario: Second writer refused across processes
    Given process A holding the lease on session "S" in a temp home
    When process B opens "S" for writing
    Then B is refused with "store/lease-held" naming process A
    And the lease-held refusal carries the store contract's code "session/owned"
    And the log of "S" contains only A's events
    And A still appends to "S"

  Scenario: Dead holder taken over safely
    Given process A holding the lease on session "S" in a temp home
    And process B has opened the store while A lives
    When process A is killed
    And process B opens "S" for writing
    Then B holds "S" at epoch 2
    And A's liveness lock file and hosts row are gone
    And a stale append carrying A's epoch is rejected with "session/lease-lost"
    And the log of "S" still holds 3 events
    And B appends to "S"

  Scenario: Crash between append and flush recovers honestly
    Given a child that appends turn/start, step/start and a tool call then exits before flush
    When the store is reopened for write
    Then the session is reported interrupted
    And the persisted rows contain balanced brackets
    And the turn is closed as "interrupted"
    And the cut step's persisted step/end is a closer of the interrupted turn
    And the tool call has an interrupted result
    And every persisted row is durable

  Scenario: Failed flush blocks the tool body
    Given a store whose flush returns an injected error
    When a checkpoint precedes a tool body
    Then the body's call counter stays 0
    And the tool-body checkpoint fails with "tools/not-started"
    And the step ends with a checkpoint failure
