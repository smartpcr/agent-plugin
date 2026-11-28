@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-record-then-replay-without-a-key @setup-inline
Feature: Record then replay without a key (DH-E-TOOLCHAIN-1)
  Stage 7.2 of harness:DSH-M2 runs DH-E-TOOLCHAIN-1 first (impl.md 17.1): a
  session recorded on the claude-code route is replayed with the route disabled,
  and the replayed persisted log, workspace and requests equal the recording
  after normalization (TC-20s). The TOOLCHAIN-1 row of
  docs/stories/harness-DSH-M2/evidence/DH-S-17.md keeps the run's commit and
  artifacts (M2-DEC-5).

  # proof: service:model-route-claude-code. The signed-in claude CLI exists only
  # on the S17 machine (DSH_SCENARIO_TOOLCHAIN1_* set): there the real scenario
  # test also runs and must pass. Everywhere else the route is an ephemeral
  # in-process instance: a loopback model server registered as route
  # "claude-code" through the testhook build of dsh, so the record is real and
  # the replay runs against the scratch SQLite store of a fresh home.
  Scenario: DH-E-TOOLCHAIN-1 record and replay
    Given dsh built from this checkout with the testhook tag
    And a scratch home whose route "claude-code" is served by an in-process model instance
    When the scenario records the session "write a note" on route "claude-code" into a scratch workspace
    And it replays the recording twice with the route disabled and no key, each into a fresh scratch home
    Then the recording holds 2 model requests, each sent to the route exactly once
    And each recorded request rebuilt from the persisted log has the digest the route received
    And the route received no request during either replay, and no claude client started
    And every replayed attempt is served by the replay, never by the route
    And each replayed persisted log read back from its SQLite store equals the recorded log after normalization
    And each replayed workspace equals the recorded workspace
    And each replayed model request equals the recorded request after normalization
    And on the S17 machine TestRecordThenReplayWithoutAKey also passes on the signed-in route

  # M2-DEC-5; proof: golden (the committed record at HEAD).
  Scenario: Evidence row complete
    Given the committed record "docs/stories/harness-DSH-M2/evidence/DH-S-17.md" still reading "Closure status: open"
    When "harness-gate ready --stage DH-S-17" reads the "DH-E-TOOLCHAIN-1" row of the checkout
    Then the row is counted only when it reads passed with commit, binary checksum, model id and run id
    And a row that is not passed reads "not run (<reason>)" and the "Stage 7.2 blockers" section names what clears it

  # The gate side of M2-DEC-5 on a copy of the committed index and record.
  Scenario: A complete TOOLCHAIN-1 row clears its blocker
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "323f4bd" with artifacts "dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then it exits 1 for the other open rows
    And it names no blocker of "DH-E-TOOLCHAIN-1"

  Scenario Outline: ready refuses a passed TOOLCHAIN-1 row without its <what>
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "<commit>" with artifacts "<artifacts>"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then it names the blocker "DH-E-TOOLCHAIN-1 [Linux (E1)]: <blocker>"

    Examples:
      | what      | commit  | artifacts                                       | blocker                           |
      | commit    |         | dsh sha256=9f2c; model id m; run id r           | ran but records no commit         |
      | SHA       | HEAD    | dsh sha256=9f2c; model id m; run id r           | commit "HEAD" is not a commit SHA |
      | artifacts | 323f4bd |                                                 | ran but records no artifacts      |