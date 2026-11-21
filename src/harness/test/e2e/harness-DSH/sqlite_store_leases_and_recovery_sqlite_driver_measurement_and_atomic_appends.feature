@story-harness:DSH @phase-sqlite-store-leases-and-recovery @stage-sqlite-driver-measurement-and-atomic-appends @setup-compose
Feature: SQLite driver measurement and atomic appends
  The SQLite session store keeps one dsh.db per harness home on the pinned
  pure-Go modernc.org/sqlite driver, embedded in the test binary. It passes the
  store contract's conformance suite like memstore, commits a multi-event append
  as one transaction, and its per-append cost is measured against the budget of
  fact F-2. The database lives in a test-kit temp home (or a fresh directory
  under DSH_E2E_STORE_HOME when the compose run mounts one); there is no DSN.

  Scenario: Conformance suite passes on SQLite
    Given storetest.Run with a factory opening dsh.db in a test-kit temp home
    When the suite runs on memstore and on SQLite
    Then every case that passes on memstore passes on SQLite
    And every SQLite case opened its own dsh.db in WAL mode

  Scenario: Multi-event append is atomic
    Given a SQLite store with session "atomic" holding no events
    And the database refuses the insert of the event at seq 2
    When an append of three events at seqs 0, 1 and 2 runs
    Then the append fails with "store/constraint"
    And a fresh read of the database holds none of the three rows
    And the session's next seq is still 0
    When the refusal is removed and the same append runs again
    Then a fresh read of the database holds all three rows

  Scenario: Driver budget measured
    Given the pinned modernc.org/sqlite driver
    When TestBudgetStoreAppend runs 1000 single-event appends with flush
    Then every append is durable
    And the append+flush p95 is under the compiled budget of 50 ms
    And the append p95 is under the compiled budget of 10 ms
    And the measured figures are printed for the evidence record
