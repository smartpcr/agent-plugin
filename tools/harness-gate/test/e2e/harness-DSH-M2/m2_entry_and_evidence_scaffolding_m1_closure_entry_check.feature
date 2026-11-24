@story-harness:DSH-M2 @phase-m2-entry-and-evidence-scaffolding @stage-m1-closure-entry-check @setup-inline
Feature: M1 closure entry check -- harness:DSH-M2 may not start until harness:DSH-M1 is closed
  "harness-gate entry" passes only when every M1 stage record reads closed, the M1
  closure record lists every M1 deliverable as done or built with a reason, and the
  acceptance index's built-through has reached DH-S-11 (M2-DEC-1, risk M2-R-04).

  Scenario: Entry refused while an M1 record is open
    Given eleven in-memory M1 evidence records of which "DH-S-11" reads "Closure status: open"
    And an in-memory M1 closure record listing every M1 deliverable as done
    And an in-memory built-through of "DH-S-11"
    When the entry predicate runs
    Then it fails with exactly 1 unmet condition
    And an unmet condition names "DH-S-11"
    And no unmet condition names "DH-S-10:"
    Given eleven in-memory M1 evidence records of which "none" reads "Closure status: open"
    And an in-memory M1 closure record listing every M1 deliverable as done
    And an in-memory built-through of "DH-S-11"
    When the entry predicate runs
    Then it passes
    Given eleven in-memory M1 evidence records of which "none" reads "Closure status: open"
    And no in-memory M1 closure record
    And an in-memory built-through of "DH-S-8"
    When the entry predicate runs
    Then it fails with exactly 2 unmet conditions
    And an unmet condition names "closure record"
    And an unmet condition names "reads DH-S-8; want DH-S-11 or later"

  Scenario: Entry subcommand over immutable fixtures
    Given the committed fixture trees "testdata/entry/m1_open" and "testdata/entry/m1_closed"
    When "harness-gate entry --story harness:DSH-M2 --root <tree>" runs on each tree
    Then the run on "m1_open" exits non-zero
    And the stdout of "m1_open" equals the committed "testdata/entry/m1_open.golden"
    And the stdout of "m1_open" names all eleven M1 records as open and built-through "DH-S-8"
    And the run on "m1_closed" exits 0
    And the stdout of "m1_closed" reports "harness:DSH-M1 is closed"
    And the committed entry fixture trees are unchanged

  Scenario: Live entry result agrees with live records
    Given the repository's live M1 evidence records and acceptance index
    When "harness-gate entry" runs against the repository root
    Then it exits 0 if and only if every live condition holds
    And its blockers name exactly the unmet live conditions
