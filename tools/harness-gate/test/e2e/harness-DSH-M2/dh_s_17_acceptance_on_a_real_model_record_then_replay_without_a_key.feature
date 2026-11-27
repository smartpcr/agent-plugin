@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-record-then-replay-without-a-key @setup-inline
Feature: Record then replay without a key (DH-E-TOOLCHAIN-1)
  Stage 7.2 of harness:DSH-M2 runs DH-E-TOOLCHAIN-1 first (impl.md 17.1,
  architecture.md rule 7): TestRecordThenReplayWithoutAKey records a session on
  the signed-in Claude Code route and replays it twice with the route disabled,
  and the TOOLCHAIN-1 row of docs/stories/harness-DSH-M2/evidence/DH-S-17.md
  keeps the commit, the dsh SHA-256, the model id and the run id (M2-DEC-5).

  The signed-in route lives only on the S17 machine (lab-bare-metal; the runbook
  run is by an operator, never an agent session). On any other host the scenario
  test is still run, and must fail closed naming its missing input, never skip
  (DH-O-15); the committed record row is the evidence of the real run, as the
  lane records are for the harness-gate lanes. The row must never read passed
  without its evidence, and "harness-gate ready" must never count it otherwise.

  # proof: service:model-route-claude-code. Live when DSH_SCENARIO_TOOLCHAIN1_DIR,
  # DSH_SCENARIO_TOOLCHAIN1_RECORD and DSH_SCENARIO_TOOLCHAIN1_REPLAY are set (the
  # S17 machine); otherwise the same test runs and must fail closed.
  Scenario: DH-E-TOOLCHAIN-1 record and replay
    Given the committed record "docs/stories/harness-DSH-M2/evidence/DH-S-17.md" with its "DH-E-TOOLCHAIN-1" row
    When TestRecordThenReplayWithoutAKey runs with the route inputs this host holds
    Then the test passes when the route inputs are set, and otherwise fails naming "DSH_SCENARIO_TOOLCHAIN1_DIR"
    And the test is never reported skipped
    And its scenario report row for "DH-E-TOOLCHAIN-1" reads the same result
    And the committed row is a required acceptance row of variant "Linux (E1)"
    And the committed row reads passed with its replay result in Artifacts, or reads not run with a reason and a "Stage 7.2 blockers" section naming what clears it
    And "harness-gate ready --stage DH-S-17" counts the committed row as passed only when it reads passed

  # M2-DEC-5; proof: golden (the committed record read from HEAD).
  Scenario: Evidence row complete
    Given the committed record "docs/stories/harness-DSH-M2/evidence/DH-S-17.md" still reading "Closure status: open"
    When "harness-gate ready --stage DH-S-17" reads the "DH-E-TOOLCHAIN-1" row of the checkout
    Then the row is counted as passed only when its commit, binary checksum, model id and run id are all present

  # The gate side of M2-DEC-5, on a copy of the committed index and record.
  Scenario: A complete TOOLCHAIN-1 row clears its blocker
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "323f4bd" with artifacts "dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z; TestRecordThenReplayWithoutAKey/claude-code executed"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then it exits 1 for the other open rows
    And it names no blocker of "DH-E-TOOLCHAIN-1"
    And the copied row carries a commit, a binary checksum, a model id and a run id

  Scenario Outline: ready refuses a passed TOOLCHAIN-1 row without its <what>
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "<commit>" with artifacts "<artifacts>"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then it names the blocker "DH-E-TOOLCHAIN-1 [Linux (E1)]: <blocker>"

    Examples:
      | what      | commit  | artifacts                                                                                       | blocker                                       |
      | commit    |         | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id m; run id r | ran but records no commit                     |
      | SHA       | HEAD    | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id m; run id r | commit "HEAD" is not a commit SHA             |
      | artifacts | 323f4bd |                                                                                                 | ran but records no artifacts                  |

  Scenario Outline: A passed TOOLCHAIN-1 row without its <what> is incomplete
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "323f4bd" with artifacts "<artifacts>"
    Then the copied row lacks its <what>

    Examples:
      | what            | artifacts                                                                                     |
      | binary checksum | dsh sha256=9f2c4e1a; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z              |
      | model id        | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; run id s17-323f4bd-20261201T0000Z |
      | run id          | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id claude-sonnet-4-5 |
