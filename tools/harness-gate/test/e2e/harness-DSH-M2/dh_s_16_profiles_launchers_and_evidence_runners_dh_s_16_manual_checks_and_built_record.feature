@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-dh-s-16-manual-checks-and-built-record @setup-inline
Feature: DH-S-16 manual checks and built record
  DH-S-16 (Stage 6.13) closes as a build stage by the two-step protocol (C57;
  M2-C-2): "harness-gate ready --stage DH-S-16 --built" exits 0 on the committed
  docs/stories/harness-DSH-M2/evidence/DH-S-16.md while its status line reads
  open, the closing PR changes the line to closed, and "harness-gate close
  --stage DH-S-16 --built" then exits 0. The stage advances the acceptance
  index's built-through to DH-S-16 (M2-DOD-1, M2-DOD-2).

  # M2-DOD-2; proof: golden (the committed record and index read from HEAD, and
  # the immutable fixture tools/harness-gate/testdata/closure/open_not_ready with
  # its committed expected.golden, are the inputs; nothing is run live but the gate)
  Scenario: S16 ready then closed
    Given the committed "docs/stories/harness-DSH-M2/evidence/DH-S-16.md" and acceptance index
    When "harness-gate ready --stage DH-S-16 --built" runs on it before the status transition
    Then the run exits 0 reporting the record ready
    And the guarded rows of "DH-E-STORE-1", "DH-E-STORE-2", "DH-E-STORE-6" and "DH-E-FS-3" all read "passed" with a commit SHA
    When the status line reads "Closure status: closed" and "harness-gate close --stage DH-S-16 --built" runs on it
    Then the run exits 0 reporting the record closed
    Given the committed fixture tree "testdata/closure/open_not_ready"
    When "harness-gate ready --stage DH-S-16 --built" runs on the fixture tree
    Then the run exits 1 naming "DH-E-STORE-6 [Linux (WSL)]" as "not run"
    And its stdout equals the "open_not_ready: ready --built (exit 1)" section of "testdata/closure/expected.golden"
    And the committed fixture tree is unchanged

  # M2-DOD-1; proof: golden (the committed index and S12..S16 records read from HEAD)
  Scenario: Built-through advanced
    Given the committed acceptance index and the evidence records of "DH-S-12", "DH-S-13", "DH-S-14", "DH-S-15" and "DH-S-16"
    When the index check runs
    Then built-through reads "DH-S-16"
    And every step of each of those records carries a commit SHA
    And the check is live: a record whose first step loses its commit is named by the step check
