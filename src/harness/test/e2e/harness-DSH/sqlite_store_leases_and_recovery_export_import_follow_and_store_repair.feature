@story-harness:DSH @phase-sqlite-store-leases-and-recovery @stage-export-import-follow-and-store-repair @setup-compose
Feature: Export, import, follow and store repair
  A session exported from one home and imported into another keeps its event
  rows, and the export of a fixed fixture equals the committed golden
  (internal/session/store/sqlite/testdata/export.golden); a follower in one
  process sees another process's commits once, in order and within a bounded
  latency; a deletion killed half way is finished when the store is reopened
  and the store check then reports clean. Every home is a test-kit home under
  t.TempDir() (or a fresh directory under DSH_E2E_STORE_HOME when the compose
  run mounts one); service:sqlite is the embedded modernc.org/sqlite engine and
  the second processes are re-executions of this test binary.

  @TC-07 @DH-T-STORE-13 @proof:golden @proof:service:sqlite
  Scenario: Export then import round-trips
    Given a session "export-fixture" with 50 events in home H1
    When it is exported from H1 and imported into empty home H2
    Then H2's event rows of "export-fixture" equal H1's in type, version, payload and sequence
    And the export file equals the committed golden "export"
    And exporting "export-fixture" again from H2 gives the same bytes

  @TC-07 @DH-T-STORE-14 @proof:service:sqlite @proof:os-process
  Scenario: Follower sees another process's commits
    Given process A appending 10 events to session "f" in a temp home
    When process B follows session "f"
    Then B observes all 10 events in order within 2 seconds of each commit
    And B observes no event past the end

  @DH-T-STORE-11 @proof:service:sqlite @proof:os-process
  Scenario: Interrupted deletion finishes
    Given a session "big" with 1000 events and a spill file in a temp home
    And a deletion of "big" killed after removing 500 of its events
    When the store is reopened
    Then the deletion has completed
    And no row of "big" remains
    And no spill file of "big" remains
    And check reports clean
