@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-dh-s-18-closure-record @setup-inline
Feature: DH-S-18 closure record
  Stage 8.10 of harness:DSH-M2 (M2-DOD-4, M2-C-2): DH-S-18 closes only when all 19
  scenarios of its acceptance-index entry passed on every in-scope variant of
  docs/stories/harness-DSH-M2/evidence/variants.md. A variant not run keeps S18 open and
  the gate names it. The close is two-step: "harness-gate ready --stage DH-S-18" exits 0
  while the record reads "Closure status: open", then the closing change sets only the
  status line to closed and "harness-gate close --stage DH-S-18" exits 0.

  # proof: in-process / golden. The built harness-gate runs over a scratch tree holding
  # the committed acceptance index and variants.md and the committed fixture
  # tools/harness-gate/testdata/closure/s18 (the committed record's rows, every in-scope
  # variant passed); its output is compared with testdata/closure/expected_s18.golden.
  # The committed record has no run yet (harness:DSH-M1 is not closed, M2-DEC-1), so a
  # pass of this feature never closes it: its gate state must only agree with its status.

  Background:
    Given the committed DH-S-18 record, acceptance index and variants.md of this checkout
    And the committed fixture "s18" carries the committed record's scenario rows

  Scenario: No partial closure
    Given a record fixture where the "linux-e2:no-user-manager" variant of "DH-E-EXEC-10" is "not run"
    And that variant is in scope for "DH-E-EXEC-10" in variants.md
    And every other in-scope row of the fixture passed
    When "harness-gate ready --stage DH-S-18" runs on the record reading "open"
    Then it exits 1 "NOT READY" with exactly one blocker, naming "DH-E-EXEC-10 [linux-e2:no-user-manager]" as "not run"
    And its output is the "open_not_ready" ready output of the golden
    When "harness-gate close --stage DH-S-18" runs on the record reading "closed"
    Then it exits 1 refused "closed-but-not-ready" naming "DH-E-EXEC-10 [linux-e2:no-user-manager]"
    And its output is the "closed_not_ready" close output of the golden
    And the refusal is live: each row of "DH-E-EXEC-10" alone set "not run" makes ready refuse S18 naming that row's variant

  Scenario: S18 ready then closed
    Given the committed fixture "s18" with all 19 scenarios passed on every in-scope variant of variants.md
    Then the committed record's gate state agrees with its status line
    When "harness-gate ready --stage DH-S-18" runs on the filled record while it reads open
    Then it exits 0 with the "open_ready" ready output of the golden
    And "harness-gate close --stage DH-S-18" on the record still reading open is refused "not-yet-closed"
    When the closing change sets only the status line to "Closure status: closed"
    Then the transitioned record differs from the fixture only in its status line
    And "harness-gate ready --stage DH-S-18" exits 0 with the "closed_ready" ready output of the golden
    And "harness-gate close --stage DH-S-18" exits 0 with the "closed_ready" close output of the golden
    And the committed fixture "s18" is unchanged from the commit
