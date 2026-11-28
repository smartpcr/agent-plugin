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
  # M2-DEC-5; proof: golden (the committed record at HEAD). The verdict is ready's
  # blockers plus the stage's identity check of Artifacts: ready itself only
  # requires Artifacts to be non-empty (see the outline below).
  Scenario: Evidence row complete
    Given the committed record "docs/stories/harness-DSH-M2/evidence/DH-S-17.md" still reading "Closure status: open"
    When "harness-gate ready --stage DH-S-17" reads the "DH-E-TOOLCHAIN-1" row of the checkout
    Then the M2-DEC-5 identity check of the committed row finds every one of commit, binary checksum, model id and run id, or the row is not passed
    And the evidence verdict counts the committed row only when it reads passed with commit, binary checksum, model id and run id
    And a row that is not passed claims no commit and no artifacts, reads "not run (<reason>)" and the "Stage 7.2 blockers" section names what clears it

  # The gate side of M2-DEC-5 on a copy of the committed index and record.
  Scenario: A complete TOOLCHAIN-1 row clears its blocker
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "323f4bd" with artifacts "dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then it exits 1 for the other open rows
    And it names no blocker of "DH-E-TOOLCHAIN-1"
    And the M2-DEC-5 identity check of the copied row finds nothing missing
    And the evidence verdict counts the copied row

  Scenario Outline: ready refuses a passed TOOLCHAIN-1 row without its <what>
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "<commit>" with artifacts "<artifacts>"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then it names the blocker "DH-E-TOOLCHAIN-1 [Linux (E1)]: <blocker>"
    And the evidence verdict does not count the copied row

    Examples:
      | what      | commit  | artifacts                                       | blocker                           |
      | commit    |         | dsh sha256=9f2c; model id m; run id r           | ran but records no commit         |
      | SHA       | HEAD    | dsh sha256=9f2c; model id m; run id r           | commit "HEAD" is not a commit SHA |
      | artifacts | 323f4bd |                                                 | ran but records no artifacts      |

  # The identities live inside Artifacts, which ready only requires to be
  # non-empty. Each case runs the real ready on the copy and asserts both halves:
  # the identity check refuses the row, and ready's own silence on it, the gate
  # gap this stage records (when ready learns the check, the last step fails and
  # this outline moves to the one above).
  Scenario Outline: A passed TOOLCHAIN-1 row with <case> is not complete evidence
    Given a copy of the committed index and record whose "DH-E-TOOLCHAIN-1" row reads passed at commit "323f4bd" with artifacts "<artifacts>"
    When "harness-gate ready --stage DH-S-17" runs on the copy
    Then the M2-DEC-5 identity check of the copied row finds exactly its <what> missing
    And the evidence verdict does not count the copied row
    And ready itself names no blocker of "DH-E-TOOLCHAIN-1", the gate gap: it checks only that Artifacts is non-empty

    Examples:
      | case                     | what            | artifacts                                                                                                   |
      | no checksum              | binary checksum | dsh built; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z                                     |
      | a truncated checksum     | binary checksum | dsh sha256=9f2c4e1a; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z                           |
      | an upper-case checksum   | binary checksum | dsh sha256=9F2C4E1A7B3D5C6E8F0A1B2C3D4E5F60718293A4B5C6D7E8F90A1B2C3D4E5F60; model id claude-sonnet-4-5; run id s17-323f4bd-20261201T0000Z |
      | no model id              | model id        | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; run id s17-323f4bd-20261201T0000Z |
      | an empty model id        | model id        | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id ; run id s17-323f4bd-20261201T0000Z |
      | no run id                | run id          | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id claude-sonnet-4-5      |
      | an empty run id          | run id          | dsh sha256=9f2c4e1a7b3d5c6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60; model id claude-sonnet-4-5; run id: |
