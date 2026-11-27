@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-scenario-helper-and-keyless-replay @setup-inline
Feature: Scenario helper and keyless replay (DH-S-16)
  A real-environment scenario declares what it needs through the scenario helper:
  an unmet need on a required scenario fails the run instead of skipping it, a
  scenario file may not import harness internals, and a committed recording
  replayed by the built binary without a key reproduces its oracle byte for byte.

  # TC-17a; proof: in-process (the e2e test binary re-executed as the scenario).
  Scenario: Missing input fails, not skips
    Given a scenario "DH-E-S68-NEEDS-BIN" requiring DSH_BIN
    And DSH_BIN is unset
    And "DH-E-S68-NEEDS-BIN" is listed in DSH_REQUIRE_SCENARIOS
    When the scenario runs
    Then it fails with "required scenario skipped"
    And its body never ran
    And its report row is "failed" with reason naming "DSH_BIN"
    And the same scenario not listed in DSH_REQUIRE_SCENARIOS is reported "skipped"

  # TC-17a; proof: service:go-toolchain (analysistest over the committed testdata).
  Scenario: Internal import forbidden
    Given the committed analysistest fixture scenario "scenarioimport" importing "internal/agent"
    When the scenarioimport analyzer runs over it
    Then it reports exactly 1 diagnostic
    And the diagnostic names "scenario file imports internal/agent"

  # TC-16g, TC-16b; proof: golden (the committed test/replay oracle).
  Scenario: Replay oracle
    Given the committed recording "unregistered-tool" under "test/replay"
    And dsh built from this checkout
    When the built binary replays it without a key into a scratch home
    Then the normalized persisted log read back from the SQLite store is byte-identical to "log.expected.jsonl"
    And the workspace tree is byte-identical to "workspace.expected"
    And the model requests are byte-identical to "requests.expected"
