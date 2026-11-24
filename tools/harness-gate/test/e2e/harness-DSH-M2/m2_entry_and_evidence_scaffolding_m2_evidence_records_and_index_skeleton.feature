@story-harness:DSH-M2 @phase-m2-entry-and-evidence-scaffolding @stage-m2-evidence-records-and-index-skeleton @setup-inline
Feature: M2 evidence records and index skeleton -- the two-step stage closure
  The ready predicate checks every scenario row of a stage record and ignores the
  status line; the close predicate re-runs ready and also requires the status line
  to read closed (C57, M2-C-2, M2-DEC-3).

  Scenario: Ready accepts an open record whose rows passed
    Given an in-memory index listing "DH-E-STORE-1" under "DH-S-16"
    And an in-memory record reading "Closure status: open" with row "DH-E-STORE-1" result "passed"
    When the ready predicate evaluates "DH-S-16"
    Then it returns no blocker
    Given an in-memory record reading "Closure status: open" with row "DH-E-STORE-1" result "not run"
    When the ready predicate evaluates "DH-S-16"
    Then it returns exactly 1 blocker
    And the blocker names "DH-E-STORE-1"
    And the blocker names "not run"

  Scenario Outline: Close needs ready plus the status transition
    Given an in-memory index listing "DH-E-STORE-1" under "DH-S-16"
    And an in-memory record reading "Closure status: <status>" with row "DH-E-STORE-1" result "<result>"
    When the close predicate evaluates "DH-S-16"
    Then the close refusal is "<refusal>"

    Examples:
      | state            | status | result                | refusal              |
      | open/not-ready   | open   | not run (no runner)   | not-yet-closed       |
      | open/ready       | open   | passed                | not-yet-closed       |
      | closed/ready     | closed | passed                | none                 |
      | closed/not-ready | closed | not run (no runner)   | closed-but-not-ready |

  Scenario: Skipped counts as not passed
    Given an in-memory index listing "DH-E-STORE-1" under "DH-S-16"
    And an in-memory record reading "Closure status: open" with row "DH-E-STORE-1" result "skipped"
    When the ready predicate evaluates "DH-S-16"
    Then the stage is refused
    And the blocker names "skipped is not passed"

  Scenario: Subcommands over committed fixtures
    Given the committed fixture trees under "testdata/closure"
    When "harness-gate ready" and "harness-gate close" run with --root pointed at each tree
    Then their exit codes and outputs match the committed "testdata/closure/expected.golden"
    And the exit codes are:
      | tree             | ready | close |
      | open_not_ready   | 1     | 1     |
      | open_ready       | 0     | 1     |
      | closed_ready     | 0     | 0     |
      | closed_not_ready | 1     | 1     |
    And the committed fixture trees are unchanged
