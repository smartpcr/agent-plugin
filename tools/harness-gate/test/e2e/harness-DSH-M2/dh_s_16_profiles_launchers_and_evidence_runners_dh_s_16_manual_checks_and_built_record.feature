@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-dh-s-16-manual-checks-and-built-record @setup-inline
Feature: DH-S-16 manual checks and built record
  DH-S-16 (Stage 6.13) closes as a build stage by the two-step protocol (C57;
  M2-C-2): "harness-gate ready --stage DH-S-16 --built" exits 0 on the committed
  docs/stories/harness-DSH-M2/evidence/DH-S-16.md while its status line reads
  open, the closing PR changes the line to closed, and "harness-gate close
  --stage DH-S-16 --built" then exits 0. The stage advances the acceptance
  index's built-through to DH-S-16 (M2-DOD-1, M2-DOD-2).

  # Open, not a pass: the HEAD-state assertions of M2-DOD-1 and M2-DOD-2 are
  # deferred, and they land in two separate PRs because they have different
  # preconditions:
  #
  # 1. The PR that records the DH-S-16 evidence and flips its status line adds:
  #    ready --built exits 0 on the committed record; the guarded rows of
  #    DH-E-STORE-1, DH-E-STORE-2, DH-E-STORE-6 and DH-E-FS-3 read "passed" with
  #    a commit SHA; close --built then exits 0. Today those rows read "not run"
  #    (M1 not closed, M2-DEC-1) and the manual checks for steps 16.3 and 16.4
  #    are unrecorded.
  # 2. The PR that bumps the acceptance index adds: built-through reads
  #    DH-S-16, and every step of the DH-S-12..DH-S-16 records carries a commit
  #    SHA. Precondition (closing order): built-through only advances over a
  #    contiguous run of closed build stages, so this PR may land only after
  #    DH-S-12, DH-S-13, DH-S-14, DH-S-15 and DH-S-16 all read "Closure status:
  #    closed"; the assertion lands together with the index bump, never before.
  #    Today built-through reads DH-S-11, all five records read open and the
  #    DH-S-15 step rows have no commit, so closing DH-S-16 alone cannot clear it.
  #
  # The step definitions ("built-through reads", "every step of each of those
  # records carries a commit SHA") stay in the _test.go file for those PRs;
  # until they land, the stage record keeps those scenarios visibly open.

  # M2-DOD-2 (gate side); proof: golden (the immutable fixture
  # tools/harness-gate/testdata/closure/open_not_ready with its committed
  # expected.golden is the input; nothing is run live but the gate)
  Scenario: S16 ready refuses the open fixture
    Given the committed fixture tree "testdata/closure/open_not_ready"
    When "harness-gate ready --stage DH-S-16 --built" runs on the fixture tree
    Then the run exits 1 naming "DH-E-STORE-6 [Linux (WSL)]" as "not run"
    And its stdout equals the "open_not_ready: ready --built (exit 1)" section of "testdata/closure/expected.golden"
    And the committed fixture tree is unchanged

  # M2-DOD-1 (step check side); proof: golden (the committed index and S12..S16
  # records read from HEAD)
  Scenario: The step check is live
    Given the committed acceptance index and the evidence records of "DH-S-12", "DH-S-13", "DH-S-14", "DH-S-15" and "DH-S-16"
    When the index check runs
    Then the check is live: a record whose first step loses its commit is named by the step check