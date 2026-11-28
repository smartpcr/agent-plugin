@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-dh-s-17-closure-record @setup-inline
Feature: DH-S-17 closure record
  Stage 7.7 of harness:DSH-M2 (M2-DOD-3, M2-C-2): DH-S-17 closes in two steps. While
  docs/stories/harness-DSH-M2/evidence/DH-S-17.md reads "Closure status: open" its
  tables are filled until "harness-gate ready --stage DH-S-17" exits 0; the closing
  PR then changes only the status line to closed and "harness-gate close --stage
  DH-S-17" must exit 0 too. An acceptance close takes no --built.

  # proof: golden. The committed record is checked against the record form of the
  # committed fixtures under tools/harness-gate/testdata/closure/ (same 14 index IDs,
  # same 17 scenario rows by ID, variant, venue and leg). The committed record has no
  # run yet (harness:DSH-M1 is not closed, M2-DEC-1), so the filled record is the
  # committed fixture open_ready, and the gate's output is compared with
  # testdata/closure/expected_s17.golden. A pass of this feature never closes the
  # live record: its gate state must only agree with its status line.
  Scenario: S17 ready then closed
    Given the committed DH-S-17 record and acceptance index of this checkout
    And the committed fixtures "open_ready", "closed_ready" and "closed_not_ready" carry a DH-S-17 record in the committed record's form
    Then the committed record's gate state agrees with its status line
    When "harness-gate ready --stage DH-S-17" runs on the filled record while it reads open
    Then it exits 0 with the "open_ready" ready output of the golden
    And every one of the 14 index IDs has a required row that passed
    And "harness-gate close --stage DH-S-17" on the record still reading open is refused "not-yet-closed"
    When the closing change sets only the status line to "Closure status: closed"
    Then the transitioned record is the committed fixture "closed_ready" but for its title
    And "harness-gate ready --stage DH-S-17" exits 0 with the "closed_ready" ready output of the golden
    And "harness-gate close --stage DH-S-17" exits 0 with the "closed_ready" close output of the golden
    Given the immutable fixture "closed_not_ready" with exactly one scenario row "not run" and status "closed"
    When "harness-gate close --stage DH-S-17" runs on the fixture in place
    Then it exits non-zero refused "closed-but-not-ready" naming "DH-E-SANDBOX-7"
    And its output is the "closed_not_ready" close output of the golden
    And the fixture is unchanged from the commit

  # proof: golden (M2-DEC-4). The committed record and the closed_ready fixture keep a
  # copilot-cli row for each of the three; the live probes delete a row or mark it
  # not run on a scratch copy and the close must name the missing leg.
  Scenario: Copilot leg kept
    Given the committed DH-S-17 record and acceptance index of this checkout
    And the committed fixture "closed_ready"
    When the close check reads "DH-E-LLM-1", "DH-E-LLM-3" and "DH-E-VIEW-1"
    Then each has exactly one "copilot-cli" row with venue "acceptance" and leg "recorded" in the committed record
    And each has a "copilot-cli" row in the fixture whose result is passed or failed, never missing
    And "harness-gate close --stage DH-S-17" on the fixture exits 0 with the failed "DH-E-LLM-3" leg kept
    And the check is live: without the "copilot-cli" row of each, close is refused "closed-but-not-ready" naming it
    And the check is live: with the "copilot-cli" row of each "not run", close is refused "closed-but-not-ready" naming it
